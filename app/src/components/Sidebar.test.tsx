import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar, type DockItem } from './Sidebar';
import { type SessionDelegationRole } from '../types/generated';
import { type DesktopWithSessions } from '../utils/desktopViewModels';
import { desktopGroups, groupIndexes } from '../test/desktops';
import { useEscapeStack } from '../hooks/useEscapeStack';

interface TestSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle' | 'recoverable';
  agent?: string;
  branch?: string;
  isWorktree?: boolean;
  cwd?: string;
  endpointId?: string;
  endpointName?: string;
  endpointStatus?: string;
  chiefOfStaff?: boolean;
  delegatedFromChief?: boolean;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
  delegation_role?: SessionDelegationRole;
  automation?: import('../types/generated').AutomationProvenance;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  pullRequests?: import('../types/generated').SessionPullRequest[];
}

function buildSidebarData(sessions: TestSession[]) {
  const viewSessions = sessions.map((session) => ({
    ...session,
    desktopId: session.cwd ? `desktop-${session.cwd}` : `desktop-${session.id}`,
  }));
  const desktopIds = new Set<string>();
  const desktops = desktopGroups(
    viewSessions
      .filter((session) => {
        if (desktopIds.has(session.desktopId)) return false;
        desktopIds.add(session.desktopId);
        return true;
      })
      .map((session) => ({ id: session.desktopId, title: session.label })),
    viewSessions,
  );
  return { desktops, visualIndexByDesktopId: groupIndexes(desktops) };
}

function desktopWithBrowserTile(): DesktopWithSessions<TestSession> {
  const session: TestSession & { desktopId: string } = {
    id: 's1',
    label: 'shell',
    state: 'idle',
    desktopId: 'desktop-browser',
  };
  return desktopGroups(
    [
      {
        id: 'desktop-browser',
        title: 'browser',
        tree: {
          type: 'split',
          split_id: 'split-root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', pane_id: 'pane-s1' },
            {
              type: 'tile',
              tile_id: 'tile-browser',
              tile_kind: 'browser',
              tile_params: 'https://www.google.com',
            },
          ],
        },
      },
    ],
    [session],
  )[0];
}

const baseProps = {
  selectedId: null,
  selectedDesktopId: null,
  collapsed: false,
  surface: 'tree-open' as const,
  headerActions: [],
  dockItems: undefined as DockItem[] | undefined,
  onSelectSession: () => {},
  onSelectDesktop: () => {},
  onNewSession: () => {},
  onCloseSession: () => {},
  onReloadSession: () => {},
  onGoToDashboard: () => {},
  onToggleCollapse: () => {},
};

