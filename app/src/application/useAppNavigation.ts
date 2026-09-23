import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { controlBrowserHost } from '../browser/host';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAgentNavigation } from '../hooks/useAgentNavigation';
import type { useDesktopRuntimeController } from '../hooks/useDesktopRuntimeController';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { hasLeaf } from '../types/workspace';
import { dispatcherOf } from '../utils/delegationLinks';
import { orderedDesktops } from '../utils/desktops';
import { oldestWantedTurn } from '../utils/queueBands';
import { probeUiAfterSwitch } from '../utils/uiDiagnosticsLog';
import {
  persistWorkspaceSelectionStyle,
  readWorkspaceSelectionStyle,
  type WorkspaceSelectionStyle,
} from '../utils/workspaceSelectionStyle';
import { AppContentProps } from './appSupport';
import { useAppSessions } from './useAppSessions';
import type { useAttentionQueue } from './useAttentionQueue';

interface Options {
  activeSessionId: string | null;
  daemonSessions: AppContentProps['daemonSessions'];
  desktopViews: ReturnType<typeof useAppSessions>['desktopViews'];
  unmutedEnrichedSessions: ReturnType<typeof useAppSessions>['unmutedEnrichedSessions'];
  attentionQueue: ReturnType<typeof useAttentionQueue>;
  focusDesktopLeaf: ReturnType<typeof useDesktopRuntimeController>['focusDesktopLeaf'];
}
export function useAppNavigation({
  activeSessionId,
  daemonSessions,
  desktopViews,
  unmutedEnrichedSessions,
  attentionQueue,
  focusDesktopLeaf,
}: Options) {
  const {
    view,
    setView,
    followNextTurn,
    setFollowNextTurn,
    selectedTile,
    setSelectedTile,
    utilityFocusRequestToken,
    requestTerminalFocus,
    goToDashboard,
    goHomeAwaitingNextTurn,
  } = useSessionStore();
  const { sendDesktopSetCurrent, sendDesktopRemoveLeaf } = useDaemonApi();
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const desktops = useProfilesStore((state) => state.desktops);
  const currentDesktopIdRef = useRef<string | null>(currentDesktopId);
  useEffect(() => {
    currentDesktopIdRef.current = currentDesktopId;
  }, [currentDesktopId]);

  const {
    selectAgent,
    selectAgentPane,
    cancelPendingSelection,
    back: navigateAgentHistoryBack,
    forward: navigateAgentHistoryForward,
  } = useAgentNavigation();

  const handleSelectSession = selectAgent;
  const selectCreatedSession = selectAgent;

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

  const [workspaceSelectionStyle, setWorkspaceSelectionStyle] = useState<WorkspaceSelectionStyle>(
    readWorkspaceSelectionStyle,
  );
  const handleWorkspaceSelectionStyleChange = useCallback((style: WorkspaceSelectionStyle) => {
    persistWorkspaceSelectionStyle(style);
    setWorkspaceSelectionStyle(style);
  }, []);

  useEffect(() => {
    probeUiAfterSwitch({
      sessionId: activeSessionId,
      workspaceId: currentDesktopId,
      view,
    });
  }, [activeSessionId, currentDesktopId, view]);

  const handleSelectDesktop = useCallback(
    (desktopId: string) => {
      setView('session');
      if (!selectedProfileId || desktopId === currentDesktopIdRef.current) return;
      void sendDesktopSetCurrent(selectedProfileId, desktopId).catch(() => {});
    },
    [selectedProfileId, sendDesktopSetCurrent, setView],
  );

  const selectTile = useCallback(
    (desktopId: string, tileId: string) => {
      handleSelectDesktop(desktopId);
      setSelectedTile({ desktopId, tileId });
      window.requestAnimationFrame(() => focusDesktopLeaf(desktopId, tileId));
    },
    [focusDesktopLeaf, handleSelectDesktop, setSelectedTile],
  );

  const selectTileRef = useRef(selectTile);
  useLayoutEffect(() => {
    selectTileRef.current = selectTile;
  }, [selectTile]);
  const pendingTileSelectionRef = useRef<{ key: string; unsubscribe: () => void } | null>(null);
  const [crewSeedTile, setCrewSeedTile] = useState<{ desktopId: string; tileId: string } | null>(
    null,
  );

  useEffect(
    () => () => {
      pendingTileSelectionRef.current?.unsubscribe();
      pendingTileSelectionRef.current = null;
    },
    [],
  );

  const handleSelectTile = useCallback((desktopId: string, tileId: string) => {
    const key = `${desktopId}:${tileId}`;
    pendingTileSelectionRef.current?.unsubscribe();
    pendingTileSelectionRef.current = null;
    const tileExists = () => {
      const layout = useSessionStore.getState().desktopSnapshots[desktopId]?.workspace.layoutTree;
      return layout ? hasLeaf(layout, tileId) : false;
    };
    if (tileExists()) {
      selectTileRef.current(desktopId, tileId);
      return;
    }
    const unsubscribe = useSessionStore.subscribe(() => {
      if (!tileExists()) return;
      unsubscribe();
      if (pendingTileSelectionRef.current?.key === key) pendingTileSelectionRef.current = null;
      window.requestAnimationFrame(() => selectTileRef.current(desktopId, tileId));
    });
    pendingTileSelectionRef.current = { key, unsubscribe };
  }, []);

  const handleCloseTile = useCallback(
    (desktopId: string, tileId: string) => {
      const clearIfClosed = <T extends { desktopId: string; tileId: string } | null>(current: T) =>
        current?.desktopId === desktopId && current.tileId === tileId ? null : current;
      const pendingKey = `${desktopId}:${tileId}`;
      if (pendingTileSelectionRef.current?.key === pendingKey) {
        pendingTileSelectionRef.current.unsubscribe();
        pendingTileSelectionRef.current = null;
      }
      setCrewSeedTile(clearIfClosed);
      setSelectedTile(clearIfClosed);
      const desktop = useProfilesStore.getState().desktops.find((entry) => entry.id === desktopId);
      if (!desktop) return;
      void sendDesktopRemoveLeaf(desktopId, tileId, desktop.revision).catch(() => {});
    },
    [sendDesktopRemoveLeaf, setSelectedTile],
  );

  const handleReloadTile = useCallback((desktopId: string, tileId: string) => {
    void controlBrowserHost(desktopId, tileId, 'reload').catch((error) => {
      console.warn('[App] Failed to reload browser tile:', error);
    });
  }, []);

  const desktopOrder = useMemo(() => orderedDesktops(desktops), [desktops]);
  const handleStepDesktop = useCallback(
    (step: 1 | -1) => {
      if (!currentDesktopId || desktopOrder.length === 0) return;
      const index = desktopOrder.findIndex((desktop) => desktop.id === currentDesktopId);
      if (index < 0) return;
      const next = desktopOrder[(index + step + desktopOrder.length) % desktopOrder.length];
      handleSelectDesktop(next.id);
    },
    [currentDesktopId, desktopOrder, handleSelectDesktop],
  );

  const handleNavigateOutOfSession = useCallback(
    (direction: 'left' | 'right' | 'up' | 'down') => {
      handleStepDesktop(direction === 'left' || direction === 'up' ? -1 : 1);
    },
    [handleStepDesktop],
  );

  const handleSelectOrchestrator = useCallback(() => {
    const session = daemonSessions.find((entry) => entry.id === activeSessionId);
    if (!session) return;
    const dispatcher = dispatcherOf(session, daemonSessions);
    if (dispatcher?.session) handleSelectSession(dispatcher.session.id);
  }, [activeSessionId, daemonSessions, handleSelectSession]);

  return {
    handleJumpToWaiting,
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
    desktopViews,
    workspaceSelectionStyle,
    handleWorkspaceSelectionStyleChange,
    currentDesktopId,
    currentDesktopIdRef,
    handleSelectDesktop,
    handleSelectTile,
    handleCloseTile,
    handleReloadTile,
    selectedTile,
    crewSeedTile,
    setCrewSeedTile,
    handleNavigateOutOfSession,
    handleSelectOrchestrator,
  };
}
