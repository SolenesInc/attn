import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Session } from '../store/sessions';
import { useAgentNavigation } from './useAgentNavigation';

function session(id: string, paneId = `pane-${id}`): Session {
  return {
    id,
    label: id,
    state: 'idle',
    cwd: '/tmp/repo',
    workspaceId: `workspace-${id}`,
    agent: 'claude',
    transcriptMatched: true,
    workspace: {
      agents: [{ id: paneId, runtimeId: id, sessionId: id, title: id }],
      layoutTree: { type: 'pane', paneId },
    },
    daemonActivePaneId: paneId,
  };
}

function controller(overrides: Partial<Parameters<typeof useAgentNavigation>[0]> = {}) {
  const options = {
    sessions: [session('a'), session('b')],
    setActiveSession: vi.fn(),
    navigateAgentHistory: vi.fn(() => null as string | null),
    setActivePane: vi.fn(),
    focusSessionPane: vi.fn(),
    revealSessionView: vi.fn(),
    requestTerminalFocus: vi.fn(),
    ...overrides,
  };
  return { options, ...renderHook(() => useAgentNavigation(options)) };
}

describe('useAgentNavigation', () => {
  it('selects a session-backed pane and runs the reveal/focus sequence', () => {
    const { result, options } = controller();

    let selected = false;
    act(() => { selected = result.current.selectAgent('a'); });

    expect(selected).toBe(true);
    expect(options.setActivePane).toHaveBeenCalledWith('a', 'pane-a');
    expect(options.setActiveSession).toHaveBeenCalledWith('a');
    expect(options.revealSessionView).toHaveBeenCalledOnce();
    expect(options.requestTerminalFocus).toHaveBeenCalledOnce();
    expect(options.focusSessionPane).toHaveBeenCalledWith('a', 'pane-a', 40);
  });

  it('rejects unknown sessions and panes without changing state', () => {
    const { result, options } = controller();

    let selected = true;
    let selectedPane = true;
    act(() => {
      selected = result.current.selectAgent('missing');
      selectedPane = result.current.selectAgentPane('a', 'pane-b');
    });

    expect(selected).toBe(false);
    expect(selectedPane).toBe(false);
    expect(options.setActivePane).not.toHaveBeenCalled();
    expect(options.setActiveSession).not.toHaveBeenCalled();
    expect(options.revealSessionView).not.toHaveBeenCalled();
    expect(options.requestTerminalFocus).not.toHaveBeenCalled();
    expect(options.focusSessionPane).not.toHaveBeenCalled();
  });

  it('selects an explicitly named pane only when it belongs to the session', () => {
    const pane = session('a', 'pane-explicit');
    const { result, options } = controller({ sessions: [pane] });

    let selected = false;
    act(() => { selected = result.current.selectAgentPane('a', 'pane-explicit'); });

    expect(selected).toBe(true);
    expect(options.setActivePane).toHaveBeenCalledWith('a', 'pane-explicit');
    expect(options.setActiveSession).toHaveBeenCalledWith('a');
  });

  it('traverses history and focuses the returned pane without recording a visit', () => {
    const navigateAgentHistory = vi.fn(() => 'b');
    const { result, options } = controller({ navigateAgentHistory });

    let moved = false;
    act(() => { moved = result.current.back(true); });

    expect(moved).toBe(true);
    expect(navigateAgentHistory).toHaveBeenCalledWith('back', true);
    expect(options.setActivePane).toHaveBeenCalledWith('b', 'pane-b');
    expect(options.setActiveSession).not.toHaveBeenCalled();
    expect(options.revealSessionView).toHaveBeenCalledOnce();
    expect(options.requestTerminalFocus).toHaveBeenCalledOnce();
    expect(options.focusSessionPane).toHaveBeenCalledWith('b', 'pane-b', 40);
  });

  it('returns false at a history boundary', () => {
    const { result, options } = controller();

    let moved = true;
    act(() => { moved = result.current.forward(); });

    expect(moved).toBe(false);
    expect(options.navigateAgentHistory).toHaveBeenCalledWith('forward', false);
    expect(options.setActivePane).not.toHaveBeenCalled();
    expect(options.revealSessionView).not.toHaveBeenCalled();
  });
});
