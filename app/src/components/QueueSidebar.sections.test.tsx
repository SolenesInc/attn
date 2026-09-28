import { StrictMode, type ComponentProps } from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar } from './Sidebar';
import { useAgentList } from './useAgentList';
import { focusedQueueRow } from './focusedQueueRow';
import { BuiltinDelegationRole, type SessionDelegationRole } from '../types/generated';
import { WAKE_ARM_TIMEOUT_MS } from './CrewWake';
import { buildQueueBands, formatTurnAge } from '../utils/queueBands';
import { desktopGroups } from '../test/desktops';

interface TestSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
  desktopId: string;
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  turnSnoozedUntil?: string;
  parentSessionId?: string;
  crewMember?: string;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
  delegation_role?: SessionDelegationRole;
}

const baseProps = {
  selectedId: null,
  selectedDesktopId: null,
  collapsed: false,
  surface: 'queue-open' as const,
  headerActions: [],
  onSelectSession: () => {},
  onSelectDesktop: () => {},
  onNewSession: () => {},
  onCloseSession: () => {},
  onReloadSession: () => {},
  onGoToDashboard: () => {},
  onToggleCollapse: () => {},
};

const fixtureDesktops = [
  { id: 'ws-a', title: 'alpha' },
  { id: 'ws-b', title: 'beta' },
];

function sidebarData(sessions: TestSession[]) {
  const desktops = desktopGroups(fixtureDesktops, sessions);
  return { desktops, visualIndexByDesktopId: new Map(fixtureDesktops.map((desktop, index) => [desktop.id, index])) };
}

function renderSidebar(
  sessions: TestSession[],
  queueMode: boolean,
  overrides: Partial<ComponentProps<typeof Sidebar>> = {},
) {
  const data = sidebarData(sessions);
  return render(
    <Sidebar
      {...baseProps}
      {...data}
      {...overrides}
      queue={queueMode ? buildQueueBands(data.desktops) : null}
    />
  );
}

function ListToggling(props: ComponentProps<typeof Sidebar>) {
  const { agentListOpen, toggleAgentList } = useAgentList();
  return <Sidebar {...props} agentListOpen={agentListOpen} onToggleAgentList={toggleAgentList} />;
}

function renderWithList(sessions: TestSession[], overrides: Partial<ComponentProps<typeof Sidebar>> = {}) {
  const { agentListOpen, ...rest } = overrides;
  const data = sidebarData(sessions);
  const view = render(<ListToggling {...baseProps} {...data} {...rest} queue={buildQueueBands(data.desktops)} />);
  if (agentListOpen) fireEvent.click(screen.getByTestId('queue-agents-toggle'));
  return view;
}

function queueRowIds(container: HTMLElement) {
  return Array.from(container.querySelectorAll('[data-testid="sidebar-queue"] .queue-row'))
    .map((row) => row.getAttribute('data-testid'));
}

