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
  useAppInputs,
  useAppPanelsContext,
  useAppShell,
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
import { useAgentOnScreen } from '../hooks/useDesktopSelectionBridge';

export function AppSidebar() {
  const {
    desktopViews,
    currentDesktopId,
    selectedTile,
    desktopSelectionStyle,
    handleDesktopSelectionStyleChange,
    handleSelectSession,
    handleSelectDesktop,
    handleSelectTile,
    handleCloseTile,
    handleReloadTile,
    goToDashboard,
    handleNextRun,
    handleJumpToWaiting,
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
  const shownAgentId = useAgentOnScreen();
  const focusRequest = useSessionStore((state) => state.focusRequest);
  const {
    desktopTileContents,
    sendRenameSession,
    sendSettleTurn,
    sendWakeTurn,
  } = useDaemonApi();
  const { sidebarCollapsed, openNotificationsPanel, toggleSidebarCollapse, agentListOpen, toggleAgentList } =
    useAppPanelsContext();
  const { setProfileSwitcherOpen, setDesktopOverviewOpen } = useDesktopNavigationContext();
  const { handleOpenPalette, attentionCount, sidebarSurface, windowCovered, agentFocused } = useAppShell();
  const { keybindings, handleToggleSidebarHarnessLogos } = useAppAppearanceContext();
  const { criticalNotifications, settings, notificationsUnread } = useAppInputs();
  const { handleChangeChiefOfStaff } = useChiefOfStaffContext();
  const allCrew = useDaemonStore((state) => state.crew);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const profileName = useProfilesStore(
    (state) => state.profiles.find((profile) => profile.id === state.selectedProfileId)?.name,
  );
  const crew = useMemo(
    () => allCrew.filter((member) => member.profile_id === selectedProfileId),
    [allCrew, selectedProfileId],
  );
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
      desktops={desktopViews}
      visualIndexByDesktopId={slotIndexByDesktopId}
      selectedId={shownAgentId}
      selectionRequest={focusRequest}
      selectedDesktopId={currentDesktopId}
      selectedTile={selectedTile}
      tileContents={desktopTileContents}
      collapsed={sidebarCollapsed}
      surface={sidebarSurface}
      instance={BUILD_INSTANCE}
      headerActions={sidebarHeaderActions}
      criticalNotifications={criticalNotifications}
      onOpenNotifications={openNotificationsPanel}
      dockItems={dockItems}
      dockCollapsed={keybindings.dock.collapsed}
      onToggleDockCollapsed={() => keybindings.setDockCollapsed(!keybindings.dock.collapsed)}
      onRenameSession={sendRenameSession}
      onRenameDesktop={desktopNavigation.renameDesktop}
      onDesktopReorder={({ desktopId, prevDesktopId, nextDesktopId }) =>
        desktopNavigation.reorderDesktop({
          desktopId,
          previousDesktopId: prevDesktopId,
          nextDesktopId,
        })
      }
      onChangeChiefOfStaff={handleChangeChiefOfStaff}
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
      desktopSelectionStyle={desktopSelectionStyle}
      onDesktopSelectionStyleChange={handleDesktopSelectionStyleChange}
      leafDrag={leafDesktopDrag ? { sourceDesktopId: leafDesktopDrag.sourceDesktopId } : null}
      dragHoverDesktopId={dragHoverDesktopId}
      onDesktopDragEnter={handleDesktopDragEnter}
      onDesktopDragLeave={handleDesktopDragLeave}
      onDesktopDragDrop={handleDesktopDragDrop}
      onNewDesktopDrop={handleNewDesktopDrop}
      onSessionDragStart={handleSessionDragStart}
      onSessionDragEnd={handleLeafDragEnd}
      queue={queueBands}
      onSettleTurn={sendSettleTurn}
      onWalkRuns={handleNextRun}
      onJumpToWaiting={handleJumpToWaiting}
      profileName={profileName}
      onSwitchProfile={() => setProfileSwitcherOpen(true)}
      onOpenCommands={() => handleOpenPalette('commands')}
      onOpenAgents={() => handleOpenPalette('agents')}
      peeksSilenced={windowCovered || agentFocused}
      commandsBadge={notificationsUnread + attentionCount}
      agentListOpen={agentListOpen}
      onToggleAgentList={toggleAgentList}
      onOpenOverview={() => setDesktopOverviewOpen(true)}
      onOpenSnooze={openSnoozeMenu}
      onWakeTurn={sendWakeTurn}
      onScreenSessionIds={onScreenSessionIds}
      onSelectSession={handleSelectSession}
      onSelectDesktop={handleSelectDesktop}
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
