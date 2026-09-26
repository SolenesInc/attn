import type { ComponentProps, ReactNode } from 'react';
import type { SidebarWorkspace } from './sidebarTypes';
import type { TileLeaf } from '../types/workspace';
import { type UISessionState } from '../types/sessionState';
import { tileContentKey } from '../types/workspace';
import './Sidebar.css';
import { useSidebarContext } from './SidebarContext';
import { isSessionless, workspaceShortcut } from './sidebarModel';
import { SidebarSessionRow, TileSidebarRow } from './SidebarRows';
import { StateIndicator } from './StateIndicator';

export function SidebarWorkspaceList() {
  const {
    onRenameWorkspace,
    onSessionDragStart,
    onSelectWorkspace,
    openRename,
    visibleWorkspaces,
    visualIndexOfWorkspace,
    reorderDrag,
    draggingSessionId,
    reorderSeamIndexByWorkspaceId,
    reorderTrailingSeamIndex,
    lastReorderParticipantId,
    renderReorderSeam,
    handleHeaderPointerDown,
    handleHeaderClickCapture,
    handleSessionPointerDown,
    handleSessionClickCapture,
  } = useSidebarContext();
  return (
    <>
      {visibleWorkspaces.map((workspace) => {
        const workspaceIndex = visualIndexOfWorkspace(workspace.id);
        const seamIndex = reorderSeamIndexByWorkspaceId?.get(workspace.id);
        const isReorderSource = reorderDrag?.workspaceId === workspace.id;
        return (
          <div className="workspace-row" key={`${workspace.endpointId || 'local'}:${workspace.id}`}>
            {seamIndex !== undefined && renderReorderSeam(seamIndex)}
            <WorkspaceDropGroup workspace={workspace} reorderSource={isReorderSource}>
              <div className="workspace-group-header">
                <button
                  type="button"
                  className="sidebar-row-select"
                  aria-label={`Open workspace ${workspace.title}`}
                  onPointerDown={(event) => handleHeaderPointerDown(workspace, event)}
                  onClickCapture={handleHeaderClickCapture}
                  onClick={() => onSelectWorkspace(workspace.id)}
                />
                {isSessionless(workspace) ? (
                  <span
                    className="workspace-neutral-indicator"
                    data-testid="workspace-neutral-indicator"
                    title={
                      workspace.hasUnresolvedAgentPanes
                        ? 'Workspace has a pane without an active session'
                        : 'Tile-only workspace — no active session'
                    }
                  />
                ) : (
                  <StateIndicator
                    state={(workspace.status as UISessionState | undefined) || 'idle'}
                    size="md"
                    seed={workspace.id}
                  />
                )}
                <span className="workspace-label">{workspace.title}</span>
                {workspace.endpointId && workspace.sessions[0]?.endpointName && (
                  <span
                    className={`session-endpoint-badge status-${workspace.sessions[0].endpointStatus || 'connected'}`}
                  >
                    {workspace.sessions[0].endpointName}
                  </span>
                )}
                {workspaceShortcut(workspaceIndex) && (
                  <span className="session-shortcut">{workspaceShortcut(workspaceIndex)}</span>
                )}
                {onRenameWorkspace && (
                  <span className="workspace-actions">
                    <button
                      type="button"
                      className="workspace-action-btn rename-workspace-btn"
                      data-testid={`rename-workspace-${workspace.id}`}
                      onClick={(e) => openRename('workspace', workspace.id, workspace.title, e)}
                      title="Rename workspace"
                      aria-label={`Rename workspace ${workspace.title}`}
                    >
                      ✎
                    </button>
                  </span>
                )}
              </div>
              {workspace.children.map((child) => {
                if (child.kind === 'tile') {
                  return (
                    <WorkspaceTileRow key={child.id} workspaceId={workspace.id} tile={child.tile} />
                  );
                }
                const session = child.session;
                const paneId = child.paneId;
                const draggable = Boolean(paneId && onSessionDragStart);
                return (
                  <WorkspaceSessionRow
                    key={session.id}
                    session={session}
                    draggable={draggable}
                    dragging={draggingSessionId === session.id}
                    onClickCapture={draggable ? handleSessionClickCapture : undefined}
                    onPointerDown={
                      draggable && paneId
                        ? (event) =>
                            handleSessionPointerDown(
                              workspace,
                              paneId,
                              session.id,
                              session.label,
                              event,
                            )
                        : undefined
                    }
                  />
                );
              })}
            </WorkspaceDropGroup>
            {workspace.id === lastReorderParticipantId &&
              renderReorderSeam(reorderTrailingSeamIndex)}
          </div>
        );
      })}
    </>
  );
}

