import { Sidebar } from '../components/Sidebar';
import { BUILD_PROFILE } from '../utils/buildProfile';
import { areSidebarHarnessLogosEnabled } from '../utils/sidebarHarnessLogos';
import { useAppContext } from './AppContext';
import { useAppSidebarActions } from './useAppSidebarActions';

export function AppSidebar() {
  const {
    sidebarWorkspaceViews,
    visualWorkspaces,
    visualIndexByWorkspaceId,
    activeSessionId,
    activeWorkspaceId,
    selectedTile,
    tileContents,
    sidebarCollapsed,
    criticalNotifications,
    openNotificationsPanel,
    gridLayout,
    handleSelectGridLayout,
    keybindings,
    mutedWorkspaceViews,
    sidebarMutedExpanded,
    setSidebarMutedExpanded,
    sendMuteWorkspace,
    sendPinWorkspace,
    sendPinSession,
    sendRenameSession,
    sendRenameWorkspace,
    handleChangeChiefOfStaff,
    showSessionlessWorkspaces,
    handleToggleShowSessionlessWorkspaces,
    crew,
    handleWakeCrewMember,
    handleSleepCrewMember,
    queueModeEnabled,
    handleToggleQueueMode,
    crewQueueEnabled,
    handleToggleCrewQueue,
    settings,
    handleToggleSidebarHarnessLogos,
    workspaceSelectionStyle,
    handleWorkspaceSelectionStyleChange,
    leafWorkspaceDrag,
    dragHoverWorkspaceId,
    handleWorkspaceDragEnter,
    handleWorkspaceDragLeave,
    handleWorkspaceDragDrop,
    handleNewWorkspaceDrop,
    handleLeafDragStart,
    handleLeafDragEnd,
    handleWorkspaceReorder,
    queueBands,
    sendSettleTurn,
    openSnoozeMenu,
    sendWakeTurn,
    onScreenSessionIds,
    handleSelectSession,
    sendTriggerNudge,
    handleSelectWorkspace,
    handleSelectTile,
    handleCloseTile,
    handleReloadTile,
    handleNewSession,
    handleRequestCloseSession,
    handleReloadSession,
    goToDashboard,
    view,
    toggleSidebarCollapse,
  } = useAppContext();
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
