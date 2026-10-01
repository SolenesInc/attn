import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, daemonSession, splitWorkspace, type DaemonSession, type DaemonWorkspace } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const HOUR = 60 * 60 * 1000;
const QUEUE = { queue_mode_enabled: 'true' };

const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const fromNow = (ms: number) => new Date(Date.now() + ms).toISOString();

function agent(id: string, overrides: Partial<DaemonSession> = {}): DaemonSession {
  return daemonSession(id, { state: 'idle', ...overrides });
}

function team(): DaemonSession[] {
  return [
    agent('chief', { chief_of_staff: true }),
    agent('newer', { state: 'waiting_input', turn_owed: true, turn_opened_at: ago(HOUR) }),
    agent('older', { state: 'working', turn_owed: true, turn_opened_at: ago(3 * HOUR) }),
    agent('settled', { state: 'waiting_input' }),
  ];
}

interface Launch {
  sessions?: DaemonSession[];
  queue?: boolean;
  workspace?: (session: DaemonSession) => Partial<DaemonWorkspace>;
}

function launch({ sessions = team(), queue = true, workspace = () => ({}) }: Launch = {}) {
  return renderApp({
    initialState: {
      settings: queue ? QUEUE : {},
      sessions,
      workspaces: sessions.map((session) => ({ ...agentWorkspace(session.id), ...workspace(session) })),
    },
  });
}

const queue = () => within(screen.getByTestId('sidebar-queue'));

function bandRows(): string[] {
  return Array.from(screen.getByTestId('sidebar-queue').querySelectorAll('.queue-row'))
    .map((row) => row.getAttribute('data-testid')!);
}

function treeRows(): string[] {
  return Array.from(document.querySelectorAll('[data-testid^="sidebar-session-"]'))
    .map((row) => row.getAttribute('data-testid')!.replace('sidebar-session-', ''));
}

function shownWorkspaces() {
  return Array.from(document.querySelectorAll('.session-terminal-workspace[data-session-visible="1"]'))
    .map((workspace) => workspace.getAttribute('data-workspace-id'));
}

async function press(daemon: ScriptedDaemon, name: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name })));
}

