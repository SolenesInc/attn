import type { WorkspaceWithSessions, WorkspaceViewSession } from './workspaceViewModels';
import { isSnoozed } from './snoozeDurations';

/** Daemon-owned setting selecting the sidebar arrangement. Always read through
 * isQueueModeEnabled so no surface disagrees. */
export const QUEUE_MODE_SETTING = 'queue_mode_enabled';
export const QUEUE_CREW_SETTING = 'queue_crew_enabled';

export function isQueueModeEnabled(settings: Record<string, string>): boolean {
  return (settings[QUEUE_MODE_SETTING] || 'false') === 'true';
}

export function isCrewQueueEnabled(settings: Record<string, string>): boolean {
  return (settings[QUEUE_CREW_SETTING] || 'false') === 'true';
}

/** Auto-settle closes a turn once the user steered the agent and it went back to
 * work. Always read these through the helpers here. */
export const AUTO_SETTLE_ENABLED_SETTING = 'auto_settle_enabled';
export const AUTO_SETTLE_ARM_SETTING = 'auto_settle_arm_seconds';
export const AUTO_SETTLE_COUNTDOWN_SETTING = 'auto_settle_countdown_seconds';

export const DEFAULT_AUTO_SETTLE_ARM_SECONDS = 30;
export const DEFAULT_AUTO_SETTLE_COUNTDOWN_SECONDS = 15;

export function isAutoSettleEnabled(settings: Record<string, string>): boolean {
  return (settings[AUTO_SETTLE_ENABLED_SETTING] || 'false') === 'true';
}

/** The effective seconds for one of the two windows; the daemon normalizes
 * both, so the fallback only covers a read before the first broadcast. */
export function autoSettleSeconds(
  settings: Record<string, string>,
  key: typeof AUTO_SETTLE_ARM_SETTING | typeof AUTO_SETTLE_COUNTDOWN_SETTING,
): number {
  const parsed = Number.parseInt(settings[key] ?? '', 10);
  if (Number.isFinite(parsed) && parsed > 0) return parsed;
  return key === AUTO_SETTLE_ARM_SETTING
    ? DEFAULT_AUTO_SETTLE_ARM_SECONDS
    : DEFAULT_AUTO_SETTLE_COUNTDOWN_SECONDS;
}

export interface QueueBandSession extends WorkspaceViewSession {
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  turnSnoozedUntil?: string;
  pinnedAt?: string;
  /** Set on a shell: the agent session it was split from. */
  parentSessionId?: string;
  crewMember?: string;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
  automation?: { definition_id: string };
}

export interface QueueBandOptions {
  crewInQueue?: boolean;
  now?: number;
}

/** Automation sessions have their own sidebar groups. Crew days only join the
 * queue when the user opts them in. */
export function sessionParticipatesInQueue(
  session: Pick<QueueBandSession, 'automation' | 'crewMember'>,
  crewInQueue = false,
): boolean {
  return !session.automation && (!session.crewMember || crewInQueue);
}

export interface QueueRow<TSession extends QueueBandSession> {
  session: TSession;
  workspaceId: string;
  workspaceTitle: string;
}

/** How long a turn has been outstanding, in the coarsest unit that still reads as an
 * age. `now` is passed in so this stays a pure projection. */
