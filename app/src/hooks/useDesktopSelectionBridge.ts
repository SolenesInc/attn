import { useEffect, useRef } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { selectedTile, useProfilesStore, type ProfilesState } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import type { Desktop } from '../types/generated';
import { desktopPaneOfAgent } from '../utils/desktops';
import { isStaleRevision } from './desktopRevisions';

interface Shown {
  desktopId: string | null;
  paneId: string;
  sessionId: string | null;
  tileId: string | null;
}

function shownOf(state: Pick<ProfilesState, 'desktops' | 'currentDesktopId'>): Shown {
  const desktop = state.desktops.find((entry) => entry.id === state.currentDesktopId);
  const paneId = desktop?.active_pane_id ?? '';
  const sessionId = desktop?.panes.find((pane) => pane.pane_id === paneId)?.session_id ?? null;
  return { desktopId: state.currentDesktopId, paneId, sessionId, tileId: selectedTile(state)?.tileId ?? null };
}

function agentToShow(
  state: Pick<ProfilesState, 'desktops'>,
  shown: Shown,
  activeSessionId: string | null,
): string | null {
  if (shown.sessionId || !shown.tileId) return shown.sessionId;
  const known = new Set(useSessionStore.getState().sessions.map((session) => session.id));
  const agents = (state.desktops.find((desktop) => desktop.id === shown.desktopId)?.panes ?? [])
    .map((pane) => pane.session_id)
    .filter((sessionId) => known.has(sessionId));
  if (activeSessionId && agents.includes(activeSessionId)) return activeSessionId;
  return agents[0] ?? null;
}

function keepTileContext(state: Pick<ProfilesState, 'desktops' | 'currentDesktopId'>) {
  const sessions = useSessionStore.getState();
  const shown = shownOf(state);
  if (sessions.view !== 'session' || !shown.tileId) return;
  const context = agentToShow(state, shown, sessions.activeSessionId);
  if (context !== sessions.activeSessionId) useSessionStore.setState({ activeSessionId: context });
}

function arrivedInPendingProfile(
  state: Pick<ProfilesState, 'selectedProfileId'>,
  previous: Pick<ProfilesState, 'selectedProfileId'>,
  sessions: ReturnType<typeof useSessionStore.getState>,
): boolean {
  const pending = sessions.pendingSelection;
  if (!pending || state.selectedProfileId === previous.selectedProfileId) return false;
  const pendingProfileId = sessions.sessions.find((session) => session.id === pending.sessionId)?.profileId;
  return pendingProfileId === state.selectedProfileId;
}

function abandonSelection(sessionId: string) {
  const sessions = useSessionStore.getState();
  if (sessions.pendingSelection?.sessionId !== sessionId && sessions.activeSessionId !== sessionId) return;
  sessions.cancelPendingSelection();
  const state = useProfilesStore.getState();
  const shown = shownOf(state);
  const current = sessions.activeSessionId;
  const shownAgent = agentToShow(state, shown, shown.tileId ? current : null);
  if (shown.tileId) {
    if (shownAgent !== current) useSessionStore.setState({ activeSessionId: shownAgent });
    return;
  }
  if (shownAgent !== current) useSessionStore.getState().setActiveSession(shownAgent);
}

type Command =
  | { key: string; kind: 'active'; desktopId: string; paneId: string }
  | { key: string; kind: 'current'; profileId: string; desktopId: string }
  | { key: string; kind: 'place'; desktop: Desktop; sessionId: string }
  | { key: string; kind: 'profile'; profileId: string };

function nextCommand(intentSessionId: string, intentProfileId: string): Command | null {
  const { desktops, currentDesktopId, selectedProfileId } = useProfilesStore.getState();
  if (!selectedProfileId) return null;
  if (intentProfileId && intentProfileId !== selectedProfileId) {
    return { key: `profile:${intentProfileId}`, kind: 'profile', profileId: intentProfileId };
  }
  const placed = desktopPaneOfAgent(desktops, intentSessionId);
  if (placed) {
    const desktop = desktops.find((entry) => entry.id === placed.desktop_id);
    if (desktop && desktop.active_pane_id !== placed.pane_id) {
      return {
        key: `active:${placed.desktop_id}:${placed.pane_id}`,
        kind: 'active',
        desktopId: placed.desktop_id,
        paneId: placed.pane_id,
      };
    }
    if (placed.desktop_id !== currentDesktopId) {
      return {
        key: `current:${placed.desktop_id}`,
        kind: 'current',
        profileId: selectedProfileId,
        desktopId: placed.desktop_id,
      };
    }
    return null;
  }
  const current = desktops.find((entry) => entry.id === currentDesktopId);
  if (!current) return null;
  return {
    key: `place:${intentSessionId}:${current.id}:${current.revision}`,
    kind: 'place',
    desktop: current,
    sessionId: intentSessionId,
  };
}

