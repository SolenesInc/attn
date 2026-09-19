import { invoke, isTauri } from '@tauri-apps/api/core';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useClientPresence } from '../hooks/useClientPresence';
import { type DockPanelId } from '../hooks/useDockPanels';
import { useKeyboardShortcuts } from '../hooks/useKeyboardShortcuts';
import { usePRsNeedingAttention } from '../hooks/usePRsNeedingAttention';
import { useSessionWorkspaceController } from '../hooks/useSessionWorkspaceController';
import { useUiAutomationBridge } from '../hooks/useUiAutomationBridge';
import { useDaemonStore } from '../store/daemonSessions';
import { useSessionStore } from '../store/sessions';
import { getAgentAvailability } from '../utils/agentAvailability';
import { latestPresentationBySessionId } from '../utils/presentationNotices';
import { appOverlayPolicy } from './appOverlayPolicy';
import { AppContentProps, diagnosticFocusKind } from './appSupport';
import { useAppAppearance } from './useAppAppearance';
import { useAppDeepLinks } from './useAppDeepLinks';
import { useAppDiagnostics } from './useAppDiagnostics';
import { useAppErrors } from './useAppErrors';
import { useAppGardenActions } from './useAppGardenActions';
import { useCrewPanel } from './useCrewPanel';
import { useAppGrid } from './useAppGrid';
import { useAppNavigation } from './useAppNavigation';
import { useAppNotebookSurface } from './useAppNotebookSurface';
import { useAppPanels } from './useAppPanels';
import { useAppSessions } from './useAppSessions';
import { useAttentionQueue } from './useAttentionQueue';
import { useChiefOfStaff } from './useChiefOfStaff';
import { usePRLauncher } from './usePRLauncher';
import { useSessionLaunch } from './useSessionLaunch';
import { useSessionLifecycle } from './useSessionLifecycle';
import { useWorkflowPanel } from './useWorkflowPanel';
import { useWorkspaceDrag } from './useWorkspaceDrag';
import { useWorkspaceResidency } from './useWorkspaceResidency';
import { useWorkspaceTiles } from './useWorkspaceTiles';

