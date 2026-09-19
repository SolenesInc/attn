import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useSessionStore, type Session } from '../store/sessions';
import { useSessionWorkspaceController } from './useSessionWorkspaceController';
import type { SessionTerminalWorkspaceHandle } from '../components/SessionTerminalWorkspace';

function session(id: string): Session {
  return {
    id,
    label: id,
    state: 'idle',
    cwd: '/tmp/repo',
    workspaceId: `workspace-${id}`,
    agent: 'claude',
    transcriptMatched: true,
    workspace: {
      agents: [{ id: `pane-${id}`, runtimeId: id, sessionId: id, title: id }],
      layoutTree: { type: 'pane', paneId: `pane-${id}` },
    },
    daemonActivePaneId: `pane-${id}`,
  };
}

beforeEach(() =>
  useSessionStore.setState(
    {
      ...useSessionStore.getInitialState(),
      sessions: [session('a'), session('b')],
    },
    true,
  ),
);

function controller(
  focusPane: ReturnType<typeof vi.fn<SessionTerminalWorkspaceHandle['focusPane']>>,
) {
  const hook = renderHook(() => {
    const { sessions, activeSessionId } = useSessionStore();
    return useSessionWorkspaceController(sessions, activeSessionId);
  });
  for (const session of useSessionStore.getState().sessions) {
    const handle: Partial<SessionTerminalWorkspaceHandle> = { focusPane };
    hook.result.current.setWorkspaceRef(session.workspaceId)(
      handle as SessionTerminalWorkspaceHandle,
    );
  }
  return hook;
}

describe('navigation focus', () => {
  it('focuses the committed selection once, even when session metadata changes', () => {
    const focus = vi.fn<SessionTerminalWorkspaceHandle['focusPane']>();
    const { rerender } = controller(focus);
    act(() => {
      expect(useSessionStore.getState().selectAgent('b')).toBe(true);
    });
    expect(focus).toHaveBeenCalledExactlyOnceWith('pane-b', 40);
    act(() =>
      useSessionStore.setState((state) => ({
        sessions: state.sessions.map((s) => ({ ...s, label: 'renamed' })),
      })),
    );
    rerender();
    expect(focus).toHaveBeenCalledTimes(1);
  });

  it('never focuses an intermediate selection superseded before commit', () => {
    const focus = vi.fn<SessionTerminalWorkspaceHandle['focusPane']>();
    controller(focus);
    act(() => {
      useSessionStore.getState().selectAgent('a');
      useSessionStore.getState().selectAgent('b');
    });
    expect(focus).toHaveBeenCalledExactlyOnceWith('pane-b', 40);
  });

  it('does not focus a selection cancelled by Home before commit', () => {
    const focus = vi.fn<SessionTerminalWorkspaceHandle['focusPane']>();
    controller(focus);
    act(() => {
      useSessionStore.getState().selectAgent('a');
      useSessionStore.getState().goToDashboard();
    });
    expect(focus).not.toHaveBeenCalled();
  });
});
