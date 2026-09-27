import { DesktopRenamePopover } from './DesktopRenamePopover';
import { useDesktopContext } from './DesktopContext';
import { DesktopDragOverlays } from './DesktopDragOverlays';
import { DesktopFocusBar } from './DesktopFocusBar';
import { DesktopLayoutRenderer } from './DesktopLayoutRenderer';
import { DesktopPane } from './DesktopPane';

function renderPaneSurface(paneId: string) {
  return <DesktopPane key={paneId} paneId={paneId} />;
}

export function DesktopSurface() {
  const {
    desktopId,
    desktopSelectionStyle,
    activeAgentPaneId,
    panesContainerRef,
    agentPaneById,
    activeLeafId,
    effectivePaneId,
    effectiveZoomedPaneId,
    renderedLayoutTree,
    renderedPaneIds,
    sessionVisible,
    effectiveDraggingLeafId,
    effectiveGhostPos,
    draggingLeafLabel,
    splitDividers,
    handleDividerPointerDown,
  } = useDesktopContext();
  if (!renderedLayoutTree) {
    return (
      <div
        className="session-terminal-desktop"
        data-session-terminal-desktop={desktopId}
        data-desktop-id={desktopId}
        data-active-pane-id=""
        data-active-leaf-id=""
        data-maximized-pane-id=""
        data-session-visible={sessionVisible ? '1' : '0'}
        data-zoomed-pane-id=""
      />
    );
  }

  return (
    <div
      className={`session-terminal-desktop desktop-selection--${desktopSelectionStyle} ${effectivePaneId ? 'focus-mode' : ''} ${effectivePaneId && agentPaneById.has(effectivePaneId) ? 'agent-focus-mode' : ''} ${effectiveZoomedPaneId && !effectivePaneId ? 'zoom-mode' : ''} ${renderedPaneIds.length > 1 ? 'multi-leaf' : ''}`
        .trim()
        .replace(/\s+/g, ' ')}
      data-session-terminal-desktop={desktopId}
      data-desktop-id={desktopId}
      data-active-pane-id={activeAgentPaneId}
      data-active-leaf-id={activeLeafId}
      data-maximized-pane-id={effectivePaneId || ''}
      data-session-visible={sessionVisible ? '1' : '0'}
      data-zoomed-pane-id={effectiveZoomedPaneId || ''}
    >
      <DesktopFocusBar />
      <DesktopLayoutRenderer
        layoutTree={renderedLayoutTree}
        paneIds={renderedPaneIds}
        renderPane={renderPaneSurface}
        containerRef={panesContainerRef}
        dividers={splitDividers}
        onDividerPointerDown={handleDividerPointerDown}
        overlay={
          <>
            <DesktopDragOverlays />
          </>
        }
      />
      {effectiveGhostPos && effectiveDraggingLeafId && (
        <div
          className="desktop-dock-ghost"
          style={{ left: effectiveGhostPos.x + 12, top: effectiveGhostPos.y + 12 }}
        >
          {draggingLeafLabel}
        </div>
      )}
      <DesktopRenamePopover />
    </div>
  );
}
