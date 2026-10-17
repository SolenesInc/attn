import { useMemo } from 'react';
import { Dashboard } from '../components/Dashboard';
import { useAsksStore } from '../store/asks';
import { askFrom, askHolder, orderAsks } from '../utils/asks';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { activityStaleMs } from '../utils/activitySettings';
import {
  useAppInputs,
  useAppPanelsContext,
  useAttentionQueueContext,
  useNavigationContext,
  usePRLauncherContext,
  useSessionLaunchContext,
} from './AppContexts';

export function AppDashboard() {
  const { view, followNextTurn, setFollowNextTurn, handleSelectSession } = useNavigationContext();
  const { prs, settings } = useAppInputs();
  const { hasReceivedInitialState, rateLimit, sendWakeTurn } = useDaemonApi();

  const { isRefreshingPRs, refreshError, handleRefreshPRs } = usePRLauncherContext();
  const { setSettingsOpen, whatsNew, openDockPanel } = useAppPanelsContext();
  const { queueModeEnabled, crewQueueEnabled, queueSessions } = useAttentionQueueContext();
  const openAsks = useAsksStore((state) => state.asks);
  const asks = useMemo(
    () => orderAsks(openAsks, queueSessions).map((ask) => ({
      id: ask.id,
      from: askFrom(ask, askHolder(ask, queueSessions)),
      subject: ask.subject,
    })),
    [openAsks, queueSessions],
  );
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
          asks={asks}
          onOpenAsk={(askId) => {
            useAsksStore.getState().selectAsk(askId);
            openDockPanel('asks');
          }}
          introBanner={whatsNew.bannerVisible ? { onReplay: whatsNew.open, onDismiss: whatsNew.dismissBanner } : undefined}
        />
      </div>
    </>
  );
}
