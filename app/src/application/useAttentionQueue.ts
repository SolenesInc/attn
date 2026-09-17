import type { MouseEvent as ReactMouseEvent } from 'react';
import { useCallback, useMemo, useState } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
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
  unmutedWorkspaceViews: WorkspaceWithSessions<EnrichedSession>[];
  workspaceViews: WorkspaceWithSessions<EnrichedSession>[];
  unmutedEnrichedSessions: EnrichedSession[];
  enrichedLocalSessions: EnrichedSession[];
  activeSessionId: string | null;
}
export function useAttentionQueue({
  settings,
  unmutedWorkspaceViews,
  workspaceViews,
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

  const waitingLocalSessions = unmutedEnrichedSessions.filter(wantsAttention);

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

  return {
    queueModeEnabled,
    crewQueueEnabled,
    queueBands,
    activeWorkspaceForCommands,
    activeSessionForCommands,
    activeSessionQueueEligible,
    wantsAttention,
    waitingLocalSessions,
    handleSettleActiveTurn,
    snoozeMenu,
    setSnoozeMenu,
    openSnoozeMenu,
    handleSnoozeActiveSession,
    handleToggleQueueMode,
    handleToggleCrewQueue,
  };
}
