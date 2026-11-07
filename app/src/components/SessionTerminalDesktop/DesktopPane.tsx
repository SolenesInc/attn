import { useDesktopContext } from './DesktopContext';
import { DesktopAgentPane } from './DesktopAgentPane';
import { DesktopTilePane } from './DesktopTilePane';

export function DesktopPane({ paneId }: { paneId: string }) {
  const { panePaths, renderedPaneBounds, paneFrameStyle, agentPaneById, tileLeafById } =
    useDesktopContext();
  const tileLeaf = tileLeafById.get(paneId);
  const bounds = renderedPaneBounds.get(paneId);
  if (!bounds) return tileLeaf ? <DesktopTilePane tileLeaf={tileLeaf} /> : null;
  const path = panePaths.get(paneId) || 'root';
  const frameStyle = paneFrameStyle(bounds);
  const agentPane = agentPaneById.get(paneId);
  if (agentPane)
    return (
      <DesktopAgentPane
        agentPane={agentPane}
        path={path}
        frameStyle={frameStyle}
      />
    );
  return tileLeaf ? (
    <DesktopTilePane tileLeaf={tileLeaf} placement={{ bounds, path, frameStyle }} />
  ) : null;
}
