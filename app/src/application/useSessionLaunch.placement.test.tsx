import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider } from '../contexts/DaemonApiContext';
import { ptySpawn } from '../pty/bridge';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { createMockDaemonApi } from '../test/mocks/daemon';
import { LayoutPaneKind, LayoutPaneStatus, LayoutSplitDirection, type Desktop } from '../types/generated';
import { useSessionLaunch } from './useSessionLaunch';

vi.mock('../pty/bridge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../pty/bridge')>()),
  ptySpawn: vi.fn(async () => {}),
}));

function currentDesktop(panes: Array<[string, string]>, activePaneId: string): Desktop {
  return {
    id: 'desktop-1',
    profile_id: 'profile-1',
    name: '',
    order_key: 'i',
    revision: 1,
    tree_json: '',
    active_pane_id: activePaneId,
    panes: panes.map(([paneId, sessionId]) => ({
      desktop_id: 'desktop-1',
      pane_id: paneId,
      session_id: sessionId,
      runtime_id: sessionId,
      kind: LayoutPaneKind.Agent,
      status: LayoutPaneStatus.Ready,
      title: sessionId,
    })),
  };
}

function renderLaunch(
  shownAgentId: string | null = null,
  methods: Parameters<typeof createMockDaemonApi>[0] = {},
  showError: (message: string) => void = vi.fn(),
) {
  const api = createMockDaemonApi(methods);
  const wrapper = ({ children }: { children: ReactNode }) => <DaemonApiProvider api={api}>{children}</DaemonApiProvider>;
  return renderHook(
    () =>
      useSessionLaunch({
        settings: {},
        daemonEndpoints: [],
        sessions: useSessionStore((state) => state.sessions),
        shownAgentId,
        selectCreatedSession: vi.fn(() => true),
        showError,
      }),
    { wrapper },
  );
}

describe('useSessionLaunch placement', () => {
  beforeEach(() => {
    vi.mocked(ptySpawn).mockClear();
    useSessionStore.setState({ sessions: [] });
  });

  it('starts a split beside the focused agent on the current desktop, in its directory', async () => {
    await useSessionStore.getState().createSession('focused', '/repo/focused', 'agent-a', 'codex', undefined, false);
    useProfilesStore.setState({
      selectedProfileId: 'profile-1',
      currentDesktopId: 'desktop-1',
      desktops: [currentDesktop([['pane-a', 'agent-a']], 'pane-a')],
    });
    const { result } = renderLaunch();

    await act(async () => {
      await result.current.createSplitSession('codex', 'horizontal');
    });

    expect(vi.mocked(ptySpawn)).toHaveBeenCalledTimes(1);
    const { args } = vi.mocked(ptySpawn).mock.calls[0][0];
    expect(args).toMatchObject({
      cwd: '/repo/focused',
      spawned_from: 'agent-a',
      placement: { desktop_id: 'desktop-1', anchor_pane_id: 'pane-a', direction: LayoutSplitDirection.Horizontal },
    });
  });

  it('asks where to start when no agent is focused', async () => {
    useProfilesStore.setState({
      selectedProfileId: 'profile-1',
      currentDesktopId: 'desktop-1',
      desktops: [currentDesktop([], '')],
    });
    const { result } = renderLaunch();

    await act(async () => {
      await result.current.createSplitSession('codex', 'vertical');
    });

    expect(vi.mocked(ptySpawn)).not.toHaveBeenCalled();
    expect(result.current.locationPickerOpen).toBe(true);
    expect(result.current.locationPickerPurpose).toBe('session');
  });
});

