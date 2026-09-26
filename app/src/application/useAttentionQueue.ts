import type { MouseEvent as ReactMouseEvent } from 'react';
import { useCallback, useMemo, useState } from 'react';
import { focusedQueueRowSessionId } from '../components/QueueSidebar';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAgentOnScreen } from '../hooks/useDesktopSelectionBridge';
import { isAttentionSessionState, type UISessionState } from '../types/sessionState';
import {
  buildQueueBands,
  isCrewQueueEnabled,
  isQueueModeEnabled,
  QUEUE_CREW_SETTING,
  QUEUE_MODE_SETTING,
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
  const { sendSetSetting, sendSettleTurn } = useDaemonApi();
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

  const waitingLocalSessions = unmutedEnrichedSessions.filter(wantsAttention);

  const agentOnScreenId = useAgentOnScreen();
  const agentOnScreen = enrichedLocalSessions.find((session) => session.id === agentOnScreenId) ?? null;
  const settleable = Boolean(
    agentOnScreen &&
      ((queueModeEnabled && sessionParticipatesInQueue(agentOnScreen, crewQueueEnabled)) ||
        (agentOnScreen.automation && agentOnScreen.turnOwed)),
  );
  const handleSettleActiveTurn = useMemo(
    () => (settleable && agentOnScreenId ? () => sendSettleTurn(agentOnScreenId) : undefined),
    [settleable, agentOnScreenId, sendSettleTurn],
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

  const openSnoozeForSession = useCallback(
    (sessionId: string) => {
      const session = enrichedLocalSessions.find((s) => s.id === sessionId);
      if (!session || !sessionParticipatesInQueue(session, crewQueueEnabled)) return;
      const row = document.querySelector<HTMLElement>(`.queue-row[data-session-id="${sessionId}"]`);
      const rect = row?.getBoundingClientRect();
      setSnoozeMenu({
        session: { id: session.id, label: session.label },
        anchor: rect ? { top: rect.bottom + 4, left: rect.left } : { top: 72, left: 72 },
      });
    },
    [enrichedLocalSessions, crewQueueEnabled],
  );

  const handleSnoozeActiveSession = useMemo(
    () =>
      queueModeEnabled && activeSessionQueueEligible && activeSessionId
        ? () => openSnoozeForSession(activeSessionId)
        : undefined,
    [queueModeEnabled, activeSessionQueueEligible, activeSessionId, openSnoozeForSession],
  );

  const handleSettleShortcut = useMemo(
    () =>
      queueModeEnabled || handleSettleActiveTurn
        ? () => {
            const focusedRow = focusedQueueRowSessionId();
            if (!focusedRow) {
              handleSettleActiveTurn?.();
            } else if (enrichedLocalSessions.find((session) => session.id === focusedRow)?.turnOwed) {
              sendSettleTurn(focusedRow);
            }
          }
        : undefined,
    [queueModeEnabled, handleSettleActiveTurn, enrichedLocalSessions, sendSettleTurn],
  );

  const handleSnoozeShortcut = useMemo(
    () =>
      queueModeEnabled
        ? () => {
            const target = focusedQueueRowSessionId() ?? (activeSessionQueueEligible ? activeSessionId : null);
            if (target) openSnoozeForSession(target);
          }
        : undefined,
    [queueModeEnabled, activeSessionQueueEligible, activeSessionId, openSnoozeForSession],
  );

  return {
    queueModeEnabled,
    crewQueueEnabled,
    queueBands,
    activeGroupForCommands,
    activeSessionForCommands,
    activeSessionQueueEligible,
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
