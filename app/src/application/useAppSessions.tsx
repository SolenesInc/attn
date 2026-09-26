import { useEffect, useMemo } from 'react';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { normalizeSessionAgent } from '../types/sessionAgent';
import { normalizeSessionState } from '../types/sessionState';
import { sessionAttentionFields } from '../navigation/sessionNavigation';
import { buildDesktopViewModels } from '../utils/workspaceViewModels';
import { AppContentProps } from './appSupport';
interface Options {
  activeSessionId: string | null;
  daemonEndpoints: AppContentProps['daemonEndpoints'];
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  daemonSessions: AppContentProps['daemonSessions'];
  connect: ReturnType<typeof useSessionStore.getState>['connect'];
}
export function useAppSessions({
  activeSessionId,
  daemonEndpoints,
  sessions,
  daemonSessions,
  connect,
}: Options) {
  const endpointById = useMemo(
    () => new Map(daemonEndpoints.map((endpoint) => [endpoint.id, endpoint])),
    [daemonEndpoints],
  );

  const enrichedLocalSessions = sessions.map((s) => {
    const daemonSession = daemonSessions.find((ds) => ds.id === s.id);
    const rawState = daemonSession?.state ?? s.state;
    const paneStatus = s.desktop.agents.find((pane) => pane.sessionId === s.id)?.status;
    const paneState =
      paneStatus === 'failed' ? 'unknown' : paneStatus === 'spawning' ? 'launching' : null;
    const endpointId = daemonSession?.endpoint_id ?? s.endpointId;
    const endpoint = endpointId ? endpointById.get(endpointId) : undefined;
    return {
      ...s,
      ...sessionAttentionFields(daemonSession),
      state: paneState || normalizeSessionState(rawState),
      endpointId,
      endpointName: endpoint?.name,
      endpointStatus: endpoint?.status,
      branch: daemonSession?.branch ?? s.branch,
      isWorktree: daemonSession?.is_worktree ?? s.isWorktree,
      delegatedFromChief: daemonSession?.delegated_from_chief ?? false,
      dispatcher_session_id: daemonSession?.dispatcher_session_id,
      dispatcher_member: daemonSession?.dispatcher_member,
      delegation_role: daemonSession?.delegation_role,
      ticketUnread: daemonSession?.ticket_unread ?? false,
      seedId: daemonSession?.seed_id,
      nudgeFiresAt: daemonSession?.nudge_fires_at,
      activity: daemonSession?.activity,
      activityAt: daemonSession?.activity_at,
      contextWindowCap: daemonSession?.context_window_cap,
      autoSettleFiresAt: daemonSession?.auto_settle_fires_at,
      autoSettleHeld: daemonSession?.auto_settle_held ?? false,
      autoSettleDismissArmed: daemonSession?.auto_settle_dismiss_armed ?? false,
      terminalBuildStale: daemonSession?.terminal_build_stale ?? false,
      usage: daemonSession?.usage,
      automation: daemonSession?.automation ?? s.automation,
      pullRequests: daemonSession?.pull_requests ?? s.pullRequests,
      state_reason: paneState ? undefined : daemonSession?.state_reason,
    };
  });

  const delegationSessions = useMemo(
    () =>
      daemonSessions.map((session) => ({
        id: session.id,
        label: session.label,
        agent: normalizeSessionAgent(session.agent),
        state: normalizeSessionState(session.state),
        dispatcher_session_id: session.dispatcher_session_id,
        dispatcher_member: session.dispatcher_member,
        delegation_role: session.delegation_role,
        endpoint_id: session.endpoint_id,
      })),
    [daemonSessions],
  );

  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const desktops = useProfilesStore((state) => state.desktops);
  const visibleEnrichedSessions = enrichedLocalSessions.filter(
    (session) => session.profileId === selectedProfileId,
  );

  const selectedProfileChiefId = daemonSessions.find(
    (session) => session.chief_of_staff === true && session.profile_id === selectedProfileId,
  )?.id;
  const notebookChiefSession = enrichedLocalSessions.find((session) => session.id === selectedProfileChiefId);
  const notebookChiefActive = notebookChiefSession
    ? notebookChiefSession.state === 'working'
    : undefined;

  useEffect(() => {
    void connect();
  }, [connect]);

  const activeDaemonSession = useMemo(() => {
    if (!activeSessionId) {
      return null;
    }
    return daemonSessions.find((session) => session.id === activeSessionId) || null;
  }, [activeSessionId, daemonSessions]);
  const activeRemoteSession = Boolean(activeDaemonSession?.endpoint_id);
  const activeEndpoint = useMemo(() => {
    const endpointId = activeDaemonSession?.endpoint_id;
    if (!endpointId) {
      return null;
    }
    return endpointById.get(endpointId) ?? null;
  }, [activeDaemonSession?.endpoint_id, endpointById]);
  const liveGardenSessions = useMemo(
    () => new Set(daemonSessions.map((session) => session.id)),
    [daemonSessions],
  );

  const gardenSessionLabels = useMemo(
    () => new Map(daemonSessions.map((session) => [session.id, session.label])),
    [daemonSessions],
  );

  const worktreePanelSessions = useMemo(
    () =>
      daemonSessions.map((session) => ({
        id: session.id,
        label: session.label,
        directory: session.directory,
      })),
    [daemonSessions],
  );

  const desktopViews = useMemo(
    () => buildDesktopViewModels(desktops, visibleEnrichedSessions),
    [desktops, visibleEnrichedSessions],
  );
  const unmutedEnrichedSessions = useMemo(
    () => desktopViews.flatMap((group) => group.sessions),
    [desktopViews],
  );

  return {
    desktopViews,
    unmutedEnrichedSessions,
    activeEndpoint,
    activeRemoteSession,
    liveGardenSessions,
    gardenSessionLabels,
    worktreePanelSessions,
    enrichedLocalSessions,
    endpointById,
    visibleEnrichedSessions,
    delegationSessions,
    notebookChiefActive,
  };
}
