import { describe, expect, it, beforeEach, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import App from './App';
import { useSessionStore } from './store/sessions';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';


const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();
const mockUseKeyboardShortcuts = vi.fn();
const mockUseUiAutomationBridge = vi.fn();

const { mockSetActiveSession } = vi.hoisted(() => ({
  mockSetActiveSession: vi.fn(),
}));

let turnOwed: Record<string, boolean>;
let sessionIds: string[];

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async () => () => {}),
  getCurrent: vi.fn(async () => []),
}));
vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

vi.mock('./components/GhosttyTerminal', async () => {
  const React = await import('react');
  return { GhosttyTerminal: React.forwardRef(function MockTerminal() { return null; }) };
});

vi.mock('./components/Sidebar', async () => {
  const { DelegationChainTrigger } = await import('./components/DelegationChain');
  return {
    EditorIcon: () => null,
    WorkflowIcon: () => null,
    DiffIcon: () => null,
    PRsIcon: () => null,
    NotebookIcon: () => null,
    MarkdownIcon: () => null,
    Sidebar: () => <DelegationChainTrigger session={{ id: 's1', label: 's1', delegation_role: { name: 'Builder' } }} />,
  };
});

vi.mock('./components/Dashboard', () => ({ Dashboard: () => null }));
vi.mock('./components/grid/GridView', () => ({ GridView: () => null }));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({ LocationPicker: () => null }));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
vi.mock('./components/SessionTerminalWorkspace', () => ({ SessionTerminalWorkspace: () => null }));
vi.mock('./components/ErrorToast', () => ({
  ErrorToast: () => null,
  useErrorToast: () => ({ message: null, showError: vi.fn(), clearError: vi.fn() }),
}));
vi.mock('./hooks/useKeyboardShortcuts', () => ({
  useKeyboardShortcuts: (args: unknown) => mockUseKeyboardShortcuts(args),
}));
vi.mock('./hooks/useUiAutomationBridge', () => ({
  useUiAutomationBridge: (args: unknown) => mockUseUiAutomationBridge(args),
}));
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

type SocketArgs = {
  onSessionsUpdate?: (sessions: unknown[]) => void;
  onWorkspacesUpdate?: (workspaces: unknown[]) => void;
  onSettingsUpdate?: (settings: Record<string, string>) => void;
};

function socketArgs(): SocketArgs {
  const calls = mockUseDaemonSocket.mock.calls;
  return calls[calls.length - 1]?.[0] as SocketArgs;
}

/** The shortcut handlers App registered on its last render. */
function shortcutHandlers<T>(): T {
  const calls = mockUseKeyboardShortcuts.mock.calls;
  return calls[calls.length - 1]?.[0] as T;
}

function selectSession(): (id: string) => void {
  return mockUseUiAutomationBridge.mock.lastCall![0].selectSession;
}

function workspacePayload() {
  return sessionIds.map((id) => ({
    id: `workspace-${id}`,
    title: id,
    directory: `/tmp/${id}`,
    status: 'active',
    layout: {
      active_pane_id: `pane-${id}`,
      layout_json: JSON.stringify({ type: 'pane', pane_id: `pane-${id}` }),
      panes: [{
        workspace_id: `workspace-${id}`,
        pane_id: `pane-${id}`,
        kind: 'agent',
        runtime_id: id,
        session_id: id,
        title: id,
      }],
    },
  }));
}

function broadcast() {
  act(() => {
    socketArgs().onSettingsUpdate?.({ queue_mode_enabled: 'true' });
    socketArgs().onWorkspacesUpdate?.(workspacePayload());
    socketArgs().onSessionsUpdate?.(mockUseDaemonStore().daemonSessions);
  });
}

function selections(): string[] {
  return useSessionStore.getState().agentHistory.entries;
}

/** Selecting the last owed agent and settling it: home, with the wait armed. */
function workTheQueueDownToHome() {
  render(<App />);
  broadcast();

  act(() => { mockSetActiveSession('s1'); });
  broadcast();
  expect(useSessionStore.getState().activeSessionId).toBe('s1');

  turnOwed.s1 = false;
  broadcast();
  expect(useSessionStore.getState().activeSessionId).toBeNull();
}

