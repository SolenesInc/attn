import { act, fireEvent, screen, within } from '@testing-library/react';
import { onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { describe, expect, it, vi } from 'vitest';
import {
  agentPane,
  daemonDesktop,
  daemonSession,
  defaultProfile,
  dockTiles,
  soloDesktop,
  splitDesktop,
  type DaemonDesktop,
  type DaemonSession,
} from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';
import type { CommandMessage } from './test/protocol';
import { useSessionStore } from './store/sessions';

const LADDER = ['profile_select', 'desktop_place_session', 'desktop_set_active_pane', 'desktop_set_current'] as const;
const LATER = '2100-01-01T00:00:00Z';

type Turns = Record<string, Partial<DaemonSession>>;

function queueSession(id: string, hour: number, overrides: Partial<DaemonSession> = {}): DaemonSession {
  return daemonSession(id, { turn_opened_at: `2026-08-03T${String(hour).padStart(2, '0')}:00:00Z`, ...overrides });
}

function queueOf(turns: Turns) {
  return Object.entries(turns).map(([id, overrides], index) => queueSession(id, 9 + index, overrides));
}

async function renderAgents(
  turns: Turns = { s1: {}, s2: {} },
  { settings = { queue_mode_enabled: 'true' } as Record<string, string>, laidOut }: { settings?: Record<string, string>; laidOut?: string[] } = {},
) {
  const sessions = queueOf(turns);
  const view = await renderApp({
    initialState: { sessions, desktops: (laidOut ?? sessions.map((session) => session.id)).map((id) => soloDesktop(id)), settings },
  });
  const update = (next: Turns) => gesture(view.daemon, () => view.daemon.emit({ event: 'sessions_updated', sessions: queueOf(next) }));
  return { ...view, update };
}

function press(key: string, modifiers: { shift?: boolean } = {}) {
  fireEvent.keyDown(window, { key, metaKey: true, shiftKey: modifiers.shift ?? false });
}

const keys = {
  home: () => press('H', { shift: true }),
  back: () => press('['),
  forward: () => press(']'),
  grid: () => pressShortcut('view.toggleGrid'),
  sidebar: () => press('B', { shift: true }),
  settings: () => press(','),
  shortcuts: () => press('/'),
  sessions: () => press('L', { shift: true }),
};

function clickOpen(label: string) {
  if (!screen.queryByRole('button', { name: `Open ${label}` })) {
    fireEvent.click(screen.getByRole('button', { name: /All agents/ }));
  }
  fireEvent.click(screen.getByRole('button', { name: `Open ${label}` }));
}

function open(daemon: ScriptedDaemon, label: string) {
  return gesture(daemon, () => clickOpen(label));
}

function shownLeaf(): string | null {
  return document.querySelector('[data-session-visible="1"]')?.getAttribute('data-active-leaf-id') ?? null;
}

function selectedAgent(): string | null {
  const leaf = shownLeaf();
  return leaf?.startsWith('pane-') ? leaf.slice('pane-'.length) : null;
}

function isHome(): boolean {
  return screen.getByTestId('sidebar-home').getAttribute('aria-current') === 'page';
}

function isGrid(): boolean {
  return screen.queryByRole('region', { name: 'Session grid' }) !== null;
}

function focusedPane(): string | null {
  return document.activeElement?.closest('[data-pane-id]')?.getAttribute('data-pane-id') ?? null;
}

async function settleFocus(daemon: ScriptedDaemon) {
  await act(() => vi.advanceTimersByTimeAsync(1000));
  await daemon.idle();
}

function shows(daemon: ScriptedDaemon): string[] {
  return daemon.sent.flatMap((command) => {
    if (command.cmd === 'desktop_show_session') return [`session:${command.session_id}`];
    if (command.cmd === 'desktop_show_leaf') return [`leaf:${command.desktop_id}/${command.leaf_id}`];
    return [];
  });
}

function ladder(daemon: ScriptedDaemon) {
  return LADDER.flatMap((cmd) => daemon.sentOf(cmd));
}

type ShowCommand = CommandMessage<'desktop_show_session'> | CommandMessage<'desktop_show_leaf'>;

function holdShows(daemon: ScriptedDaemon) {
  const held: ShowCommand[] = [];
  daemon.on('desktop_show_session', (command) => {
    held.push(command);
    return undefined;
  });
  daemon.on('desktop_show_leaf', (command) => {
    held.push(command);
    return undefined;
  });
  const answer = (success: boolean) => gesture(daemon, () => {
    for (const command of held.splice(0)) {
      const target = command.cmd === 'desktop_show_session'
        ? daemon.arrangement.placementOf(command.session_id)
        : { desktopId: command.desktop_id, paneId: command.leaf_id };
      if (success && target) daemon.arrangement.show(target.desktopId, target.paneId);
      daemon.replyTo(command, {
        event: 'profile_action_result',
        action: command.cmd,
        request_id: command.request_id,
        success,
        ...(success ? {} : { error: 'the daemon is shutting down', error_code: 'unavailable' }),
      });
      if (success) daemon.emit(daemon.arrangement.changed());
    }
  });
  return { held, release: () => answer(true), refuse: () => answer(false) };
}

function deepLinkTo(id: string) {
  act(() => vi.mocked(onOpenUrl).mock.lastCall![0]([`attn://spawn?cwd=%2Ftmp%2F${id}`]));
}

function notesDesktop(id = 'notes'): DaemonDesktop {
  return daemonDesktop(id, { root: { type: 'tile', tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/tmp/notes.md' } }, { name: id, shortcut_slot: 9, active_pane_id: 'tile-notes' });
}

function agentBesideNotes(sessionId: string, desktopId = 'd1'): DaemonDesktop {
  return daemonDesktop(desktopId, {
    root: dockTiles({ type: 'pane', pane_id: `pane-${sessionId}` }, [{ tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }]),
    panes: [agentPane(sessionId, desktopId)],
  }, { active_pane_id: `pane-${sessionId}`, shortcut_slot: 1 });
}

function tileEl(tileId = 'tile-notes') {
  return document.querySelector<HTMLElement>(`[data-session-visible="1"] [data-pane-id="${tileId}"]`)!;
}

function sidebarBadges() {
  return Array.from(document.querySelectorAll<HTMLElement>('.sidebar-collapsed .session-icon, .icon-btn.session-icon'))
    .filter((icon) => icon.querySelector('.mini-badge'))
    .map((icon) => icon.title);
}

describe('agent selection', () => {
  it('shows an agent with one request and renders the leaf the daemon made active', async () => {
    const { daemon } = await renderAgents();

    await open(daemon, 's2');

    expect(shows(daemon)).toEqual(['session:s2']);
    expect(ladder(daemon)).toEqual([]);
    expect(selectedAgent()).toBe('s2');
  });

  it('keeps showing the current leaf until the daemon shows the one asked for', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');
    const hold = holdShows(daemon);

    await open(daemon, 's2');
    expect(selectedAgent()).toBe('s1');

    await hold.release();
    expect(selectedAgent()).toBe('s2');
    expect(shows(daemon)).toEqual(['session:s1', 'session:s2']);
  });

  it('asks the daemon to place an agent that has no pane, and shows it', async () => {
    const { daemon } = await renderAgents({ s1: {}, s2: {} }, { laidOut: ['s1'] });

    await open(daemon, 's2');

    expect(shows(daemon)).toEqual(['session:s2']);
    expect(ladder(daemon)).toEqual([]);
    expect(selectedAgent()).toBe('s2');
  });

  it('sends a show for an agent that is already shown and changes nothing', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');

    await open(daemon, 's1');

    expect(shows(daemon)).toEqual(['session:s1', 'session:s1']);
    expect(selectedAgent()).toBe('s1');
  });

  it('shows an agent from an active tile without borrowing an agent for the tile', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      profiles: [defaultProfile('d1')],
      desktops: [agentBesideNotes('s1'), soloDesktop('s2')],
    } });
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => fireEvent.mouseDown(tileEl()));
    expect(shownLeaf()).toBe('tile-notes');
    expect(selectedAgent()).toBeNull();

    await open(daemon, 's2');

    expect(shows(daemon)).toEqual(['leaf:d1/tile-notes', 'session:s2']);
    expect(selectedAgent()).toBe('s2');
  });

  it('reports a refused show and stays on the leaf it had, and a later selection still works', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');
    const hold = holdShows(daemon);

    await open(daemon, 's2');
    await hold.refuse();

    expect(screen.getByText(/Could not show that agent: the daemon is shutting down/)).toBeInTheDocument();
    expect(selectedAgent()).toBe('s1');

    await open(daemon, 's2');
    await hold.release();
    expect(selectedAgent()).toBe('s2');
  });

  it.each([
    ['going home', keys.home, () => expect(isHome()).toBe(true)],
    ['opening the grid', keys.grid, () => expect(isGrid()).toBe(true)],
  ])('stays where the user went after %s while a show was on its way', async (_, leave, stayed) => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');
    const hold = holdShows(daemon);

    await open(daemon, 's2');
    expect(selectedAgent()).toBe('s1');
    await gesture(daemon, leave);
    await hold.release();

    stayed();
    expect(focusedPane()).not.toBe('pane-s2');
  });

  it('keeps Home until the daemon shows the agent picked there, then shows it with the keyboard in it', async () => {
    const { daemon } = await renderAgents();
    const hold = holdShows(daemon);

    await open(daemon, 's2');
    expect(isHome()).toBe(true);
    await hold.release();
    await settleFocus(daemon);

    expect(isHome()).toBe(false);
    expect(selectedAgent()).toBe('s2');
    expect(focusedPane()).toBe('pane-s2');
  });

  it.each([
    ['Home', () => undefined, () => expect(isHome()).toBe(true)],
    ['the grid', keys.grid, () => expect(isGrid()).toBe(true)],
  ])('stays on %s when the daemon refuses a show asked for from there', async (_, arrive, stayed) => {
    const { daemon } = await renderAgents();
    await gesture(daemon, arrive);
    const hold = holdShows(daemon);

    await open(daemon, 's2');
    stayed();
    await hold.refuse();

    stayed();
    expect(screen.getByText(/Could not show that agent/)).toBeInTheDocument();
  });

  it.each([
    ['Home', () => undefined, () => expect(isHome()).toBe(true)],
    ['an agent', undefined, () => expect(selectedAgent()).toBe('s1')],
  ])('closes the grid back to %s, where it was opened from', async (_, arrive, cameFrom) => {
    const { daemon } = await renderAgents();
    if (arrive) await gesture(daemon, arrive);
    else await open(daemon, 's1');

    await gesture(daemon, keys.grid);
    expect(isGrid()).toBe(true);
    await gesture(daemon, keys.grid);

    expect(isGrid()).toBe(false);
    cameFrom();
  });

  it('records the leaf a desktop showed when the user entered it from Home', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [queueSession('s1', 9), queueSession('s2', 10)],
      profiles: [defaultProfile('desktop-s1')],
      desktops: [soloDesktop('s1', { shortcut_slot: 1 }), soloDesktop('s2', { shortcut_slot: 2 })],
    } });
    expect(isHome()).toBe(true);

    await gesture(daemon, () => pressShortcut('desktop.select1'));
    expect(selectedAgent()).toBe('s1');
    await open(daemon, 's2');
    await gesture(daemon, keys.back);

    expect(shows(daemon).pop()).toBe('leaf:desktop-s1/pane-s1');
    expect(selectedAgent()).toBe('s1');
  });

  it('lets a selection made while a launch spawns win over showing the launched agent', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [queueSession('s1', 9), queueSession('s2', 10)],
      desktops: [soloDesktop('s1'), soloDesktop('s2')],
    } });
    await open(daemon, 's1');
    const spawns: Array<Extract<(typeof daemon.sent)[number], { cmd: 'spawn_session' }>> = [];
    daemon.on('spawn_session', (command) => {
      spawns.push(command);
      return undefined;
    });
    pressShortcut('terminal.splitVertical');
    await daemon.idle();
    const [spawn] = spawns.splice(0);

    await act(async () => {
      daemon.arrangement.place(spawn.id, `pane-${spawn.id}`, 'desktop-s1');
      daemon.replyTo(spawn, { event: 'spawn_result', id: spawn.id, success: true, desktop_id: 'desktop-s1', pane_id: `pane-${spawn.id}` });
      clickOpen('s2');
      for (let turn = 0; turn < 20; turn += 1) await Promise.resolve();
    });
    await daemon.idle();

    expect(shows(daemon).filter((show) => show === `session:${spawn.id}`)).toEqual([]);
    expect(selectedAgent()).toBe('s2');
  });

  it('drops a pending show when its agent ends and never places it again', async () => {
    const { daemon } = await renderAgents({ s1: {}, s2: {} }, { laidOut: ['s1'] });
    await open(daemon, 's1');
    const hold = holdShows(daemon);

    await open(daemon, 's2');
    await gesture(daemon, () => daemon.emit({ event: 'session_unregistered', session: queueSession('s2', 10) }));
    await hold.refuse();

    expect(shows(daemon)).toEqual(['session:s1', 'session:s2']);
    expect(daemon.sentOf('desktop_place_session')).toEqual([]);
    expect(selectedAgent()).toBe('s1');
  });

  it('shows an agent a deep link names with one request', async () => {
    const { daemon } = await renderAgents({ s1: {}, s2: {} }, { laidOut: ['s1'] });

    await gesture(daemon, () => deepLinkTo('s2'));

    expect(shows(daemon)).toEqual(['session:s2']);
    expect(selectedAgent()).toBe('s2');
  });

  it.each([
    ['going home', keys.home],
    ['going back', keys.back],
    ['toggling the sidebar', keys.sidebar],
    ['opening settings', keys.settings],
    ['opening the shortcuts', keys.shortcuts],
    ['opening the sessions list', keys.sessions],
  ])('dismisses the delegation chain when %s', async (_, shortcut) => {
    const { daemon } = await renderAgents({ s1: { delegation_role: { name: 'Builder' } }, s2: {} });
    await open(daemon, 's2');
    await open(daemon, 's1');
    fireEvent.click(within(screen.getByTestId('sidebar-queue')).getByTestId('delegation-chain-trigger-s1'));
    expect(screen.getByRole('dialog', { name: 'Delegation chain' })).toBeInTheDocument();

    await gesture(daemon, shortcut);

    expect(screen.queryByRole('dialog', { name: 'Delegation chain' })).toBeNull();
  });

  it('keeps the grid through unrelated updates, and leaves it for the agent the user picks', async () => {
    const { daemon } = await renderAgents();
    await gesture(daemon, keys.grid);
    expect(isGrid()).toBe(true);

    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { queue_mode_enabled: 'true', unrelated: 'x' } }));
    await gesture(daemon, () => daemon.emit({ event: 'sessions_updated', sessions: [queueSession('s1', 9), queueSession('s2', 10, { state: 'idle' })] }));
    expect(isGrid()).toBe(true);

    await open(daemon, 's1');
    expect(isGrid()).toBe(false);
    expect(selectedAgent()).toBe('s1');

    await gesture(daemon, keys.home);
    expect(isHome()).toBe(true);
  });

  it('follows a selection another client made on the same profile', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');

    await gesture(daemon, () => {
      daemon.arrangement.show('desktop-s2', 'pane-s2');
      daemon.emit(daemon.arrangement.changed());
    });

    expect(selectedAgent()).toBe('s2');
    expect(shows(daemon)).toEqual(['session:s1']);
  });
});

