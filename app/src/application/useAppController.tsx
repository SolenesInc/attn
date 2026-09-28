import { invoke, isTauri } from '@tauri-apps/api/core';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useClientPresence } from '../hooks/useClientPresence';
import { type DockPanelId } from '../hooks/useDockPanels';
import { useKeyboardShortcuts } from '../hooks/useKeyboardShortcuts';
import { usePRsNeedingAttention } from '../hooks/usePRsNeedingAttention';
import { useDesktopNavigation } from '../hooks/useDesktopNavigation';
import { useDesktopRuntimeController } from '../hooks/useDesktopRuntimeController';
import { useAgentOnScreen, useSessionBehindScreen, useDesktopSelectionBridge, useSurface } from '../hooks/useDesktopSelectionBridge';
import { useUiAutomationBridge } from '../hooks/useUiAutomationBridge';
import { useDaemonStore } from '../store/daemonSessions';
import { useDesktopFocus } from '../store/desktopFocus';
import { useProfilesStore } from '../store/profiles';
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
import type { SidebarSurface } from '../components/sidebarTypes';
import { useAppSessions } from './useAppSessions';
import { useAttentionQueue } from './useAttentionQueue';
import { useChiefOfStaff } from './useChiefOfStaff';
import { usePRLauncher } from './usePRLauncher';
import { useSessionLaunch } from './useSessionLaunch';
import { useSessionLifecycle } from './useSessionLifecycle';
import { useWorkflowPanel } from './useWorkflowPanel';
import { useDesktopResidency } from './useDesktopResidency';
import { useLeafDrag } from './useLeafDrag';
import { useDesktopTiles } from './useDesktopTiles';
import { openPalette, switchPalette, type PaletteMode } from '../components/palette/paletteState';
import { focusLanded, selectionShown } from '../hooks/uiAutomationSelection';