describe('Sidebar', () => {

  it('ends a session drag on Escape without dropping it', () => {
    const desktop = desktopWithBrowserTile();
    const onSessionDragEnd = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        desktops={[desktop]}
        visualIndexByDesktopId={new Map([[desktop.id, 0]])}
        onSessionDragStart={vi.fn()}
        onSessionDragEnd={onSessionDragEnd}
      />,
    );
    const row = screen
      .getByTestId('sidebar-session-s1')
      .querySelector('.sidebar-row-select') as HTMLButtonElement;

    fireEvent.pointerDown(row, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 40 });
    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onSessionDragEnd).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('session-drag-ghost')).not.toBeInTheDocument();
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 10, clientY: 40 });
    expect(onSessionDragEnd).toHaveBeenCalledTimes(1);
  });

  it('reorders a desktop whose first agent runs on a remote endpoint among every desktop', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a', endpointId: 'ep-1', endpointName: 'box' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
      { id: 'c1', label: 'C1', state: 'idle', cwd: '/repo/c' },
    ]);
    expect(sidebarData.desktops[0].sessions[0].endpointId).toBe('ep-1');
    const onDesktopReorder = vi.fn();
    render(<Sidebar {...baseProps} {...sidebarData} onDesktopReorder={onDesktopReorder} />);

    const header = screen
      .getByTestId('sidebar-desktop-desktop-/repo/a')
      .querySelector('.desktop-rule > .sidebar-row-select') as HTMLElement;
    fireEvent.pointerDown(header, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });
    expect(screen.getByTestId('desktop-reorder-seam-3')).toBeInTheDocument();

    fireEvent.pointerEnter(screen.getByTestId('desktop-reorder-seam-3'));
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 10, clientY: 120 });

    expect(onDesktopReorder).toHaveBeenCalledWith({
      desktopId: 'desktop-/repo/a',
      prevDesktopId: 'desktop-/repo/c',
      nextDesktopId: undefined,
    });
  });

  it('drops nothing when Escape cancels a header drag', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
      { id: 'c1', label: 'C1', state: 'idle', cwd: '/repo/c' },
    ]);
    const onDesktopReorder = vi.fn();
    render(<Sidebar {...baseProps} {...sidebarData} onDesktopReorder={onDesktopReorder} />);
    const header = screen
      .getByTestId('sidebar-desktop-desktop-/repo/a')
      .querySelector('.desktop-rule > .sidebar-row-select') as HTMLElement;

    fireEvent.pointerDown(header, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });
    fireEvent.pointerEnter(screen.getByTestId('desktop-reorder-seam-3'));
    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 10, clientY: 120 });

    expect(onDesktopReorder).not.toHaveBeenCalled();
    expect(screen.queryByTestId('desktop-reorder-seam-0')).not.toBeInTheDocument();
  });

  it('takes Escape as the top of the escape stack, leaving the surface beneath it alone', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
    ]);
    const underneath = vi.fn();
    function EscapeOwner() {
      useEscapeStack(underneath, true);
      return null;
    }
    const onDesktopReorder = vi.fn();
    render(
      <>
        <EscapeOwner />
        <Sidebar {...baseProps} {...sidebarData} onDesktopReorder={onDesktopReorder} />
      </>,
    );
    const header = screen
      .getByTestId('sidebar-desktop-desktop-/repo/a')
      .querySelector('.desktop-rule > .sidebar-row-select') as HTMLElement;

    fireEvent.pointerDown(header, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(underneath).not.toHaveBeenCalled();
    expect(screen.queryByTestId('desktop-reorder-seam-0')).not.toBeInTheDocument();

    fireEvent.pointerUp(window, { pointerId: 1 });
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(underneath).toHaveBeenCalledOnce();
    expect(onDesktopReorder).not.toHaveBeenCalled();
  });

  it('answers the next header click after Escape cancels a header drag released elsewhere', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
    ]);
    const onSelectDesktop = vi.fn();
    render(
      <Sidebar {...baseProps} {...sidebarData} onSelectDesktop={onSelectDesktop} onDesktopReorder={vi.fn()} />,
    );
    const headerOf = (cwd: string) =>
      screen
        .getByTestId(`sidebar-desktop-desktop-${cwd}`)
        .querySelector('.desktop-rule > .sidebar-row-select') as HTMLElement;

    fireEvent.pointerDown(headerOf('/repo/a'), { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });
    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 400, clientY: 400 });

    fireEvent.pointerDown(headerOf('/repo/b'), { button: 0, pointerId: 2, clientX: 10, clientY: 40 });
    fireEvent.pointerUp(window, { pointerId: 2, clientX: 10, clientY: 40 });
    fireEvent.click(headerOf('/repo/b'));

    expect(onSelectDesktop).toHaveBeenCalledTimes(1);
  });

  it('lists a desktop without agents in its slot beside the others', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
    ]);
    const emptyDesktop: DesktopWithSessions<TestSession> = {
      id: 'desktop-/repo/empty',
      title: 'empty',
      directory: '/repo/empty',
      sessions: [],
      children: [],
      firstSessionId: null,
      focusedSessionId: null,
      hasUnresolvedAgentPanes: false,
    };
    const groups = [emptyDesktop, ...sidebarData.desktops];
    render(<Sidebar {...baseProps} desktops={groups} visualIndexByDesktopId={groupIndexes(groups)} />);

    expect(screen.getByTestId('sidebar-desktop-desktop-/repo/empty')).toHaveTextContent('⌘1');
    expect(screen.getByTestId('sidebar-desktop-desktop-/repo/a')).toHaveTextContent('⌘2');
    expect(screen.getByTestId('sidebar-desktop-desktop-/repo/b')).toHaveTextContent('⌘3');
  });

  it('renames a desktop back to its default label with an empty name', async () => {
    const sessions: TestSession[] = [{ id: 's1', label: 'claude', state: 'idle', agent: 'claude' }];
    const sidebarData = buildSidebarData(sessions);
    const desktops = sidebarData.desktops.map((desktop) => ({
      ...desktop,
      title: 'Reviews',
      desktop: { name: 'Reviews', defaultLabel: 'Desktop 1' },
    }));
    const onRenameDesktop = vi.fn(async () => {});
    render(<Sidebar {...baseProps} {...sidebarData} desktops={desktops} onRenameDesktop={onRenameDesktop} />);

    fireEvent.click(screen.getByTestId('rename-desktop-desktop-s1'));
    const input = screen.getByRole('textbox') as HTMLInputElement;
    expect(input.value).toBe('Reviews');
    expect(input.placeholder).toBe('Desktop 1');
    fireEvent.change(input, { target: { value: '' } });
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });

    await waitFor(() => expect(onRenameDesktop).toHaveBeenCalledWith('desktop-s1', ''));
  });


});
