import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { DEFAULT_PROFILE_ID, daemonSession, soloDesktop } from './test/daemonFixtures';
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

async function renderAsks(asks: Ask[] = [cacheStore, releaseDay]) {
  const view = await renderApp({
    initialState: {
      sessions: [
        daemonSession('api', { state: 'working' }),
        daemonSession('web', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-01-01T00:06:00Z' }),
      ],
      desktops: [soloDesktop('api'), soloDesktop('web')],
      asks,
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

describe('App asks', () => {
  it('opens on the ask of the agent waiting on you first', async () => {
    const daemon = await renderAsks();
    const list = await openDrawer(daemon);

    expect(within(list).getAllByRole('button').map((row) => row.getAttribute('data-testid'))).toEqual([
      'asks-row-a-release',
      'asks-row-a-cache1',
    ]);
    expect(openAsk()).toBe('Release day');
    expect(document.activeElement).toBe(screen.getByRole('checkbox', { name: 'macOS' }));

    await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? window, { key: 'j' }));
    expect(openAsk()).toBe('Cache store');
    expect(document.activeElement).toBe(screen.getByRole('radio', { name: /Postgres/ }));
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
