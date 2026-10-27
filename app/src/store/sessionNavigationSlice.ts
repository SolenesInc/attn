import type { SessionStore } from './sessions';
import { sessionIsOffline } from './sessions';
import type { ActiveLeaf, Arrangement } from '../navigation/activeLeaf';
import type { LeafHistoryDirection } from '../navigation/leafHistory';
import type { LeafMoved } from '../types/generated';
import {
  applyQueueAdvance,
  beginIntent,
  commandSent,
  commandSettled,
  changeView,
  claimFocus,
  endIntent,
  enterHome,
  focusClaimDelivered,
  focusIntent,
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
  type NavigationQueueAdvance,
  queueAdvance,
  type SessionNavigationState,
  type StateUpdate,
} from '../navigation/sessionNavigation';
import type { LeafRef } from '../navigation/leafHistory';
import type { QueueBands, QueueBandSession } from '../utils/queueBands';
import { recordAppAction, type AppActionProps } from '../analytics/appActions';
import { usePendingTerminalInput } from './pendingTerminalInput';

export interface SessionNavigationActions {
  selectAgent: (sessionId: string, focusOwner?: Element | null, announce?: boolean) => boolean;
  selectLeaf: (desktopId: string, leafId: string, focusOwner?: Element | null) => void;
  beginIntent: (target: IntentTarget) => number;
  commandSent: (cmd: string, body: Record<string, unknown>, requestId: string) => number | null;
  commandSettled: (requestId: string) => void;
  cancelIntent: () => void;
  intentFailed: (id: number, reason?: string) => boolean;
  historyLanded: (profileId: string, cursor: number, leaf: LeafRef) => void;
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

function advanceQueue(
  state: SessionNavigationState,
  arrangement: Arrangement,
  previous: QueueBands<QueueBandSession> | null,
  next: QueueBands<QueueBandSession> | null,
): SessionNavigationState {
  const advance = queueAdvance(state, arrangement, previous, next);
  if (advance) recordAppAction('queue_advance', queueAdvanceProps(advance));
  return applyQueueAdvance(state, arrangement, advance, focusOwner());
}

function queueAdvanceProps(advance: NavigationQueueAdvance): AppActionProps {
  if (advance.to === 'dashboard') return { reason: advance.reason, to: 'dashboard' };
  const openedAt = Date.parse(advance.session.turnOpenedAt ?? '');
  return Number.isNaN(openedAt)
    ? { reason: advance.reason, to: 'session' }
    : { reason: advance.reason, to: 'session', wait_ms: Math.max(0, Date.now() - openedAt) };
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
    next.navigationSessions.map((session) => ({ ...session, offline: sessionIsOffline(session, next.outposts) })),
    next.navigationProfileId,
    next.navigationDesktops,
    next.navigationSettings,
  );
  const arrangement = arrangementOf(next);
  const reconciled = arrival ? reconcileArrangement(next, arrangement, arrangementOf(state), arrival) : next;
  const advanced = advanceQueue(reconciled, arrangement, state.navigationQueue, next.navigationQueue);
  return { ...next, ...focusIntent(advanced, arrangement) };
}

export function createSessionNavigationActions(
  write: SetState,
  get: () => SessionStore,
): SessionNavigationActions {
  const set: SetState = (update) => write((state) => {
    const next = { ...state, ...(typeof update === 'function' ? update(state) : update) };
    return focusIntent(next, arrangementOf(next));
  });
  return {
    selectAgent: (sessionId, owner = focusOwner(), announce = true) => {
      set((state) => selectAgent(state, state.navigationProfileId, sessionId, owner, announce));
      return true;
    },
    selectLeaf: (desktopId, leafId, owner = focusOwner()) =>
      set((state) => requestShow(state, state.navigationProfileId, { kind: 'leaf', desktopId, leafId }, owner)),
    beginIntent: (target) => {
      set((state) => beginIntent(state, target.kind === 'profile' ? target.profileId : state.navigationProfileId, target, focusOwner()));
      return get().intentSequence;
    },
    commandSent: (cmd, body, requestId) => {
      const before = get().intent;
      set((state) => commandSent(state, state.navigationProfileId, cmd, body, requestId, focusOwner()));
      const after = get().intent;
      return after && after !== before ? after.id : null;
    },
    commandSettled: (requestId) => set((state) => commandSettled(state, requestId)),
    cancelIntent: () => set((state) => endIntent(state)),
    intentFailed: (id, reason) => {
      const discarded = usePendingTerminalInput.getState().discard(id, reason);
      set((state) => intentFailed(state, id));
      return discarded;
    },
    historyLanded: (profileId, cursor, leaf) => set((state) => historyLanded(state, profileId, cursor, leaf)),
    focusClaimDelivered: (id) => set((state) => focusClaimDelivered(state, id)),
    transferFocus: (leaf) => set((state) => claimFocus(state, leaf, focusOwner(), false)),
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
        return advanceQueue(next, arrangementOf(state), state.navigationQueue, state.navigationQueue);
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
        }),
      ),
  };
}