describe('queue', () => {
  const OWED = { turn_owed: true };
  const SETTLED = { turn_owed: false };
  const SNOOZED = { turn_owed: false, turn_snoozed_until: LATER };

  async function workTheQueueDownToHome() {
    const view = await renderAgents({ s1: OWED, s2: SETTLED });
    await open(view.daemon, 's1');
    await view.update({ s1: SETTLED, s2: SETTLED });
    expect(isHome()).toBe(true);
    return view;
  }

  it.each<[string, Turns, string, Turns, string | null]>([
    ['moves on to the next owed turn', { s1: OWED, s2: OWED, s3: OWED }, 's1', { s1: SETTLED, s2: OWED, s3: OWED }, 's2'],
    ['moves on from the middle of the queue', { s1: OWED, s2: OWED, s3: OWED }, 's2', { s1: OWED, s2: SETTLED, s3: OWED }, 's3'],
    ['wraps to the top from the bottom row', { s1: OWED, s2: OWED, s3: OWED }, 's3', { s1: OWED, s2: OWED, s3: SETTLED }, 's1'],
    ['skips a successor that settled in the same update', { s1: OWED, s2: OWED, s3: OWED }, 's1', { s1: SETTLED, s2: SETTLED, s3: OWED }, 's3'],
    ['wraps past a successor that settled in the same update', { s1: OWED, s2: OWED, s3: OWED }, 's2', { s1: OWED, s2: SETTLED, s3: SETTLED }, 's1'],
    ['lands on a turn that opened in the update that closed this one', { s1: OWED, s2: SETTLED }, 's1', { s1: SETTLED, s2: OWED }, 's2'],
    ['moves on when the watched turn is snoozed', { s1: OWED, s2: OWED }, 's1', { s1: SNOOZED, s2: OWED }, 's2'],
    ['goes home when the last owed turn closes', { s1: OWED, s2: SETTLED }, 's1', { s1: SETTLED, s2: SETTLED }, null],
    ['goes home when the last owed turn is snoozed', { s1: OWED, s2: SETTLED }, 's1', { s1: SNOOZED, s2: SETTLED }, null],
    ['stays while the watched turn is still owed', { s1: OWED, s2: OWED }, 's1', { s1: { ...OWED, state: 'waiting_input' }, s2: OWED }, 's1'],
    ['stays when the turn that closed was not the watched agent’s', { s1: SETTLED, s2: OWED }, 's1', { s1: SETTLED, s2: SETTLED }, 's1'],
    ['stays on an agent that left the queue for the crew', { s1: OWED, s2: OWED }, 's1', { s1: { turn_owed: false, crew_member: 'fern' }, s2: OWED }, 's1'],
  ])('%s', async (_, turns, watched, update, landing) => {
    const view = await renderAgents(turns);
    await open(view.daemon, watched);

    await view.update(update);

    if (landing) expect(selectedAgent()).toBe(landing);
    else expect(isHome()).toBe(true);
  });

  it('moves on only from the active leaf, not from a tile beside the agent whose turn closed', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [queueSession('s1', 9, OWED), queueSession('s2', 10, OWED)],
      profiles: [defaultProfile('d1')],
      desktops: [agentBesideNotes('s1'), soloDesktop('s2')],
      settings: { queue_mode_enabled: 'true' },
    } });
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => fireEvent.mouseDown(tileEl()));

    await gesture(daemon, () => daemon.emit({ event: 'sessions_updated', sessions: [queueSession('s1', 9, SETTLED), queueSession('s2', 10, OWED)] }));

    expect(shownLeaf()).toBe('tile-notes');
    expect(shows(daemon)).toEqual(['leaf:d1/tile-notes']);
  });

  it('keeps a settled agent the user chose while other turns are owed', async () => {
    const view = await renderAgents({ s1: SETTLED, s2: OWED });
    await open(view.daemon, 's1');

    await view.update({ s1: SETTLED, s2: OWED });

    expect(selectedAgent()).toBe('s1');
  });

  it('takes the user to the next turn that opens after the queue ran dry', async () => {
    const view = await workTheQueueDownToHome();

    await view.update({ s1: SETTLED, s2: OWED });

    expect(selectedAgent()).toBe('s2');
  });

  it('hands over the oldest owed turn when several opened while home waited', async () => {
    const view = await workTheQueueDownToHome();

    await view.update({ s1: OWED, s2: OWED });

    expect(selectedAgent()).toBe('s1');
  });

  it('leaves the user alone at a home they walked to', async () => {
    const view = await renderAgents({ s1: OWED, s2: SETTLED });
    await open(view.daemon, 's1');
    await gesture(view.daemon, keys.home);

    await view.update({ s1: OWED, s2: OWED });

    expect(isHome()).toBe(true);
    expect(shows(view.daemon)).toEqual(['session:s1']);
  });

  it('ends the wait when the user leaves home, however they come back', async () => {
    const view = await workTheQueueDownToHome();
    await gesture(view.daemon, keys.grid);
    await gesture(view.daemon, keys.grid);

    await view.update({ s1: SETTLED, s2: OWED });

    expect(shows(view.daemon)).toEqual(['session:s1']);
  });

  it('takes the user to the next turn after they ask to follow from an all-settled home', async () => {
    const view = await renderAgents({ s1: SETTLED, s2: SETTLED });
    fireEvent.click(within(screen.getByTestId('follow-next-turn')).getByRole('checkbox'));

    await view.update({ s1: SETTLED, s2: OWED });

    expect(selectedAgent()).toBe('s2');
  });

  it.each([
    ['opening a desktop of tiles', (daemon: ScriptedDaemon) => gesture(daemon, () => pressShortcut('desktop.select9'))],
    ['going back through history', (daemon: ScriptedDaemon) => gesture(daemon, keys.back)],
  ])('stops waiting for the next turn after %s', async (_, navigate) => {
    const view = await renderAgents({ s1: OWED, s2: SETTLED });
    view.daemon.arrange((desktops) => [...desktops, notesDesktop()]);
    await open(view.daemon, 's1');
    await view.update({ s1: SETTLED, s2: SETTLED });
    expect(isHome()).toBe(true);

    await navigate(view.daemon);
    const settledOn = shownLeaf();
    await view.update({ s1: SETTLED, s2: OWED });

    expect(shownLeaf()).toBe(settledOn);
  });

  it.each([['⌘J', () => press('j')]])('jumps with %s to the turn owed longest, not the first row', async (_, jump) => {
    const view = await renderAgents({ s1: { ...OWED, turn_opened_at: '2026-08-03T11:00:00Z' }, s2: { ...OWED, turn_opened_at: '2026-08-03T09:00:00Z' } });

    await gesture(view.daemon, jump);

    expect(shows(view.daemon)).toEqual(['session:s2']);
    expect(selectedAgent()).toBe('s2');
  });

  it('stays home on ⌘J when no turn is owed', async () => {
    const view = await renderAgents({ s1: SETTLED, s2: SETTLED });

    await gesture(view.daemon, () => press('j'));

    expect(isHome()).toBe(true);
    expect(shows(view.daemon)).toEqual([]);
  });

  it('leaves history and keyboard focus consistent when it moves on', async () => {
    const view = await renderAgents({ s1: OWED, s2: OWED });
    await open(view.daemon, 's1');

    await view.update({ s1: SETTLED, s2: OWED });
    await settleFocus(view.daemon);
    expect(selectedAgent()).toBe('s2');
    expect(focusedPane()).toBe('pane-s2');

    await gesture(view.daemon, keys.back);
    expect(selectedAgent()).toBe('s1');
  });
});

