import { sessionDisplayName } from '../utils/sessionDisplayName';
import { openPath } from '@tauri-apps/plugin-opener';
import { SessionTerminalDesktop } from '../components/SessionTerminalDesktop';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import type { Desktop } from '../types/generated';
import { desktopLabel, desktopTerminalState, orderedDesktops } from '../utils/desktops';
import {
  useAppAppearanceContext,
  useAppGardenActionsContext,
  useAppInputs,
  useAppPanelsContext,
  useAppSessionsContext,
  useAppShell,
  useAttentionQueueContext,
  useCrewPanelContext,
  useDesktopResidencyContext,
  useDesktopRuntimeContext,
  useLeafDragContext,
  useNavigationContext,
  useSessionLaunchContext,
  useSessionLifecycleContext,
} from './AppContexts';
import { EmptyDesktopLauncher } from './EmptyDesktopLauncher';
import { openThenShow } from './openThenShow';
import { leafOn, sessionOfLeaf } from '../navigation/activeLeaf';

export function AppDesktops() {
  const { snoozeMenu } = useAttentionQueueContext();
  const desktops = useProfilesStore((state) => state.desktops);
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const {
    desktopViews,
    desktopSelectionStyle,
    utilityFocusRequestToken,
    view,
    handleSelectSession,
    handleNavigateOutOfSession,
    handleCloseTile,
    crewSeedTile,
  } = useNavigationContext();
  const { handleBackToCrew } = useCrewPanelContext();
  const { setDesktopRef, eventRouter } = useDesktopRuntimeContext();
  const { mountedDesktopIds } = useDesktopResidencyContext();
  const focusRequest = useSessionStore((state) => state.focusRequest);
  const { selectLeaf } = useSessionStore.getState();
  const {
    annotationApi,
    blockingOverlayOpen,
    windowCovered,
    zoomModeBySessionId,
    setZoomModeBySessionId,
  } = useAppShell();
  const { seedPopoverRequest, usagePopoverRequest, openAsk } = useAppPanelsContext();
  const { terminalFontSize, resolvedTheme } = useAppAppearanceContext();
  const { delegationSessions } = useAppSessionsContext();
  const { daemonSessions } = useAppInputs();
  const allSessions = useSessionStore((state) => state.sessions);
  const seeds = useDaemonStore((state) => state.seeds);
  const { handleOpenSeedTile, handleRevealSeedInGarden } = useAppGardenActionsContext();
  const {
    sendCancelCountdown,
    sendTerminalPointerActivity,
    sendOpenMarkdown,
    sendRenameSession,
    sendDesktopSetSplitRatio,
    sendDesktopUpdateTile,
    desktopTileContents,
  } = useDaemonApi();
  const { createSplitSession } = useSessionLaunchContext();
  const { handleCloseTerminalTile } = useSessionLifecycleContext();
  const {
    getActiveLeafDropSnapshot,
    handleLeafDragStart,
    handleSurfaceLeafDrop,
    handleLeafDragGhostMove,
    handleLeafDragPreview,
    handleLeafDragEnd,
    leafDragPreview,
  } = useLeafDragContext();

  const whereOpen = (sessionId: string, viewing: Desktop) => {
    const label = allSessions.find((session) => session.id === sessionId)?.label;
    const named = label ? `“${label}”` : 'another terminal';
    const home = desktops.find((desktop) => desktop.panes.some((pane) => pane.kind === 'agent' && pane.session_id === sessionId));
    if (!home) return named;
    return home.id === viewing.id ? `${named} on this desktop` : `${named} on ${desktopLabel(home, desktops)}`;
  };

  const mounted = orderedDesktops(desktops).filter((desktop) => mountedDesktopIds.has(desktop.id));

  const renderDesktop = (desktop: Desktop) => {
    const group = desktopViews.find((entry) => entry.id === desktop.id);
    const desktopSessions = group?.sessions ?? [];
    const terminalState = desktopTerminalState(desktop);
    const isCurrent = desktop.id === currentDesktopId;
    const shownSessionId = terminalState.agents.find((pane) => pane.id === desktop.active_pane_id)?.sessionId ?? null;
    const leafSessionId = sessionOfLeaf(leafOn(desktop.profile_id, desktop, desktop.active_pane_id));
    const desktopDirectory = allSessions.find((session) => session.id === leafSessionId)?.cwd;
    return (
      <div key={desktop.id} className={`terminal-wrapper ${isCurrent ? 'active' : ''}`}>
        <SessionTerminalDesktop
          ref={setDesktopRef(desktop.id)}
          desktopId={desktop.id}
          shortcutsEnabled={!snoozeMenu}
          desktopDirectory={desktopDirectory}
          desktopSessions={desktopSessions.map((entry) => ({
            id: entry.id,
            label: entry.label,
            title: entry.title,
            priority: entry.priority,
            agent: entry.agent,
            cwd: entry.cwd,
            state: entry.state,
            autoSettleFiresAt: entry.autoSettleFiresAt,
            autoSettleHeld: entry.autoSettleHeld,
            autoSettleDismissArmed: entry.autoSettleDismissArmed,
            terminalExit: entry.terminalExit,
            state_reason: entry.state_reason,
            terminalBuildStale: entry.terminalBuildStale,
            conversationAlsoOpenIn: entry.conversationSharedWith.map((peer) => whereOpen(peer, desktop)),
            usage: entry.usage,
            isActive: isCurrent && entry.id === shownSessionId,
            seedId: entry.seedId,
            crewMember: entry.crewMember,
            chiefOfStaff: entry.chiefOfStaff,
            automation: entry.automation,
            pullRequests: entry.pullRequests,
          }))}
          delegationSessions={delegationSessions}
          selectedSessionId={isCurrent ? shownSessionId : null}
          seedTargetSessions={daemonSessions.map((session) => ({
            sessionId: session.id,
            label: sessionDisplayName(session),
            priority: session.priority,
            state: session.state,
          }))}
          gardenSeeds={seeds}
          onOpenSeed={handleOpenSeedTile}
          onRevealSeedInGarden={handleRevealSeedInGarden}
          onOpenAsk={openAsk}
          backToCrewTileId={crewSeedTile?.desktopId === desktop.id ? crewSeedTile.tileId : undefined}
          onBackToCrew={handleBackToCrew}
          seedPopoverRequest={seedPopoverRequest}
          usagePopoverRequest={usagePopoverRequest}
          annotationApi={annotationApi}
          onCancelCountdown={sendCancelCountdown}
          onTerminalPointerActivity={sendTerminalPointerActivity}
          onOpenMarkdown={(path, sessionId) => {
            void openThenShow(() => sendOpenMarkdown(path, sessionId))
              .catch((error) => {
                console.error('[Markdown] in-app open failed, falling back to OS open:', error);
                void openPath(path).catch((openError) => {
                  console.error('[Markdown] OS open fallback failed:', openError);
                });
              });
          }}
          terminalState={terminalState}
          desktopSelectionStyle={desktopSelectionStyle}
          activePaneId={desktop.active_pane_id}
          fontSize={terminalFontSize}
          resolvedTheme={resolvedTheme}
          focusRequestToken={utilityFocusRequestToken}
          focusClaim={focusRequest?.desktopId === desktop.id ? focusRequest : null}
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
            if (paneSessionId) handleCloseTerminalTile(desktop.id, paneId, paneSessionId);
          }}
          onRenameSession={sendRenameSession}
          onSelectSession={handleSelectSession}
          onResizeSplit={(splitId, ratio) =>
            sendDesktopSetSplitRatio(desktop.id, splitId, ratio)
          }
          onFocusPane={(paneId) => {
            if (paneId !== desktop.active_pane_id || !isCurrent) selectLeaf(desktop.id, paneId);
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
          onUpdateTile={(tileId, tileParams, tileSessionId) =>
            sendDesktopUpdateTile({
              desktopId: desktop.id,
              tileId,
              tileParams,
              tileSessionId,
            })
          }
          onMoveLeaf={(leafId, anchorId, edge, ratio) => handleSurfaceLeafDrop(desktop.id, leafId, anchorId, edge, ratio)}
          getActiveLeafDropSnapshot={getActiveLeafDropSnapshot}
          onLeafDragStart={handleLeafDragStart}
          onLeafDragGhostMove={handleLeafDragGhostMove}
          onLeafDragPreview={handleLeafDragPreview}
          onLeafDragEnd={handleLeafDragEnd}
          leafDragPreview={leafDragPreview}
          tileContents={desktopTileContents}
        />
        {isCurrent && view === 'session' && !terminalState.layoutTree && (
          <EmptyDesktopLauncher desktopId={desktop.id} label={desktopLabel(desktop, desktops)} active={!windowCovered} />
        )}
      </div>
    );
  };

  return <div className="terminal-main-area">{mounted.map(renderDesktop)}</div>;
}
