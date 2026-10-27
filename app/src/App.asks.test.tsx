import { act, fireEvent, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { DEFAULT_PROFILE_ID, daemonSeed, daemonSession, seedDocument, soloDesktop, type DaemonSeed, type DaemonSession } from './test/daemonFixtures';
import { seedHeading } from './test/garden';
import type { EventMessage } from './test/protocol';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type Ask = EventMessage<'asks_updated'>['asks'][number];

const cacheStore: Ask = {
  id: 'a-cache1',
  session_id: 'api',
  subject: 'Cache store',
  base_dir: '/tmp/api',
  created_at: '2026-01-01T00:00:00Z',
  parts: [
    { markdown: 'Deploys empty the cache.' },
    {
      question: {
        key: 'q1',
        kind: 'one',
        prompt: 'Which store should keep the cache?',
        choices: [{ label: 'Postgres', description: 'already deployed' }, { label: 'Redis' }],
        recommended: 'Postgres',
      },
    },
    { question: { key: 'q2', kind: 'text', prompt: 'What should the banner say?', choices: [] } },
  ],
};

const releaseDay: Ask = {
  id: 'a-release',
  session_id: 'web',
  subject: 'Release day',
  base_dir: '/tmp/web',
  created_at: '2026-01-01T00:05:00Z',
  parts: [{ question: { key: 'q1', kind: 'any', prompt: 'Which platforms?', choices: [{ label: 'macOS' }, { label: 'Linux' }] } }],
};

const cacheStoreReplied: Ask = {
  ...cacheStore,
  reply: {
    answers: [{ key: 'q1', picked: ['Postgres'] }, { key: 'q2', picked: [], text: 'Welcome back' }],
    note: 'Keep it boring.',
    answered_at: '2026-01-01T00:30:00Z',
  },
};

const migration = daemonSeed('s-cache1', { title: 'Cache store migration', claimed: true, tender: { ref: 'session:successor', name: 'successor', session_id: 'successor' } });

const landingWeek: Ask = {
  id: 'a-landing',
  session_id: 'delegate',
  seed_id: migration.id,
  seed_title: migration.title,
  seed_slug: 'cache-migration',
  subject: 'Landing week',
  base_dir: '/tmp/delegate',
  created_at: '2026-01-01T00:10:00Z',
  parts: [{ question: { key: 'q1', kind: 'text', prompt: 'Which week should the migration land?', choices: [] } }],
};

const apiWorking = daemonSession('api', { state: 'working' });
const webWaiting = daemonSession('web', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-01-01T00:06:00Z' });

async function renderAsks(asks: Ask[] = [cacheStore, releaseDay], sessions: DaemonSession[] = [apiWorking, webWaiting], seeds: DaemonSeed[] = [], settings: Record<string, string> = {}) {
  const view = await renderApp({
    initialState: {
      sessions,
      desktops: sessions.map((session) => soloDesktop(session.id)),
      asks,
      seeds,
      settings,
    },
  });
  view.daemon.on('ask_answer', (command) => ({
    event: 'ask_answer_result',
    request_id: command.request_id,
    ask_id: command.ask_id,
    success: true,
  }));
  return view.daemon;
}

async function openDrawer(daemon: ScriptedDaemon) {
  await gesture(daemon, () => pressShortcut('dock.asks'));
  return screen.getByRole('list', { name: 'Open asks' });
}

const openAsk = () => document.querySelector('[data-testid^="ask-a-"]')?.getAttribute('aria-label');
const rows = (list: HTMLElement) => within(list).getAllByRole('button').map((row) => row.getAttribute('data-testid'));
const terminalInput = () => document.querySelector<HTMLElement>('[data-session-visible="1"] [role="textbox"][aria-label="Terminal input"]');

describe('App asks', () => {
  it('opens on the ask of the agent waiting on you first', async () => {
    const daemon = await renderAsks();
    const list = await openDrawer(daemon);

    expect(rows(list)).toEqual(['asks-row-a-release', 'asks-row-a-cache1']);
    expect(openAsk()).toBe('Release day');
    expect(document.activeElement).toBe(screen.getByRole('checkbox', { name: 'macOS' }));

    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'j' }));
    expect(openAsk()).toBe('Cache store');
    expect(document.activeElement).toBe(screen.getByRole('radio', { name: /Postgres/ }));
  });

  it('keeps the shown ask and focus when another agent starts waiting', async () => {
    const daemon = await renderAsks([cacheStore, releaseDay], [apiWorking, daemonSession('web', { state: 'working' })]);
    const list = await openDrawer(daemon);
    expect(openAsk()).toBe('Cache store');
    const postgres = screen.getByRole('radio', { name: /Postgres/ });
    expect(document.activeElement).toBe(postgres);

    daemon.emit({ event: 'session_state_changed', session: webWaiting });
    await daemon.idle();

    expect(rows(list)).toEqual(['asks-row-a-release', 'asks-row-a-cache1']);
    expect(openAsk()).toBe('Cache store');
    expect(document.activeElement).toBe(postgres);
  });

  it('keeps its keys after a click on the ask itself', async () => {
    const daemon = await renderAsks();
    await openDrawer(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('asks-row-a-cache1')));
    const clicking = userEvent.setup({ delay: null }).click(screen.getByText('Deploys empty the cache.'));
    await vi.advanceTimersByTimeAsync(5);
    await clicking;
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'k' }));

    expect(openAsk()).toBe('Release day');
  });

  it('gives focus back to the agent when it closes', async () => {
    const daemon = await renderAsks();
    await openSession(daemon, 'api');
    await openDrawer(daemon);
    expect(document.activeElement).toBe(screen.getByRole('checkbox', { name: 'macOS' }));

    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'Escape' }));
    await act(() => vi.advanceTimersToNextFrame());
    await daemon.idle();

    expect(terminalInput()).not.toBeNull();
    expect(document.activeElement).toBe(terminalInput());
  });

  it('sends the picked answers and the note, then brings the next ask', async () => {
    const daemon = await renderAsks();
    await openDrawer(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('asks-row-a-cache1')));

    fireEvent.click(screen.getByRole('radio', { name: /Postgres/ }));
    fireEvent.change(screen.getByRole('textbox', { name: 'What should the banner say?' }), { target: { value: 'Welcome back' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Note' }), { target: { value: 'Keep it boring.' } });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /^Send/ })));
    daemon.emit({ event: 'asks_updated', profile_id: DEFAULT_PROFILE_ID, asks: [releaseDay, cacheStoreReplied] });
    await daemon.idle();

    expect(daemon.sentOf('ask_answer').map(({ ask_id, answers, note }) => ({ ask_id, answers, note }))).toEqual([{
      ask_id: 'a-cache1',
      answers: [{ key: 'q1', picked: ['Postgres'] }, { key: 'q2', picked: [], text: 'Welcome back' }],
      note: 'Keep it boring.',
    }]);
    expect(openAsk()).toBe('Release day');
  });

  it('keeps the draft and shows why when the daemon refuses the answer', async () => {
    const daemon = await renderAsks([releaseDay]);
    daemon.on('ask_answer', (command) => ({
      event: 'ask_answer_result', request_id: command.request_id, ask_id: command.ask_id, success: false,
      error: 'this ask was already answered or withdrawn',
    }));
    await openDrawer(daemon);

    fireEvent.click(screen.getByRole('checkbox', { name: 'Linux' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Other answer to Which platforms?' }), { target: { value: 'FreeBSD' } });
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('textbox', { name: 'Note' }), { key: 'Enter', metaKey: true }));

    expect(screen.getByRole('alert')).toHaveTextContent('this ask was already answered or withdrawn');
    expect(screen.getByRole('checkbox', { name: 'Linux' })).toBeChecked();
    expect(daemon.sentOf('ask_answer').map(({ answers }) => answers)).toEqual([[{ key: 'q1', picked: ['Linux'], other: 'FreeBSD' }]]);
  });

  it('sends nothing until a question is answered or a note written', async () => {
    const daemon = await renderAsks([releaseDay]);
    await openDrawer(daemon);

    expect(screen.getByRole('button', { name: /^Send/ })).toBeDisabled();
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('textbox', { name: 'Note' }), { key: 'Enter', metaKey: true }));
    expect(daemon.sentOf('ask_answer')).toEqual([]);
  });

  it('marks the agents that asked and jumps to the asking agent', async () => {
    const daemon = await renderAsks();

    expect(screen.getByTestId('ask-marker-api')).toBeInTheDocument();
    expect(screen.getByTestId('ask-marker-web')).toBeInTheDocument();
    await openDrawer(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open agent' })));

    expect(daemon.sentOf('desktop_show_session').map(({ session_id }) => session_id).slice(-1)).toEqual(['web']);
    expect(screen.queryByRole('list', { name: 'Open asks' })).toBeNull();
  });

  it('holds an ask about a seed on the agent whose stake it is and names the seed', async () => {
    const daemon = await renderAsks([cacheStore, landingWeek], [apiWorking, daemonSession('successor', { seed_id: migration.id })]);

    expect(screen.getByTestId('ask-marker-successor')).toBeInTheDocument();
    await openDrawer(daemon);
    expect(openAsk()).toBe('Cache store');
    expect(screen.queryByRole('button', { name: 'Open in Garden' })).toBeNull();

    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'j' }));
    const ask = screen.getByTestId('ask-a-landing');
    expect(within(ask).getByText('successor')).toBeInTheDocument();
    expect(within(ask).getByText('cache-migration')).toBeInTheDocument();
    expect(within(ask).getByText('Cache store migration')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(within(ask).getByRole('button', { name: 'Open agent' })));

    expect(daemon.sentOf('desktop_show_session').map(({ session_id }) => session_id).slice(-1)).toEqual(['successor']);
  });

  it('holds an ask about a seed on whoever the garden says tends it', async () => {
    const daemon = await renderAsks([landingWeek], [daemonSession('delegate', { seed_id: migration.id }), daemonSession('successor')], [migration]);

    expect(screen.getByTestId('ask-marker-successor')).toBeInTheDocument();
    expect(screen.queryByTestId('ask-marker-delegate')).toBeNull();
    await openDrawer(daemon);
    expect(within(screen.getByTestId('ask-a-landing')).getByText('successor')).toBeInTheDocument();
  });

  it('waits for the next tender when no live agent tends the seed', async () => {
    const daemon = await renderAsks([landingWeek], [apiWorking], [{ ...migration, tender: { ref: 'session:delegate', name: 'delegate', session_id: 'delegate' } }]);

    expect(screen.queryByTestId('ask-marker-api')).toBeNull();
    await openDrawer(daemon);
    const ask = screen.getByTestId('ask-a-landing');
    expect(within(ask).getByText('waiting for the next tender of Cache store migration')).toBeInTheDocument();
    expect(within(ask).queryByRole('button', { name: 'Open agent' })).toBeNull();
    expect(within(ask).getByRole('textbox', { name: 'Note' })).toHaveAttribute('placeholder', 'Anything else for its next tender?');
  });

  it('opens the seed in the Garden beside the drawer', async () => {
    const daemon = await renderAsks([landingWeek], [daemonSession('successor')], [migration]);
    daemon.on('seed_document_get', () => ({ event: 'seed_document_get_result', success: true, document: seedDocument(migration) }));
    await openDrawer(daemon);

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open in Garden' })));
    expect(seedHeading()).toBe('Cache store migration');
    expect(daemon.sentOf('seed_document_get').map(({ seed_id }) => seed_id)).toContain(migration.id);
    expect(openAsk()).toBe('Landing week');

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    await act(() => vi.advanceTimersToNextFrame());
    expect(screen.queryByRole('region', { name: 'The garden' })).toBeNull();
    expect(openAsk()).toBe('Landing week');
    expect(screen.getByRole('region', { name: 'Asks' })).toHaveFocus();

    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'g' }));
    expect(seedHeading()).toBe('Cache store migration');
  });

  it('shows open asks on the collapsed queue bar and opens the one picked', async () => {
    const daemon = await renderAsks(undefined, undefined, [], { queue_mode_enabled: 'true' });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Collapse sidebar' })));

    expect(screen.getByTestId('queue-bar-asks')).toHaveTextContent('?2 asks· 1 waiting on you');
    await gesture(daemon, () => fireEvent.pointerEnter(screen.getByTestId('queue-bar-asks-anchor')));
    const peek = screen.getByTestId('queue-bar-asks-peek');
    expect(within(peek).getAllByRole('button').map((row) => row.getAttribute('data-testid'))).toEqual(['queue-bar-peek-ask-a-release', 'queue-bar-peek-ask-a-cache1']);
    await gesture(daemon, () => fireEvent.click(within(peek).getByTestId('queue-bar-peek-ask-a-cache1')));
    expect(openAsk()).toBe('Cache store');

    daemon.emit({ event: 'asks_updated', profile_id: DEFAULT_PROFILE_ID, asks: [] });
    await daemon.idle();
    expect(screen.queryByTestId('queue-bar-asks')).toBeNull();
  });

  it('counts open asks on the collapsed rail and marks the desktops that hold them', async () => {
    const daemon = await renderAsks([cacheStore], [apiWorking, daemonSession('idle')]);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Collapse sidebar' })));

    const asks = screen.getByRole('button', { name: /^1 open ask/ });
    expect(asks).toHaveTextContent('1');
    const marked = Array.from(document.querySelectorAll('.rail-desktops .session-icon'))
      .filter((desktop) => desktop.querySelector('.mini-ask-badge'))
      .map((desktop) => desktop.getAttribute('data-desktop-id'));
    expect(marked).toEqual(['desktop-api']);
    await gesture(daemon, () => fireEvent.click(asks));
    expect(openAsk()).toBe('Cache store');
  });

  it('puts the ask in its agent\'s tile header and opens that ask', async () => {
    const daemon = await renderAsks();
    await openSession(daemon, 'api');

    const chip = screen.getByTestId('ask-chip-api');
    expect(chip).toHaveAccessibleName('Open ask: Cache store');
    fireEvent.pointerEnter(chip);
    await act(() => vi.advanceTimersByTimeAsync(160));
    const preview = screen.getByRole('dialog', { name: 'Open asks from this agent' });
    expect(within(preview).getByText('Which store should keep the cache?')).toBeInTheDocument();
    expect(within(preview).getByText('Written answer')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(within(preview).getByRole('button', { name: 'Answer' })));

    expect(openAsk()).toBe('Cache store');
    expect(screen.queryByRole('dialog', { name: 'Open asks from this agent' })).toBeNull();
  });

  it('leaves the preview closed when the agent asks again after its last ask closed under the pointer', async () => {
    const daemon = await renderAsks([cacheStore], [apiWorking]);
    await openSession(daemon, 'api');
    fireEvent.pointerEnter(screen.getByTestId('ask-chip-api'));
    await act(() => vi.advanceTimersByTimeAsync(160));
    expect(screen.getByRole('dialog', { name: 'Open asks from this agent' })).toBeInTheDocument();

    daemon.emit({ event: 'asks_updated', profile_id: DEFAULT_PROFILE_ID, asks: [] });
    await daemon.idle();
    daemon.emit({ event: 'asks_updated', profile_id: DEFAULT_PROFILE_ID, asks: [{ ...cacheStore, id: 'a-follow-up', subject: 'Cache size' }] });
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(1_000));

    expect(screen.getByTestId('ask-chip-api')).toHaveAccessibleName('Open ask: Cache size');
    expect(screen.queryByRole('dialog', { name: 'Open asks from this agent' })).toBeNull();
  });

  it('takes the keyboard from the terminal when an opener shows the ask the drawer already shows', async () => {
    const daemon = await renderAsks([cacheStore], [apiWorking]);
    await openSession(daemon, 'api');
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('ask-chip-api')));
    const postgres = screen.getByRole('radio', { name: /Postgres/ });
    expect(document.activeElement).toBe(postgres);

    terminalInput()?.focus();
    expect(document.activeElement).toBe(terminalInput());
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('ask-chip-api')));

    expect(openAsk()).toBe('Cache store');
    expect(document.activeElement).toBe(postgres);
  });

  it('lists asks on the home screen and opens the one picked', async () => {
    const daemon = await renderAsks();

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('dashboard-ask-a-cache1')));

    expect(openAsk()).toBe('Cache store');
  });

  it('keeps the asks you replied to out of the way until you ask for them', async () => {
    const daemon = await renderAsks([cacheStoreReplied, releaseDay]);
    expect(screen.queryByTestId('ask-marker-api')).toBeNull();
    const list = await openDrawer(daemon);
    expect(rows(list)).toEqual(['asks-row-a-release']);
    const toggle = screen.getByRole('button', { name: /^Replied/ });
    expect(toggle).toHaveAttribute('aria-pressed', 'false');

    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'r' }));
    expect(toggle).toHaveAttribute('aria-pressed', 'true');
    expect(rows(screen.getByRole('list', { name: 'Open and replied asks' }))).toEqual(['asks-row-a-release', 'asks-row-a-cache1']);
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'j' }));

    expect(openAsk()).toBe('Cache store');
    expect(screen.getByRole('radio', { name: /Postgres/ })).toBeChecked();
    expect(screen.getByRole('radio', { name: /Postgres/ })).toBeDisabled();
    expect(screen.getByLabelText('What should the banner say?')).toHaveTextContent('Welcome back');
    expect(screen.getByLabelText('Note')).toHaveTextContent('Keep it boring.');
    expect(screen.queryByRole('button', { name: /^Send/ })).toBeNull();
    expect(screen.getByText(/^Replied .* ago$|^Replied just now$/)).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'k' }));
    expect(openAsk()).toBe('Release day');
    await gesture(daemon, () => fireEvent.click(toggle));
    expect(rows(screen.getByRole('list', { name: 'Open asks' }))).toEqual(['asks-row-a-release']);
  });

  it('shows the answer you just sent when it was the last open ask and replied asks are shown', async () => {
    const daemon = await renderAsks([releaseDay]);
    await openDrawer(daemon);
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'r' }));

    fireEvent.click(screen.getByRole('checkbox', { name: 'Linux' }));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /^Send/ })));
    daemon.emit({
      event: 'asks_updated',
      profile_id: DEFAULT_PROFILE_ID,
      asks: [{ ...releaseDay, reply: { answers: [{ key: 'q1', picked: ['Linux'] }], answered_at: '2026-01-01T00:40:00Z' } }],
    });
    await daemon.idle();

    expect(openAsk()).toBe('Release day');
    expect(screen.getByRole('checkbox', { name: 'Linux' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Linux' })).toBeDisabled();
    expect(document.activeElement).toBe(screen.getByRole('region', { name: 'Asks' }));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'r' }));
    expect(screen.getByText('No open asks.')).toBeInTheDocument();
    expect(screen.getByText(/shows the one you replied to in the last day/)).toBeInTheDocument();
  });

  it('takes the keyboard when it opens with no open asks, so r shows the replies instead of reaching the agent', async () => {
    const daemon = await renderAsks([cacheStoreReplied], [apiWorking]);
    await openSession(daemon, 'api');
    terminalInput()?.focus();
    expect(document.activeElement).toBe(terminalInput());

    await gesture(daemon, () => pressShortcut('dock.asks'));
    expect(screen.getByText('No open asks.')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'r' }));

    expect(openAsk()).toBe('Cache store');
    expect(daemon.sentOf('pty_input')).toEqual([]);
  });

  it('follows the selected profile and forgets asks that close', async () => {
    const daemon = await renderAsks();
    daemon.emit({ event: 'asks_updated', profile_id: 'another-profile', asks: [] });
    await daemon.idle();
    expect(screen.getByTestId('ask-marker-api')).toBeInTheDocument();

    daemon.emit({ event: 'asks_updated', profile_id: DEFAULT_PROFILE_ID, asks: [] });
    await daemon.idle();
    expect(screen.queryByTestId('ask-marker-api')).toBeNull();
    expect(screen.queryByTestId('session-group-asks')).toBeNull();
  });
});
