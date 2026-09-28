import type { DaemonSessionSnapshot } from '../store/sessions';
import type { Desktop, LeafMoved } from '../types/generated';
import { activeLeafOf, leafShows, sameLeaf, type ActiveLeaf, type Arrangement, type ShowTarget } from './activeLeaf';
import {
  createLeafHistory,
  moveLeafHistory,
  reconcileLeafHistory,
  recordLeafVisit,
  remapLeafHistory,
  type LeafHistoryDirection,
  type LeafHistoryState,
  type LeafRef,
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

export type IntentTarget =
  | ShowTarget
  | { kind: 'desktop'; desktopId: string }
  | { kind: 'profile'; profileId: string }
  | { kind: 'open' };

// The user's latest gesture that changes what is shown, until an arrival shows its target.
export interface Intent {
  id: number;
  profileId: string;
  target: IntentTarget;
  // The selection bridge sends the show; other intents send their own commands.
  sendsShow: boolean;
  historyCursor: number | null;
  focusOwner: Element | null;
}

export type Arrival =
  | { kind: 'own' }
  | { kind: 'broadcast' }
  | { kind: 'scope' };

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
  intent: Intent | null;
  focusRequest: FocusClaim | null;
  leafHistoryByProfile: Record<string, LeafHistoryState>;
  intentSequence: number;
  focusSequence: number;
  utilityFocusRequestToken: number;
}

