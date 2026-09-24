import { openPath } from '@tauri-apps/plugin-opener';
import { SessionTerminalWorkspace } from '../components/SessionTerminalWorkspace';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import type { Desktop } from '../types/generated';
import { desktopTerminalState, orderedDesktops } from '../utils/desktops';
import {
  useAppAppearanceContext,
  useAppErrorsContext,
  useAppGardenActionsContext,
  useAppInputs,
  useAppPanelsContext,
  useAppSessionsContext,
  useAppShell,
  useCrewPanelContext,
  useDesktopResidencyContext,
  useDesktopRuntimeContext,
  useLeafDragContext,
  useNavigationContext,
  useSessionLaunchContext,
  useSessionLifecycleContext,
} from './AppContexts';

function revisionOf(desktopId: string): number | null {
  return useProfilesStore.getState().desktops.find((desktop) => desktop.id === desktopId)?.revision ?? null;
}

export function AppDesktops() {
  const desktops = useProfilesStore((state) => state.desktops);
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const {
    desktopViews,
    workspaceSelectionStyle,
    utilityFocusRequestToken,
    view,
    handleSelectSession,
    handleNavigateOutOfSession,
    handleCloseTile,
    crewSeedTile,
  } = useNavigationContext();
  const { handleBackToCrew } = useCrewPanelContext();
  const { setDesktopRef, focusDesktopLeaf, eventRouter } = useDesktopRuntimeContext();
  const { mountedDesktopIds } = useDesktopResidencyContext();
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const {
    presentationBySessionId,
    annotationApi,
    handleOpenPresentationWindow,
    blockingOverlayOpen,
    zoomModeBySessionId,
    setZoomModeBySessionId,
  } = useAppShell();
  const { handleTerminalModelRecovered } = useAppErrorsContext();
  const { seedPopoverRequest, usagePopoverRequest } = useAppPanelsContext();
  const { terminalFontSize, resolvedTheme } = useAppAppearanceContext();
  const { delegationSessions } = useAppSessionsContext();
  const { daemonSessions } = useAppInputs();
  const seeds = useDaemonStore((state) => state.seeds);
  const { handleOpenSeedTile, handleRevealSeedInGarden } = useAppGardenActionsContext();
  const {
    sendTriggerNudge,
    sendCancelCountdown,
    sendTerminalPointerActivity,
    sendOpenMarkdown,
    sendRenameSession,
    sendDesktopSetActivePane,
    sendDesktopSetSplitRatio,
    sendDesktopUpdateTile,
    sendDesktopMoveLeaf,
    desktopTileContents,
  } = useDaemonApi();
  const { createSplitSession } = useSessionLaunchContext();
  const { handleCloseSession } = useSessionLifecycleContext();
  const {
    getActiveLeafDropSnapshot,
    handleLeafDragStart,
    handleLeafDragGhostMove,
    handleLeafDragPreview,
    handleLeafDragEnd,
    leafDragPreview,
  } = useLeafDragContext();

  const mounted = orderedDesktops(desktops).filter((desktop) => mountedDesktopIds.has(desktop.id));

  const renderDesktop = (desktop: Desktop) => {
    const group = desktopViews.find((entry) => entry.id === desktop.id);
    const desktopSessions = group?.sessions ?? [];
    const terminalState = desktopTerminalState(desktop);
    const isCurrent = desktop.id === currentDesktopId;
    const activePane = terminalState.agents.find((pane) => pane.id === desktop.active_pane_id);
    const workspaceDirectory = desktopSessions.find((session) => session.id === activePane?.sessionId)?.cwd;
    return (
      <div key={desktop.id} className={`terminal-wrapper ${isCurrent ? 'active' : ''}`}>
        <SessionTerminalWorkspace
          ref={setDesktopRef(desktop.id)}
          workspaceId={desktop.id}
          workspaceDirectory={workspaceDirectory}
          workspaceSessions={desktopSessions.map((entry) => ({
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
          selectedSessionId={isCurrent ? (activePane?.sessionId ?? null) : null}
          seedTargetSessions={daemonSessions.map((session) => ({
            sessionId: session.id,
            label: session.label || session.id,
            state: session.state,
          }))}
          gardenSeeds={seeds}
          onOpenSeed={handleOpenSeedTile}
          onRevealSeedInGarden={handleRevealSeedInGarden}
          backToCrewTileId={crewSeedTile?.desktopId === desktop.id ? crewSeedTile.tileId : undefined}
          onBackToCrew={handleBackToCrew}
          seedPopoverRequest={seedPopoverRequest}
          usagePopoverRequest={usagePopoverRequest}
          annotationApi={annotationApi}
          onTriggerNudge={sendTriggerNudge}
          onCancelCountdown={sendCancelCountdown}
          onTerminalPointerActivity={sendTerminalPointerActivity}
          onOpenPresentation={handleOpenPresentationWindow}
          onOpenMarkdown={(path, sessionId) => {
            void sendOpenMarkdown(path, sessionId)
              .then(({ desktopId, tileId }) => {
                if (desktopId && tileId) focusDesktopLeaf(desktopId, tileId);
              })
              .catch((error) => {
                console.error('[Markdown] in-app open failed, falling back to OS open:', error);
                void openPath(path).catch((openError) => {
                  console.error('[Markdown] OS open fallback failed:', openError);
                });
              });
          }}
          onTerminalModelRecovered={handleTerminalModelRecovered}
          workspace={terminalState}
          workspaceSelectionStyle={workspaceSelectionStyle}
          activePaneId={desktop.active_pane_id}
          fontSize={terminalFontSize}
          resolvedTheme={resolvedTheme}
          focusRequestToken={utilityFocusRequestToken}
          enabled={!blockingOverlayOpen}
          isActiveSession={isCurrent && view !== 'dashboard'}
          isSessionViewVisible={view === 'session'}
          terminalsLive
          eventRouter={eventRouter}
          onSplitPane={(targetPaneId, direction) => {
            void createSplitSession('shell', direction, targetPaneId);
          }}
          onClosePane={(paneId) => {
            const paneSessionId = terminalState.agents.find((pane) => pane.id === paneId)?.sessionId;
            if (paneSessionId) {
              void handleCloseSession(paneSessionId).catch(console.error);
            }
          }}
          onRenameSession={sendRenameSession}
          onSelectSession={handleSelectSession}
          onResizeSplit={(splitId, ratio) => {
            const revision = revisionOf(desktop.id);
            return revision === null ? undefined : sendDesktopSetSplitRatio(desktop.id, splitId, ratio, revision);
          }}
          onFocusPane={(paneId) => {
            if (paneId === desktop.active_pane_id) return;
            void sendDesktopSetActivePane(desktop.id, paneId).catch(() => {});
          }}
          zoomActive={Boolean(zoomModeBySessionId[desktop.id])}
          onSetZoomActive={(active) => {
            setZoomModeBySessionId((prev) =>
              prev[desktop.id] === active ? prev : { ...prev, [desktop.id]: active },
            );
          }}
          onNavigateOutOfSession={handleNavigateOutOfSession}
          onUndockTile={(tileId) => {
            handleCloseTile(desktop.id, tileId);
          }}
          onUpdateTile={(tileId, tileParams, tileSessionId) => {
            const revision = revisionOf(desktop.id);
            return revision === null
              ? undefined
              : sendDesktopUpdateTile({ desktopId: desktop.id, expectedRevision: revision, tileId, tileParams, tileSessionId });
          }}
          onMoveLeaf={(leafId, anchorId, edge) => {
            const revision = revisionOf(desktop.id);
            if (revision === null) return;
            void sendDesktopMoveLeaf({
              sourceDesktopId: desktop.id,
              targetDesktopId: desktop.id,
              leafId,
              anchorId,
              edge,
              expectedSourceRevision: revision,
              expectedTargetRevision: revision,
            }).catch(() => {});
          }}
          getActiveLeafDropSnapshot={getActiveLeafDropSnapshot}
          onLeafDragStart={handleLeafDragStart}
          onLeafDragGhostMove={handleLeafDragGhostMove}
          onLeafDragPreview={handleLeafDragPreview}
          onLeafDragEnd={handleLeafDragEnd}
          leafDragPreview={leafDragPreview}
          tileContents={desktopTileContents}
          allowLocalTileTargets
        />
      </div>
    );
  };

  return <div className="terminal-main-area">{mounted.map(renderDesktop)}</div>;
}
