import type { MouseEvent as ReactMouseEvent } from 'react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { SnoozePlacement } from '../components/SnoozeMenu';
import { useSessionStore } from '../store/sessions';
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
    placement: SnoozePlacement;
    origin: HTMLElement | null;
    activeSessionId: string | null;
  } | null>(null);

  const openSnoozeMenu = useCallback(
    (session: { id: string; label: string }, event: ReactMouseEvent) => {
      const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
      setSnoozeMenu({
        session,
        placement: { kind: 'anchor', top: rect.bottom + 4, left: rect.left },
        origin: event.currentTarget as HTMLElement,
        activeSessionId,
      });
    },
    [activeSessionId],
  );

  const handleSnoozeActiveSession = useMemo(
    () =>
      queueModeEnabled && activeSessionQueueEligible
        ? (origin: HTMLElement | null = document.activeElement instanceof HTMLElement ? document.activeElement : null) => {
            if (!activeSessionId) return;
            const session = enrichedLocalSessions.find((s) => s.id === activeSessionId);
            if (!session) return;
            const pane = document.querySelector<HTMLElement>(
              `.terminal-wrapper.active [data-pane-session-id="${activeSessionId}"]`,
            );
            setSnoozeMenu({
              session: { id: session.id, label: session.label },
              placement: { kind: 'center', pane },
              origin,
              activeSessionId,
            });
          }
        : undefined,
    [queueModeEnabled, activeSessionQueueEligible, activeSessionId, enrichedLocalSessions],
  );

  useEffect(() => {
    if (snoozeMenu && !enrichedLocalSessions.some((session) => session.id === snoozeMenu.session.id)) {
      setSnoozeMenu(null);
    }
  }, [enrichedLocalSessions, snoozeMenu]);

  const restoreSnoozeFocus = useCallback((reason: 'cancel' | 'choose' | 'removed') => {
    const selectionUnchanged = snoozeMenu?.activeSessionId === useSessionStore.getState().activeSessionId;
    if (reason === 'cancel' && selectionUnchanged && snoozeMenu?.origin?.isConnected) {
      snoozeMenu.origin.focus({ preventScroll: true });
      return;
    }
    const workspace = document.querySelector<HTMLElement>(
      '.terminal-wrapper.active .session-terminal-workspace[data-session-visible="1"]',
    );
    const leaf = workspace?.querySelector<HTMLElement>(`[data-pane-id="${workspace.dataset.activeLeafId}"]`);
    const terminal = leaf?.querySelector<HTMLElement>('.terminal-container');
    const editor = leaf?.querySelector<HTMLElement>('[role="textbox"], textarea, [contenteditable="true"]');
    const tile = leaf?.querySelector<HTMLElement>('.workspace-dock-tile-body');
    const home = document.querySelector<HTMLElement>('[data-testid="sidebar-home"]');
    const destination = [terminal, editor, tile, home].find((element) => element && element.getClientRects().length > 0);
    destination?.focus({ preventScroll: true });
  }, [snoozeMenu]);

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
    restoreSnoozeFocus,
    openSnoozeMenu,
    handleSnoozeActiveSession,
    handleToggleQueueMode,
    handleToggleCrewQueue,
  };
}