describe('agent navigation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useSessionStore.setState(useSessionStore.getInitialState(), true);
    localStorage.clear();
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    turnOwed = { s1: true, s2: false };
    sessionIds = ['s1', 's2'];

    mockSetActiveSession.mockImplementation((id: string | null) => useSessionStore.getState().setActiveSession(id));

    useSessionStore.setState({
      sessions: sessionIds.map((id) => ({
        id,
        label: id,
        state: 'working',
        cwd: `/tmp/${id}`,
        workspaceId: `workspace-${id}`,
        agent: 'claude',
        transcriptMatched: true,
        daemonActivePaneId: `pane-${id}`,
        workspace: {
          agents: [{ id: `pane-${id}`, runtimeId: id, sessionId: id, title: id }],
          layoutTree: { type: 'pane', paneId: `pane-${id}` },
        },
      })),
      activeSessionId: null,
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
    });

    mockUseDaemonStore.mockImplementation(() => ({
      daemonSessions: sessionIds.map((id) => ({
        id,
        label: id,
        directory: `/tmp/${id}`,
        workspace_id: `workspace-${id}`,
        agent: 'claude',
        state: 'working',
        turn_owed: turnOwed[id],
        turn_opened_at: id === 's1' ? '2026-08-03T09:00:00Z' : '2026-08-03T10:00:00Z',
      })),
      crew: [],
      setDaemonSessions: vi.fn(),
      prs: [], setPRs: vi.fn(),
      repoStates: [], setRepoStates: vi.fn(),
      authorStates: [], setAuthorStates: vi.fn(),
      seeds: [], setSeeds: vi.fn(),
    }));

    const fn = vi.fn();
    mockUseDaemonSocket.mockReturnValue({
      sendPRAction: fn, sendMutePR: fn, sendMuteRepo: fn, sendMuteAuthor: fn, sendPRVisited: fn,
      sendRefreshPRs: vi.fn(async () => ({ success: true })),
      sendUnregisterSession: vi.fn(async () => {}),
      sendRegisterWorkspace: fn,
      sendUnregisterWorkspace: vi.fn(async () => {}),
      sendMuteWorkspace: vi.fn(async () => ({ success: true })),
      sendSetSetting: fn,
      sendSetClientPresence: fn,
      sendCreateWorktree: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendDeleteWorktree: vi.fn(async () => ({ success: true })),
      sendGetRecentLocations: vi.fn(async () => ({ success: true, locations: [] })),
      sendCreateWorktreeFromBranch: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendFetchRemotes: vi.fn(async () => ({ success: true })),
      sendFetchPRDetails: vi.fn(async () => ({ success: true })),
      sendEnsureRepo: vi.fn(async () => ({ success: true, path: '/tmp/repo' })),
      sendSubscribeGitStatus: fn, sendUnsubscribeGitStatus: fn,
      sendSessionList: vi.fn(async () => ({ entries: [], omitted: 0 })),
      sendWorkspaceClosePane: vi.fn(async () => ({ success: true })),
      sendWorkspaceAddSessionPane: vi.fn(async () => ({ success: true })),
      requestTileContent: fn,
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

  it('keeps a newer selection when an old callback queues a now-ready session', () => {
    sessionIds = ['s1'];
    useSessionStore.setState({ sessions: useSessionStore.getState().sessions.filter(s => s.id === 's1') });
    const app = render(<App />);
    broadcast();
    const beforeCreation = selectSession();

    sessionIds = ['s1', 's2'];
    broadcast();
    app.rerender(<App />);
    act(() => { beforeCreation('s2'); });
    act(() => { selectSession()('s1'); });

    expect(useSessionStore.getState().activeSessionId).toBe('s1');
  });

  it.each(['onGoToDashboard', 'onHistoryBack', 'onToggleSidebar'] as const)('dismisses the chain when %s changes navigation', (shortcut) => {
    useSessionStore.getState().selectAgent('s2');
    useSessionStore.getState().selectAgent('s1');
    const app = render(<App />);
    broadcast();
    fireEvent.click(screen.getByTestId('delegation-chain-trigger-s1'));
    expect(screen.getByRole('dialog', { name: 'Delegation chain' })).toBeInTheDocument();
    act(() => { shortcutHandlers<Record<typeof shortcut, () => void>>()[shortcut](); });
    app.rerender(<App />);
    expect(screen.queryByRole('dialog', { name: 'Delegation chain' })).toBeNull();
  });

  it.each(['onOpenSettings', 'onShowShortcuts', 'onOpenSessions'] as const)('dismisses the chain when %s opens another surface', (shortcut) => {
    useSessionStore.getState().selectAgent('s2');
    useSessionStore.getState().selectAgent('s1');
    render(<App />);
    broadcast();
    fireEvent.click(screen.getByTestId('delegation-chain-trigger-s1'));
    expect(screen.getByRole('dialog', { name: 'Delegation chain' })).toBeInTheDocument();
    act(() => { shortcutHandlers<Record<typeof shortcut, () => void>>()[shortcut](); });
    expect(screen.queryByRole('dialog', { name: 'Delegation chain' })).toBeNull();
  });

  it('selects a deferred session when its pane becomes available', () => {
    sessionIds = ['s1'];
    useSessionStore.setState({ sessions: useSessionStore.getState().sessions.filter(s => s.id === 's1') });
    const app = render(<App />);
    broadcast();
    act(() => { selectSession()('s2'); });

    sessionIds = ['s1', 's2'];
    broadcast();
    app.rerender(<App />);

    expect(useSessionStore.getState().activeSessionId).toBe('s2');
  });

  it('does not leave home when an older deferred selection becomes ready', () => {
    sessionIds = ['s1'];
    useSessionStore.setState({ sessions: useSessionStore.getState().sessions.filter(s => s.id === 's1') });
    const app = render(<App />);
    broadcast();
    const beforeCreation = selectSession();
    sessionIds = ['s1', 's2'];
    broadcast();
    app.rerender(<App />);
    act(() => { beforeCreation('s2'); });

    act(() => { shortcutHandlers<{ onGoToDashboard: () => void }>().onGoToDashboard(); });
    broadcast();

    expect(useSessionStore.getState().activeSessionId).toBeNull();
  });

  it('takes the user to the next turn that opens after the queue ran dry', () => {
    workTheQueueDownToHome();

    turnOwed.s2 = true;
    broadcast();

    expect(useSessionStore.getState().activeSessionId).toBe('s2');
  });

  it('leaves the user alone at a home they walked to', () => {
    render(<App />);
    broadcast();
    act(() => { mockSetActiveSession('s1'); });
    broadcast();

    const shortcuts = shortcutHandlers<{ onGoToDashboard: () => void }>();
    act(() => { shortcuts.onGoToDashboard(); });
    broadcast();
    expect(useSessionStore.getState().activeSessionId).toBeNull();

    const before = selections().length;
    turnOwed.s2 = true;
    broadcast();

    expect(useSessionStore.getState().activeSessionId).toBeNull();
    expect(selections()).toHaveLength(before);
  });

  it('ends the wait when the user leaves home, however they come back', () => {
    workTheQueueDownToHome();

    const shortcuts = shortcutHandlers<{ onToggleGridMode?: () => void }>();
    act(() => { shortcuts.onToggleGridMode?.(); });
    broadcast();
    act(() => { shortcuts.onToggleGridMode?.(); });
    broadcast();

    const before = selections().length;
    turnOwed.s2 = true;
    broadcast();

    expect(useSessionStore.getState().activeSessionId).toBeNull();
    expect(selections()).toHaveLength(before);
  });

  it('hands over the oldest owed turn when several opened while home waited', () => {
    workTheQueueDownToHome();

    turnOwed.s1 = true;
    turnOwed.s2 = true;
    broadcast();

    expect(useSessionStore.getState().activeSessionId).toBe('s1');
  });

  it('resumes history from dashboard and grid, then traverses normally in the session view', () => {
    useSessionStore.getState().selectAgent('s1');
    useSessionStore.getState().selectAgent('s2');
    useSessionStore.getState().goToDashboard();
    render(<App />);

    let shortcuts = shortcutHandlers<{
      onHistoryBack: () => void;
      onHistoryForward: () => void;
      onToggleGridMode: () => void;
    }>();
    act(() => { shortcuts.onHistoryBack(); });
    expect(useSessionStore.getState().activeSessionId).toBe('s2');
    expect(useSessionStore.getState().agentHistory.cursor).toBe(1);

    shortcuts = shortcutHandlers();
    act(() => { shortcuts.onToggleGridMode(); });
    shortcuts = shortcutHandlers();
    act(() => { shortcuts.onHistoryForward(); });
    expect(useSessionStore.getState().activeSessionId).toBe('s2');
    expect(useSessionStore.getState().view).toBe('grid');
    act(() => { shortcutHandlers<{ onHistoryBack: () => void }>().onHistoryBack(); });
    expect(useSessionStore.getState().view).toBe('session');

    shortcuts = shortcutHandlers();
    act(() => { shortcuts.onHistoryBack(); });
    expect(useSessionStore.getState().activeSessionId).toBe('s1');
    expect(useSessionStore.getState().agentHistory.entries).toEqual(['s1', 's2']);
  });
});
