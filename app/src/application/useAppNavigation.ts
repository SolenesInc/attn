import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { controlBrowserHost } from '../browser/host';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAgentNavigation } from '../hooks/useAgentNavigation';
import { useAgentOnScreen } from '../hooks/useDesktopSelectionBridge';
import { useProfilesStore, useSelectedTile } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { dispatcherOf } from '../utils/delegationLinks';
import { orderedDesktops } from '../utils/desktops';
import { automationRunGroups, nextRunNeedingYou } from '../utils/automationRuns';
import { oldestWantedTurn, stepQueue } from '../utils/queueBands';
import { probeUiAfterSwitch } from '../utils/uiDiagnosticsLog';
import {
  persistDesktopSelectionStyle,
  readDesktopSelectionStyle,
  type DesktopSelectionStyle,
} from '../utils/desktopSelectionStyle';
import { AppContentProps } from './appSupport';
import { useAppSessions } from './useAppSessions';
import type { useAttentionQueue } from './useAttentionQueue';

interface Options {
  shownAgentId: string | null;
  daemonSessions: AppContentProps['daemonSessions'];
  desktopViews: ReturnType<typeof useAppSessions>['desktopViews'];
  profileSessions: ReturnType<typeof useAppSessions>['profileSessions'];
  attentionQueue: ReturnType<typeof useAttentionQueue>;
  showError: (message: string) => void;
}
export function useAppNavigation({
  shownAgentId,
  daemonSessions,
  desktopViews,
  profileSessions,
  attentionQueue,
  showError,
}: Options) {
  const {
    view,
    setView,
    followNextTurn,
    setFollowNextTurn,
    utilityFocusRequestToken,
    requestTerminalFocus,
    goToDashboard,
    goHomeAwaitingNextTurn,
  } = useSessionStore();
  const { sendDesktopSetCurrent, sendDesktopRemoveLeaf } = useDaemonApi();
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const desktops = useProfilesStore((state) => state.desktops);
  const shownTile = useSelectedTile();
  const selectedTile = view === 'dashboard' ? null : shownTile;
  const currentDesktopIdRef = useRef<string | null>(currentDesktopId);
  useEffect(() => {
    currentDesktopIdRef.current = currentDesktopId;
  }, [currentDesktopId]);

  const {
    selectAgent,
    selectLeaf,
    back: navigateLeafHistoryBack,
    forward: navigateLeafHistoryForward,
  } = useAgentNavigation();

  const handleSelectSession = selectAgent;
  const selectCreatedSession = useCallback((id: string, owner?: Element | null) =>
    selectAgent(id, owner, false), [selectAgent]);

  const { wantsAttention, queueBands } = attentionQueue;

  const handleJumpToWaiting = useCallback(() => {
    const waiting = oldestWantedTurn(profileSessions, wantsAttention);
    if (waiting) {
      handleSelectSession(waiting.id);
    }
  }, [profileSessions, handleSelectSession, wantsAttention]);

  const agentOnScreenId = useAgentOnScreen();
  const handleNextRun = useCallback(() => {
    const groups = automationRunGroups(desktopViews, Date.now());
    const step = nextRunNeedingYou(groups, agentOnScreenId);
    if (step) handleSelectSession(step.run.id);
  }, [desktopViews, agentOnScreenId, handleSelectSession]);

  const [desktopSelectionStyle, setDesktopSelectionStyle] = useState<DesktopSelectionStyle>(
    readDesktopSelectionStyle,
  );
  const handleDesktopSelectionStyleChange = useCallback((style: DesktopSelectionStyle) => {
    persistDesktopSelectionStyle(style);
    setDesktopSelectionStyle(style);
  }, []);

  useEffect(() => {
    probeUiAfterSwitch({
      sessionId: shownAgentId,
      desktopId: currentDesktopId,
      view,
    });
  }, [shownAgentId, currentDesktopId, view]);

  const handleSelectDesktop = useCallback(
    (desktopId: string) => {
      const { selectedProfileId, desktops } = useProfilesStore.getState();
      if (!selectedProfileId || !desktops.some((desktop) => desktop.id === desktopId)) return;
      setView('session');
      void sendDesktopSetCurrent(selectedProfileId, desktopId).catch(() => {});
    },
    [sendDesktopSetCurrent, setView],
  );

  const [crewSeedTile, setCrewSeedTile] = useState<{ desktopId: string; tileId: string } | null>(
    null,
  );

  const handleSelectTile = useCallback(
    (desktopId: string, tileId: string) => {
      const { desktops } = useProfilesStore.getState();
      if (!desktops.some((desktop) => desktop.id === desktopId)) return;
      selectLeaf(desktopId, tileId);
    },
    [selectLeaf],
  );

  const handleCloseTile = useCallback(
    (desktopId: string, tileId: string) => {
      setCrewSeedTile((current) =>
        current?.desktopId === desktopId && current.tileId === tileId ? null : current,
      );
      void sendDesktopRemoveLeaf(desktopId, tileId).catch((error) => {
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
  const steppedToDesktopId = useRef<string | null>(null);
  useEffect(() => {
    steppedToDesktopId.current = null;
  }, [currentDesktopId]);

  const handleStepDesktop = useCallback(
    (step: 1 | -1) => {
      const from = steppedToDesktopId.current ?? currentDesktopId;
      if (!from || desktopOrder.length === 0) return;
      const index = desktopOrder.findIndex((desktop) => desktop.id === from);
      if (index < 0) return;
      const next = desktopOrder[(index + step + desktopOrder.length) % desktopOrder.length];
      steppedToDesktopId.current = next.id;
      handleSelectDesktop(next.id);
    },
    [currentDesktopId, desktopOrder, handleSelectDesktop],
  );

  const steppedToAgentId = useRef<string | null>(null);
  useEffect(() => {
    steppedToAgentId.current = null;
  }, [agentOnScreenId]);

  const handleStepQueue = useCallback(
    (step: 1 | -1) => {
      if (!queueBands) return;
      const from = steppedToAgentId.current ?? (view === 'dashboard' ? null : agentOnScreenId);
      const next = stepQueue(queueBands, from, step);
      if (!next) return;
      steppedToAgentId.current = next.id;
      handleSelectSession(next.id);
    },
    [queueBands, view, agentOnScreenId, handleSelectSession],
  );

  const handleStepSession = useCallback(
    (step: 1 | -1) => (queueBands ? handleStepQueue(step) : handleStepDesktop(step)),
    [queueBands, handleStepQueue, handleStepDesktop],
  );

  // The queue is a vertical list, so only up/down leave a pane for it.
  const handleNavigateOutOfSession = useCallback(
    (direction: 'left' | 'right' | 'up' | 'down') => {
      const step = direction === 'left' || direction === 'up' ? -1 : 1;
      if (direction === 'up' || direction === 'down') handleStepSession(step);
      else handleStepDesktop(step);
    },
    [handleStepSession, handleStepDesktop],
  );

  const handleSelectOrchestrator = useCallback(() => {
    const session = daemonSessions.find((entry) => entry.id === shownAgentId);
    if (!session) return;
    const dispatcher = dispatcherOf(session, daemonSessions);
    if (dispatcher) handleSelectSession(dispatcher.id);
  }, [shownAgentId, daemonSessions, handleSelectSession]);

  return {
    handleJumpToWaiting,
    handleNextRun,
    view,
    setView,
    followNextTurn,
    setFollowNextTurn,
    utilityFocusRequestToken,
    requestTerminalFocus,
    selectAgent,
    selectLeaf,
    navigateLeafHistoryBack,
    navigateLeafHistoryForward,
    handleSelectSession,
    selectCreatedSession,
    goToDashboard,
    goHomeAwaitingNextTurn,
    desktopViews,
    desktopSelectionStyle,
    handleDesktopSelectionStyleChange,
    currentDesktopId,
    currentDesktopIdRef,
    handleSelectDesktop,
    handleSelectTile,
    handleCloseTile,
    handleReloadTile,
    selectedTile,
    crewSeedTile,
    setCrewSeedTile,
    handleStepSession,
    handleNavigateOutOfSession,
    handleSelectOrchestrator,
  };
}
