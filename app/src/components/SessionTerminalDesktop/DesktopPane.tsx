import { useDesktopContext } from './DesktopContext';
import { DesktopAgentPane } from './DesktopAgentPane';
import { DesktopTilePane } from './DesktopTilePane';

export function DesktopPane({ paneId }: { paneId: string }) {
  const { panePaths, renderedPaneBounds, paneFrameStyle, agentPaneById, tileLeafById } =
    useDesktopContext();
  const bounds = renderedPaneBounds.get(paneId);
  if (!bounds) return null;
  const path = panePaths.get(paneId) || 'root';
  const frameStyle = paneFrameStyle(bounds);
  const agentPane = agentPaneById.get(paneId);
  if (agentPane)
    return (
      <DesktopAgentPane
        agentPane={agentPane}
        bounds={bounds}
        path={path}
        frameStyle={frameStyle}
      />
    );
  const tileLeaf = tileLeafById.get(paneId);
  return tileLeaf ? (
    <DesktopTilePane tileLeaf={tileLeaf} bounds={bounds} path={path} frameStyle={frameStyle} />
  ) : null;
}
