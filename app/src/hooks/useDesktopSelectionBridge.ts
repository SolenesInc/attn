import { useEffect, useRef } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore, type ProfilesState } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import type { Desktop } from '../types/generated';
import { desktopPaneOfAgent } from '../utils/desktops';

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
  const tileId = paneId && !sessionId ? paneId : null;
  return { desktopId: state.currentDesktopId, paneId, sessionId, tileId };
}

function agentToShow(
  state: Pick<ProfilesState, 'desktops'>,
  shown: Shown,
  activeSessionId: string | null,
): string | null {
  if (shown.sessionId || !shown.tileId) return shown.sessionId;
  const panes = state.desktops.find((desktop) => desktop.id === shown.desktopId)?.panes ?? [];
  if (panes.some((pane) => pane.session_id === activeSessionId)) return activeSessionId;
  return panes[0]?.session_id ?? null;
}

export function resyncShownTile() {
  mirrorShownTile(shownOf(useProfilesStore.getState()));
}

function mirrorShownTile(shown: Shown) {
  const current = useSessionStore.getState().selectedTile;
  if (current?.desktopId === shown.desktopId && current.tileId === shown.tileId) return;
  if (!current && !shown.tileId) return;
  useSessionStore.setState({
    selectedTile: shown.desktopId && shown.tileId ? { desktopId: shown.desktopId, tileId: shown.tileId } : null,
  });
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

export function useDesktopSelectionBridge(focusSessionPane: (sessionId: string, paneId: string) => void) {
  const { sendDesktopSetActivePane, sendDesktopSetCurrent, sendDesktopPlaceSession, sendProfileSelect } =
    useDaemonApi();
  const view = useSessionStore((state) => state.view);
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const pendingSessionId = useSessionStore((state) => state.pendingSelection?.sessionId ?? null);
  const tileSelected = useSessionStore((state) => state.selectedTile !== null);
  const intentSessionId = view === 'session' ? (pendingSessionId ?? (tileSelected ? null : activeSessionId)) : null;
  const intentProfileId = useSessionStore(
    (state) => state.sessions.find((session) => session.id === intentSessionId)?.profileId ?? null,
  );
  const desktops = useProfilesStore((state) => state.desktops);
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const sentKey = useRef<string | null>(null);

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
    const release = () => {
      if (sentKey.current === command.key) sentKey.current = null;
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
    const sessionId = agentToShow(state, shown, null);
    if (!sessionId) return;
    useSessionStore.getState().setActiveSession(sessionId);
    mirrorShownTile(shown);
  }, [view, activeSessionId, pendingSessionId, currentDesktopId]);

  const focusRef = useRef(focusSessionPane);
  useEffect(() => {
    focusRef.current = focusSessionPane;
  }, [focusSessionPane]);

  useEffect(
    () =>
      useProfilesStore.subscribe((state, previous) => {
        const shown = shownOf(state);
        const before = shownOf(previous);
        if (shown.desktopId === before.desktopId && shown.paneId === before.paneId) return;
        const sessions = useSessionStore.getState();
        if (sessions.view === 'session') {
          const sessionId = agentToShow(state, shown, sessions.activeSessionId);
          if (sessionId !== sessions.activeSessionId || sessions.pendingSelection) {
            sessions.setActiveSession(sessionId);
          }
          if (shown.sessionId) focusRef.current(shown.sessionId, shown.paneId);
        }
        mirrorShownTile(shown);
      }),
    [],
  );
}
