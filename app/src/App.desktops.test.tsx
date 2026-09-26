import { describe, expect, it, beforeEach, vi } from 'vitest';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';
import { useProfilesStore } from './store/profiles';
import { useSessionStore, type Session } from './store/sessions';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';
import { ProfileCommandError } from './hooks/daemonProfileEvents';
import { MigrationPhase, type CrewMember, type Desktop } from './types/generated';
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';
import { formatShortcut } from './shortcuts/formatShortcut';
import type { TerminalLayoutNode } from './types/workspace';
import { agentDesktop, arrangeDesktops, fakeDesktopCommands, TEST_PROFILE_ID } from './test/desktops';

const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();

let desktopCommands: ReturnType<typeof fakeDesktopCommands>;
let mockOpenUrlListener: ((urls: string[]) => void) | null;

function collectTileIds(node: TerminalLayoutNode | null): string[] {
  if (!node) {
    return [];
  }
  if (node.type === 'split') {
    return [...collectTileIds(node.children[0]), ...collectTileIds(node.children[1])];
  }
  return node.type === 'tile' ? [node.tileId] : [];
}

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async (listener: (urls: string[]) => void) => {
    mockOpenUrlListener = listener;
    return () => {};
  }),
  getCurrent: vi.fn(async () => []),
}));
vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

vi.mock('./components/GhosttyTerminal', async () => {
  const React = await import('react');
  return { GhosttyTerminal: React.forwardRef(function MockTerminal() { return null; }) };
});

vi.mock('./components/Sidebar', () => ({
  EditorIcon: () => null,
  WorkflowIcon: () => null,
  DiffIcon: () => null,
  PRsIcon: () => null,
  NotebookIcon: () => null,
  MarkdownIcon: () => null,
  Sidebar: ({
    workspaces,
    crew,
    selectedWorkspaceId,
    selectedTile,
    onSelectWorkspace,
    onSelectTile,
    onSelectGridLayout,
  }: {
    workspaces: Array<{ id: string; title: string; sessions: Array<{ id: string }> }>;
    crew?: Array<{ id: string }>;
    selectedWorkspaceId: string | null;
    selectedTile?: { workspaceId: string; tileId: string } | null;
    onSelectWorkspace: (id: string) => void;
    onSelectTile: (workspaceId: string, tileId: string) => void;
    onSelectGridLayout?: (layout: { mode: 'auto' }) => void;
  }) => (
    <div
      data-testid="sidebar"
      data-selected-desktop={selectedWorkspaceId ?? ''}
      data-crew={(crew ?? []).map((member) => member.id).join(',')}
      data-selected-tile={selectedTile ? `${selectedTile.workspaceId}:${selectedTile.tileId}` : ''}
      data-groups={workspaces.map((group) => `${group.title}=${group.sessions.map((entry) => entry.id).join('+')}`).join(',')}
    >
      {workspaces.map((group) => (
        <button key={group.id} data-testid={`select-${group.id}`} onClick={() => onSelectWorkspace(group.id)}>
          {group.id}
        </button>
      ))}
      <button type="button" data-testid="open-grid" onClick={() => onSelectGridLayout?.({ mode: 'auto' })}>
        grid
      </button>
      <button type="button" data-testid="select-readme-tile" onClick={() => onSelectTile('d2', 'tile-readme')}>
        readme
      </button>
      <button type="button" data-testid="select-notes-tile" onClick={() => onSelectTile('d1', 'tile-notes')}>
        notes
      </button>
    </div>
  ),
}));

vi.mock('./components/grid/GridView', () => ({
  GridView: ({ tiles }: { tiles: Array<{ runtimeId: string }> }) => (
    <div data-testid="grid-view" data-runtime-ids={tiles.map((tile) => tile.runtimeId).join(',')} />
  ),
}));

vi.mock('./components/SessionTerminalWorkspace', async () => {
  const React = await import('react');
  return { SessionTerminalWorkspace: React.forwardRef(function MockWorkspace({
    workspaceId,
    workspace,
    isActiveSession,
    selectedSessionId,
    activePaneId,
    workspaceDirectory,
    onFocusPane,
    onUndockTile,
    onLeafDragStart,
  }: {
    workspaceId: string;
    workspace: { agents: unknown[]; layoutTree: TerminalLayoutNode | null };
    isActiveSession: boolean;
    selectedSessionId?: string | null;
    activePaneId: string;
    workspaceDirectory?: string;
    onFocusPane?: (paneId: string) => void;
    onUndockTile?: (tileId: string) => void;
    onLeafDragStart?: (leafId: string) => void;
  }, ref) {
    React.useImperativeHandle(ref, () => ({ focusPane: vi.fn() }));
    return (
    <div>
      <div
        data-testid={`desktop-${workspaceId}`}
        data-active={isActiveSession ? '1' : '0'}
        data-selected-session={selectedSessionId ?? ''}
        data-active-leaf={activePaneId}
        data-workspace-directory={workspaceDirectory ?? ''}
        data-agent-count={workspace.agents.length}
        data-tile-ids={collectTileIds(workspace.layoutTree).join(',')}
      />
      <button type="button" data-testid={`drag-from-${workspaceId}`} onClick={() => onLeafDragStart?.('dragged-leaf')} />
      {collectTileIds(workspace.layoutTree).map((tileId) => (
        <button key={tileId} type="button" data-testid={`undock-${tileId}`} onClick={() => onUndockTile?.(tileId)} />
      ))}
      {workspace.agents.map((agent) => {
        const pane = agent as { id: string };
        return (
          <div key={pane.id}>
            <button
              type="button"
              data-testid={`focus-${pane.id}`}
              onClick={() => onFocusPane?.(pane.id)}
            />
          </div>
        );
      })}
    </div>
    );
  }) };
});

