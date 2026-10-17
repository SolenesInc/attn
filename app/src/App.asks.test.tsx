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

const migration = daemonSeed('s-cache1', { title: 'Cache store migration', tender_session: 'successor' });

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

async function renderAsks(asks: Ask[] = [cacheStore, releaseDay], sessions: DaemonSession[] = [apiWorking, webWaiting], seeds: DaemonSeed[] = []) {
  const view = await renderApp({
    initialState: {
      sessions,
      desktops: sessions.map((session) => soloDesktop(session.id)),
      asks,
      seeds,
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
    daemon.emit({ event: 'asks_updated', profile_id: DEFAULT_PROFILE_ID, asks: [releaseDay] });
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
    const daemon = await renderAsks([landingWeek], [apiWorking], [{ ...migration, tender_session: 'delegate' }]);

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

  it('lists asks on the home screen and opens the one picked', async () => {
    const daemon = await renderAsks();

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('dashboard-ask-a-cache1')));

    expect(openAsk()).toBe('Cache store');
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
