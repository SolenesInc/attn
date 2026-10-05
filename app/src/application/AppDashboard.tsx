import { Dashboard } from '../components/Dashboard';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { activityStaleMs } from '../utils/activitySettings';
import {
  useAppErrorsContext,
  useAppInputs,
  useAppPanelsContext,
  useAttentionQueueContext,
  useNavigationContext,
  usePRLauncherContext,
  useSessionLaunchContext,
} from './AppContexts';

export function AppDashboard() {
  const { view, followNextTurn, setFollowNextTurn, handleSelectSession } = useNavigationContext();
  const { prs, daemonEndpoints, settings } = useAppInputs();
  const { hasReceivedInitialState, rateLimit, sendWakeTurn } = useDaemonApi();

  const { isRefreshingPRs, refreshError, handleRefreshPRs } = usePRLauncherContext();
  const { handleRebootstrapEndpoint } = useAppErrorsContext();
  const { setSettingsOpen, whatsNew } = useAppPanelsContext();
  const { queueModeEnabled, crewQueueEnabled, queueSessions } = useAttentionQueueContext();
  const { handleNewSession } = useSessionLaunchContext();
  const { handleOpenPR } = usePRLauncherContext();
  return (
    <>
      <div className={`view-container ${view === 'dashboard' ? 'visible' : 'hidden'}`}>
        <Dashboard
          sessions={queueSessions}
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
          introBanner={whatsNew.bannerVisible ? { onReplay: whatsNew.open, onDismiss: whatsNew.dismissBanner } : undefined}
        />
      </div>
    </>
  );
}