describe('App queue', () => {
  it('replaces the workspace tree with the chief, the owed turns oldest first, then the settled rest', async () => {
    await launch();

    expect(bandRows()).toEqual(['queue-chief-chief', 'queue-turn-older', 'queue-turn-newer', 'queue-settled-settled']);
    expect(treeRows()).toEqual([]);
  });

  it('leaves the workspace tree alone while the queue is off', async () => {
    await launch({ queue: false });

    expect(screen.queryByTestId('sidebar-queue')).toBeNull();
    expect(treeRows().sort()).toEqual(['chief', 'newer', 'older', 'settled']);
  });

  it('keeps a pinned workspace as a group in the tree, its agents out of the bands', async () => {
    await launch({ workspace: (session) => ({ pinned: session.id === 'older' || session.id === 'settled' }) });

    expect(bandRows()).toEqual(['queue-chief-chief', 'queue-turn-newer']);
    expect(treeRows().sort()).toEqual(['older', 'settled']);
  });

  it('shows what an owed agent is doing and how long its turn has waited', async () => {
    const { daemon } = await launch();
    const older = () => screen.getByTestId('queue-turn-older');

    expect(older()).toHaveAttribute('data-state', 'working');
    expect(within(older()).getByText('3h')).toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(HOUR));
    await daemon.idle();

    expect(within(older()).getByText('4h')).toBeInTheDocument();
  });

  it('says so when nothing is owed', async () => {
    await launch({ sessions: [agent('chief', { chief_of_staff: true }), agent('settled', { state: 'waiting_input' })] });

    expect(queue().getByText('Nothing owed.')).toBeInTheDocument();
  });

  it('opens an agent from its row', async () => {
    const { daemon } = await launch();

    await press(daemon, 'Open older');

    expect(daemon.sentOf('session_selected')).toEqual([{ cmd: 'session_selected', id: 'older' }]);
    expect(shownWorkspaces()).toEqual(['workspace-older']);
  });

  it('queues one shared owner with views in two workspaces and retains both workspace rows', async () => {
    const owner = agent('shared', { agent: 'codex', turn_owed: true, turn_opened_at: ago(HOUR) });
    const home = agentWorkspace(owner.id);
    home.layout!.panes[0] = { ...home.layout!.panes[0], codex_resolution: 'resolved', codex_revision: '1' };
    const satellite = agent('satellite', { agent: 'shell', parent_session_id: owner.id, workspace_id: 'workspace-other' });
    const other = splitWorkspace('workspace-other', ['other', satellite.id], { rank: 'a' });
    other.layout!.panes[0] = {
      ...other.layout!.panes[0], session_id: owner.id, codex_resolution: 'resolved', codex_revision: '1',
    };
    const { daemon } = await renderApp({ initialState: { settings: QUEUE, sessions: [owner, satellite], workspaces: [other, home] } });

    expect(bandRows()).toEqual(['queue-turn-shared']);
    expect(within(screen.getByTestId('queue-turn-shared')).getByText('shared')).toBeInTheDocument();
    await press(daemon, 'Open shared');
    expect(shownWorkspaces()).toEqual([home.id]);
    expect(daemon.sentOf('session_selected')).toEqual([{ cmd: 'session_selected', id: owner.id }]);

    await daemon.emit({ event: 'settings_updated', settings: { queue_mode_enabled: 'false' } });
    expect(treeRows()).toEqual(['shared', 'satellite', 'shared']);
  });

  it('queues a moved shared view in its destination and follows its next turn there', async () => {
    const owner = agent('moved', { agent: 'codex' });
    const source = { ...agentWorkspace(owner.id), layout: undefined };
    const destination = agentWorkspace('destination');
    destination.layout!.panes[0] = {
      ...destination.layout!.panes[0], session_id: owner.id, codex_resolution: 'resolved', codex_revision: '1',
    };
    const { daemon } = await renderApp({ initialState: { settings: QUEUE, sessions: [owner], workspaces: [source, destination] } });

    expect(bandRows()).toEqual(['queue-settled-moved']);
    expect(screen.getByTestId('queue-settled-moved')).toHaveAttribute('data-workspace-id', destination.id);
    await press(daemon, 'Open moved');
    expect(shownWorkspaces()).toEqual([destination.id]);
    await gesture(daemon, () => pressShortcut('session.goToDashboard'));
    await gesture(daemon, () => fireEvent.click(within(screen.getByTestId('follow-next-turn')).getByRole('checkbox')));
    await daemon.emit({ event: 'session_state_changed', session: { ...owner, turn_owed: true, turn_opened_at: ago(HOUR) } });
    expect(shownWorkspaces()).toEqual([destination.id]);

    await daemon.emit({ event: 'settings_updated', settings: { queue_mode_enabled: 'false' } });
    expect(treeRows()).toEqual([owner.id]);
  });

  it('keeps the session menu reachable from every band', async () => {
    await launch();

    for (const id of ['chief', 'older', 'settled']) {
      expect(queue().getByRole('button', { name: `Actions for ${id}` })).toBeInTheDocument();
    }
  });

  it('settles an owed turn without opening it, and offers nothing to settle on a settled row or the chief', async () => {
    const { daemon } = await launch();

    await press(daemon, 'Settle older');

    expect(daemon.sentOf('settle_turn')).toEqual([{ cmd: 'settle_turn', session_id: 'older' }]);
    expect(daemon.sentOf('session_selected')).toEqual([]);
    expect(queue().queryByRole('button', { name: 'Settle settled' })).toBeNull();
    expect(queue().queryByRole('button', { name: 'Settle chief' })).toBeNull();
  });

  it('pins an agent from either band, and unpins it from a pinned band below settled, without opening either', async () => {
    const { daemon } = await launch({ sessions: [...team(), agent('held', { state: 'working', pinned_at: ago(HOUR) })] });

    await press(daemon, 'Pin older');
    await press(daemon, 'Pin settled');
    await press(daemon, 'Unpin held');

    expect(daemon.sentOf('pin_session')).toEqual([
      { cmd: 'pin_session', session_id: 'older', pinned: true },
      { cmd: 'pin_session', session_id: 'settled', pinned: true },
      { cmd: 'pin_session', session_id: 'held', pinned: false },
    ]);
    expect(daemon.sentOf('session_selected')).toEqual([]);
    expect(bandRows().slice(-2)).toEqual(['queue-settled-settled', 'queue-pinned-held']);
    expect(queue().queryByRole('button', { name: 'Settle held' })).toBeNull();
    expect(queue().queryByRole('button', { name: 'Snooze held' })).toBeNull();
  });

  it.each([['1h', 1], ['2h', 2], ['4h', 4]] as const)('snoozes an owed or settled agent for %s, and never the chief', async (choice, hours) => {
    const { daemon } = await launch();
    expect(queue().queryByRole('button', { name: 'Snooze chief' })).toBeNull();

    await press(daemon, 'Snooze older');
    const until = new Date(Date.now() + hours * HOUR).toISOString();
    await gesture(daemon, () => fireEvent.click(within(screen.getByRole('menu', { name: 'Snooze older' })).getByTestId(`snooze-choice-${choice}`)));
    await press(daemon, 'Snooze settled');
    expect(screen.getByRole('menu', { name: 'Snooze settled' })).toBeInTheDocument();

    expect(daemon.sentOf('snooze_turn')).toEqual([{ cmd: 'snooze_turn', session_id: 'older', until }]);
    expect(daemon.sentOf('session_selected')).toEqual([]);
  });

  it('collects deferred agents under a collapsed Snoozed section above the muted workspaces, and wakes one without opening it', async () => {
    const { daemon } = await launch({
      sessions: [...team(), agent('later', { turn_snoozed_until: fromNow(HOUR) }), agent('quiet')],
      workspace: (session) => ({ muted: session.id === 'quiet' }),
    });

    expect(bandRows()).not.toContain('queue-settled-later');
    const sections = Array.from(document.querySelectorAll('.muted-sessions-header')).map((header) => header.textContent);
    expect(sections).toEqual(['▸Snoozed (1)', '▸Muted Workspaces (1)']);
    expect(screen.queryByTestId('queue-snoozed-later')).toBeNull();

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('snoozed-section-header')));
    const later = within(screen.getByTestId('queue-snoozed-later'));
    expect(later.queryByRole('button', { name: 'Settle later' })).toBeNull();
    expect(screen.getByTestId('queue-snoozed-later').querySelector('.queue-row-wake-at')?.textContent).not.toBe('');

    await gesture(daemon, () => fireEvent.click(later.getByRole('button', { name: 'Wake later' })));

    expect(daemon.sentOf('wake_turn')).toEqual([{ cmd: 'wake_turn', session_id: 'later' }]);
    expect(daemon.sentOf('session_selected')).toEqual([]);
  });

  it('draws no Snoozed section while nothing is deferred', async () => {
    await launch();

    expect(screen.queryByTestId('sidebar-snoozed')).toBeNull();
  });

  it.each([
    [false, 'settled'],
    [true, 'owed'],
  ])('badges the collapsed rail by what the queue owes (queue on: %s)', async (queueOn, badged) => {
    const { daemon } = await launch({
      queue: queueOn,
      sessions: [
        agent('settled', { state: 'waiting_input' }),
        agent('owed', { state: 'working', turn_owed: true, turn_opened_at: ago(HOUR) }),
      ],
    });

    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));

    const badges = Array.from(document.querySelectorAll('.session-icon'))
      .filter((icon) => icon.querySelector('.mini-badge'))
      .map((icon) => icon.getAttribute('title'));
    expect(badges).toHaveLength(1);
    expect(badges[0]).toMatch(new RegExp(`^${badged}`));
  });
});
