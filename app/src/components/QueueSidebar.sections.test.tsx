import { StrictMode, type ComponentProps } from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar } from './Sidebar';
import { useAgentList } from './useAgentList';
import { focusedQueueRow } from './focusedQueueRow';
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
  it('anchors the chief above the owed turns, oldest first, and keeps the rest behind the list', () => {
    const { container } = renderSidebar(sessions, true);
    expect(queueRowIds(container)).toEqual(['queue-chief-chief', 'queue-turn-older', 'queue-turn-newer']);
    expect(screen.getByTestId('queue-waiting-card')).toHaveAttribute('data-waiting', '2');
    expect(screen.getByTestId('queue-waiting-head')).toHaveTextContent('2 waiting');
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
      [...sessions, { id: 'loose', label: 'loose', state: 'idle', desktopId: 'ws-b' }],
      true,
      { agentListOpen: true },
    );
    const where = (id: string) => screen.getByTestId(id).querySelector('.queue-lead-badge');

    expect(where('queue-turn-older')).toHaveTextContent('2');
    expect(where('queue-turn-older')).toHaveAttribute('title', 'beta');
    expect(where('queue-turn-newer')).toHaveTextContent('1');
    expect(where('queue-settled-loose')).toHaveTextContent('2');
    expect(screen.getByTestId('queue-select-loose')).toHaveAttribute('title', expect.stringContaining('beta'));
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

  it('offers settle only where a turn is owed', () => {
    renderSidebar(sessions, true, { agentListOpen: true, onSettleTurn: vi.fn() });
    expect(screen.getByTestId('queue-settle-older')).toBeTruthy();
    expect(screen.queryByTestId('queue-settle-settled')).toBeNull();
    expect(screen.queryByTestId('queue-settle-chief')).toBeNull();
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
    expect(onDesktopDragDrop).toHaveBeenCalledWith(expect.objectContaining({ id: 'ws-b' }), false);
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
  const roster = [{ id: 'alder', name: 'Alder' }, { id: 'keel', name: 'Keel' }, { id: 'trellis', name: 'Trellis' }];

  function renderCrew(
    crewSessions: TestSession[],
    overrides: Record<string, unknown> = {},
    crew: { id: string; name: string; binding_session?: string }[] = roster,
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

    const { unmount } = renderSidebar(chiefOnRoster, true, { crew: [{ id: 'keel', name: 'Keel' }] });
    const withoutChiefOnRoster = moreAgents();
    unmount();

    renderSidebar(chiefOnRoster, true, { crew: [{ id: 'alder', name: 'Alder' }, { id: 'keel', name: 'Keel' }] });
    expect(screen.queryByTestId('queue-crew-alder')).toBeNull();
    expect(screen.getByTestId('queue-chief-chief')).toBeInTheDocument();
    expect(moreAgents()).toBe(withoutChiefOnRoster);
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

  describe('arming a wake', () => {
      // Waking starts a day of a durable identity and cannot be un-rung, so an unconfirmed arm must never wake anyone.

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
  });
});
