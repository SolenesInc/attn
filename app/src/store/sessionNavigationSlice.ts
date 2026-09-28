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
  toggleGrid,
  focusClaimDelivered,
  leafMoved,
  navigateHistory,
  navigationQueue,
  reconcileArrangement,
  requestShow,
  selectAgent,
  selectionFailed,
  type AppView,
  type SessionNavigationState,
  type StateUpdate,
} from '../navigation/sessionNavigation';
import { getAgentExecutableSettings } from '../utils/agentAvailability';

export interface SessionNavigationActions {
  selectAgent: (sessionId: string) => boolean;
  selectLeaf: (desktopId: string, leafId: string) => void;
  cancelPendingSelection: () => void;
  selectionFailed: (id: number) => void;
  toggleGrid: () => void;
  focusClaimDelivered: (id: number) => void;
  claimLeafFocus: (desktopId: string, leafId: string) => void;
  transferFocus: (leaf: ActiveLeaf) => void;
  leafMoved: (profileId: string, moved: LeafMoved) => void;
  navigateLeafHistory: (direction: LeafHistoryDirection, resumeCurrent?: boolean) => boolean;
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

function focusOwner(): Element | null {
  return typeof document === 'undefined' ? null : document.activeElement;
}

export function arrangementOf(state: Pick<SessionStore, 'navigationProfileId' | 'navigationCurrentDesktopId' | 'navigationDesktops'>): Arrangement {
  return {
    profileId: state.navigationProfileId,
    currentDesktopId: state.navigationCurrentDesktopId,
    desktops: state.navigationDesktops,
  };
}

function withVisit(state: SessionStore, next: SessionNavigationState): SessionNavigationState {
  return next.view === 'session' && state.view !== 'session' ? reconcileArrangement(next, arrangementOf(state)) : next;
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
  const reconciled = reconcileArrangement(next, arrangement);
  const advanced = state.pendingSelection
    ? reconciled
    : advanceQueue(reconciled, arrangement, state.navigationQueue, next.navigationQueue, focusOwner());
  return { ...next, ...advanced };
}

export function createSessionNavigationActions(
  set: SetState,
  get: () => SessionStore,
): SessionNavigationActions {
  return {
    selectAgent: (sessionId) => {
      set((state) => selectAgent(state, state.navigationProfileId, sessionId, focusOwner()));
      return true;
    },
    selectLeaf: (desktopId, leafId) =>
      set((state) => requestShow(state, state.navigationProfileId, { kind: 'leaf', desktopId, leafId }, focusOwner())),
    cancelPendingSelection: () => set((state) => cancelSelection(state)),
    selectionFailed: (id) => set((state) => selectionFailed(state, id)),
    toggleGrid: () => set((state) => withVisit(state, toggleGrid(state))),
    focusClaimDelivered: (id) => set((state) => focusClaimDelivered(state, id)),
    claimLeafFocus: (desktopId, leafId) => set((state) => claimFocus(state, { desktopId, leafId }, focusOwner())),
    transferFocus: (leaf) => set((state) => claimFocus(state, leaf, focusOwner())),
    leafMoved: (profileId, moved) => set((state) => leafMoved(state, profileId, moved)),
    navigateLeafHistory: (direction, resumeCurrent = false) => {
      set((state) => navigateHistory(state, arrangementOf(state), direction, resumeCurrent, focusOwner()));
      return get().pendingSelection !== null;
    },
    setView: (view) => set((state) => withVisit(state, changeView(state, view))),
    setFollowNextTurn: (update) =>
      set((state) => {
        const followNextTurn = typeof update === 'function' ? update(state.followNextTurn) : update;
        const next = { ...cancelSelection(state), followNextTurn };
        return advanceQueue(next, arrangementOf(state), state.navigationQueue, state.navigationQueue, focusOwner());
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
