import { GardenFrame } from '../components/GardenFrame';
import { NotebookBrowser } from '../components/NotebookBrowser';
import { NotificationsPanel } from '../components/NotificationsPanel';
import { LedgerSurface } from '../components/ledger/LedgerSurface';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
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
  const { workspaceNamesById, liveGardenSessions, worktreePanelSessions, gardenSessionLabels } =
    useAppSessionsContext();
  const {
    sessionsOpen,
    ledgerTab,
    setLedgerTab,
    setSessionsOpen,
    notebookOpen,
    notebookRequestedPath,
    setNotebookOpen,
    setNotebookRequestedPath,
    gardenMode,
    gardenDockRect,
    toggleGardenFrame,
    closeGarden,
    notificationsPanelOpen,
    closeNotificationsPanel,
  } = useAppPanelsContext();
  const {
    listWorktrees,
    refreshWorktrees,
    gitOperations,
    sendSessionList,
    subscribeSessionLedger,
    getWorktreeSweepLog,
    setWorktreeKeep,
    sendFsList,
    sendFsRead,
    sendFsWrite,
    sendFsExists,
    sendFsReadAsset,
    sendNotebookBacklinks,
    sendNotebookToChief,
    connectionGeneration,
    isConnected,
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
  const { notebookBrowserListFiles, notebookRootChangeSignal } = useAppNotebookSurfaceContext();
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
            connected: isConnected,
            generation: connectionGeneration,
          },
          workspaceNames: workspaceNamesById,
          liveSessionIds: liveGardenSessions,
          seedForSession,
          onFocusSession: handleSelectSession,
          onOpenSeed: handleOpenSeedTile,
          onReopen: handleReopenSession,
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
        isOpen={notebookOpen}
        initialPath={notebookRequestedPath}
        onClose={() => {
          setNotebookOpen(false);
          setNotebookRequestedPath(null);
        }}
        listDir={sendFsList}
        readFile={sendFsRead}
        writeFile={sendFsWrite}
        existsFile={sendFsExists}
        readAsset={sendFsReadAsset}
        backlinksNotebook={sendNotebookBacklinks}
        sendToChief={sendNotebookToChief}
        listFiles={notebookBrowserListFiles}
        changeSignal={notebookRootChangeSignal}
        chiefActive={notebookChiefActive}
      />
      <GardenFrame
        mode={gardenMode}
        dockRect={gardenDockRect}
        onToggleFrame={toggleGardenFrame}
        onEscapeFloor={closeGarden}
        onClose={closeGarden}
        seeds={seeds}
        seedsTotal={seedsTotal}
        liveSessions={liveGardenSessions}
        tenderSessionLabels={gardenSessionLabels}
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
