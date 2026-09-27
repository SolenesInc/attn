import { describe, expect, it, beforeEach, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';
import { useProfilesStore } from './store/profiles';
import { useSessionStore } from './store/sessions';
import { agentDesktop, arrangeDesktops, fakeDesktopCommands, TEST_PROFILE_ID } from './test/desktops';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';


const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();
const mockUseKeyboardShortcuts = vi.fn();

const { mockShowError, mockSendUnregisterSession } = vi.hoisted(() => ({
  mockShowError: vi.fn(),
  mockSendUnregisterSession: vi.fn(async () => {}),
}));

let chiefOfStaff: boolean;
let crewMember: string | undefined;

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
  Sidebar: ({ onCloseSession }: { onCloseSession: (id: string) => void }) => (
    <button data-testid="close-session" onClick={() => onCloseSession('s1')}>
      Close Session
    </button>
  ),
}));

vi.mock('./components/Dashboard', () => ({ Dashboard: () => null }));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({ LocationPicker: () => null }));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
vi.mock('./components/SessionTerminalWorkspace', () => ({ SessionTerminalWorkspace: () => null }));
vi.mock('./components/Toast', () => ({
  Toast: () => null,
  useToast: () => ({ toast: null, showError: mockShowError, showNotice: vi.fn(), clearToast: vi.fn() }),
}));
vi.mock('./hooks/useKeyboardShortcuts', () => ({
  useKeyboardShortcuts: (args: unknown) => mockUseKeyboardShortcuts(args),
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

function triggerCmdW() {
  const calls = mockUseKeyboardShortcuts.mock.calls;
  const args = calls[calls.length - 1]?.[0] as { onCloseSession?: () => void };
  act(() => {
    args.onCloseSession?.();
  });
}

describe('chief and crew sessions are protected from close', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useSessionStore.setState(useSessionStore.getInitialState(), true);
    useProfilesStore.setState(useProfilesStore.getInitialState(), true);
    arrangeDesktops([agentDesktop('desktop-s1', 1, ['s1'])]);
    localStorage.clear();
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    chiefOfStaff = false;
    crewMember = undefined;

    useSessionStore.setState({
      sessions: [{
        id: 's1',
        label: 'orchestrator',
        state: 'working',
        cwd: '/tmp/repo',
        workspaceId: '',
        profileId: TEST_PROFILE_ID,
        desktopId: 'desktop-s1',
        agent: 'claude',
        transcriptMatched: true,
        daemonActivePaneId: 'pane-s1',
        desktop: {
          agents: [{ id: 'pane-s1', runtimeId: 's1', sessionId: 's1', title: 'orchestrator' }],
          layoutTree: { type: 'pane', paneId: 'pane-s1' },
        },
      }],
      activeSessionId: 's1',
      view: 'session',
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
    });

    mockUseDaemonStore.mockImplementation(() => ({
      daemonSessions: [{
        id: 's1',
        label: 'orchestrator',
        directory: '/tmp/repo',
        state: 'working',
        chief_of_staff: chiefOfStaff,
        crew_member: crewMember,
      }],
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
      sendUnregisterSession: mockSendUnregisterSession,
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
      ...fakeDesktopCommands(),
      sendWorkspaceAddSessionPane: vi.fn(async () => ({ success: true })),
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

  it('no-ops the close button on the chief session and shows the protected hint', async () => {
    chiefOfStaff = true;
    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    expect(mockSendUnregisterSession).not.toHaveBeenCalled();
    expect(mockShowError).toHaveBeenCalledWith(expect.stringContaining('Chief of staff is protected'));
  });

  it('no-ops the ⌘W shortcut on the chief session and shows the protected hint', async () => {
    chiefOfStaff = true;
    render(<App />);

    triggerCmdW();

    expect(mockSendUnregisterSession).not.toHaveBeenCalled();
    expect(mockShowError).toHaveBeenCalledWith(expect.stringContaining('Chief of staff is protected'));
  });

  it('no-ops the close button on a crew member and points to Sleep', async () => {
    crewMember = 'coda';
    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    expect(mockSendUnregisterSession).not.toHaveBeenCalled();
    expect(mockShowError).toHaveBeenCalledWith('Coda is protected — put Coda to sleep to close the day.');
  });

  it('no-ops the ⌘W shortcut on a crew member and points to Sleep', async () => {
    crewMember = 'coda';
    render(<App />);

    triggerCmdW();

    expect(mockSendUnregisterSession).not.toHaveBeenCalled();
    expect(mockShowError).toHaveBeenCalledWith('Coda is protected — put Coda to sleep to close the day.');
  });

  it('closes an ordinary session normally and shows no hint', async () => {
    chiefOfStaff = false;
    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    expect(mockSendUnregisterSession).toHaveBeenCalledExactlyOnceWith('s1');
    expect(mockShowError).not.toHaveBeenCalled();
  });
});
