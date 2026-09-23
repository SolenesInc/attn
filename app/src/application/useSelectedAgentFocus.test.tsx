import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { createMockDaemonApi } from '../test/mocks/daemon';
import { LayoutPaneKind, LayoutPaneStatus, type Desktop } from '../types/generated';
import { useSelectedAgentFocus } from './useSelectedAgentFocus';

function desktop(activePaneId: string, id = 'desktop-1', panes: Array<[string, string]> = [['pane-a', 'agent-a'], ['pane-b', 'agent-b']]): Desktop {
  return {
    id,
    profile_id: 'profile-1',
    name: '',
    order_key: 'i',
    revision: 1,
    tree_json: '',
    active_pane_id: activePaneId,
    panes: panes.map(([paneId, sessionId]) => ({
      desktop_id: id,
      pane_id: paneId,
      session_id: sessionId,
      kind: LayoutPaneKind.Agent,
      status: LayoutPaneStatus.Ready,
      title: sessionId,
    })),
  };
}

describe('useSelectedAgentFocus', () => {
  const sendDesktopSetActivePane = vi.fn(async () => ({}) as never);
  const sendDesktopSetCurrent = vi.fn(async () => ({}) as never);

  beforeEach(() => {
    sendDesktopSetActivePane.mockClear();
    sendDesktopSetCurrent.mockClear();
    useProfilesStore.setState({ selectedProfileId: 'profile-1', currentDesktopId: 'desktop-1', desktops: [desktop('pane-a')] });
  });

  function renderFocus(initial: string | null) {
    const api = createMockDaemonApi({ sendDesktopSetActivePane, sendDesktopSetCurrent });
    const wrapper = ({ children }: { children: ReactNode }) => <DaemonApiProvider api={api}>{children}</DaemonApiProvider>;
    return renderHook(({ selected }) => useSelectedAgentFocus(selected), { wrapper, initialProps: { selected: initial } });
  }

  it('focuses the pane of the agent the user selects, once', () => {
    const { rerender } = renderFocus('agent-a');
    expect(sendDesktopSetActivePane).not.toHaveBeenCalled();

    rerender({ selected: 'agent-b' });
    expect(sendDesktopSetActivePane.mock.calls).toEqual([['desktop-1', 'pane-b']]);
  });

  it('leaves focus to another client when only the arrangement changes', () => {
    renderFocus('agent-a');
    act(() => {
      useProfilesStore.setState({ desktops: [desktop('pane-b')] });
    });
    expect(sendDesktopSetActivePane).not.toHaveBeenCalled();
  });

  it('switches to the desktop of an agent selected elsewhere, then focuses its pane', async () => {
    useProfilesStore.setState({
      desktops: [desktop('pane-a'), desktop('pane-c', 'desktop-2', [['pane-c', 'agent-c']])],
    });
    const { rerender } = renderFocus('agent-a');
    await act(async () => {
      rerender({ selected: 'agent-c' });
    });
    expect(sendDesktopSetCurrent.mock.calls).toEqual([['profile-1', 'desktop-2']]);
    expect(sendDesktopSetActivePane.mock.calls).toEqual([['desktop-2', 'pane-c']]);
  });
});
