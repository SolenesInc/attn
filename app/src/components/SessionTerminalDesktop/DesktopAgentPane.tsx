import { SuspendedDesktopPane } from './SuspendedDesktopPane';
import type { CSSProperties } from 'react';
import type { NormalizedPaneBounds, TerminalDesktopState } from '../../types/desktop';
import { StateIndicator } from '../StateIndicator';
import { DesktopAgentBody } from './DesktopAgentBody';
import { DesktopAgentHeader } from './DesktopAgentHeader';
import { useDesktopContext } from './DesktopContext';

export function DesktopAgentPane({
  agentPane,
  bounds,
  path,
  frameStyle,
}: {
  agentPane: TerminalDesktopState['agents'][number];
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
  } = useDesktopContext();

  const paneSession = sessionById.get(agentPane.sessionId);
  const paneTitle = paneSession?.label || agentPane.title || 'Session';
  if (suspendedLeafIds.has(agentPane.id) && !effectivePaneId) {
    return (
      <SuspendedDesktopPane
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
          <span className="desktop-suspended-state" />
        )}
      </SuspendedDesktopPane>
    );
  }
  return (
    <div
      key={agentPane.id}
      className={`desktop-pane ${activeLeafId === agentPane.id ? 'active' : ''} ${effectiveDraggingLeafId === agentPane.id ? 'desktop-pane--dragging' : ''}`.trim()}
      role="group"
      aria-label={paneTitle}
      onMouseDown={() => focusLeaf(agentPane.id)}
      data-pane-session-id={agentPane.sessionId}
      data-pane-id={agentPane.id}
      data-pane-kind="agent"
      data-pane-path={path}
      style={frameStyle}
    >
      <DesktopAgentHeader agentPane={agentPane} paneSession={paneSession} paneTitle={paneTitle} />
      <DesktopAgentBody agentPane={agentPane} paneSession={paneSession} paneTitle={paneTitle} />
    </div>
  );
}
