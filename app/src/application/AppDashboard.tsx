import { useMemo } from 'react';
import { Dashboard } from '../components/Dashboard';
import { useAsksStore } from '../store/asks';
import { askFrom, askHolder, orderAsks } from '../utils/asks';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { activityStaleMs } from '../utils/activitySettings';
import {
  useAppInputs,
  useAppSessionsContext,
  useAppPanelsContext,
  useAttentionQueueContext,
  useNavigationContext,
  usePRLauncherContext,
  useSessionLaunchContext,
} from './AppContexts';

export function AppDashboard() {
  const { profileSessions } = useAppSessionsContext();
  const { view, followNextTurn, setFollowNextTurn, handleSelectSession } = useNavigationContext();
  const { prs, settings } = useAppInputs();
  const { hasReceivedInitialState, rateLimit, sendWakeTurn } = useDaemonApi();

  const { isRefreshingPRs, refreshError, handleRefreshPRs } = usePRLauncherContext();
  const { setSettingsOpen, whatsNew, openDockPanel } = useAppPanelsContext();
  const openAsks = useAsksStore((state) => state.asks);
  const asks = useMemo(
    () => orderAsks(openAsks, profileSessions).map((ask) => ({
      id: ask.id,
      from: askFrom(ask, askHolder(ask, profileSessions)),
      subject: ask.subject,
    })),
    [openAsks, profileSessions],
  );
  const { queueModeEnabled, crewQueueEnabled } = useAttentionQueueContext();
  const { handleNewSession } = useSessionLaunchContext();
  const { handleOpenPR } = usePRLauncherContext();
  return (
    <>
      <div className={`view-container ${view === 'dashboard' ? 'visible' : 'hidden'}`}>
        <Dashboard
          sessions={profileSessions}
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
