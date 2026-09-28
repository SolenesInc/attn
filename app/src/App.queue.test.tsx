import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { soloDesktop, daemonSession, crewMember, type DaemonSession, type DaemonDesktop } from './test/daemonFixtures';
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
  crewInQueue?: boolean;
  desktop?: (session: DaemonSession) => Partial<DaemonDesktop>;
  crew?: ReturnType<typeof crewMember>[];
}

function launch({ sessions = team(), queue = true, crewInQueue = false, desktop = () => ({}), crew = [] }: Launch = {}) {
  return renderApp({
    initialState: {
      settings: queue ? { ...QUEUE, ...(crewInQueue ? { queue_crew_enabled: 'true' } : {}) } : {},
      sessions,
      crew,
      desktops: sessions.map((session) => ({ ...soloDesktop(session.id), ...desktop(session) })),
    },
  });
}

async function openAgentList(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /more agents/i })));
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

function shownDesktops() {
  return Array.from(document.querySelectorAll('.session-terminal-desktop[data-session-visible="1"]'))
    .map((desktop) => desktop.getAttribute('data-desktop-id'));
}

async function press(daemon: ScriptedDaemon, name: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name })));
}

describe('App queue', () => {
  it('keeps pinned crew out of the hidden count when crew also joins the queue', async () => {
    const sessions = [
      agent('chief', { chief_of_staff: true }),
      ...[4, 3, 2].map((n) => agent(`owed-${n}`, { turn_owed: true, turn_opened_at: ago(n * HOUR) })),
      agent('crew-owed', { crew_member: 'alder', turn_owed: true, turn_opened_at: ago(HOUR) }),
      agent('working'),
    ];
    const { daemon } = await launch({ sessions, crewInQueue: true, crew: [crewMember('alder')] });

    expect(screen.getByTestId('queue-crew-alder')).toBeInTheDocument();
    expect(screen.getAllByTestId(/queue-turn-owed-/)).toHaveLength(3);
    expect(screen.getByTestId('queue-agents-toggle')).toHaveTextContent('1 more agents');
    expect(screen.getByTestId('queue-agents-counts')).toHaveTextContent('1 working');
    await openAgentList(daemon);
    expect(screen.getByTestId('queue-also-waiting-header')).toHaveTextContent('Also waiting 1');
  });

  it('fits owed turns to the sidebar height and counts only rows below the toggle', async () => {
    let height = 500;
    const observers: Array<{ trigger: (node?: Element) => void }> = [];
    class FitObserver implements ResizeObserver {
      private connected = true;
      private readonly nodes = new Set<Element>();
      constructor(private readonly callback: ResizeObserverCallback) { observers.push(this); }
      observe(node: Element) { this.nodes.add(node); }
      unobserve(node: Element) { this.nodes.delete(node); }
      disconnect() { this.connected = false; }
      trigger(node?: Element) { if (this.connected && (!node || this.nodes.has(node))) this.callback([], this); }
    }
    const NativeMutationObserver = MutationObserver;
    const mutations: Array<{ trigger: (node: Node) => void }> = [];
    class FitMutationObserver implements MutationObserver {
      private readonly native: MutationObserver;
      private body = false;
      constructor(private readonly callback: MutationCallback) {
        this.native = new NativeMutationObserver(callback);
      }
      observe(target: Node, options?: MutationObserverInit) {
        this.native.observe(target, options);
        if (target instanceof HTMLElement && target.classList.contains('queue-sidebar-body')) {
          this.body = true;
          mutations.push(this);
        }
      }
      disconnect() { this.native.disconnect(); }
      takeRecords() { return this.native.takeRecords(); }
      trigger(node: Node) {
        if (!this.body) return;
        this.callback([{
          type: 'childList', target: node.parentNode ?? node,
          addedNodes: document.querySelectorAll('[data-testid="sidebar-automation-runs"]'),
          removedNodes: document.querySelectorAll('.no-removed-sidebar-block'),
          attributeName: null, attributeNamespace: null, nextSibling: null, oldValue: null, previousSibling: null,
        }], this);
      }
    }
    vi.stubGlobal('ResizeObserver', FitObserver);
    vi.stubGlobal('MutationObserver', FitMutationObserver);
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('queue-sidebar-body') ? height : 0;
    });
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
      if (this.classList.contains('queue-waiting-lead')) return this.children.length * 33 - 1;
      if (this.classList.contains('queue-waiting-card')) return 90 + (this.querySelector('.queue-waiting-lead')?.children.length ?? 0) * 33;
      if (this.classList.contains('queue-crew-block')) return 120;
      if (this.classList.contains('automation-runs')) return 130;
      if (this.classList.contains('sidebar-home-row')) return 30;
      if (this.classList.contains('queue-row')) return 32;
      return 0;
    });

    try {
      const sessions = [
        agent('chief', { chief_of_staff: true }),
        ...[1, 2, 3, 4, 5].map((n) => agent(`owed-${n}`, { turn_owed: true, turn_opened_at: ago(n * HOUR) })),
        agent('crew-awake', { crew_member: 'alder' }),
        agent('working'),
        agent('snoozed-1', { turn_snoozed_until: fromNow(HOUR) }),
        agent('snoozed-2', { turn_snoozed_until: fromNow(2 * HOUR) }),
      ];
      const { daemon } = await launch({ sessions, crew: [crewMember('alder'), crewMember('keel'), crewMember('trellis')] });
      await daemon.idle();
      expect(screen.getAllByTestId(/queue-turn-owed-/)).toHaveLength(5);
      expect(screen.getByTestId('queue-agents-toggle')).toHaveTextContent('3 more agents');
      expect(screen.getByTestId('queue-agents-counts')).toHaveTextContent('1 working2 snoozed');

      const sent = [...daemon.sent];
      await act(async () => { height = 360; observers.forEach((observer) => observer.trigger()); });
      await daemon.idle();
      expect(screen.getAllByTestId(/queue-turn-owed-/)).toHaveLength(3);
      expect(screen.getByTestId('queue-agents-toggle')).toHaveTextContent('5 more agents');
      expect(screen.getByTestId('queue-agents-counts')).toHaveTextContent('2 waiting1 working2 snoozed');
      expect(daemon.sent).toEqual(sent);

      await openAgentList(daemon);
      const waitingBand = screen.getByTestId('queue-also-waiting-header');
      expect(waitingBand).toHaveTextContent('Also waiting 2');
      expect(waitingBand.compareDocumentPosition(screen.getByText('Working 1')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      await act(async () => { height = 500; observers.forEach((observer) => observer.trigger()); });
      expect(screen.getByTestId('queue-also-waiting-header')).toHaveTextContent('Also waiting 2');
      expect(daemon.sent).toEqual(sent);

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('queue-agents-toggle')));
      expect(screen.getAllByTestId(/queue-turn-owed-/)).toHaveLength(5);
      await gesture(daemon, () => daemon.emit({
        event: 'session_state_changed',
        session: agent('working', { automation: {
          definition_id: 'review', definition_name: 'Review', run_id: 'run-1', trigger_type: 'manual',
        } }),
      }));
      const automationBlock = screen.getByTestId('sidebar-automation-runs');
      await act(async () => {
        mutations.forEach((observer) => observer.trigger(automationBlock));
        observers.forEach((observer) => observer.trigger(automationBlock));
      });
      expect(screen.getAllByTestId(/queue-turn-owed-/)).toHaveLength(4);
      expect(screen.getByTestId('queue-agents-toggle')).toHaveTextContent('3 more agents');
    } finally {
      vi.restoreAllMocks();
      vi.unstubAllGlobals();
    }
  });

  it('replaces the desktop tree with the chief and the owed turns oldest first, and keeps the settled rest in the agent list', async () => {
    const { daemon } = await launch();

    expect(bandRows()).toEqual(['queue-chief-chief', 'queue-turn-older', 'queue-turn-newer']);
    expect(treeRows()).toEqual([]);

    await openAgentList(daemon);
    expect(bandRows()).toContain('queue-settled-settled');
  });

  it('leaves the desktop tree alone while the queue is off', async () => {
    await launch({ queue: false });

    expect(screen.queryByTestId('sidebar-queue')).toBeNull();
    expect(treeRows().sort()).toEqual(['chief', 'newer', 'older', 'settled']);
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

    expect(queue().getByText('Nothing owed')).toBeInTheDocument();
  });

  it('opens an agent from its row', async () => {
    const { daemon } = await launch();

    await press(daemon, 'Open older');

    expect(daemon.sentOf('desktop_show_session')).toEqual([expect.objectContaining({ session_id: 'older' })]);
    expect(daemon.sentOf('desktop_set_current')).toEqual([]);
    expect(shownDesktops()).toEqual(['desktop-older']);
  });

  it('keeps the session menu reachable from every band', async () => {
    const { daemon } = await launch();
    await openAgentList(daemon);

    for (const id of ['chief', 'older', 'settled']) {
      expect(queue().getByRole('button', { name: `Actions for ${id}` })).toBeInTheDocument();
    }
  });

  it('settles an owed turn without opening it, and offers nothing to settle on a settled row or the chief', async () => {
    const { daemon } = await launch();

    await press(daemon, 'Settle older');

    expect(daemon.sentOf('settle_turn')).toEqual([{ cmd: 'settle_turn', session_id: 'older' }]);
    expect(daemon.sentOf('desktop_set_current')).toEqual([]);
    expect(queue().queryByRole('button', { name: 'Settle settled' })).toBeNull();
    expect(queue().queryByRole('button', { name: 'Settle chief' })).toBeNull();
  });

  it.each([['1h', 1], ['2h', 2], ['4h', 4]] as const)('snoozes an owed or settled agent for %s, and never the chief', async (choice, hours) => {
    const { daemon } = await launch();
    await openAgentList(daemon);
    expect(queue().queryByRole('button', { name: 'Snooze chief' })).toBeNull();

    await press(daemon, 'Snooze older');
    const until = new Date(Date.now() + hours * HOUR).toISOString();
    await gesture(daemon, () => fireEvent.click(within(screen.getByRole('menu', { name: 'Snooze older' })).getByTestId(`snooze-choice-${choice}`)));
    await press(daemon, 'Snooze settled');
    expect(screen.getByRole('menu', { name: 'Snooze settled' })).toBeInTheDocument();

    expect(daemon.sentOf('snooze_turn')).toEqual([{ cmd: 'snooze_turn', session_id: 'older', until }]);
    expect(daemon.sentOf('desktop_set_current')).toEqual([]);
  });

  it('collects deferred agents under Snoozed in the agent list, and wakes one without opening it', async () => {
    const { daemon } = await launch({ sessions: [...team(), agent('later', { turn_snoozed_until: fromNow(HOUR) })] });

    expect(bandRows()).not.toContain('queue-settled-later');
    await openAgentList(daemon);
    const later = within(screen.getByTestId('queue-snoozed-later'));
    expect(later.queryByRole('button', { name: 'Settle later' })).toBeNull();

    await gesture(daemon, () => fireEvent.click(later.getByRole('button', { name: 'Wake later' })));

    expect(daemon.sentOf('wake_turn')).toEqual([{ cmd: 'wake_turn', session_id: 'later' }]);
    expect(daemon.sentOf('desktop_set_current')).toEqual([]);
  });

  it('draws no Snoozed section while nothing is deferred', async () => {
    await launch();

    expect(screen.queryByTestId('sidebar-snoozed')).toBeNull();
  });

  const collapsedRailTeam = [
    agent('settled', { state: 'waiting_input' }),
    agent('owed', { state: 'working', turn_owed: true, turn_opened_at: ago(HOUR) }),
  ];

  it('badges the collapsed rail by state while the queue is off', async () => {
    const { daemon } = await launch({ queue: false, sessions: collapsedRailTeam, desktop: (session) => ({ name: session.id }) });

    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));

    const badges = Array.from(document.querySelectorAll('.session-icon'))
      .filter((icon) => icon.querySelector('.mini-badge'))
      .map((icon) => icon.getAttribute('title'));
    expect(badges).toHaveLength(1);
    expect(badges[0]).toMatch(/^settled/);
  });

  it('names what the queue owes on the collapsed queue bar', async () => {
    const { daemon } = await launch({ sessions: collapsedRailTeam });

    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));

    expect(screen.getByTestId('queue-bar-pill')).toHaveAttribute('data-waiting', '1');
    expect(screen.getByTestId('queue-bar-pill')).toHaveTextContent('1 waiting·owed');
  });
});
