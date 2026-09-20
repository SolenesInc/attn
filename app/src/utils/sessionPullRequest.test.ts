import { describe, expect, it } from 'vitest';
import type { SessionPullRequest } from '../types/generated';
import {
  describeSessionPullRequest,
  pickSessionPullRequest,
  sortSessionPullRequests,
} from './sessionPullRequest';

function pr(overrides: Partial<SessionPullRequest> = {}): SessionPullRequest {
  return {
    repository: 'github.com/victorarias/attn',
    number: 71,
    url: 'https://github.com/victorarias/attn/pull/71',
    created_at: '2026-08-30T12:00:00Z',
    state: 'open',
    ...overrides,
  };
}

describe('pickSessionPullRequest', () => {
  it('takes the newest open one', () => {
    const older = pr({ number: 71, created_at: '2026-08-30T10:00:00Z' });
    const newer = pr({ number: 74, created_at: '2026-08-30T12:00:00Z' });
    expect(pickSessionPullRequest([older, newer])?.number).toBe(74);
  });

  it('prefers an open one over a newer merged one', () => {
    const open = pr({ number: 71, created_at: '2026-08-30T10:00:00Z' });
    const merged = pr({ number: 74, state: 'merged', created_at: '2026-08-30T12:00:00Z' });
    expect(pickSessionPullRequest([merged, open])?.number).toBe(71);
  });

  it('counts a draft as open', () => {
    const draft = pr({ number: 74, state: 'draft', created_at: '2026-08-30T12:00:00Z' });
    const merged = pr({ number: 71, state: 'merged', created_at: '2026-08-30T13:00:00Z' });
    expect(pickSessionPullRequest([merged, draft])?.number).toBe(74);
  });

  it('falls back to the newest merged one', () => {
    const old = pr({ number: 71, state: 'merged', created_at: '2026-08-30T10:00:00Z' });
    const recent = pr({ number: 74, state: 'merged', created_at: '2026-08-30T12:00:00Z' });
    expect(pickSessionPullRequest([old, recent])?.number).toBe(74);
  });

  it('keeps closed pull requests off the line entirely', () => {
    expect(pickSessionPullRequest([pr({ state: 'closed' })])).toBeUndefined();
    expect(pickSessionPullRequest([])).toBeUndefined();
    expect(pickSessionPullRequest(undefined)).toBeUndefined();
  });
});

describe('sortSessionPullRequests', () => {
  it('orders open, then merged, then closed, newest first inside each', () => {
    const list = [
      pr({ number: 70, state: 'closed', created_at: '2026-08-30T15:00:00Z' }),
      pr({ number: 71, state: 'merged', created_at: '2026-08-30T09:00:00Z' }),
      pr({ number: 72, state: 'open', created_at: '2026-08-30T10:00:00Z' }),
      pr({ number: 74, state: 'open', created_at: '2026-08-30T14:00:00Z' }),
      pr({ number: 73, state: 'merged', created_at: '2026-08-30T13:00:00Z' }),
    ];
    expect(sortSessionPullRequests(list).map((entry) => entry.number)).toEqual([74, 72, 73, 71, 70]);
  });
});

describe('describeSessionPullRequest', () => {
  it('reports the strongest blocker first', () => {
    expect(describeSessionPullRequest(pr({
      mergeable_state: 'dirty',
      ci_status: 'failure',
      review_status: 'changes_requested',
    }))).toEqual({ label: 'conflicts', tone: 'bad' });

    expect(describeSessionPullRequest(pr({
      ci_status: 'failure',
      review_status: 'changes_requested',
    }))).toEqual({ label: 'checks failed', tone: 'bad' });

    expect(describeSessionPullRequest(pr({
      ci_status: 'pending',
      review_status: 'changes_requested',
    }))).toEqual({ label: 'changes requested', tone: 'warn' });

    expect(describeSessionPullRequest(pr({ ci_status: 'pending' })))
      .toEqual({ label: 'checks running', tone: 'warn' });
  });

  it('separates a landed pull request from a live one', () => {
    expect(describeSessionPullRequest(pr({ state: 'merged', ci_status: 'failure' })))
      .toEqual({ label: 'merged', tone: 'merged' });
    expect(describeSessionPullRequest(pr({ state: 'closed' })))
      .toEqual({ label: 'closed', tone: 'neutral' });
  });

  it('holds "ready to merge" back while the merge is still blocked', () => {
    expect(describeSessionPullRequest(pr({ review_status: 'approved', mergeable_state: 'clean' })))
      .toEqual({ label: 'ready to merge', tone: 'ok' });
    expect(describeSessionPullRequest(pr({ review_status: 'approved', mergeable_state: 'blocked' })))
      .toEqual({ label: 'approved', tone: 'ok' });
  });

  it('says nothing it cannot know before the first GitHub fetch', () => {
    expect(describeSessionPullRequest(pr())).toEqual({ label: 'open', tone: 'neutral' });
    expect(describeSessionPullRequest(pr({ state: 'draft' })))
      .toEqual({ label: 'draft', tone: 'neutral' });
  });
});
