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
  setSelectedTile: (tile: StateUpdate<TileSelection | null>) => void;
  requestTerminalFocus: () => void;
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
  if (next.activeSessionId !== state.activeSessionId && next.activeSessionId) {
    next.focusRequest = null;
    if (!next.selectedTile) {
      next.view = 'session';
      next.followNextTurn = false;
    }
  }
  next.navigationQueue = navigationQueue(
    next.navigationSessions,
    next.navigationProfileId,
    next.navigationDesktops,
    next.navigationSettings,
  );
  const pending = reconcilePendingSelection(next, next.sessions);
  const advanced = state.pendingSelection
    ? pending
    : advanceQueue(pending, next.sessions, state.navigationQueue, next.navigationQueue);
  if (update.navigationDesktops && advanced.selectedTile) {
    const { desktopId, tileId } = advanced.selectedTile;
    const desktop = update.navigationDesktops.find((entry) => entry.id === desktopId);
    const exists = collectLayoutLeaves(parseLayoutJSON(desktop?.tree_json ?? '')).some(
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
    syncNavigationSettings: (settings) =>
      set((state) =>
        reconcileSessionNavigation(state, {
          navigationSettings: settings,
          launcherConfig: { executables: getAgentExecutableSettings(settings) },
        }),
      ),
  };
}
