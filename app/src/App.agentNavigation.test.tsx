import { act, fireEvent, screen, within } from '@testing-library/react';
import { onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, daemonSession, daemonWorkspace, type DaemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const S1_TURN_OPENED = '2026-08-03T09:00:00Z';
const S2_TURN_OPENED = '2026-08-03T10:00:00Z';

function agent(id: 's1' | 's2', overrides: Partial<DaemonSession> = {}): DaemonSession {
  return daemonSession(id, {
    turn_opened_at: id === 's1' ? S1_TURN_OPENED : S2_TURN_OPENED,
    ...overrides,
  });
}

async function renderQueue({
  owed = [] as string[],
  laidOut = ['s1', 's2'],
  s1 = {} as Partial<DaemonSession>,
} = {}) {
  return renderApp({
    initialState: {
      sessions: [
        agent('s1', { turn_owed: owed.includes('s1'), ...s1 }),
        agent('s2', { turn_owed: owed.includes('s2') }),
      ],
      workspaces: laidOut.map(agentWorkspace),
      settings: { queue_mode_enabled: 'true' },
    },
  });
}

function press(key: string, modifiers: { shift?: boolean } = {}) {
  fireEvent.keyDown(window, { key, metaKey: true, shiftKey: modifiers.shift ?? false });
}

const keys = {
  home: () => press('H', { shift: true }),
  back: () => press('['),
  forward: () => press(']'),
  grid: () => press('g'),
  sidebar: () => press('B', { shift: true }),
  settings: () => press(','),
  shortcuts: () => press('/'),
  sessions: () => press('L', { shift: true }),
};

function open(label: string) {
  fireEvent.click(screen.getByRole('button', { name: `Open ${label}` }));
}

function selectedAgent(): string | null {
  return document.querySelector('.session-item.selected .session-label')?.textContent ?? null;
}

function isHome(): boolean {
  return screen.getByTestId('sidebar-home').getAttribute('aria-current') === 'page';
}

function isGrid(): boolean {
  return screen.queryByRole('region', { name: 'Session grid' }) !== null;
}

function setTurn(daemon: ScriptedDaemon, id: 's1' | 's2', owed: boolean) {
  daemon.emit({ event: 'session_state_changed', session: agent(id, { turn_owed: owed }) });
}

function layOut(daemon: ScriptedDaemon, id: string) {
  daemon.emit({ event: 'workspace_state_changed', workspace: agentWorkspace(id) });
}

function deepLinkTo(id: string) {
  act(() => vi.mocked(onOpenUrl).mock.lastCall![0]([`attn://spawn?cwd=%2Ftmp%2F${id}`]));
}

function selectionsOf(daemon: ScriptedDaemon, workspaceId: string) {
  return daemon.sent.filter(
    (command) => command.cmd === 'workspace_selected' && command.workspace_id === workspaceId,
  );
}

async function workTheQueueDownToHome() {
  const rendered = await renderQueue({ owed: ['s1'] });
  open('s1');
  expect(selectedAgent()).toBe('s1');

  setTurn(rendered.daemon, 's1', false);
  expect(isHome()).toBe(true);
  return rendered;
}


const LATER = '2100-01-01T00:00:00Z';

type Turns = Record<string, Partial<DaemonSession>>;

function queueSession(id: string, hour: number, overrides: Partial<DaemonSession> = {}): DaemonSession {
  return daemonSession(id, { turn_opened_at: `2026-08-03T${String(hour).padStart(2, '0')}:00:00Z`, ...overrides });
}

function queueOf(turns: Turns) {
  return Object.entries(turns).map(([id, overrides], index) => queueSession(id, 9 + index, overrides));
}

async function renderAgents(turns: Turns, settings: Record<string, string> = { queue_mode_enabled: 'true' }) {
  const sessions = queueOf(turns);
  const view = await renderApp({ initialState: { sessions, workspaces: sessions.map((session) => agentWorkspace(session.id)), settings } });
  const update = (next: Turns) => view.daemon.emit({ event: 'sessions_updated', sessions: queueOf(next) });
  return { ...view, update };
}

function focusedPane(): string | null {
  return document.activeElement?.closest('[data-pane-id]')?.getAttribute('data-pane-id') ?? null;
}

async function settleFocus(daemon: ScriptedDaemon) {
  await act(() => vi.advanceTimersByTimeAsync(1000));
  await daemon.idle();
}

function sidebarBadges() {
  return Array.from(document.querySelectorAll<HTMLElement>('.sidebar-collapsed .session-icon, .icon-btn.session-icon'))
    .filter((icon) => icon.querySelector('.mini-badge'))
    .map((icon) => icon.title);
}

describe('agent navigation', () => {
  it('selects a deferred session when its pane becomes available', async () => {
    const { daemon } = await renderQueue({ laidOut: ['s1'] });

    deepLinkTo('s2');
    expect(isHome()).toBe(true);

    layOut(daemon, 's2');

    expect(selectedAgent()).toBe('s2');
  });

  it('keeps a newer selection when a deferred one becomes ready', async () => {
    const { daemon } = await renderQueue({ laidOut: ['s1'] });

    deepLinkTo('s2');
    open('s1');
    layOut(daemon, 's2');

    expect(selectedAgent()).toBe('s1');
  });

  it('does not leave home when an older deferred selection becomes ready', async () => {
    const { daemon } = await renderQueue({ laidOut: ['s1'] });

    deepLinkTo('s2');
    keys.home();
    layOut(daemon, 's2');

    expect(isHome()).toBe(true);
    expect(selectionsOf(daemon, 'workspace-s2')).toEqual([]);
  });

  it.each([
    ['going home', keys.home],
    ['going back', keys.back],
    ['toggling the sidebar', keys.sidebar],
    ['opening settings', keys.settings],
    ['opening the shortcuts', keys.shortcuts],
    ['opening the sessions list', keys.sessions],
  ])('dismisses the delegation chain when %s', async (_, shortcut) => {
    await renderQueue({ s1: { delegation_role: { name: 'Builder' } } });
    open('s2');
    open('s1');
    fireEvent.click(within(screen.getByTestId('sidebar-queue')).getByTestId('delegation-chain-trigger-s1'));
    expect(screen.getByRole('dialog', { name: 'Delegation chain' })).toBeInTheDocument();

    shortcut();

    expect(screen.queryByRole('dialog', { name: 'Delegation chain' })).toBeNull();
  });

  it('takes the user to the next turn that opens after the queue ran dry', async () => {
    const { daemon } = await workTheQueueDownToHome();

    setTurn(daemon, 's2', true);

    expect(selectedAgent()).toBe('s2');
  });

  it('leaves the user alone at a home they walked to', async () => {
    const { daemon } = await renderQueue({ owed: ['s1'] });
    open('s1');
    keys.home();
    expect(isHome()).toBe(true);

    setTurn(daemon, 's2', true);

    expect(isHome()).toBe(true);
    expect(selectionsOf(daemon, 'workspace-s2')).toEqual([]);
  });

  it('ends the wait when the user leaves home, however they come back', async () => {
    const { daemon } = await workTheQueueDownToHome();
    keys.grid();
    keys.grid();
    expect(isHome()).toBe(true);

    setTurn(daemon, 's2', true);

    expect(isHome()).toBe(true);
    expect(selectionsOf(daemon, 'workspace-s2')).toEqual([]);
  });

  it('hands over the oldest owed turn when several opened while home waited', async () => {
    const { daemon } = await workTheQueueDownToHome();

    daemon.emit({
      event: 'sessions_updated',
      sessions: [agent('s1', { turn_owed: true }), agent('s2', { turn_owed: true })],
    });

    expect(selectedAgent()).toBe('s1');
  });

  it('resumes history from home and grid, then traverses normally in the session view', async () => {
    await renderQueue();
    open('s1');
    open('s2');
    keys.home();

    keys.back();
    expect(selectedAgent()).toBe('s2');
    expect(isGrid()).toBe(false);

    keys.grid();
    keys.forward();
    expect(selectedAgent()).toBe('s2');
    expect(isGrid()).toBe(true);

    keys.back();
    expect(selectedAgent()).toBe('s2');
    expect(isGrid()).toBe(false);

    keys.back();
    expect(selectedAgent()).toBe('s1');

    keys.forward();
    expect(selectedAgent()).toBe('s2');
    keys.forward();
    expect(selectedAgent()).toBe('s2');
  });

  const OWED = { turn_owed: true };
  const SETTLED = { turn_owed: false };
  const SNOOZED = { turn_owed: false, turn_snoozed_until: LATER };

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
    ['stays on an agent the user just pinned', { s1: OWED, s2: OWED }, 's1', { s1: { turn_owed: false, pinned_at: '2026-08-03T12:00:00Z' }, s2: OWED }, 's1'],
    ['stays on an agent that left the queue for the crew', { s1: OWED, s2: OWED }, 's1', { s1: { turn_owed: false, crew_member: 'fern' }, s2: OWED }, 's1'],
  ])('%s', async (_, turns, watched, update, landing) => {
    const view = await renderAgents(turns);
    open(watched);

    view.update(update);

    if (landing) expect(selectedAgent()).toBe(landing);
    else expect(isHome()).toBe(true);
  });

  it('leaves history and keyboard focus consistent when it moves on', async () => {
    const view = await renderAgents({ s1: OWED, s2: OWED });
    open('s1');
    await view.daemon.idle();

    view.update({ s1: SETTLED, s2: OWED });
    await settleFocus(view.daemon);
    expect(selectedAgent()).toBe('s2');
    expect(focusedPane()).toBe('pane-s2');

    keys.back();
    expect(selectedAgent()).toBe('s1');
  });

  it('keeps a settled agent the user chose while other turns are owed', async () => {
    const view = await renderAgents({ s1: SETTLED, s2: OWED });
    open('s1');

    view.update({ s1: SETTLED, s2: OWED });

    expect(selectedAgent()).toBe('s1');
  });

  it.each([
    ['⌘J', () => press('j')],
  ])('jumps with %s to the turn owed longest, not the first row', async (_, jump) => {
    const view = await renderAgents({ s1: { ...OWED, turn_opened_at: '2026-08-03T11:00:00Z' }, s2: { ...OWED, turn_opened_at: '2026-08-03T09:00:00Z' } });

    jump();
    await view.daemon.idle();

    expect(selectedAgent()).toBe('s2');
  });

  it('stays home on ⌘J when no turn is owed', async () => {
    const view = await renderAgents({ s1: SETTLED, s2: SETTLED });

    press('j');
    await view.daemon.idle();

    expect(isHome()).toBe(true);
  });

  it('takes the user to the next turn after they ask to follow from an all-settled home', async () => {
    const view = await renderAgents({ s1: SETTLED, s2: SETTLED });
    fireEvent.click(within(screen.getByTestId('follow-next-turn')).getByRole('checkbox'));

    view.update({ s1: SETTLED, s2: OWED });

    expect(selectedAgent()).toBe('s2');
  });

  it.each([
    ['opening a tile-only workspace', () => fireEvent.click(screen.getByRole('button', { name: 'Open workspace notes' }))],
    ['going back through history', () => keys.back()],
  ])('stops waiting for the next turn after %s', async (_, navigate) => {
    const view = await renderAgents({ s1: OWED, s2: SETTLED });
    view.daemon.emit({
      event: 'workspace_registered',
      workspace: daemonWorkspace('notes', { root: { type: 'tile', tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/tmp/notes.md' } }, { pinned: true }),
    });
    open('s1');
    view.update({ s1: SETTLED, s2: SETTLED });
    expect(isHome()).toBe(true);

    navigate();
    await view.daemon.idle();
    const settledOn = selectedAgent();
    view.update({ s1: SETTLED, s2: OWED });

    expect(selectedAgent()).toBe(settledOn);
  });

  it('keeps a tile-only workspace open when another session closes', async () => {
    const view = await renderAgents({ s1: SETTLED, s2: SETTLED });
    view.daemon.emit({
      event: 'workspace_registered',
      workspace: daemonWorkspace('notes', { root: { type: 'tile', tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/tmp/notes.md' } }, { title: 'notes', pinned: true }),
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open workspace notes' }));
    await view.daemon.idle();

    view.daemon.emit({ event: 'session_unregistered', session: queueSession('s2', 10) });
    await view.daemon.idle();

    expect(isHome()).toBe(false);
    expect(document.querySelector('[data-pane-id="tile-notes"]')).not.toBeNull();
  });

  it.each([
    ['grid', () => keys.grid()],
    ['back', () => keys.back()],
  ])('drops a deep-linked selection that is still waiting for its pane after %s', async (_, navigate) => {
    const { daemon } = await renderQueue({ laidOut: ['s1'] });
    open('s1');

    deepLinkTo('s2');
    navigate();
    layOut(daemon, 's2');

    expect(selectedAgent()).not.toBe('s2');
  });

  it('forgets a deep-linked selection once the daemon drops its session', async () => {
    const { daemon } = await renderQueue({ laidOut: ['s1'] });
    open('s1');
    deepLinkTo('s2');

    daemon.emit({ event: 'sessions_updated', sessions: [agent('s1')] });
    layOut(daemon, 's2');
    daemon.emit({ event: 'sessions_updated', sessions: [agent('s1'), agent('s2')] });

    expect(selectedAgent()).toBe('s1');
  });

  it('finishes a deep-linked selection even when the queue would move elsewhere', async () => {
    const { daemon } = await renderQueue({ owed: ['s1', 's2'], laidOut: ['s1'] });
    open('s1');
    deepLinkTo('s2');

    daemon.emit({ event: 'sessions_updated', sessions: [agent('s1', { turn_owed: false }), agent('s2', { turn_owed: true })] });
    layOut(daemon, 's2');

    expect(selectedAgent()).toBe('s2');
  });

  it('keeps the grid through unrelated updates, and leaves it for the agent the user picks', async () => {
    const { daemon } = await renderQueue();
    keys.grid();
    expect(isGrid()).toBe(true);

    daemon.emit({ event: 'settings_updated', settings: { queue_mode_enabled: 'true', unrelated: 'x' } });
    daemon.emit({ event: 'sessions_updated', sessions: [agent('s1'), agent('s2', { state: 'idle' })] });
    expect(isGrid()).toBe(true);

    open('s1');
    expect(isGrid()).toBe(false);
    expect(selectedAgent()).toBe('s1');

    keys.home();
    expect(isHome()).toBe(true);
  });

  describe('keyboard focus', () => {
    it('lands in the opened agent’s terminal once, and later updates do not pull it back', async () => {
      const { daemon } = await renderQueue();
      open('s2');
      await settleFocus(daemon);
      expect(focusedPane()).toBe('pane-s2');

      screen.getByRole('button', { name: 'Open s1' }).focus();
      daemon.emit({ event: 'session_state_changed', session: agent('s2', { label: 'renamed' }) });
      await settleFocus(daemon);

      expect(focusedPane()).toBeNull();
    });

    it('lands only in the last of several quick selections', async () => {
      const ids = ['s1', 's2', 's3'];
      const { daemon } = await renderApp({
        initialState: {
          sessions: ids.map((id) => daemonSession(id, { state: 'idle' })),
          workspaces: ids.map(agentWorkspace),
        },
      });
      open('s1');
      await settleFocus(daemon);

      pressShortcut('session.next');
      pressShortcut('session.next');
      await settleFocus(daemon);

      expect(selectedAgent()).toBe('s3');
      expect(focusedPane()).toBe('pane-s3');
    });

    it('stays out of the terminal when Home follows the selection in the same moment', async () => {
      const { daemon } = await renderQueue();

      act(() => {
        open('s1');
        keys.home();
      });
      await settleFocus(daemon);

      expect(isHome()).toBe(true);
      expect(focusedPane()).toBeNull();
    });
  });

  it('asks for attention from sessions waiting on the user or in an unknown state, and not from the others', async () => {
    const states = ['waiting_input', 'pending_approval', 'unknown', 'stopped', 'waiting', 'working', 'idle', 'launching', 'scheduled', 'recoverable'] as DaemonSession['state'][];
    const { daemon } = await renderApp({
      initialState: {
        sessions: states.map((state) => daemonSession(state, { state })),
        workspaces: states.map((state) => agentWorkspace(state)),
      },
    });

    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));

    expect(sidebarBadges().map((title) => title.replace(/ \(.*\)$/, '')).sort()).toEqual(['pending_approval', 'stopped', 'unknown', 'waiting', 'waiting_input']);
  });
});
