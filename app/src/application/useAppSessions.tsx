import type { OutpostInfo } from '../types/generated';
import type { TerminalReach } from '../components/SessionTerminalDesktop/useGhosttyPaneRuntime';
import { sessionDisplayName } from '../utils/sessionDisplayName';
import { useEffect, useMemo } from 'react';
import { useProfilesStore } from '../store/profiles';
import { sessionIsOffline, useSessionStore } from '../store/sessions';
import { normalizeSessionAgent } from '../types/sessionAgent';
import { normalizeSessionState } from '../types/sessionState';
import { sessionAttentionFields } from '../navigation/sessionNavigation';
import { buildDesktopViewModels } from '../utils/desktopViewModels';
import { AppContentProps } from './appSupport';
interface Options {
  outposts?: OutpostInfo[];
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  daemonSessions: AppContentProps['daemonSessions'];
  connect: ReturnType<typeof useSessionStore.getState>['connect'];
}
export function useAppSessions({
  sessions,
  daemonSessions,
  connect,
  outposts = [],
}: Options) {
  const enrichedLocalSessions = sessions.map((s) => {
    const daemonSession = daemonSessions.find((ds) => ds.id === s.id);
    const host = daemonSession?.host;
    const outpost = outposts.find((row) => row.id === host);
    const reach: TerminalReach = host ? outpost?.reach ?? 'unreachable' : 'reachable';
    const rawState = daemonSession?.state ?? s.state;
    const paneStatus = s.desktop.agents.find((pane) => pane.sessionId === s.id)?.status;
    const paneState =
      paneStatus === 'failed' ? 'unknown' : paneStatus === 'spawning' ? 'launching' : null;
    return {
      ...s,
      title: daemonSession?.title,
      host,
      hostName: outpost?.name ?? host,
      reach,
      ...sessionAttentionFields(daemonSession),
      offline: sessionIsOffline({ host }, outposts),
      state: paneState || normalizeSessionState(rawState),
      branch: daemonSession?.branch ?? s.branch,
      isWorktree: daemonSession?.is_worktree ?? s.isWorktree,
      delegatedFromChief: daemonSession?.delegated_from_chief ?? false,
      dispatcher_session_id: daemonSession?.dispatcher_session_id,
      dispatcher_member: daemonSession?.dispatcher_member,
      delegation_role: daemonSession?.delegation_role,
      seedId: daemonSession?.seed_id,
      activity: daemonSession?.activity,
      activityAt: daemonSession?.activity_at,
      contextWindowCap: daemonSession?.context_window_cap,
      autoSettleFiresAt: daemonSession?.auto_settle_fires_at,
      autoSettleHeld: daemonSession?.auto_settle_held ?? false,
      autoSettleDismissArmed: daemonSession?.auto_settle_dismiss_armed ?? false,
      terminalExit: daemonSession?.terminal_exit,
      terminalBuildStale: daemonSession?.terminal_build_stale ?? false,
      conversationSharedWith: daemonSession?.conversation_shared_with ?? [],
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
        label: sessionDisplayName(session),
        priority: session.priority,
        agent: normalizeSessionAgent(session.agent),
        state: normalizeSessionState(session.state),
        dispatcher_session_id: session.dispatcher_session_id,
        dispatcher_member: session.dispatcher_member,
        delegation_role: session.delegation_role,
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

  const liveGardenSessions = useMemo(
    () => new Set(daemonSessions.map((session) => session.id)),
    [daemonSessions],
  );

  const gardenSessionLabels = useMemo(
    () => new Map(daemonSessions.map((session) => [session.id, sessionDisplayName(session)])),
    [daemonSessions],
  );

  const worktreePanelSessions = useMemo(
    () =>
      daemonSessions.map((session) => ({
        id: session.id,
        label: sessionDisplayName(session),
        priority: session.priority,
        directory: session.directory,
      })),
    [daemonSessions],
  );

  const desktopViews = useMemo(
    () => buildDesktopViewModels(desktops, visibleEnrichedSessions),
    [desktops, visibleEnrichedSessions],
  );
  const profileSessions = useMemo(
    () => desktopViews.flatMap((group) => group.sessions),
    [desktopViews],
  );
  const hiddenSessions = useMemo(() => {
    const shown = new Set(profileSessions.map((session) => session.id));
    return visibleEnrichedSessions.filter((session) => session.hidden && !shown.has(session.id));
  }, [profileSessions, visibleEnrichedSessions]);

  return {
    desktopViews,
    profileSessions,
    hiddenSessions,
    liveGardenSessions,
    gardenSessionLabels,
    worktreePanelSessions,
    enrichedLocalSessions,
    visibleEnrichedSessions,
    delegationSessions,
    notebookChiefActive,
  };
}
