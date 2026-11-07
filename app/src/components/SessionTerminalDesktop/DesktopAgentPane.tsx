import { sessionDisplayName } from '../../utils/sessionDisplayName';
import { useRef, type CSSProperties } from 'react';
import type { TerminalDesktopState } from '../../types/desktop';
import { DesktopAgentBody } from './DesktopAgentBody';
import { DesktopAgentHeader } from './DesktopAgentHeader';
import { useDesktopContext } from './DesktopContext';
import { useTerminalRefusals } from '../../store/terminalRefusals';

export function DesktopAgentPane({
  agentPane,
  path,
  frameStyle,
}: {
  agentPane: TerminalDesktopState['agents'][number];
  path: string;
  frameStyle: CSSProperties;
}) {
  const {
    sessionById,
    activeLeafId,
    focusLeaf,
    effectiveDraggingLeafId,
  } = useDesktopContext();
  const refusal = useTerminalRefusals((state) => state.byTerminal.get(agentPane.runtimeId));

  const paneSession = sessionById.get(agentPane.sessionId);
  const lastSessionName = useRef<string | null>(null);
  if (paneSession) lastSessionName.current = sessionDisplayName(paneSession);
  const paneTitle = lastSessionName.current ?? (agentPane.title || 'Session');
  return (
    <div
      key={agentPane.id}
      className={`desktop-pane ${activeLeafId === agentPane.id ? 'active' : ''} ${effectiveDraggingLeafId === agentPane.id ? 'desktop-pane--dragging' : ''}`.trim()}
      role="group"
      aria-label={paneTitle}
      onMouseDown={() => focusLeaf(agentPane.id)}
      data-pane-session-id={agentPane.sessionId}
      data-pane-id={agentPane.id}
      data-offline={paneSession?.offline || undefined}
      data-pane-kind="agent"
      data-pane-path={path}
      style={frameStyle}
    >
      <DesktopAgentHeader agentPane={agentPane} paneSession={paneSession} paneTitle={paneTitle} />
      <DesktopAgentBody agentPane={agentPane} paneSession={paneSession} paneTitle={paneTitle} keysNotSent={refusal?.keysNotSent ?? 0} />
      {refusal?.flashing ? <div key={refusal.flash} className="desktop-pane-refusal" aria-hidden="true" onAnimationEnd={() => useTerminalRefusals.getState().flashEnded(agentPane.runtimeId)} /> : null}
    </div>
  );
}
