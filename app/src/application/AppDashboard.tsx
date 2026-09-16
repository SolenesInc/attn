import { Dashboard } from '../components/Dashboard';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { activityStaleMs } from '../utils/activitySettings';
import {
  useAppErrorsContext,
  useAppInputs,
  useAppSessionsContext,
  useAppPanelsContext,
  useAttentionQueueContext,
  useNavigationContext,
  usePRLauncherContext,
  useSessionLaunchContext,
} from './AppContexts';

export function AppDashboard() {
  const { unmutedEnrichedSessions, mutedWorkspaceViews } = useAppSessionsContext();
  const { view, followNextTurn, setFollowNextTurn, handleSelectSession, setView } =
    useNavigationContext();
  const { prs, daemonEndpoints, settings } = useAppInputs();
  const { hasReceivedInitialState, rateLimit, sendWakeTurn } = useDaemonApi();

  const { isRefreshingPRs, refreshError, handleRefreshPRs } = usePRLauncherContext();
  const { handleRebootstrapEndpoint } = useAppErrorsContext();
  const { setSettingsOpen, setSidebarCollapsed, setSidebarMutedExpanded } = useAppPanelsContext();
  const { queueModeEnabled, crewQueueEnabled } = useAttentionQueueContext();
  const { handleNewSession } = useSessionLaunchContext();
  const { handleOpenPR } = usePRLauncherContext();
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