describe('leaf history', () => {
  it('walks back and forward through agents and tiles alike', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      profiles: [defaultProfile('d1')],
      desktops: [agentBesideNotes('s1'), soloDesktop('s2')],
    } });
    await open(daemon, 's1');
    await gesture(daemon, () => fireEvent.mouseDown(tileEl()));
    await open(daemon, 's2');

    await gesture(daemon, keys.back);
    expect(shownLeaf()).toBe('tile-notes');
    await gesture(daemon, keys.back);
    expect(selectedAgent()).toBe('s1');
    await gesture(daemon, keys.forward);
    expect(shownLeaf()).toBe('tile-notes');

    expect(shows(daemon)).toEqual([
      'session:s1',
      'leaf:d1/tile-notes',
      'session:s2',
      'leaf:d1/tile-notes',
      'leaf:d1/pane-s1',
      'leaf:d1/tile-notes',
    ]);
  });

  it('resumes history from home and grid, then traverses normally in the session view', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');
    await open(daemon, 's2');
    await gesture(daemon, keys.home);

    await gesture(daemon, keys.back);
    expect(selectedAgent()).toBe('s2');

    await gesture(daemon, keys.grid);
    await gesture(daemon, keys.forward);
    expect(isGrid()).toBe(true);

    await gesture(daemon, keys.back);
    expect(selectedAgent()).toBe('s2');
    expect(isGrid()).toBe(false);

    await gesture(daemon, keys.back);
    expect(selectedAgent()).toBe('s1');
    await gesture(daemon, keys.forward);
    expect(selectedAgent()).toBe('s2');
    await gesture(daemon, keys.forward);
    expect(selectedAgent()).toBe('s2');
  });

  it('forgets the leaves ahead once the user opens another after going back', async () => {
    const { daemon } = await renderAgents({ s1: {}, s2: {}, s3: {} }, { settings: {} });
    await open(daemon, 's1');
    await open(daemon, 's2');
    await open(daemon, 's3');

    await gesture(daemon, keys.back);
    await open(daemon, 's1');
    await gesture(daemon, keys.forward);
    expect(selectedAgent()).toBe('s1');

    await gesture(daemon, keys.back);
    expect(selectedAgent()).toBe('s2');
  });

  it('steps over a leaf that is gone when going back', async () => {
    const view = await renderAgents({ s1: {}, s2: {}, s3: {} }, { settings: {} });
    await open(view.daemon, 's1');
    await open(view.daemon, 's2');
    await open(view.daemon, 's3');

    await gesture(view.daemon, () => {
      view.daemon.emit({ event: 'session_unregistered', session: queueSession('s2', 10) });
      view.daemon.arrange((desktops) => desktops.filter((desktop) => desktop.id !== 'desktop-s2'));
    });
    await gesture(view.daemon, keys.back);

    expect(selectedAgent()).toBe('s1');
  });

  it('takes two quick steps back before the first one lands, and a refusal restores the committed cursor', async () => {
    const { daemon } = await renderAgents({ s1: {}, s2: {}, s3: {} }, { settings: {} });
    await open(daemon, 's1');
    await open(daemon, 's2');
    await open(daemon, 's3');
    const hold = holdShows(daemon);

    await gesture(daemon, keys.back);
    await gesture(daemon, keys.back);
    expect(hold.held.map((command) => command.cmd === 'desktop_show_leaf' && command.leaf_id)).toEqual(['pane-s2', 'pane-s1']);
    await hold.release();
    expect(selectedAgent()).toBe('s1');

    await gesture(daemon, keys.forward);
    await hold.refuse();
    expect(selectedAgent()).toBe('s1');
    await gesture(daemon, keys.forward);
    await hold.release();
    expect(selectedAgent()).toBe('s2');
  });

  it('keeps the cursor where it was when the daemon refuses a step back', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');
    await open(daemon, 's2');
    const hold = holdShows(daemon);

    await gesture(daemon, keys.back);
    await hold.refuse();
    expect(selectedAgent()).toBe('s2');

    await gesture(daemon, keys.back);
    await hold.release();
    expect(selectedAgent()).toBe('s1');
  });

  it('follows a leaf the daemon moved and renamed, not the leaf that kept its old id', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      profiles: [defaultProfile('d1')],
      desktops: [agentBesideNotes('s1'), soloDesktop('s2'), notesDesktop('d2')],
    } });
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => fireEvent.mouseDown(tileEl()));
    await open(daemon, 's2');

    await gesture(daemon, () => {
      daemon.arrangement.desktops = daemon.arrangement.desktops.map((desktop) => {
        if (desktop.id === 'd1') return soloDesktop('s1', { id: 'd1', shortcut_slot: 1 });
        if (desktop.id !== 'd2') return desktop;
        return daemonDesktop('d2', {
          root: dockTiles({ type: 'tile', tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }, [
            { tile_id: 'tile-notes-moved', tile_kind: 'markdown', tile_params: '/tmp/notes.md' },
          ]),
        }, { name: 'd2', active_pane_id: 'tile-notes-moved' });
      });
      daemon.emit({
        event: 'profile_arrangement_changed',
        profile: daemon.arrangement.profile,
        desktops: daemon.arrangement.desktops,
        moved_leaf: { from_desktop_id: 'd1', from_leaf_id: 'tile-notes', to_desktop_id: 'd2', to_leaf_id: 'tile-notes-moved' },
      });
    });
    await gesture(daemon, keys.back);

    expect(shows(daemon).pop()).toBe('leaf:d2/tile-notes-moved');
  });

  it('follows a leaf moved to another desktop when its id is still unique', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');
    await open(daemon, 's2');

    await gesture(daemon, () => daemon.arrange((desktops) => desktops.map((desktop) =>
      desktop.id === 'desktop-s1' ? soloDesktop('s1', { id: 'desktop-elsewhere' }) : desktop)));
    await gesture(daemon, keys.back);

    expect(shows(daemon).pop()).toBe('leaf:desktop-elsewhere/pane-s1');
    expect(selectedAgent()).toBe('s1');
  });

  it('keeps a profile’s history while the user is on another profile', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');
    await open(daemon, 's2');
    const home = daemon.arrangement.profile;

    await gesture(daemon, () => daemon.emit({
      event: 'profile_arrangement_changed',
      profile: { id: 'profile-other', name: 'Other', current_desktop_id: 'desktop-other', revision: 1 },
      desktops: [soloDesktop('s9', { id: 'desktop-other', profile_id: 'profile-other' })],
    }));
    await gesture(daemon, () => daemon.emit({ event: 'profile_arrangement_changed', profile: home, desktops: daemon.arrangement.desktops }));
    await gesture(daemon, keys.back);

    expect(shows(daemon).pop()).toBe('leaf:desktop-s1/pane-s1');
  });
});

