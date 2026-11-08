import { DesktopRenamePopover } from './DesktopRenamePopover';
import { ShelfWidthLabel } from './DesktopShelf';
import { ShelfPeekPanel } from './ShelfPeekPanel';
import { useDesktopContext } from './DesktopContext';
import { DesktopDragOverlays } from './DesktopDragOverlays';
import { DesktopLayoutRenderer } from './DesktopLayoutRenderer';
import { DesktopPane } from './DesktopPane';
import { DesktopReviewTabs } from './DesktopReviewTabs';

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
    offScreenBrowserIds,
    poppedLeafIds,
    sessionVisible,
    effectiveDraggingLeafId,
    effectiveGhostPos,
    draggingLeafLabel,
    splitDividers,
    handleRenderedDividerPointerDown,
    handleColumnDividerDoubleClick,
    shelfWidthLabel,
  } = useDesktopContext();
  if (!renderedLayoutTree && poppedLeafIds.length === 0) {
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
      <DesktopReviewTabs />
      {shelfWidthLabel && <ShelfWidthLabel desktopId={desktopId} clearedWidths={shelfWidthLabel.clearedWidths} tileCounts={shelfWidthLabel.tileCounts} />}
      <ShelfPeekPanel />
      <DesktopLayoutRenderer
        layoutTree={renderedLayoutTree}
        paneIds={[...renderedPaneIds, ...offScreenBrowserIds, ...poppedLeafIds]}
        renderPane={renderPaneSurface}
        containerRef={panesContainerRef}
        dividers={splitDividers}
        onDividerPointerDown={handleRenderedDividerPointerDown}
        onDividerDoubleClick={handleColumnDividerDoubleClick}
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