export function formatTurnAge(openedAt: string | undefined, now: number): string {
  if (!openedAt) return '';
  const opened = Date.parse(openedAt);
  if (Number.isNaN(opened)) return '';
  const seconds = Math.max(0, Math.round((now - opened) / 1000));
  if (seconds < 60) return 'now';
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

/** Queue order: turn owed longest first, tie-broken by id so the order is total. Home
 * lists the same turns and must use the same order. */
export function compareTurnOrder(a: QueueBandSession, b: QueueBandSession): number {
  const openedA = a.turnOpenedAt ?? '';
  const openedB = b.turnOpenedAt ?? '';
  if (openedA !== openedB) {
    return openedA < openedB ? -1 : 1;
  }
  return a.id < b.id ? -1 : 1;
}

/** The jump-to-waiting (⌘J) target, in queue order rather than list order. `wants` is
 * the caller's: each arrangement has its own notion of wanting the user. */
export function oldestWantedTurn<TSession extends QueueBandSession>(
  sessions: TSession[],
  wants: (session: TSession) => boolean,
): TSession | null {
  let oldest: TSession | null = null;
  for (const session of sessions) {
    if (!wants(session)) continue;
    if (!oldest || compareTurnOrder(session, oldest) < 0) {
      oldest = session;
    }
  }
  return oldest;
}

export interface QueueBands<TSession extends QueueBandSession> {
  /** The chief's anchored slot. It never queues, so it is always its own row. */
  chief: QueueRow<TSession> | null;
  turns: QueueRow<TSession>[];
  settled: QueueRow<TSession>[];
  /** Sessions pinned out of the queue, in pin order — not state order, so a row never
     * moves because the agent in it started working. */
  pinned: QueueRow<TSession>[];
  /** The days crew members are living right now, member id order. A member's row is
     * permanent, so it renders in the pinned region awake or asleep. */
  crew: QueueRow<TSession>[];
  /** Agents the user deferred, soonest wake first. */
  snoozed: QueueRow<TSession>[];
}

/** Derive the sidebar's standing order. Every queue participant lands in one
 * band; automation sessions and pinned or muted workspaces land in none. */
export function buildQueueBands<TSession extends QueueBandSession>(
  workspaces: WorkspaceWithSessions<TSession>[],
  optionsOrNow: QueueBandOptions | number = {},
): QueueBands<TSession> {
  const options = typeof optionsOrNow === 'number' ? { now: optionsOrNow } : optionsOrNow;
  const now = options.now ?? Date.now();
  let chief: QueueRow<TSession> | null = null;
  const turns: QueueRow<TSession>[] = [];
  const settled: QueueRow<TSession>[] = [];
  const pinned: QueueRow<TSession>[] = [];
  const snoozed: QueueRow<TSession>[] = [];
  const crew: QueueRow<TSession>[] = [];
  const attachedParents = liveParentIds(workspaces);

  for (const workspace of workspaces) {
    for (const session of workspace.sessions) {
      const row: QueueRow<TSession> = {
        session,
        workspaceId: workspace.id,
        workspaceTitle: workspace.title,
      };
      if (session.chiefOfStaff) {
        if (!chief) {
          chief = row;
        }
        continue;
      }
      if (session.automation) {
        continue;
      }
      // Before the workspace's own pin or mute: a member's row is permanent and does not depend on where its day happens to be living.
      if (session.crewMember) {
        crew.push(row);
        if (!options.crewInQueue) {
          continue;
        }
      }
      if (workspace.pinned || workspace.muted) {
        continue;
      }
      if (session.pinnedAt) {
        pinned.push(row);
        continue;
      }
      if (isAttachedSatellite(session, workspace.id, attachedParents)) {
        continue;
      }
      // Before the turn check, so the row's home does not depend on the daemon's settle-as-it-snoozes invariant holding in a mid-broadcast snapshot.
      if (isSnoozed(session.turnSnoozedUntil, now)) {
        snoozed.push(row);
      } else if (session.turnOwed) {
        turns.push(row);
      } else {
        settled.push(row);
      }
    }
  }

  turns.sort((a, b) => compareTurnOrder(a.session, b.session));
  pinned.sort((a, b) => comparePinOrder(a.session, b.session));
  snoozed.sort((a, b) => compareWakeOrder(a.session, b.session));
  crew.sort((a, b) => compareCrewOrder(a.session, b.session));

  return { chief, turns, settled, pinned, snoozed, crew };
}

/** Index every session by its workspace, so a satellite's parent is confirmed present
 * *and* co-located in one lookup. */
function liveParentIds(workspaces: WorkspaceWithSessions<QueueBandSession>[]): Map<string, string> {
  const byId = new Map<string, string>();
  for (const workspace of workspaces) {
    for (const session of workspace.sessions) {
      byId.set(session.id, workspace.id);
    }
  }
  return byId;
}

/** Whether this is a shell whose parent agent is present in the same workspace — the one
 * case that earns no row. An orphan keeps its settled row: the queue reorders, never hides. */
function isAttachedSatellite(
  session: QueueBandSession,
  workspaceId: string,
  parents: Map<string, string>,
): boolean {
  const parentId = session.parentSessionId;
  if (!parentId) return false;
  return parents.get(parentId) === workspaceId;
}

/** Member order: by name, so a member's row is where it was yesterday. */
function compareCrewOrder(a: QueueBandSession, b: QueueBandSession): number {
  const memberA = a.crewMember ?? '';
  const memberB = b.crewMember ?? '';
  if (memberA !== memberB) {
    return memberA < memberB ? -1 : 1;
  }
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/** Pin order: earliest pin first, tie-broken by id so the order is total. */
function comparePinOrder(a: QueueBandSession, b: QueueBandSession): number {
  const pinnedA = a.pinnedAt ?? '';
  const pinnedB = b.pinnedAt ?? '';
  if (pinnedA !== pinnedB) {
    return pinnedA < pinnedB ? -1 : 1;
  }
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/** Soonest wake first, tie-broken by id so the order is total. Home lists the same
 * deferred agents in the same order as the sidebar. */
export function compareWakeOrder(a: QueueBandSession, b: QueueBandSession): number {
  const untilA = a.turnSnoozedUntil ?? '';
  const untilB = b.turnSnoozedUntil ?? '';
  if (untilA !== untilB) {
    return untilA < untilB ? -1 : 1;
  }
  return a.id < b.id ? -1 : 1;
}

/** The row after `settledSessionId` in queue order, wrapping, that is still owed.
 * Null means the old snapshot holds nobody still owed — not that nothing is. */
function nextOwedAfter<TSession extends QueueBandSession>(
  turns: QueueRow<TSession>[],
  settledSessionId: string,
  stillOwed: ReadonlySet<string>,
): QueueRow<TSession> | null {
  const current = turns.findIndex((row) => row.session.id === settledSessionId);
  const start = current === -1 ? 0 : current + 1;
  for (let offset = 0; offset < turns.length; offset += 1) {
    const row = turns[(start + offset) % turns.length];
    if (row.session.id !== settledSessionId && stillOwed.has(row.session.id)) {
      return row;
    }
  }
  return null;
}

/** The turn at the head of the queue. Exported because the handover and home both send
 * the user there and must not disagree about which agent "next" means. */
export function headOfQueue<TSession extends QueueBandSession>(
  bands: QueueBands<TSession> | null,
): QueueRow<TSession> | null {
  return bands?.turns[0] ?? null;
}

/** Where selection goes when a turn closes: the next agent, or home. */
export type QueueAdvance<TSession extends QueueBandSession> =
  | { to: 'session'; row: QueueRow<TSession> }
  | { to: 'dashboard' };

/** Where the user lands when the turn they were looking at closed without them
 * asking. Null means stay put; eligibility always comes from `bands`. */
export function advanceAfterTurnClosed<TSession extends QueueBandSession>(
  previousTurns: QueueRow<TSession>[],
  bands: QueueBands<TSession> | null,
  sessionId: string | null,
): QueueAdvance<TSession> | null {
  if (!sessionId || !bands) return null;
  const owedBefore = previousTurns.some((row) => row.session.id === sessionId);
  const closedNow =
    bands.settled.some((row) => row.session.id === sessionId) ||
    bands.snoozed.some((row) => row.session.id === sessionId);
  if (!owedBefore || !closedNow) return null;
  const stillOwed = new Set(bands.turns.map((row) => row.session.id));
  const next = nextOwedAfter(previousTurns, sessionId, stillOwed) ?? headOfQueue(bands);
  return next ? { to: 'session', row: next } : { to: 'dashboard' };
}
