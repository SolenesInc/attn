import { formatShortcut } from '../../shortcuts/formatShortcut';
import { StateIndicator } from '../StateIndicator';
import { DesktopAgentIdentity } from './DesktopAgentIdentity';
import { DesktopAgentSignals } from './DesktopAgentSignals';
import { useDesktopContext } from './DesktopContext';
import type { DesktopAgentProps } from './desktopTypes';

export function DesktopAgentHeader({ agentPane, paneSession, paneTitle }: DesktopAgentProps) {
  const {
    onRenameSession,
    onSetZoomActive,
    setRenamePane,
    showPaneHeader,
    effectivePaneId,
    setMaximizedLeafId,
    focusLeaf,
    beginLeafDrag,
  } = useDesktopContext();
  return (
    <div
      className={`desktop-pane-header ${
        showPaneHeader ? 'desktop-pane-header--draggable' : 'desktop-pane-header--static'
      }`.trim()}
      onPointerDown={showPaneHeader ? (event) => beginLeafDrag(agentPane.id, event) : undefined}
      title={showPaneHeader ? 'Drag to move' : undefined}
    >
      {/* The same dot the sidebar puts beside this session. */}
      {paneSession?.state ? (
        <StateIndicator state={paneSession.state} size="sm" seed={agentPane.sessionId} />
      ) : null}
      <DesktopAgentIdentity
        agentPane={agentPane}
        paneSession={paneSession}
        paneTitle={paneTitle}
      />
      {onRenameSession && paneSession ? (
        <button
          type="button"
          className="desktop-pane-rename-btn"
          data-testid={`rename-pane-${agentPane.id}`}
          onPointerDown={(event) => event.stopPropagation()}
          onClick={(event) => {
            event.stopPropagation();
            const rect = event.currentTarget.getBoundingClientRect();
            setRenamePane({
              sessionId: agentPane.sessionId,
              name: paneTitle,
              anchor: { top: rect.bottom + 4, left: rect.left },
            });
          }}
          title="Rename session"
          aria-label={`Rename session ${paneTitle}`}
        >
          ✎
        </button>
      ) : null}
      {effectivePaneId !== agentPane.id ? (
        <button
          type="button"
          className="desktop-pane-focus-btn"
          data-testid={`focus-pane-${agentPane.id}`}
          onPointerDown={(event) => event.stopPropagation()}
          onClick={(event) => {
            event.stopPropagation();
            focusLeaf(agentPane.id);
            onSetZoomActive?.(false);
            setMaximizedLeafId(agentPane.id);
          }}
          title={`Focus agent (${formatShortcut('terminal.toggleMaximize')})`}
          aria-label={`Focus agent ${paneTitle}`}
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path d="M6 2.5H2.5V6M10 2.5h3.5V6M6 13.5H2.5V10M10 13.5h3.5V10" />
          </svg>
        </button>
      ) : null}
      <DesktopAgentSignals
        agentPane={agentPane}
        paneSession={paneSession}
        paneTitle={paneTitle}
      />
    </div>
  );
}
