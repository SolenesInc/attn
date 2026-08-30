import type { SessionPullRequest } from '../types/generated';

export type SessionPullRequestTone = 'ok' | 'warn' | 'bad' | 'merged' | 'neutral';

export interface SessionPullRequestDescription {
  label: string;
  tone: SessionPullRequestTone;
}

// Draft shares the open bucket: both are live work worth a surface. Closed
// sorts last and never reaches one, so the popover list is its only home.
const STATE_RANK: Record<string, number> = { draft: 0, open: 0, merged: 1, closed: 2 };
const CLOSED_RANK = 2;

function stateRank(pr: SessionPullRequest): number {
  return STATE_RANK[pr.state] ?? 0;
}

function createdAt(pr: SessionPullRequest): number {
  const parsed = Date.parse(pr.created_at);
  return Number.isFinite(parsed) ? parsed : 0;
}

/** Open first, then merged, then closed; newest first inside each bucket. */
export function sortSessionPullRequests(
  pullRequests: readonly SessionPullRequest[],
): SessionPullRequest[] {
  return [...pullRequests].sort(
    (a, b) => stateRank(a) - stateRank(b) || createdAt(b) - createdAt(a),
  );
}

/** The one a surface shows: newest open, else newest merged, else nothing. */
export function pickSessionPullRequest(
  pullRequests?: readonly SessionPullRequest[],
): SessionPullRequest | undefined {
  if (!pullRequests?.length) return undefined;
  const best = sortSessionPullRequests(pullRequests)[0];
  return stateRank(best) === CLOSED_RANK ? undefined : best;
}

// Strongest blocker wins, and every status field can be absent until the
// refresh job has fetched from GitHub once.
export function describeSessionPullRequest(pr: SessionPullRequest): SessionPullRequestDescription {
  if (pr.state === 'merged') return { label: 'merged', tone: 'merged' };
  if (pr.state === 'closed') return { label: 'closed', tone: 'neutral' };
  if (pr.mergeable_state === 'dirty') return { label: 'conflicts', tone: 'bad' };
  if (pr.ci_status === 'failure') return { label: 'checks failed', tone: 'bad' };
  if (pr.review_status === 'changes_requested') return { label: 'changes requested', tone: 'warn' };
  if (pr.ci_status === 'pending') return { label: 'checks running', tone: 'warn' };
  if (pr.state === 'draft') return { label: 'draft', tone: 'neutral' };
  if (pr.review_status === 'approved') {
    const held = pr.mergeable_state === 'blocked' || pr.mergeable_state === 'unstable';
    return { label: held ? 'approved' : 'ready to merge', tone: 'ok' };
  }
  if (pr.review_status === 'pending') return { label: 'in review', tone: 'neutral' };
  if (pr.ci_status === 'success') return { label: 'checks passed', tone: 'ok' };
  return { label: 'open', tone: 'neutral' };
}

export function describeSessionPullRequestChecks(
  pr: SessionPullRequest,
): SessionPullRequestDescription {
  switch (pr.ci_status) {
    case 'success': return { label: 'passed', tone: 'ok' };
    case 'failure': return { label: 'failed', tone: 'bad' };
    case 'pending': return { label: 'running', tone: 'warn' };
    default: return { label: 'none reported', tone: 'neutral' };
  }
}

export function describeSessionPullRequestReview(
  pr: SessionPullRequest,
): SessionPullRequestDescription {
  switch (pr.review_status) {
    case 'approved': return { label: 'approved', tone: 'ok' };
    case 'changes_requested': return { label: 'changes requested', tone: 'warn' };
    case 'pending': return { label: 'waiting on a reviewer', tone: 'neutral' };
    default: return { label: 'none requested', tone: 'neutral' };
  }
}

export function describeSessionPullRequestMerge(
  pr: SessionPullRequest,
): SessionPullRequestDescription {
  switch (pr.mergeable_state) {
    case 'clean': return { label: 'no conflicts', tone: 'ok' };
    case 'dirty': return { label: 'conflicts', tone: 'bad' };
    case 'blocked': return { label: 'blocked', tone: 'warn' };
    case 'unstable': return { label: 'unstable', tone: 'warn' };
    default: return { label: 'unknown', tone: 'neutral' };
  }
}

/** True until the refresh job has fetched this PR's status from GitHub once. */
export function sessionPullRequestAwaitsStatus(pr: SessionPullRequest): boolean {
  return !pr.status_fetched_at;
}

export function sessionPullRequestRepositoryName(repository: string): string {
  const parts = repository.split('/').filter(Boolean);
  return parts[parts.length - 1] || repository;
}
