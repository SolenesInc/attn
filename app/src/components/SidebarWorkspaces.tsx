import type { ComponentProps, ReactNode } from 'react';
import type { SidebarWorkspace } from './sidebarTypes';
import type { TileLeaf } from '../types/workspace';
import { type UISessionState } from '../types/sessionState';
import { tileContentKey } from '../types/workspace';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { DelegatedFromChiefBadge } from './DelegatedFromChiefBadge';
import { DelegationChainTrigger } from './DelegationChain';
import './Sidebar.css';
import { useSidebarContext } from './SidebarContext';
import { isSessionless, workspaceShortcut } from './sidebarModel';
import { SidebarSessionIdentity, SidebarSessionRow, TileSidebarRow } from './SidebarRows';
import { StateIndicator } from './StateIndicator';

export function SidebarWorkspaceList() {
  const {
    onMuteWorkspace,
    onPinWorkspace,
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
                {(onRenameWorkspace || onMuteWorkspace || onPinWorkspace) && (
                  <span className="workspace-actions">
                    {onPinWorkspace && (
                      <button
                        type="button"
                        className={`workspace-action-btn pin-workspace-btn${workspace.pinned ? ' pinned' : ''}`}
                        data-testid={`pin-workspace-${workspace.id}`}
                        onClick={(e) => {
                          e.stopPropagation();
                          onPinWorkspace(workspace.id, !workspace.pinned);
                        }}
                        title={workspace.pinned ? 'Unpin workspace' : 'Pin workspace'}
                        aria-label={`${workspace.pinned ? 'Unpin' : 'Pin'} workspace ${workspace.title}`}
                      >
                        {workspace.pinned ? '\u{1F4CC}' : '\u{1F4CD}'}
                      </button>
                    )}
                    {onRenameWorkspace && (
                      <button
                        type="button"
                        className="workspace-action-btn rename-workspace-btn"
                        onClick={(e) => openRename('workspace', workspace.id, workspace.title, e)}
                        title="Rename workspace"
                        aria-label={`Rename workspace ${workspace.title}`}
                      >
                        ✎
                      </button>
                    )}
                    {onMuteWorkspace && (
                      <button
                        type="button"
                        className="workspace-action-btn mute-workspace-btn"
                        onClick={(e) => {
                          e.stopPropagation();
                          onMuteWorkspace(workspace.id, workspace.endpointId);
                        }}
                        title="Mute workspace"
                        aria-label={`Mute workspace ${workspace.title}`}
                      >
                        ⊘
                      </button>
                    )}
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

export function SidebarMutedWorkspaces() {
  const {
    selectedId,
    onMuteWorkspace,
    onSelectSession,
    onSelectWorkspace,
    mutedExpanded,
    setMutedExpanded,
    delegates,
    visibleMutedWorkspaces,
  } = useSidebarContext();
  return (
    <>
      {visibleMutedWorkspaces.length > 0 && (
        <div className="muted-sessions-section">
          <button
            className="muted-sessions-header"
            onClick={() => setMutedExpanded(!mutedExpanded)}
            aria-expanded={mutedExpanded}
          >
            <span className={`muted-sessions-chevron ${mutedExpanded ? 'expanded' : ''}`}>▸</span>
            Muted Workspaces ({visibleMutedWorkspaces.length})
          </button>
          {mutedExpanded && (
            <div className="muted-sessions-list">
              {visibleMutedWorkspaces.map((workspace) => {
                return (
                  <WorkspaceDropGroup
                    workspace={workspace}
                    muted
                    key={`${workspace.endpointId || 'local'}:${workspace.id}`}
                  >
                    <div className="workspace-group-header">
                      <button
                        type="button"
                        className="sidebar-row-select"
                        aria-label={`Open workspace ${workspace.title}`}
                        onClick={() => onSelectWorkspace(workspace.id)}
                      />
                      <StateIndicator
                        state={(workspace.status as UISessionState | undefined) || 'idle'}
                        size="md"
                        seed={workspace.id}
                      />
                      <span className="workspace-label">{workspace.title}</span>
                      {onMuteWorkspace && (
                        <span className="workspace-actions">
                          <button
                            type="button"
                            className="workspace-action-btn unmute-workspace-btn"
                            onClick={(e) => {
                              e.stopPropagation();
                              onMuteWorkspace(workspace.id, workspace.endpointId);
                            }}
                            title="Unmute workspace"
                            aria-label={`Unmute workspace ${workspace.title}`}
                          >
                            ⊙
                          </button>
                        </span>
                      )}
                    </div>
                    <div className="muted-workspace-sessions">
                      {workspace.children.map((child) => {
                        if (child.kind === 'tile') {
                          return (
                            <WorkspaceTileRow
                              key={child.id}
                              workspaceId={workspace.id}
                              tile={child.tile}
                              muted
                            />
                          );
                        }
                        const session = child.session;
                        return (
                          <div
                            key={session.id}
                            className={`session-item grouped muted-session ${selectedId === session.id ? 'selected' : ''}`.trim()}
                            data-testid={`sidebar-session-${session.id}`}
                            data-state={session.state}
                          >
                            <button
                              type="button"
                              className="sidebar-row-select"
                              aria-label={`Open ${session.label}`}
                              onClick={() => onSelectSession(session.id)}
                            />
                            <StateIndicator
                              state={session.state}
                              size="md"
                              seed={session.id}
                              reason={session.state_reason}
                            />
                            <SidebarSessionIdentity
                              session={session}
                              hasDelegates={(delegates.get(session.id)?.length ?? 0) > 0}
                            />
                            <DelegationChainTrigger
                              session={session}
                              hasDelegates={(delegates.get(session.id)?.length ?? 0) > 0}
                            />
                            {session.chiefOfStaff && <ChiefOfStaffBadge />}
                            {session.delegatedFromChief && <DelegatedFromChiefBadge />}
                            {session.endpointName && (
                              <span
                                className={`session-endpoint-badge status-${session.endpointStatus || 'connected'}`}
                              >
                                {session.endpointName}
                              </span>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  </WorkspaceDropGroup>
                );
              })}
            </div>
          )}
        </div>
      )}
    </>
  );
}

function WorkspaceDropGroup({
  workspace,
  muted = false,
  reorderSource = false,
  children,
}: {
  workspace: SidebarWorkspace;
  muted?: boolean;
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
      className={`workspace-group ${muted ? 'muted-workspace ' : ''}${selectedWorkspaceId === workspace.id ? 'selected' : ''}${reorderSource ? ' workspace-group--reorder-source' : ''}${workspaceDragClass(workspace)}`}
      data-testid={`sidebar-${muted ? 'muted-' : ''}workspace-${workspace.id}`}
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
  muted = false,
}: {
  workspaceId: string;
  tile: TileLeaf;
  muted?: boolean;
}) {
  const { tileContents, selectedTile, onSelectTile, onCloseTile, onReloadTile } =
    useSidebarContext();
  return (
    <TileSidebarRow
      workspaceId={workspaceId}
      tile={tile}
      content={tileContents[tileContentKey(workspaceId, tile.tileId)]}
      selected={selectedTile?.workspaceId === workspaceId && selectedTile.tileId === tile.tileId}
      muted={muted}
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
