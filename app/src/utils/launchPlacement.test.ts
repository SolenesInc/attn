import { describe, expect, it } from 'vitest';
import { LayoutPaneKind, LayoutPaneStatus, LayoutSplitDirection, type Desktop } from '../types/generated';
import { launchTarget } from './launchPlacement';

function desktop(activePaneId: string, panes: Array<[string, string]>): Desktop {
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

describe('launchTarget', () => {
  it('splits beside the active pane and names its agent as the focused one', () => {
    const target = launchTarget(desktop('pane-b', [['pane-a', 'agent-a'], ['pane-b', 'agent-b']]), 'horizontal');
    expect(target).toEqual({
      placement: { desktop_id: 'desktop-1', direction: LayoutSplitDirection.Horizontal, anchor_pane_id: 'pane-b' },
      focusedSessionId: 'agent-b',
    });
  });

  it('keeps a requested anchor of this desktop and replaces one from elsewhere with the active pane', () => {
    const current = desktop('pane-b', [['pane-a', 'agent-a'], ['pane-b', 'agent-b']]);
    expect(launchTarget(current, 'vertical', 'pane-a').placement.anchor_pane_id).toBe('pane-a');
    expect(launchTarget(current, 'vertical', 'pane-from-a-workspace').placement.anchor_pane_id).toBe('pane-b');
  });

  it('places on an empty desktop without an anchor or a focused agent', () => {
    expect(launchTarget(desktop('', []), 'vertical')).toEqual({
      placement: { desktop_id: 'desktop-1', direction: LayoutSplitDirection.Vertical },
      focusedSessionId: null,
    });
  });
});
