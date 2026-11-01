import { useRef } from 'react';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import { StateIndicator } from '../StateIndicator';
import { DesktopAgentSignals } from './DesktopAgentSignals';
import { DesktopAgentSubline } from './DesktopAgentSubline';
import { useDesktopContext } from './DesktopContext';
import { HEADLINE_RANK, TITLE_MINIMUM } from './headerFit';
import { useHeaderFit } from './useHeaderFit';
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
  const headlineRef = useRef<HTMLDivElement>(null);
  const headline = useHeaderFit(headlineRef, HEADLINE_RANK, { reserve: TITLE_MINIMUM });
  return (
    <div
      className={`desktop-pane-header ${
        showPaneHeader ? 'desktop-pane-header--draggable' : 'desktop-pane-header--static'
      }`.trim()}
      onPointerDown={showPaneHeader ? (event) => beginLeafDrag(agentPane.id, event) : undefined}
      title={showPaneHeader ? 'Drag to move' : undefined}
    >
      <div ref={headlineRef} className="desktop-pane-headline">
        {paneSession?.state ? (
          <span className="desktop-pane-dot" data-fit-fixed>
            <StateIndicator state={paneSession.state} offline={paneSession.offline} size="sm" seed={agentPane.sessionId} />
          </span>
        ) : null}
        <span className="desktop-pane-title-slot">
          <span className="desktop-pane-title">{paneTitle}</span>
          <span className="desktop-pane-title-actions">
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
                    name: paneSession.label,
                    defaultName: paneSession.agent === 'shell' ? paneTitle : undefined,
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
          </span>
        </span>
        <DesktopAgentSignals agentPane={agentPane} paneSession={paneSession} levels={headline} />
      </div>
      <DesktopAgentSubline agentPane={agentPane} paneSession={paneSession} paneTitle={paneTitle} />
    </div>
  );
}
