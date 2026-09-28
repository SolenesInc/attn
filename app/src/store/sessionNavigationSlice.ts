import type { SessionStore } from './sessions';
import type { ActiveLeaf, Arrangement } from '../navigation/activeLeaf';
import type { LeafHistoryDirection } from '../navigation/leafHistory';
import type { LeafMoved } from '../types/generated';
import {
  advanceQueue,
  cancelSelection,
  changeView,
  claimFocus,
  enterHome,
  focusDelivered,
  leafMoved,
  navigateHistory,
  navigationQueue,
  reconcileArrangement,
  requestShow,
  selectAgent,
  selectionFailed,
  type AppView,
  type StateUpdate,
} from '../navigation/sessionNavigation';
import { getAgentExecutableSettings } from '../utils/agentAvailability';

export interface SessionNavigationActions {
  selectAgent: (sessionId: string) => boolean;
  selectLeaf: (desktopId: string, leafId: string) => void;
  cancelPendingSelection: () => void;
  selectionFailed: (id: number) => void;
  focusDelivered: (id: number) => void;
  transferFocus: (leaf: ActiveLeaf) => void;
  leafMoved: (profileId: string, moved: LeafMoved) => void;
  navigateAgentHistory: (direction: LeafHistoryDirection, resumeCurrent?: boolean) => boolean;
  setView: (view: StateUpdate<AppView>) => void;
  setFollowNextTurn: (follow: StateUpdate<boolean>) => void;
  goToDashboard: () => void;
  goHomeAwaitingNextTurn: () => void;
  requestTerminalFocus: () => void;
  syncNavigationSettings: (settings: Record<string, string>) => void;
}

type SetState = (
  update: Partial<SessionStore> | ((state: SessionStore) => Partial<SessionStore>),
) => void;

export function arrangementOf(state: Pick<SessionStore, 'navigationProfileId' | 'navigationCurrentDesktopId' | 'navigationDesktops'>): Arrangement {
  return {
    profileId: state.navigationProfileId,
    currentDesktopId: state.navigationCurrentDesktopId,
    desktops: state.navigationDesktops,
  };
}

export function reconcileSessionNavigation(
  state: SessionStore,
  update: Partial<SessionStore>,
): Partial<SessionStore> {
  const next = { ...state, ...update };
  next.navigationQueue = navigationQueue(
    next.navigationSessions,
    next.navigationProfileId,
    next.navigationDesktops,
    next.navigationSettings,
  );
  const arrangement = arrangementOf(next);
  const reconciled = reconcileArrangement(next, next.sessions, arrangement);
  const advanced = state.pendingSelection
    ? reconciled
    : advanceQueue(reconciled, next.sessions, arrangement, state.navigationQueue, next.navigationQueue);
  return { ...next, ...advanced };
}

export function createSessionNavigationActions(
  set: SetState,
  get: () => SessionStore,
): SessionNavigationActions {
  return {
    selectAgent: (sessionId) => {
      const before = get().pendingSelection;
      set((state) => selectAgent(state, state.sessions, state.navigationProfileId, sessionId));
      return get().pendingSelection !== before;
    },
    selectLeaf: (desktopId, leafId) =>
      set((state) => requestShow(state, state.navigationProfileId, { kind: 'leaf', desktopId, leafId })),
    cancelPendingSelection: () => set((state) => cancelSelection(state)),
    selectionFailed: (id) => set((state) => selectionFailed(state, id)),
    focusDelivered: (id) => set((state) => focusDelivered(state, id)),
    transferFocus: (leaf) => set((state) => claimFocus(state, leaf)),
    leafMoved: (profileId, moved) => set((state) => leafMoved(state, profileId, moved)),
    navigateAgentHistory: (direction, resumeCurrent = false) => {
      set((state) => navigateHistory(state, arrangementOf(state), direction, resumeCurrent));
      return get().pendingSelection !== null;
    },
    setView: (view) => set((state) => changeView(state, view)),
    setFollowNextTurn: (update) =>
      set((state) => {
        const followNextTurn = typeof update === 'function' ? update(state.followNextTurn) : update;
        const next = { ...cancelSelection(state), followNextTurn };
        return advanceQueue(next, state.sessions, arrangementOf(state), state.navigationQueue, state.navigationQueue);
      }),
    goToDashboard: () => set((state) => enterHome(state, false)),
    goHomeAwaitingNextTurn: () => set((state) => enterHome(state, true)),
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
