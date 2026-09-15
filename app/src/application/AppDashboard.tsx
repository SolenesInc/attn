import { Dashboard } from '../components/Dashboard';
import { activityStaleMs } from '../utils/activitySettings';
import { useAppContext } from './AppContext';

export function AppDashboard() {
  const {
    view,
    unmutedEnrichedSessions,
    mutedWorkspaceViews,
    prs,
    hasReceivedInitialState,
    isRefreshingPRs,
    refreshError,
    rateLimit,
    daemonEndpoints,
    handleRebootstrapEndpoint,
    queueModeEnabled,
    crewQueueEnabled,
    settings,
    followNextTurn,
    setFollowNextTurn,
    handleSelectSession,
    handleNewSession,
    sendWakeTurn,
    handleRefreshPRs,
    handleOpenPR,
    setSettingsOpen,
    setSidebarCollapsed,
    setSidebarMutedExpanded,
    setView,
  } = useAppContext();
  return (
    <>
      <div className={`view-container ${view === 'dashboard' ? 'visible' : 'hidden'}`}>
        <Dashboard
          sessions={unmutedEnrichedSessions}
          mutedWorkspaces={mutedWorkspaceViews}
          prs={prs}
          isLoading={!hasReceivedInitialState}
          isRefreshing={isRefreshingPRs}
          refreshError={refreshError}
          rateLimit={rateLimit}
          endpoints={daemonEndpoints}
          onRebootstrapEndpoint={handleRebootstrapEndpoint}
          queueModeEnabled={queueModeEnabled}
          crewQueueEnabled={crewQueueEnabled}
          activityStaleMs={activityStaleMs(settings)}
          followNextTurn={followNextTurn}
          onToggleFollowNextTurn={() => setFollowNextTurn((armed) => !armed)}
          onSelectSession={handleSelectSession}
          onNewSession={() => handleNewSession('vertical')}
          onWakeTurn={sendWakeTurn}
          onRefreshPRs={handleRefreshPRs}
          onOpenPR={handleOpenPR}
          onOpenSettings={() => setSettingsOpen(true)}
          onMutedGroupClick={() => {
            setSidebarCollapsed(false);
            setSidebarMutedExpanded(true);
            setView('session');
          }}
        />
      </div>
    </>
  );
}
