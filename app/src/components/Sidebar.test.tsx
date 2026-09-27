import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar, type DockItem } from './Sidebar';
import { BuiltinDelegationRole, type SessionDelegationRole } from '../types/generated';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { type DesktopWithSessions } from '../utils/desktopViewModels';
import { desktopGroups, groupIndexes } from '../test/desktops';
import { useEscapeStack } from '../hooks/useEscapeStack';

function sessionlessDesktop(): DesktopWithSessions<TestSession> {
  return {
    id: 'desktop-/repo/docs',
    title: 'docs',
    directory: '/repo/docs',
    sessions: [],
    children: [],
    firstSessionId: null,
    focusedSessionId: null,
    hasUnresolvedAgentPanes: false,
  };
}

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
  it('offers delegation navigation without a dispatcher subtitle in desktop rows', () => {
    const onSelectSession = vi.fn();
    const sessions: TestSession[] = [
      {
        id: 'root',
        label: 'root',
        state: 'working',
        delegation_role: { name: 'Orchestrator', builtin: BuiltinDelegationRole.Orchestrator },
      },
      {
        id: 'child',
        label: 'child',
        state: 'working',
        dispatcher_session_id: 'root',
        dispatcher_member: 'alder',
      },
    ];
    render(
      <Sidebar {...baseProps} {...buildSidebarData(sessions)} onSelectSession={onSelectSession} />,
    );

    const root = screen.getByTestId('sidebar-session-root');
    const child = screen.getByTestId('sidebar-session-child');
    expect(
      within(root).getByRole('button', { name: 'Orchestrator · Show delegation chain for root' }),
    ).toHaveAttribute('data-role', 'orchestrator');
    expect(within(child).queryByTestId('sidebar-dispatcher')).toBeNull();
    expect(
      within(child).getByRole('button', { name: 'Show delegation chain for child' }),
    ).toBeInTheDocument();
    expect(onSelectSession).not.toHaveBeenCalled();

    fireEvent.pointerEnter(child);
    expect(root).not.toHaveClass('kin-up');
    fireEvent.pointerLeave(child);
    expect(root).not.toHaveClass('kin-up');
  });

  it('shows only a non-default instance marker', () => {
    const data = buildSidebarData([]);
    const { rerender } = render(<Sidebar {...baseProps} {...data} />);
    expect(screen.queryByTestId('sidebar-instance-marker')).not.toBeInTheDocument();

    rerender(<Sidebar {...baseProps} {...data} instance="fixture-lab" />);
    expect(screen.getByTestId('sidebar-instance-marker')).toHaveTextContent('instance fixture-lab');
  });

  it('uses regular state indicator for codex sessions', () => {
    const sessions: TestSession[] = [
      {
        id: 's1',
        label: 'codex',
        state: 'working',
        agent: 'codex',
      },
    ];
    const { container } = render(<Sidebar {...baseProps} {...buildSidebarData(sessions)} />);
    expect(container.querySelector('.state-indicator--working')).toBeTruthy();
    expect(container.querySelector('.state-indicator--unknown')).toBeFalsy();
  });

  it('shows automation name and PR number without requiring hover', () => {
    const sessions: TestSession[] = [
      {
        id: 's1',
        label: 'feed-nexus-web',
        state: 'working',
        agent: 'codex',
        automation: {
          run_id: 'run-1',
          definition_id: 'review-sol',
          definition_name: 'Requested PR review - GPT Sol medium',
          trigger_type: 'github_review_requested',
          pull_request: {
            repository: 'ghe.spotify.net/audiobook-feed-mgmt/feed-nexus-web',
            number: 101,
            url: 'https://ghe.spotify.net/audiobook-feed-mgmt/feed-nexus-web/pull/101',
            title: 'Fix validation race',
            head_sha: '82f1c7a000000000000000000000000000000000',
          },
        },
      },
    ];

    render(<Sidebar {...baseProps} {...buildSidebarData(sessions)} />);
    fireEvent.click(screen.getByTestId('sidebar-automation-header-review-sol'));

    const row = screen.getByTestId('sidebar-session-s1');
    expect(row).toHaveTextContent('feed-nexus-web');
    expect(row).toHaveTextContent('GPT Sol medium');
    expect(row).toHaveTextContent('#101');
  });

  describe('session pull request', () => {
    const pr = (
      number: number,
      state: string,
      extra: Partial<import('../types/generated').SessionPullRequest> = {},
    ): import('../types/generated').SessionPullRequest => ({
      repository: 'github.com/victorarias/attn',
      number,
      url: `https://github.com/victorarias/attn/pull/${number}`,
      created_at: '2026-08-30T10:00:00Z',
      state,
      ...extra,
    });

    function renderRow(
      pullRequests: import('../types/generated').SessionPullRequest[],
      label = 'ledger sweep',
    ) {
      const sessions: TestSession[] = [
        { id: 's1', label, state: 'working', agent: 'claude', pullRequests },
      ];
      render(<Sidebar {...baseProps} {...buildSidebarData(sessions)} />);
      return screen.getByTestId('sidebar-session-s1');
    }

    it('shows the newest open pull request beside the name, not under it', () => {
      const row = renderRow([pr(71, 'open', { ci_status: 'failure' })]);

      const headline = row.querySelector('.sidebar-session-headline');
      const entry = headline?.querySelector('.sidebar-session-pr');
      expect(entry).toBeTruthy();
      expect(headline?.querySelector('.session-label')).toBeTruthy();
      expect(entry).toHaveTextContent('#71');
      expect(entry).toHaveAttribute('data-tone', 'bad');
      expect(entry).toHaveAttribute('title', 'github.com/victorarias/attn#71 · checks failed');
    });

    it('keeps the status out of the row and in the tooltip, so the name keeps its width', () => {
      const row = renderRow([pr(71, 'open', { review_status: 'changes_requested' })]);

      const entry = row.querySelector('.sidebar-session-pr');
      expect(entry?.textContent).toBe('#71');
      expect(entry).toHaveAttribute(
        'aria-label',
        'github.com/victorarias/attn#71 · changes requested',
      );
    });

    it('prefers an open pull request over a merged one', () => {
      const row = renderRow([pr(72, 'open'), pr(71, 'merged')]);

      const entries = row.querySelectorAll('.sidebar-session-pr');
      expect(entries).toHaveLength(1);
      expect(entries[0]).toHaveTextContent('#72');
    });

    it('keeps a merged pull request when no open one is left', () => {
      const row = renderRow([pr(71, 'merged')]);

      const entry = row.querySelector('.sidebar-session-pr');
      expect(entry).toHaveTextContent('#71');
      expect(entry).toHaveAttribute('data-tone', 'merged');
      expect(entry).toHaveAttribute('title', 'github.com/victorarias/attn#71 · merged');
    });

    it('shows nothing for a session whose only pull request is closed', () => {
      const row = renderRow([pr(71, 'closed')]);

      expect(row.querySelector('.sidebar-session-pr')).toBeNull();
    });

    it('shows nothing for a session that opened no pull request', () => {
      const row = renderRow([]);

      expect(row.querySelector('.sidebar-session-pr')).toBeNull();
    });

    it('keeps the whole number beside a long name, which is the half that truncates', () => {
      const row = renderRow(
        [pr(71, 'open', { ci_status: 'pending' })],
        'delegate: rebuild the entire attention ledger projection pipeline',
      );

      const label = row.querySelector('.sidebar-session-headline > .session-label');
      const entry = row.querySelector('.sidebar-session-pr');
      expect(label).toHaveTextContent(
        'delegate: rebuild the entire attention ledger projection pipeline',
      );
      expect(entry?.textContent).toBe('#71');
      expect(entry?.getAttribute('title')).toBe('github.com/victorarias/attn#71 · checks running');
    });
  });

  it('groups every session from one automation below ordinary sessions and starts collapsed', () => {
    const automation = {
      run_id: 'run-1',
      definition_id: 'review-sol',
      definition_name: 'Requested PR review - GPT Sol medium',
      trigger_type: 'github_review_requested',
    };
    const sessions: TestSession[] = [
      { id: 'manual', label: 'manual', state: 'working', cwd: '/repo/manual' },
      { id: 'run-a', label: 'review A', state: 'idle', cwd: '/repo/a', automation },
      {
        id: 'run-b',
        label: 'review B',
        state: 'working',
        cwd: '/repo/b',
        automation: { ...automation, run_id: 'run-2' },
      },
    ];

    render(<Sidebar {...baseProps} {...buildSidebarData(sessions)} />);

    const group = screen.getByTestId('sidebar-automation-review-sol');
    const header = screen.getByTestId('sidebar-automation-header-review-sol');
    expect(header).toHaveAttribute('aria-expanded', 'false');
    expect(header).toHaveTextContent('Requested PR review - GPT Sol medium');
    expect(group).toHaveAttribute('data-runs', '2');
    expect(screen.getByTestId('sidebar-automation-runs')).toHaveTextContent('Automations2 runs');
    expect(screen.queryByTestId('sidebar-runs-needing-you')).toBeNull();
    expect(screen.getByTestId('sidebar-session-manual')).toBeInTheDocument();
    expect(screen.queryByTestId('sidebar-session-run-a')).toBeNull();
    expect(screen.queryByTestId('sidebar-desktop-desktop-/repo/a')).toBeNull();
    expect(screen.getByTestId('sidebar-session-manual').compareDocumentPosition(group)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );

    fireEvent.click(header);

    expect(header).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByTestId('sidebar-session-run-b').compareDocumentPosition(
      screen.getByTestId('sidebar-session-run-a'),
    )).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it('flags runs that stopped with a question, and settles or walks them from the sidebar', () => {
    const automation = {
      run_id: 'run-1',
      definition_id: 'review-sol',
      definition_name: 'Requested PR review - GPT Sol medium',
      trigger_type: 'github_review_requested',
    };
    const sessions: TestSession[] = [
      { id: 'run-a', label: 'review A', state: 'working', cwd: '/repo/a', automation },
      {
        id: 'run-b',
        label: 'review B',
        state: 'waiting_input',
        cwd: '/repo/b',
        turnOwed: true,
        turnOpenedAt: '2026-09-26T09:00:00Z',
        automation: { ...automation, run_id: 'run-2' },
      },
    ];
    const onSettleTurn = vi.fn();
    const onWalkRuns = vi.fn();

    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
        onSettleTurn={onSettleTurn}
        onWalkRuns={onWalkRuns}
      />,
    );

    expect(screen.getByTestId('sidebar-automation-review-sol')).toHaveAttribute('data-needing', '1');
    expect(screen.getByTestId('sidebar-automation-header-review-sol')).toHaveTextContent(/GPT Sol medium12$/);
    const batch = screen.getByTestId('sidebar-runs-needing-you');
    expect(batch).toHaveTextContent(`1 run needs you${formatShortcut('session.nextRun')}`);
    fireEvent.click(batch);
    expect(onWalkRuns).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId('sidebar-automation-header-review-sol'));
    expect(screen.queryByTestId('session-settle-run-a')).toBeNull();
    fireEvent.click(screen.getByTestId('session-settle-run-b'));
    expect(onSettleTurn).toHaveBeenCalledWith('run-b');
  });

  it('opens the group of a run when that run is selected', () => {
    const sessions: TestSession[] = [
      { id: 'manual', label: 'manual', state: 'working', cwd: '/repo/manual' },
      {
        id: 'run-a',
        label: 'review A',
        state: 'idle',
        cwd: '/repo/a',
        automation: {
          run_id: 'run-1',
          definition_id: 'review-sol',
          definition_name: 'Requested PR review - GPT Sol medium',
          trigger_type: 'github_review_requested',
        },
      },
    ];
    const data = buildSidebarData(sessions);
    const { rerender } = render(<Sidebar {...baseProps} {...data} selectedId="manual" />);
    expect(screen.getByTestId('sidebar-automation-header-review-sol')).toHaveAttribute('aria-expanded', 'false');

    rerender(<Sidebar {...baseProps} {...data} selectedId="run-a" />);
    const header = screen.getByTestId('sidebar-automation-header-review-sol');
    expect(header).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(header);
    expect(header).toHaveAttribute('aria-expanded', 'false');

    rerender(<Sidebar {...baseProps} {...data} selectedId="run-a" />);
    expect(header).toHaveAttribute('aria-expanded', 'false');

    rerender(<Sidebar {...baseProps} {...data} selectedId="run-a" selectionRequest={{ sessionId: 'run-a' }} />);
    expect(header).toHaveAttribute('aria-expanded', 'true');
  });

  it('shows waiting badge in collapsed sidebar', () => {
    const sessions: TestSession[] = [
      {
        id: 's1',
        label: 'codex',
        state: 'waiting_input',
        agent: 'codex',
      },
    ];
    const { container } = render(
      <Sidebar {...baseProps} collapsed {...buildSidebarData(sessions)} />,
    );
    expect(container.querySelector('.mini-badge.unknown')).toBeFalsy();
    expect(container.querySelector('.mini-badge')).toBeTruthy();
    expect(screen.queryByText('?')).not.toBeInTheDocument();
  });

  it('fires reload callback when reload button is clicked', () => {
    const sessions: TestSession[] = [
      {
        id: 's1',
        label: 'claude',
        state: 'idle',
        agent: 'claude',
      },
    ];
    const onReloadSession = vi.fn();
    render(
      <Sidebar {...baseProps} onReloadSession={onReloadSession} {...buildSidebarData(sessions)} />,
    );

    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('reload-session-action'));
    expect(onReloadSession).toHaveBeenCalledWith('s1');
  });

  it('fires close callback when close button is clicked', () => {
    const sessions: TestSession[] = [
      {
        id: 's1',
        label: 'claude',
        state: 'idle',
        agent: 'claude',
      },
    ];
    const onCloseSession = vi.fn();
    render(
      <Sidebar {...baseProps} onCloseSession={onCloseSession} {...buildSidebarData(sessions)} />,
    );

    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('close-session-action'));
    expect(onCloseSession).toHaveBeenCalledWith('s1');
  });

  it('renders browser tiles in layout order and exposes tile actions', () => {
    const desktop = desktopWithBrowserTile();
    const onSelectTile = vi.fn();
    const onCloseTile = vi.fn();
    const onReloadTile = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        desktops={[desktop]}
        visualIndexByDesktopId={new Map([[desktop.id, 0]])}
        onSelectTile={onSelectTile}
        onCloseTile={onCloseTile}
        onReloadTile={onReloadTile}
      />,
    );

    const tile = screen.getByTestId('sidebar-tile-desktop-browser-tile-browser');
    expect(tile).toHaveTextContent('www.google.com');
    expect(
      screen.getByTestId('sidebar-session-s1').compareDocumentPosition(tile) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();

    fireEvent.click(within(tile).getByRole('button', { name: 'Open www.google.com' }));
    expect(onSelectTile).toHaveBeenCalledWith('desktop-browser', 'tile-browser');

    fireEvent.click(screen.getByTestId('reload-tile-desktop-browser-tile-browser'));
    expect(onReloadTile).toHaveBeenCalledWith('desktop-browser', 'tile-browser');

    fireEvent.click(screen.getByTestId('close-tile-desktop-browser-tile-browser'));
    expect(onCloseTile).toHaveBeenCalledWith('desktop-browser', 'tile-browser');
  });

  it('marks same-endpoint desktop rows as leaf drag targets', () => {
    const sessions: TestSession[] = [
      { id: 's1', label: 'source', state: 'idle', cwd: '/repo/source' },
      { id: 's2', label: 'target', state: 'idle', cwd: '/repo/target' },
    ];
    const onDesktopDragEnter = vi.fn();
    const onDesktopDragDrop = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
        leafDrag={{ sourceDesktopId: 'desktop-/repo/source' }}
        dragHoverDesktopId="desktop-/repo/target"
        onDesktopDragEnter={onDesktopDragEnter}
        onDesktopDragDrop={onDesktopDragDrop}
      />,
    );

    const source = screen.getByTestId('sidebar-desktop-desktop-/repo/source');
    const target = screen.getByTestId('sidebar-desktop-desktop-/repo/target');
    expect(source).toHaveClass('desktop-group--drag-disabled');
    expect(target).toHaveClass('desktop-group--drag-entering');

    fireEvent.pointerEnter(target);
    fireEvent.pointerUp(target);
    expect(onDesktopDragEnter).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'desktop-/repo/target' }),
    );
    expect(onDesktopDragDrop).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'desktop-/repo/target' }),
    );
  });

  it('shows the new-desktop drop-zone only during a leaf drag and splits on drop', () => {
    const sessions: TestSession[] = [
      { id: 's1', label: 'source', state: 'idle', cwd: '/repo/source' },
      { id: 's2', label: 'target', state: 'idle', cwd: '/repo/target' },
    ];
    const onNewDesktopDrop = vi.fn();
    const { rerender } = render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
        onNewDesktopDrop={onNewDesktopDrop}
      />,
    );

    expect(screen.queryByTestId('new-desktop-dropzone')).not.toBeInTheDocument();

    rerender(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
        leafDrag={{ sourceDesktopId: 'desktop-/repo/source' }}
        onNewDesktopDrop={onNewDesktopDrop}
      />,
    );

    const zone = screen.getByTestId('new-desktop-dropzone');
    expect(zone).toBeInTheDocument();
    expect(zone).not.toHaveClass('new-desktop-dropzone--active');

    fireEvent.pointerEnter(zone);
    expect(zone).toHaveClass('new-desktop-dropzone--active');

    fireEvent.pointerUp(zone);
    expect(onNewDesktopDrop).toHaveBeenCalledTimes(1);
  });

  it('starts a leaf drag when a session row is dragged out of the sidebar', () => {
    const desktop = desktopWithBrowserTile();
    const onSessionDragStart = vi.fn();
    const onSessionDragEnd = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        desktops={[desktop]}
        visualIndexByDesktopId={new Map([[desktop.id, 0]])}
        onSessionDragStart={onSessionDragStart}
        onSessionDragEnd={onSessionDragEnd}
      />,
    );

    const row = screen
      .getByTestId('sidebar-session-s1')
      .querySelector('.sidebar-row-select') as HTMLButtonElement;
    expect(screen.queryByTestId('session-drag-ghost')).not.toBeInTheDocument();

    // The leaf id is the session's layout pane id, not the session id.
    fireEvent.pointerDown(row, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    expect(onSessionDragStart).not.toHaveBeenCalled();

    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 40 });
    expect(onSessionDragStart).toHaveBeenCalledWith('desktop-browser', undefined, 'pane-s1');
    expect(screen.getByTestId('session-drag-ghost')).toBeInTheDocument();
    expect(screen.getByTestId('sidebar-session-s1')).toHaveClass('session-item--dragging');

    fireEvent.pointerUp(window, { pointerId: 1, clientX: 10, clientY: 40 });
    expect(onSessionDragEnd).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('session-drag-ghost')).not.toBeInTheDocument();
  });

  it.each([false, true])('cleans up an unmounted session drag (armed=%s)', (armed) => {
    const desktop = desktopWithBrowserTile();
    const onSessionDragStart = vi.fn();
    const onSessionDragEnd = vi.fn();
    const { unmount } = render(
      <Sidebar
        {...baseProps}
        desktops={[desktop]}
        visualIndexByDesktopId={new Map([[desktop.id, 0]])}
        onSessionDragStart={onSessionDragStart}
        onSessionDragEnd={onSessionDragEnd}
      />,
    );
    const row = screen
      .getByTestId('sidebar-session-s1')
      .querySelector('.sidebar-row-select') as HTMLButtonElement;
    fireEvent.pointerDown(row, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    if (armed) fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 40 });
    unmount();
    expect(onSessionDragEnd).toHaveBeenCalledTimes(armed ? 1 : 0);
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });
    fireEvent.pointerUp(window, { pointerId: 1 });
    expect(onSessionDragStart).toHaveBeenCalledTimes(armed ? 1 : 0);
    expect(onSessionDragEnd).toHaveBeenCalledTimes(armed ? 1 : 0);
  });

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

  it('treats a sub-threshold press on a session row as a plain selection click', () => {
    const desktop = desktopWithBrowserTile();
    const onSessionDragStart = vi.fn();
    const onSelectSession = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        desktops={[desktop]}
        visualIndexByDesktopId={new Map([[desktop.id, 0]])}
        onSelectSession={onSelectSession}
        onSessionDragStart={onSessionDragStart}
      />,
    );

    const row = screen
      .getByTestId('sidebar-session-s1')
      .querySelector('.sidebar-row-select') as HTMLButtonElement;
    fireEvent.pointerDown(row, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 11, clientY: 12 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 11, clientY: 12 });
    fireEvent.click(row);

    expect(onSessionDragStart).not.toHaveBeenCalled();
    expect(onSelectSession).toHaveBeenCalledWith('s1');
  });

  it('reorders a desktop by dragging its header onto an insertion seam', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
      { id: 'c1', label: 'C1', state: 'idle', cwd: '/repo/c' },
    ]);
    const onDesktopReorder = vi.fn();
    const onSelectDesktop = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...sidebarData}
        onSelectDesktop={onSelectDesktop}
        onDesktopReorder={onDesktopReorder}
      />,
    );

    const sourceGroup = screen.getByTestId('sidebar-desktop-desktop-/repo/a');
    const header = sourceGroup.querySelector(
      '.desktop-group-header > .sidebar-row-select',
    ) as HTMLElement;

    expect(screen.queryByTestId('desktop-reorder-seam-0')).not.toBeInTheDocument();

    fireEvent.pointerDown(header, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });

    const seam2 = screen.getByTestId('desktop-reorder-seam-2');
    expect(seam2).toBeInTheDocument();
    expect(screen.getByTestId('desktop-reorder-seam-3')).toBeInTheDocument();

    fireEvent.pointerEnter(seam2);
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 10, clientY: 120 });

    expect(onDesktopReorder).toHaveBeenCalledWith({
      desktopId: 'desktop-/repo/a',
      prevDesktopId: 'desktop-/repo/b',
      nextDesktopId: 'desktop-/repo/c',
    });
    expect(onSelectDesktop).not.toHaveBeenCalled();
  });

  it('reorders a desktop whose first agent runs on a remote endpoint among every desktop', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a', endpointId: 'ep-1', endpointName: 'box' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
      { id: 'c1', label: 'C1', state: 'idle', cwd: '/repo/c' },
    ]);
    expect(sidebarData.desktops[0].endpointId).toBe('ep-1');
    const onDesktopReorder = vi.fn();
    render(<Sidebar {...baseProps} {...sidebarData} onDesktopReorder={onDesktopReorder} />);

    const header = screen
      .getByTestId('sidebar-desktop-desktop-/repo/a')
      .querySelector('.desktop-group-header > .sidebar-row-select') as HTMLElement;
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

  it('releases an armed desktop reorder on unmount without dropping', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
    ]);
    const onDesktopReorder = vi.fn();
    const { unmount } = render(
      <Sidebar {...baseProps} {...sidebarData} onDesktopReorder={onDesktopReorder} />,
    );
    const header = screen
      .getByTestId('sidebar-desktop-desktop-/repo/a')
      .querySelector('.sidebar-row-select') as HTMLButtonElement;
    header.setPointerCapture = vi.fn();
    header.releasePointerCapture = vi.fn();
    fireEvent.pointerDown(header, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });
    expect(header.setPointerCapture).toHaveBeenCalledWith(1);
    unmount();
    expect(header.releasePointerCapture).toHaveBeenCalledWith(1);
    fireEvent.pointerUp(window, { pointerId: 1 });
    expect(onDesktopReorder).not.toHaveBeenCalled();
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
      .querySelector('.desktop-group-header > .sidebar-row-select') as HTMLElement;

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
      .querySelector('.desktop-group-header > .sidebar-row-select') as HTMLElement;

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
        .querySelector('.desktop-group-header > .sidebar-row-select') as HTMLElement;

    fireEvent.pointerDown(headerOf('/repo/a'), { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });
    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 400, clientY: 400 });

    fireEvent.pointerDown(headerOf('/repo/b'), { button: 0, pointerId: 2, clientX: 10, clientY: 40 });
    fireEvent.pointerUp(window, { pointerId: 2, clientX: 10, clientY: 40 });
    fireEvent.click(headerOf('/repo/b'));

    expect(onSelectDesktop).toHaveBeenCalledTimes(1);
  });

  it('cancels the preceding pointer gesture when another header is pressed', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
    ]);
    render(<Sidebar {...baseProps} {...sidebarData} onDesktopReorder={vi.fn()} />);
    const first = screen
      .getByTestId('sidebar-desktop-desktop-/repo/a')
      .querySelector('.sidebar-row-select') as HTMLButtonElement;
    const second = screen
      .getByTestId('sidebar-desktop-desktop-/repo/b')
      .querySelector('.sidebar-row-select') as HTMLButtonElement;
    first.setPointerCapture = vi.fn();
    second.setPointerCapture = vi.fn();
    fireEvent.pointerDown(first, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerDown(second, { button: 0, pointerId: 2, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 2, clientX: 10, clientY: 80 });
    expect(first.setPointerCapture).not.toHaveBeenCalled();
    expect(second.setPointerCapture).toHaveBeenCalledWith(2);
  });

  it('treats a sub-threshold header press as a plain selection click', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
    ]);
    const onDesktopReorder = vi.fn();
    const onSelectDesktop = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...sidebarData}
        onSelectDesktop={onSelectDesktop}
        onDesktopReorder={onDesktopReorder}
      />,
    );

    const header = screen
      .getByTestId('sidebar-desktop-desktop-/repo/a')
      .querySelector('.desktop-group-header > .sidebar-row-select') as HTMLElement;

    fireEvent.pointerDown(header, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 12, clientY: 11 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 12, clientY: 11 });
    fireEvent.click(header);

    expect(onDesktopReorder).not.toHaveBeenCalled();
    expect(onSelectDesktop).toHaveBeenCalledWith('desktop-/repo/a');
    expect(screen.queryByTestId('desktop-reorder-seam-0')).not.toBeInTheDocument();
  });

  it('shows the delegated-from-chief badge only on sessions delegated from the chief', () => {
    const sessions: TestSession[] = [
      { id: 's1', label: 'delegated', state: 'working', agent: 'claude', delegatedFromChief: true },
      { id: 's2', label: 'self-started', state: 'working', agent: 'claude' },
    ];
    render(<Sidebar {...baseProps} {...buildSidebarData(sessions)} />);

    const badges = screen.getAllByLabelText('Delegated from chief of staff');
    expect(badges).toHaveLength(1);
    expect(screen.getByTestId('sidebar-session-s1')).toContainElement(badges[0]);
    expect(screen.getByTestId('sidebar-session-s2')).not.toContainElement(badges[0]);
  });

  it('renders desktop shortcuts in desktop visual order', () => {
    const sessions: TestSession[] = [
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
      { id: 'a2', label: 'A2', state: 'idle', cwd: '/repo/a' },
    ];
    render(<Sidebar {...baseProps} {...buildSidebarData(sessions)} />);

    expect(screen.getByTestId('sidebar-desktop-desktop-/repo/a')).toHaveTextContent('⌘1');
    expect(screen.getByTestId('sidebar-desktop-desktop-/repo/b')).toHaveTextContent('⌘2');
    expect(screen.getByTestId('sidebar-session-a1')).not.toHaveTextContent('⌘1');
  });

  it('hides empty desktops without renumbering the slots of the others', () => {
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

    expect(screen.queryByTestId('sidebar-desktop-desktop-/repo/empty')).not.toBeInTheDocument();
    expect(screen.getByTestId('sidebar-desktop-desktop-/repo/a')).toHaveTextContent('⌘2');
    expect(screen.getByTestId('sidebar-desktop-desktop-/repo/b')).toHaveTextContent('⌘3');
  });

  it('hides sessionless desktops by default and reveals them when showSessionless is set', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
    ]);
    const all = [...sidebarData.desktops, sessionlessDesktop()];
    const indexMap = new Map(all.map((desktop, index) => [desktop.id, index]));

    const { rerender } = render(
      <Sidebar
        {...baseProps}
        desktops={all}
        visualIndexByDesktopId={indexMap}
      />,
    );
    expect(screen.queryByTestId('sidebar-desktop-desktop-/repo/docs')).not.toBeInTheDocument();

    rerender(
      <Sidebar
        {...baseProps}
        desktops={all}
        visualIndexByDesktopId={indexMap}
        showSessionless
      />,
    );
    expect(screen.getByTestId('sidebar-desktop-desktop-/repo/docs')).toBeInTheDocument();
  });

  it('marks sessionless desktops with a neutral indicator instead of a state dot', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'working', cwd: '/repo/a' },
    ]);
    const all = [...sidebarData.desktops, sessionlessDesktop()];
    render(
      <Sidebar
        {...baseProps}
        desktops={all}
        visualIndexByDesktopId={new Map(all.map((desktop, index) => [desktop.id, index]))}
        showSessionless
      />,
    );

    const sessionlessGroup = screen.getByTestId('sidebar-desktop-desktop-/repo/docs');
    expect(sessionlessGroup.querySelector('.desktop-neutral-indicator')).toBeTruthy();
    expect(sessionlessGroup.querySelector('.state-indicator')).toBeFalsy();

    const sessionGroup = screen.getByTestId('sidebar-desktop-desktop-/repo/a');
    expect(sessionGroup.querySelector('.state-indicator')).toBeTruthy();
    expect(sessionGroup.querySelector('.desktop-neutral-indicator')).toBeFalsy();
  });

  it('invokes onToggleShowSessionless when the tile-only switch is clicked', () => {
    const onToggleShowSessionless = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData([])}
        onToggleShowSessionless={onToggleShowSessionless}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sidebar settings' }));
    fireEvent.click(screen.getByTestId('toggle-show-sessionless'));

    expect(onToggleShowSessionless).toHaveBeenCalledTimes(1);
  });

  it('reflects and toggles queue mode from the display popover', () => {
    const onToggleQueueMode = vi.fn();
    const { rerender } = render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData([])}
        queueModeEnabled={false}
        onToggleQueueMode={onToggleQueueMode}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sidebar settings' }));
    const toggle = screen.getByTestId('toggle-queue-mode');
    expect(toggle).toHaveAttribute('role', 'switch');
    expect(toggle).toHaveAttribute('aria-checked', 'false');

    fireEvent.click(toggle);
    expect(onToggleQueueMode).toHaveBeenCalledTimes(1);

    rerender(
      <Sidebar
        {...baseProps}
        {...buildSidebarData([])}
        queueModeEnabled
        onToggleQueueMode={onToggleQueueMode}
      />,
    );
    expect(screen.getByTestId('toggle-queue-mode')).toHaveAttribute('aria-checked', 'true');
  });

  it('keeps crew queue participation beside queue mode and off by default', () => {
    const onToggleCrewQueue = vi.fn();
    const { rerender } = render(
      <Sidebar {...baseProps} {...buildSidebarData([])} onToggleCrewQueue={onToggleCrewQueue} />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sidebar settings' }));
    const toggle = screen.getByTestId('toggle-crew-queue');
    expect(toggle).toHaveAttribute('aria-checked', 'false');
    fireEvent.click(toggle);
    expect(onToggleCrewQueue).toHaveBeenCalledTimes(1);

    rerender(
      <Sidebar
        {...baseProps}
        {...buildSidebarData([])}
        crewQueueEnabled
        onToggleCrewQueue={onToggleCrewQueue}
      />,
    );
    expect(screen.getByTestId('toggle-crew-queue')).toHaveAttribute('aria-checked', 'true');
  });

  it('reflects and toggles harness logos from the display popover', () => {
    const onToggleHarnessLogos = vi.fn();
    const { rerender } = render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData([])}
        onToggleHarnessLogos={onToggleHarnessLogos}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sidebar settings' }));
    const toggle = screen.getByTestId('toggle-harness-logos');
    expect(toggle).toHaveAttribute('aria-checked', 'true');
    fireEvent.click(toggle);
    expect(onToggleHarnessLogos).toHaveBeenCalledTimes(1);

    rerender(
      <Sidebar
        {...baseProps}
        {...buildSidebarData([])}
        harnessLogosEnabled={false}
        onToggleHarnessLogos={onToggleHarnessLogos}
      />,
    );
    expect(screen.getByTestId('toggle-harness-logos')).toHaveAttribute('aria-checked', 'false');
  });

  it('keeps display options visible after selecting a mode', () => {
    render(<Sidebar {...baseProps} {...buildSidebarData([])} />);

    fireEvent.click(screen.getByRole('button', { name: 'Sidebar settings' }));
    fireEvent.click(screen.getByRole('button', { name: 'tight' }));

    expect(screen.getByRole('dialog', { name: 'Sidebar settings' })).toBeInTheDocument();
  });

  it('selects the desktop tile-focus treatment from display settings', () => {
    const onDesktopSelectionStyleChange = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData([])}
        desktopSelectionStyle="rail"
        onDesktopSelectionStyleChange={onDesktopSelectionStyleChange}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sidebar settings' }));

    expect(screen.getByRole('button', { name: 'rail' })).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'dim' }));

    expect(onDesktopSelectionStyleChange).toHaveBeenCalledWith('dim');
  });

  it('renders config-driven dock items, marks active ones, and fires actions on click', () => {
    const onAttention = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        dockItems={[
          { id: 'dock.attention', label: 'attention', keys: '⌘⇧P', onClick: onAttention },
          { id: 'terminal.toggleZoom', label: 'zoom', keys: '⌘⇧Z', active: true },
          { id: 'session.toggleSidebar', label: 'sidebar', keys: '⌘⇧B' },
        ]}
        {...buildSidebarData([])}
      />,
    );

    const attention = screen.getByRole('button', { name: /attention/ });
    expect(attention).toBeInTheDocument();
    fireEvent.click(attention);
    expect(onAttention).toHaveBeenCalledTimes(1);

    expect(screen.getByText('zoom')).toBeInTheDocument();
    expect(screen.getByText('zoom').closest('.shortcut-hint')).toHaveAttribute(
      'data-active',
      'true',
    );
    expect(screen.getByText('sidebar')).toBeInTheDocument();
    expect(screen.getByText('⌘⇧B')).toBeInTheDocument();
  });

  it('hides dock items behind the collapse toggle and toggles via the header button', () => {
    const onToggle = vi.fn();
    const { rerender } = render(
      <Sidebar
        {...baseProps}
        dockItems={[{ id: 'dock.attention', label: 'attention', keys: '⌘⇧P' }]}
        dockCollapsed={false}
        onToggleDockCollapsed={onToggle}
        {...buildSidebarData([])}
      />,
    );
    expect(screen.getByText('attention')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Hide dock' }));
    expect(onToggle).toHaveBeenCalledTimes(1);

    rerender(
      <Sidebar
        {...baseProps}
        dockItems={[{ id: 'dock.attention', label: 'attention', keys: '⌘⇧P' }]}
        dockCollapsed={true}
        onToggleDockCollapsed={onToggle}
        {...buildSidebarData([])}
      />,
    );
    expect(screen.queryByText('attention')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Show dock' })).toBeInTheDocument();
  });

  it('shows endpoint badge and renders actions for remote sessions', () => {
    const sessions: TestSession[] = [
      {
        id: 'remote-1',
        label: 'codex',
        state: 'idle',
        endpointId: 'ep-1',
        endpointName: 'gpu-box',
        endpointStatus: 'connected',
      },
    ];
    render(<Sidebar {...baseProps} {...buildSidebarData(sessions)} />);

    expect(screen.getAllByText('gpu-box')).toHaveLength(2);
    fireEvent.click(screen.getByTestId('session-actions-remote-1'));
    expect(screen.getByTestId('close-session-action')).toBeInTheDocument();
    expect(screen.getByTestId('reload-session-action')).toBeInTheDocument();
  });

  it('renames a session through the pencil trigger and popover', async () => {
    const sessions: TestSession[] = [{ id: 's1', label: 'claude', state: 'idle', agent: 'claude' }];
    const onRenameSession = vi.fn(async () => {});
    render(
      <Sidebar {...baseProps} onRenameSession={onRenameSession} {...buildSidebarData(sessions)} />,
    );

    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('rename-session-action'));
    const input = screen.getByRole('textbox') as HTMLInputElement;
    expect(input.value).toBe('claude');
    fireEvent.change(input, { target: { value: 'renamed-session' } });
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });

    await waitFor(() => expect(onRenameSession).toHaveBeenCalledWith('s1', 'renamed-session'));
  });

  it('renames a desktop through the pencil trigger and popover', async () => {
    const sessions: TestSession[] = [{ id: 's1', label: 'claude', state: 'idle', agent: 'claude' }];
    const onRenameDesktop = vi.fn(async () => {});
    render(
      <Sidebar
        {...baseProps}
        onRenameDesktop={onRenameDesktop}
        {...buildSidebarData(sessions)}
      />,
    );

    fireEvent.click(screen.getByTestId('rename-desktop-desktop-s1'));
    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'renamed-desktop' } });
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });

    await waitFor(() =>
      expect(onRenameDesktop).toHaveBeenCalledWith('desktop-s1', 'renamed-desktop'),
    );
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

  it('neither renames nor reorders the agents that are not on a desktop', () => {
    const sessions = [
      { id: 'a1', label: 'A1', state: 'idle' as const, desktopId: 'desktop-a' },
      { id: 'b1', label: 'B1', state: 'idle' as const, desktopId: 'desktop-b' },
      { id: 'loose', label: 'Loose', state: 'idle' as const },
    ];
    const desktops = desktopGroups(
      [{ id: 'desktop-a', title: 'A' }, { id: 'desktop-b', title: 'B' }],
      sessions,
    );
    const onDesktopReorder = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        desktops={desktops}
        visualIndexByDesktopId={groupIndexes(desktops)}
        onRenameDesktop={vi.fn(async () => {})}
        onDesktopReorder={onDesktopReorder}
      />,
    );

    const loose = screen.getByTestId('sidebar-desktop-unplaced');
    expect(within(loose).getByText('Not on a desktop')).toBeInTheDocument();
    expect(screen.queryByTestId('rename-desktop-unplaced')).not.toBeInTheDocument();
    expect(screen.getByTestId('rename-desktop-desktop-a')).toBeInTheDocument();

    const header = loose.querySelector('.desktop-group-header > .sidebar-row-select') as HTMLElement;
    fireEvent.pointerDown(header, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });
    expect(screen.queryByTestId('desktop-reorder-seam-0')).not.toBeInTheDocument();

    const aHeader = screen
      .getByTestId('sidebar-desktop-desktop-a')
      .querySelector('.desktop-group-header > .sidebar-row-select') as HTMLElement;
    fireEvent.pointerDown(aHeader, { button: 0, pointerId: 2, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 2, clientX: 10, clientY: 80 });
    expect(screen.getByTestId('desktop-reorder-seam-2')).toBeInTheDocument();
    expect(screen.queryByTestId('desktop-reorder-seam-3')).not.toBeInTheDocument();
    fireEvent.pointerEnter(screen.getByTestId('desktop-reorder-seam-2'));
    fireEvent.pointerUp(window, { pointerId: 2, clientX: 10, clientY: 120 });

    expect(onDesktopReorder).toHaveBeenCalledWith({
      desktopId: 'desktop-a',
      prevDesktopId: 'desktop-b',
      nextDesktopId: undefined,
    });
  });

  it('shows the chief role and requests removal from the session menu', () => {
    const sessions: TestSession[] = [
      {
        id: 's1',
        label: 'coordinator',
        state: 'working',
        chiefOfStaff: true,
      },
    ];
    const onChangeChiefOfStaff = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        onChangeChiefOfStaff={onChangeChiefOfStaff}
        {...buildSidebarData(sessions)}
      />,
    );

    expect(screen.getByLabelText('Chief of staff')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('chief-of-staff-session-action'));

    expect(onChangeChiefOfStaff).toHaveBeenCalledWith('s1', false);
  });

  it('requests promotion from the session menu', () => {
    const sessions: TestSession[] = [{ id: 's1', label: 'worker', state: 'idle' }];
    const onChangeChiefOfStaff = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        onChangeChiefOfStaff={onChangeChiefOfStaff}
        {...buildSidebarData(sessions)}
      />,
    );

    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('chief-of-staff-session-action'));

    expect(onChangeChiefOfStaff).toHaveBeenCalledWith('s1', true);
  });
});