const sessions: TestSession[] = [
  { id: 'chief', label: 'chief', state: 'idle', desktopId: 'ws-a', chiefOfStaff: true },
  { id: 'newer', label: 'newer', state: 'waiting_input', desktopId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T11:00:00Z' },
  { id: 'older', label: 'older', state: 'working', desktopId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
  { id: 'settled', label: 'settled', state: 'waiting_input', desktopId: 'ws-b' },
];

function owed(id: string, hour: number, desktopId = 'ws-a'): TestSession {
  return {
    id,
    label: id,
    state: 'waiting_input',
    desktopId,
    turnOwed: true,
    turnOpenedAt: `2026-07-26T${String(hour).padStart(2, '0')}:00:00Z`,
  };
}

describe('the queue sidebar', () => {
  it('replaces the desktop tree at its own narrower width', () => {
    const { container } = renderSidebar(sessions, true);
    expect(screen.getByTestId('queue-sidebar')).toHaveClass('sidebar', 'queue-sidebar');
    expect(container.querySelectorAll('.session-list [data-testid^="sidebar-session-"]')).toHaveLength(0);
  });

  it('leaves the tree alone while the arrangement is off, satellites included', () => {
    const tagged: TestSession[] = [
      ...sessions,
      { id: 'shell', label: 'shell', state: 'idle', desktopId: 'ws-b', parentSessionId: 'older' },
    ];
    renderSidebar(tagged, false);

    expect(screen.queryByTestId('sidebar-queue')).toBeNull();
    for (const id of ['chief', 'newer', 'older', 'settled', 'shell']) {
      expect(screen.getByTestId(`sidebar-session-${id}`)).toBeTruthy();
    }
  });

  it('anchors the chief above the owed turns, oldest first, and keeps the rest behind the list', () => {
    const { container } = renderSidebar(sessions, true);
    expect(queueRowIds(container)).toEqual(['queue-chief-chief', 'queue-turn-older', 'queue-turn-newer']);
    expect(screen.getByTestId('queue-waiting-card')).toHaveAttribute('data-waiting', '2');
    expect(screen.getByTestId('queue-waiting-head')).toHaveTextContent('2 waiting');
  });

  it('starts with three turns and names the overflow in the list', () => {
    const many = [owed('t1', 1), owed('t2', 2), owed('t3', 3), owed('t4', 4), owed('t5', 5)];
    const closed = renderSidebar(many, true);
    expect(queueRowIds(closed.container)).toEqual(['queue-turn-t1', 'queue-turn-t2', 'queue-turn-t3']);
    expect(screen.getByTestId('queue-agents-toggle')).toHaveTextContent('2 more agents');
    expect(screen.getByTestId('queue-agents-counts')).toHaveTextContent('2 waiting');
    closed.unmount();

    const open = renderSidebar(many, true, { agentListOpen: true });
    expect(queueRowIds(open.container)).toEqual(
      ['queue-turn-t1', 'queue-turn-t2', 'queue-turn-t3', 'queue-turn-t4', 'queue-turn-t5'],
    );
    expect(screen.getByTestId('queue-also-waiting-header')).toHaveTextContent('Also waiting 2');
  });

  it('opens the oldest turn from the waiting head, and says so when nothing is owed', () => {
    const onJumpToWaiting = vi.fn();
    const { unmount } = renderSidebar(sessions, true, { onJumpToWaiting });
    fireEvent.click(screen.getByTestId('queue-waiting-head'));
    expect(onJumpToWaiting).toHaveBeenCalledOnce();
    unmount();

    renderSidebar([sessions[0], sessions[3]], true, { onJumpToWaiting });
    expect(screen.getByTestId('queue-empty')).toHaveTextContent('Nothing owed');
    expect(screen.getByTestId('queue-waiting-head')).toBeDisabled();
  });

  it('counts only the rows hidden below the list toggle', () => {
    const unplaced: TestSession = { id: 'loose', label: 'loose', state: 'idle', desktopId: 'nowhere' };
    const shell: TestSession = { id: 'loose-shell', label: 'shell', state: 'idle', desktopId: 'nowhere', parentSessionId: 'loose' };
    const later: TestSession = {
      id: 'later', label: 'later', state: 'idle', desktopId: 'ws-a',
      turnSnoozedUntil: new Date(Date.now() + 3600_000).toISOString(),
    };
    renderSidebar([...sessions, unplaced, shell, later], true, { crew: [{ id: 'alder' }] });

    expect(screen.getByTestId('queue-agents-toggle')).toHaveTextContent('3 more agents');
    expect(screen.getByTestId('queue-agents-toggle')).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByTestId('queue-agents-counts')).toHaveTextContent('2 working1 snoozed');
  });

  it('keeps the filter reachable when no agents are hidden', () => {
    renderWithList([owed('only', 1)]);
    expect(screen.getByTestId('queue-agents-toggle')).toHaveTextContent('No more agents');
    fireEvent.click(screen.getByTestId('queue-agents-toggle'));
    expect(screen.getByTestId('queue-agent-filter')).toBeInTheDocument();
  });

  it('lists the working and snoozed agents under the waiting turns once opened', () => {
    const later: TestSession = {
      id: 'later', label: 'later', state: 'idle', desktopId: 'ws-a',
      turnSnoozedUntil: new Date(Date.now() + 3600_000).toISOString(),
    };
    const { container } = renderSidebar([...sessions, later], true, { agentListOpen: true });

    expect(queueRowIds(container)).toEqual([
      'queue-chief-chief', 'queue-turn-older', 'queue-turn-newer', 'queue-settled-settled', 'queue-snoozed-later',
    ]);
    expect(screen.getByTestId('queue-snoozed-header')).toHaveTextContent('Snoozed 1');
  });

  it('narrows the opened list by the filter, never the lead turns', () => {
    const { container } = renderSidebar(
      [...sessions, { id: 'other', label: 'Other thing', state: 'idle', desktopId: 'ws-a' }],
      true,
      { agentListOpen: true },
    );
    fireEvent.change(screen.getByTestId('queue-agent-filter'), { target: { value: 'oth' } });

    expect(queueRowIds(container)).toEqual(['queue-chief-chief', 'queue-turn-older', 'queue-turn-newer', 'queue-settled-other']);
  });

  it('marks each row with the desktop slot it lives on', () => {
    renderSidebar(
      [...sessions, { id: 'loose', label: 'loose', state: 'idle', desktopId: 'nowhere' }],
      true,
      { agentListOpen: true },
    );
    const where = (id: string) => screen.getByTestId(id).querySelector('.queue-row-where');

    expect(where('queue-turn-older')).toHaveTextContent('2');
    expect(where('queue-turn-older')).toHaveAttribute('title', 'beta');
    expect(where('queue-turn-newer')).toHaveTextContent('1');
    expect(where('queue-settled-loose')).toHaveTextContent('—');
    expect(where('queue-settled-loose')).toHaveClass('is-unplaced');
  });

  it('offers the delegation chain without repeating the dispatcher below the title', () => {
    const onSelectSession = vi.fn();
    const linked: TestSession[] = [
      { id: 'root', label: 'root session', state: 'idle', desktopId: 'ws-a', delegation_role: { name: 'Orchestrator', builtin: BuiltinDelegationRole.Orchestrator } },
      {
        id: 'child',
        label: 'child',
        state: 'working',
        desktopId: 'ws-b',
        dispatcher_session_id: 'root',
        dispatcher_member: 'alder',
      },
    ];
    renderSidebar(linked, true, { onSelectSession, agentListOpen: true });

    const child = screen.getByTestId('queue-settled-child');
    expect(within(child).queryByTestId('sidebar-dispatcher')).toBeNull();
    expect(within(child).getByRole('button', { name: 'Show delegation chain for child' })).toBeInTheDocument();
    expect(onSelectSession).not.toHaveBeenCalled();

    const root = screen.getByTestId('queue-settled-root');
    expect(within(root).getByRole('button', { name: 'Orchestrator · Show delegation chain for root session' })).toHaveAttribute('data-role', 'orchestrator');
  });

  it('keeps navigation available after the dispatcher session ended without adding a subtitle', () => {
    const linked: TestSession[] = [{
      id: 'child',
      label: 'child',
      state: 'working',
      desktopId: 'ws-a',
      dispatcher_session_id: 'ended',
      dispatcher_member: 'alder',
    }];
    renderSidebar(linked, true, { agentListOpen: true });

    const child = screen.getByTestId('queue-settled-child');
    expect(within(child).queryByTestId('sidebar-dispatcher')).toBeNull();
    expect(within(child).getByRole('button', { name: 'Show delegation chain for child' })).toBeInTheDocument();
    expect(within(child).queryByRole('button', { name: 'Open dispatcher Alder' })).toBeNull();
  });

  it('keeps hover local to the pointed row', () => {
    const chain: TestSession[] = [
      { id: 'root', label: 'root', state: 'idle', desktopId: 'ws-a' },
      { id: 'middle', label: 'middle', state: 'idle', desktopId: 'ws-a', dispatcher_session_id: 'root' },
      { id: 'leaf', label: 'leaf', state: 'idle', desktopId: 'ws-b', dispatcher_session_id: 'middle' },
      { id: 'grandchild', label: 'grandchild', state: 'idle', desktopId: 'ws-b', dispatcher_session_id: 'leaf' },
    ];
    renderSidebar(chain, true, { selectedId: 'middle', agentListOpen: true });
    const root = screen.getByTestId('queue-settled-root');
    const middle = screen.getByTestId('queue-settled-middle');
    const leaf = screen.getByTestId('queue-settled-leaf');
    const grandchild = screen.getByTestId('queue-settled-grandchild');

    expect(middle).toHaveClass('selected');
    fireEvent.pointerEnter(middle);
    expect(root).not.toHaveClass('kin-up');
    expect(leaf).not.toHaveClass('kin-down');
    expect(middle).not.toHaveClass('kin-up', 'kin-down');
    expect(grandchild).not.toHaveClass('kin-down');
    fireEvent.pointerLeave(middle);
    expect(root).not.toHaveClass('kin-up');
    expect(leaf).not.toHaveClass('kin-down');
  });

  it('shows the live state of a queued agent, because being queued no longer means stopped', () => {
    renderSidebar(sessions, true);
    expect(screen.getByTestId('queue-turn-older').getAttribute('data-state')).toBe('working');
  });

  it('hands the agent over on click and from the keyboard', () => {
    const onSelectSession = vi.fn();
    renderSidebar(sessions, true, { onSelectSession });

    const open = screen.getByTestId('queue-select-older');
    expect(open.tagName).toBe('BUTTON');
    expect(open.getAttribute('aria-label')).toBe('Open older');
    fireEvent.click(open);
    expect(onSelectSession).toHaveBeenCalledWith('older');
  });

  it('settles a row without selecting it', () => {
    const onSettleTurn = vi.fn();
    const onSelectSession = vi.fn();
    renderSidebar(sessions, true, { onSettleTurn, onSelectSession });

    fireEvent.click(screen.getByTestId('queue-settle-older'));
    expect(onSettleTurn).toHaveBeenCalledWith('older');
    expect(onSelectSession).not.toHaveBeenCalled();
  });

  it('gives a shell no row of its own while it sits beside its agent', () => {
    const withShell: TestSession[] = [
      ...sessions,
      { id: 'shell', label: 'shell', state: 'idle', desktopId: 'ws-b', parentSessionId: 'older' },
      { id: 'orphan', label: 'orphan', state: 'idle', desktopId: 'ws-b' },
    ];
    const { container } = renderSidebar(withShell, true, { agentListOpen: true });

    const rows = queueRowIds(container);
    expect(rows).not.toContain('queue-settled-shell');
    expect(rows).toContain('queue-settled-orphan');
  });

  it('drops an open session menu when the sidebar surface changes', () => {
    const data = sidebarData(sessions);
    const queue = buildQueueBands(data.desktops);
    const view = render(<Sidebar {...baseProps} {...data} queue={queue} />);
    fireEvent.click(screen.getByTestId('session-actions-older'));
    expect(screen.getByRole('menuitem', { name: /Reload session/ })).toBeInTheDocument();

    view.rerender(<Sidebar {...baseProps} {...data} queue={queue} surface="hidden" />);
    view.rerender(<Sidebar {...baseProps} {...data} queue={queue} surface="queue-open" />);
    expect(screen.queryByRole('menuitem', { name: /Reload session/ })).toBeNull();
  });

  it('keeps the per-session menu reachable from every band', () => {
    renderSidebar(sessions, true, { agentListOpen: true });
    for (const id of ['chief', 'older', 'settled']) {
      expect(screen.getByTestId(`session-actions-${id}`)).toBeTruthy();
    }
  });

  it('offers settle only where a turn is owed', () => {
    renderSidebar(sessions, true, { agentListOpen: true, onSettleTurn: vi.fn() });
    expect(screen.getByTestId('queue-settle-older')).toBeTruthy();
    expect(screen.queryByTestId('queue-settle-settled')).toBeNull();
    expect(screen.queryByTestId('queue-settle-chief')).toBeNull();
  });

  it('badges the collapsed rail by state outside queue mode', () => {
    const owedOnly: TestSession[] = [
      { id: 'settled', label: 'settled', state: 'waiting_input', desktopId: 'ws-a' },
      { id: 'owed', label: 'owed', state: 'working', desktopId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
    ];

    const { container } = renderSidebar(owedOnly, false, { collapsed: true });
    expect(container.querySelectorAll('.mini-badge')).toHaveLength(1);
    expect(container.querySelector('.session-icon .mini-badge')).toBeTruthy();
    expect(screen.queryByTestId('queue-bar')).toBeNull();
  });
});

describe('the queue sidebar header', () => {
  it('names the profile with its waiting count and switches profile', () => {
    const onSwitchProfile = vi.fn();
    renderSidebar(sessions, true, { profileName: 'Work', onSwitchProfile });
    const pill = screen.getByTestId('queue-profile-pill');
    expect(pill).toHaveTextContent('Work2');
    fireEvent.click(pill);
    expect(onSwitchProfile).toHaveBeenCalledOnce();
  });

  it('reaches a new agent and the commands, badged with what needs a look', () => {
    const onNewSession = vi.fn();
    const onOpenCommands = vi.fn();
    renderSidebar(sessions, true, { onNewSession, onOpenCommands, commandsBadge: 12 });

    fireEvent.click(screen.getByTestId('queue-new-agent'));
    expect(onNewSession).toHaveBeenCalledOnce();
    expect(screen.getByTestId('queue-commands')).toHaveTextContent('9+');
    fireEvent.click(screen.getByTestId('queue-commands'));
    expect(onOpenCommands).toHaveBeenCalledOnce();
  });
});

describe('the desktop strip', () => {
  it('puts every slotted desktop on a chip, dotted where a turn waits', () => {
    const onSelectDesktop = vi.fn();
    renderSidebar(sessions, true, { onSelectDesktop, selectedDesktopId: 'ws-b' });

    const beta = screen.getByTestId('queue-desktop-chip-2');
    expect(beta).toHaveClass('is-current');
    expect(beta).toHaveAttribute('data-waiting', 'true');
    expect(beta.getAttribute('title')).toMatch(/^beta \(/);
    expect(screen.getByTestId('queue-desktop-current')).toHaveTextContent('beta');
    fireEvent.click(screen.getByTestId('queue-desktop-chip-1'));
    expect(onSelectDesktop).toHaveBeenCalledWith('ws-a');
  });

  it('gathers desktops without a shortcut behind the overview', () => {
    const onOpenOverview = vi.fn();
    const data = sidebarData(sessions);
    render(
      <Sidebar
        {...baseProps}
        {...data}
        visualIndexByDesktopId={new Map([['ws-a', 0]])}
        selectedDesktopId="ws-b"
        onOpenOverview={onOpenOverview}
        queue={buildQueueBands(data.desktops)}
      />,
    );

    expect(screen.queryByTestId('queue-desktop-chip-2')).toBeNull();
    const extras = screen.getByTestId('queue-desktop-extras');
    expect(extras).toHaveTextContent('+1');
    expect(extras).toHaveClass('is-current');
    expect(extras.querySelector('.queue-desktop-chip-waiting')).toBeTruthy();
    expect(screen.getByTestId('queue-desktop-current')).toHaveTextContent('betano shortcut');

    fireEvent.click(extras);
    fireEvent.click(screen.getByTestId('queue-desktop-overview'));
    expect(onOpenOverview).toHaveBeenCalledTimes(2);
  });

  it('takes a dragged pane onto another desktop chip', () => {
    const onDesktopDragEnter = vi.fn();
    const onDesktopDragDrop = vi.fn();
    renderSidebar(sessions, true, {
      leafDrag: { sourceDesktopId: 'ws-a' },
      dragHoverDesktopId: 'ws-b',
      onDesktopDragEnter,
      onDesktopDragDrop,
    });

    const source = screen.getByTestId('queue-desktop-chip-1');
    const target = screen.getByTestId('queue-desktop-chip-2');
    expect(source).toHaveClass('is-drop-disabled');
    expect(target).toHaveClass('is-drop-entering');

    fireEvent.pointerEnter(source);
    fireEvent.pointerUp(source);
    expect(onDesktopDragEnter).not.toHaveBeenCalled();
    expect(onDesktopDragDrop).not.toHaveBeenCalled();

    fireEvent.pointerEnter(target);
    fireEvent.pointerUp(target);
    expect(onDesktopDragEnter).toHaveBeenCalledWith(expect.objectContaining({ id: 'ws-b' }));
    expect(onDesktopDragDrop).toHaveBeenCalledWith(expect.objectContaining({ id: 'ws-b' }));
  });
});

describe('walking the queue sidebar from the keyboard', () => {
  const focusedTestId = () => document.activeElement?.getAttribute('data-testid');

  it('walks every row with the arrows and wraps at both ends', () => {
    renderWithList(sessions, { agentListOpen: true });
    const filter = screen.getByTestId('queue-agent-filter');
    expect(document.activeElement).toBe(filter);

    const walk = (key: string) => fireEvent.keyDown(document.activeElement!, { key });
    walk('ArrowDown');
    expect(focusedTestId()).toBe('queue-select-chief');
    walk('ArrowDown');
    walk('ArrowDown');
    walk('ArrowDown');
    expect(focusedTestId()).toBe('queue-select-settled');
    walk('ArrowDown');
    expect(focusedTestId()).toBe('queue-select-chief');
    walk('ArrowUp');
    expect(focusedTestId()).toBe('queue-select-settled');
    expect(focusedQueueRow()).toMatchObject({ kind: 'session', sessionId: 'settled' });
  });

  it('types into the filter from any row while the list is open', () => {
    renderWithList(sessions, { agentListOpen: true });
    screen.getByTestId('queue-select-older').focus();

    fireEvent.keyDown(document.activeElement!, { key: 's' });
    expect(document.activeElement).toBe(screen.getByTestId('queue-agent-filter'));
    expect(screen.getByTestId('queue-agent-filter')).toHaveValue('s');
  });

  it('steps back one layer per Escape: filter, list, then the selected agent', () => {
    const onSelectSession = vi.fn();
    renderWithList(sessions, { agentListOpen: true, selectedId: 'newer', onSelectSession });
    fireEvent.change(screen.getByTestId('queue-agent-filter'), { target: { value: 'set' } });

    const escape = () => fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    escape();
    expect(screen.getByTestId('queue-agent-filter')).toHaveValue('');
    escape();
    expect(screen.queryByTestId('queue-agent-list')).toBeNull();
    expect(focusedTestId()).toBe('queue-agents-toggle');
    expect(onSelectSession).not.toHaveBeenCalled();
    escape();
    expect(onSelectSession).toHaveBeenCalledWith('newer');
  });

  it('focuses the filter on opening and hands focus back to where it was on closing', () => {
    const data = sidebarData(sessions);
    function WithTerminal() {
      const { agentListOpen, toggleAgentList } = useAgentList();
      return (
        <>
          <textarea data-testid="terminal" />
          <button data-testid="shortcut" onClick={toggleAgentList} />
          <Sidebar {...baseProps} {...data} queue={buildQueueBands(data.desktops)} agentListOpen={agentListOpen} onToggleAgentList={toggleAgentList} />
        </>
      );
    }
    render(<WithTerminal />);
    const terminal = screen.getByTestId('terminal');
    terminal.focus();

    fireEvent.click(screen.getByTestId('shortcut'));
    expect(focusedTestId()).toBe('queue-agent-filter');
    fireEvent.click(screen.getByTestId('shortcut'));
    expect(screen.queryByTestId('queue-agent-list')).toBeNull();
    expect(document.activeElement).toBe(terminal);

    fireEvent.click(screen.getByTestId('shortcut'));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(document.activeElement).toBe(terminal);
  });

  it('keeps focus on the terminal the user moved to while the list was open', () => {
    const data = sidebarData(sessions);
    function WithTwoTerminals() {
      const { agentListOpen, toggleAgentList } = useAgentList();
      return (
        <>
          <textarea data-testid="terminal-a" />
          <textarea data-testid="terminal-b" />
          <button data-testid="shortcut" onClick={toggleAgentList} />
          <Sidebar {...baseProps} {...data} queue={buildQueueBands(data.desktops)} agentListOpen={agentListOpen} onToggleAgentList={toggleAgentList} />
        </>
      );
    }
    render(<WithTwoTerminals />);
    screen.getByTestId('terminal-a').focus();
    fireEvent.click(screen.getByTestId('shortcut'));
    expect(focusedTestId()).toBe('queue-agent-filter');

    screen.getByTestId('terminal-b').focus();
    fireEvent.click(screen.getByTestId('shortcut'));
    expect(screen.queryByTestId('queue-agent-list')).toBeNull();
    expect(focusedTestId()).toBe('terminal-b');
  });

  it('forgets the filter when the list closes', () => {
    renderWithList(sessions, { agentListOpen: true });
    fireEvent.change(screen.getByTestId('queue-agent-filter'), { target: { value: 'set' } });
    fireEvent.click(screen.getByTestId('queue-agents-toggle'));
    fireEvent.click(screen.getByTestId('queue-agents-toggle'));
    expect(screen.getByTestId('queue-agent-filter')).toHaveValue('');
  });

  it('leaves chords to the shortcut layer', () => {
    renderWithList(sessions, { agentListOpen: true });
    screen.getByTestId('queue-select-older').focus();
    fireEvent.keyDown(document.activeElement!, { key: 'e', metaKey: true, shiftKey: true });
    expect(screen.getByTestId('queue-agent-filter')).toHaveValue('');
    expect(focusedQueueRow()).toMatchObject({ kind: 'session', sessionId: 'older' });
  });

  it('names no row when focus is elsewhere', () => {
    renderSidebar(sessions, true);
    expect(focusedQueueRow()).toEqual({ kind: 'none' });
  });
});

describe('snoozing from the sidebar', () => {
  const inAnHour = () => new Date(Date.now() + 3600_000).toISOString();

  it('offers snooze on an owed turn and on a working row alike, never on the chief', () => {
    const onOpenSnooze = vi.fn();
    renderSidebar(sessions, true, { onOpenSnooze, agentListOpen: true });

    fireEvent.click(screen.getByTestId('queue-snooze-older'));
    expect(onOpenSnooze).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'older' }), expect.anything());
    fireEvent.click(screen.getByTestId('queue-snooze-settled'));
    expect(onOpenSnooze).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'settled' }), expect.anything());
    expect(screen.queryByTestId('queue-snooze-chief')).toBeNull();
  });

  it('shows when each deferred agent comes back, and wakes it without selecting it', () => {
    const onWakeTurn = vi.fn();
    const onSelectSession = vi.fn();
    const deferred: TestSession[] = [
      { id: 'later', label: 'later', state: 'idle', desktopId: 'ws-a', turnSnoozedUntil: inAnHour() },
    ];
    renderSidebar(deferred, true, { onWakeTurn, onSelectSession, agentListOpen: true });

    const row = screen.getByTestId('queue-snoozed-later');
    expect(row.querySelector('.queue-row-wake-at')?.textContent).toBeTruthy();
    expect(screen.queryByTestId('queue-settle-later')).toBeNull();

    fireEvent.click(screen.getByTestId('queue-wake-later'));
    expect(onWakeTurn).toHaveBeenCalledWith('later');
    expect(onSelectSession).not.toHaveBeenCalled();
  });

  it('draws no snoozed band while nothing is deferred', () => {
    renderSidebar(sessions, true, { onWakeTurn: vi.fn(), agentListOpen: true });
    expect(screen.queryByTestId('queue-snoozed-header')).toBeNull();
  });
});

