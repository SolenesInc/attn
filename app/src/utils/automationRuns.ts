import type { UISessionState } from '../types/sessionState';
import { isSnoozed } from './snoozeDurations';

export interface AutomationRunSession {
  id: string;
  state: UISessionState;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  turnSnoozedUntil?: string;
  stateSince?: string;
  automation?: { definition_id: string; definition_name: string };
}

export interface AutomationRunGroup<S extends AutomationRunSession> {
  id: string;
  name: string;
  runs: S[];
  needingYou: S[];
}

export interface RunBatchStep<S extends AutomationRunSession> {
  group: AutomationRunGroup<S>;
  run: S;
  position: number;
  total: number;
}

const LIVE_STATES: ReadonlySet<UISessionState> = new Set(['launching', 'working', 'scheduled', 'recoverable']);

export function runNeedsYou(run: AutomationRunSession, now: number): boolean {
  return Boolean(run.turnOwed) && !isSnoozed(run.turnSnoozedUntil, now);
}

export function runIsLive(run: AutomationRunSession): boolean {
  return LIVE_STATES.has(run.state);
}

function runPhase(run: AutomationRunSession, now: number): number {
  if (runNeedsYou(run, now)) return 0;
  return runIsLive(run) ? 1 : 2;
}

function compareRuns(now: number) {
  return (a: AutomationRunSession, b: AutomationRunSession) => {
    const phase = runPhase(a, now);
    const byPhase = phase - runPhase(b, now);
    if (byPhase !== 0) return byPhase;
    const byTime =
      phase === 0
        ? (a.turnOpenedAt ?? '').localeCompare(b.turnOpenedAt ?? '')
        : phase === 2
          ? (b.stateSince ?? '').localeCompare(a.stateSince ?? '')
          : 0;
    return byTime || a.id.localeCompare(b.id);
  };
}

export function automationRunGroups<S extends AutomationRunSession>(
  workspaces: readonly { sessions: readonly S[] }[],
  now: number,
): AutomationRunGroup<S>[] {
  const groups = new Map<string, AutomationRunGroup<S>>();
  const seen = new Set<string>();
  for (const workspace of workspaces) {
    for (const session of workspace.sessions) {
      const automation = session.automation;
      if (!automation || seen.has(session.id)) continue;
      seen.add(session.id);
      const group = groups.get(automation.definition_id);
      if (group) group.runs.push(session);
      else groups.set(automation.definition_id, { id: automation.definition_id, name: automation.definition_name, runs: [session], needingYou: [] });
    }
  }
  const order = compareRuns(now);
  return [...groups.values()]
    .map((group) => {
      const runs = group.runs.sort(order);
      return { ...group, runs, needingYou: runs.filter((run) => runNeedsYou(run, now)) };
    })
    .sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
}

export function runCount(groups: readonly AutomationRunGroup<AutomationRunSession>[]): number {
  return groups.reduce((total, group) => total + group.runs.length, 0);
}

export function runsNeedingYouCount(groups: readonly AutomationRunGroup<AutomationRunSession>[]): number {
  return groups.reduce((total, group) => total + group.needingYou.length, 0);
}

export function nextRunNeedingYou<S extends AutomationRunSession>(
  groups: readonly AutomationRunGroup<S>[],
  currentRunId: string | null,
): RunBatchStep<S> | null {
  const batch = groups.flatMap((group) => group.needingYou.map((run) => ({ group, run })));
  if (batch.length === 0) return null;
  const current = batch.findIndex((entry) => entry.run.id === currentRunId);
  const index = (current + 1) % batch.length;
  return { ...batch[index], position: index + 1, total: batch.length };
}
