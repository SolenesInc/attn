import { useMemo } from 'react';
import { Dashboard } from '../components/Dashboard';
import { useAskList } from '../store/asks';
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
  const { setSettingsOpen, whatsNew, openAsk } = useAppPanelsContext();
  const { queueBands, queueSessions } = useAttentionQueueContext();
  const askItems = useAskList(queueSessions);
  const asks = useMemo(
    () => askItems.map(({ ask, from }) => ({ id: ask.id, from, subject: ask.subject })),
    [askItems],
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
          queue={queueBands}
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
          onOpenAsk={openAsk}
          introBanner={whatsNew.bannerVisible ? { onReplay: whatsNew.open, onDismiss: whatsNew.dismissBanner } : undefined}
        />
      </div>
    </>
  );
}
