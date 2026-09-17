import { Sidebar } from '../components/Sidebar';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { useSessionStore } from '../store/sessions';
import { BUILD_PROFILE } from '../utils/buildProfile';
import { areSidebarHarnessLogosEnabled } from '../utils/sidebarHarnessLogos';
import {
  useAppAppearanceContext,
  useAppGardenActionsContext,
  useAppGridContext,
  useAppInputs,
  useAppSessionsContext,
  useAppPanelsContext,
  useAttentionQueueContext,
  useChiefOfStaffContext,
  useNavigationContext,
  useSessionLaunchContext,
  useSessionLifecycleContext,
  useWorkspaceDragContext,
  useWorkspaceResidencyContext,
} from './AppContexts';
import { useAppSidebarActions } from './useAppSidebarActions';

export function AppSidebar() {
  const { mutedWorkspaceViews } = useAppSessionsContext();
  const {
    sidebarWorkspaceViews,
    visualWorkspaces,
    visualIndexByWorkspaceId,
    activeWorkspaceId,
    selectedTile,
    showSessionlessWorkspaces,
    handleToggleShowSessionlessWorkspaces,
    workspaceSelectionStyle,
    handleWorkspaceSelectionStyleChange,
    handleWorkspaceReorder,
    handleSelectSession,
    handleSelectWorkspace,
    handleSelectTile,
    handleCloseTile,
    handleReloadTile,
    goToDashboard,
    view,
  } = useNavigationContext();
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const {
    tileContents,
    sendMuteWorkspace,
    sendPinWorkspace,
    sendPinSession,
    sendRenameSession,
    sendRenameWorkspace,
    sendSettleTurn,
    sendWakeTurn,
    sendTriggerNudge,
  } = useDaemonApi();
  const {
    sidebarCollapsed,
    openNotificationsPanel,
    sidebarMutedExpanded,
    setSidebarMutedExpanded,
    toggleSidebarCollapse,
  } = useAppPanelsContext();
  const { keybindings, handleToggleSidebarHarnessLogos } = useAppAppearanceContext();
  const { criticalNotifications, settings } = useAppInputs();
  const { gridLayout, handleSelectGridLayout } = useAppGridContext();
  const { handleChangeChiefOfStaff } = useChiefOfStaffContext();
  const crew = useDaemonStore((state) => state.crew);
  const { handleWakeCrewMember, handleSleepCrewMember } = useAppGardenActionsContext();
  const {
    queueModeEnabled,
    handleToggleQueueMode,
    crewQueueEnabled,
    handleToggleCrewQueue,
    queueBands,
    openSnoozeMenu,
  } = useAttentionQueueContext();
  const {
    leafWorkspaceDrag,
    dragHoverWorkspaceId,
    handleWorkspaceDragEnter,
    handleWorkspaceDragLeave,
    handleWorkspaceDragDrop,
    handleNewWorkspaceDrop,
    handleLeafDragStart,
    handleLeafDragEnd,
  } = useWorkspaceDragContext();
  const { onScreenSessionIds } = useWorkspaceResidencyContext();
  const { handleNewSession } = useSessionLaunchContext();
  const { handleRequestCloseSession, handleReloadSession } = useSessionLifecycleContext();
  const { sidebarHeaderActions, dockItems } = useAppSidebarActions();
  return (
    <>
      <Sidebar
        workspaces={sidebarWorkspaceViews}
        visualOrder={visualWorkspaces}
        visualIndexByWorkspaceId={visualIndexByWorkspaceId}
        selectedId={activeSessionId}
        selectedWorkspaceId={activeWorkspaceId}
        selectedTile={selectedTile}
        tileContents={tileContents}
        collapsed={sidebarCollapsed}
        profile={BUILD_PROFILE}
        headerActions={sidebarHeaderActions}
        criticalNotifications={criticalNotifications}
        onOpenNotifications={openNotificationsPanel}
        gridLayout={gridLayout}
        onSelectGridLayout={handleSelectGridLayout}
        dockItems={dockItems}
        dockCollapsed={keybindings.dock.collapsed}
        onToggleDockCollapsed={() => keybindings.setDockCollapsed(!keybindings.dock.collapsed)}
        mutedWorkspaces={mutedWorkspaceViews}
        mutedExpanded={sidebarMutedExpanded}
        onMutedExpandedChange={setSidebarMutedExpanded}
        onMuteWorkspace={sendMuteWorkspace}
        onPinWorkspace={sendPinWorkspace}
        onPinSession={sendPinSession}
        onRenameSession={sendRenameSession}
        onRenameWorkspace={sendRenameWorkspace}
        onChangeChiefOfStaff={handleChangeChiefOfStaff}
        showSessionless={showSessionlessWorkspaces}
        onToggleShowSessionless={handleToggleShowSessionlessWorkspaces}
        crew={crew}
        onWakeCrewMember={handleWakeCrewMember}
        onSleepCrewMember={handleSleepCrewMember}
        queueModeEnabled={queueModeEnabled}
        onToggleQueueMode={handleToggleQueueMode}
        crewQueueEnabled={crewQueueEnabled}
        onToggleCrewQueue={handleToggleCrewQueue}
        harnessLogosEnabled={areSidebarHarnessLogosEnabled(settings)}
        onToggleHarnessLogos={handleToggleSidebarHarnessLogos}
        workspaceSelectionStyle={workspaceSelectionStyle}
        onWorkspaceSelectionStyleChange={handleWorkspaceSelectionStyleChange}
        leafDrag={
          leafWorkspaceDrag
            ? {
                sourceWorkspaceId: leafWorkspaceDrag.sourceWorkspaceId,
                endpointId: leafWorkspaceDrag.sourceEndpointId,
              }
            : null
        }
        dragHoverWorkspaceId={dragHoverWorkspaceId}
        onWorkspaceDragEnter={handleWorkspaceDragEnter}
        onWorkspaceDragLeave={handleWorkspaceDragLeave}
        onWorkspaceDragDrop={handleWorkspaceDragDrop}
        onNewWorkspaceDrop={handleNewWorkspaceDrop}
        onSessionDragStart={handleLeafDragStart}
        onSessionDragEnd={handleLeafDragEnd}
        onWorkspaceReorder={handleWorkspaceReorder}
        queue={queueBands}
        onSettleTurn={sendSettleTurn}
        onOpenSnooze={openSnoozeMenu}
        onWakeTurn={sendWakeTurn}
        onScreenSessionIds={onScreenSessionIds}
        onSelectSession={handleSelectSession}
        onTriggerNudge={sendTriggerNudge}
        onSelectWorkspace={handleSelectWorkspace}
        onSelectTile={handleSelectTile}
        onCloseTile={handleCloseTile}
        onReloadTile={handleReloadTile}
        onNewSession={() => handleNewSession('vertical')}
        onCloseSession={handleRequestCloseSession}
        onReloadSession={handleReloadSession}
        onGoToDashboard={goToDashboard}
        homeActive={view === 'dashboard'}
        onToggleCollapse={toggleSidebarCollapse}
      />
    </>
  );
}
