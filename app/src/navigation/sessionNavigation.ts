import type { DaemonSessionSnapshot } from '../store/sessions';
import type { Desktop, LeafMoved } from '../types/generated';
import { activeLeafOf, leafShows, type Arrangement, type ShowTarget } from './activeLeaf';
import {
  createLeafHistory,
  moveLeafHistory,
  reconcileLeafHistory,
  recordLeafVisit,
  remapLeafHistory,
  type LeafHistoryDirection,
  type LeafHistoryState,
} from './leafHistory';
import {
  advanceAfterTurnClosed,
  buildQueueBands,
  headOfQueue,
  isCrewQueueEnabled,
  isQueueModeEnabled,
  type QueueBands,
  type QueueBandSession,
} from '../utils/queueBands';
import { buildDesktopViewModels } from '../utils/desktopViewModels';

export type AppView = 'dashboard' | 'session' | 'grid';
export type StateUpdate<T> = T | ((previous: T) => T);

export interface PendingShow {
  id: number;
  profileId: string;
  target: ShowTarget;
  historyCursor: number | null;
  focusOwner: Element | null;
}

export interface FocusClaim {
  id: number;
  desktopId: string;
  // Null claims whichever leaf the daemon shows on that desktop when it lands.
  leafId: string | null;
  focusOwner: Element | null;
}

export interface SessionNavigationState {
  view: AppView;
  viewBeforeGrid: AppView;
  followNextTurn: boolean;
  pendingSelection: PendingShow | null;
  focusRequest: FocusClaim | null;
  leafHistoryByProfile: Record<string, LeafHistoryState>;
  selectionSequence: number;
  navigationEpoch: number;
  expectedArrival: ExpectedArrival | null;
  focusSequence: number;
  utilityFocusRequestToken: number;
}

export function initialSessionNavigation(): SessionNavigationState {
  return {
    view: 'dashboard',
    viewBeforeGrid: 'dashboard',
    followNextTurn: false,
    pendingSelection: null,
    focusRequest: null,
    leafHistoryByProfile: {},
    selectionSequence: 0,
    navigationEpoch: 0,
    expectedArrival: null,
    focusSequence: 0,
    utilityFocusRequestToken: 0,
  };
}

function historyOf(state: SessionNavigationState, profileId: string): LeafHistoryState {
  return state.leafHistoryByProfile[profileId] ?? createLeafHistory();
}

function withHistory(state: SessionNavigationState, profileId: string, history: LeafHistoryState): SessionNavigationState {
  if (!profileId || state.leafHistoryByProfile[profileId] === history) return state;
  return { ...state, leafHistoryByProfile: { ...state.leafHistoryByProfile, [profileId]: history } };
}

export function requestShow(
  state: SessionNavigationState,
  profileId: string,
  target: ShowTarget,
  focusOwner: Element | null,
  historyCursor: number | null = null,
): SessionNavigationState {
  const id = state.selectionSequence + 1;
  return {
    ...state,
    followNextTurn: false,
    pendingSelection: { id, profileId, target, historyCursor, focusOwner },
    focusRequest: null,
    selectionSequence: id,
    navigationEpoch: state.navigationEpoch + 1,
  };
}

export function selectAgent(
  state: SessionNavigationState,
  profileId: string,
  sessionId: string,
  focusOwner: Element | null,
): SessionNavigationState {
  return requestShow(state, profileId, { kind: 'session', sessionId }, focusOwner);
}

export function cancelSelection(state: SessionNavigationState): SessionNavigationState {
  if (!state.pendingSelection && !state.focusRequest) return state;
  return { ...state, pendingSelection: null, focusRequest: null };
}

// A refused history move leaves the cursor on the shown leaf when the move had already passed it.
export function selectionFailed(state: SessionNavigationState, id: number, arrangement: Arrangement): SessionNavigationState {
  const pending = state.pendingSelection;
  if (pending?.id !== id) return state;
  const next = cancelSelection(state);
  const leaf = activeLeafOf(arrangement);
  if (pending.historyCursor === null || pending.profileId !== arrangement.profileId || !leaf) return next;
  const history = historyOf(next, arrangement.profileId);
  const step = pending.historyCursor < history.cursor ? -1 : 1;
  for (let index = pending.historyCursor; index !== history.cursor; index -= step) {
    const entry = history.entries[index];
    if (entry?.leafId === leaf.leafId && entry.lastKnownDesktopId === leaf.desktopId) {
      return withHistory(next, arrangement.profileId, { entries: history.entries, cursor: index });
    }
  }
  return next;
}

export function focusClaimDelivered(state: SessionNavigationState, id: number): SessionNavigationState {
  return state.focusRequest?.id === id ? { ...state, focusRequest: null } : state;
}

export function claimFocus(state: SessionNavigationState, leaf: { desktopId: string; leafId: string | null }, focusOwner: Element | null): SessionNavigationState {
  const id = state.focusSequence + 1;
  return { ...state, focusSequence: id, focusRequest: { id, desktopId: leaf.desktopId, leafId: leaf.leafId, focusOwner } };
}

export interface ExpectedArrival {
  profileId: string;
  desktopId: string | null;
}

export function navigated(state: SessionNavigationState, expect: ExpectedArrival | null = null): SessionNavigationState {
  return { ...state, navigationEpoch: state.navigationEpoch + 1, expectedArrival: expect ?? state.expectedArrival };
}