describe('useSessionLaunch from the new-session picker', () => {
  beforeEach(() => {
    vi.mocked(ptySpawn).mockClear();
    useSessionStore.setState({ sessions: [] });
    useProfilesStore.setState({
      selectedProfileId: 'profile-1',
      currentDesktopId: 'desktop-1',
      desktops: [currentDesktop([], '')],
    });
  });

  it('starts an ordinary pick on the current desktop', async () => {
    const { result } = renderLaunch();

    await act(async () => {
      await result.current.handleLocationSelect('/repo/picked', 'shell');
    });

    const { args } = vi.mocked(ptySpawn).mock.calls[0][0];
    expect(args).toMatchObject({ cwd: '/repo/picked', placement: { desktop_id: 'desktop-1' } });
    expect(args.chief_of_staff).toBeFalsy();
  });

  it('launches a chief of staff into a new worktree when the picker asks for one', async () => {
    const { result } = renderLaunch(null, {
      sendCreateWorktree: vi.fn(async () => ({ success: true, path: '/repo/exsin--chief' })),
    });

    await act(async () => {
      result.current.handleCreateWorktreeSession('/repo/exsin', 'chief', 'main', undefined, 'shell', false, undefined, true);
      await vi.waitFor(() => expect(vi.mocked(ptySpawn)).toHaveBeenCalled());
    });

    const { args } = vi.mocked(ptySpawn).mock.calls[0][0];
    expect(args).toMatchObject({ cwd: '/repo/exsin--chief', chief_of_staff: true, placement: { desktop_id: 'desktop-1' } });
  });

  it('launches a chief of staff on the current desktop when the picker asks for one', async () => {
    const { result } = renderLaunch();

    await act(async () => {
      await result.current.handleLocationSelect('/repo/chief', 'shell', undefined, false, true);
    });

    const { args } = vi.mocked(ptySpawn).mock.calls[0][0];
    expect(args).toMatchObject({ cwd: '/repo/chief', chief_of_staff: true, placement: { desktop_id: 'desktop-1' } });
  });
});

describe('useSessionLaunch on a remote endpoint', () => {
  beforeEach(() => {
    vi.mocked(ptySpawn).mockClear();
    useSessionStore.setState({ sessions: [] });
  });

  it('refuses an agent on another daemon before spawning, naming the fence', async () => {
    await useSessionStore.getState().createSession('focused', '/repo/focused', 'agent-a', 'codex', undefined, false);
    useProfilesStore.setState({
      selectedProfileId: 'profile-1',
      currentDesktopId: 'desktop-1',
      desktops: [currentDesktop([['pane-a', 'agent-a']], 'pane-a')],
    });
    const showError = vi.fn();
    const { result } = renderLaunch(null, {}, showError);

    await act(async () => {
      await result.current.createSplitSession('codex', 'vertical', undefined, { cwd: '/remote/repo', endpointId: 'outpost-1' });
    });

    expect(ptySpawn).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith(expect.stringContaining('remote endpoints are off'));
    expect(useSessionStore.getState().sessions.map((session) => session.id)).toEqual(['agent-a']);
  });

  it('refuses a worktree launch before creating the worktree on the endpoint', async () => {
    useProfilesStore.setState({
      selectedProfileId: 'profile-1',
      currentDesktopId: 'desktop-1',
      desktops: [currentDesktop([], '')],
    });
    const sendCreateWorktree = vi.fn(async () => ({ success: true, path: '/remote/repo--feature' }));
    const showError = vi.fn();
    const { result } = renderLaunch(null, { sendCreateWorktree }, showError);

    act(() => {
      result.current.handleCreateWorktreeSession('/remote/repo', 'feature', 'main', 'outpost-1', 'codex', false);
    });

    expect(sendCreateWorktree).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith(expect.stringContaining('remote endpoints are off'));
    expect(result.current.sessionCreationJob).toBeNull();
  });
});

describe('useSessionLaunch from a remote agent', () => {
  beforeEach(() => {
    vi.mocked(ptySpawn).mockClear();
    useSessionStore.setState({ sessions: [] });
  });

  it('refuses to split a remote agent instead of leaving an unplaced session behind', async () => {
    await useSessionStore.getState().createSession('home', '/repo/home', 'agent-home', 'codex', undefined, false);
    await useSessionStore.getState().createSession('remote', '/remote/repo', 'agent-remote', 'codex', 'outpost-1', false);
    useProfilesStore.setState({
      selectedProfileId: 'profile-1',
      currentDesktopId: 'desktop-1',
      desktops: [currentDesktop([['pane-home', 'agent-home']], 'pane-home')],
    });
    const showError = vi.fn();
    const { result } = renderLaunch('agent-remote', {}, showError);

    await act(async () => {
      await result.current.createSplitSession('codex', 'vertical', 'pane-of-the-remote-agent');
    });

    expect(ptySpawn).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith(expect.stringContaining('remote endpoints are off'));
  });
});

