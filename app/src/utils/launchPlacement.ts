import { LayoutPaneKind, LayoutSplitDirection, type Desktop, type SessionPlacement } from '../types/generated';

export interface LaunchTarget {
  placement: SessionPlacement;
  focusedSessionId: string | null;
}

export function launchTarget(
  desktop: Desktop,
  direction: 'vertical' | 'horizontal',
  requestedAnchorPaneId?: string,
): LaunchTarget {
  const onDesktop = requestedAnchorPaneId && desktop.panes.some((pane) => pane.pane_id === requestedAnchorPaneId);
  const anchor = onDesktop ? requestedAnchorPaneId : desktop.active_pane_id;
  const focused = desktop.panes.find(
    (pane) => pane.pane_id === desktop.active_pane_id && pane.kind === LayoutPaneKind.Agent,
  );
  return {
    placement: {
      desktop_id: desktop.id,
      direction: direction === 'horizontal' ? LayoutSplitDirection.Horizontal : LayoutSplitDirection.Vertical,
      ...(anchor ? { anchor_pane_id: anchor } : {}),
    },
    focusedSessionId: focused?.session_id || null,
  };
}