vi.mock('./components/Dashboard', () => ({ Dashboard: () => null }));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({ LocationPicker: () => null }));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
const { mockShowError, mockShowNotice } = vi.hoisted(() => ({ mockShowError: vi.fn(), mockShowNotice: vi.fn() }));
vi.mock('./components/Toast', () => ({
  Toast: () => null,
  useToast: () => ({ toast: null, showError: mockShowError, showNotice: mockShowNotice, clearToast: vi.fn() }),
}));
vi.mock('./hooks/useKeyboardShortcuts', () => ({ useKeyboardShortcuts: vi.fn() }));
vi.mock('./hooks/useUIScale', () => ({
  useUIScale: () => ({ scale: 1, increaseScale: vi.fn(), decreaseScale: vi.fn(), resetScale: vi.fn() }),
}));
vi.mock('./hooks/useOpenPR', () => ({ useOpenPR: () => vi.fn() }));
vi.mock('./hooks/usePRsNeedingAttention', () => ({ usePRsNeedingAttention: () => ({ needsAttention: [] }) }));
vi.mock('./store/daemonSessions', async () => {
  const { selectorStoreMock } = await import('./test/mocks/selectorStore');
  return { useDaemonStore: selectorStoreMock(() => mockUseDaemonStore()) };
});
vi.mock('./hooks/useDaemonSocket', () => ({
  useDaemonSocket: (args: unknown) => mockUseDaemonSocket(args),
}));
vi.mock('./pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('./pty/bridge')>('./pty/bridge');
  return { ...actual, ptySpawn: vi.fn(async () => {}) };
});

function tileDesktop(id: string, slot: number, tileId: string, tileKind: string, tileParams: string): Desktop {
  return {
    ...agentDesktop(id, slot, []),
    tree_json: JSON.stringify({ type: 'tile', tile_id: tileId, tile_kind: tileKind, tile_params: tileParams }),
    active_pane_id: tileId,
  };
}

