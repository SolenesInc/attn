import { useMemo } from 'react';
import { GardenFrame } from '../components/GardenFrame';
import { NotebookBrowser } from '../components/NotebookBrowser';
import { NotificationsPanel } from '../components/NotificationsPanel';
import { LedgerSurface } from '../components/ledger/LedgerSurface';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import {
  useAppGardenActionsContext,
  useAppInputs,
  useAppNotebookSurfaceContext,
  useAppPanelsContext,
  useAppSessionsContext,
  useAppShell,
  useChiefOfStaffContext,
  useNavigationContext,
  useSessionLaunchContext,
  useSessionLifecycleContext,
} from './AppContexts';

export function AppLibrarySurfaces() {
  const { seedForSession, handleDeleteWorktreeFromPanel } = useAppShell();
  const { liveGardenSessions, worktreePanelSessions } = useAppSessionsContext();
  const profiles = useProfilesStore((state) => state.profiles);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const profileNames = useMemo(
    () => Object.fromEntries(profiles.map((profile) => [profile.id, profile.name])),
    [profiles],
  );
  const daemonSessions = useDaemonStore((state) => state.daemonSessions);
  const profileMembership = useMemo(() => [
    ...profiles.map((profile) => `${profile.id}:${profile.name}`),
    ...daemonSessions.map((session) => `${session.id}@${session.profile_id}`),
  ].sort().join('\n'), [profiles, daemonSessions]);
  const liveSessionUsage = useMemo(() => new Map(daemonSessions.flatMap(
    (session) => (session.usage ? [[session.id, session.usage] as const] : []),
  )), [daemonSessions]);
  const {
    sessionsOpen,
    ledgerTab,
    setLedgerTab,
    setSessionsOpen,
    notebookOpen,
    setNotebookOpen,
    gardenMode,
    gardenDockRect,
    toggleGardenFrame,
    closeGarden,
    notificationsPanelOpen,
    closeNotificationsPanel,
  } = useAppPanelsContext();
  const {
    listKeptConversations,
    setConversationKeep,
    forgetConversation,
    keptConversationsChangeSignal,
    connectionGeneration,
    listWorktrees,
    refreshWorktrees,
    gitOperations,
    sendSessionList,
    subscribeSessionLedger,
    getWorktreeSweepLog,
    setWorktreeKeep,
    hasReceivedInitialState,
    sendSeedTransition,
    sendSeedNote,
    sendSeedDocumentGet,
    seedReviewOverview,
    sendSeedReviewShow,
    sendSeedReviewStart,
    sendSeedReviewRetry,
    sendSeedReviewKeep,
    sendSeedReviewDraft,
    sendNotificationList,
    sendNotificationMarkRead,
    sendTaskRetry,
  } = useDaemonApi();
  const { locationPickerOpen, locationPickerPurpose } = useSessionLaunchContext();
  const { handleSelectSession } = useNavigationContext();
  const {
    handleOpenSeedTile,
    handleOpenMarkdownArtifact,
    checkArtifactPath,
    handleResumeSeed,
    handleHandoverSeed,
    handleSendSeedToChief,
  } = useAppGardenActionsContext();
  const { handleReopenSession } = useSessionLifecycleContext();
  const { notificationsChangeSignal } = useAppInputs();
  const { notebookSurfaceContextValue, notebookRootChangeSignal } = useAppNotebookSurfaceContext();
  const { effectiveNotebookRoot, makeDaemon } = notebookSurfaceContextValue;
  const browserDaemon = useMemo(() => makeDaemon(effectiveNotebookRoot), [makeDaemon, effectiveNotebookRoot]);
  const { notebookChiefActive } = useAppSessionsContext();
  const seeds = useDaemonStore((state) => state.seeds);
  const seedsTotal = useDaemonStore((state) => state.seedsTotal);
  const { hasChiefOfStaff } = useChiefOfStaffContext();
  return (
    <>
      <LedgerSurface
        isOpen={sessionsOpen}
        tab={ledgerTab}
        onTabChange={setLedgerTab}
        onClose={() => setSessionsOpen(false)}
        yieldsFocus={locationPickerOpen && locationPickerPurpose === 'reopen'}
        sessions={{
          connection: {
            list: sendSessionList,
            subscribe: subscribeSessionLedger,
          },
          profileNames,
          profileMembership,
          liveSessionIds: liveGardenSessions,
          liveSessionUsage,
          seedForSession,
          onFocusSession: handleSelectSession,
          onOpenSeed: handleOpenSeedTile,
          onReopen: handleReopenSession,
          setConversationKeep,
          conversationChangeSignal: keptConversationsChangeSignal,
        }}
        conversations={{
          listConversations: listKeptConversations,
          setKeep: setConversationKeep,
          forget: forgetConversation,
          changeSignal: keptConversationsChangeSignal,
          connectionGeneration,
          onOpenSeed: handleOpenSeedTile,
        }}
        worktrees={{
          listWorktrees,
          getSweepLog: getWorktreeSweepLog,
          setKeep: setWorktreeKeep,
          refreshWorktrees,
          deleteWorktree: handleDeleteWorktreeFromPanel,
          sessions: worktreePanelSessions,
          gitOperations,
          onSelectSession: handleSelectSession,
        }}
      />
      <NotebookBrowser
        key={`${selectedProfileId}:${effectiveNotebookRoot}`}
        isOpen={notebookOpen && !!effectiveNotebookRoot}
        onClose={() => setNotebookOpen(false)}
        {...browserDaemon}
        changeSignal={notebookRootChangeSignal}
        chiefActive={notebookChiefActive}
      />
      <GardenFrame
        key={selectedProfileId}
        mode={gardenMode}
        dockRect={gardenDockRect}
        onToggleFrame={toggleGardenFrame}
        onClose={closeGarden}
        seeds={seeds}
        seedsTotal={seedsTotal}
        liveSessions={liveGardenSessions}
        loaded={hasReceivedInitialState}
        moveSeed={sendSeedTransition}
        noteSeed={sendSeedNote}
        fetchSeedDocument={sendSeedDocumentGet}
        onOpenAsTile={(seedId) => {
          closeGarden();
          handleOpenSeedTile(seedId);
        }}
        onOpenMarkdownArtifact={handleOpenMarkdownArtifact}
        checkArtifactPath={checkArtifactPath}
        onResumeSeed={handleResumeSeed}
        onHandoverSeed={handleHandoverSeed}
        onSendSeedToChief={handleSendSeedToChief}
        chiefAvailable={hasChiefOfStaff}
        reviewOverview={seedReviewOverview}
        showReview={sendSeedReviewShow}
        startReview={sendSeedReviewStart}
        retryReviewItem={sendSeedReviewRetry}
        keepReviewItem={sendSeedReviewKeep}
        draftReviewHandover={sendSeedReviewDraft}
      />
      <NotificationsPanel
        open={notificationsPanelOpen}
        onClose={closeNotificationsPanel}
        listNotifications={sendNotificationList}
        markRead={sendNotificationMarkRead}
        retryTask={sendTaskRetry}
        onOpenSession={handleSelectSession}
        changeSignal={notificationsChangeSignal}
      />
    </>
  );
}