describe('Sidebar home row', () => {
  const sessions: TestSession[] = [{ id: 's1', label: 'agent', state: 'working' }];

  it('goes home when the row is clicked', () => {
    const onGoToDashboard = vi.fn();
    render(
      <Sidebar {...baseProps} {...buildSidebarData(sessions)} onGoToDashboard={onGoToDashboard} />,
    );

    fireEvent.click(screen.getByTestId('sidebar-home'));

    expect(onGoToDashboard).toHaveBeenCalledTimes(1);
  });

  it('marks the row as the current page only while home is on screen', () => {
    const { rerender } = render(
      <Sidebar {...baseProps} {...buildSidebarData(sessions)} homeActive={false} />,
    );
    expect(screen.getByTestId('sidebar-home')).not.toHaveAttribute('aria-current');

    rerender(<Sidebar {...baseProps} {...buildSidebarData(sessions)} homeActive />);
    expect(screen.getByTestId('sidebar-home')).toHaveAttribute('aria-current', 'page');
    expect(screen.getByTestId('sidebar-home').className).toContain('selected');
  });

  it('replaces the desktops title, so the header carries no label of its own', () => {
    render(<Sidebar {...baseProps} {...buildSidebarData(sessions)} />);

    expect(screen.queryByText('Desktops')).not.toBeInTheDocument();
    expect(screen.getByTestId('sidebar-home')).toBeInTheDocument();
  });

  // The old header hardcoded ⌘G, which went stale when grid view took that chord. Read the hint
  // from the registry so it cannot drift again, or disagree with a rebind.
  it('shows the shortcut home is actually bound to, not a hardcoded one', () => {
    render(<Sidebar {...baseProps} {...buildSidebarData(sessions)} />);

    const hint = screen.getByTestId('sidebar-home').querySelector('.sidebar-home-shortcut');
    expect(hint?.textContent).toBe(formatShortcut('session.goToDashboard'));
    expect(hint?.textContent).not.toBe('⌘G');
  });

  it('keeps a way home when the sidebar is collapsed', () => {
    const onGoToDashboard = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
        collapsed
        homeActive
        onGoToDashboard={onGoToDashboard}
      />,
    );

    const home = screen.getByLabelText('Home');
    expect(home).toHaveAttribute('aria-current', 'page');
    fireEvent.click(home);
    expect(onGoToDashboard).toHaveBeenCalledTimes(1);
  });
});
