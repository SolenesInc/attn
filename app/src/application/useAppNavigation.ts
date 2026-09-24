import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { controlBrowserHost } from '../browser/host';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAgentNavigation } from '../hooks/useAgentNavigation';
import { withFreshDesktopRevisions } from '../hooks/desktopRevisions';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
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
  showError: (message: string) => void;
}
export function useAppNavigation({
  activeSessionId,
  daemonSessions,
  desktopViews,
  unmutedEnrichedSessions,
  attentionQueue,
  showError,
}: Options) {
  const {
    view,
    setView,
    followNextTurn,
    setFollowNextTurn,
    selectedTile,
    utilityFocusRequestToken,
    requestTerminalFocus,
    goToDashboard,
    goHomeAwaitingNextTurn,
  } = useSessionStore();
  const { sendDesktopSetCurrent, sendDesktopSetActivePane, sendDesktopRemoveLeaf } = useDaemonApi();
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
      const { selectedProfileId, desktops } = useProfilesStore.getState();
      if (!selectedProfileId || !desktops.some((desktop) => desktop.id === desktopId)) return;
      setView('session');
      if (desktopId === currentDesktopIdRef.current) return;
      void sendDesktopSetCurrent(selectedProfileId, desktopId).catch(() => {});
    },
    [sendDesktopSetCurrent, setView],
  );

  const [crewSeedTile, setCrewSeedTile] = useState<{ desktopId: string; tileId: string } | null>(
    null,
  );

  const handleSelectTile = useCallback(
    (desktopId: string, tileId: string) => {
      const { selectedProfileId, desktops } = useProfilesStore.getState();
      if (!selectedProfileId || !desktops.some((desktop) => desktop.id === desktopId)) return;
      setView('session');
      void sendDesktopSetActivePane(desktopId, tileId)
        .then(() => {
          if (desktopId !== currentDesktopIdRef.current) return sendDesktopSetCurrent(selectedProfileId, desktopId);
        })
        .catch((error) => {
          showError(`Could not focus that tile: ${error instanceof Error ? error.message : String(error)}`);
        });
    },
    [sendDesktopSetActivePane, sendDesktopSetCurrent, setView, showError],
  );

  const handleCloseTile = useCallback(
    (desktopId: string, tileId: string) => {
      setCrewSeedTile((current) =>
        current?.desktopId === desktopId && current.tileId === tileId ? null : current,
      );
      void withFreshDesktopRevisions([desktopId], (revisionOf) =>
        sendDesktopRemoveLeaf(desktopId, tileId, revisionOf(desktopId)),
      ).catch((error) => {
        showError(`Could not close that tile: ${error instanceof Error ? error.message : String(error)}`);
      });
    },
    [sendDesktopRemoveLeaf, showError],
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
