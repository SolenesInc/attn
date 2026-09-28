import type { DaemonSessionSnapshot, Session } from '../store/sessions';
import type { Desktop, LeafMoved } from '../types/generated';
import { activeLeafOf, leafOn, leafShows, type ActiveLeaf, type Arrangement, type ShowTarget } from './activeLeaf';
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
  leafId: string;
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
  };
}

export function selectAgent(
  state: SessionNavigationState,
  sessions: Session[],
  profileId: string,
  sessionId: string,
  focusOwner: Element | null,
): SessionNavigationState {
  if (!sessions.some((session) => session.id === sessionId)) return state;
  return requestShow(state, profileId, { kind: 'session', sessionId }, focusOwner);
}

export function cancelSelection(state: SessionNavigationState): SessionNavigationState {
  if (!state.pendingSelection && !state.focusRequest) return state;
  return { ...state, pendingSelection: null, focusRequest: null };
}

export function selectionFailed(state: SessionNavigationState, id: number): SessionNavigationState {
  return state.pendingSelection?.id === id ? cancelSelection(state) : state;
}

export function claimFocus(state: SessionNavigationState, leaf: ActiveLeaf, focusOwner: Element | null): SessionNavigationState {
  const id = state.focusSequence + 1;
  return { ...state, focusSequence: id, focusRequest: { id, desktopId: leaf.desktopId, leafId: leaf.leafId, focusOwner } };
}

export function toggleGrid(state: SessionNavigationState): SessionNavigationState {
  return changeView(state, state.view === 'grid' ? state.viewBeforeGrid : 'grid');
}

export function enterHome(state: SessionNavigationState, followNextTurn: boolean): SessionNavigationState {
  return { ...cancelSelection(state), view: 'dashboard', followNextTurn };
}

export function changeView(state: SessionNavigationState, update: StateUpdate<AppView>): SessionNavigationState {
  const view = typeof update === 'function' ? update(state.view) : update;
  return {
    ...cancelSelection(state),
    view,
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
  const move = moveLeafHistory(historyOf(state, arrangement.profileId), direction, arrangement.desktops, resumeCurrent);
  const next = withHistory(cancelSelection(state), arrangement.profileId, move.state);
  if (!move.target) return { ...next, followNextTurn: false };
  const target: ShowTarget = { kind: 'leaf', desktopId: move.target.lastKnownDesktopId, leafId: move.target.leafId };
  return requestShow(next, arrangement.profileId, target, focusOwner, move.cursor);
}

export function leafMoved(state: SessionNavigationState, profileId: string, moved: LeafMoved): SessionNavigationState {
  return withHistory(state, profileId, remapLeafHistory(historyOf(state, profileId), moved));
}

function pendingTargetGone(pending: PendingShow, sessions: Session[], arrangement: Arrangement): boolean {
  if (pending.target.kind === 'session') {
    const sessionId = pending.target.sessionId;
    return !sessions.some((session) => session.id === sessionId);
  }
  if (arrangement.profileId !== pending.profileId) return false;
  const { desktopId, leafId } = pending.target;
  const desktop = arrangement.desktops.find((entry) => entry.id === desktopId);
  return !desktop || !leafOn(arrangement.profileId, desktop, leafId);
}

export function reconcileArrangement(
  state: SessionNavigationState,
  sessions: Session[],
  arrangement: Arrangement,
): SessionNavigationState {
  const leaf = activeLeafOf(arrangement);
  let history = reconcileLeafHistory(historyOf(state, arrangement.profileId), arrangement.desktops);
  let next = state;
  const pending = state.pendingSelection;
  if (pending && leafShows(leaf, pending.target) && leaf) {
    if (pending.historyCursor !== null && pending.profileId === arrangement.profileId) {
      history = { entries: history.entries, cursor: Math.min(pending.historyCursor, history.entries.length - 1) };
    }
    next = claimFocus({ ...next, view: 'session', pendingSelection: null }, leaf, pending.focusOwner);
  } else if (pending && pendingTargetGone(pending, sessions, arrangement)) {
    next = cancelSelection(next);
  }
  if (next.view === 'session' && leaf) {
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
  sessions: Session[],
  arrangement: Arrangement,
  previous: QueueBands<QueueBandSession> | null,
  next: QueueBands<QueueBandSession> | null,
  focusOwner: Element | null,
): SessionNavigationState {
  if (!next || state.pendingSelection) return state;
  if (state.view === 'dashboard' && state.followNextTurn) {
    const target = headOfQueue(next);
    return target ? selectAgent(state, sessions, arrangement.profileId, target.session.id, focusOwner) : state;
  }
  if (state.view !== 'session') return state;
  const leaf = activeLeafOf(arrangement);
  if (leaf?.kind !== 'agent') return state;
  const advance = advanceAfterTurnClosed(previous?.turns ?? [], next, leaf.sessionId);
  if (!advance) return state;
  return advance.to === 'session'
    ? selectAgent(state, sessions, arrangement.profileId, advance.row.session.id, focusOwner)
    : enterHome(state, true);
}
