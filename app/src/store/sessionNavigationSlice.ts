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
  navigated,
  forgetArrival,
  focusClaimDelivered,
  leafMoved,
  navigateHistory,
  navigationQueue,
  reconcileArrangement,
  requestShow,
  selectAgent,
  selectionFailed,
  type AppView,
  type ArrivalTarget,
  type SessionNavigationState,
  type StateUpdate,
} from '../navigation/sessionNavigation';
import { getAgentExecutableSettings } from '../utils/agentAvailability';

export interface SessionNavigationActions {
  selectAgent: (sessionId: string) => boolean;
  selectLeaf: (desktopId: string, leafId: string, focusOwner?: Element | null) => void;
  cancelPendingSelection: () => void;
  selectionFailed: (id: number) => void;
  toggleGrid: () => void;
  navigated: (expect?: ArrivalTarget | null) => number | null;
  forgetArrival: (key: number) => void;
  focusClaimDelivered: (id: number) => void;
  claimLeafFocus: (desktopId: string, leafId: string, focusOwner?: Element | null) => void;
  claimDesktopFocus: (desktopId: string, focusOwner?: Element | null) => void;
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

function arrivedAt(
  state: Pick<SessionStore, 'navigationProfileId' | 'navigationCurrentDesktopId'>,
  expected: ArrivalTarget,
): boolean {
  return expected.profileId === state.navigationProfileId
    && (expected.desktopId === null || expected.desktopId === state.navigationCurrentDesktopId);
}

export function reconcileSessionNavigation(
  state: SessionStore,
  update: Partial<SessionStore>,
): Partial<SessionStore> {
  const next = { ...state, ...update };
  if (next.navigationProfileId !== state.navigationProfileId || next.navigationCurrentDesktopId !== state.navigationCurrentDesktopId) {
    // The daemon applies requests in order, so an arrival also settles every one sent before it.
    const arrival = next.expectedArrivals.findIndex((expected) => arrivedAt(next, expected));
    if (arrival >= 0) next.expectedArrivals = next.expectedArrivals.slice(arrival + 1);
    else next.navigationEpoch = state.navigationEpoch + 1;
  }
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
    selectLeaf: (desktopId, leafId, owner = focusOwner()) =>
      set((state) => requestShow(state, state.navigationProfileId, { kind: 'leaf', desktopId, leafId }, owner)),
    cancelPendingSelection: () => set((state) => cancelSelection(state)),
    selectionFailed: (id) => set((state) => selectionFailed(state, id, arrangementOf(state))),
    toggleGrid: () => set((state) => withVisit(state, toggleGrid(state))),
    navigated: (expect) => {
      const state = get();
      const expects = expect && (state.expectedArrivals.length > 0 || !arrivedAt(state, expect)) ? expect : null;
      set(navigated(state, expects));
      return expects ? get().arrivalSequence : null;
    },
    forgetArrival: (key) => set((state) => forgetArrival(state, key)),
    focusClaimDelivered: (id) => set((state) => focusClaimDelivered(state, id)),
    claimLeafFocus: (desktopId, leafId, owner = focusOwner()) => set((state) => claimFocus(state, { desktopId, leafId }, owner)),
    claimDesktopFocus: (desktopId, owner = focusOwner()) => set((state) => claimFocus(state, { desktopId, leafId: null }, owner)),
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
