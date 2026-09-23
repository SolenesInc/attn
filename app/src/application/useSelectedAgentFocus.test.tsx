import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { createMockDaemonApi } from '../test/mocks/daemon';
import { LayoutPaneKind, LayoutPaneStatus, type Desktop } from '../types/generated';
import { useSelectedAgentFocus } from './useSelectedAgentFocus';

function desktop(activePaneId: string): Desktop {
  return {
    id: 'desktop-1',
    profile_id: 'profile-1',
    name: '',
    order_key: 'i',
    revision: 1,
    tree_json: '',
    active_pane_id: activePaneId,
    panes: [
      ['pane-a', 'agent-a'],
      ['pane-b', 'agent-b'],
    ].map(([paneId, sessionId]) => ({
      desktop_id: 'desktop-1',
      pane_id: paneId,
      session_id: sessionId,
      kind: LayoutPaneKind.Agent,
      status: LayoutPaneStatus.Ready,
      title: sessionId,
    })),
  };
}

describe('useSelectedAgentFocus', () => {
  const sendDesktopFocusSession = vi.fn(async () => ({}) as never);

  beforeEach(() => {
    sendDesktopFocusSession.mockClear();
    useProfilesStore.setState({ selectedProfileId: 'profile-1', currentDesktopId: 'desktop-1', desktops: [desktop('pane-a')] });
  });

  function renderFocus(initial: string | null) {
    const api = createMockDaemonApi({ sendDesktopFocusSession });
    const wrapper = ({ children }: { children: ReactNode }) => <DaemonApiProvider api={api}>{children}</DaemonApiProvider>;
    return renderHook(({ selected }) => useSelectedAgentFocus(selected), { wrapper, initialProps: { selected: initial } });
  }

  it('asks the daemon to focus each agent the user selects that is not already focused', () => {
    const { rerender } = renderFocus('agent-a');
    expect(sendDesktopFocusSession).not.toHaveBeenCalled();

    rerender({ selected: 'agent-b' });
    rerender({ selected: 'agent-in-another-profile' });
    expect(sendDesktopFocusSession.mock.calls).toEqual([['agent-b'], ['agent-in-another-profile']]);
  });

  it('leaves focus to another client when only the arrangement changes', () => {
    renderFocus('agent-a');
    act(() => {
      useProfilesStore.setState({ desktops: [desktop('pane-b')] });
    });
    expect(sendDesktopFocusSession).not.toHaveBeenCalled();
  });
});
