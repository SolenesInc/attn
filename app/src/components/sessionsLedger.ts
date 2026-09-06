import type { SessionLedgerEntry, SessionReopen, SessionReopenEntry } from '../types/generated';

export type SessionScope = 'live' | 'closed' | 'all';

export type SessionRangeId = 'any' | 'today' | 'yesterday' | '7d' | '30d' | 'custom';

export interface SessionRange {
  since?: string;
  until?: string;
}

export interface SessionRangeChoice {
  id: SessionRangeId;
  label: string;
}

export const SESSION_RANGE_CHOICES: SessionRangeChoice[] = [
  { id: 'any', label: 'Any time' },
  { id: 'today', label: 'Today' },
  { id: 'yesterday', label: 'Yesterday' },
  { id: '7d', label: 'Last 7 days' },
  { id: '30d', label: 'Last 30 days' },
  { id: 'custom', label: 'Custom range' },
];

function midnight(now: Date, daysBack: number): Date {
  const day = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  day.setDate(day.getDate() - daysBack);
  return day;
}

export function sessionRangeWindow(id: SessionRangeId, now: Date): SessionRange {
  switch (id) {
    case 'today':
      return { since: midnight(now, 0).toISOString() };
    case 'yesterday':
      return { since: midnight(now, 1).toISOString(), until: midnight(now, 0).toISOString() };
    case '7d':
      return { since: midnight(now, 6).toISOString() };
    case '30d':
      return { since: midnight(now, 29).toISOString() };
    default:
      return {};
  }
}

/** Both picked days count, so the exclusive `until` is the day after the end. */
export function customSessionRange(from: string, to: string): SessionRange | { error: string } {
  const start = parseDay(from);
  if (!start) return { error: `${from || 'The start date'} is not a date like 2026-09-05` };
  const end = parseDay(to);
  if (!end) return { error: `${to || 'The end date'} is not a date like 2026-09-05` };
  if (end < start) return { error: 'The range ends before it starts; swap the two dates' };
  const exclusive = new Date(end);
  exclusive.setDate(exclusive.getDate() + 1);
  return { since: start.toISOString(), until: exclusive.toISOString() };
}

export function isRangeError(range: SessionRange | { error: string }): range is { error: string } {
  return 'error' in range;
}

function parseDay(value: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value.trim());
  if (!match) return null;
  const day = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return Number.isNaN(day.getTime()) ? null : day;
}

export function isClosed(entry: SessionLedgerEntry): boolean {
  return !!entry.closed_at;
}

export function ledgerState(entry: SessionLedgerEntry): string {
  return isClosed(entry) ? 'closed' : entry.state;
}

export function ledgerInstant(entry: SessionLedgerEntry): string {
  return entry.closed_at || entry.last_seen;
}

export function closedBySomeone(
  entry: SessionLedgerEntry,
  sessionLabel?: (sessionId: string) => string,
): string {
  const by = entry.closed_by ?? '';
  if (by === 'user') return 'you';
  return sessionLabel?.(by) || by;
}

export function shortPath(path: string, segments = 2): string {
  const parts = path.split('/').filter(Boolean);
  if (parts.length <= segments) return path;
  return `…/${parts.slice(-segments).join('/')}`;
}

const ABSOLUTE_PATH = /\/(?:[^\s/,;:]+\/)+[^\s,;:]+/g;
const REFUSAL = /^[0-9a-f-]{36} cannot be reopened with (\S+): (.*?)(?:\. Offered instead: (\S+))?$/;

export function compactVerdictText(text: string): string {
  return text.replace(ABSOLUTE_PATH, (match) => shortPath(match));
}

export function compactRefusalText(text: string): string {
  const match = REFUSAL.exec(text);
  if (!match) return compactVerdictText(text);
  const [, action, reason, offered] = match;
  const verb = `${action.split('_').join(' ')} was refused`;
  return offered ? `${verb}; it offers ${reopenActionLabel(offered)} instead` : `${verb}: ${compactVerdictText(reason)}`;
}

const REOPEN_ACTION_LABELS: Record<string, string> = {
  reopen: 'Reopen',
  recreate_worktree_and_reopen: 'Recreate the worktree',
  fetch_recreate_and_reopen: 'Fetch, recreate, reopen',
  start_fresh_same_place: 'Start fresh here',
  start_fresh_elsewhere: 'Start fresh elsewhere',
  start_fresh_default_branch: 'Start fresh on the default branch',
};

export function reopenActionLabel(id: string): string {
  return REOPEN_ACTION_LABELS[id] ?? id;
}

export interface ReopenActionView {
  id: string;
  label: string;
}

export interface ReopenVerdictView {
  refreshing: boolean;
  summary: string;
  reopenable: boolean;
  actions: ReopenActionView[];
  reason?: string;
  warning?: string;
  directoryState: string;
  branchState?: string;
  workspacePlan: string;
  workspaceId: string;
  panePlan: string;
}

export function reopenVerdictView(reopen: SessionReopen): ReopenVerdictView {
  return {
    refreshing: reopen.checking,
    summary: reopen.reason || reopen.warning || 'it can be reopened where it ran',
    reopenable: reopen.reopenable,
    actions: reopen.actions.map((id) => ({ id, label: reopenActionLabel(id) })),
    reason: reopen.reason,
    warning: reopen.warning,
    directoryState: reopen.directory_state,
    branchState: reopen.branch_state,
    workspacePlan: reopen.workspace_plan,
    workspaceId: reopen.workspace_id,
    panePlan: reopen.pane_plan,
  };
}

export function reopenPlacement(
  verdict: ReopenVerdictView,
  workspaceLabel: (workspaceId: string) => string,
): string {
  const workspace = verdict.workspacePlan === 'reuse'
    ? `lands in ${workspaceLabel(verdict.workspaceId)}`
    : 'opens a workspace named after the session';
  const pane = verdict.panePlan === 'reuse' ? 'in its old pane' : 'in a new pane';
  return `${workspace}, ${pane}`;
}

const BRANCH_STATE_LABELS: Record<string, string> = {
  local: 'branch is local',
  remote_only: 'branch is only on the remote',
  gone: 'branch is gone everywhere',
  merged: 'branch was merged and is gone',
  unknown: 'branch is being checked',
};

export function branchStateLabel(state: string | undefined): string | null {
  if (!state) return null;
  return BRANCH_STATE_LABELS[state] ?? state;
}

const DIRECTORY_STATE_LABELS: Record<string, string> = {
  present: 'directory is there',
  missing: 'directory is gone',
  unavailable: 'directory cannot be opened',
  unknown: 'no directory saved',
  remote: 'directory is on another host',
};

export function directoryStateLabel(state: string): string {
  return DIRECTORY_STATE_LABELS[state] ?? state;
}

export function reopenVerdictsById(
  verdicts: SessionReopenEntry[] | undefined,
): Record<string, ReopenVerdictView> {
  const byId: Record<string, ReopenVerdictView> = {};
  for (const entry of verdicts ?? []) {
    byId[entry.session_id] = reopenVerdictView(entry.reopen);
  }
  return byId;
}
