import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { controlBrowserHost } from '../browser/host';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAgentNavigation } from '../hooks/useAgentNavigation';
import { withFreshDesktopRevisions } from '../hooks/desktopRevisions';
import { useAgentOnScreen } from '../hooks/useDesktopSelectionBridge';
import { useProfilesStore, useSelectedTile } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { dispatcherOf } from '../utils/delegationLinks';
import { orderedDesktops } from '../utils/desktops';
import { automationRunGroups, nextRunNeedingYou, runCount } from '../utils/automationRuns';
import { oldestWantedTurn } from '../utils/queueBands';
import { formatShortcut } from '../shortcuts/formatShortcut';
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
  activeSessionId: string | null;
  daemonSessions: AppContentProps['daemonSessions'];
  desktopViews: ReturnType<typeof useAppSessions>['desktopViews'];
  profileSessions: ReturnType<typeof useAppSessions>['profileSessions'];
  attentionQueue: ReturnType<typeof useAttentionQueue>;
  showError: (message: string) => void;
  showNotice: (message: string) => void;
}
export function useAppNavigation({
  activeSessionId,
  daemonSessions,
  desktopViews,
  profileSessions,
  attentionQueue,
  showError,
  showNotice,
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
  const { sendDesktopSetCurrent, sendDesktopSetActivePane, sendDesktopRemoveLeaf } = useDaemonApi();
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
    selectAgentPane,
    cancelPendingSelection,
    back: navigateAgentHistoryBack,
    forward: navigateAgentHistoryForward,
  } = useAgentNavigation();

  const handleSelectSession = selectAgent;
  const selectCreatedSession = selectAgent;

  const { wantsAttention } = attentionQueue;

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
    if (!step) {
      const total = runCount(groups);
      const { profiles, selectedProfileId } = useProfilesStore.getState();
      const profileName = profiles.find((profile) => profile.id === selectedProfileId)?.name ?? 'this profile';
      showNotice(
        total === 0
          ? `No automation runs in ${profileName}`
          : `No run needs you · ${total} ${total === 1 ? 'run' : 'runs'} on file`,
      );
      return;
    }
    handleSelectSession(step.run.id);
    showNotice(
      `${step.group.name} · run ${step.position} of ${step.total} needing you · ${formatShortcut('session.settle')} settles, ${formatShortcut('session.nextRun')} moves on`,
    );
  }, [desktopViews, agentOnScreenId, handleSelectSession, showNotice]);

  const toggleGridMode = useCallback(() => {
    setView((prev) => (prev === 'grid' ? (activeSessionId ? 'session' : 'dashboard') : 'grid'));
  }, [activeSessionId, setView]);

  const [desktopSelectionStyle, setDesktopSelectionStyle] = useState<DesktopSelectionStyle>(
    readDesktopSelectionStyle,
  );
  const handleDesktopSelectionStyleChange = useCallback((style: DesktopSelectionStyle) => {
    persistDesktopSelectionStyle(style);
    setDesktopSelectionStyle(style);
  }, []);

  useEffect(() => {
    probeUiAfterSwitch({
      sessionId: activeSessionId,
      desktopId: currentDesktopId,
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
    if (dispatcher) handleSelectSession(dispatcher.id);
  }, [activeSessionId, daemonSessions, handleSelectSession]);

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
    handleNavigateOutOfSession,
    handleSelectOrchestrator,
  };
}
