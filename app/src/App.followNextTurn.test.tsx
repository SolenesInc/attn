import { describe, expect, it, beforeEach, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import App from './App';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';


const mockUseSessionStore = vi.fn();
const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();
const mockUseKeyboardShortcuts = vi.fn();

const { mockSetActiveSession } = vi.hoisted(() => ({
  mockSetActiveSession: vi.fn(),
}));

let turnOwed: Record<string, boolean>;
let activeSessionId: string | null;

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
  Sidebar: () => null,
}));

vi.mock('./components/Dashboard', () => ({ Dashboard: ({ onSurf }: { onSurf: () => void }) => <button onClick={onSurf}>Go surfing</button> }));
vi.mock('./components/SurfBreak/SurfBreak', () => ({
  SurfIcon: () => null,
  SurfBreak: ({ waitingCount, onReturnToWaiting }: { waitingCount: number; onReturnToWaiting: () => void }) => (
    <button onClick={onReturnToWaiting}>{waitingCount} agents waiting in Swell</button>
  ),
}));
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
vi.mock('./hooks/useUIScale', () => ({
  useUIScale: () => ({ scale: 1, increaseScale: vi.fn(), decreaseScale: vi.fn(), resetScale: vi.fn() }),
}));
vi.mock('./hooks/useOpenPR', () => ({ useOpenPR: () => vi.fn() }));
vi.mock('./hooks/usePRsNeedingAttention', () => ({ usePRsNeedingAttention: () => ({ needsAttention: [] }) }));
vi.mock('./store/sessions', () => ({ useSessionStore: () => mockUseSessionStore() }));
vi.mock('./store/daemonSessions', () => ({ useDaemonStore: () => mockUseDaemonStore() }));
vi.mock('./hooks/useDaemonSocket', () => ({
  useDaemonSocket: (args: unknown) => mockUseDaemonSocket(args),
}));
vi.mock('./pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('./pty/bridge')>('./pty/bridge');
  return { ...actual, ptySpawn: vi.fn(async () => {}) };
});

type SocketArgs = {
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

function workspacePayload() {
  return ['s1', 's2'].map((id) => ({
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
  });
}

function selections(): string[] {
  return mockSetActiveSession.mock.calls.map((call) => call[0] as string);
}

/** Selecting the last owed agent and settling it: home, with the wait armed. */
function workTheQueueDownToHome() {
  render(<App />);
  broadcast();

  act(() => { mockSetActiveSession('s1'); });
  broadcast();
  expect(activeSessionId).toBe('s1');

  turnOwed.s1 = false;
  broadcast();
  expect(activeSessionId).toBeNull();
}

describe('waiting at home for the next turn', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    turnOwed = { s1: true, s2: false };
    activeSessionId = null;

    mockSetActiveSession.mockImplementation((id: string | null) => { activeSessionId = id; });

    mockUseSessionStore.mockImplementation(() => ({
      sessions: ['s1', 's2'].map((id) => ({
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
      activeSessionId,
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: vi.fn(),
      setActiveSession: mockSetActiveSession,
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    }));

    mockUseDaemonStore.mockImplementation(() => ({
      daemonSessions: ['s1', 's2'].map((id) => ({
        id,
        label: id,
        directory: `/tmp/${id}`,
        state: 'working',
        turn_owed: turnOwed[id],
        turn_opened_at: id === 's1' ? '2026-08-03T09:00:00Z' : '2026-08-03T10:00:00Z',
      })),
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
      sendSessionSelected: fn, sendWorkspaceSelected: fn,
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

  it('takes the user to the next turn that opens after the queue ran dry', () => {
    workTheQueueDownToHome();

    turnOwed.s2 = true;
    broadcast();

    expect(activeSessionId).toBe('s2');
  });

  it('disarms the automatic return while surfing and lets the user return explicitly', () => {
    workTheQueueDownToHome();
    fireEvent.click(screen.getByRole('button', { name: 'Go surfing' }));
    turnOwed.s2 = true;
    broadcast();
    expect(activeSessionId).toBeNull();
    expect(shortcutHandlers<{ enabled: boolean }>().enabled).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: '1 agents waiting in Swell' }));
    expect(activeSessionId).toBe('s2');
    expect(screen.queryByText('1 agents waiting in Swell')).toBeNull();
  });

  it('leaves the user alone at a home they walked to', () => {
    render(<App />);
    broadcast();
    act(() => { mockSetActiveSession('s1'); });
    broadcast();

    const shortcuts = shortcutHandlers<{ onGoToDashboard: () => void }>();
    act(() => { shortcuts.onGoToDashboard(); });
    broadcast();
    expect(activeSessionId).toBeNull();

    const before = selections().length;
    turnOwed.s2 = true;
    broadcast();

    expect(activeSessionId).toBeNull();
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

    expect(activeSessionId).toBeNull();
    expect(selections()).toHaveLength(before);
  });

  it('hands over the oldest owed turn when several opened while home waited', () => {
    workTheQueueDownToHome();

    turnOwed.s1 = true;
    turnOwed.s2 = true;
    broadcast();

    expect(activeSessionId).toBe('s1');
  });
});
