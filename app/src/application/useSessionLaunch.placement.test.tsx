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
  ptySpawn: vi.fn(async () => undefined),
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
      kind: LayoutPaneKind.Agent,
      status: LayoutPaneStatus.Ready,
      title: sessionId,
    })),
  };
}

function renderLaunch() {
  const api = createMockDaemonApi({});
  const wrapper = ({ children }: { children: ReactNode }) => <DaemonApiProvider api={api}>{children}</DaemonApiProvider>;
  return renderHook(
    () =>
      useSessionLaunch({
        settings: {},
        daemonSessions: [],
        daemonEndpoints: [],
        sessions: useSessionStore((state) => state.sessions),
        activeSessionId: null,
        selectCreatedSession: vi.fn(() => true),
        showError: vi.fn(),
      }),
    { wrapper },
  );
}

describe('useSessionLaunch placement', () => {
  beforeEach(() => {
    vi.mocked(ptySpawn).mockClear();
    useSessionStore.setState({ sessions: [], activeSessionId: null });
  });

  it('starts a split beside the focused agent on the current desktop, in its directory', async () => {
    await useSessionStore.getState().createSession('focused', '/repo/focused', 'agent-a', 'codex', undefined, false, undefined);
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
