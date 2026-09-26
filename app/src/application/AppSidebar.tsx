import { useMemo } from 'react';
import { Sidebar } from '../components/Sidebar';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { BUILD_INSTANCE } from '../utils/buildInstance';
import { areSidebarHarnessLogosEnabled } from '../utils/sidebarHarnessLogos';
import {
  useAppAppearanceContext,
  useAppGardenActionsContext,
  useAppGridContext,
  useAppInputs,
  useAppPanelsContext,
  useAttentionQueueContext,
  useChiefOfStaffContext,
  useCrewPanelContext,
  useDesktopNavigationContext,
  useDesktopResidencyContext,
  useLeafDragContext,
  useNavigationContext,
  useSessionLaunchContext,
  useSessionLifecycleContext,
} from './AppContexts';
import { useAppSidebarActions } from './useAppSidebarActions';

export function AppSidebar() {
  const {
    desktopViews,
    currentDesktopId,
    selectedTile,
    workspaceSelectionStyle,
    handleWorkspaceSelectionStyleChange,
    handleSelectSession,
    handleSelectDesktop,
    handleSelectTile,
    handleCloseTile,
    handleReloadTile,
    goToDashboard,
    view,
  } = useNavigationContext();
  const desktops = useProfilesStore((state) => state.desktops);
  const slotIndexByDesktopId = useMemo(
    () =>
      new Map(
        desktops.map((desktop) => [desktop.id, desktop.shortcut_slot ? desktop.shortcut_slot - 1 : -1]),
      ),
    [desktops],
  );
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const {
    desktopTileContents,
    sendRenameSession,
    sendSettleTurn,
    sendWakeTurn,
    sendTriggerNudge,
  } = useDaemonApi();
  const { sidebarCollapsed, openNotificationsPanel, toggleSidebarCollapse } = useAppPanelsContext();
  const { keybindings, handleToggleSidebarHarnessLogos } = useAppAppearanceContext();
  const { criticalNotifications, settings } = useAppInputs();
  const { gridLayout, handleSelectGridLayout } = useAppGridContext();
  const { handleChangeChiefOfStaff } = useChiefOfStaffContext();
  const crew = useDaemonStore((state) => state.crew);
  const { handleWakeCrewMember, handleSleepCrewMember } = useAppGardenActionsContext();
  const { handleOpenCrew } = useCrewPanelContext();
  const {
    queueModeEnabled,
    handleToggleQueueMode,
    crewQueueEnabled,
    handleToggleCrewQueue,
    queueBands,
    openSnoozeMenu,
  } = useAttentionQueueContext();
  const { onScreenSessionIds } = useDesktopResidencyContext();
  const {
    leafDesktopDrag,
    dragHoverDesktopId,
    handleDesktopDragEnter,
    handleDesktopDragLeave,
    handleDesktopDragDrop,
    handleNewDesktopDrop,
    handleSessionDragStart,
    handleLeafDragEnd,
  } = useLeafDragContext();
  const { handleNewSession } = useSessionLaunchContext();
  const { handleRequestCloseSession, handleReloadSession } = useSessionLifecycleContext();
  const { sidebarHeaderActions, dockItems } = useAppSidebarActions();
  const { desktopNavigation } = useDesktopNavigationContext();
  return (
    <Sidebar
      workspaces={desktopViews}
      visualIndexByWorkspaceId={slotIndexByDesktopId}
      selectedId={activeSessionId}
      selectedWorkspaceId={currentDesktopId}
      selectedTile={selectedTile ? { workspaceId: selectedTile.desktopId, tileId: selectedTile.tileId } : null}
      tileContents={desktopTileContents}
      collapsed={sidebarCollapsed}
      instance={BUILD_INSTANCE}
      headerActions={sidebarHeaderActions}
      criticalNotifications={criticalNotifications}
      onOpenNotifications={openNotificationsPanel}
      gridLayout={gridLayout}
      onSelectGridLayout={handleSelectGridLayout}
      dockItems={dockItems}
      dockCollapsed={keybindings.dock.collapsed}
      onToggleDockCollapsed={() => keybindings.setDockCollapsed(!keybindings.dock.collapsed)}
      onRenameSession={sendRenameSession}
      onRenameWorkspace={desktopNavigation.renameDesktop}
      onWorkspaceReorder={({ workspaceId, prevWorkspaceId, nextWorkspaceId }) =>
        desktopNavigation.reorderDesktop({
          desktopId: workspaceId,
          previousDesktopId: prevWorkspaceId,
          nextDesktopId: nextWorkspaceId,
        })
      }
      onChangeChiefOfStaff={handleChangeChiefOfStaff}
      showSessionless
      crew={crew}
      onWakeCrewMember={handleWakeCrewMember}
      onSleepCrewMember={handleSleepCrewMember}
      onManageCrew={(event) => handleOpenCrew(undefined, event.currentTarget)}
      onOpenCrewMemberDetails={handleOpenCrew}
      queueModeEnabled={queueModeEnabled}
      onToggleQueueMode={handleToggleQueueMode}
      crewQueueEnabled={crewQueueEnabled}
      onToggleCrewQueue={handleToggleCrewQueue}
      harnessLogosEnabled={areSidebarHarnessLogosEnabled(settings)}
      onToggleHarnessLogos={handleToggleSidebarHarnessLogos}
      workspaceSelectionStyle={workspaceSelectionStyle}
      onWorkspaceSelectionStyleChange={handleWorkspaceSelectionStyleChange}
      leafDrag={leafDesktopDrag ? { sourceWorkspaceId: leafDesktopDrag.sourceDesktopId } : null}
      dragHoverWorkspaceId={dragHoverDesktopId}
      onWorkspaceDragEnter={handleDesktopDragEnter}
      onWorkspaceDragLeave={handleDesktopDragLeave}
      onWorkspaceDragDrop={handleDesktopDragDrop}
      onNewWorkspaceDrop={handleNewDesktopDrop}
      onSessionDragStart={handleSessionDragStart}
      onSessionDragEnd={handleLeafDragEnd}
      queue={queueBands}
      onSettleTurn={sendSettleTurn}
      onOpenSnooze={openSnoozeMenu}
      onWakeTurn={sendWakeTurn}
      onScreenSessionIds={onScreenSessionIds}
      onSelectSession={handleSelectSession}
      onTriggerNudge={sendTriggerNudge}
      onSelectWorkspace={handleSelectDesktop}
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
  );
}