describe('formatTurnAge', () => {
  const opened = Date.parse('2026-07-26T10:00:00Z');

  it('reads how long the turn has been owed', () => {
    expect(formatTurnAge('2026-07-26T10:00:00Z', opened + 30_000)).toBe('now');
    expect(formatTurnAge('2026-07-26T10:00:00Z', opened + 12 * 60_000)).toBe('12m');
    expect(formatTurnAge('2026-07-26T10:00:00Z', opened + 3 * 3600_000)).toBe('3h');
    expect(formatTurnAge('2026-07-26T10:00:00Z', opened + 50 * 3600_000)).toBe('2d');
  });

  it('is empty when there is no stamp', () => {
    expect(formatTurnAge(undefined, opened)).toBe('');
    expect(formatTurnAge('not a date', opened)).toBe('');
  });
});

describe('the crew in the sidebar', () => {
  const roster = [{ id: 'alder' }, { id: 'keel' }, { id: 'trellis' }];

  function renderCrew(
    crewSessions: TestSession[],
    overrides: Record<string, unknown> = {},
    crew: { id: string; binding_session?: string }[] = roster,
  ) {
    return renderSidebar([...sessions, ...crewSessions], true, { crew, ...overrides });
  }

  it('anchors every member, awake or asleep, beside the chief', () => {
    const { container } = renderCrew([
      { id: 'sess-keel', label: 'keel of the day', state: 'working', desktopId: 'ws-a', crewMember: 'keel' },
    ]);

    const block = container.querySelector('[data-testid="queue-crew-block"]') as HTMLElement;
    expect(queueRowIds(block.parentElement!).filter((id) => id?.startsWith('queue-crew-')))
      .toEqual(['queue-crew-alder', 'queue-crew-keel', 'queue-crew-trellis']);
    expect(within(block).getByTestId('queue-chief-chief')).toBeInTheDocument();

    expect(screen.getByTestId('queue-crew-keel').getAttribute('data-crew-state')).toBe('awake');
    expect(screen.getByTestId('queue-crew-alder').getAttribute('data-crew-state')).toBe('asleep');
    expect(screen.getByTestId('queue-crew-alder').className).toContain('queue-row--crew');
  });

  it('shows a chief on the roster once, as the chief, and excludes it from more agents', () => {
    const chiefOnRoster = sessions.map((entry) => (entry.id === 'chief' ? { ...entry, crewMember: 'alder' } : entry));
    const moreAgents = () => screen.getByTestId('queue-agents-toggle').textContent;

    const { unmount } = renderSidebar(chiefOnRoster, true, { crew: [{ id: 'keel' }] });
    const withoutChiefOnRoster = moreAgents();
    unmount();

    renderSidebar(chiefOnRoster, true, { crew: [{ id: 'alder' }, { id: 'keel' }] });
    expect(screen.queryByTestId('queue-crew-alder')).toBeNull();
    expect(screen.getByTestId('queue-chief-chief')).toBeInTheDocument();
    expect(moreAgents()).toBe(withoutChiefOnRoster);
  });

  it('names each focused row by what it holds: an agent, or nothing for a sleeping member', () => {
    renderCrew([
      { id: 'sess-keel', label: 'keel of the day', state: 'working', desktopId: 'ws-a', crewMember: 'keel' },
    ], { onWakeCrewMember: () => {} });

    screen.getByTestId('queue-crew-select-alder').focus();
    expect(focusedQueueRow()).toEqual({ kind: 'other' });
    within(screen.getByTestId('queue-crew-keel')).getAllByRole('button')[0].focus();
    expect(focusedQueueRow()).toMatchObject({ kind: 'session', sessionId: 'sess-keel' });
    within(screen.getByTestId('queue-chief-chief')).getAllByRole('button')[0].focus();
    expect(focusedQueueRow()).toMatchObject({ kind: 'session', sessionId: 'chief' });
  });

  it('opens member details anchored on the row action for awake and asleep members', () => {
    const onOpenCrewMemberDetails = vi.fn();
    renderCrew(
      [{ id: 'sess-keel', label: 'keel of the day', state: 'working', desktopId: 'ws-a', crewMember: 'keel' }] as TestSession[],
      { onOpenCrewMemberDetails },
    );

    fireEvent.click(screen.getByTestId('crew-actions-alder'));
    fireEvent.click(screen.getByTestId('crew-member-details-action'));
    expect(onOpenCrewMemberDetails).toHaveBeenLastCalledWith('alder', screen.getByTestId('crew-actions-alder'));

    fireEvent.click(screen.getByTestId('session-actions-sess-keel'));
    fireEvent.click(screen.getByTestId('crew-member-details-action'));
    expect(onOpenCrewMemberDetails).toHaveBeenLastCalledWith('keel', screen.getByTestId('session-actions-sess-keel'));
  });

  it('shows an awake member exactly once, under its own row', () => {
    const { container } = renderCrew([
      { id: 'sess-keel', label: 'keel of the day', state: 'working', desktopId: 'ws-a', crewMember: 'keel', turnOwed: true, turnOpenedAt: '2026-07-26T08:00:00Z' },
    ]);

    expect(queueRowIds(container).filter((id) => id?.includes('keel'))).toEqual(['queue-crew-keel']);
  });

  it('adds an opted-in member owing a turn to the waiting turns and keeps its crew row', () => {
    const crewSession: TestSession = {
      id: 'sess-keel',
      label: 'keel of the day',
      state: 'working',
      desktopId: 'ws-a',
      crewMember: 'keel',
      turnOwed: true,
      turnOpenedAt: '2026-07-26T08:00:00Z',
    };
    const data = sidebarData([...sessions, crewSession]);
    render(
      <Sidebar
        {...baseProps}
        {...data}
        crew={roster}
        queue={buildQueueBands(data.desktops, { crewInQueue: true })}
      />,
    );

    expect(screen.getByTestId('queue-turn-sess-keel')).toBeInTheDocument();
    expect(screen.getByTestId('queue-crew-keel')).toHaveAttribute('data-crew-state', 'awake');
    expect(screen.getByTestId('queue-crew-alder')).toHaveAttribute('data-crew-state', 'asleep');
  });

  it('wakes a sleeping member on the second click, asks an awake one to sleep, and focuses its day', () => {
    const onWakeCrewMember = vi.fn();
    const onSleepCrewMember = vi.fn();
    const onSelectSession = vi.fn();
    renderCrew(
      [{ id: 'sess-keel', label: 'keel of the day', state: 'working', desktopId: 'ws-a', crewMember: 'keel' }] as TestSession[],
      { onWakeCrewMember, onSleepCrewMember, onSelectSession },
    );

    fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
    expect(onWakeCrewMember).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
    expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);

    fireEvent.click(screen.getByTestId('queue-crew-sleep-keel'));
    expect(onSleepCrewMember.mock.calls).toEqual([['keel']]);
    expect(onSelectSession).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('queue-crew-select-keel'));
    expect(onSelectSession.mock.calls).toEqual([['sess-keel']]);

    expect(screen.queryByTestId('queue-crew-wake-keel')).toBeNull();
    expect(screen.queryByTestId('queue-crew-sleep-trellis')).toBeNull();
    expect(screen.getByTestId('queue-crew-sleep-keel').getAttribute('aria-label')).toBe('Ask Keel to sleep');
  });

  it('writes a sleeping member as a name while the id stays the address', () => {
    renderCrew([], { onWakeCrewMember: vi.fn() });
    const row = screen.getByTestId('queue-crew-trellis');
    expect(row.textContent).toContain('Trellis');
    expect(row.textContent).not.toContain('trellis');
    expect(row.getAttribute('data-crew-member')).toBe('trellis');
    expect(screen.getByTestId('queue-crew-wake-trellis').getAttribute('aria-label')).toBe('Wake Trellis');
  });

  it('keeps the band when the crew is the only thing in it', () => {
    renderCrew([]);
    expect(screen.getByTestId('queue-crew-alder')).toBeTruthy();
  });

  it('still draws a bound session whose member left the roster', () => {
    renderCrew(
      [{ id: 'sess-ghost', label: 'ghost', state: 'working', desktopId: 'ws-a', crewMember: 'sable' }] as TestSession[],
      {},
      roster,
    );
    expect(screen.getByTestId('queue-crew-sable').getAttribute('data-crew-state')).toBe('awake');
  });

  it('renders no crew rows while the queue arrangement is off', () => {
    renderSidebar(sessions, false, { crew: roster });
    expect(screen.queryByTestId('queue-crew-alder')).toBeNull();
  });

  describe('arming a wake', () => {
      // Waking starts a day of a durable identity and cannot be un-rung, so an unconfirmed arm must never wake anyone.

    function armed(member: string) {
      return screen.getByTestId(`queue-crew-${member}`).getAttribute('data-crew-wake');
    }

    it('says what the second click does, and says it without the animation', () => {
      renderCrew([], { onWakeCrewMember: vi.fn() });
      const button = screen.getByTestId('queue-crew-wake-trellis');
      expect(armed('trellis')).toBeNull();

      fireEvent.click(button);

      expect(armed('trellis')).toBe('armed');
      expect(button.getAttribute('aria-label')).toBe('Wake Trellis — click again to confirm');
      expect(screen.getByTestId('queue-crew-trellis').textContent).toContain('confirm');
      expect(screen.getByTestId('queue-crew-select-trellis').getAttribute('aria-label'))
        .toBe('Wake Trellis — click again to confirm');
    });

    it('arms on the row and confirms on the sun, because they are one gesture', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-select-trellis'));
      expect(onWakeCrewMember).not.toHaveBeenCalled();
      expect(armed('trellis')).toBe('armed');

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);
      expect(armed('trellis')).toBe('breaking');
    });

    it('is not a target while it is flaring', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));

      expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);
      expect(armed('trellis')).toBe('breaking');
    });

    it('stands down when the next click lands somewhere else', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.pointerDown(document.body);
      expect(armed('trellis')).toBeNull();

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      expect(onWakeCrewMember).not.toHaveBeenCalled();
    });

    it('arms one member at a time', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.pointerDown(screen.getByTestId('queue-crew-wake-alder'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-alder'));

      expect(armed('trellis')).toBeNull();
      expect(armed('alder')).toBe('armed');
      expect(onWakeCrewMember).not.toHaveBeenCalled();
    });

    it('arms one member at a time for the keyboard too', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.focusIn(screen.getByTestId('queue-crew-wake-alder'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-alder'));

      expect(armed('trellis')).toBeNull();
      expect(armed('alder')).toBe('armed');
      expect(onWakeCrewMember).not.toHaveBeenCalled();
    });

    it('keeps the arm while focus moves inside its own row', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-select-trellis'));
      fireEvent.focusIn(screen.getByTestId('queue-crew-wake-trellis'));

      expect(armed('trellis')).toBe('armed');
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);
    });

    it('stands down on Escape', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.keyDown(document, { key: 'Escape' });

      expect(armed('trellis')).toBeNull();
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      expect(onWakeCrewMember).not.toHaveBeenCalled();
    });

    it('wakes once even where React runs the click path twice', () => {
      // StrictMode double-invokes state updaters, so sending the wake inside one would spend two days on one click.
      const onWakeCrewMember = vi.fn();
      const data = sidebarData(sessions);
      render(
        <StrictMode>
          <Sidebar
            {...baseProps}
            {...data}
            crew={roster}
            onWakeCrewMember={onWakeCrewMember}
            queue={buildQueueBands(data.desktops)}
          />
        </StrictMode>,
      );

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));

      expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);
    });

    it('stands down on its own, and wakes nobody doing it', () => {
      vi.useFakeTimers();
      try {
        const onWakeCrewMember = vi.fn();
        renderCrew([], { onWakeCrewMember });

        fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
        expect(armed('trellis')).toBe('armed');

        act(() => {
          vi.advanceTimersByTime(WAKE_ARM_TIMEOUT_MS - 1);
        });
        expect(armed('trellis')).toBe('armed');

        act(() => {
          vi.advanceTimersByTime(1);
        });
        expect(armed('trellis')).toBeNull();
        expect(onWakeCrewMember).not.toHaveBeenCalled();
      } finally {
        vi.useRealTimers();
      }
    });
  });
});
