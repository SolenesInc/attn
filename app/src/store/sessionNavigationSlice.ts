import type { SessionStore } from './sessions';
import type { ActiveLeaf, Arrangement } from '../navigation/activeLeaf';
import type { LeafHistoryDirection } from '../navigation/leafHistory';
import type { LeafMoved } from '../types/generated';
import {
  advanceQueue,
  beginIntent,
  changeView,
  claimFocus,
  endIntent,
  enterHome,
  toggleGrid,
  focusClaimDelivered,
  historyLanded,
  intentFailed,
  leafMoved,
  navigateHistory,
  navigationQueue,
  reconcileArrangement,
  recordVisit,
  requestShow,
  selectAgent,
  type AppView,
  type Arrival,
  type IntentTarget,
  type SessionNavigationState,
  type StateUpdate,
} from '../navigation/sessionNavigation';
import type { LeafRef } from '../navigation/leafHistory';
import { getAgentExecutableSettings } from '../utils/agentAvailability';

export interface SessionNavigationActions {
  selectAgent: (sessionId: string, focusOwner?: Element | null) => boolean;
  selectLeaf: (desktopId: string, leafId: string, focusOwner?: Element | null) => void;
  beginIntent: (target: IntentTarget) => number;
  cancelIntent: () => void;
  intentFailed: (id: number) => void;
  historyLanded: (profileId: string, cursor: number, leaf: LeafRef) => void;
  toggleGrid: () => void;
  focusClaimDelivered: (id: number) => void;
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
  return next.view === 'session' && state.view !== 'session' ? recordVisit(next, arrangementOf(state)) : next;
}

export function reconcileSessionNavigation(
  state: SessionStore,
  update: Partial<SessionStore>,
  arrival: Arrival | null = null,
): Partial<SessionStore> {
  const next = { ...state, ...update };
  next.navigationQueue = navigationQueue(
    next.navigationSessions,
    next.navigationProfileId,
    next.navigationDesktops,
    next.navigationSettings,
  );
  const arrangement = arrangementOf(next);
  const reconciled = arrival ? reconcileArrangement(next, arrangement, arrangementOf(state), arrival) : next;
  const advanced = state.intent?.sendsShow
    ? reconciled
    : advanceQueue(reconciled, arrangement, state.navigationQueue, next.navigationQueue, focusOwner());
  return { ...next, ...advanced };
}

export function createSessionNavigationActions(
  set: SetState,
  get: () => SessionStore,
): SessionNavigationActions {
  return {
    selectAgent: (sessionId, owner = focusOwner()) => {
      set((state) => selectAgent(state, state.navigationProfileId, sessionId, owner));
      return true;
    },
    selectLeaf: (desktopId, leafId, owner = focusOwner()) =>
      set((state) => requestShow(state, state.navigationProfileId, { kind: 'leaf', desktopId, leafId }, owner)),
    beginIntent: (target) => {
      set((state) => beginIntent(state, target.kind === 'profile' ? target.profileId : state.navigationProfileId, target, focusOwner()));
      return get().intentSequence;
    },
    cancelIntent: () => set((state) => endIntent(state)),
    intentFailed: (id) => set((state) => intentFailed(state, id)),
    historyLanded: (profileId, cursor, leaf) => set((state) => historyLanded(state, profileId, cursor, leaf)),
    toggleGrid: () => set((state) => withVisit(state, toggleGrid(state))),
    focusClaimDelivered: (id) => set((state) => focusClaimDelivered(state, id)),
    transferFocus: (leaf) => set((state) => claimFocus(state, leaf, focusOwner())),
    leafMoved: (profileId, moved) => set((state) => leafMoved(state, profileId, moved)),
    navigateLeafHistory: (direction, resumeCurrent = false) => {
      set((state) => navigateHistory(state, arrangementOf(state), direction, resumeCurrent, focusOwner()));
      return get().intent !== null;
    },
    setView: (view) => set((state) => withVisit(state, changeView(state, view))),
    setFollowNextTurn: (update) =>
      set((state) => {
        const followNextTurn = typeof update === 'function' ? update(state.followNextTurn) : update;
        const next = { ...endIntent(state), followNextTurn };
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