export function useDesktopSelectionBridge(
  focusSessionPane: (sessionId: string, paneId: string) => void,
  reportFailure: (message: string) => void,
) {
  const { sendDesktopSetActivePane, sendDesktopSetCurrent, sendDesktopPlaceSession, sendProfileSelect } =
    useDaemonApi();
  const view = useSessionStore((state) => state.view);
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const pendingSessionId = useSessionStore((state) => state.pendingSelection?.sessionId ?? null);
  const requestedSessionId = useSessionStore((state) => state.focusRequest?.sessionId ?? null);
  const tileSelected = useProfilesStore((state) => selectedTile(state) !== null);
  const shownIntent = tileSelected ? requestedSessionId : activeSessionId;
  const intentSessionId = pendingSessionId ?? (view === 'session' ? shownIntent : null);
  const intentProfileId = useSessionStore(
    (state) => state.sessions.find((session) => session.id === intentSessionId)?.profileId ?? null,
  );
  const desktops = useProfilesStore((state) => state.desktops);
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const sentKey = useRef<string | null>(null);
  const reportFailureRef = useRef(reportFailure);
  useEffect(() => {
    reportFailureRef.current = reportFailure;
  }, [reportFailure]);

  useEffect(() => {
    if (!intentSessionId || intentProfileId === null) {
      sentKey.current = null;
      return;
    }
    const command = nextCommand(intentSessionId, intentProfileId);
    if (!command) {
      sentKey.current = null;
      return;
    }
    if (command.key === sentKey.current) return;
    sentKey.current = command.key;
    const release = (error: unknown) => {
      if (sentKey.current === command.key) sentKey.current = null;
      if (isStaleRevision(error)) return;
      abandonSelection(intentSessionId);
      reportFailureRef.current(`Could not show that agent: ${error instanceof Error ? error.message : String(error)}`);
    };
    switch (command.kind) {
      case 'active':
        void sendDesktopSetActivePane(command.desktopId, command.paneId).catch(release);
        return;
      case 'current':
        void sendDesktopSetCurrent(command.profileId, command.desktopId).catch(release);
        return;
      case 'place':
        void sendDesktopPlaceSession({
          desktopId: command.desktop.id,
          sessionId: command.sessionId,
          expectedRevision: command.desktop.revision,
          anchorPaneId: command.desktop.active_pane_id || undefined,
        }).catch(release);
        return;
      case 'profile':
        void sendProfileSelect(command.profileId).catch(release);
    }
  }, [
    intentSessionId,
    intentProfileId,
    desktops,
    currentDesktopId,
    sendDesktopSetActivePane,
    sendDesktopSetCurrent,
    sendDesktopPlaceSession,
    sendProfileSelect,
  ]);

  useEffect(() => {
    if (view !== 'session' || activeSessionId || pendingSessionId) return;
    const state = useProfilesStore.getState();
    const shown = shownOf(state);
    if (shown.tileId) {
      keepTileContext(state);
      return;
    }
    const sessionId = agentToShow(state, shown, null);
    if (sessionId) useSessionStore.getState().setActiveSession(sessionId);
  }, [view, activeSessionId, pendingSessionId, tileSelected, currentDesktopId]);

  const focusRef = useRef(focusSessionPane);
  useEffect(() => {
    focusRef.current = focusSessionPane;
  }, [focusSessionPane]);

  useEffect(
    () =>
      useProfilesStore.subscribe((state, previous) => {
        const shown = shownOf(state);
        const before = shownOf(previous);
        if (shown.desktopId === before.desktopId && shown.paneId === before.paneId) {
          keepTileContext(state);
          return;
        }
        const sessions = useSessionStore.getState();
        if (arrivedInPendingProfile(state, previous, sessions)) return;
        if (sessions.view === 'session') {
          const sessionId = agentToShow(state, shown, sessions.activeSessionId);
          const overridesRequest = shown.tileId !== null && sessions.focusRequest !== null;
          if (sessionId !== sessions.activeSessionId || sessions.pendingSelection || overridesRequest) {
            sessions.setActiveSession(sessionId);
          }
          if (shown.sessionId) focusRef.current(shown.sessionId, shown.paneId);
        }
      }),
    [],
  );
}
