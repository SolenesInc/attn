import type { MouseEvent as ReactMouseEvent } from 'react';
import { useCallback, useMemo, useState } from 'react';
import { focusedQueueRow } from '../components/focusedQueueRow';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAgentOnScreen } from '../hooks/useDesktopSelectionBridge';
import { isAttentionSessionState, type UISessionState } from '../types/sessionState';
import {
  buildQueueBands,
  isCrewQueueEnabled,
  isQueueModeEnabled,
  QUEUE_CREW_SETTING,
  QUEUE_MODE_SETTING,
  queueActions,
  sessionParticipatesInQueue,
} from '../utils/queueBands';
import { AppContentProps } from './appSupport';
import { useAppSessions } from './useAppSessions';

import type { WorkspaceWithSessions } from '../utils/workspaceViewModels';
type EnrichedSession = ReturnType<typeof useAppSessions>['enrichedLocalSessions'][number];

interface Options {
  settings: AppContentProps['settings'];
  desktopViews: WorkspaceWithSessions<EnrichedSession>[];
  unmutedEnrichedSessions: EnrichedSession[];
  enrichedLocalSessions: EnrichedSession[];
  activeSessionId: string | null;
}
export function useAttentionQueue({
  settings,
  desktopViews,
  unmutedEnrichedSessions,
  enrichedLocalSessions,
  activeSessionId,
}: Options) {
  const { sendSetSetting, sendSettleTurn, sendWakeTurn } = useDaemonApi();
  const handleToggleQueueMode = useCallback(() => {
    sendSetSetting(QUEUE_MODE_SETTING, isQueueModeEnabled(settings) ? 'false' : 'true');
  }, [sendSetSetting, settings]);
  const handleToggleCrewQueue = useCallback(() => {
    sendSetSetting(QUEUE_CREW_SETTING, isCrewQueueEnabled(settings) ? 'false' : 'true');
  }, [sendSetSetting, settings]);
  const queueModeEnabled = isQueueModeEnabled(settings);
  const crewQueueEnabled = isCrewQueueEnabled(settings);
  const queueBands = useMemo(
    () =>
      queueModeEnabled
        ? buildQueueBands(desktopViews, { crewInQueue: crewQueueEnabled })
        : null,
    [queueModeEnabled, crewQueueEnabled, desktopViews],
  );

  const activeGroupForCommands = useMemo(
    () =>
      desktopViews.find((group) =>
        group.sessions.some((session) => session.id === activeSessionId),
      ) ?? null,
    [desktopViews, activeSessionId],
  );
  const activeSessionForCommands = useMemo(
    () =>
      activeGroupForCommands?.sessions.find((session) => session.id === activeSessionId) ??
      null,
    [activeGroupForCommands, activeSessionId],
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

  const waitingLocalSessions = unmutedEnrichedSessions.filter(wantsAttention);

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

  const openSnoozeForSession = useCallback((session: { id: string; label: string }, row?: HTMLElement) => {
    const anchorRow = row ?? document.querySelector<HTMLElement>(`.queue-row[data-session-id="${session.id}"]`);
    const rect = anchorRow?.getBoundingClientRect();
    setSnoozeMenu({
      session: { id: session.id, label: session.label },
      anchor: rect ? { top: rect.bottom + 4, left: rect.left } : { top: 72, left: 72 },
    });
  }, []);

  const actionsFor = useCallback(
    (session: EnrichedSession) =>
      queueActions(session, { queueMode: queueModeEnabled, crewInQueue: crewQueueEnabled, now: Date.now() }),
    [queueModeEnabled, crewQueueEnabled],
  );

  const agentOnScreenId = useAgentOnScreen();
  const agentOnScreen = enrichedLocalSessions.find((session) => session.id === agentOnScreenId) ?? null;
  const agentOnScreenActions = agentOnScreen ? actionsFor(agentOnScreen) : null;

  const handleSettleActiveTurn = useMemo(
    () =>
      agentOnScreen && agentOnScreenActions?.settle ? () => sendSettleTurn(agentOnScreen.id) : undefined,
    [agentOnScreen, agentOnScreenActions?.settle, sendSettleTurn],
  );

  const handleSnoozeActiveSession = useMemo(
    () =>
      agentOnScreen && agentOnScreenActions?.snooze ? () => openSnoozeForSession(agentOnScreen) : undefined,
    [agentOnScreen, agentOnScreenActions?.snooze, openSnoozeForSession],
  );

  const handleWakeActiveSession = useMemo(
    () =>
      queueModeEnabled &&
      agentOnScreen?.turnSnoozedUntil &&
      sessionParticipatesInQueue(agentOnScreen, crewQueueEnabled)
        ? () => sendWakeTurn(agentOnScreen.id)
        : undefined,
    [queueModeEnabled, crewQueueEnabled, agentOnScreen, sendWakeTurn],
  );

  const shortcutTarget = useCallback(() => {
    const focused = focusedQueueRow();
    if (focused.kind === 'other') return null;
    const id = focused.kind === 'session' ? focused.sessionId : agentOnScreenId;
    const session = enrichedLocalSessions.find((entry) => entry.id === id);
    if (!session) return null;
    return { session, actions: actionsFor(session), row: focused.kind === 'session' ? focused.row : undefined };
  }, [agentOnScreenId, enrichedLocalSessions, actionsFor]);

  const handleSettleShortcut = useMemo(
    () =>
      queueModeEnabled || handleSettleActiveTurn
        ? () => {
            const target = shortcutTarget();
            if (target?.actions.settle) sendSettleTurn(target.session.id);
          }
        : undefined,
    [queueModeEnabled, handleSettleActiveTurn, shortcutTarget, sendSettleTurn],
  );

  const handleSnoozeShortcut = useMemo(
    () =>
      queueModeEnabled
        ? () => {
            const target = shortcutTarget();
            if (target?.actions.snooze) openSnoozeForSession(target.session, target.row);
          }
        : undefined,
    [queueModeEnabled, shortcutTarget, openSnoozeForSession],
  );

  return {
    queueModeEnabled,
    crewQueueEnabled,
    queueBands,
    activeGroupForCommands,
    activeSessionForCommands,
    handleWakeActiveSession,
    wantsAttention,
    waitingLocalSessions,
    handleSettleActiveTurn,
    snoozeMenu,
    setSnoozeMenu,
    openSnoozeMenu,
    handleSnoozeActiveSession,
    handleSettleShortcut,
    handleSnoozeShortcut,
    handleToggleQueueMode,
    handleToggleCrewQueue,
  };
}