export function useAppController({
  daemonSessions,
  daemonWorkspaces,
  prs,
  daemonEndpoints,
  daemonPlugins,
  daemonPluginIssues,
  daemonGitHubHosts,
  githubPollingOffReason,
  settings,
  updateAvailableVersion,
  onOpenLatestRelease,
  onDismissLatestRelease,
  presentationNotices,
  settingError,
  clearSettingError,
  notificationsUnread,
  criticalNotifications,
  notificationsChangeSignal,
  fsChangeSignals,
  notebookTaskChangeSignal,
  sessionCloseNotice,
  sessionResolutionNotice,
  registerSessionExitHandler,
}: AppContentProps) {
  const hasCriticalNotification = criticalNotifications.count > 0;

  const {
    connectionError,
    connectionGeneration,
    hasReceivedInitialState,
    sendSetSetting,
    sendDeleteWorktree,
    sendFsList,
    sendFsRead,
    sendFsWrite,
    sendFsExists,
    sendFsReadAsset,
    sendFsWatch,
    sendFsUnwatch,
    sendFsIndex,
    sendNotebookBacklinks,
    sendNotebookToChief,
    sendCancelCountdown,
    sendOpenMarkdown,
    sendOpenSeed,
    sendSessionMessagesGet,
    subscribeSessionMessagesChanged,
    sendSessionAnnotationsGet,
    sendSessionAnnotationsSave,
    sendSessionAnnotationsClear,
    sendSessionAnnotationsSubmit,
    sendWorkspaceMoveLeafToWorkspace,
    sendRuntimeInput,
    sendSetClientPresence,
    isRuntimeAttached,
    sendSeedHandover,
    sendSeedToChief,
    sendSeedResume,
    sendCrewWake,
    sendCrewSleep,
    sendSupportSnapshot,
  } = useDaemonApi();

  const presentationBySessionId = useMemo(
    () => latestPresentationBySessionId(presentationNotices),
    [presentationNotices],
  );
  const handleOpenPresentationWindow = useCallback((presentationId: string) => {
    void invoke('open_presentation_window', { presentationId }).catch((err) => {
      console.error('[App] Failed to open presentation window:', err);
    });
  }, []);

  const { connect, sessions, activeSessionId, reloadSession } = useSessionStore();

  const appErrors = useAppErrors({ settingError, clearSettingError });
  const { showError } = appErrors;

  const workspaceRuntime = useSessionWorkspaceController(sessions, activeSessionId);
  const {
    getActivePaneIdForSession,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
    removeWorkspaceRef,
    getWorkspaceLeafDropSnapshot,
    focusWorkspaceLeaf,
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    scrollSessionPaneToTop,
    fitSessionActivePane,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    getPaneVisibleStyleSummary,
    getPaneBlockState,
    getPanePlacementState,
    resetSessionPaneTerminal,
    injectSessionPaneBytes,
    injectSessionPaneBase64,
    drainSessionPaneTerminal,
  } = workspaceRuntime;

  const appSessions = useAppSessions({
    activeSessionId,
    daemonEndpoints,
    sessions,
    daemonSessions,
    daemonWorkspaces,
    connect,
  });
  const { enrichedLocalSessions, workspaceViews, unmutedWorkspaceViews, unmutedEnrichedSessions } =
    appSessions;

  const attentionQueue = useAttentionQueue({
    settings,
    unmutedWorkspaceViews,
    workspaceViews,
    unmutedEnrichedSessions,
    enrichedLocalSessions,
    activeSessionId,
  });
  const {
    wantsAttention,
    waitingLocalSessions,
    handleSettleActiveTurn,
    handleSnoozeActiveSession,
  } = attentionQueue;

  const navigation = useAppNavigation({
    activeSessionId,
    daemonSessions,
    daemonWorkspaces,
    workspaceViews,
    unmutedEnrichedSessions,
    attentionQueue,
    focusWorkspaceLeaf,
  });
  const {
    view,
    setView,
    selectAgent,
    selectAgentPane,
    cancelPendingSelection,
    navigateAgentHistoryBack,
    navigateAgentHistoryForward,
    handleSelectSession,
    selectCreatedSession,
    goToDashboard,
    toggleGridMode,
    handleJumpToWaiting,
    activeWorkspaceId,
    activeWorkspaceIdRef,
    handleSelectWorkspace,
    handleSelectTile,
    handleCloseTile,
    setCrewSeedTile,
    handleSelectWorkspaceByIndex,
    handlePrevWorkspace,
    handleNextWorkspace,
    handleSelectOrchestrator,
  } = navigation;

  const sessionLaunch = useSessionLaunch({
    settings,
    daemonSessions,
    daemonEndpoints,
    sessions,
    activeSessionId,
    getActivePaneIdForSession,
    selectCreatedSession,
    showError,
  });
  const {
    sessionCreationJob,
    createWorkspaceSession,
    createSessionForUiAutomation,
    locationPickerOpen,
    handleNewWorkspace,
    handleNewSession,
    createSplitSession,
    chooseReopenDirectory,
  } = sessionLaunch;

  const prLauncher = usePRLauncher({ settings, createWorkspaceSession, selectCreatedSession });
  const { openPRLauncherJob, handleRefreshPRs } = prLauncher;

  const appAppearance = useAppAppearance({ settings });
  const { increaseScale, decreaseScale, resetScale } = appAppearance;

  const agentSurfaceCount =
    sessions.length +
    workspaceViews.filter((workspace) => workspace.hasUnresolvedAgentPanes).length;
  const appPanels = useAppPanels({ agentSurfaceCount });
  const crewPanelState = useCrewPanel();
  const { crewPanel, closeCrewPanel } = crewPanelState;
  const {
    settingsOpen,
    setSettingsOpen,
    settingsModalRef,
    shortcutsOpen,
    setShortcutsOpen,
    shortcutEditorOpen,
    setShortcutEditorOpen,
    actionMenuOpen,
    setActionMenuOpen,
    delegationChainRef,
    sessionsOpen,
    setSessionsOpen,
    openLedger,
    notebookOpen,
    whatsNew,
    toggleDockPanel,
    openDockPanel,
    toggleSidebarCollapse,
    workflowRunPanelOpen,
    gardenHoldsWindow,
    toggleGardenFrame,
    openNotebookBrowser,
  } = appPanels;

  const workspaceTiles = useWorkspaceTiles({
    settings,
    sessions,
    daemonWorkspaces,
    activeSessionId,
    activeWorkspaceIdRef,
  });
  const {
    markdownOpenerOpen,
    appViewParamsPrompt,
    handleOpenMarkdownFile,
    handleOpenNotebookTile,
  } = workspaceTiles;

  const { seeds } = useDaemonStore();
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);

  useAppDeepLinks({ selectAgent, createWorkspaceSession, selectCreatedSession });

  const onReopened = useCallback(() => setSessionsOpen(false), [setSessionsOpen]);
  const sessionLifecycle = useSessionLifecycle({
    activeSessionId,
    activeWorkspaceId,
    handleCloseTile,
    getActivePaneIdForSession,
    sessions,
    daemonSessions,
    enrichedLocalSessions,
    registerSessionExitHandler,
    removeWorkspaceRef,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
    getPaneSize,
    selectAgentPane,
    handleSelectSession,
    showError,
    chooseReopenDirectory,
    onReopened,
  });
  const {
    pendingSessionClose,
    handleCloseSession,
    handleClosePane,
    handleCloseCurrentSessionShortcut,
  } = sessionLifecycle;

  useClientPresence(sendSetClientPresence, {
    dashboardVisible: view === 'dashboard',
    connected: hasReceivedInitialState,
  });
  const appShellRef = useRef<HTMLDivElement>(null);
  const [zoomModeBySessionId, setZoomModeBySessionId] = useState<Record<string, boolean>>({});
  const appDiagnostics = useAppDiagnostics({
    sessions,
    daemonWorkspaces,
    getPaneSize,
    activeSessionId,
    getActivePaneIdForSession,
    view,
    settings,
    sendSupportSnapshot,
    getPaneText,
  });
  const { diagnosticCapture, actionMenuOriginRef } = appDiagnostics;

  const chiefOfStaff = useChiefOfStaff({ enrichedLocalSessions, daemonSessions, showError });
  const { chiefTransferTarget } = chiefOfStaff;

  const [contextCapPromptSession, setContextCapPromptSession] = useState<{
    id: string;
    label: string;
    currentCap?: number;
  } | null>(null);

  const workflowPanel = useWorkflowPanel({ activeSessionId, workflowRunPanelOpen });

  const { blockingOverlayOpen, actionMenuBlocked, appShortcutsEnabled } = appOverlayPolicy({
    locationPickerOpen,
    whatsNewOpen: whatsNew.isOpen,
    settingsOpen,
    shortcutsOpen,
    shortcutEditorOpen,
    actionMenuOpen,
    sessionsOpen,
    notebookOpen,
    crewPanelOpen: crewPanel.open,
    gardenHoldsWindow,
    chiefTransferOpen: Boolean(chiefTransferTarget),
    contextCapOpen: Boolean(contextCapPromptSession),
    appViewParamsOpen: Boolean(appViewParamsPrompt),
    sessionCloseOpen: Boolean(pendingSessionClose),
    sessionCreationOpen: Boolean(sessionCreationJob),
    prLauncherOpen: Boolean(openPRLauncherJob),
    diagnosticCaptureOpen: Boolean(diagnosticCapture),
    markdownOpenerOpen,
  });

  // Views with nothing focusable (dashboard, empty workspaces) can leave the WebView off first responder, killing EVERY shortcut until the user clicks the window.
  useEffect(() => {
    const claimShellFocus = () => {
      if (activeSessionId) return;
      if (blockingOverlayOpen) return;
      const shell = appShellRef.current;
      if (!shell) return;
      const active = document.activeElement;
      if (active && active !== document.body) return;
      shell.focus({ preventScroll: true });
    };
    claimShellFocus();
    window.addEventListener('focus', claimShellFocus);
    return () => window.removeEventListener('focus', claimShellFocus);
  }, [activeSessionId, blockingOverlayOpen, view]);

  const seedForSession = useCallback(
    (sessionId: string) => {
      const seed = seeds.find((candidate) => candidate.tender_session === sessionId);
      return seed ? { id: seed.id, title: seed.title } : null;
    },
    [seeds],
  );
  const handleDeleteWorktreeFromPanel = useCallback(
    async (path: string, force: boolean) => {
      const result = await sendDeleteWorktree(path, undefined, force ? { force: true } : {});
      if (!result.success) {
        throw new Error(result.error || 'Deleting the worktree failed');
      }
    },
    [sendDeleteWorktree],
  );

  const handleToggleActionMenu = useCallback(() => {
    if (actionMenuOpen) {
      setActionMenuOpen(false);
      return;
    }
    if (actionMenuBlocked) {
      return;
    }
    const activeSession = activeSessionId
      ? sessions.find((session) => session.id === activeSessionId)
      : null;
    actionMenuOriginRef.current = {
      capturedAtUnixMs: Date.now(),
      view,
      activeSessionId,
      activePaneId: activeSession ? getActivePaneIdForSession(activeSession) || null : null,
      activeElement: diagnosticFocusKind(document.activeElement),
      documentFocused: document.hasFocus(),
      visibility: document.visibilityState,
      window: {
        width: window.innerWidth,
        height: window.innerHeight,
        devicePixelRatio: window.devicePixelRatio,
      },
    };
    delegationChainRef.current?.prepareCommand();
    setActionMenuOpen(true);
  }, [
    actionMenuOpen,
    actionMenuBlocked,
    actionMenuOriginRef,
    setActionMenuOpen,
    delegationChainRef,
    activeSessionId,
    sessions,
    getActivePaneIdForSession,
    view,
  ]);
  useUiAutomationBridge({
    sessions,
    activeSessionId,
    daemonReady: hasReceivedInitialState && !connectionError,
    connectionError,
    getActivePaneIdForSession,
    createSession: createSessionForUiAutomation,
    selectSession: handleSelectSession,
    selectWorkspace: handleSelectWorkspace,
    moveWorkspaceLeafToWorkspace: sendWorkspaceMoveLeafToWorkspace,
    closeSession: handleCloseSession,
    reloadSession,
    setSetting: sendSetSetting,
    openDockPanel: (panelId: string) => openDockPanel(panelId as DockPanelId),
    openShortcutEditor: () => setShortcutEditorOpen(true),
    splitPane: (sessionId, paneId, direction) => {
      return createSplitSession('shell', direction, paneId, { baseSessionId: sessionId });
    },
    closePane: handleClosePane,
    focusPane: (sessionId: string, paneId: string) => {
      const ownerSessionId = sessions.find((session) =>
        session.workspace.agents.some(
          (pane) => pane.id === paneId && pane.sessionId === session.id,
        ),
      )?.id;
      selectAgentPane(ownerSessionId ?? sessionId, paneId);
    },
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    scrollSessionPaneToTop,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    getPaneVisibleStyleSummary,
    getPaneBlockState,
    getPanePlacementState,
    fitSessionActivePane,
    sendRuntimeInput,
    isRuntimeAttached,
    openAutomationsPanel: () => openDockPanel('automations'),
    openWorktreesPanel: () => openLedger('worktrees'),
    presentationNotices,
    resetSessionPaneTerminal,
    injectSessionPaneBytes,
    injectSessionPaneBase64,
    drainSessionPaneTerminal,
  });

  const { needsAttention: prsNeedingAttention } = usePRsNeedingAttention(prs);
  const attentionCount = waitingLocalSessions.length + prsNeedingAttention.length;

  const appGrid = useAppGrid({
    unmutedEnrichedSessions,
    wantsAttention,
    cancelPendingSelection,
    setView,
  });
  const { visibleGridTiles } = appGrid;

  const workspaceResidency = useWorkspaceResidency({
    workspaceViews,
    activeWorkspaceId,
    view,
    visibleGridTiles,
  });
  const { onScreenSessionIds } = workspaceResidency;

  const visibleCountdownSessionIds = useMemo(() => {
    const ids: string[] = [];
    for (const session of enrichedLocalSessions) {
      if (!onScreenSessionIds.has(session.id)) continue;
      if (session.autoSettleFiresAt || session.autoSettleHeld || session.nudgeFiresAt)
        ids.push(session.id);
    }
    return ids;
  }, [enrichedLocalSessions, onScreenSessionIds]);
  const armDismissSessionId = useMemo(
    () =>
      activeSessionId && onScreenSessionIds.has(activeSessionId) ? activeSessionId : undefined,
    [activeSessionId, onScreenSessionIds],
  );
  const handleCancelCountdown = useMemo(() => {
    if (visibleCountdownSessionIds.length > 0) {
      return () => visibleCountdownSessionIds.forEach(sendCancelCountdown);
    }
    if (!armDismissSessionId) return undefined;
    return () => sendCancelCountdown(armDismissSessionId);
  }, [visibleCountdownSessionIds, armDismissSessionId, sendCancelCountdown]);

  const workspaceDrag = useWorkspaceDrag({
    activeWorkspaceIdRef,
    getWorkspaceLeafDropSnapshot,
    handleSelectWorkspace,
  });

  const appGardenActions = useAppGardenActions({
    sendOpenSeed,
    activeSessionId,
    showError,
    seeds,
    openDockPanel,
    sendFsExists,
    sendOpenMarkdown,
    sendSeedResume,
    handleSelectSession,
    sendSeedHandover,
    sendSeedToChief,
    sendCrewWake,
    sendCrewSleep,
    handleSelectTile,
    focusWorkspaceLeaf,
    setCrewSeedTile,
    closeCrewPanel,
  });

  // One stable object: the surface re-fetches on identity change.
  const annotationApi = useMemo(
    () => ({
      fetchMessages: sendSessionMessagesGet,
      subscribeMessagesChanged: subscribeSessionMessagesChanged,
      fetchAnnotations: sendSessionAnnotationsGet,
      saveAnnotations: sendSessionAnnotationsSave,
      clearAnnotations: sendSessionAnnotationsClear,
      submitAnnotations: sendSessionAnnotationsSubmit,
    }),
    [
      sendSessionMessagesGet,
      subscribeSessionMessagesChanged,
      sendSessionAnnotationsGet,
      sendSessionAnnotationsSave,
      sendSessionAnnotationsClear,
      sendSessionAnnotationsSubmit,
    ],
  );

  const handleQuitApp = useCallback(() => {
    if (isTauri()) {
      void invoke('quit_app');
      return;
    }
    window.close();
  }, []);

  useKeyboardShortcuts({
    onNewSession: () => handleNewSession('vertical'),
    onNewSessionHorizontal: () => handleNewSession('horizontal'),
    onNewWorkspace: handleNewWorkspace,
    onCloseSession: handleCloseCurrentSessionShortcut,
    onToggleActionMenu: handleToggleActionMenu,
    onGoToDashboard: goToDashboard,
    onToggleGridMode: toggleGridMode,
    onJumpToWaiting: handleJumpToWaiting,
    onSettleTurn: handleSettleActiveTurn,
    onSnoozeTurn: handleSnoozeActiveSession,
    onCancelCountdown: handleCancelCountdown,
    onSelectWorkspaceByIndex: handleSelectWorkspaceByIndex,
    onPrevSession: handlePrevWorkspace,
    onNextSession: handleNextWorkspace,
    onHistoryBack: () => navigateAgentHistoryBack(view !== 'session'),
    onHistoryForward: () => navigateAgentHistoryForward(view !== 'session'),
    onSelectOrchestrator: handleSelectOrchestrator,
    onToggleSidebar: toggleSidebarCollapse,
    onRefreshPRs: handleRefreshPRs,
    onToggleAttentionPanel: () => toggleDockPanel('attention'),
    onOpenSettings: useCallback(() => {
      if (settingsOpen) void settingsModalRef.current?.close();
      else setSettingsOpen(true);
    }, [settingsOpen, settingsModalRef, setSettingsOpen]),
    onShowShortcuts: useCallback(() => setShortcutsOpen((prev) => !prev), [setShortcutsOpen]),
    onIncreaseFontSize: increaseScale,
    onDecreaseFontSize: decreaseScale,
    onResetFontSize: resetScale,
    onOpenFile: handleOpenMarkdownFile,
    onOpenNotebookTile: handleOpenNotebookTile,
    onOpenNotebookFullscreen: openNotebookBrowser,
    // The key toggles: it closes whatever list is up, and opens on Sessions, as its name says.
    onOpenSessions: () => (sessionsOpen ? setSessionsOpen(false) : openLedger('sessions')),
    onOpenGarden: toggleGardenFrame,
    onQuit: handleQuitApp,
    enabled: appShortcutsEnabled && !gardenHoldsWindow,
    gardenShortcutEnabled: appShortcutsEnabled,
  });

  const effectiveNotebookRoot = settings['notebook.root.effective'] || '';

  const appNotebookSurface = useAppNotebookSurface({
    sendFsList,
    sendFsRead,
    sendFsWrite,
    sendFsExists,
    sendFsReadAsset,
    sendNotebookBacklinks,
    sendNotebookToChief,
    sendFsIndex,
    fsChangeSignals,
    effectiveNotebookRoot,
    sendFsWatch,
    sendFsUnwatch,
    connectionGeneration,
  });

  return {
    inputs: {
      daemonSessions,
      daemonWorkspaces,
      prs,
      daemonEndpoints,
      daemonPlugins,
      daemonPluginIssues,
      daemonGitHubHosts,
      githubPollingOffReason,
      settings,
      updateAvailableVersion,
      onOpenLatestRelease,
      onDismissLatestRelease,
      presentationNotices,
      settingError,
      clearSettingError,
      notificationsUnread,
      criticalNotifications,
      notificationsChangeSignal,
      fsChangeSignals,
      notebookTaskChangeSignal,
      sessionCloseNotice,
      sessionResolutionNotice,
      registerSessionExitHandler,
    },
    workspaces: {
      workspaceRuntime,
      navigation,
      workspaceTiles,
      workspaceResidency,
      workspaceDrag,
    },
    sessions: {
      appSessions,
      sessionLaunch,
      prLauncher,
      sessionLifecycle,
      chiefOfStaff,
    },
    attention: {
      attentionQueue,
      appGrid,
    },
    libraries: {
      workflowPanel,
      appGardenActions,
      appNotebookSurface,
      crewPanel: crewPanelState,
    },
    shell: {
      surface: {
        presentationBySessionId,
        annotationApi,
        handleOpenPresentationWindow,
        blockingOverlayOpen,
        zoomModeBySessionId,
        setZoomModeBySessionId,
        agentAvailability,
        contextCapPromptSession,
        setContextCapPromptSession,
        handleDeleteWorktreeFromPanel,
        appShellRef,
        seedForSession,
        attentionCount,
        hasCriticalNotification,
      },
      appAppearance,
      appPanels,
      appErrors,
      appDiagnostics,
    },
  };
}
