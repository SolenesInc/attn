import { openPath } from '@tauri-apps/plugin-opener';
import { SessionTerminalWorkspace } from '../components/SessionTerminalWorkspace';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { localWorkspaceDirectory } from '../types/workspace';
import { useAppContext } from './AppContext';
import { activePaneIdForFocusedSession, terminalStateForWorkspaceSessions } from './appSupport';

export function AppWorkspaces() {
  const {
    workspaceViews,
    sessionlessWorkspaceStateById,
    workspaceSelection,
    getActivePaneIdForSession,
    activeWorkspaceId,
    warmWorkspaceIds,
    setWorkspaceRef,
    activeSessionId,
    presentationBySessionId,
    delegationSessions,
    daemonSessions,
    seeds,
    handleOpenSeedTile,
    handleRevealSeedInGarden,
    seedPopoverRequest,
    usagePopoverRequest,
    annotationApi,
    sendTriggerNudge,
    sendCancelCountdown,
    sendTerminalPointerActivity,
    handleOpenPresentationWindow,
    sendOpenMarkdown,
    focusWorkspaceLeaf,
    handleTerminalModelRecovered,
    workspaceSelectionStyle,
    terminalFontSize,
    resolvedTheme,
    utilityFocusRequestToken,
    blockingOverlayOpen,
    view,
    paneRuntimeEventRouter,
    createSplitSession,
    handleClosePane,
    sendRenameSession,
    handleSelectSession,
    sendWorkspaceSetSplitRatio,
    selectAgentPane,
    zoomModeBySessionId,
    setZoomModeBySessionId,
    handleNavigateOutOfSession,
    handleCloseTile,
    sendWorkspaceUpdateTile,
    activeWorkspaceIdRef,
    sendWorkspaceMoveLeafToWorkspace,
    sendWorkspaceMoveLeaf,
    getActiveLeafDropSnapshot,
    handleLeafDragStart,
    handleLeafDragGhostMove,
    handleLeafDragPreview,
    handleLeafDragEnd,
    leafDragPreview,
    tileContents,
    requestTileContent,
    sessions,
  } = useAppContext();
  return (
    <>
      <div className="terminal-main-area">
        {workspaceViews.map((workspace) => {
          const workspaceState =
            terminalStateForWorkspaceSessions(workspace.sessions) ??
            sessionlessWorkspaceStateById.get(workspace.id) ??
            null;
          if (!workspaceState) {
            return null;
          }
          const focusedSessionId =
            workspaceSelection.focusedSessionIdByWorkspace[workspace.id] ??
            workspace.focusedSessionId;
          const focusedSession = focusedSessionId
            ? (workspace.sessions.find((session) => session.id === focusedSessionId) ?? null)
            : null;
          const activePaneId = activePaneIdForFocusedSession(
            workspaceState,
            focusedSession,
            getActivePaneIdForSession,
          );
          const isActiveWorkspace = workspace.id === activeWorkspaceId;
          const terminalsLive =
            warmWorkspaceIds === null || isActiveWorkspace || warmWorkspaceIds.has(workspace.id);
          return (
            <div
              key={`${workspace.endpointId || 'local'}:${workspace.id}`}
              className={`terminal-wrapper ${isActiveWorkspace ? 'active' : ''}`}
            >
              <SessionTerminalWorkspace
                ref={setWorkspaceRef(workspace.id)}
                workspaceId={workspace.id}
                workspaceDirectory={localWorkspaceDirectory({
                  directory: workspace.directory,
                  endpoint_id: workspace.endpointId,
                })}
                workspaceSessions={workspace.sessions.map((entry) => ({
                  id: entry.id,
                  label: entry.label,
                  agent: entry.agent,
                  cwd: entry.cwd,
                  endpointId: entry.endpointId,
                  state: entry.state,
                  ticketUnread: entry.ticketUnread,
                  nudgeFiresAt: entry.nudgeFiresAt,
                  autoSettleFiresAt: entry.autoSettleFiresAt,
                  autoSettleHeld: entry.autoSettleHeld,
                  autoSettleDismissArmed: entry.autoSettleDismissArmed,
                  terminalBuildStale: entry.terminalBuildStale,
                  usage: entry.usage,
                  isActive: entry.id === activeSessionId,
                  presentation: presentationBySessionId.get(entry.id),
                  seedId: entry.seedId,
                  crewMember: entry.crewMember,
                  automation: entry.automation,
                  pullRequests: entry.pullRequests,
                }))}
                delegationSessions={delegationSessions}
                seedTargetSessions={daemonSessions.map((session) => ({
                  sessionId: session.id,
                  label: session.label || session.id,
                  state: session.state,
                }))}
                gardenSeeds={seeds}
                onOpenSeed={handleOpenSeedTile}
                onRevealSeedInGarden={handleRevealSeedInGarden}
                seedPopoverRequest={seedPopoverRequest}
                usagePopoverRequest={usagePopoverRequest}
                annotationApi={annotationApi}
                onTriggerNudge={sendTriggerNudge}
                onCancelCountdown={sendCancelCountdown}
                onTerminalPointerActivity={sendTerminalPointerActivity}
                onOpenPresentation={handleOpenPresentationWindow}
                onOpenMarkdown={(path, sessionId) => {
                  void sendOpenMarkdown(path, sessionId)
                    .then(({ workspaceId, tileId }) => {
                      if (workspaceId && tileId) focusWorkspaceLeaf(workspaceId, tileId);
                    })
                    .catch((error) => {
                      console.error(
                        '[Markdown] in-app open failed, falling back to OS open:',
                        error,
                      );
                      void openPath(path).catch((openError) => {
                        console.error('[Markdown] OS open fallback failed:', openError);
                      });
                    });
                }}
                onTerminalModelRecovered={handleTerminalModelRecovered}
                workspace={workspaceState}
                workspaceSelectionStyle={workspaceSelectionStyle}
                activePaneId={activePaneId}
                fontSize={terminalFontSize}
                resolvedTheme={resolvedTheme}
                focusRequestToken={utilityFocusRequestToken}
                enabled={!blockingOverlayOpen}
                isActiveSession={isActiveWorkspace}
                isSessionViewVisible={view === 'session'}
                terminalsLive={terminalsLive}
                eventRouter={paneRuntimeEventRouter}
                onSplitPane={(targetPaneId, direction) => {
                  void createSplitSession('shell', direction, targetPaneId);
                }}
                onClosePane={(paneId) => {
                  const paneSessionId = workspaceState.agents.find(
                    (pane) => pane.id === paneId,
                  )?.sessionId;
                  if (paneSessionId) {
                    void handleClosePane(paneSessionId, paneId).catch(console.error);
                  }
                }}
                onRenameSession={sendRenameSession}
                onSelectSession={handleSelectSession}
                onResizeSplit={(splitId, ratio) => {
                  return sendWorkspaceSetSplitRatio(workspace.id, splitId, ratio);
                }}
                onFocusPane={(paneId) => {
                  const agentPane = workspaceState.agents.find((pane) => pane.id === paneId);
                  const paneSessionId = agentPane?.sessionId;
                  if (!paneSessionId) {
                    return;
                  }
                  selectAgentPane(paneSessionId, paneId);
                }}
                zoomActive={Boolean(zoomModeBySessionId[workspace.id])}
                onSetZoomActive={(active) => {
                  setZoomModeBySessionId((prev) =>
                    prev[workspace.id] === active ? prev : { ...prev, [workspace.id]: active },
                  );
                }}
                onNavigateOutOfSession={handleNavigateOutOfSession}
                onUndockTile={(tileId) => {
                  handleCloseTile(workspace.id, tileId);
                }}
                onUpdateTile={(tileId, tileParams, tileSessionId) =>
                  sendWorkspaceUpdateTile(workspace.id, tileId, tileParams, tileSessionId)
                }
                onMoveLeaf={(leafId, anchorId, edge, ratio) => {
                  const targetWorkspaceId = activeWorkspaceIdRef.current || workspace.id;
                  if (targetWorkspaceId !== workspace.id) {
                    void sendWorkspaceMoveLeafToWorkspace(workspace.id, targetWorkspaceId, leafId, {
                      anchorId,
                      edge,
                      ratio,
                    }).catch(() => {});
                    return;
                  }
                  void sendWorkspaceMoveLeaf(workspace.id, leafId, { anchorId, edge, ratio }).catch(
                    () => {},
                  );
                }}
                getActiveLeafDropSnapshot={getActiveLeafDropSnapshot}
                onLeafDragStart={(leafId) =>
                  handleLeafDragStart(workspace.id, workspace.endpointId, leafId)
                }
                onLeafDragGhostMove={handleLeafDragGhostMove}
                onLeafDragPreview={handleLeafDragPreview}
                onLeafDragEnd={handleLeafDragEnd}
                leafDragPreview={leafDragPreview}
                tileContents={tileContents}
                allowLocalTileTargets={!workspace.endpointId}
                onRequestTileContent={requestTileContent}
              />
            </div>
          );
        })}
        {sessions.length === 0 && (
          <div className="no-sessions">
            <p>No active sessions</p>
            <p>Press {formatShortcut('session.newWorkspace')} to start a new workspace</p>
          </div>
        )}
      </div>
    </>
  );
}