export function initialSessionNavigation(): SessionNavigationState {
  return {
    view: 'dashboard',
    viewBeforeGrid: 'dashboard',
    followNextTurn: false,
    intent: null,
    focusRequest: null,
    leafHistoryByProfile: {},
    intentSequence: 0,
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

export function beginIntent(
  state: SessionNavigationState,
  profileId: string,
  target: IntentTarget,
  focusOwner: Element | null,
  { sendsShow = false, historyCursor = null }: { sendsShow?: boolean; historyCursor?: number | null } = {},
): SessionNavigationState {
  const id = state.intentSequence + 1;
  return {
    ...state,
    followNextTurn: false,
    intent: { id, profileId, target, sendsShow, historyCursor, focusOwner },
    focusRequest: null,
    intentSequence: id,
  };
}

export function requestShow(
  state: SessionNavigationState,
  profileId: string,
  target: ShowTarget,
  focusOwner: Element | null,
  historyCursor: number | null = null,
): SessionNavigationState {
  return beginIntent(state, profileId, target, focusOwner, { sendsShow: true, historyCursor });
}

export function selectAgent(
  state: SessionNavigationState,
  profileId: string,
  sessionId: string,
  focusOwner: Element | null,
): SessionNavigationState {
  return requestShow(state, profileId, { kind: 'session', sessionId }, focusOwner);
}

// Commands that never change what a window shows; everything else the user sends does.
const LAYOUT_ONLY_COMMANDS = new Set([
  'desktop_create', 'desktop_rename', 'desktop_reorder', 'desktop_set_shortcut_slot', 'desktop_set_split_ratio',
  'desktop_update_tile', 'profile_create', 'profile_rename',
]);

function commandServes(target: IntentTarget, cmd: string, body: Record<string, unknown>): boolean {
  switch (target.kind) {
    case 'session':
      return cmd === 'desktop_show_session' && body.session_id === target.sessionId;
    case 'leaf':
      return (cmd === 'desktop_show_leaf' && body.leaf_id === target.leafId && body.desktop_id === target.desktopId)
        || (cmd === 'desktop_dock_tile' && body.tile_id === target.leafId && body.desktop_id === target.desktopId);
    case 'desktop':
      return cmd === 'desktop_set_current' && body.desktop_id === target.desktopId;
    case 'profile':
      return cmd === 'profile_select' && body.profile_id === target.profileId;
    default:
      return false;
  }
}

// Rule 1: a command that can change what is shown and does not act on the current intent's target supersedes it.
export function commandSent(state: SessionNavigationState, cmd: string, body: Record<string, unknown>): SessionNavigationState {
  if (!state.intent || LAYOUT_ONLY_COMMANDS.has(cmd) || commandServes(state.intent.target, cmd, body)) return state;
  return { ...state, intent: null };
}

export function endIntent(state: SessionNavigationState): SessionNavigationState {
  if (!state.intent && !state.focusRequest) return state;
  return { ...state, intent: null, focusRequest: null };
}

export function intentFailed(state: SessionNavigationState, id: number): SessionNavigationState {
  return state.intent?.id === id ? endIntent(state) : state;
}

// A history step that landed moves the cursor even when a later step superseded it.
export function historyLanded(state: SessionNavigationState, profileId: string, cursor: number, leaf: LeafRef): SessionNavigationState {
  const history = historyOf(state, profileId);
  const entry = history.entries[cursor];
  if (entry?.leafId !== leaf.leafId || entry.lastKnownDesktopId !== leaf.lastKnownDesktopId) return state;
  return withHistory(state, profileId, { entries: history.entries, cursor });
}

export function focusClaimDelivered(state: SessionNavigationState, id: number): SessionNavigationState {
  return state.focusRequest?.id === id ? { ...state, focusRequest: null } : state;
}

export function claimFocus(state: SessionNavigationState, leaf: { desktopId: string; leafId: string | null }, focusOwner: Element | null): SessionNavigationState {
  const id = state.focusSequence + 1;
  return { ...state, focusSequence: id, focusRequest: { id, desktopId: leaf.desktopId, leafId: leaf.leafId, focusOwner } };
}

export function toggleGrid(state: SessionNavigationState): SessionNavigationState {
  return changeView(state, state.view === 'grid' ? state.viewBeforeGrid : 'grid');
}

export function enterHome(state: SessionNavigationState, followNextTurn: boolean): SessionNavigationState {
  return { ...endIntent(state), view: 'dashboard', followNextTurn };
}

export function changeView(state: SessionNavigationState, update: StateUpdate<AppView>): SessionNavigationState {
  const view = typeof update === 'function' ? update(state.view) : update;
  return {
    ...endIntent(state),
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
  const committed = historyOf(state, arrangement.profileId);
  const intent = state.intent;
  const from = intent?.historyCursor != null && intent.profileId === arrangement.profileId
    ? { entries: committed.entries, cursor: intent.historyCursor }
    : committed;
  const move = moveLeafHistory(from, direction, arrangement.desktops, resumeCurrent);
  const next = from === committed ? withHistory(endIntent(state), arrangement.profileId, move.state) : endIntent(state);
  if (!move.target) return { ...next, followNextTurn: false };
  const target: ShowTarget = { kind: 'leaf', desktopId: move.target.lastKnownDesktopId, leafId: move.target.leafId };
  return requestShow(next, arrangement.profileId, target, focusOwner, move.cursor);
}

export function leafMoved(state: SessionNavigationState, profileId: string, moved: LeafMoved): SessionNavigationState {
  return withHistory(state, profileId, remapLeafHistory(historyOf(state, profileId), moved));
}

function intentShown(arrangement: Arrangement, leaf: ActiveLeaf | null, target: IntentTarget): boolean {
  switch (target.kind) {
    case 'desktop':
      return arrangement.currentDesktopId === target.desktopId;
    case 'profile':
      return arrangement.profileId === target.profileId;
    case 'open':
      return false;
    default:
      return leafShows(leaf, target);
  }
}

export function recordVisit(state: SessionNavigationState, arrangement: Arrangement): SessionNavigationState {
  const leaf = activeLeafOf(arrangement);
  if (state.view !== 'session' || !leaf) return state;
  const history = recordLeafVisit(historyOf(state, arrangement.profileId), { leafId: leaf.leafId, lastKnownDesktopId: leaf.desktopId });
  return withHistory(state, arrangement.profileId, history);
}

// An arrival confirms the intent it shows; another client's change supersedes it; an own answer never does.
export function reconcileArrangement(
  state: SessionNavigationState,
  arrangement: Arrangement,
  previous: Arrangement,
  arrival: Arrival,
): SessionNavigationState {
  const leaf = activeLeafOf(arrangement);
  let next = withHistory(state, arrangement.profileId, reconcileLeafHistory(historyOf(state, arrangement.profileId), arrangement.desktops));
  const intent = state.intent;
  if (intent && intentShown(arrangement, leaf, intent.target)) {
    const shows = intent.target.kind === 'session' || intent.target.kind === 'leaf';
    next = { ...next, intent: null, view: shows ? 'session' : next.view };
    if (intent.historyCursor !== null && intent.profileId === arrangement.profileId) {
      const history = historyOf(next, arrangement.profileId);
      next = withHistory(next, arrangement.profileId, { entries: history.entries, cursor: Math.min(intent.historyCursor, history.entries.length - 1) });
    } else {
      next = recordVisit(next, arrangement);
    }
    if (next.view === 'session' && arrangement.currentDesktopId) {
      next = claimFocus(next, { desktopId: arrangement.currentDesktopId, leafId: leaf?.leafId ?? null }, intent.focusOwner);
    }
    return next;
  }
  if (arrival.kind === 'broadcast') {
    return sameLeaf(leaf, activeLeafOf(previous)) ? next : recordVisit({ ...next, intent: null }, arrangement);
  }
  return intent ? next : recordVisit(next, arrangement);
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
  if (!next || state.intent) return state;
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
