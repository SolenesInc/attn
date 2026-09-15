import { useWorkspaceContext } from './WorkspaceContext';
import { WorkspaceAgentPane } from './WorkspaceAgentPane';
import { WorkspaceTilePane } from './WorkspaceTilePane';

export function WorkspacePane({ paneId }: { paneId: string }) {
  const { panePaths, renderedPaneBounds, paneFrameStyle, agentPaneById, tileLeafById } =
    useWorkspaceContext();
  const bounds = renderedPaneBounds.get(paneId);
  if (!bounds) return null;
  const path = panePaths.get(paneId) || 'root';
  const frameStyle = paneFrameStyle(bounds);
  const agentPane = agentPaneById.get(paneId);
  if (agentPane)
    return (
      <WorkspaceAgentPane
        agentPane={agentPane}
        bounds={bounds}
        path={path}
        frameStyle={frameStyle}
      />
    );
  const tileLeaf = tileLeafById.get(paneId);
  return tileLeaf ? (
    <WorkspaceTilePane tileLeaf={tileLeaf} bounds={bounds} path={path} frameStyle={frameStyle} />
  ) : null;
}
