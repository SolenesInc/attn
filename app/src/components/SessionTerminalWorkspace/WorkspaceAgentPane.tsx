import { SuspendedWorkspacePane } from './SuspendedWorkspacePane';
import type { CSSProperties } from 'react';
import type { NormalizedPaneBounds, TerminalWorkspaceState } from '../../types/workspace';
import { StateIndicator } from '../StateIndicator';
import { WorkspaceAgentBody } from './WorkspaceAgentBody';
import { WorkspaceAgentHeader } from './WorkspaceAgentHeader';
import { useWorkspaceContext } from './WorkspaceContext';

export function WorkspaceAgentPane({
  agentPane,
  bounds,
  path,
  frameStyle,
}: {
  agentPane: TerminalWorkspaceState['agents'][number];
  bounds: NormalizedPaneBounds;
  path: string;
  frameStyle: CSSProperties;
}) {
  const {
    sessionById,
    activeLeafId,
    effectivePaneId,
    suspendedLeafIds,
    focusLeaf,
    effectiveDraggingLeafId,
  } = useWorkspaceContext();

  const paneSession = sessionById.get(agentPane.sessionId);
  const paneTitle = paneSession?.label || agentPane.title || 'Session';
  if (suspendedLeafIds.has(agentPane.id) && !effectivePaneId) {
    return (
      <SuspendedWorkspacePane
        leafId={agentPane.id}
        title={paneTitle}
        kind="agent"
        sessionId={agentPane.sessionId}
        bounds={bounds}
        path={path}
        frameStyle={frameStyle}
      >
        {paneSession?.state ? (
          <StateIndicator state={paneSession.state} size="sm" seed={agentPane.sessionId} />
        ) : (
          <span className="workspace-suspended-state" />
        )}
      </SuspendedWorkspacePane>
    );
  }
  return (
    <div
      key={agentPane.id}
      className={`workspace-pane ${activeLeafId === agentPane.id ? 'active' : ''} ${effectiveDraggingLeafId === agentPane.id ? 'workspace-pane--dragging' : ''}`.trim()}
      role="group"
      aria-label={paneTitle}
      onMouseDown={() => focusLeaf(agentPane.id)}
      data-pane-session-id={agentPane.sessionId}
      data-pane-id={agentPane.id}
      data-pane-kind="agent"
      data-pane-path={path}
      style={frameStyle}
    >
      <WorkspaceAgentHeader agentPane={agentPane} paneSession={paneSession} paneTitle={paneTitle} />
      <WorkspaceAgentBody agentPane={agentPane} paneSession={paneSession} paneTitle={paneTitle} />
    </div>
  );
}