export function useAppController({
  daemonSessions,
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
    sendRuntimeInput,
    sendDesktopMoveLeaf,
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

  const { connect, sessions, reloadSession, selectLeaf } = useSessionStore();
  const shownAgentId = useAgentOnScreen();
  const contextSessionId = useSessionBehindScreen();

  const appErrors = useAppErrors({ settingError, clearSettingError });
  const { showError, showNotice } = appErrors;

  const desktopRuntime = useDesktopRuntimeController(sessions, shownAgentId);
  const {
    getActivePaneIdForSession,
    getDesktopLeafDropSnapshot,
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
  } = desktopRuntime;
  useDesktopSelectionBridge(showError);

  const appSessions = useAppSessions({
    daemonEndpoints,
    sessions,
    daemonSessions,
    connect,
  });
  const { enrichedLocalSessions, desktopViews, profileSessions } = appSessions;

  const attentionQueue = useAttentionQueue({
    settings,
    desktopViews,
    profileSessions,
    enrichedLocalSessions,
    shownAgentId,
  });
  const {
    wantsAttention,
    waitingLocalSessions,
    handleSettleShortcut,
    handleSnoozeShortcut,
    queueModeEnabled,
  } = attentionQueue;

  const navigation = useAppNavigation({
    shownAgentId,
    daemonSessions,
    desktopViews,
    profileSessions,
    attentionQueue,
    showError,
    showNotice,
  });
  const {
    view,
    setView,
    selectAgent,
    cancelIntent,
    navigateLeafHistoryBack,
    navigateLeafHistoryForward,
    handleSelectSession,
    selectCreatedSession,
    goToDashboard,
    toggleGridMode,
    handleJumpToWaiting,
    handleNextRun,
    currentDesktopIdRef,
    handleSelectDesktop,
    handleCloseTile,
    setCrewSeedTile,
    handleNavigateOutOfSession,
    handleSelectOrchestrator,
  } = navigation;

  const sessionLaunch = useSessionLaunch({
    settings,
    daemonEndpoints,
    sessions,
    shownAgentId,
    selectCreatedSession,
    showError,
  });
  const {
    sessionCreationJob,
    launchAgent,
    createSessionForUiAutomation,
    locationPickerOpen,
    handleNewSession,
    createSplitSession,
    chooseReopenDirectory,
  } = sessionLaunch;

  const prLauncher = usePRLauncher({ settings, launchAgent });
  const { openPRLauncherJob, handleRefreshPRs } = prLauncher;

  const appAppearance = useAppAppearance({ settings });
  const { increaseScale, decreaseScale, resetScale } = appAppearance;

  const agentSurfaceCount =
    sessions.length +
    desktopViews.filter((group) => group.hasUnresolvedAgentPanes).length;
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
    palette,
    setPalette,
    delegationChainRef,
    sessionsOpen,
    setSessionsOpen,
    openLedger,
    notebookOpen,
    whatsNew,
    toggleDockPanel,
    openDockPanel,
    toggleSidebarCollapse,
    sidebarCollapsed,
    toggleAgentList,
    closeAgentList,
    workflowRunPanelOpen,
    gardenHoldsWindow,
    toggleGardenFrame,
    openNotebookBrowser,
  } = appPanels;

  const desktopTiles = useDesktopTiles({
    settings,
    sessions,
    contextSessionId,
    showError,
  });
  const {
    markdownOpenerOpen,
    handleOpenMarkdownFile,
    handleOpenNotebookTile,
  } = desktopTiles;

  const { seeds } = useDaemonStore();
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);

  useAppDeepLinks({ selectAgent, launchAgent });

  const onReopened = useCallback(() => setSessionsOpen(false), [setSessionsOpen]);
  const sessionLifecycle = useSessionLifecycle({
    shownAgentId,
    handleCloseTile,
    sessions,
    daemonSessions,
    enrichedLocalSessions,
    registerSessionExitHandler,
    getPaneSize,
    handleSelectSession,
    showError,
    chooseReopenDirectory,
    onReopened,
  });
  const {
    handleCloseSession,
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
    getPaneSize,
    contextSessionId,
    getActivePaneIdForSession,
    view,
    settings,
    sendSupportSnapshot,
    getPaneText,
  });
  const { diagnosticCapture, paletteOriginRef } = appDiagnostics;

  const chiefOfStaff = useChiefOfStaff({ enrichedLocalSessions, daemonSessions, showError });
  const { chiefTransferTarget } = chiefOfStaff;

  const [contextCapPromptSession, setContextCapPromptSession] = useState<{
    id: string;
    label: string;
    currentCap?: number;
  } | null>(null);

  const workflowPanel = useWorkflowPanel({ contextSessionId, workflowRunPanelOpen });

  const [desktopOverviewOpen, setDesktopOverviewOpen] = useState(false);
  const [profileSwitcherOpen, setProfileSwitcherOpen] = useState(false);
  const { blockingOverlayOpen, windowCovered, paletteBlocked, appShortcutsEnabled } = appOverlayPolicy({
    desktopOverviewOpen,
    profileSwitcherOpen,
    locationPickerOpen,
    whatsNewOpen: whatsNew.isOpen,
    settingsOpen,
    shortcutsOpen,
    shortcutEditorOpen,
    paletteOpen: palette !== null,
    snoozeMenuOpen: Boolean(attentionQueue.snoozeMenu),
    sessionsOpen,
    notebookOpen,
    crewPanelOpen: crewPanel.open,
    gardenHoldsWindow,
    chiefTransferOpen: Boolean(chiefTransferTarget),
    contextCapOpen: Boolean(contextCapPromptSession),
    sessionCreationOpen: Boolean(sessionCreationJob),
    prLauncherOpen: Boolean(openPRLauncherJob),
    diagnosticCaptureOpen: Boolean(diagnosticCapture),
    markdownOpenerOpen,
  });

  // Views with nothing focusable (dashboard, empty desktops) can leave the WebView off first responder, killing EVERY shortcut until the user clicks the window.
  useEffect(() => {
    const claimShellFocus = () => {
      if (shownAgentId) return;
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
  }, [shownAgentId, blockingOverlayOpen, view]);

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

  const surface = useSurface();
  const focusedLeafByDesktop = useDesktopFocus((state) => state.focusedLeafByDesktop);
  const currentDesktopAgentFocused = useProfilesStore((state) => {
    const leafId = state.currentDesktopId ? focusedLeafByDesktop[state.currentDesktopId] : undefined;
    const desktop = state.desktops.find((entry) => entry.id === state.currentDesktopId);
    return Boolean(leafId && desktop?.panes.some((pane) => pane.pane_id === leafId && pane.session_id));
  });
  const agentFocused = (surface.kind === 'agent' || surface.kind === 'tile') && currentDesktopAgentFocused;
  const sidebarVisible = !sidebarCollapsed && surface.kind !== 'grid' && !agentFocused;
  const queueSidebarShown = queueModeEnabled && sidebarVisible;
  const sidebarHidden = surface.kind === 'grid' || agentFocused;
  const sidebarSurface: SidebarSurface = sidebarHidden
    ? 'hidden'
    : `${queueModeEnabled ? 'queue' : 'tree'}-${sidebarCollapsed ? 'collapsed' : 'open'}`;
  const previousSidebarSurface = useRef(sidebarSurface);
  useLayoutEffect(() => {
    const changed = previousSidebarSurface.current !== sidebarSurface;
    previousSidebarSurface.current = sidebarSurface;
    if (!changed) return;
    closeAgentList();
    const focused = document.activeElement;
    if (!focused || focused === document.body || focused.closest('.sidebar')) {
      useSessionStore.getState().requestTerminalFocus();
    }
  }, [closeAgentList, sidebarSurface]);

  const handleOpenPalette = useCallback((mode: PaletteMode) => {
    if (palette !== null) {
      setPalette(switchPalette(palette, mode));
      return;
    }
    if (paletteBlocked) {
      return;
    }
    const activeSession = contextSessionId
      ? sessions.find((session) => session.id === contextSessionId)
      : null;
    paletteOriginRef.current = {
      capturedAtUnixMs: Date.now(),
      view,
      activeSessionId: contextSessionId,
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
    actionMenuFocusOriginRef.current = document.activeElement instanceof HTMLElement
      ? document.activeElement : null;
    delegationChainRef.current?.prepareCommand();
    setPalette(openPalette(mode));
  }, [
    palette,
    paletteBlocked,
    paletteOriginRef,
    setPalette,
    delegationChainRef,
    contextSessionId,
    sessions,
    getActivePaneIdForSession,
    view,
  ]);
  useUiAutomationBridge({
    sessions,
    shownAgentId,
    daemonReady: hasReceivedInitialState && !connectionError,
    connectionError,
    getActivePaneIdForSession,
    createSession: createSessionForUiAutomation,
    selectSession: handleSelectSession,
    selectDesktop: handleSelectDesktop,
    moveDesktopLeaf: sendDesktopMoveLeaf,
    closeSession: handleCloseSession,
    reloadSession,
    setSetting: sendSetSetting,
    openDockPanel: (panelId: string) => openDockPanel(panelId as DockPanelId),
    openShortcutEditor: () => setShortcutEditorOpen(true),
    splitPane: (sessionId, paneId, direction) => {
      return createSplitSession('shell', direction, paneId, { baseSessionId: sessionId });
    },
    closePaneSession: handleCloseSession,
    focusPane: async (sessionId: string, paneId: string) => {
      const owner = sessions.find((session) =>
        session.desktop.agents.some(
          (pane) => pane.id === paneId && pane.sessionId === session.id,
        ),
      ) ?? sessions.find((session) => session.id === sessionId);
      if (!owner?.desktopId) throw new Error(`focus_pane: pane ${paneId} of session ${sessionId} is on no desktop`);
      selectLeaf(owner.desktopId, paneId);
      await selectionShown({ kind: 'leaf', desktopId: owner.desktopId, leafId: paneId });
      await focusLanded(owner.desktopId, paneId);
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
    profileSessions,
    wantsAttention,
    cancelIntent,
    setView,
  });
  const { visibleGridTiles } = appGrid;

  const leafDrag = useLeafDrag({ currentDesktopIdRef, getDesktopLeafDropSnapshot, handleSelectDesktop, showError });
  const desktopResidency = useDesktopResidency({
    desktopViews,
    view,
    visibleGridTiles,
    dragSourceDesktopId: leafDrag.leafDesktopDrag?.sourceDesktopId ?? null,
  });
  const { onScreenSessionIds } = desktopResidency;

  const visibleCountdownSessionIds = useMemo(() => {
    const ids: string[] = [];
    for (const session of enrichedLocalSessions) {
      if (!onScreenSessionIds.has(session.id)) continue;
      if (session.autoSettleFiresAt || session.autoSettleHeld)
        ids.push(session.id);
    }
    return ids;
  }, [enrichedLocalSessions, onScreenSessionIds]);
  const armDismissSessionId = useMemo(
    () =>
      shownAgentId && onScreenSessionIds.has(shownAgentId) ? shownAgentId : undefined,
    [shownAgentId, onScreenSessionIds],
  );
  const handleCancelCountdown = useMemo(() => {
    if (visibleCountdownSessionIds.length > 0) {
      return () => visibleCountdownSessionIds.forEach(sendCancelCountdown);
    }
    if (!armDismissSessionId) return undefined;
    return () => sendCancelCountdown(armDismissSessionId);
  }, [visibleCountdownSessionIds, armDismissSessionId, sendCancelCountdown]);

  const appGardenActions = useAppGardenActions({
    sendOpenSeed,
    contextSessionId,
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

  const showNavigationNotice = useCallback((message: string) => showError(message), [showError]);
  const desktopNavigation = useDesktopNavigation(showNavigationNotice);

  useKeyboardShortcuts({
    onNewSession: () => handleNewSession('vertical'),
    onNewSessionHorizontal: () => handleNewSession('horizontal'),
    onCloseSession: handleCloseCurrentSessionShortcut,
    onOpenPalette: handleOpenPalette,
    onGoToDashboard: goToDashboard,
    onToggleGridMode: toggleGridMode,
    onJumpToWaiting: handleJumpToWaiting,
    onNextRun: handleNextRun,
    onSettleTurn: handleSettleShortcut,
    onSnoozeTurn: handleSnoozeShortcut,
    onCancelCountdown: handleCancelCountdown,
    onSwitchToDesktopSlot: (slot) => {
      const looking = useSessionStore.getState().view === 'session';
      setView('session');
      desktopNavigation.switchToSlot(slot, looking);
    },
    onSendToDesktopSlot: desktopNavigation.sendActivePaneToSlot,
    onOpenDesktopOverview: () => setDesktopOverviewOpen(true),
    onSwitchProfile: () => setProfileSwitcherOpen(true),
    onPrevSession: () => handleNavigateOutOfSession('left'),
    onNextSession: () => handleNavigateOutOfSession('right'),
    onHistoryBack: () => navigateLeafHistoryBack(view !== 'session'),
    onHistoryForward: () => navigateLeafHistoryForward(view !== 'session'),
    onSelectOrchestrator: handleSelectOrchestrator,
    onToggleSidebar: toggleSidebarCollapse,
    onShowAgentList: queueSidebarShown ? toggleAgentList : () => handleOpenPalette('agents'),
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
    blocked: Boolean(attentionQueue.snoozeMenu),
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
      registerSessionExitHandler,
    },
    desktops: {
      desktopRuntime,
      navigation,
      desktopNavigation,
      desktopTiles,
      desktopResidency,
      leafDrag,
      desktopOverviewOpen,
      setDesktopOverviewOpen,
      profileSwitcherOpen,
      setProfileSwitcherOpen,
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
        windowCovered,
        zoomModeBySessionId,
        setZoomModeBySessionId,
        agentFocused,
        sidebarSurface,
        agentAvailability,
        contextCapPromptSession,
        setContextCapPromptSession,
        handleDeleteWorktreeFromPanel,
        appShellRef,
        seedForSession,
        attentionCount,
        hasCriticalNotification,
        handleOpenPalette,
      },
      appAppearance,
      appPanels,
      appErrors,
      appDiagnostics,
    },
  };
}
