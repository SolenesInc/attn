import type { Seed } from '../types/generated';
import { crewDisplayName } from '../utils/crewName';

export type SeedCheck = NonNullable<Seed['checks']>[number];
export type SeedSharedCheck = NonNullable<Seed['shared_checks']>[number];
export type SeedCheckEvidence = NonNullable<SeedCheck['report']>['evidence'][number];
export type SeedCheckTally = NonNullable<NonNullable<Seed['plot_progress']>['checks']>;

export type CheckState = 'open' | 'reported' | 'confirmed' | 'rejected';

export function checkState(state: string): CheckState {
  return state === 'reported' || state === 'confirmed' || state === 'rejected' ? state : 'open';
}

export function tallyOfChecks(checks: readonly SeedCheck[]): SeedCheckTally | undefined {
  const live = checks.filter((check) => !check.removed);
  if (live.length === 0) return undefined;
  const tally: SeedCheckTally = { total: live.length, reported: 0, confirmed: 0, rejected: 0 };
  for (const check of live) {
    const state = checkState(check.state);
    if (state !== 'open') tally[state] += 1;
  }
  return tally;
}

export function checkTally(seed: Seed): SeedCheckTally | undefined {
  if (seed.plot_progress) return seed.plot_progress.checks;
  return tallyOfChecks(seed.checks ?? []);
}

export function tallyWords(tally: SeedCheckTally): string {
  const parts = [`✓${tally.confirmed}/${tally.total}`];
  if (tally.reported) parts.push(`${tally.reported} to judge`);
  if (tally.rejected) parts.push(`${tally.rejected} rejected`);
  return parts.join(' · ');
}

export function tallySentence(tally: SeedCheckTally): string {
  const parts = [`${tally.confirmed} of ${tally.total} confirmed`];
  if (tally.reported) parts.push(`${tally.reported} to judge`);
  if (tally.rejected) parts.push(`${tally.rejected} rejected`);
  return parts.join(' · ');
}

export function checkAuthor(
  by: { by_session?: string; by_member?: string },
  sessionLabels?: ReadonlyMap<string, string>,
): string {
  if (by.by_member?.trim()) return crewDisplayName(by.by_member);
  const session = by.by_session?.trim();
  if (!session) return 'you';
  return sessionLabels?.get(session)?.trim() || session.slice(0, 8);
}

const PULL_REQUEST_URL = /^https?:\/\/(?:www\.)?github\.com\/[^/]+\/[^/]+\/pull\/(\d+)/;

export function evidenceLabel(evidence: SeedCheckEvidence): string {
  if (evidence.artifact) return evidence.artifact;
  const url = evidence.url ?? '';
  const pullRequest = PULL_REQUEST_URL.exec(url);
  if (pullRequest) return `PR ${pullRequest[1]}`;
  try {
    const parsed = new URL(url);
    return `${parsed.host.replace(/^www\./, '')}${parsed.pathname.replace(/\/$/, '')}`;
  } catch {
    return url;
  }
}