export function SidebarAutomationGroups() {
  const { expandedAutomationGroups, automationGroups, toggleAutomationGroup } = useSidebarContext();
  return (
    <>
      {automationGroups.map((group) => {
        const expanded = expandedAutomationGroups.has(group.id);
        return (
          <div
            className="automation-session-group"
            data-testid={`sidebar-automation-${group.id}`}
            data-automation-id={group.id}
            key={group.id}
          >
            <button
              type="button"
              className="automation-session-header"
              data-testid={`sidebar-automation-header-${group.id}`}
              aria-expanded={expanded}
              onClick={() => toggleAutomationGroup(group.id)}
            >
              <span className={`automation-session-chevron ${expanded ? 'expanded' : ''}`}>▸</span>
              <span className="automation-session-name">{group.name}</span>
              <span className="automation-session-count">
                {group.sessions.length} {group.sessions.length === 1 ? 'agent' : 'agents'}
              </span>
            </button>
            {expanded && (
              <div className="automation-session-list">
                {group.sessions.map((session) => (
                  <WorkspaceSessionRow key={session.id} session={session} />
                ))}
              </div>
            )}
          </div>
        );
      })}
    </>
  );
}

function WorkspaceDropGroup({
  workspace,
  reorderSource = false,
  children,
}: {
  workspace: SidebarWorkspace;
  reorderSource?: boolean;
  children: ReactNode;
}) {
  const {
    selectedWorkspaceId,
    workspaceDragClass,
    canAcceptLeafDrag,
    onWorkspaceDragEnter,
    onWorkspaceDragLeave,
    onWorkspaceDragDrop,
  } = useSidebarContext();
  return (
    <div
      className={`workspace-group ${selectedWorkspaceId === workspace.id ? 'selected' : ''}${reorderSource ? ' workspace-group--reorder-source' : ''}${workspaceDragClass(workspace)}`}
      data-testid={`sidebar-workspace-${workspace.id}`}
      onPointerEnter={() => {
        if (canAcceptLeafDrag(workspace)) onWorkspaceDragEnter?.(workspace);
      }}
      onPointerLeave={() => {
        if (canAcceptLeafDrag(workspace)) onWorkspaceDragLeave?.(workspace);
      }}
      onPointerUp={() => {
        if (canAcceptLeafDrag(workspace)) onWorkspaceDragDrop?.(workspace);
      }}
    >
      {children}
    </div>
  );
}

function WorkspaceTileRow({
  workspaceId,
  tile,
}: {
  workspaceId: string;
  tile: TileLeaf;
}) {
  const { tileContents, selectedTile, onSelectTile, onCloseTile, onReloadTile } =
    useSidebarContext();
  return (
    <TileSidebarRow
      workspaceId={workspaceId}
      tile={tile}
      content={tileContents[tileContentKey(workspaceId, tile.tileId)]}
      selected={selectedTile?.workspaceId === workspaceId && selectedTile.tileId === tile.tileId}
      onSelect={() => onSelectTile?.(workspaceId, tile.tileId)}
      onClose={() => onCloseTile?.(workspaceId, tile.tileId)}
      onReload={() => onReloadTile?.(workspaceId, tile.tileId)}
    />
  );
}

function WorkspaceSessionRow(
  props: Omit<
    ComponentProps<typeof SidebarSessionRow>,
    'selected' | 'onSelect' | 'onOpenActions' | 'onTriggerNudge' | 'showSettling' | 'delegates'
  >,
) {
  const {
    selectedId,
    onSelectSession,
    openSessionActions,
    onTriggerNudge,
    onScreenSessionIds,
    rowDelegation,
  } = useSidebarContext();
  const { session } = props;
  return (
    <SidebarSessionRow
      {...props}
      selected={selectedId === session.id}
      onSelect={() => onSelectSession(session.id)}
      onOpenActions={(event) => openSessionActions(session, event)}
      onTriggerNudge={() => onTriggerNudge?.(session.id)}
      showSettling={!onScreenSessionIds?.has(session.id)}
      {...rowDelegation(session)}
    />
  );
}
