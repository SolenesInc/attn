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
    selectedId,
    selectedWorkspaceId,
    selectedTile,
    tileContents,
    onScreenSessionIds,
    onMuteWorkspace,
    onPinWorkspace,
    onRenameWorkspace,
    onWorkspaceDragEnter,
    onWorkspaceDragLeave,
    onWorkspaceDragDrop,
    onSessionDragStart,
    onSelectSession,
    onTriggerNudge,
    onSelectWorkspace,
    onSelectTile,
    onCloseTile,
    onReloadTile,
    openRename,
    openSessionActions,
    rowDelegation,
    visibleWorkspaces,
    canAcceptLeafDrag,
    workspaceDragClass,
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
            <div
              className={`workspace-group ${selectedWorkspaceId === workspace.id ? 'selected' : ''}${isReorderSource ? ' workspace-group--reorder-source' : ''}${workspaceDragClass(workspace)}`}
              data-testid={`sidebar-workspace-${workspace.id}`}
              onPointerEnter={() => {
                if (canAcceptLeafDrag(workspace)) {
                  onWorkspaceDragEnter?.(workspace);
                }
              }}
              onPointerLeave={() => {
                if (canAcceptLeafDrag(workspace)) {
                  onWorkspaceDragLeave?.(workspace);
                }
              }}
              onPointerUp={() => {
                if (canAcceptLeafDrag(workspace)) {
                  onWorkspaceDragDrop?.(workspace);
                }
              }}
            >
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
                    title="Tile-only workspace — no active session"
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
                        data-testid={`rename-workspace-${workspace.id}`}
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
                        data-testid={`mute-workspace-${workspace.id}`}
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
                    <TileSidebarRow
                      key={child.id}
                      workspaceId={workspace.id}
                      tile={child.tile}
                      content={tileContents[tileContentKey(workspace.id, child.tile.tileId)]}
                      selected={
                        selectedTile?.workspaceId === workspace.id &&
                        selectedTile.tileId === child.tile.tileId
                      }
                      onSelect={() => onSelectTile?.(workspace.id, child.tile.tileId)}
                      onClose={() => onCloseTile?.(workspace.id, child.tile.tileId)}
                      onReload={() => onReloadTile?.(workspace.id, child.tile.tileId)}
                    />
                  );
                }
                const session = child.session;
                const paneId = child.paneId;
                const draggable = Boolean(paneId && onSessionDragStart);
                return (
                  <SidebarSessionRow
                    key={session.id}
                    session={session}
                    selected={selectedId === session.id}
                    draggable={draggable}
                    dragging={draggingSessionId === session.id}
                    onSelect={() => onSelectSession(session.id)}
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
                    onOpenActions={(event) => openSessionActions(session, event)}
                    onTriggerNudge={() => onTriggerNudge?.(session.id)}
                    showSettling={!onScreenSessionIds?.has(session.id)}
                    {...rowDelegation(session)}
                  />
                );
              })}
            </div>
            {workspace.id === lastReorderParticipantId &&
              renderReorderSeam(reorderTrailingSeamIndex)}
          </div>
        );
      })}
    </>
  );
}

export function SidebarAutomationGroups() {
  const {
    selectedId,
    onScreenSessionIds,
    onSelectSession,
    onTriggerNudge,
    expandedAutomationGroups,
    openSessionActions,
    automationGroups,
    rowDelegation,
    toggleAutomationGroup,
  } = useSidebarContext();
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
                  <SidebarSessionRow
                    key={session.id}
                    session={session}
                    selected={selectedId === session.id}
                    onSelect={() => onSelectSession(session.id)}
                    onOpenActions={(event) => openSessionActions(session, event)}
                    onTriggerNudge={() => onTriggerNudge?.(session.id)}
                    showSettling={!onScreenSessionIds?.has(session.id)}
                    {...rowDelegation(session)}
                  />
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
    selectedWorkspaceId,
    selectedTile,
    tileContents,
    onMuteWorkspace,
    onWorkspaceDragEnter,
    onWorkspaceDragLeave,
    onWorkspaceDragDrop,
    onSelectSession,
    onSelectWorkspace,
    onSelectTile,
    onCloseTile,
    onReloadTile,
    mutedExpanded,
    setMutedExpanded,
    delegates,
    visibleMutedWorkspaces,
    canAcceptLeafDrag,
    workspaceDragClass,
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
                  <div
                    key={`${workspace.endpointId || 'local'}:${workspace.id}`}
                    className={`workspace-group muted-workspace ${selectedWorkspaceId === workspace.id ? 'selected' : ''}${workspaceDragClass(workspace)}`}
                    data-testid={`sidebar-muted-workspace-${workspace.id}`}
                    onPointerEnter={() => {
                      if (canAcceptLeafDrag(workspace)) {
                        onWorkspaceDragEnter?.(workspace);
                      }
                    }}
                    onPointerLeave={() => {
                      if (canAcceptLeafDrag(workspace)) {
                        onWorkspaceDragLeave?.(workspace);
                      }
                    }}
                    onPointerUp={() => {
                      if (canAcceptLeafDrag(workspace)) {
                        onWorkspaceDragDrop?.(workspace);
                      }
                    }}
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
                            <TileSidebarRow
                              key={child.id}
                              workspaceId={workspace.id}
                              tile={child.tile}
                              content={
                                tileContents[tileContentKey(workspace.id, child.tile.tileId)]
                              }
                              selected={
                                selectedTile?.workspaceId === workspace.id &&
                                selectedTile.tileId === child.tile.tileId
                              }
                              muted
                              onSelect={() => onSelectTile?.(workspace.id, child.tile.tileId)}
                              onClose={() => onCloseTile?.(workspace.id, child.tile.tileId)}
                              onReload={() => onReloadTile?.(workspace.id, child.tile.tileId)}
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
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}
    </>
  );
}