function withNotesTile(desktop: Desktop): Desktop {
  const notes = { type: 'tile', tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/tmp/notes.md' };
  const tree = desktop.tree_json
    ? { type: 'split', split_id: 'notes', direction: 'vertical', ratio: 0.6, children: [JSON.parse(desktop.tree_json), notes] }
    : notes;
  return { ...desktop, tree_json: JSON.stringify(tree) };
}

function session(id: string): Session {
  return {
    id,
    label: id,
    state: 'working',
    cwd: '/tmp/repo',
    workspaceId: '',
    profileId: TEST_PROFILE_ID,
    desktopId: '',
    agent: 'claude',
    transcriptMatched: true,
    daemonActivePaneId: '',
    desktop: { agents: [], layoutTree: null },
  };
}

function seedSessions(ids: string[], activeSessionId: string | null = null) {
  useSessionStore.setState({
    sessions: ids.map(session),
    activeSessionId,
    view: 'session',
    connect: vi.fn(async () => {}),
    connected: true,
    launcherConfig: { executables: {} },
    createSession: vi.fn(async () => ids[0]),
    closeSession: vi.fn(),
    takeSessionSpawnArgs: vi.fn(() => null),
    reloadSession: vi.fn(async () => {}),
  });
  mockUseDaemonStore.mockReturnValue({
    daemonSessions: ids.map((id) => ({ id, label: id, directory: '/tmp/repo', state: 'working', profile_id: TEST_PROFILE_ID })),
    crew: [],
    setDaemonSessions: vi.fn(),
    prs: [], setPRs: vi.fn(),
    repoStates: [], setRepoStates: vi.fn(),
    authorStates: [], setAuthorStates: vi.fn(),
    seeds: [], setSeeds: vi.fn(),
  });
}

const desktopTestId = (id: string) => `desktop-${id}`;
const isActive = (id: string) => screen.getByTestId(desktopTestId(id)).getAttribute('data-active') === '1';

describe('desktop surface', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useSessionStore.setState(useSessionStore.getInitialState(), true);
    useProfilesStore.setState(useProfilesStore.getInitialState(), true);
    localStorage.clear();
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    mockOpenUrlListener = null;
    desktopCommands = fakeDesktopCommands();

    arrangeDesktops([
      agentDesktop('d1', 1, ['s1', 's2']),
      tileDesktop('d2', 2, 'tile-readme', 'markdown', '/tmp/project/README.md'),
      agentDesktop('d3', 3, ['s3']),
    ]);
    seedSessions(['s1', 's2', 's3', 's4'], 's1');

    const fn = vi.fn();
    mockUseDaemonSocket.mockReturnValue({
      sendPRAction: fn, sendMutePR: fn, sendMuteRepo: fn, sendMuteAuthor: fn, sendPRVisited: fn,
      sendRefreshPRs: vi.fn(async () => ({ success: true })),
      sendUnregisterSession: fn, sendRegisterWorkspace: fn,
      sendUnregisterWorkspace: vi.fn(async () => {}),
      sendSetSetting: fn,
      sendSettleTurn: vi.fn(),
      sendSetClientPresence: fn,
      sendCreateWorktree: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendDeleteWorktree: vi.fn(async () => ({ success: true })),
      sendGetRecentLocations: vi.fn(async () => ({ success: true, locations: [] })),
      sendCreateWorktreeFromBranch: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendFetchRemotes: vi.fn(async () => ({ success: true })),
      sendFetchPRDetails: vi.fn(async () => ({ success: true })),
      sendEnsureRepo: vi.fn(async () => ({ success: true, path: '/tmp/repo' })),
      sendSubscribeGitStatus: fn, sendUnsubscribeGitStatus: fn,
      ...desktopCommands,
      desktopTileContents: {},
      sendGetFileDiff: vi.fn(async () => ({ success: true, original: '', modified: '' })),
      getRepoInfo: vi.fn(async () => ({ success: true, is_git_repo: true, branch: 'main' })),
      listWorkflowRuns: vi.fn(async () => ({ success: true, runs: [] })),
      getPresentations: vi.fn(async () => []),
      connectionError: null,
      hasReceivedInitialState: true,
      sendNotificationList: vi.fn(async () => ({ notifications: [], unreadCount: 0, critical: { count: 0, title: '' } })),
      sendNotificationMarkRead: vi.fn(async () => 0),
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
      sendSetTerminalTheme: fn,
    });
  });

  it('mounts no sidebar or agent desktop until the migration completes and the user continues', async () => {
    useProfilesStore.setState({ migrationPhase: MigrationPhase.PlacementRequired });
    render(<App />);

    expect(await screen.findByText('Loading your desktops…')).toBeInTheDocument();
    expect(screen.queryByTestId('sidebar')).not.toBeInTheDocument();

    act(() => useProfilesStore.setState({ migrationPhase: MigrationPhase.Complete }));
    await userEvent.click(await screen.findByRole('button', { name: 'Continue →' }));
    expect(await screen.findByTestId('sidebar')).toBeInTheDocument();
  });

  it('groups the sidebar by desktop in slot order, then Unplaced', async () => {
    render(<App />);

    await waitFor(() => {
      expect(screen.getByTestId('sidebar').getAttribute('data-groups')).toBe(
        'Desktop 1=s1+s2,Desktop 2=,Desktop 3=s3,Unplaced=s4',
      );
    });
  });

  it('opens the agent palette on this profile’s crew, agents and tiles, and focuses a picked tile', async () => {
    const crewMember = (id: string, profileId: string): CrewMember => ({
      id,
      profile_id: profileId,
      awareness_dirs: [],
      charter_path: '',
      home_dir: '',
      resolved_agent: 'claude',
      revision: 1,
    });
    mockUseDaemonStore.mockReturnValue({
      ...mockUseDaemonStore(),
      crew: [crewMember('figgy', TEST_PROFILE_ID), crewMember('ops', 'profile-other')],
    });
    render(<App />);
    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-crew')).toBe('figgy'));

    act(() => vi.mocked(useKeyboardShortcuts).mock.lastCall![0].onOpenPalette('agents'));
    const palette = await screen.findByRole('dialog', { name: 'Agents' });
    const options = within(palette).getAllByRole('option').map((option) => option.textContent ?? '');
    expect(options.some((text) => text.includes('Figgy'))).toBe(true);
    expect(options.some((text) => text.includes('Ops'))).toBe(false);
    expect(options.filter((text) => /^s\d/.test(text)).map((text) => text.slice(0, 2))).toEqual(['s1', 's2', 's3', 's4']);
    expect(options.some((text) => text.includes('README.md'))).toBe(true);

    await userEvent.type(within(palette).getByRole('combobox'), 'readme{Enter}');
    await waitFor(() => expect(desktopCommands.sendDesktopSetActivePane).toHaveBeenCalledWith('d2', 'tile-readme'));
    expect(screen.queryByRole('dialog', { name: 'Agents' })).toBeNull();
  });

  it('walks automation runs needing the user outside the queue, and settles the one being looked at', async () => {
    const docs = { run_id: 'r', definition_id: 'docs', definition_name: 'nightly docs', trigger_type: 'schedule' };
    const daemonSession = (id: string, extra: object) => ({
      id, label: id, directory: '/tmp/repo', state: 'waiting_input', profile_id: TEST_PROFILE_ID, ...extra,
    });
    const withRuns = (owed: boolean) =>
      mockUseDaemonStore.mockReturnValue({
        ...mockUseDaemonStore(),
        daemonSessions: [
          daemonSession('s1', {}),
          daemonSession('s2', { automation: docs, turn_owed: owed, turn_opened_at: '2026-09-26T09:00:00Z' }),
          daemonSession('s3', { automation: docs, turn_owed: owed, turn_opened_at: '2026-09-26T10:00:00Z' }),
        ],
      });
    withRuns(true);
    const { rerender } = render(<App />);
    const shortcuts = () => vi.mocked(useKeyboardShortcuts).mock.lastCall![0];

    act(() => shortcuts().onNextRun());
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s2'));
    expect(mockShowNotice).toHaveBeenLastCalledWith(
      `nightly docs · run 1 of 2 needing you · ${formatShortcut('session.settle')} settles, ${formatShortcut('session.nextRun')} moves on`,
    );

    act(() => shortcuts().onSettleTurn!());
    expect(mockUseDaemonSocket().sendSettleTurn).toHaveBeenCalledWith('s2');

    act(() => shortcuts().onNextRun());
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s3'));
    expect(mockShowNotice).toHaveBeenLastCalledWith(expect.stringContaining('run 2 of 2 needing you'));

    withRuns(false);
    rerender(<App />);
    act(() => shortcuts().onNextRun());
    expect(mockShowNotice).toHaveBeenLastCalledWith('No run needs you · 2 runs on file');
  });

  it('mounts only the current desktop until the user leaves it', async () => {
    render(<App />);

    expect(await screen.findByTestId(desktopTestId('d1'))).toBeInTheDocument();
    expect(isActive('d1')).toBe(true);
    expect(screen.queryByTestId(desktopTestId('d2'))).toBeNull();
    expect(screen.queryByTestId(desktopTestId('d3'))).toBeNull();
  });

  it('switches desktops through the daemon and keeps the bounce target mounted', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    await userEvent.click(screen.getByTestId('select-d2'));

    expect(desktopCommands.sendDesktopSetCurrent).toHaveBeenLastCalledWith(TEST_PROFILE_ID, 'd2');
    await waitFor(() => expect(isActive('d2')).toBe(true));
    expect(screen.getByTestId(desktopTestId('d2')).getAttribute('data-tile-ids')).toBe('tile-readme');
    expect(isActive('d1')).toBe(false);
    expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-selected-session')).toBe('');
    expect(screen.queryByTestId(desktopTestId('d3'))).toBeNull();
    expect(screen.getByTestId('sidebar').getAttribute('data-selected-desktop')).toBe('d2');
    expect(useSessionStore.getState().activeSessionId).toBeNull();

    await userEvent.click(screen.getByTestId('select-d1'));

    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s1'));
    expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-selected-session')).toBe('s1');
  });

  it('stops treating the current desktop as active at home', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    expect(isActive('d1')).toBe(true);

    act(() => {
      useSessionStore.getState().goToDashboard();
    });

    expect(isActive('d1')).toBe(false);
  });

  it('ignores a click on the Unplaced group, which is not a desktop', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    act(() => {
      useSessionStore.getState().goToDashboard();
    });

    await userEvent.click(screen.getByTestId('select-unplaced'));

    expect(desktopCommands.sendDesktopSetCurrent).not.toHaveBeenCalled();
    expect(useSessionStore.getState().view).toBe('dashboard');
  });

  it('sends pane focus to the daemon and follows its broadcast', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    await userEvent.click(screen.getByTestId('focus-pane-s2'));

    expect(desktopCommands.sendDesktopSetActivePane).toHaveBeenCalledWith('d1', 'pane-s2');
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s2'));
    expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-selected-session')).toBe('s2');
  });

  it('follows another client that moves the active pane without sending anything back', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s1'));

    act(() => {
      const { desktops } = useProfilesStore.getState();
      arrangeDesktops(
        desktops.map((desktop) => (desktop.id === 'd1' ? { ...desktop, active_pane_id: 'pane-s2', revision: 2 } : desktop)),
        'd1',
      );
    });

    expect(useSessionStore.getState().activeSessionId).toBe('s2');
    expect(desktopCommands.sendDesktopSetActivePane).not.toHaveBeenCalled();
    expect(desktopCommands.sendDesktopSetCurrent).not.toHaveBeenCalled();
  });

  it('moves to the desktop that holds a selected agent', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    act(() => {
      useSessionStore.getState().selectAgent('s3');
    });

    expect(desktopCommands.sendDesktopSetCurrent).toHaveBeenLastCalledWith(TEST_PROFILE_ID, 'd3');
    await waitFor(() => expect(isActive('d3')).toBe(true));
    expect(useSessionStore.getState().activeSessionId).toBe('s3');
  });

  it('places an unplaced agent beside the active pane of the current desktop', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    act(() => {
      useSessionStore.getState().selectAgent('s4');
    });

    expect(desktopCommands.sendDesktopPlaceSession).toHaveBeenCalledWith({
      desktopId: 'd1',
      sessionId: 's4',
      expectedRevision: 1,
      anchorPaneId: 'pane-s1',
    });
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s4'));
    expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-agent-count')).toBe('3');
  });

  it('places an unplaced agent selected from Home and shows it', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    act(() => useSessionStore.getState().goToDashboard());

    act(() => {
      useSessionStore.getState().selectAgent('s4');
    });

    expect(desktopCommands.sendDesktopPlaceSession).toHaveBeenCalledWith(
      expect.objectContaining({ desktopId: 'd1', sessionId: 's4' }),
    );
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s4'));
    expect(useSessionStore.getState().view).toBe('session');
  });

  it('leaves a selected agent it has not seen yet to its launch placement', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    act(() => {
      useSessionStore.getState().selectAgent('s9');
    });
    expect(desktopCommands.sendDesktopPlaceSession).not.toHaveBeenCalled();

    act(() => {
      arrangeDesktops(
        useProfilesStore.getState().desktops.map((desktop) =>
          desktop.id === 'd1' ? { ...agentDesktop('d1', 1, ['s1', 's2', 's9'], 's1'), revision: 2 } : desktop,
        ),
        'd1',
      );
      useSessionStore.getState().syncFromDaemonSessions(
        ['s1', 's2', 's3', 's4', 's9'].map((id) => ({
          id,
          label: id,
          directory: '/tmp/repo',
          state: 'working',
          profile_id: TEST_PROFILE_ID,
          workspace_id: '',
        })),
      );
    });

    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s9'));
    expect(desktopCommands.sendDesktopPlaceSession).not.toHaveBeenCalled();
    expect(desktopCommands.sendDesktopSetActivePane).toHaveBeenCalledWith('d1', 'pane-s9');
  });

  it('switches profile before placing an agent that belongs to another profile', async () => {
    useSessionStore.setState((state) => ({
      sessions: [...state.sessions, { ...session('s5'), profileId: 'profile-other' }],
    }));
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    act(() => {
      useSessionStore.getState().selectAgent('s5');
    });

    expect(desktopCommands.sendProfileSelect).toHaveBeenCalledWith('profile-other');
    expect(desktopCommands.sendDesktopPlaceSession).not.toHaveBeenCalled();
  });

  it('keeps a selection in another profile through the profile switch and finishes it there', async () => {
    const OTHER = 'profile-other';
    useSessionStore.setState((state) => ({
      sessions: [...state.sessions, { ...session('s5'), profileId: OTHER }, { ...session('s6'), profileId: OTHER }],
    }));
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    desktopCommands.sendDesktopSetCurrent.mockImplementation(() => new Promise(() => {}));

    act(() => {
      useSessionStore.getState().selectAgent('s5');
    });
    expect(desktopCommands.sendProfileSelect).toHaveBeenCalledWith(OTHER);
    act(() => {
      const other = { id: OTHER, name: 'Other', current_desktop_id: 'e1', revision: 1 };
      useProfilesStore.getState().profilesChanged([other]);
      useProfilesStore.getState().arrangementArrived(other, [
        { ...agentDesktop('e1', 1, ['s6']), profile_id: OTHER },
        { ...agentDesktop('e2', 2, ['s5']), profile_id: OTHER },
      ]);
    });

    await waitFor(() => expect(desktopCommands.sendDesktopSetCurrent).toHaveBeenCalledWith(OTHER, 'e2'));
    expect(useSessionStore.getState().activeSessionId).toBe('s5');
  });

  it('finishes a selection in another profile whose current desktop shows a tile', async () => {
    const OTHER = 'profile-other';
    useSessionStore.setState((state) => ({
      sessions: [...state.sessions, { ...session('s5'), profileId: OTHER }, { ...session('s6'), profileId: OTHER }],
    }));
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    desktopCommands.sendDesktopSetCurrent.mockImplementation(() => new Promise(() => {}));
    const other = { id: OTHER, name: 'Other', current_desktop_id: 'e1', revision: 1 };
    const tileOn = (desktop: Desktop) => ({ ...withNotesTile(desktop), active_pane_id: 'tile-notes' });

    act(() => {
      useSessionStore.getState().selectAgent('s5');
    });
    act(() => {
      useProfilesStore.getState().profilesChanged([other]);
      useProfilesStore.getState().arrangementArrived(other, [
        { ...tileOn(agentDesktop('e1', 1, ['s6'])), profile_id: OTHER },
        { ...agentDesktop('e2', 2, ['s5']), profile_id: OTHER },
      ]);
    });

    await waitFor(() => expect(desktopCommands.sendDesktopSetCurrent).toHaveBeenCalledWith(OTHER, 'e2'));
    expect(useSessionStore.getState().activeSessionId).toBe('s5');

    act(() => {
      useProfilesStore.getState().arrangementArrived({ ...other, current_desktop_id: 'e2' }, useProfilesStore.getState().desktops);
    });

    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe(''));
    expect(useSessionStore.getState().activeSessionId).toBe('s5');
  });

  describe('when the agent behind a focused tile closes', () => {
    const tileFocused = (desktop: Desktop) => ({ ...withNotesTile(desktop), active_pane_id: 'tile-notes' });
    const dropFromD1 = (remaining: string[]) =>
      act(() => {
        arrangeDesktops(
          useProfilesStore.getState().desktops.map((desktop) =>
            desktop.id === 'd1' ? { ...tileFocused(agentDesktop('d1', 1, remaining)), revision: 2 } : desktop,
          ),
          'd1',
        );
      });
    const closeSession = (id: string) => act(() => useSessionStore.getState().removeSessionLocalState(id));

    beforeEach(() => {
      arrangeDesktops(useProfilesStore.getState().desktops.map((desktop) => (desktop.id === 'd1' ? tileFocused(desktop) : desktop)), 'd1');
    });

    async function renderWithTileFocused() {
      render(<App />);
      await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d1:tile-notes'));
      expect(useSessionStore.getState().activeSessionId).toBe('s1');
    }

    it('moves to another agent on the shown desktop when the session goes first', async () => {
      await renderWithTileFocused();

      closeSession('s1');
      dropFromD1(['s2']);

      await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s2'));
      expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d1:tile-notes');
      expect(desktopCommands.sendDesktopSetActivePane).not.toHaveBeenCalled();
    });

    it('moves to another agent on the shown desktop when the pane goes first', async () => {
      await renderWithTileFocused();

      dropFromD1(['s2']);
      closeSession('s1');

      await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s2'));
      expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d1:tile-notes');
    });

    it('has no agent rather than one from another desktop when none is left', async () => {
      await renderWithTileFocused();

      closeSession('s1');
      closeSession('s2');
      dropFromD1([]);

      await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBeNull());
      expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d1:tile-notes');
    });
  });

  it('drops a selection the daemon refuses so selecting the agent again retries', async () => {
    desktopCommands.sendDesktopPlaceSession.mockRejectedValueOnce(new Error('desktop is gone'));
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    act(() => {
      useSessionStore.getState().selectAgent('s4');
    });
    await waitFor(() => expect(useSessionStore.getState().pendingSelection).toBeNull());
    expect(desktopCommands.sendDesktopPlaceSession).toHaveBeenCalledTimes(1);
    expect(useSessionStore.getState().activeSessionId).toBe('s1');
    expect(mockShowError).toHaveBeenCalledWith(expect.stringContaining('desktop is gone'));

    act(() => {
      useSessionStore.getState().selectAgent('s4');
    });

    await waitFor(() => expect(desktopCommands.sendDesktopPlaceSession).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe('s4'));
  });

  it('puts the active agent back on the shown desktop when switching to its desktop is refused', async () => {
    desktopCommands.sendDesktopSetCurrent.mockRejectedValueOnce(new Error('profile is closed'));
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    act(() => {
      useSessionStore.getState().selectAgent('s3');
    });

    await waitFor(() => expect(mockShowError).toHaveBeenCalledWith(expect.stringContaining('profile is closed')));
    expect(useSessionStore.getState().activeSessionId).toBe('s1');
    expect(isActive('d1')).toBe(true);
  });

  it('keeps a selection whose command lost a revision race, for the newer arrangement to retry', async () => {
    desktopCommands.sendDesktopPlaceSession.mockRejectedValueOnce(new ProfileCommandError({
      event: 'profile_action_result',
      request_id: 'test',
      action: 'desktop_place_session',
      success: false,
      error: 'stale',
      error_code: 'stale_revision',
    } as never));
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    act(() => {
      useSessionStore.getState().selectAgent('s4');
    });
    await waitFor(() => expect(desktopCommands.sendDesktopPlaceSession).toHaveBeenCalledTimes(1));
    expect(useSessionStore.getState().pendingSelection?.sessionId).toBe('s4');

    act(() => arrangeDesktops(useProfilesStore.getState().desktops.map((desktop) => ({ ...desktop, revision: desktop.revision + 1 })), 'd1'));

    await waitFor(() => expect(desktopCommands.sendDesktopPlaceSession).toHaveBeenCalledTimes(2));
  });

  it('lets a broadcast that contradicts a pending selection win', async () => {
    desktopCommands.sendDesktopPlaceSession.mockImplementation(() => new Promise(() => {}));
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    act(() => {
      useSessionStore.getState().selectAgent('s4');
    });
    expect(useSessionStore.getState().pendingSelection?.sessionId).toBe('s4');

    act(() => {
      const { desktops } = useProfilesStore.getState();
      arrangeDesktops(desktops, 'd3');
    });

    expect(useSessionStore.getState().pendingSelection).toBeNull();
    expect(useSessionStore.getState().activeSessionId).toBe('s3');
  });

  it('focuses a tile through the daemon and follows the focus it broadcasts', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    await userEvent.click(screen.getByTestId('select-readme-tile'));

    await waitFor(() => expect(isActive('d2')).toBe(true));
    expect(desktopCommands.sendDesktopSetActivePane).toHaveBeenLastCalledWith('d2', 'tile-readme');
    expect(desktopCommands.sendDesktopSetCurrent).toHaveBeenLastCalledWith(TEST_PROFILE_ID, 'd2');
    expect(desktopCommands.sendDesktopSetActivePane.mock.invocationCallOrder[0]).toBeLessThan(
      desktopCommands.sendDesktopSetCurrent.mock.invocationCallOrder[0],
    );
    expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d2:tile-readme');
    expect(screen.getByTestId(desktopTestId('d2')).getAttribute('data-active-leaf')).toBe('tile-readme');
  });

  it('follows the daemon onto a tile an open docked, keeping the agent as context without pulling focus back', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    act(() => {
      arrangeDesktops(
        useProfilesStore.getState().desktops.map((desktop) =>
          desktop.id === 'd1'
            ? {
                ...desktop,
                tree_json: JSON.stringify({
                  type: 'split',
                  split_id: 'opened',
                  direction: 'vertical',
                  ratio: 0.6,
                  children: [
                    JSON.parse(desktop.tree_json),
                    { type: 'tile', tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/tmp/notes.md' },
                  ],
                }),
                active_pane_id: 'tile-notes',
                revision: desktop.revision + 1,
              }
            : desktop,
        ),
        'd1',
      );
    });

    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d1:tile-notes'));
    expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-active-leaf')).toBe('tile-notes');
    expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-selected-session')).toBe('');
    expect(useSessionStore.getState().activeSessionId).toBe('s1');
    expect(desktopCommands.sendDesktopSetActivePane).not.toHaveBeenCalled();
  });

  describe('with a tile holding the daemon focus', () => {
    const selectedTileAttr = () => screen.getByTestId('sidebar').getAttribute('data-selected-tile');

    beforeEach(() => {
      arrangeDesktops(
        useProfilesStore.getState().desktops.map((desktop) =>
          desktop.id === 'd1' ? { ...withNotesTile(desktop), active_pane_id: 'tile-notes' } : desktop,
        ),
        'd1',
      );
    });

    async function renderOnTile() {
      render(<App />);
      await waitFor(() => expect(selectedTileAttr()).toBe('d1:tile-notes'));
    }

    it('moves focus to the tile\'s own context agent when the user selects it', async () => {
      await renderOnTile();
      expect(useSessionStore.getState().activeSessionId).toBe('s1');

      act(() => {
        useSessionStore.getState().selectAgent('s1');
      });

      expect(desktopCommands.sendDesktopSetActivePane).toHaveBeenLastCalledWith('d1', 'pane-s1');
      await waitFor(() => expect(selectedTileAttr()).toBe(''));
      expect(useSessionStore.getState().activeSessionId).toBe('s1');
    });

    it('moves focus to another agent on the same desktop when the user selects it', async () => {
      await renderOnTile();

      act(() => {
        useSessionStore.getState().selectAgent('s2');
      });

      expect(desktopCommands.sendDesktopSetActivePane).toHaveBeenLastCalledWith('d1', 'pane-s2');
      await waitFor(() => expect(selectedTileAttr()).toBe(''));
      expect(useSessionStore.getState().activeSessionId).toBe('s2');
    });

    it('sends pane focus when the user clicks an agent pane', async () => {
      await renderOnTile();

      await userEvent.click(screen.getByTestId('focus-pane-s1'));

      expect(desktopCommands.sendDesktopSetActivePane).toHaveBeenLastCalledWith('d1', 'pane-s1');
      await waitFor(() => expect(selectedTileAttr()).toBe(''));
    });

    it('drops the tile when the user switches to a desktop without one', async () => {
      await renderOnTile();

      await userEvent.click(screen.getByTestId('select-d3'));

      await waitFor(() => expect(isActive('d3')).toBe(true));
      expect(selectedTileAttr()).toBe('');
      expect(useSessionStore.getState().activeSessionId).toBe('s3');
    });

    it('shows the other desktop\'s tile when the user switches to a tile-only desktop', async () => {
      await renderOnTile();

      await userEvent.click(screen.getByTestId('select-d2'));

      await waitFor(() => expect(selectedTileAttr()).toBe('d2:tile-readme'));
      expect(useSessionStore.getState().activeSessionId).toBeNull();
      expect(desktopCommands.sendDesktopSetActivePane).not.toHaveBeenCalled();
    });
  });

  it('lets a tile another window focuses win over the agent the user selected before', async () => {
    arrangeDesktops(useProfilesStore.getState().desktops.map((desktop) => (desktop.id === 'd1' ? withNotesTile(desktop) : desktop)), 'd1');
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    act(() => {
      useSessionStore.getState().selectAgent('s1');
    });

    act(() => {
      arrangeDesktops(
        useProfilesStore.getState().desktops.map((desktop) =>
          desktop.id === 'd1' ? { ...desktop, active_pane_id: 'tile-notes', revision: desktop.revision + 1 } : desktop,
        ),
        'd1',
      );
    });

    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d1:tile-notes'));
    expect(desktopCommands.sendDesktopSetActivePane).not.toHaveBeenCalled();
    expect(useSessionStore.getState().activeSessionId).toBe('s1');
  });

  it('puts the sidebar back on the shown focus when the daemon refuses a tile selection', async () => {
    arrangeDesktops(useProfilesStore.getState().desktops.map((desktop) => (desktop.id === 'd1' ? withNotesTile(desktop) : desktop)), 'd1');
    desktopCommands.sendDesktopSetActivePane.mockRejectedValueOnce(new Error('leaf gone'));
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    await userEvent.click(screen.getByTestId('select-notes-tile'));

    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe(''));
  });

  it('shows the focused tile again when the user comes back from Home to a tile-only desktop', async () => {
    render(<App />);
    await userEvent.click(await screen.findByTestId('select-d2'));
    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d2:tile-readme'));

    act(() => useSessionStore.getState().goToDashboard());
    expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('');
    await userEvent.click(screen.getByTestId('select-d2'));

    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d2:tile-readme'));
    expect(desktopCommands.sendDesktopSetActivePane).not.toHaveBeenCalled();
  });

  it('keeps a tile the user picks from Home while the daemon applies it', async () => {
    arrangeDesktops(useProfilesStore.getState().desktops.map((desktop) => (desktop.id === 'd1' ? withNotesTile(desktop) : desktop)), 'd1');
    let applyFocus = () => {};
    desktopCommands.sendDesktopSetActivePane.mockImplementationOnce(
      (desktopId: string, paneId: string) =>
        new Promise((resolve) => {
          applyFocus = () => {
            arrangeDesktops(
              useProfilesStore.getState().desktops.map((desktop) =>
                desktop.id === desktopId ? { ...desktop, active_pane_id: paneId, revision: desktop.revision + 1 } : desktop,
              ),
              'd1',
            );
            resolve({ event: 'profile_action_result', request_id: 'test', action: 'desktop_set_active_pane', success: true });
          };
        }),
    );
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    act(() => useSessionStore.getState().goToDashboard());

    await userEvent.click(screen.getByTestId('select-notes-tile'));
    await act(async () => applyFocus());

    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d1:tile-notes'));
    expect(desktopCommands.sendDesktopSetActivePane.mock.calls).toEqual([['d1', 'tile-notes']]);
    expect(useSessionStore.getState().view).toBe('session');
  });

  it('offers the focused agent\'s directory as the workspace root only when it runs on this machine', async () => {
    render(<App />);
    await waitFor(() =>
      expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-workspace-directory')).toBe('/tmp/repo'),
    );

    act(() => {
      useSessionStore.setState((state) => ({
        sessions: state.sessions.map((entry) => (entry.id === 's1' ? { ...entry, endpointId: 'remote-box' } : entry)),
      }));
    });

    await waitFor(() =>
      expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-workspace-directory')).toBe(''),
    );
  });

  it('keeps the desktop a drag started on mounted while hovering switches through others', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));

    await userEvent.click(screen.getByTestId('drag-from-d1'));
    await userEvent.click(screen.getByTestId('select-d2'));
    await waitFor(() => expect(isActive('d2')).toBe(true));
    await userEvent.click(screen.getByTestId('select-d3'));
    await waitFor(() => expect(isActive('d3')).toBe(true));

    expect(screen.getByTestId(desktopTestId('d1'))).toBeInTheDocument();
  });

  it('drops the selected tile when the shown desktop moves to one without agents', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    await userEvent.click(screen.getByTestId('select-d2'));
    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d2:tile-readme'));

    act(() => arrangeDesktops([...useProfilesStore.getState().desktops, agentDesktop('d-empty', 4, [])], 'd-empty'));

    await waitFor(() => expect(isActive('d-empty')).toBe(true));
    expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('');
  });

  it('retries closing a tile against the revision another window moved the desktop to', async () => {
    const { sendDesktopRemoveLeaf } = desktopCommands;
    sendDesktopRemoveLeaf.mockRejectedValueOnce(new ProfileCommandError({
      event: 'profile_action_result',
      request_id: 'test',
      action: 'desktop_remove_leaf',
      success: false,
      error: 'stale',
      error_code: 'stale_revision',
    } as never));
    render(<App />);
    await userEvent.click(await screen.findByTestId('select-d2'));
    await userEvent.click(await screen.findByTestId('undock-tile-readme'));
    expect(sendDesktopRemoveLeaf).toHaveBeenLastCalledWith('d2', 'tile-readme', 1);

    act(() => arrangeDesktops(
      useProfilesStore.getState().desktops.map((desktop) => (desktop.id === 'd2' ? { ...desktop, revision: 7 } : desktop)),
      'd2',
    ));

    await waitFor(() => expect(sendDesktopRemoveLeaf).toHaveBeenLastCalledWith('d2', 'tile-readme', 7));
    expect(sendDesktopRemoveLeaf).toHaveBeenCalledTimes(2);
  });

  it('mounts the desktops the grid shows', async () => {
    render(<App />);
    await screen.findByTestId(desktopTestId('d1'));
    expect(screen.queryByTestId(desktopTestId('d3'))).toBeNull();

    await userEvent.click(screen.getByTestId('open-grid'));

    await waitFor(() => {
      expect(screen.getByTestId('grid-view').getAttribute('data-runtime-ids')).toContain('s3');
      expect(screen.getByTestId(desktopTestId('d3'))).toBeInTheDocument();
    });
  });

  it('uses sessions loaded after mount when an existing-session deep link arrives', async () => {
    useSessionStore.setState({ sessions: [], activeSessionId: null });
    render(<App />);
    await waitFor(() => expect(mockOpenUrlListener).not.toBeNull());

    act(() => {
      useSessionStore.getState().syncFromDaemonSessions([
        { id: 's3', label: 's3', directory: '/tmp/elsewhere', state: 'working', profile_id: TEST_PROFILE_ID, workspace_id: '' },
      ]);
    });
    await act(async () => {
      mockOpenUrlListener?.(['attn://spawn?cwd=%2Ftmp%2Felsewhere']);
    });

    expect(useSessionStore.getState().focusRequest?.sessionId).toBe('s3');
    await waitFor(() => expect(isActive('d3')).toBe(true));
  });

  it('sends the resolved terminal theme once the daemon handshake completes', async () => {
    render(<App />);

    await waitFor(() => {
      expect(mockUseDaemonSocket.mock.results[0]?.value.sendSetTerminalTheme).toHaveBeenCalledWith({
        foreground: '#d4d4d4',
        background: '#1e1e1e',
        cursor: '#d4d4d4',
        ansi_palette: [
          '#000000', '#cd3131', '#0dbc79', '#e5e510',
          '#2472c8', '#bc3fbc', '#11a8cd', '#e5e5e5',
          '#666666', '#f14c4c', '#23d18b', '#f5f543',
          '#3b8eea', '#d670d6', '#29b8db', '#ffffff',
        ],
      });
    });
  });
});