describe('keyboard focus', () => {
  it('lands in the opened agent’s terminal once, and later updates do not pull it back', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's2');
    await settleFocus(daemon);
    expect(focusedPane()).toBe('pane-s2');

    screen.getByRole('button', { name: 'Open s1' }).focus();
    daemon.emit({ event: 'session_state_changed', session: queueSession('s2', 10, { label: 'renamed' }) });
    await settleFocus(daemon);

    expect(focusedPane()).toBeNull();
  });

  it('lands only in the last of several quick selections', async () => {
    const { daemon } = await renderAgents({ s1: { state: 'idle' }, s2: { state: 'idle' }, s3: { state: 'idle' } }, { settings: {} });
    await open(daemon, 's1');
    await settleFocus(daemon);

    pressShortcut('session.next');
    pressShortcut('session.next');
    await settleFocus(daemon);

    expect(selectedAgent()).toBe('s3');
    expect(focusedPane()).toBe('pane-s3');
  });

  it('stays out of the terminal when Home follows the selection in the same moment', async () => {
    const { daemon } = await renderAgents();

    act(() => {
      clickOpen('s1');
      keys.home();
    });
    await settleFocus(daemon);

    expect(isHome()).toBe(true);
    expect(focusedPane()).toBeNull();
  });

  it('leaves the keyboard on a control the user moved to before the show landed', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');
    await settleFocus(daemon);
    const hold = holdShows(daemon);

    await open(daemon, 's2');
    screen.getByRole('button', { name: 'Open s1' }).focus();
    await hold.release();
    await settleFocus(daemon);

    expect(selectedAgent()).toBe('s2');
    expect(focusedPane()).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Open s1' }));
  });

  it('keeps the keyboard claim until the shown agent’s terminal is ready, then focuses it once', async () => {
    const spawning = (status: 'spawning' | 'ready') => daemonDesktop('desktop-s2', {
      root: { type: 'pane', pane_id: 'pane-s2' },
      panes: [{ ...agentPane('s2', 'desktop-s2'), status }],
    });
    const { daemon } = await renderApp({ initialState: {
      sessions: [queueSession('s1', 9), queueSession('s2', 10, { state: 'launching' })],
      desktops: [soloDesktop('s1'), spawning('spawning')],
    } });

    await open(daemon, 's2');
    await settleFocus(daemon);
    expect(shownLeaf()).toBe('pane-s2');
    expect(focusedPane()).not.toBe('pane-s2');
    expect(useSessionStore.getState().focusRequest).toMatchObject({ leafId: 'pane-s2' });

    await gesture(daemon, () => daemon.arrange((desktops) => desktops.map((desktop) =>
      desktop.id === 'desktop-s2' ? { ...spawning('ready'), active_pane_id: 'pane-s2', revision: desktop.revision + 1 } : desktop)));
    await settleFocus(daemon);

    expect(focusedPane()).toBe('pane-s2');
    expect(useSessionStore.getState().focusRequest).toBeNull();
  });

  it('puts the keyboard in the leaf of a desktop the user opened from its sidebar row', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [queueSession('s1', 9), queueSession('s2', 10)],
      desktops: [soloDesktop('s1', { name: 'alpha' }), soloDesktop('s2', { name: 'beta' })],
    } });
    await open(daemon, 's1');
    await settleFocus(daemon);
    const row = screen.getByRole('button', { name: 'Open beta' });
    row.focus();

    await gesture(daemon, () => fireEvent.click(row));
    await settleFocus(daemon);

    expect(daemon.sentOf('desktop_set_current').slice(-1)).toEqual([expect.objectContaining({ desktop_id: 'desktop-s2' })]);
    expect(focusedPane()).toBe('pane-s2');
  });

  it('puts the keyboard in the leaf the daemon shows when a desktop switch lands, not the one shown when it was asked', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [queueSession('s1', 9), queueSession('s2', 10), queueSession('s3', 11)],
      desktops: [soloDesktop('s1', { name: 'alpha' }), splitDesktop('beta', ['s2', 's3'], { name: 'beta', active_pane_id: 'pane-s2' })],
    } });
    await open(daemon, 's1');
    await settleFocus(daemon);
    daemon.on('desktop_set_current', (command) => {
      daemon.arrangement.show('beta', 'pane-s3');
      return [
        { event: 'profile_action_result', action: command.cmd, request_id: command.request_id ?? '', success: true } as Reply,
        daemon.arrangement.changed(),
      ];
    });
    const row = screen.getByRole('button', { name: 'Open beta' });
    row.focus();

    await gesture(daemon, () => fireEvent.click(row));
    await settleFocus(daemon);

    expect(focusedPane()).toBe('pane-s3');
  });

  it('puts the keyboard in a tile the user reached through history', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      profiles: [defaultProfile('d1')],
      desktops: [agentBesideNotes('s1'), soloDesktop('s2')],
    } });
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => fireEvent.mouseDown(tileEl()));
    await open(daemon, 's2');

    await gesture(daemon, keys.back);
    await settleFocus(daemon);

    expect(focusedPane()).toBe('tile-notes');
  });

  it('moves the keyboard with a remote switch only when it was in the outgoing leaf', async () => {
    const { daemon } = await renderAgents();
    await open(daemon, 's1');
    await settleFocus(daemon);
    expect(focusedPane()).toBe('pane-s1');

    await gesture(daemon, () => {
      daemon.arrangement.show('desktop-s2', 'pane-s2');
      daemon.emit(daemon.arrangement.changed());
    });
    await settleFocus(daemon);
    expect(focusedPane()).toBe('pane-s2');

    screen.getByRole('button', { name: 'Open s1' }).focus();
    await gesture(daemon, () => {
      daemon.arrangement.show('desktop-s1', 'pane-s1');
      daemon.emit(daemon.arrangement.changed());
    });
    await settleFocus(daemon);
    expect(selectedAgent()).toBe('s1');
    expect(focusedPane()).toBeNull();
  });
});

describe('attention', () => {
  it('asks for attention from sessions waiting on the user or in an unknown state, and not from the others', async () => {
    const states = ['waiting_input', 'pending_approval', 'unknown', 'stopped', 'waiting', 'working', 'idle', 'launching', 'scheduled', 'recoverable'] as DaemonSession['state'][];
    const { daemon } = await renderApp({
      initialState: {
        sessions: states.map((state) => daemonSession(state, { state })),
        desktops: states.map((state) => soloDesktop(state, { name: state })),
      },
    });

    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));

    expect(sidebarBadges().map((title) => title.replace(/ \(.*\)$/, '')).sort()).toEqual(['pending_approval', 'stopped', 'unknown', 'waiting', 'waiting_input']);
  });
});
