import type { CSSProperties, ReactNode } from 'react';
import { MovablePane } from '../../tileWindow/MovablePane';
import { tileWindowKey } from '../../tileWindow/pool';
import type { NormalizedPaneBounds } from '../../types/desktop';
import { useDesktopContext } from './DesktopContext';
import { DesktopAgentPane } from './DesktopAgentPane';
import { DesktopTilePane } from './DesktopTilePane';

const WINDOW_BOUNDS: NormalizedPaneBounds = { left: 0, top: 0, right: 1, bottom: 1, width: 1, height: 1, centerX: 0.5, centerY: 0.5 };
const WINDOW_FRAME: CSSProperties = { position: 'absolute', inset: 0 };

function PaneContent({ paneId }: { paneId: string }): ReactNode {
  const { panePaths, renderedPaneBounds, paneFrameStyle, agentPaneById, tileLeafById, poppedLeafIdSet } =
    useDesktopContext();
  const tileLeaf = tileLeafById.get(paneId);
  const agentPane = agentPaneById.get(paneId);
  if (poppedLeafIdSet.has(paneId)) {
    if (agentPane) return <DesktopAgentPane agentPane={agentPane} path="window" frameStyle={WINDOW_FRAME} />;
    return tileLeaf ? (
      <DesktopTilePane tileLeaf={tileLeaf} placement={{ bounds: WINDOW_BOUNDS, path: 'window', frameStyle: WINDOW_FRAME, popped: true }} />
    ) : null;
  }
  const bounds = renderedPaneBounds.get(paneId);
  if (!bounds) return tileLeaf ? <DesktopTilePane tileLeaf={tileLeaf} /> : null;
  const path = panePaths.get(paneId) || 'root';
  const frameStyle = paneFrameStyle(bounds);
  if (agentPane) return <DesktopAgentPane agentPane={agentPane} path={path} frameStyle={frameStyle} />;
  return tileLeaf ? (
    <DesktopTilePane tileLeaf={tileLeaf} placement={{ bounds, path, frameStyle }} />
  ) : null;
}

export function DesktopPane({ paneId }: { paneId: string }) {
  const { desktopId, poppedLeafIdSet } = useDesktopContext();
  return (
    <MovablePane leafKey={tileWindowKey(desktopId, paneId)} popped={poppedLeafIdSet.has(paneId)}>
      <PaneContent paneId={paneId} />
    </MovablePane>
  );
}