export function toggleGrid(state: SessionNavigationState): SessionNavigationState {
  return changeView(state, state.view === 'grid' ? state.viewBeforeGrid : 'grid');
}

export function enterHome(state: SessionNavigationState, followNextTurn: boolean): SessionNavigationState {
  return { ...cancelSelection(state), view: 'dashboard', followNextTurn, navigationEpoch: state.navigationEpoch + 1 };
}

export function changeView(state: SessionNavigationState, update: StateUpdate<AppView>): SessionNavigationState {
  const view = typeof update === 'function' ? update(state.view) : update;
  return {
    ...cancelSelection(state),
    view,
    navigationEpoch: view === state.view ? state.navigationEpoch : state.navigationEpoch + 1,
    viewBeforeGrid: view === 'grid' && state.view !== 'grid' ? state.view : state.viewBeforeGrid,
    followNextTurn: view === 'dashboard' && state.followNextTurn,
  };
}

export function navigateHistory(
  state: SessionNavigationState,
  arrangement: Arrangement,
  direction: LeafHistoryDirection,
  resumeCurrent: boolean,
  focusOwner: Element | null,
): SessionNavigationState {
  const committed = historyOf(state, arrangement.profileId);
  const pending = state.pendingSelection;
  const from = pending?.historyCursor != null && pending.profileId === arrangement.profileId
    ? { entries: committed.entries, cursor: pending.historyCursor }
    : committed;
  const move = moveLeafHistory(from, direction, arrangement.desktops, resumeCurrent);
  const next = from === committed ? withHistory(cancelSelection(state), arrangement.profileId, move.state) : cancelSelection(state);
  if (!move.target) return { ...next, followNextTurn: false };
  const target: ShowTarget = { kind: 'leaf', desktopId: move.target.lastKnownDesktopId, leafId: move.target.leafId };
  return requestShow(next, arrangement.profileId, target, focusOwner, move.cursor);
}

export function leafMoved(state: SessionNavigationState, profileId: string, moved: LeafMoved): SessionNavigationState {
  return withHistory(state, profileId, remapLeafHistory(historyOf(state, profileId), moved));
}

export function reconcileArrangement(
  state: SessionNavigationState,
  arrangement: Arrangement,
): SessionNavigationState {
  const leaf = activeLeafOf(arrangement);
  let history = reconcileLeafHistory(historyOf(state, arrangement.profileId), arrangement.desktops);
  let next = state;
  const pending = state.pendingSelection;
  const historyCursor = pending?.profileId === arrangement.profileId ? pending.historyCursor : null;
  if (pending && leafShows(leaf, pending.target) && leaf) {
    if (historyCursor !== null) {
      history = { entries: history.entries, cursor: Math.min(historyCursor, history.entries.length - 1) };
    }
    next = claimFocus({ ...next, view: 'session', pendingSelection: null }, leaf, pending.focusOwner);
  }
  if (next.view === 'session' && leaf && (historyCursor === null || next.pendingSelection === null)) {
    history = recordLeafVisit(history, { leafId: leaf.leafId, lastKnownDesktopId: leaf.desktopId });
  }
  return withHistory(next, arrangement.profileId, history);
}

export function sessionAttentionFields(session: DaemonSessionSnapshot | undefined) {
  return {
    chiefOfStaff: session?.chief_of_staff ?? false,
    turnOwed: session?.turn_owed ?? false,
    turnOpenedAt: session?.turn_opened_at,
    turnSnoozedUntil: session?.turn_snoozed_until,
    stateSince: session?.state_since,
    crewMember: session?.crew_member,
    parentSessionId: session?.parent_session_id,
  };
}

export function navigationQueue(
  sessions: DaemonSessionSnapshot[],
  profileId: string,
  desktops: Desktop[],
  settings: Record<string, string>,
): QueueBands<QueueBandSession> | null {
  if (!isQueueModeEnabled(settings)) return null;
  const queueSessions = sessions
    .filter((session) => session.profile_id === profileId)
    .map((session) => ({
      ...session,
      ...sessionAttentionFields(session),
    }));
  const views = buildDesktopViewModels(desktops, queueSessions);
  return buildQueueBands(views, { crewInQueue: isCrewQueueEnabled(settings) });
}

export function advanceQueue(
  state: SessionNavigationState,
  arrangement: Arrangement,
  previous: QueueBands<QueueBandSession> | null,
  next: QueueBands<QueueBandSession> | null,
  focusOwner: Element | null,
): SessionNavigationState {
  if (!next || state.pendingSelection) return state;
  if (state.view === 'dashboard' && state.followNextTurn) {
    const target = headOfQueue(next);
    return target ? selectAgent(state, arrangement.profileId, target.session.id, focusOwner) : state;
  }
  if (state.view !== 'session') return state;
  const leaf = activeLeafOf(arrangement);
  if (leaf?.kind !== 'agent') return state;
  const advance = advanceAfterTurnClosed(previous?.turns ?? [], next, leaf.sessionId);
  if (!advance) return state;
  return advance.to === 'session'
    ? selectAgent(state, arrangement.profileId, advance.row.session.id, focusOwner)
    : enterHome(state, true);
}
