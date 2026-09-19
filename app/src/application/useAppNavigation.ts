import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { controlBrowserHost } from '../browser/host';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAgentNavigation } from '../hooks/useAgentNavigation';
import { useWorkspaceSelectionController } from '../hooks/useWorkspaceSelectionController';
import type { useSessionWorkspaceController } from '../hooks/useSessionWorkspaceController';
import { useSessionStore, type TerminalWorkspaceState } from '../store/sessions';
import { workspaceSnapshotFromDaemonWorkspace } from '../types/workspace';
import { dispatcherOf } from '../utils/delegationLinks';
import { oldestWantedTurn } from '../utils/queueBands';
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
  activeSessionId: string | null;
  daemonSessions: AppContentProps['daemonSessions'];
  daemonWorkspaces: AppContentProps['daemonWorkspaces'];
  workspaceViews: ReturnType<typeof useAppSessions>['workspaceViews'];
  unmutedEnrichedSessions: ReturnType<typeof useAppSessions>['unmutedEnrichedSessions'];
  attentionQueue: ReturnType<typeof useAttentionQueue>;
  focusWorkspaceLeaf: ReturnType<typeof useSessionWorkspaceController>['focusWorkspaceLeaf'];
}
export function useAppNavigation({
  activeSessionId,
  daemonSessions,
  daemonWorkspaces,
  workspaceViews,
  unmutedEnrichedSessions,
  attentionQueue,
  focusWorkspaceLeaf,
}: Options) {
  const {
    view,
    setView,
    followNextTurn,
    setFollowNextTurn,
    selectedSessionlessWorkspaceId,
    selectSessionlessWorkspace,
    selectedTile,
    setSelectedTile,
    utilityFocusRequestToken,
    requestTerminalFocus,
    goToDashboard,
    goHomeAwaitingNextTurn,
  } = useSessionStore();
  const {
    sendSessionSelected,
    sendWorkspaceSelected,
    sendWorkspaceUndockTile,
    sendSetWorkspaceRank,
  } = useDaemonApi();
  const activeWorkspaceIdRef = useRef<string | null>(null);

  const {
    selectAgent,
    selectAgentPane,
    cancelPendingSelection,
    back: navigateAgentHistoryBack,
    forward: navigateAgentHistoryForward,
  } = useAgentNavigation();

  const handleSelectSession = selectAgent;
  const selectCreatedSession = selectAgent;

  useEffect(() => {
    if (view === 'session' && activeSessionId) {
      sendSessionSelected(activeSessionId);
    }
  }, [activeSessionId, sendSessionSelected, view]);

  const { wantsAttention } = attentionQueue;

  const handleJumpToWaiting = useCallback(() => {
    const waiting = oldestWantedTurn(unmutedEnrichedSessions, wantsAttention);
    if (waiting) {
      handleSelectSession(waiting.id);
    }
  }, [unmutedEnrichedSessions, handleSelectSession, wantsAttention]);

  const toggleGridMode = useCallback(() => {
    setView((prev) => (prev === 'grid' ? (activeSessionId ? 'session' : 'dashboard') : 'grid'));
  }, [activeSessionId, setView]);

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
          (workspace.pinned ||
            workspace.sessions.length > 0 ||
            workspace.hasUnresolvedAgentPanes ||
            showSessionlessWorkspaces),
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

  const daemonWorkspaceStateById = useMemo(() => {
    const map = new Map<string, TerminalWorkspaceState>();
    const unresolvedWorkspaceIds = new Set(
      workspaceViews
        .filter((workspace) => workspace.hasUnresolvedAgentPanes)
        .map((workspace) => workspace.id),
    );
    for (const workspace of daemonWorkspaces) {
      if (!workspace.layout) {
        continue;
      }
      const { workspace: state } = workspaceSnapshotFromDaemonWorkspace(workspace.layout);
      if (
        state.layoutTree &&
        (state.agents.length === 0 || unresolvedWorkspaceIds.has(workspace.id))
      ) {
        map.set(workspace.id, state);
      }
    }
    return map;
  }, [daemonWorkspaces, workspaceViews]);

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
      selectSessionlessWorkspace(workspace.id);
    },
    [handleSelectSession, selectSessionlessWorkspace, sidebarWorkspaceViews, workspaceViews],
  );

  const selectTile = useCallback(
    (workspaceId: string, tileId: string) => {
      handleSelectWorkspace(workspaceId);
      setSelectedTile({ workspaceId, tileId });
      window.requestAnimationFrame(() => focusWorkspaceLeaf(workspaceId, tileId));
    },
    [focusWorkspaceLeaf, handleSelectWorkspace, setSelectedTile],
  );

  // A tile the daemon just opened may not be in workspace state yet; the
  // selection waits for it so the leaf exists when it receives focus.
  const [pendingTileSelection, setPendingTileSelection] = useState<{
    workspaceId: string;
    tileId: string;
  } | null>(null);
  const [crewSeedTile, setCrewSeedTile] = useState<{ workspaceId: string; tileId: string } | null>(
    null,
  );
  const tileExists = useCallback(
    (workspaceId: string, tileId: string) =>
      workspaceViews.some(
        (workspace) =>
          workspace.id === workspaceId &&
          workspace.children.some((child) => child.kind === 'tile' && child.tile.tileId === tileId),
      ),
    [workspaceViews],
  );

  const handleSelectTile = useCallback(
    (workspaceId: string, tileId: string) => {
      if (!tileExists(workspaceId, tileId)) {
        setPendingTileSelection({ workspaceId, tileId });
        return;
      }
      setPendingTileSelection(null);
      selectTile(workspaceId, tileId);
    },
    [selectTile, tileExists],
  );

  useEffect(() => {
    if (!pendingTileSelection) return;
    if (!tileExists(pendingTileSelection.workspaceId, pendingTileSelection.tileId)) return;
    setPendingTileSelection(null);
    selectTile(pendingTileSelection.workspaceId, pendingTileSelection.tileId);
  }, [pendingTileSelection, selectTile, tileExists]);

  const handleCloseTile = useCallback(
    (workspaceId: string, tileId: string) => {
      const clearIfClosed = <T extends { workspaceId: string; tileId: string } | null>(current: T) =>
        current?.workspaceId === workspaceId && current.tileId === tileId ? null : current;
      setPendingTileSelection(clearIfClosed);
      setCrewSeedTile(clearIfClosed);
      setSelectedTile(clearIfClosed);
      void sendWorkspaceUndockTile(workspaceId, tileId).catch(() => {});
    },
    [sendWorkspaceUndockTile, setSelectedTile],
  );

  const handleReloadTile = useCallback((workspaceId: string, tileId: string) => {
    void controlBrowserHost(workspaceId, tileId, 'reload').catch((error) => {
      console.warn('[App] Failed to reload browser tile:', error);
    });
  }, []);

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
    daemonWorkspaceStateById,
    visualWorkspaces,
    visualIndexByWorkspaceId,
    handleSelectWorkspace,
    handleSelectTile,
    handleCloseTile,
    handleReloadTile,
    selectedTile,
    crewSeedTile,
    setCrewSeedTile,
    handleWorkspaceReorder,
    handleSelectWorkspaceByIndex,
    handlePrevWorkspace,
    handleNextWorkspace,
    handleNavigateOutOfSession,
    handleSelectOrchestrator,
  };
}
