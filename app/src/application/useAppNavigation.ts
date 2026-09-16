import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { controlBrowserHost } from '../browser/host';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAgentNavigation } from '../hooks/useAgentNavigation';
import { useAppView } from '../hooks/useAppView';
import { useSessionWorkspaceController } from '../hooks/useSessionWorkspaceController';
import { useWorkspaceSelectionController } from '../hooks/useWorkspaceSelectionController';
import { useSessionStore, type TerminalWorkspaceState } from '../store/sessions';
import { workspaceSnapshotFromDaemonWorkspace } from '../types/workspace';
import { dispatcherOf } from '../utils/delegationLinks';
import { advanceAfterTurnClosed, headOfQueue, oldestWantedTurn } from '../utils/queueBands';
import { probeUiAfterSwitch } from '../utils/uiDiagnosticsLog';
import {
  persistWorkspaceSelectionStyle,
  readWorkspaceSelectionStyle,
  type WorkspaceSelectionStyle,
} from '../utils/workspaceSelectionStyle';
import {
  AppContentProps,
  persistShowSessionlessWorkspaces,
  readShowSessionlessWorkspaces,
} from './appSupport';
import { useAppSessions } from './useAppSessions';
import type { useAttentionQueue } from './useAttentionQueue';

interface Options {
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  activeSessionId: string | null;
  daemonSessions: AppContentProps['daemonSessions'];
  daemonWorkspaces: AppContentProps['daemonWorkspaces'];
  workspaceViews: ReturnType<typeof useAppSessions>['workspaceViews'];
  unmutedEnrichedSessions: ReturnType<typeof useAppSessions>['unmutedEnrichedSessions'];
  attentionQueue: ReturnType<typeof useAttentionQueue>;
  setActivePane: ReturnType<typeof useSessionWorkspaceController>['setActivePane'];
  focusSessionPane: ReturnType<typeof useSessionWorkspaceController>['focusSessionPane'];
}
export function useAppNavigation({
  sessions,
  activeSessionId,
  daemonSessions,
  daemonWorkspaces,
  workspaceViews,
  unmutedEnrichedSessions,
  attentionQueue,
  setActivePane,
  focusSessionPane,
}: Options) {
  const { setActiveSession, navigateAgentHistory } = useSessionStore();
  const {
    sendSessionSelected,
    sendWorkspaceSelected,
    sendWorkspaceUndockTile,
    sendSetWorkspaceRank,
  } = useDaemonApi();
  const activeWorkspaceIdRef = useRef<string | null>(null);

  const [selectedSessionlessWorkspaceId, setSelectedSessionlessWorkspaceId] = useState<
    string | null
  >(null);
  const [selectedTileRequest, setSelectedTile] = useState<{
    workspaceId: string;
    tileId: string;
  } | null>(null);
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

  const { queueBands, queueModeEnabled, wantsAttention } = attentionQueue;
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

  const toggleGridMode = useCallback(() => {
    cancelPendingSelection();
    setView((prev) => (prev === 'grid' ? (activeSessionId ? 'session' : 'dashboard') : 'grid'));
  }, [activeSessionId, cancelPendingSelection, setView]);

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
    if (view === 'session' && activeWorkspaceId) {
      sendWorkspaceSelected(activeWorkspaceId);
    }
  }, [activeWorkspaceId, sendWorkspaceSelected, view]);

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

  const handleSelectOrchestrator = useCallback(() => {
    const session = daemonSessions.find((entry) => entry.id === activeSessionId);
    if (!session) return;
    const dispatcher = dispatcherOf(session, daemonSessions);
    if (dispatcher?.session) handleSelectSession(dispatcher.session.id);
  }, [activeSessionId, daemonSessions, handleSelectSession]);

  return {
    handleJumpToWaiting,
    workspaceSelection,
    view,
    setView,
    followNextTurn,
    setFollowNextTurn,
    utilityFocusRequestToken,
    requestTerminalFocus,
    selectAgent,
    selectAgentPane,
    cancelPendingSelection,
    navigateAgentHistoryBack,
    navigateAgentHistoryForward,
    handleSelectSession,
    selectCreatedSession,
    goToDashboard,
    goHomeAwaitingNextTurn,
    toggleGridMode,
    workspaceViews,
    showSessionlessWorkspaces,
    handleToggleShowSessionlessWorkspaces,
    workspaceSelectionStyle,
    handleWorkspaceSelectionStyleChange,
    sidebarWorkspaceViews,
    activeWorkspaceId,
    activeWorkspaceIdRef,
    sessionlessWorkspaceStateById,
    visualWorkspaces,
    visualIndexByWorkspaceId,
    handleSelectWorkspace,
    handleSelectTile,
    handleCloseTile,
    handleReloadTile,
    selectedTile,
    handleWorkspaceReorder,
    handleSelectWorkspaceByIndex,
    handlePrevWorkspace,
    handleNextWorkspace,
    handleNavigateOutOfSession,
    handleSelectOrchestrator,
  };
}
