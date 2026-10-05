import { describe, expect, it, beforeEach, vi } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';
import { useProfilesStore } from './store/profiles';
import { useSessionStore, type Session } from './store/sessions';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';
import { ProfileCommandError } from './hooks/daemonProfileEvents';
import { MigrationPhase, type Desktop } from './types/generated';
import type { TerminalLayoutNode } from './types/desktop';
import { agentDesktop, arrangeDesktops, fakeDesktopCommands, TEST_PROFILE_ID } from './test/desktops';

const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();

let desktopCommands: ReturnType<typeof fakeDesktopCommands>;

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
  onOpenUrl: vi.fn(async () => () => {}),
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
    desktops,
    crew,
    selectedDesktopId,
    selectedTile,
    onSelectDesktop,
    onSelectTile,
    headerActions,
  }: {
    headerActions: Array<{ id: string; disabled?: boolean }>;
    desktops: Array<{ id: string; title: string; sessions: Array<{ id: string }> }>;
    crew?: Array<{ id: string }>;
    selectedDesktopId: string | null;
    selectedTile?: { desktopId: string; tileId: string } | null;
    onSelectDesktop: (id: string) => void;
    onSelectTile: (desktopId: string, tileId: string) => void;
  }) => (
    <div
      data-testid="sidebar"
      data-editor-enabled={String(headerActions.some((action) => action.id === 'editor' && !action.disabled))}
      data-selected-desktop={selectedDesktopId ?? ''}
      data-crew={(crew ?? []).map((member) => member.id).join(',')}
      data-selected-tile={selectedTile ? `${selectedTile.desktopId}:${selectedTile.tileId}` : ''}
      data-groups={desktops.map((group) => `${group.title}=${group.sessions.map((entry) => entry.id).join('+')}`).join(',')}
    >
      {desktops.map((group) => (
        <button key={group.id} data-testid={`select-${group.id}`} onClick={() => onSelectDesktop(group.id)}>
          {group.id}
        </button>
      ))}
      <button type="button" data-testid="select-readme-tile" onClick={() => onSelectTile('d2', 'tile-readme')}>
        readme
      </button>
      <button type="button" data-testid="select-notes-tile" onClick={() => onSelectTile('d1', 'tile-notes')}>
        notes
      </button>
    </div>
  ),
}));

vi.mock('./components/SessionTerminalDesktop', async () => {
  const React = await import('react');
  return { SessionTerminalDesktop: React.forwardRef(function MockDesktop({
    desktopId,
    terminalState,
    isActiveSession,
    selectedSessionId,
    activePaneId,
    desktopDirectory,
    onFocusPane,
    onUndockTile,
    onLeafDragStart,
  }: {
    desktopId: string;
    terminalState: { agents: unknown[]; layoutTree: TerminalLayoutNode | null };
    isActiveSession: boolean;
    selectedSessionId?: string | null;
    activePaneId: string;
    desktopDirectory?: string;
    onFocusPane?: (paneId: string) => void;
    onUndockTile?: (tileId: string) => void;
    onLeafDragStart?: (leafId: string) => void;
  }, ref) {
    React.useImperativeHandle(ref, () => ({ focusPane: vi.fn() }));
    return (
    <div>
      <div
        data-testid={`desktop-${desktopId}`}
        data-active={isActiveSession ? '1' : '0'}
        data-selected-session={selectedSessionId ?? ''}
        data-active-leaf={activePaneId}
        data-desktop-directory={desktopDirectory ?? ''}
        data-agent-count={terminalState.agents.length}
        data-tile-ids={collectTileIds(terminalState.layoutTree).join(',')}
      />
      <button type="button" data-testid={`drag-from-${desktopId}`} onClick={() => onLeafDragStart?.('dragged-leaf')} />
      {collectTileIds(terminalState.layoutTree).map((tileId) => (
        <button key={tileId} type="button" data-testid={`undock-${tileId}`} onClick={() => onUndockTile?.(tileId)} />
      ))}
      {terminalState.agents.map((agent) => {
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
const { mockShowError } = vi.hoisted(() => ({ mockShowError: vi.fn() }));
vi.mock('./components/Toast', () => ({
  Toast: () => null,
  useToast: () => ({ showError: mockShowError }),
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

function session(id: string): Session {
  return {
    id,
    label: id,
    state: 'working',
    cwd: '/tmp/repo',
    profileId: TEST_PROFILE_ID,
    desktopId: '',
    agent: 'claude',
    transcriptMatched: true,
    daemonActivePaneId: '',
    desktop: { agents: [], layoutTree: null },
  };
}

function seedSessions(ids: string[]) {
  useSessionStore.setState({
    sessions: ids.map(session),
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
    desktopCommands = fakeDesktopCommands();

    seedSessions(['s1', 's2', 's3', 's4']);
    arrangeDesktops([
      agentDesktop('d1', 1, ['s1', 's2']),
      tileDesktop('d2', 2, 'tile-readme', 'markdown', '/tmp/project/README.md'),
      agentDesktop('d3', 3, ['s3']),
    ]);

    const fn = vi.fn();
    mockUseDaemonSocket.mockReturnValue({
      sendPRAction: fn, sendMutePR: fn, sendMuteRepo: fn, sendMuteAuthor: fn, sendPRVisited: fn,
      sendRefreshPRs: vi.fn(async () => ({ success: true })),
      sendUnregisterSession: fn, sendRegisterDesktop: fn,
      sendUnregisterDesktop: vi.fn(async () => {}),
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

  it('groups the sidebar by desktop in the arrangement order', async () => {
    render(<App />);

    await waitFor(() => {
      expect(screen.getByTestId('sidebar').getAttribute('data-groups')).toBe(
        'Desktop 1=s1+s2,Desktop 2=,Desktop 3=s3',
      );
    });
  });

  it('mounts only the current desktop until the user leaves it', async () => {
    render(<App />);

    expect(await screen.findByTestId(desktopTestId('d1'))).toBeInTheDocument();
    expect(isActive('d1')).toBe(true);
    expect(screen.queryByTestId(desktopTestId('d2'))).toBeNull();
    expect(screen.queryByTestId(desktopTestId('d3'))).toBeNull();
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


  it('shows the focused tile again when the user comes back from Home to a desktop whose active leaf is a tile', async () => {
    render(<App />);
    await userEvent.click(await screen.findByTestId('select-d2'));
    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d2:tile-readme'));

    act(() => useSessionStore.getState().goToDashboard());
    expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('');
    await userEvent.click(screen.getByTestId('select-d2'));

    await waitFor(() => expect(screen.getByTestId('sidebar').getAttribute('data-selected-tile')).toBe('d2:tile-readme'));
    expect(desktopCommands.sendDesktopSetActivePane).not.toHaveBeenCalled();
  });

  it('offers the focused agent\'s directory as the desktop root only when it runs on this machine', async () => {
    render(<App />);
    await waitFor(() =>
      expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-desktop-directory')).toBe('/tmp/repo'),
    );

    act(() => {
      useSessionStore.setState((state) => ({
        sessions: state.sessions.map((entry) => (entry.id === 's1' ? { ...entry, endpointId: 'remote-box' } : entry)),
      }));
    });

    await waitFor(() =>
      expect(screen.getByTestId(desktopTestId('d1')).getAttribute('data-desktop-directory')).toBe(''),
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
