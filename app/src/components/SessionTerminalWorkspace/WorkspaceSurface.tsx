import { WorkspaceRenamePopover } from './WorkspaceRenamePopover';
import { useWorkspaceContext } from './WorkspaceContext';
import { WorkspaceDragOverlays } from './WorkspaceDragOverlays';
import { WorkspaceFocusBar } from './WorkspaceFocusBar';
import { WorkspaceLayoutRenderer } from './WorkspaceLayoutRenderer';
import { WorkspacePane } from './WorkspacePane';

function renderPaneSurface(paneId: string) {
  return <WorkspacePane key={paneId} paneId={paneId} />;
}

export function WorkspaceSurface() {
  const {
    workspaceId,
    workspaceSelectionStyle,
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
  } = useWorkspaceContext();
  if (!renderedLayoutTree) {
    return (
      <div
        className="session-terminal-workspace"
        data-session-terminal-workspace={workspaceId}
        data-workspace-id={workspaceId}
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
      className={`session-terminal-workspace workspace-selection--${workspaceSelectionStyle} ${effectivePaneId ? 'focus-mode' : ''} ${effectivePaneId && agentPaneById.has(effectivePaneId) ? 'agent-focus-mode' : ''} ${effectiveZoomedPaneId && !effectivePaneId ? 'zoom-mode' : ''} ${renderedPaneIds.length > 1 ? 'multi-leaf' : ''}`
        .trim()
        .replace(/\s+/g, ' ')}
      data-session-terminal-workspace={workspaceId}
      data-workspace-id={workspaceId}
      data-active-pane-id={activeAgentPaneId}
      data-active-leaf-id={activeLeafId}
      data-maximized-pane-id={effectivePaneId || ''}
      data-session-visible={sessionVisible ? '1' : '0'}
      data-zoomed-pane-id={effectiveZoomedPaneId || ''}
    >
      <WorkspaceFocusBar />
      <WorkspaceLayoutRenderer
        layoutTree={renderedLayoutTree}
        paneIds={renderedPaneIds}
        renderPane={renderPaneSurface}
        containerRef={panesContainerRef}
        dividers={splitDividers}
        onDividerPointerDown={handleDividerPointerDown}
        overlay={
          <>
            <WorkspaceDragOverlays />
          </>
        }
      />
      {effectiveGhostPos && effectiveDraggingLeafId && (
        <div
          className="workspace-dock-ghost"
          style={{ left: effectiveGhostPos.x + 12, top: effectiveGhostPos.y + 12 }}
        >
          {draggingLeafLabel}
        </div>
      )}
      <WorkspaceRenamePopover />
    </div>
  );
}
