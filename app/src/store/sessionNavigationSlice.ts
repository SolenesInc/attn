import type { SessionStore } from './sessions';
import type { AgentHistoryDirection } from '../navigation/agentHistory';
import {
  activateSession,
  advanceQueue,
  changeView,
  enterHome,
  navigateHistory,
  navigationQueue,
  reconcilePendingSelection,
  selectAgent,
  type AppView,
  type StateUpdate,
  type TileSelection,
} from '../navigation/sessionNavigation';
import {
  closingPaneFallback,
  reconcileWorkspacePanes,
  selectWorkspacePane,
} from '../navigation/workspacePaneSelection';
import { collectLayoutLeaves, parseLayoutJSON } from '../types/workspace';
import { getAgentExecutableSettings } from '../utils/agentAvailability';

export interface SessionNavigationActions {
  selectAgent: (sessionId: string) => boolean;
  selectAgentPane: (sessionId: string, paneId: string) => boolean;
  cancelPendingSelection: () => void;
  setActiveSession: (id: string | null) => void;
  navigateAgentHistory: (
    direction: AgentHistoryDirection,
    resumeCurrent?: boolean,
  ) => string | null;
  setView: (view: StateUpdate<AppView>) => void;
  setFollowNextTurn: (follow: StateUpdate<boolean>) => void;
  goToDashboard: () => void;
  goHomeAwaitingNextTurn: () => void;
  selectSessionlessWorkspace: (id: string) => void;
  setSelectedTile: (tile: StateUpdate<TileSelection | null>) => void;
  requestTerminalFocus: () => void;
  setActivePane: (sessionId: string, paneId: string) => void;
  prepareClosePaneFocus: (sessionId: string, paneId: string) => string;
  clearPreparedClosePaneFocus: (sessionId: string) => void;
  syncNavigationSettings: (settings: Record<string, string>) => void;
}

type SetState = (
  update: Partial<SessionStore> | ((state: SessionStore) => Partial<SessionStore>),
) => void;

export function reconcileSessionNavigation(
  state: SessionStore,
  update: Partial<SessionStore>,
): Partial<SessionStore> {
  const next = { ...state, ...update };
  next.workspacePaneSelections = reconcileWorkspacePanes(
    state.workspacePaneSelections,
    next.sessions,
  );
  if (next.activeSessionId !== state.activeSessionId && next.activeSessionId) {
    next.focusRequest = null;
    if (!next.selectedSessionlessWorkspaceId && !next.selectedTile) {
      next.view = 'session';
      next.followNextTurn = false;
    }
  }
  next.navigationQueue = navigationQueue(
    next.navigationSessions,
    next.navigationWorkspaces,
    next.navigationSettings,
  );
  const pending = reconcilePendingSelection(next, next.sessions);
  const advanced = state.pendingSelection
    ? pending
    : advanceQueue(pending, next.sessions, state.navigationQueue, next.navigationQueue);
  if (update.navigationWorkspaces && advanced.selectedTile) {
    const { workspaceId, tileId } = advanced.selectedTile;
    const workspace = update.navigationWorkspaces.find((entry) => entry.id === workspaceId);
    const exists = collectLayoutLeaves(parseLayoutJSON(workspace?.layout?.layout_json ?? '')).some(
      (leaf) => leaf.type === 'tile' && leaf.tileId === tileId,
    );
    if (!exists) advanced.selectedTile = null;
  }
  return { ...next, ...advanced };
}

export function createSessionNavigationActions(
  set: SetState,
  get: () => SessionStore,
): SessionNavigationActions {
  const select = (sessionId: string, paneId?: string) => {
    set((state) => selectAgent(state, state.sessions, sessionId, paneId));
    return get().focusRequest?.sessionId === sessionId;
  };
  return {
    selectAgent: (sessionId) => select(sessionId),
    selectAgentPane: (sessionId, paneId) => select(sessionId, paneId),
    cancelPendingSelection: () => set({ pendingSelection: null, focusRequest: null }),
    setActiveSession: (id) => set((state) => activateSession(state, id)),
    navigateAgentHistory: (direction, resumeCurrent = false) => {
      set((state) => navigateHistory(state, state.sessions, direction, resumeCurrent));
      return get().focusRequest?.sessionId ?? get().pendingSelection?.sessionId ?? null;
    },
    setView: (view) => set((state) => changeView(state, view)),
    setFollowNextTurn: (update) =>
      set((state) => {
        const followNextTurn = typeof update === 'function' ? update(state.followNextTurn) : update;
        const next = { ...state, followNextTurn, pendingSelection: null };
        return advanceQueue(next, state.sessions, state.navigationQueue, state.navigationQueue);
      }),
    goToDashboard: () => set((state) => enterHome(state, false)),
    goHomeAwaitingNextTurn: () => set((state) => enterHome(state, true)),
    selectSessionlessWorkspace: (id) =>
      set((state) => ({
        ...changeView(state, 'session'),
        selectedSessionlessWorkspaceId: id,
        selectedTile: null,
        utilityFocusRequestToken: state.utilityFocusRequestToken + 1,
      })),
    setSelectedTile: (update) =>
      set((state) => ({
        selectedTile: typeof update === 'function' ? update(state.selectedTile) : update,
        pendingSelection: null,
        focusRequest: null,
        followNextTurn: false,
      })),
    requestTerminalFocus: () =>
      set((state) => ({
        utilityFocusRequestToken: state.utilityFocusRequestToken + 1,
      })),
    setActivePane: (sessionId, paneId) =>
      set((state) => {
        const session = state.sessions.find((entry) => entry.id === sessionId);
        return session
          ? {
              workspacePaneSelections: selectWorkspacePane(
                state.workspacePaneSelections,
                session,
                paneId,
              ),
            }
          : state;
      }),
    prepareClosePaneFocus: (sessionId, paneId) => {
      const state = get();
      const session = state.sessions.find((entry) => entry.id === sessionId);
      if (!session) return '';
      const fallback = closingPaneFallback(state.workspacePaneSelections, session, paneId);
      const workspacePaneSelections = reconcileWorkspacePanes(
        state.workspacePaneSelections,
        state.sessions,
      );
      workspacePaneSelections[session.workspaceId] = {
        ...workspacePaneSelections[session.workspaceId],
        closingFallback: fallback,
      };
      set({ workspacePaneSelections });
      return fallback;
    },
    clearPreparedClosePaneFocus: (sessionId) =>
      set((state) => {
        const workspaceId = state.sessions.find((entry) => entry.id === sessionId)?.workspaceId;
        if (!workspaceId || !state.workspacePaneSelections[workspaceId]) return state;
        return {
          workspacePaneSelections: {
            ...state.workspacePaneSelections,
            [workspaceId]: {
              ...state.workspacePaneSelections[workspaceId],
              closingFallback: undefined,
            },
          },
        };
      }),
    syncNavigationSettings: (settings) =>
      set((state) =>
        reconcileSessionNavigation(state, {
          navigationSettings: settings,
          launcherConfig: { executables: getAgentExecutableSettings(settings) },
        }),
      ),
  };
}
