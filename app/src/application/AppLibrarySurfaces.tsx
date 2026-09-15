import { GardenFrame } from '../components/GardenFrame';
import { NotebookBrowser } from '../components/NotebookBrowser';
import { NotificationsPanel } from '../components/NotificationsPanel';
import { LedgerSurface } from '../components/ledger/LedgerSurface';
import { useAppContext } from './AppContext';

export function AppLibrarySurfaces() {
  const {
    seedForSession,
    listWorktrees,
    refreshWorktrees,
    gitOperations,
    sessionsOpen,
    ledgerTab,
    setLedgerTab,
    setSessionsOpen,
    locationPickerOpen,
    locationPickerPurpose,
    sendSessionList,
    workspaceNamesById,
    liveGardenSessions,
    handleSelectSession,
    handleOpenSeedTile,
    handleReopenSession,
    sessionCloseNotice,
    sessionVerdictNotice,
    getWorktreeSweepLog,
    setWorktreeKeep,
    handleDeleteWorktreeFromPanel,
    worktreePanelSessions,
    notebookOpen,
    notebookRequestedPath,
    setNotebookOpen,
    setNotebookRequestedPath,
    sendFsList,
    sendFsRead,
    sendFsWrite,
    sendFsExists,
    sendFsReadAsset,
    sendNotebookBacklinks,
    sendNotebookToChief,
    notebookBrowserListFiles,
    notebookRootChangeSignal,
    notebookChiefActive,
    gardenMode,
    gardenDockRect,
    toggleGardenFrame,
    closeGarden,
    seeds,
    seedsTotal,
    gardenSessionLabels,
    hasReceivedInitialState,
    sendSeedTransition,
    sendSeedNote,
    sendSeedDocumentGet,
    handleOpenMarkdownArtifact,
    checkArtifactPath,
    handleResumeSeed,
    handleHandoverSeed,
    handleSendSeedToChief,
    hasChiefOfStaff,
    seedReviewOverview,
    sendSeedReviewShow,
    sendSeedReviewStart,
    sendSeedReviewRetry,
    sendSeedReviewKeep,
    sendSeedReviewDraft,
    notificationsPanelOpen,
    closeNotificationsPanel,
    sendNotificationList,
    sendNotificationMarkRead,
    sendTaskRetry,
    notificationsChangeSignal,
  } = useAppContext();
  return (
    <>
      <LedgerSurface
        isOpen={sessionsOpen}
        tab={ledgerTab}
        onTabChange={setLedgerTab}
        onClose={() => setSessionsOpen(false)}
        yieldsFocus={locationPickerOpen && locationPickerPurpose === 'reopen'}
        sessions={{
          listSessions: sendSessionList,
          workspaceNames: workspaceNamesById,
          liveSessionIds: liveGardenSessions,
          seedForSession,
          onFocusSession: handleSelectSession,
          onOpenSeed: handleOpenSeedTile,
          onReopen: handleReopenSession,
          closeNotice: sessionCloseNotice,
          verdictNotice: sessionVerdictNotice,
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
