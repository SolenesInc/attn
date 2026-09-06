import { describe, expect, it, beforeEach, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';
import type { SidebarHeaderAction } from './components/Sidebar';

const mockUseSessionStore = vi.fn();
const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();

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
  Sidebar: ({ headerActions }: { headerActions: SidebarHeaderAction[] }) => (
    <div data-testid="sidebar">
      {headerActions.map((action) => (
        <button
          key={action.id}
          type="button"
          aria-label={action.title}
          data-dock-id={action.id}
          data-active={action.active ? 'true' : 'false'}
          onClick={action.onClick}
        />
      ))}
    </div>
  ),
}));

vi.mock('./components/ledger/LedgerSurface', () => ({
  LedgerSurface: ({ isOpen, tab, onClose }: { isOpen: boolean; tab: string; onClose: () => void }) => (
    isOpen ? <button type="button" data-testid="sessions-panel" data-tab={tab} onClick={onClose}>close</button> : null
  ),
}));

vi.mock('./components/Dashboard', () => ({ Dashboard: () => null }));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({ LocationPicker: () => null }));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
vi.mock('./components/SessionTerminalWorkspace', () => ({ SessionTerminalWorkspace: () => null }));
vi.mock('./components/ErrorToast', () => ({
  ErrorToast: () => null,
  useErrorToast: () => ({ message: null, showError: vi.fn(), clearError: vi.fn() }),
}));
vi.mock('./hooks/useKeyboardShortcuts', () => ({ useKeyboardShortcuts: vi.fn() }));
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

function sessionsButton() {
  return screen.getByRole('button', { name: /^Open Sessions \(/ });
}

describe('sessions dock button', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);

    mockUseSessionStore.mockReturnValue({
      sessions: [],
      activeSessionId: null,
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: vi.fn(),
      setActiveSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    mockUseDaemonStore.mockImplementation(() => ({
      daemonSessions: [],
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
      sendNotificationList: vi.fn(async () => ({
        notifications: [], unreadCount: 0, critical: { count: 0, title: '' },
      })),
      sendNotificationMarkRead: vi.fn(async () => 0),
      sendSessionList: vi.fn(async () => ({ entries: [], hasMore: false })),
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
      sendSetTerminalTheme: fn,
    });
  });

  it('opens the sessions panel and tracks its open state', async () => {
    const user = userEvent.setup();
    render(<App />);

    expect(sessionsButton()).toHaveAttribute('data-active', 'false');
    expect(screen.queryByTestId('sessions-panel')).not.toBeInTheDocument();

    await user.click(sessionsButton());

    expect(screen.getByTestId('sessions-panel')).toBeInTheDocument();
    expect(sessionsButton()).toHaveAttribute('data-active', 'true');

    await user.click(screen.getByTestId('sessions-panel'));

    expect(screen.queryByTestId('sessions-panel')).not.toBeInTheDocument();
    expect(sessionsButton()).toHaveAttribute('data-active', 'false');
  });

  it('sits next to the worktrees button, which opens the same surface on its other list', async () => {
    const user = userEvent.setup();
    await act(async () => {
      render(<App />);
    });

    const ids = Array.from(screen.getByTestId('sidebar').querySelectorAll('button'))
      .map((button) => button.getAttribute('data-dock-id'));
    expect(ids).toContain('sessions');
    expect(ids.indexOf('worktrees')).toBe(ids.indexOf('sessions') + 1);

    await user.click(screen.getByRole('button', { name: 'Open Worktrees' }));
    expect(screen.getByTestId('sessions-panel')).toHaveAttribute('data-tab', 'worktrees');
    expect(screen.getByRole('button', { name: 'Open Worktrees' })).toHaveAttribute('data-active', 'true');
    expect(sessionsButton()).toHaveAttribute('data-active', 'false');
  });
});
