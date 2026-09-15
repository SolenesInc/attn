import { appOverlayPolicy } from './appOverlayPolicy';
import { invoke, isTauri } from '@tauri-apps/api/core';
import type { MouseEvent as ReactMouseEvent } from 'react';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { controlBrowserHost } from '../browser/host';
import { type DelegationChainHandle } from '../components/DelegationChain';
import { useErrorToast } from '../components/ErrorToast';
import { useDockSlotRect } from '../components/GardenFrame';
import type { DockTarget } from '../components/SessionTerminalWorkspace/dockTarget';
import { type SettingsModalHandle } from '../components/SettingsModal';
import type { LedgerTab } from '../components/ledger/LedgerSurface';
import { OPENER_EXTENSIONS } from '../components/palette/MarkdownOpener';
import { resolveMarkdownOpenerTarget } from '../components/palette/openerTarget';
import { claimPaletteFocus } from '../components/palette/paletteClaim';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useKeybindings } from '../contexts/KeybindingsContext';
import { useAgentNavigation } from '../hooks/useAgentNavigation';
import { useAppView } from '../hooks/useAppView';
import { useClientPresence } from '../hooks/useClientPresence';
import { DaemonPR, DaemonWorkspace, SessionExitInfo } from '../hooks/useDaemonSocket';
import { useDockPanels, type DockPanelId } from '../hooks/useDockPanels';
import { useGardenPresentation } from '../hooks/useGardenPresentation';
import { useGardenScale } from '../hooks/useGardenScale';
import { useKeyboardShortcuts } from '../hooks/useKeyboardShortcuts';
import { useOpenPR, type OpenPRProgress } from '../hooks/useOpenPR';
import { usePRsNeedingAttention } from '../hooks/usePRsNeedingAttention';
import { useSessionWorkspaceController } from '../hooks/useSessionWorkspaceController';
import { useTheme } from '../hooks/useTheme';
import { useUIScale } from '../hooks/useUIScale';
import { useUiAutomationBridge } from '../hooks/useUiAutomationBridge';
import { useWhatsNew } from '../hooks/useWhatsNew';
import { useWorkspaceSelectionController } from '../hooks/useWorkspaceSelectionController';
import { ptySpawn } from '../pty/bridge';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { useDaemonStore } from '../store/daemonSessions';
import {
  isSessionReloading,
  useSessionStore,
  type TerminalWorkspaceState,
} from '../store/sessions';
import {
  selectLatestWorkflowRunForSession,
  useWorkflowRunsStore,
  workflowRunIdNeedingHydration,
} from '../store/workflowRuns';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import { isAttentionSessionState, type UISessionState } from '../types/sessionState';
import {
  localWorkspaceDirectory,
  resolveEditorTileRoot,
  serializeNotebookTileParams,
  soleWorkspaceForId,
  workspaceSnapshotFromDaemonWorkspace,
  type TerminalSplitDirection,
} from '../types/workspace';
import {
  agentLabel,
  getAgentAvailability,
  getAgentExecutableSettings,
  hasAnyAvailableAgents,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import { appViewTileKind } from '../utils/appBundle';
import { dispatcherOf } from '../utils/delegationLinks';
import { latestPresentationBySessionId } from '../utils/presentationNotices';
import {
  advanceAfterTurnClosed,
  buildQueueBands,
  headOfQueue,
  isCrewQueueEnabled,
  isQueueModeEnabled,
  oldestWantedTurn,
  QUEUE_CREW_SETTING,
  QUEUE_MODE_SETTING,
  sessionParticipatesInQueue,
} from '../utils/queueBands';
import {
  areSidebarHarnessLogosEnabled,
  SIDEBAR_HARNESS_LOGOS_SETTING,
} from '../utils/sidebarHarnessLogos';
import { getTerminalAnsiPaletteColors, getTerminalTheme } from '../utils/terminalSizing';
import {
  computeWarmWorkspaceIds,
  DEFAULT_WARM_WORKSPACE_LIMIT,
  readWarmWorkspaceLimit,
  writeWarmWorkspaceLimit,
} from '../utils/terminalVirtualization';
import { probeUiAfterSwitch, UI_DIAGNOSTICS_FILE_DISPLAY } from '../utils/uiDiagnosticsLog';
import {
  persistWorkspaceSelectionStyle,
  readWorkspaceSelectionStyle,
  type WorkspaceSelectionStyle,
} from '../utils/workspaceSelectionStyle';
import { buildWorkspaceViewModels } from '../utils/workspaceViewModels';
import {
  AppContentProps,
  diagnosticFocusKind,
  LeafDragPreviewState,
  LeafWorkspaceDragState,
  LocationPickerPurpose,
  OpenPRLauncherJob,
  paneIdForSession,
  persistShowSessionlessWorkspaces,
  readShowSessionlessWorkspaces,
  sessionCloseProtectionHint,
  SessionCreationJob,
  SIDEBAR_LEAF_DROP_PLACEMENT,
  SplitSessionOptions,
  TERMINAL_AGENT,
} from './appSupport';
import { useAppDeepLinks } from './useAppDeepLinks';
import { useAppDiagnostics } from './useAppDiagnostics';
import { useAppGardenActions } from './useAppGardenActions';
import { useAppGrid } from './useAppGrid';
import { useAppNotebookSurface } from './useAppNotebookSurface';
import { useAppSessions } from './useAppSessions';
import { useWorkspaceCreation } from './useWorkspaceCreation';

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
  sessionVerdictNotice,
  registerSessionExitHandler,
}: AppContentProps) {
  const hasCriticalNotification = criticalNotifications.count > 0;

  const {
    connectionError,
    disconnectExplanation,
    clearDisconnectExplanation,
    connectionGeneration,
    hasReceivedInitialState,
    rateLimit,
    warnings,
    clearWarnings,
    sendPRAction,
    getScreenSnapshot,
    sendMutePR,
    sendMuteRepo,
    sendMuteAuthor,
    sendMuteWorkspace,
    sendPinWorkspace,
    sendPinSession,
    sendPRVisited,
    sendRefreshPRs,
    sendRegisterWorkspace,
    sendUnregisterWorkspace,
    sendRenameSession,
    sendRenameWorkspace,
    sendSetChiefOfStaff,
    sendSetSessionContextWindowCap,
    sendUnregisterSession,
    sendSetSetting,
    sendSaveSetting,
    sendCreateWorktree,
    sendDeleteWorktree,
    sendListPlugins,
    sendInstallPlugin,
    sendInstallBundledPlugin,
    sendUninstallPlugin,
    sendRemovePlugin,
    sendSetPluginPriority,
    sendAddEndpoint,
    sendUpdateEndpoint,
    sendRemoveEndpoint,
    sendSetEndpointRemoteWeb,
    sendBootstrapEndpoint,
    sendFsList,
    sendFsRead,
    sendFsWrite,
    sendFsExists,
    sendFsReadAsset,
    sendFsWatch,
    sendFsUnwatch,
    sendFsIndex,
    sendRecentFiles,
    sendTaskList,
    sendTaskRetry,
    sendNotificationList,
    sendNotificationMarkRead,
    sendNotebookBacklinks,
    sendNotebookToChief,
    sendGetRecentLocations,
    sendBrowseDirectory,
    sendInspectPath,
    sendCreateWorktreeFromBranch,
    sendFetchPRDetails,
    sendEnsureRepo,
    sendSessionSelected,
    sendTriggerNudge,
    sendSettleTurn,
    sendSnoozeTurn,
    sendWakeTurn,
    sendCancelCountdown,
    sendWorkspaceSelected,
    sendWorkspaceAddSessionPane,
    sendWorkspaceClosePane,
    sendWorkspaceSetSplitRatio,
    sendWorkspaceDockTile,
    sendWorkspaceUndockTile,
    sendWorkspaceUpdateTile,
    sendOpenMarkdown,
    sendOpenSeed,
    sendSeedDocumentGet,
    sendSeedTransition,
    sendSeedNote,
    sendSessionMessagesGet,
    subscribeSessionMessagesChanged,
    sendSessionAnnotationsGet,
    sendSessionAnnotationsSave,
    sendSessionAnnotationsClear,
    sendSessionAnnotationsSubmit,
    sendWorkspaceMoveLeaf,
    sendWorkspaceMoveLeafToWorkspace,
    sendWorkspaceMoveLeafToNewWorkspace,
    sendSetWorkspaceRank,
    tileContents,
    requestTileContent,
    sendRuntimeInput,
    sendTerminalPointerActivity,
    sendSetClientPresence,
    sendSetTerminalTheme,
    isRuntimeAttached,
    getRepoInfo,
    listWorkflowRuns,
    getWorkflowRun,
    listWorktrees,
    setWorktreeKeep,
    getWorktreeSweepLog,
    refreshWorktrees,
    gitOperations,
    listAutomationDefinitions,
    listAutomationRuns,
    setAutomationEnabled,
    runAutomationNow,
    getAutomationDefinition,
    applyAutomationDefinition,
    deleteAutomationDefinition,
    sendSeedHandover,
    sendSeedToChief,
    sendSeedResume,
    seedReviewOverview,
    sendSeedReviewShow,
    sendSeedReviewStart,
    sendSeedReviewRetry,
    sendSeedReviewKeep,
    sendSeedReviewDraft,
    sendCrewWake,
    sendCrewSleep,
    sendSessionList,
    sendSessionReopen,
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

  const [openPRLauncherJob, setOpenPRLauncherJob] = useState<OpenPRLauncherJob | null>(null);
  const openPRLauncherIdRef = useRef(0);
  const [sessionCreationJob, setSessionCreationJob] = useState<SessionCreationJob | null>(null);
  const sessionCreationJobIdRef = useRef(0);
  const worktreeSessionCreateEndpointsRef = useRef<Set<string>>(new Set());
  const {
    connect,
    sessions,
    activeSessionId,
    createSession,
    closeSession,
    setActiveSession,
    navigateAgentHistory,
    takeSessionSpawnArgs,
    reloadSession,
    setLauncherConfig,
    syncFromDaemonSessions,
    syncFromDaemonWorkspaces,
  } = useSessionStore();

  const [selectedSessionlessWorkspaceId, setSelectedSessionlessWorkspaceId] = useState<
    string | null
  >(null);
  const [selectedTileRequest, setSelectedTile] = useState<{
    workspaceId: string;
    tileId: string;
  } | null>(null);
  const selectWorkspaceRef = useRef<(workspaceId: string) => void>(() => {});
  const { view, setView, followNextTurn, setFollowNextTurn } = useAppView(activeSessionId);
  const [utilityFocusRequestToken, setUtilityFocusRequestToken] = useState(0);

  const revealSessionView = useCallback(() => {
    setSelectedTile(null);
    setSelectedSessionlessWorkspaceId(null);
    setView('session');
  }, [setView]);

  const requestTerminalFocus = useCallback(() => {
    setUtilityFocusRequestToken((token) => token + 1);
  }, []);

  const {
    eventRouter: paneRuntimeEventRouter,
    getActivePaneIdForSession,
    setActivePane,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
    setWorkspaceRef,
    removeWorkspaceRef,
    getWorkspaceLeafDropSnapshot,
    focusWorkspaceLeaf,
    focusSessionPane,
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
  } = useSessionWorkspaceController(sessions, activeSessionId);

  const {
    selectAgent,
    selectAgentPane,
    cancelPendingSelection,
    back: navigateAgentHistoryBack,
    forward: navigateAgentHistoryForward,
  } = useAgentNavigation({
    sessions,
    setActiveSession,
    navigateAgentHistory,
    setActivePane,
    focusSessionPane,
    revealSessionView,
    requestTerminalFocus,
  });

  const handleSelectSession = selectAgent;
  const selectCreatedSession = selectAgent;

  const { createWorkspaceSession, rollbackSessionCreation, createSessionForUiAutomation } =
    useWorkspaceCreation({
      sendWorkspaceClosePane,
      closeSession,
      sendUnregisterWorkspace,
      sendRegisterWorkspace,
      createSession,
      takeSessionSpawnArgs,
      sendWorkspaceAddSessionPane,
      selectCreatedSession,
      sessionCreationJob,
      daemonSessions,
      setSessionCreationJob,
    });

  const { scale, increaseScale, decreaseScale, resetScale } = useUIScale();
  const terminalFontSize = Math.round(14 * scale);

  const gardenScale = useGardenScale(scale);

  const { preference: themePreference, resolved: resolvedTheme, setTheme } = useTheme();
  const keybindings = useKeybindings();

  // The daemon worker answers OSC 10/11/12 on the frontend's behalf, so it needs the resolved theme; hasReceivedInitialState doubles as the reconnect signal.
  useEffect(() => {
    if (!hasReceivedInitialState) return;
    const theme = getTerminalTheme(resolvedTheme);
    sendSetTerminalTheme({
      foreground: theme.foreground,
      background: theme.background,
      cursor: theme.cursor,
      ansi_palette: getTerminalAnsiPaletteColors(resolvedTheme),
    });
  }, [hasReceivedInitialState, resolvedTheme, sendSetTerminalTheme]);

  const [settingsOpen, setSettingsOpen] = useState(false);
  const settingsModalRef = useRef<SettingsModalHandle>(null);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [shortcutEditorOpen, setShortcutEditorOpen] = useState(false);
  const [markdownOpenerOpen, setMarkdownOpenerOpen] = useState(false);
  const [actionMenuOpen, setActionMenuOpen] = useState(false);
  const delegationChainRef = useRef<DelegationChainHandle>(null);
  const actionMenuReturnFocusRef = useRef<HTMLElement | null>(null);
  const [seedPopoverRequest, setSeedPopoverRequest] = useState<{
    sessionId: string;
    nonce: number;
  }>();
  const [usagePopoverRequest, setUsagePopoverRequest] = useState<{
    sessionId: string;
    nonce: number;
  }>();
  const [sessionsOpen, setSessionsOpen] = useState(false);
  const [ledgerTab, setLedgerTab] = useState<LedgerTab>('sessions');
  const openLedger = useCallback((tab: LedgerTab) => {
    setLedgerTab(tab);
    setSessionsOpen(true);
  }, []);
  const [notebookOpen, setNotebookOpen] = useState(false);
  const [notebookRequestedPath, setNotebookRequestedPath] = useState<string | null>(null);
  const [notificationsPanelOpen, setNotificationsPanelOpen] = useState(false);
  const whatsNew = useWhatsNew();
  const { repoStates, authorStates, seeds, seedsTotal, apps, crew } = useDaemonStore();
  const mutedRepos = useMemo(
    () => repoStates.filter((r) => r.muted).map((r) => r.repo),
    [repoStates],
  );
  const mutedAuthors = useMemo(
    () => authorStates.filter((a) => a.muted).map((a) => a.author),
    [authorStates],
  );
  const [isRefreshingPRs, setIsRefreshingPRs] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);

  const [pendingSessionClose, setPendingSessionClose] = useState<{
    id: string;
    label: string;
    splitCount: number;
  } | null>(null);

  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = useMemo(
    () => hasAnyAvailableAgents(agentAvailability),
    [agentAvailability],
  );

  useEffect(() => {
    setLauncherConfig({
      executables: getAgentExecutableSettings(settings),
    });
  }, [settings, setLauncherConfig]);

  useEffect(() => {
    if (!hasReceivedInitialState) {
      return;
    }
    syncFromDaemonSessions(daemonSessions);
  }, [daemonSessions, hasReceivedInitialState, syncFromDaemonSessions]);

  useEffect(() => {
    if (!hasReceivedInitialState) {
      return;
    }
    syncFromDaemonWorkspaces(daemonWorkspaces);
  }, [daemonWorkspaces, hasReceivedInitialState, syncFromDaemonWorkspaces]);

  const handleRefreshPRs = useCallback(async () => {
    setIsRefreshingPRs(true);
    setRefreshError(null);
    try {
      const result = await sendRefreshPRs();
      if (!result.success) {
        setRefreshError(result.error || 'Refresh failed');
      }
    } catch (err) {
      setRefreshError(err instanceof Error ? err.message : 'Refresh failed');
    } finally {
      setIsRefreshingPRs(false);
    }
  }, [sendRefreshPRs]);

  useAppDeepLinks({ selectAgent, createWorkspaceSession, selectCreatedSession });

  const {
    enrichedLocalSessions,
    endpointById,
    visibleEnrichedSessions,
    delegationSessions,
    notebookChiefActive,
  } = useAppSessions({ daemonEndpoints, sessions, daemonSessions, daemonWorkspaces, connect });

  const [sidebarMutedExpanded, setSidebarMutedExpanded] = useState(false);

  useClientPresence(sendSetClientPresence, {
    dashboardVisible: view === 'dashboard',
    connected: hasReceivedInitialState,
  });
  const appShellRef = useRef<HTMLDivElement>(null);
  const { dockState, toggleDockPanel, openDockPanel, closeDockPanel } = useDockPanels();

  useEffect(() => {
    if (view === 'session' && !activeSessionId && sessions.length > 0) {
      selectAgent(sessions[0].id);
    }
  }, [activeSessionId, selectAgent, sessions, view]);

  useEffect(() => {
    if (view === 'session' && activeSessionId) {
      sendSessionSelected(activeSessionId);
    }
  }, [activeSessionId, sendSessionSelected, view]);

  const enterHome = useCallback(
    (awaitingNextTurn: boolean) => {
      cancelPendingSelection();
      setActiveSession(null);
      setView('dashboard');
      setFollowNextTurn(awaitingNextTurn);
    },
    [cancelPendingSelection, setActiveSession, setView, setFollowNextTurn],
  );

  const goToDashboard = useCallback(() => enterHome(false), [enterHome]);

  const goHomeAwaitingNextTurn = useCallback(() => enterHome(true), [enterHome]);

  const toggleGridMode = useCallback(() => {
    cancelPendingSelection();
    setView((prev) => (prev === 'grid' ? (activeSessionId ? 'session' : 'dashboard') : 'grid'));
  }, [activeSessionId, cancelPendingSelection, setView]);

  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);

  const toggleSidebarCollapse = useCallback(() => {
    if (delegationChainRef.current?.dismiss()) requestTerminalFocus();
    setSidebarCollapsed((prev) => !prev);
  }, [requestTerminalFocus]);

  const prevSessionCountRef = useRef(sessions.length);
  useEffect(() => {
    const prevCount = prevSessionCountRef.current;
    const currentCount = sessions.length;
    prevSessionCountRef.current = currentCount;

    if (currentCount === 0) {
      setSidebarCollapsed(true);
    } else if (prevCount === 0 && currentCount > 0) {
      setSidebarCollapsed(false);
    }
  }, [sessions.length]);

  const [locationPickerOpen, setLocationPickerOpen] = useState(false);
  const [locationPickerPurpose, setLocationPickerPurpose] =
    useState<LocationPickerPurpose>('workspace');
  const locationPickerSessionDirection = useRef<TerminalSplitDirection>('vertical');
  const reopenPickRef = useRef<{ settle: (path?: string) => void } | null>(null);

  const [zoomModeBySessionId, setZoomModeBySessionId] = useState<Record<string, boolean>>({});
  const {
    message: errorMessage,
    durationMs: errorDurationMs,
    showError,
    clearError,
  } = useErrorToast();
  const {
    diagnosticCapture,
    handleCreateDiagnosticReport,
    actionMenuOriginRef,
    diagnosticReportSaved,
    handleSaveDiagnosticReport,
    setDiagnosticCapture,
  } = useAppDiagnostics({
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

  const [chiefTransferTarget, setChiefTransferTarget] = useState<{
    sessionId: string;
    targetLabel: string;
    currentLabel: string;
  } | null>(null);
  const [chiefTransferSaving, setChiefTransferSaving] = useState(false);
  const [contextCapPromptSession, setContextCapPromptSession] = useState<{
    id: string;
    label: string;
    currentCap?: number;
  } | null>(null);

  const [appViewParamsPrompt, setAppViewParamsPrompt] = useState<{
    app: string;
    view: string;
    viewTitle: string;
    label: string;
    placeholder?: string;
  } | null>(null);

  const handleTerminalModelRecovered = useCallback(() => {
    showError(
      `Terminal issue recovered. We reloaded it for you. Diagnostics were saved to ${UI_DIAGNOSTICS_FILE_DISPLAY}; please send this file to Victor so he can troubleshoot it.`,
      { durationMs: 12_000 },
    );
  }, [showError]);

  const handleRebootstrapEndpoint = useCallback(
    async (endpointId: string) => {
      try {
        await sendBootstrapEndpoint(endpointId);
      } catch (err) {
        showError(err instanceof Error ? err.message : 'Sync failed.');
      }
    },
    [sendBootstrapEndpoint, showError],
  );

  const applyChiefOfStaffChange = useCallback(
    async (sessionId: string, enabled: boolean) => {
      try {
        await sendSetChiefOfStaff(sessionId, enabled);
      } catch (err) {
        showError(err instanceof Error ? err.message : 'Chief of staff update failed.');
        throw err;
      }
    },
    [sendSetChiefOfStaff, showError],
  );

  const handleChangeChiefOfStaff = useCallback(
    (sessionId: string, enabled: boolean) => {
      const target = enrichedLocalSessions.find((session) => session.id === sessionId);
      if (!target) {
        showError('Session not found.');
        return;
      }
      if (!enabled) {
        void applyChiefOfStaffChange(sessionId, false).catch(() => {});
        return;
      }
      const current = enrichedLocalSessions.find((session) => session.chiefOfStaff);
      if (current && current.id !== sessionId) {
        setChiefTransferTarget({
          sessionId,
          targetLabel: target.label,
          currentLabel: current.label,
        });
        return;
      }
      void applyChiefOfStaffChange(sessionId, true).catch(() => {});
    },
    [applyChiefOfStaffChange, enrichedLocalSessions, showError],
  );

  const handleConfirmChiefTransfer = useCallback(async () => {
    if (!chiefTransferTarget || chiefTransferSaving) return;
    setChiefTransferSaving(true);
    try {
      await applyChiefOfStaffChange(chiefTransferTarget.sessionId, true);
      setChiefTransferTarget(null);
    } catch {
    } finally {
      setChiefTransferSaving(false);
    }
  }, [applyChiefOfStaffChange, chiefTransferSaving, chiefTransferTarget]);

  const workflowRunsMap = useWorkflowRunsStore((s) => s.workflowRuns);
  const activeWorkflowRun = useMemo(
    () => selectLatestWorkflowRunForSession(workflowRunsMap, activeSessionId),
    [workflowRunsMap, activeSessionId],
  );
  const activeLocalSession = useMemo(
    () => (activeSessionId ? (sessions.find((s) => s.id === activeSessionId) ?? null) : null),
    [activeSessionId, sessions],
  );
  const activeDaemonSession = useMemo(() => {
    if (!activeSessionId) {
      return null;
    }
    return daemonSessions.find((session) => session.id === activeSessionId) || null;
  }, [activeSessionId, daemonSessions]);
  const activeRemoteSession = Boolean(activeDaemonSession?.endpoint_id);
  const activeEndpoint = useMemo(() => {
    const endpointId = activeDaemonSession?.endpoint_id;
    if (!endpointId) {
      return null;
    }
    return endpointById.get(endpointId) ?? null;
  }, [activeDaemonSession?.endpoint_id, endpointById]);
  const openDockPanels = dockState.openPanels;
  const dockPanelStack = dockState.stack;
  const workflowRunPanelOpen = openDockPanels.workflowRun;
  const attentionPanelOpen = openDockPanels.attention;
  const automationsPanelOpen = openDockPanels.automations;
  const gardenPanelOpen = openDockPanels.garden;
  const openGardenDock = useCallback(() => openDockPanel('garden'), [openDockPanel]);
  const closeGardenDock = useCallback(() => closeDockPanel('garden'), [closeDockPanel]);
  const {
    mode: gardenMode,
    holdsWindow: gardenHoldsWindow,
    toggleFrame: toggleGardenFrame,
    toggleFromIcon: toggleGardenFromIcon,
    close: closeGarden,
  } = useGardenPresentation({
    dockOpen: gardenPanelOpen,
    openDock: openGardenDock,
    closeDock: closeGardenDock,
  });
  const [gardenSlotRef, gardenDockRect] = useDockSlotRect();
  const { blockingOverlayOpen, actionMenuBlocked, appShortcutsEnabled } = appOverlayPolicy({
    locationPickerOpen,
    whatsNewOpen: whatsNew.isOpen,
    settingsOpen,
    shortcutsOpen,
    shortcutEditorOpen,
    actionMenuOpen,
    sessionsOpen,
    notebookOpen,
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

  const openNotebookBrowser = useCallback(() => {
    setNotebookOpen(true);
  }, []);

  const liveGardenSessions = useMemo(
    () => new Set(daemonSessions.map((session) => session.id)),
    [daemonSessions],
  );

  const workspaceNamesById = useMemo(() => {
    const names: Record<string, string> = {};
    for (const workspace of daemonWorkspaces) names[workspace.id] = workspace.name || workspace.id;
    return names;
  }, [daemonWorkspaces]);

  const seedForSession = useCallback(
    (sessionId: string) => {
      const seed = seeds.find((candidate) => candidate.tender_session === sessionId);
      return seed ? { id: seed.id, title: seed.title } : null;
    },
    [seeds],
  );
  const gardenSessionLabels = useMemo(
    () => new Map(daemonSessions.map((session) => [session.id, session.label])),
    [daemonSessions],
  );

  const worktreePanelSessions = useMemo(
    () =>
      daemonSessions.map((session) => ({
        id: session.id,
        label: session.label,
        directory: session.directory,
      })),
    [daemonSessions],
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

  const toggleNotificationsPanel = useCallback(() => {
    setNotificationsPanelOpen((open) => !open);
  }, []);
  const openNotificationsPanel = useCallback(() => {
    setNotificationsPanelOpen(true);
  }, []);
  const closeNotificationsPanel = useCallback(() => {
    setNotificationsPanelOpen(false);
  }, []);

  const activeWorkspaceIdRef = useRef<string | null>(null);

  const daemonWorkspacesRef = useRef<DaemonWorkspace[]>([]);

  // A unique tile id every time: the daemon treats a duplicate id as a move.
  const handleOpenMarkdownFile = useCallback(() => {
    if (claimPaletteFocus()) return;
    setMarkdownOpenerOpen(true);
  }, []);

  const handleToggleQueueMode = useCallback(() => {
    sendSetSetting(QUEUE_MODE_SETTING, isQueueModeEnabled(settings) ? 'false' : 'true');
  }, [sendSetSetting, settings]);
  const handleToggleCrewQueue = useCallback(() => {
    sendSetSetting(QUEUE_CREW_SETTING, isCrewQueueEnabled(settings) ? 'false' : 'true');
  }, [sendSetSetting, settings]);
  const handleToggleSidebarHarnessLogos = useCallback(() => {
    sendSetSetting(
      SIDEBAR_HARNESS_LOGOS_SETTING,
      areSidebarHarnessLogosEnabled(settings) ? 'false' : 'true',
    );
  }, [sendSetSetting, settings]);
  const markdownOpenerTarget = useMemo(
    () =>
      resolveMarkdownOpenerTarget(
        sessions.find((session) => session.id === activeSessionId),
        settings['notebook.root.effective'],
      ),
    [sessions, activeSessionId, settings],
  );
  const loadOpenerRecents = useCallback(
    () =>
      sendRecentFiles(50, markdownOpenerTarget.root || undefined).then((files) =>
        files.map((file) => ({ path: file.path, lastAt: file.lastAt })),
      ),
    [sendRecentFiles, markdownOpenerTarget.root],
  );
  const loadOpenerIndex = useCallback(
    (root: string) => sendFsIndex(root, OPENER_EXTENSIONS),
    [sendFsIndex],
  );

  const handleOpenNotebookTile = useCallback(() => {
    const workspaceId = activeWorkspaceIdRef.current;
    if (!workspaceId) return;
    const tileId = `notebook-tile-${crypto.randomUUID()}`;
    // A twin across endpoints forfeits the workspace-dir default rather than adopt a remote directory: the active id carries no endpoint identity.
    const workspace = soleWorkspaceForId(daemonWorkspacesRef.current, workspaceId);
    const localDirectory = localWorkspaceDirectory(workspace);

    const effectiveNotebookRoot = settings['notebook.root.effective'] || '';
    const root = resolveEditorTileRoot(localDirectory, effectiveNotebookRoot);
    void sendWorkspaceDockTile(workspaceId, tileId, 'notebook', {
      edge: 'right',
      ratio: 0.4,
      tileParams: root ? serializeNotebookTileParams({ root }) : undefined,
    }).catch((error) => {
      console.warn('[App] Failed to dock notebook tile:', error);
    });
  }, [sendWorkspaceDockTile, settings]);

  // A fresh tile id every time: the daemon reads a duplicate id as a move.
  const dockAppViewTile = useCallback(
    (app: string, view: string, params: string) => {
      const workspaceId = activeWorkspaceIdRef.current;
      if (!workspaceId) return;
      void sendWorkspaceDockTile(
        workspaceId,
        `app-view-tile-${crypto.randomUUID()}`,
        appViewTileKind(app, view),
        { edge: 'right', ratio: 0.4, ...(params ? { tileParams: params } : {}) },
      ).catch((error) => {
        console.warn('[App] Failed to dock app view tile:', error);
      });
    },
    [sendWorkspaceDockTile],
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
    actionMenuReturnFocusRef.current =
      delegationChainRef.current?.dismiss() ??
      (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    setActionMenuOpen(true);
  }, [
    actionMenuOpen,
    actionMenuBlocked,
    actionMenuOriginRef,
    activeSessionId,
    sessions,
    getActivePaneIdForSession,
    view,
  ]);
  useEffect(() => {
    if (!settingError) {
      return;
    }
    showError(settingError);
    clearSettingError();
  }, [clearSettingError, settingError, showError]);

  useEffect(() => {
    if (!disconnectExplanation) {
      return;
    }
    showError(disconnectExplanation, { durationMs: 8000 });
    clearDisconnectExplanation();
  }, [clearDisconnectExplanation, disconnectExplanation, showError]);

  useEffect(() => {
    if (!activeSessionId) {
      return;
    }
    listWorkflowRuns(activeSessionId).catch((error) => {
      console.error('[App] Failed to list workflow runs:', error);
    });
  }, [activeSessionId, listWorkflowRuns]);

  // listWorkflowRuns omits agent_calls and a completed run sees no further broadcasts, so without this fetch it stays call-less forever after a reload.
  const workflowRunIdToHydrate = workflowRunIdNeedingHydration(
    workflowRunPanelOpen,
    activeWorkflowRun,
  );
  useEffect(() => {
    if (!workflowRunIdToHydrate) {
      return;
    }
    getWorkflowRun(workflowRunIdToHydrate).catch((error) => {
      console.error('[App] Failed to hydrate workflow run:', error);
    });
  }, [workflowRunIdToHydrate, getWorkflowRun]);

  const handleNewWorkspace = useCallback(() => {
    setLocationPickerPurpose('workspace');
    setLocationPickerOpen(true);
  }, []);

  const nextSplitSessionLabel = useCallback(
    (workspaceId: string, agent: SessionAgent) => {
      const normalizedAgent = normalizeSessionAgent(agent, 'codex');
      const base = normalizedAgent === 'shell' ? 'shell' : normalizedAgent;
      const matchingCount = sessions.filter(
        (session) =>
          session.workspaceId === workspaceId &&
          normalizeSessionAgent(session.agent, 'codex') === normalizedAgent,
      ).length;
      return matchingCount === 0 ? base : `${base} ${matchingCount + 1}`;
    },
    [sessions],
  );

  const createSplitSession = useCallback(
    async (
      agent: SessionAgent,
      direction: 'vertical' | 'horizontal',
      targetPaneId?: string,
      options: SplitSessionOptions = {},
    ) => {
      const activeSession = options.baseSessionId
        ? sessions.find((session) => session.id === options.baseSessionId)
        : activeLocalSession;
      if (!activeSession?.workspaceId) {
        handleNewWorkspace();
        return;
      }
      const sessionId = crypto.randomUUID();
      const workspaceId = activeSession.workspaceId;
      const paneId = targetPaneId || getActivePaneIdForSession(activeSession);
      const newPaneId = paneIdForSession(sessionId);
      const label = options.label || nextSplitSessionLabel(workspaceId, agent);
      const endpointId =
        options.endpointId === null ? undefined : (options.endpointId ?? activeSession.endpointId);
      let paneAdded = false;

      try {
        await createSession(
          label,
          options.cwd || activeSession.cwd,
          sessionId,
          agent,
          endpointId,
          agent === 'shell' ? false : (options.yoloMode ?? activeSession.yoloMode),
          workspaceId,
          undefined,
          agent === 'shell' ? undefined : options.autoMode,
        );
        const spawnArgs = takeSessionSpawnArgs(sessionId, 80, 24);
        await sendWorkspaceAddSessionPane(workspaceId, sessionId, label, {
          paneId: newPaneId,
          targetPaneId: paneId,
          direction,
        });
        paneAdded = true;
        if (spawnArgs) {
          await ptySpawn({ args: { ...spawnArgs, spawned_from: activeSession.id } });
        } else {
          throw new Error('Session spawn arguments were not prepared.');
        }
        selectCreatedSession(sessionId);
      } catch (error) {
        await rollbackSessionCreation({
          sessionId,
          workspaceId,
          paneId: paneAdded ? newPaneId : undefined,
        });
        showError(error instanceof Error ? error.message : 'Failed to create session split');
      }
    },
    [
      activeLocalSession,
      createSession,
      getActivePaneIdForSession,
      handleNewWorkspace,
      nextSplitSessionLabel,
      rollbackSessionCreation,
      sendWorkspaceAddSessionPane,
      sessions,
      selectCreatedSession,
      showError,
      takeSessionSpawnArgs,
    ],
  );

  const handleNewSession = useCallback(
    (direction: TerminalSplitDirection = 'vertical') => {
      if (!activeLocalSession?.workspaceId) {
        handleNewWorkspace();
        return;
      }
      setLocationPickerPurpose('session');
      locationPickerSessionDirection.current = direction;
      setLocationPickerOpen(true);
    },
    [activeLocalSession?.workspaceId, handleNewWorkspace],
  );

  const handleLocationSelect = useCallback(
    async (
      path: string,
      agent: SessionAgent,
      endpointId?: string,
      yoloMode = false,
      chiefOfStaff = false,
      autoMode?: boolean,
    ) => {
      if (locationPickerPurpose === 'reopen') {
        reopenPickRef.current?.settle(path);
        reopenPickRef.current = null;
        return;
      }
      const jobId = sessionCreationJobIdRef.current + 1;
      sessionCreationJobIdRef.current = jobId;
      let selectedAgent: SessionAgent;
      if (endpointId) {
        const endpoint = daemonEndpoints.find((entry) => entry.id === endpointId);
        if (!endpoint) {
          showError('Selected endpoint no longer exists.');
          return;
        }
        if (endpoint.status !== 'connected') {
          showError(`Endpoint ${endpoint.name} is ${endpoint.status}.`);
          return;
        }
        if (agent !== TERMINAL_AGENT && !endpoint.capabilities?.agents_available.includes(agent)) {
          showError(`${agentLabel(agent)} is not available on ${endpoint.name}.`);
          return;
        }
        selectedAgent = agent;
      } else {
        if (agent !== TERMINAL_AGENT && !hasAvailableAgents) {
          showError('No supported agent CLI found in PATH.');
          return;
        }
        selectedAgent =
          agent === TERMINAL_AGENT
            ? TERMINAL_AGENT
            : resolvePreferredAgent(agent, agentAvailability, 'codex');
      }
      const folderName = path.split('/').pop() || 'session';
      if (locationPickerPurpose === 'session' && activeLocalSession?.workspaceId) {
        await createSplitSession(selectedAgent, locationPickerSessionDirection.current, undefined, {
          cwd: path,
          endpointId: endpointId ?? null,
          label: folderName,
          yoloMode,
          autoMode,
        });
        return;
      }
      setSessionCreationJob({
        id: jobId,
        label: folderName,
        path,
        phase: 'starting_session',
        error: null,
      });
      try {
        const sessionId = await createWorkspaceSession(
          folderName,
          path,
          undefined,
          selectedAgent,
          endpointId,
          yoloMode,
          { chiefOfStaff, autoMode },
        );
        selectCreatedSession(sessionId);
        setSessionCreationJob((current) =>
          current?.id === jobId ? { ...current, sessionId, phase: 'starting_session' } : current,
        );
      } catch (err) {
        setSessionCreationJob((current) =>
          current?.id === jobId
            ? { ...current, error: err instanceof Error ? err.message : 'Failed to create session' }
            : current,
        );
      }
    },
    [
      activeLocalSession?.workspaceId,
      agentAvailability,
      createSplitSession,
      createWorkspaceSession,
      daemonEndpoints,
      hasAvailableAgents,
      locationPickerPurpose,
      selectCreatedSession,
      showError,
    ],
  );

  const handleCreateWorktreeSession = useCallback(
    (
      mainRepo: string,
      branchName: string,
      startingFrom: string,
      endpointId: string | undefined,
      agent: SessionAgent,
      yoloMode: boolean,
      autoMode?: boolean,
    ) => {
      const endpointKey = endpointId || 'local';
      if (worktreeSessionCreateEndpointsRef.current.has(endpointKey)) {
        showError('A worktree session is already being created for this target.');
        return;
      }
      worktreeSessionCreateEndpointsRef.current.add(endpointKey);
      const jobId = sessionCreationJobIdRef.current + 1;
      sessionCreationJobIdRef.current = jobId;
      setSessionCreationJob({
        id: jobId,
        label: branchName,
        path: mainRepo,
        phase: 'creating_worktree',
        error: null,
      });

      void (async () => {
        try {
          const result = await sendCreateWorktree(
            mainRepo,
            branchName,
            undefined,
            startingFrom,
            endpointId,
          );
          if (!result.success || !result.path) {
            throw new Error(result.error || 'Failed to create worktree');
          }
          const worktreePath = result.path;
          setSessionCreationJob((current) =>
            current?.id === jobId
              ? { ...current, path: worktreePath, phase: 'starting_session' }
              : current,
          );
          const folderName = worktreePath.split('/').pop() || branchName || 'session';
          if (locationPickerPurpose === 'session' && activeLocalSession?.workspaceId) {
            await createSplitSession(agent, locationPickerSessionDirection.current, undefined, {
              cwd: worktreePath,
              endpointId: endpointId ?? null,
              label: folderName,
              yoloMode,
              autoMode,
            });
            setSessionCreationJob((current) => (current?.id === jobId ? null : current));
            return;
          }
          const sessionId = await createWorkspaceSession(
            folderName,
            worktreePath,
            undefined,
            agent,
            endpointId,
            yoloMode,
            { autoMode },
          );
          selectCreatedSession(sessionId);
          setSessionCreationJob((current) =>
            current?.id === jobId
              ? {
                  ...current,
                  label: folderName,
                  path: worktreePath,
                  phase: 'starting_session',
                  sessionId,
                }
              : current,
          );
        } catch (err) {
          setSessionCreationJob((current) =>
            current?.id === jobId
              ? {
                  ...current,
                  error: err instanceof Error ? err.message : 'Failed to create session',
                }
              : current,
          );
        } finally {
          worktreeSessionCreateEndpointsRef.current.delete(endpointKey);
        }
      })();
    },
    [
      activeLocalSession?.workspaceId,
      createSplitSession,
      createWorkspaceSession,
      locationPickerPurpose,
      selectCreatedSession,
      sendCreateWorktree,
      showError,
    ],
  );

  const closeLocationPicker = useCallback(() => {
    setLocationPickerOpen(false);
    reopenPickRef.current?.settle(undefined);
    reopenPickRef.current = null;
  }, []);

  const hasChiefOfStaff = useMemo(
    () => daemonSessions.some((ds) => ds.chief_of_staff === true),
    [daemonSessions],
  );

  const handleCloseSession = useCallback(
    async (id: string) => {
      const closeProtection = sessionCloseProtectionHint(daemonSessions, id);
      if (closeProtection) {
        showError(closeProtection);
        return;
      }
      const session = enrichedLocalSessions.find((s) => s.id === id);

      const localDaemonSession = daemonSessions.find((ds) => ds.id === session?.id);
      if (localDaemonSession && session) {
        await sendUnregisterSession(session.id);
      } else {
        closeSession(id);
      }

      if (session) {
        removeWorkspaceRef(session.workspaceId);
      }
    },
    [
      closeSession,
      daemonSessions,
      enrichedLocalSessions,
      removeWorkspaceRef,
      sendUnregisterSession,
      showError,
    ],
  );

  const handleClosePane = useCallback(
    (sessionId: string, paneId: string) => {
      const closeProtection = sessionCloseProtectionHint(daemonSessions, sessionId);
      if (closeProtection) {
        showError(closeProtection);
        return Promise.resolve();
      }
      const session = enrichedLocalSessions.find((entry) => entry.id === sessionId);
      const fallbackPaneId = prepareClosePaneFocus(sessionId, paneId);
      const fallbackSessionId = session?.workspace.agents.find(
        (pane) => pane.id === fallbackPaneId && pane.id !== paneId,
      )?.sessionId;
      const workspaceId = sessions.find((session) => session.id === sessionId)?.workspaceId;
      if (!workspaceId) {
        return Promise.reject(
          new Error(`Cannot close pane ${paneId}: session ${sessionId} has no workspace`),
        );
      }
      return sendWorkspaceClosePane(workspaceId, paneId)
        .then((result) => {
          if (fallbackSessionId) {
            selectAgentPane(fallbackSessionId, fallbackPaneId);
          }
          return result;
        })
        .catch((error) => {
          clearPreparedClosePaneFocus(sessionId);
          throw error;
        });
    },
    [
      clearPreparedClosePaneFocus,
      daemonSessions,
      enrichedLocalSessions,
      prepareClosePaneFocus,
      selectAgentPane,
      sendWorkspaceClosePane,
      sessions,
      showError,
    ],
  );

  const handleRequestCloseSession = useCallback(
    (id: string) => {
      const session = sessions.find((entry) => entry.id === id);
      if (!session) {
        return;
      }

      const sessionPane = session.workspace.agents.find((pane) => pane.sessionId === session.id);
      if (sessionPane) {
        void handleClosePane(session.id, sessionPane.id).catch(console.error);
        return;
      }

      void handleCloseSession(id);
    },
    [handleClosePane, handleCloseSession, sessions],
  );

  const handleSessionProcessExit = useCallback(
    (info: SessionExitInfo) => {
      if (info.exitCode !== 0 || info.signal) {
        return;
      }
      // A reload's kill can surface as a clean exit (code 0, no signal); the same id is about to respawn in place, so closing the pane here would tear the workspace down under the pending spawn.
      if (isSessionReloading(info.id)) {
        return;
      }
      handleRequestCloseSession(info.id);
    },
    [handleRequestCloseSession],
  );

  useEffect(() => {
    registerSessionExitHandler(handleSessionProcessExit);
    return () => registerSessionExitHandler(null);
  }, [registerSessionExitHandler, handleSessionProcessExit]);

  const handleCancelSessionClose = useCallback(() => {
    setPendingSessionClose(null);
  }, []);

  const handleConfirmSessionClose = useCallback(() => {
    if (!pendingSessionClose) {
      return;
    }
    const sessionID = pendingSessionClose.id;
    setPendingSessionClose(null);
    void handleCloseSession(sessionID);
  }, [handleCloseSession, pendingSessionClose]);

  const handleSelectOrchestrator = useCallback(() => {
    const session = daemonSessions.find((entry) => entry.id === activeSessionId);
    if (!session) return;
    const dispatcher = dispatcherOf(session, daemonSessions);
    if (dispatcher?.session) handleSelectSession(dispatcher.session.id);
  }, [activeSessionId, daemonSessions, handleSelectSession]);

  useUiAutomationBridge({
    sessions,
    activeSessionId,
    daemonReady: hasReceivedInitialState && !connectionError,
    connectionError,
    getActivePaneIdForSession,
    createSession: createSessionForUiAutomation,
    selectSession: handleSelectSession,
    selectWorkspace: (workspaceId: string) => selectWorkspaceRef.current(workspaceId),
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

  const openPR = useOpenPR({
    settings,
    sendFetchPRDetails,
    sendEnsureRepo,
    sendCreateWorktreeFromBranch,
    createSession: createWorkspaceSession,
  });

  const handleOpenPR = useCallback(
    async (pr: DaemonPR) => {
      console.log(`[App] Open PR requested: ${pr.repo}#${pr.number} - ${pr.title}`);

      if (!hasAvailableAgents) {
        alert('No supported agent CLI found in PATH.');
        return;
      }
      const configuredDefaultAgent = normalizeSessionAgent(settings.new_session_agent, 'claude');
      const defaultAgent = resolvePreferredAgent(
        configuredDefaultAgent,
        agentAvailability,
        'codex',
      );
      const launcherId = openPRLauncherIdRef.current + 1;
      openPRLauncherIdRef.current = launcherId;
      const isActiveLauncher = () => openPRLauncherIdRef.current === launcherId;
      const updateLauncherProgress = (progress: OpenPRProgress) => {
        setOpenPRLauncherJob((current) =>
          current?.id === launcherId ? { ...current, progress } : current,
        );
      };

      setOpenPRLauncherJob({
        id: launcherId,
        pr,
        progress: { step: pr.head_branch ? 'ensuring_repo' : 'fetching_pr_details' },
      });
      const result = await openPR(pr, defaultAgent, { onProgress: updateLauncherProgress }).finally(
        () => {
          if (isActiveLauncher()) {
            setOpenPRLauncherJob(null);
          }
        },
      );
      if (!isActiveLauncher()) {
        return;
      }
      if (result.success) {
        selectCreatedSession(result.sessionId);
        console.log(`[App] Worktree created at ${result.worktreePath}`);
        return;
      }

      const errorMsg = result.error.message || '';
      switch (result.error.kind) {
        case 'missing_projects_directory':
          alert(
            'Please configure your Projects Directory in Settings first.\n\nThis tells the app where to find your local git repositories.',
          );
          break;
        case 'missing_head_branch':
          alert(
            `PR branch information not available.\n\nTry refreshing PRs (${formatShortcut('session.refreshPRs')}) to fetch branch details.`,
          );
          break;
        case 'fetch_pr_details_failed':
          alert(
            `Failed to fetch PR details.\n\n${errorMsg || `Try refreshing PRs (${formatShortcut('session.refreshPRs')}) and try again.`}`,
          );
          break;
        case 'ensure_repo_failed':
        case 'create_worktree_failed':
        case 'create_session_failed':
        case 'unknown': {
          if (errorMsg.includes('clone failed')) {
            alert(
              `Failed to clone repository ${pr.repo}.\n\nError: ${errorMsg}\n\nCheck your network connection and GitHub access.`,
            );
          } else if (errorMsg.includes('already exists')) {
            alert(`A worktree for this branch may already exist.\n\nError: ${errorMsg}`);
          } else {
            alert(`Failed to open PR: ${errorMsg || 'Unknown error'}`);
          }
          break;
        }
      }
    },
    [
      agentAvailability,
      hasAvailableAgents,
      openPR,
      selectCreatedSession,
      settings.new_session_agent,
    ],
  );

  const workspaceViews = useMemo(
    () => buildWorkspaceViewModels(daemonWorkspaces, visibleEnrichedSessions),
    [daemonWorkspaces, visibleEnrichedSessions],
  );
  const unmutedWorkspaceViews = useMemo(
    () =>
      workspaceViews.filter(
        (workspace) => !workspace.muted && (workspace.pinned || workspace.sessions.length > 0),
      ),
    [workspaceViews],
  );
  const mutedWorkspaceViews = useMemo(
    () =>
      workspaceViews.filter(
        (workspace) => workspace.muted && (workspace.pinned || workspace.sessions.length > 0),
      ),
    [workspaceViews],
  );
  const unmutedEnrichedSessions = useMemo(
    () => unmutedWorkspaceViews.flatMap((workspace) => workspace.sessions),
    [unmutedWorkspaceViews],
  );

  const queueModeEnabled = isQueueModeEnabled(settings);
  const crewQueueEnabled = isCrewQueueEnabled(settings);
  const queueBands = useMemo(
    () =>
      queueModeEnabled
        ? buildQueueBands(unmutedWorkspaceViews, { crewInQueue: crewQueueEnabled })
        : null,
    [queueModeEnabled, crewQueueEnabled, unmutedWorkspaceViews],
  );

  const activeWorkspaceForCommands = useMemo(
    () =>
      workspaceViews.find((workspace) =>
        workspace.sessions.some((session) => session.id === activeSessionId),
      ) ?? null,
    [workspaceViews, activeSessionId],
  );
  const activeSessionForCommands = useMemo(
    () =>
      activeWorkspaceForCommands?.sessions.find((session) => session.id === activeSessionId) ??
      null,
    [activeWorkspaceForCommands, activeSessionId],
  );
  const activeSessionQueueEligible = Boolean(
    activeSessionForCommands &&
      sessionParticipatesInQueue(activeSessionForCommands, crewQueueEnabled),
  );

  const wantsAttention = useCallback(
    (session: {
      state: UISessionState;
      turnOwed?: boolean;
      crewMember?: string;
      automation?: { definition_id: string };
    }) =>
      queueModeEnabled
        ? sessionParticipatesInQueue(session, crewQueueEnabled) && Boolean(session.turnOwed)
        : isAttentionSessionState(session.state),
    [queueModeEnabled, crewQueueEnabled],
  );

  const {
    visibleGridTiles,
    gridLayout,
    handleSelectGridLayout,
    resolvedGridLayout,
    gridOffBoardCount,
    hiddenGridSessions,
    handleRemoveFromGrid,
    handleRestoreToGrid,
  } = useAppGrid({ unmutedEnrichedSessions, wantsAttention, cancelPendingSelection, setView });

  const waitingLocalSessions = unmutedEnrichedSessions.filter(wantsAttention);
  const { needsAttention: prsNeedingAttention } = usePRsNeedingAttention(prs);
  const attentionCount = waitingLocalSessions.length + prsNeedingAttention.length;

  const handleSettleActiveTurn = useMemo(
    () =>
      queueModeEnabled && activeSessionQueueEligible
        ? () => {
            if (!activeSessionId) return;
            sendSettleTurn(activeSessionId);
          }
        : undefined,
    [queueModeEnabled, activeSessionQueueEligible, activeSessionId, sendSettleTurn],
  );

  const [snoozeMenu, setSnoozeMenu] = useState<{
    session: { id: string; label: string };
    anchor: { top: number; left: number };
  } | null>(null);

  const openSnoozeMenu = useCallback(
    (session: { id: string; label: string }, event: ReactMouseEvent) => {
      const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
      setSnoozeMenu({ session, anchor: { top: rect.bottom + 4, left: rect.left } });
    },
    [],
  );

  const handleSnoozeActiveSession = useMemo(
    () =>
      queueModeEnabled && activeSessionQueueEligible
        ? () => {
            if (!activeSessionId) return;
            const session = enrichedLocalSessions.find((s) => s.id === activeSessionId);
            if (!session) return;
            const row = document.querySelector<HTMLElement>(
              `[data-testid$="-${activeSessionId}"].queue-row`,
            );
            const rect = row?.getBoundingClientRect();
            setSnoozeMenu({
              session: { id: session.id, label: session.label },
              anchor: rect ? { top: rect.bottom + 4, left: rect.left } : { top: 72, left: 72 },
            });
          }
        : undefined,
    [queueModeEnabled, activeSessionQueueEligible, activeSessionId, enrichedLocalSessions],
  );

  const previousQueueTurnsRef = useRef<NonNullable<typeof queueBands>['turns']>([]);
  useEffect(() => {
    const previousTurns = previousQueueTurnsRef.current;
    previousQueueTurnsRef.current = queueBands?.turns ?? [];
    if (!queueModeEnabled || view !== 'session') return;
    const advance = advanceAfterTurnClosed(previousTurns, queueBands, activeSessionId);
    if (!advance) return;
    if (advance.to === 'session') {
      handleSelectSession(advance.row.session.id);
    } else {
      goHomeAwaitingNextTurn();
    }
  }, [
    queueBands,
    queueModeEnabled,
    view,
    activeSessionId,
    handleSelectSession,
    goHomeAwaitingNextTurn,
  ]);

  useEffect(() => {
    if (!followNextTurn || !queueModeEnabled || view !== 'dashboard') return;
    const next = headOfQueue(queueBands);
    if (next) handleSelectSession(next.session.id);
  }, [followNextTurn, queueModeEnabled, view, queueBands, handleSelectSession]);

  const handleJumpToWaiting = useCallback(() => {
    const waiting = oldestWantedTurn(unmutedEnrichedSessions, wantsAttention);
    if (waiting) {
      handleSelectSession(waiting.id);
    }
  }, [unmutedEnrichedSessions, handleSelectSession, wantsAttention]);

  const [showSessionlessWorkspaces, setShowSessionlessWorkspaces] = useState<boolean>(
    readShowSessionlessWorkspaces,
  );
  const [workspaceSelectionStyle, setWorkspaceSelectionStyle] = useState<WorkspaceSelectionStyle>(
    readWorkspaceSelectionStyle,
  );
  const handleWorkspaceSelectionStyleChange = useCallback((style: WorkspaceSelectionStyle) => {
    persistWorkspaceSelectionStyle(style);
    setWorkspaceSelectionStyle(style);
  }, []);
  useEffect(() => {
    persistShowSessionlessWorkspaces(showSessionlessWorkspaces);
  }, [showSessionlessWorkspaces]);

  const handleToggleShowSessionlessWorkspaces = useCallback(() => {
    setShowSessionlessWorkspaces((prev) => {
      const next = !prev;
      return next;
    });
  }, []);
  const sidebarWorkspaceViews = useMemo(
    () =>
      workspaceViews.filter(
        (workspace) =>
          !workspace.muted &&
          (workspace.pinned || workspace.sessions.length > 0 || showSessionlessWorkspaces),
      ),
    [workspaceViews, showSessionlessWorkspaces],
  );
  const workspaceSelection = useWorkspaceSelectionController(
    workspaceViews,
    activeSessionId,
    selectedSessionlessWorkspaceId,
  );
  const activeWorkspaceId = workspaceSelection.activeWorkspaceId;

  useEffect(() => {
    probeUiAfterSwitch({
      sessionId: activeSessionId,
      workspaceId: activeWorkspaceId,
      view,
    });
  }, [activeSessionId, activeWorkspaceId, view]);

  useEffect(() => {
    activeWorkspaceIdRef.current = activeWorkspaceId;
  }, [activeWorkspaceId]);

  useEffect(() => {
    daemonWorkspacesRef.current = daemonWorkspaces;
  }, [daemonWorkspaces]);
  useEffect(() => {
    if (view === 'session' && activeWorkspaceId) {
      sendWorkspaceSelected(activeWorkspaceId);
    }
  }, [activeWorkspaceId, sendWorkspaceSelected, view]);

  const [warmWorkspaceLimit, setWarmWorkspaceLimit] = useState<number>(() =>
    readWarmWorkspaceLimit(),
  );
  useEffect(() => {
    const w = window as Window & { attnSetWarmWorkspaces?: (n: number) => number };
    w.attnSetWarmWorkspaces = (n: number) => {
      const next = Number.isFinite(n) ? Math.trunc(n) : DEFAULT_WARM_WORKSPACE_LIMIT;
      writeWarmWorkspaceLimit(next);
      setWarmWorkspaceLimit(next);
      console.log(
        `[attn] warm workspace limit = ${next} ` +
          (next < 0
            ? '(virtualization disabled; all workspaces live)'
            : `(active + ${next} recent kept live)`),
      );
      return next;
    };
    return () => {
      delete w.attnSetWarmWorkspaces;
    };
  }, []);
  const [recentWorkspaceIds, setRecentWorkspaceIds] = useState<string[]>([]);
  useEffect(() => {
    if (!activeWorkspaceId) return;
    setRecentWorkspaceIds((prev) =>
      prev[0] === activeWorkspaceId
        ? prev
        : [activeWorkspaceId, ...prev.filter((id) => id !== activeWorkspaceId)].slice(0, 32),
    );
  }, [activeWorkspaceId]);
  const allWorkspaceIds = useMemo(() => workspaceViews.map((w) => w.id), [workspaceViews]);
  const visibleGridSessionIds = useMemo(
    () => new Set(visibleGridTiles.map((tile) => tile.sessionId)),
    [visibleGridTiles],
  );
  const gridVisibleWorkspaceIds = useMemo(
    () =>
      view === 'grid'
        ? workspaceViews
            .filter((workspace) =>
              workspace.sessions.some((session) => visibleGridSessionIds.has(session.id)),
            )
            .map((workspace) => workspace.id)
        : [],
    [view, visibleGridSessionIds, workspaceViews],
  );
  const onScreenSessionIds = useMemo(() => {
    if (view === 'grid') return visibleGridSessionIds;
    if (view !== 'session' || !activeWorkspaceId) return new Set<string>();
    const workspace = workspaceViews.find((w) => w.id === activeWorkspaceId);
    return new Set((workspace?.sessions ?? []).map((session) => session.id));
  }, [view, visibleGridSessionIds, activeWorkspaceId, workspaceViews]);

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

  const warmWorkspaceIds = useMemo(
    () =>
      computeWarmWorkspaceIds(
        allWorkspaceIds,
        recentWorkspaceIds,
        activeWorkspaceId,
        warmWorkspaceLimit,
        gridVisibleWorkspaceIds,
      ),
    [
      allWorkspaceIds,
      recentWorkspaceIds,
      activeWorkspaceId,
      warmWorkspaceLimit,
      gridVisibleWorkspaceIds,
    ],
  );

  const getActiveLeafDropSnapshot = useCallback(
    () => getWorkspaceLeafDropSnapshot(activeWorkspaceIdRef.current),
    [getWorkspaceLeafDropSnapshot],
  );
  const [leafWorkspaceDrag, setLeafWorkspaceDrag] = useState<LeafWorkspaceDragState | null>(null);
  const [leafDragPreview, setLeafDragPreview] = useState<LeafDragPreviewState | null>(null);
  const [dragHoverWorkspaceId, setDragHoverWorkspaceId] = useState<string | null>(null);
  const leafWorkspaceDragRef = useRef<LeafWorkspaceDragState | null>(null);
  const dragHoverTimerRef = useRef<number | null>(null);

  const clearWorkspaceDragHoverTimer = useCallback(() => {
    if (dragHoverTimerRef.current != null) {
      window.clearTimeout(dragHoverTimerRef.current);
      dragHoverTimerRef.current = null;
    }
  }, []);

  const handleLeafDragStart = useCallback(
    (sourceWorkspaceId: string, sourceEndpointId: string | undefined, leafId: string) => {
      clearWorkspaceDragHoverTimer();
      const next = { sourceWorkspaceId, sourceEndpointId, leafId };
      leafWorkspaceDragRef.current = next;
      setLeafWorkspaceDrag(next);
      setLeafDragPreview({ draggingLeafId: leafId, dockTarget: null, ghostPos: null });
      setDragHoverWorkspaceId(null);
    },
    [clearWorkspaceDragHoverTimer],
  );

  const handleLeafDragGhostMove = useCallback((x: number, y: number) => {
    setLeafDragPreview((prev) => (prev ? { ...prev, ghostPos: { x, y } } : prev));
  }, []);

  const handleLeafDragPreview = useCallback((target: DockTarget | null) => {
    setLeafDragPreview((prev) => (prev ? { ...prev, dockTarget: target } : prev));
  }, []);

  const handleLeafDragEnd = useCallback(() => {
    clearWorkspaceDragHoverTimer();
    window.setTimeout(() => {
      leafWorkspaceDragRef.current = null;
      setLeafWorkspaceDrag(null);
      setLeafDragPreview(null);
      setDragHoverWorkspaceId(null);
    }, 0);
  }, [clearWorkspaceDragHoverTimer]);

  useEffect(
    () => () => {
      clearWorkspaceDragHoverTimer();
    },
    [clearWorkspaceDragHoverTimer],
  );

  const sessionlessWorkspaceStateById = useMemo(() => {
    const map = new Map<string, TerminalWorkspaceState>();
    for (const workspace of daemonWorkspaces) {
      if (!workspace.layout) {
        continue;
      }
      const { workspace: state } = workspaceSnapshotFromDaemonWorkspace(workspace.layout);
      if (state.layoutTree && state.agents.length === 0) {
        map.set(workspace.id, state);
      }
    }
    return map;
  }, [daemonWorkspaces]);

  const visualWorkspaces = sidebarWorkspaceViews;
  const visualIndexByWorkspaceId = useMemo(() => {
    return new Map(visualWorkspaces.map((workspace, index) => [workspace.id, index]));
  }, [visualWorkspaces]);

  const handleSelectWorkspace = useCallback(
    (workspaceId: string) => {
      const workspace =
        sidebarWorkspaceViews.find((entry) => entry.id === workspaceId) ||
        workspaceViews.find((entry) => entry.id === workspaceId);
      if (!workspace) {
        return;
      }
      const sessionId = workspace.firstSessionId;
      if (sessionId) {
        handleSelectSession(sessionId);
        return;
      }
      cancelPendingSelection();
      setSelectedSessionlessWorkspaceId(workspace.id);
      setView('session');
      requestTerminalFocus();
    },
    [
      cancelPendingSelection,
      handleSelectSession,
      requestTerminalFocus,
      sidebarWorkspaceViews,
      workspaceViews,
      setView,
    ],
  );
  useLayoutEffect(() => {
    selectWorkspaceRef.current = handleSelectWorkspace;
  }, [handleSelectWorkspace]);

  const handleSelectTile = useCallback(
    (workspaceId: string, tileId: string) => {
      handleSelectWorkspace(workspaceId);
      setSelectedTile({ workspaceId, tileId });
    },
    [handleSelectWorkspace],
  );

  const handleCloseTile = useCallback(
    (workspaceId: string, tileId: string) => {
      setSelectedTile((current) =>
        current?.workspaceId === workspaceId && current.tileId === tileId ? null : current,
      );
      void sendWorkspaceUndockTile(workspaceId, tileId).catch(() => {});
    },
    [sendWorkspaceUndockTile],
  );

  const handleReloadTile = useCallback((workspaceId: string, tileId: string) => {
    void controlBrowserHost(workspaceId, tileId, 'reload').catch((error) => {
      console.warn('[App] Failed to reload browser tile:', error);
    });
  }, []);

  const selectedTile =
    selectedTileRequest &&
    workspaceViews.some(
      (workspace) =>
        workspace.id === selectedTileRequest.workspaceId &&
        workspace.children.some(
          (child) => child.kind === 'tile' && child.tile.tileId === selectedTileRequest.tileId,
        ),
    )
      ? selectedTileRequest
      : null;
  if (selectedTileRequest && !selectedTile) setSelectedTile(null);

  const canMoveDraggedLeafToWorkspace = useCallback(
    (workspace: { id: string; endpointId?: string }) => {
      const drag = leafWorkspaceDragRef.current;
      return Boolean(
        drag &&
          workspace.id !== drag.sourceWorkspaceId &&
          (workspace.endpointId || '') === (drag.sourceEndpointId || ''),
      );
    },
    [],
  );

  const handleWorkspaceDragEnter = useCallback(
    (workspace: { id: string; endpointId?: string }) => {
      if (!canMoveDraggedLeafToWorkspace(workspace)) {
        return;
      }
      clearWorkspaceDragHoverTimer();
      setDragHoverWorkspaceId(workspace.id);
      dragHoverTimerRef.current = window.setTimeout(() => {
        dragHoverTimerRef.current = null;
        handleSelectWorkspace(workspace.id);
      }, 320);
    },
    [canMoveDraggedLeafToWorkspace, clearWorkspaceDragHoverTimer, handleSelectWorkspace],
  );

  const handleWorkspaceDragLeave = useCallback(
    (workspace: { id: string; endpointId?: string }) => {
      if (dragHoverWorkspaceId !== workspace.id) {
        return;
      }
      clearWorkspaceDragHoverTimer();
      setDragHoverWorkspaceId(null);
    },
    [clearWorkspaceDragHoverTimer, dragHoverWorkspaceId],
  );

  const handleWorkspaceDragDrop = useCallback(
    (workspace: { id: string; endpointId?: string }) => {
      const drag = leafWorkspaceDragRef.current;
      if (!drag || !canMoveDraggedLeafToWorkspace(workspace)) {
        return;
      }
      clearWorkspaceDragHoverTimer();
      setDragHoverWorkspaceId(null);
      handleSelectWorkspace(workspace.id);
      void sendWorkspaceMoveLeafToWorkspace(
        drag.sourceWorkspaceId,
        workspace.id,
        drag.leafId,
        SIDEBAR_LEAF_DROP_PLACEMENT,
      ).catch(() => {});
    },
    [
      canMoveDraggedLeafToWorkspace,
      clearWorkspaceDragHoverTimer,
      handleSelectWorkspace,
      sendWorkspaceMoveLeafToWorkspace,
    ],
  );

  const handleNewWorkspaceDrop = useCallback(() => {
    const drag = leafWorkspaceDragRef.current;
    if (!drag) {
      return;
    }
    clearWorkspaceDragHoverTimer();
    setDragHoverWorkspaceId(null);
    void sendWorkspaceMoveLeafToNewWorkspace(
      drag.sourceWorkspaceId,
      drag.leafId,
      SIDEBAR_LEAF_DROP_PLACEMENT,
    ).catch(() => {});
  }, [clearWorkspaceDragHoverTimer, sendWorkspaceMoveLeafToNewWorkspace]);

  const handleWorkspaceReorder = useCallback(
    (args: { workspaceId: string; prevWorkspaceId?: string; nextWorkspaceId?: string }) => {
      void sendSetWorkspaceRank(args.workspaceId, args.prevWorkspaceId, args.nextWorkspaceId).catch(
        () => {},
      );
    },
    [sendSetWorkspaceRank],
  );

  const handleSelectWorkspaceByIndex = useCallback(
    (index: number) => {
      const workspace = visualWorkspaces[index];
      if (workspace) {
        handleSelectWorkspace(workspace.id);
      }
    },
    [visualWorkspaces, handleSelectWorkspace],
  );

  const handlePrevWorkspace = useCallback(() => {
    if (!activeWorkspaceId || visualWorkspaces.length === 0) return;
    const currentIndex = visualIndexByWorkspaceId.get(activeWorkspaceId);
    if (currentIndex === undefined) return;
    const prevIndex = currentIndex > 0 ? currentIndex - 1 : visualWorkspaces.length - 1;
    handleSelectWorkspace(visualWorkspaces[prevIndex].id);
  }, [activeWorkspaceId, visualWorkspaces, visualIndexByWorkspaceId, handleSelectWorkspace]);

  const handleNextWorkspace = useCallback(() => {
    if (!activeWorkspaceId || visualWorkspaces.length === 0) return;
    const currentIndex = visualIndexByWorkspaceId.get(activeWorkspaceId);
    if (currentIndex === undefined) return;
    const nextIndex = currentIndex < visualWorkspaces.length - 1 ? currentIndex + 1 : 0;
    handleSelectWorkspace(visualWorkspaces[nextIndex].id);
  }, [activeWorkspaceId, visualWorkspaces, visualIndexByWorkspaceId, handleSelectWorkspace]);

  const handleNavigateOutOfSession = useCallback(
    (direction: 'left' | 'right' | 'up' | 'down') => {
      if (direction === 'left' || direction === 'up') {
        handlePrevWorkspace();
        return;
      }
      handleNextWorkspace();
    },
    [handleNextWorkspace, handlePrevWorkspace],
  );

  const handleCloseCurrentSessionShortcut = useCallback(() => {
    // The packaged app's native "Close Pane" item claims Cmd+W and dispatches session.close, so a focused docked tile must be closed here, not the session.
    const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const tileId =
      focused?.closest('[data-pane-kind="tile"]')?.getAttribute('data-pane-id') ??
      focused?.closest('.session-terminal-workspace')?.getAttribute('data-active-leaf-id') ??
      '';
    const isTile =
      !!tileId &&
      !!document.querySelector(`[data-pane-kind="tile"][data-pane-id="${CSS.escape(tileId)}"]`);
    if (isTile && activeWorkspaceId) {
      handleCloseTile(activeWorkspaceId, tileId);
      return;
    }

    if (!activeSessionId) {
      return;
    }

    const activeSession = sessions.find((session) => session.id === activeSessionId);
    if (!activeSession) {
      return;
    }

    const activePaneId = getActivePaneIdForSession(activeSession);
    if (activePaneId) {
      void handleClosePane(activeSessionId, activePaneId);
      return;
    }

    handleRequestCloseSession(activeSessionId);
  }, [
    activeSessionId,
    activeWorkspaceId,
    getActivePaneIdForSession,
    handleCloseTile,
    handleClosePane,
    handleRequestCloseSession,
    sessions,
  ]);

  const handleReloadSession = useCallback(
    (id: string) => {
      const session = sessions.find((entry) => entry.id === id);
      const paneId = session?.workspace.agents.find((pane) => pane.sessionId === id)?.id;
      const size = paneId ? getPaneSize(id, paneId) || undefined : undefined;
      void reloadSession(id, size).catch((error) => {
        const message = error instanceof Error ? error.message : String(error);
        showError(`Failed to reload session: ${message}`);
      });
    },
    [getPaneSize, reloadSession, sessions, showError],
  );

  const [reopenedSessionId, setReopenedSessionId] = useState<string | null>(null);
  const handleReopenSession = useCallback(
    async (sessionId: string, actionId: string): Promise<boolean> => {
      let directory: string | undefined;
      if (actionId === 'start_fresh_elsewhere') {
        const chosen = await new Promise<string | undefined>((settle) => {
          reopenPickRef.current?.settle(undefined);
          reopenPickRef.current = { settle };
          setLocationPickerPurpose('reopen');
          setLocationPickerOpen(true);
        });
        if (!chosen) return false;
        directory = chosen;
      }
      const result = await sendSessionReopen(sessionId, actionId, directory);
      setReopenedSessionId(result.session_id);
      setSessionsOpen(false);
      return true;
    },
    [sendSessionReopen],
  );

  useEffect(() => {
    if (!reopenedSessionId) return;
    if (!sessions.some((entry) => entry.id === reopenedSessionId)) return;
    handleSelectSession(reopenedSessionId);
    setReopenedSessionId(null);
  }, [reopenedSessionId, sessions, handleSelectSession]);

  const {
    handleWakeCrewMember,
    handleSleepCrewMember,
    handleOpenSeedTile,
    handleRevealSeedInGarden,
    handleOpenMarkdownArtifact,
    checkArtifactPath,
    handleResumeSeed,
    handleHandoverSeed,
    handleSendSeedToChief,
  } = useAppGardenActions({
    sendOpenSeed,
    activeSessionId,
    focusWorkspaceLeaf,
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
    }, [settingsOpen]),
    onShowShortcuts: useCallback(() => setShortcutsOpen((prev) => !prev), []),
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

  const { notebookBrowserListFiles, notebookRootChangeSignal, notebookSurfaceContextValue } =
    useAppNotebookSurface({
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
    sidebarWorkspaceViews,
    visualWorkspaces,
    visualIndexByWorkspaceId,
    activeSessionId,
    activeWorkspaceId,
    selectedTile,
    tileContents,
    sidebarCollapsed,
    criticalNotifications,
    openNotificationsPanel,
    gridLayout,
    handleSelectGridLayout,
    keybindings,
    mutedWorkspaceViews,
    sidebarMutedExpanded,
    setSidebarMutedExpanded,
    sendMuteWorkspace,
    sendPinWorkspace,
    sendPinSession,
    sendRenameSession,
    sendRenameWorkspace,
    handleChangeChiefOfStaff,
    showSessionlessWorkspaces,
    handleToggleShowSessionlessWorkspaces,
    crew,
    handleWakeCrewMember,
    handleSleepCrewMember,
    queueModeEnabled,
    handleToggleQueueMode,
    crewQueueEnabled,
    handleToggleCrewQueue,
    settings,
    handleToggleSidebarHarnessLogos,
    workspaceSelectionStyle,
    handleWorkspaceSelectionStyleChange,
    leafWorkspaceDrag,
    dragHoverWorkspaceId,
    handleWorkspaceDragEnter,
    handleWorkspaceDragLeave,
    handleWorkspaceDragDrop,
    handleNewWorkspaceDrop,
    handleLeafDragStart,
    handleLeafDragEnd,
    handleWorkspaceReorder,
    queueBands,
    sendSettleTurn,
    openSnoozeMenu,
    sendWakeTurn,
    onScreenSessionIds,
    handleSelectSession,
    sendTriggerNudge,
    handleSelectWorkspace,
    handleSelectTile,
    handleCloseTile,
    handleReloadTile,
    handleNewSession,
    handleRequestCloseSession,
    handleReloadSession,
    goToDashboard,
    view,
    toggleSidebarCollapse,
    requestTerminalFocus,
    unmutedEnrichedSessions,
    prs,
    hasReceivedInitialState,
    isRefreshingPRs,
    refreshError,
    rateLimit,
    daemonEndpoints,
    handleRebootstrapEndpoint,
    followNextTurn,
    setFollowNextTurn,
    handleRefreshPRs,
    handleOpenPR,
    setSettingsOpen,
    setSidebarCollapsed,
    setView,
    workspaceViews,
    sessionlessWorkspaceStateById,
    workspaceSelection,
    getActivePaneIdForSession,
    warmWorkspaceIds,
    setWorkspaceRef,
    presentationBySessionId,
    delegationSessions,
    daemonSessions,
    seeds,
    handleOpenSeedTile,
    handleRevealSeedInGarden,
    seedPopoverRequest,
    usagePopoverRequest,
    annotationApi,
    sendCancelCountdown,
    sendTerminalPointerActivity,
    handleOpenPresentationWindow,
    sendOpenMarkdown,
    focusWorkspaceLeaf,
    handleTerminalModelRecovered,
    terminalFontSize,
    resolvedTheme,
    utilityFocusRequestToken,
    blockingOverlayOpen,
    paneRuntimeEventRouter,
    createSplitSession,
    handleClosePane,
    sendWorkspaceSetSplitRatio,
    selectAgentPane,
    zoomModeBySessionId,
    setZoomModeBySessionId,
    handleNavigateOutOfSession,
    sendWorkspaceUpdateTile,
    activeWorkspaceIdRef,
    sendWorkspaceMoveLeafToWorkspace,
    sendWorkspaceMoveLeaf,
    getActiveLeafDropSnapshot,
    handleLeafDragGhostMove,
    handleLeafDragPreview,
    leafDragPreview,
    requestTileContent,
    sessions,
    dockPanelStack,
    workflowRunPanelOpen,
    activeWorkflowRun,
    closeDockPanel,
    attentionPanelOpen,
    waitingLocalSessions,
    automationsPanelOpen,
    listAutomationDefinitions,
    listAutomationRuns,
    setAutomationEnabled,
    runAutomationNow,
    getAutomationDefinition,
    applyAutomationDefinition,
    deleteAutomationDefinition,
    gardenPanelOpen,
    gardenHoldsWindow,
    gardenSlotRef,
    visibleGridTiles,
    resolvedGridLayout,
    gridOffBoardCount,
    hiddenGridSessions,
    handleRemoveFromGrid,
    handleRestoreToGrid,
    getScreenSnapshot,
    locationPickerOpen,
    locationPickerPurpose,
    closeLocationPicker,
    handleLocationSelect,
    sendGetRecentLocations,
    sendBrowseDirectory,
    sendInspectPath,
    getRepoInfo,
    sendCreateWorktree,
    handleCreateWorktreeSession,
    sendDeleteWorktree,
    showError,
    agentAvailability,
    hasChiefOfStaff,
    sessionCreationJob,
    setSessionCreationJob,
    pendingSessionClose,
    handleConfirmSessionClose,
    handleCancelSessionClose,
    chiefTransferTarget,
    chiefTransferSaving,
    handleConfirmChiefTransfer,
    setChiefTransferTarget,
    appViewParamsPrompt,
    dockAppViewTile,
    setAppViewParamsPrompt,
    contextCapPromptSession,
    sendSetSessionContextWindowCap,
    setContextCapPromptSession,
    sessionsOpen,
    ledgerTab,
    setLedgerTab,
    setSessionsOpen,
    sendSessionList,
    workspaceNamesById,
    liveGardenSessions,
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
    seedsTotal,
    gardenSessionLabels,
    sendSeedTransition,
    sendSeedNote,
    sendSeedDocumentGet,
    handleOpenMarkdownArtifact,
    checkArtifactPath,
    handleResumeSeed,
    handleHandoverSeed,
    handleSendSeedToChief,
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
    markdownOpenerOpen,
    markdownOpenerTarget,
    loadOpenerRecents,
    loadOpenerIndex,
    setMarkdownOpenerOpen,
    snoozeMenu,
    sendSnoozeTurn,
    setSnoozeMenu,
    actionMenuOpen,
    setActionMenuOpen,
    shortcutsOpen,
    setShortcutsOpen,
    setShortcutEditorOpen,
    shortcutEditorOpen,
    whatsNew,
    settingsModalRef,
    settingsOpen,
    mutedRepos,
    daemonGitHubHosts,
    sendMuteRepo,
    mutedAuthors,
    sendMuteAuthor,
    daemonPlugins,
    daemonPluginIssues,
    sendAddEndpoint,
    sendUpdateEndpoint,
    sendRemoveEndpoint,
    sendSetEndpointRemoteWeb,
    sendListPlugins,
    sendInstallPlugin,
    sendInstallBundledPlugin,
    sendUninstallPlugin,
    sendRemovePlugin,
    sendSetPluginPriority,
    sendSaveSetting,
    themePreference,
    setTheme,
    scale,
    increaseScale,
    decreaseScale,
    resetScale,
    gardenScale,
    sendTaskList,
    notebookTaskChangeSignal,
    sendPRAction,
    sendMutePR,
    sendPRVisited,
    githubPollingOffReason,
    notebookSurfaceContextValue,
    delegationChainRef,
    appShellRef,
    connectionError,
    warnings,
    updateAvailableVersion,
    clearWarnings,
    onOpenLatestRelease,
    onDismissLatestRelease,
    openPRLauncherJob,
    errorMessage,
    errorDurationMs,
    clearError,
    diagnosticReportSaved,
    diagnosticCapture,
    handleSaveDiagnosticReport,
    setDiagnosticCapture,
    apps,
    handleOpenNotebookTile,
    openLedger,
    openDockPanel,
    sendSetSetting,
    handleCreateDiagnosticReport,
    activeWorkspaceForCommands,
    activeSessionForCommands,
    actionMenuReturnFocusRef,
    activeSessionQueueEligible,
    setSeedPopoverRequest,
    setUsagePopoverRequest,
    handleSnoozeActiveSession,
    seedForSession,
    listWorktrees,
    refreshWorktrees,
    gitOperations,
    activeEndpoint,
    activeRemoteSession,
    toggleDockPanel,
    attentionCount,
    openNotebookBrowser,
    notificationsUnread,
    hasCriticalNotification,
    toggleNotificationsPanel,
    toggleGardenFromIcon,
  };
}
