import { describe, expect, it } from 'vitest';
import { renderWithDaemon } from '../test/renderApp';
import type { EventMessage } from '../test/protocol';

type Seed = EventMessage<'garden_seeds_updated'>['seeds'][number];

function seed(id: string, title: string): Seed {
  return {
    id,
    title,
    body: '',
    status: 'planted',
    step_slug: title,
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    ready: false,
    state_changed_at: '2026-08-12T10:00:00Z',
    state_changed_at_exact: true,
    created_at: '2026-08-12T10:00:00Z',
    updated_at: '2026-08-12T10:00:00Z',
  };
}
type Review = EventMessage<'garden_review_updated'>['review'];
type ReviewItem = Review['items'][number];


function reviewItem(overrides: Partial<ReviewItem> = {}): ReviewItem {
  return {
    id: 'r-1.s-review1',
    run_id: 'r-1',
    seed_id: 's-review1',
    seed_rev: 4,
    evidence_version: 'evidence-1',
    title: 'Review this seed',
    body: 'Check the outcome.',
    evidence: [{ label: 'Review signal', text: 'The seed is stale.' }],
    actions: ['handover', 'park', 'harvest', 'wither'],
    status: 'ready',
    resolution: 'unresolved',
    recommendation: 'park',
    explanation: 'Useful work remains, but it does not need an agent now.',
    ...overrides,
  };
}

function review(items = [reviewItem()], runOverrides: Partial<Review['run']> = {}): Review {
  return {
    run: {
      id: 'r-1',
      candidate_ids: items.map((item) => item.seed_id),
      recipe: { agent: 'codex', model: 'gpt-5.6-luna', effort: 'xhigh' },
      status: 'running',
      captured_at: '2026-08-30T10:00:00Z',
      ...runOverrides,
    },
    items,
  };
}

const receipt = { reviewId: 'r-1', evidenceVersion: 'evidence-1' };

describe('useDaemonSocket garden', () => {
  it('correlates the Review garden overview and keeps progressive updates', async () => {
    const { daemon, api } = await renderWithDaemon();
    daemon.on('seed_review_show', () => ({
      event: 'seed_review_result', operation: 'show', success: true, candidate_count: 1, review: review(),
    }));

    await expect(api.current.sendSeedReviewShow()).resolves.toEqual(expect.objectContaining({ candidateCount: 1 }));
    expect(api.current.seedReviewOverview.review?.items[0].recommendation).toBe('park');

    daemon.emit({
      event: 'garden_review_updated',
      review: review([reviewItem({ recommendation: 'harvest', explanation: 'The work is done.' })]),
    });
    expect(api.current.seedReviewOverview.review?.items[0].recommendation).toBe('harvest');
  });

  it('moves the overview to a newer review run and rejects an older broadcast', async () => {
    const { daemon, api } = await renderWithDaemon();
    daemon.on('seed_review_show', () => ({
      event: 'seed_review_result', operation: 'show', success: true, candidate_count: 0,
      review: review([], { id: 'r-old', status: 'completed', captured_at: '2026-08-30T10:00:00Z' }),
    }));
    await expect(api.current.sendSeedReviewShow()).resolves.toEqual(expect.objectContaining({ candidateCount: 0 }));

    daemon.emit({
      event: 'garden_review_updated',
      review: review(
        [reviewItem({ id: 'r-new.s-review1', run_id: 'r-new' })],
        { id: 'r-new', captured_at: '2026-08-30T11:00:00Z' },
      ),
    });
    expect(api.current.seedReviewOverview.review?.run.id).toBe('r-new');
    expect(api.current.seedReviewOverview.candidateCount).toBe(1);

    daemon.emit({
      event: 'garden_review_updated',
      review: review([reviewItem({ recommendation: 'wither' })], { id: 'r-old', captured_at: '2026-08-30T10:00:00Z' }),
    });
    expect(api.current.seedReviewOverview.review?.run.id).toBe('r-new');
  });

  it('sends the review receipt with a lifecycle move', async () => {
    const { daemon, api } = await renderWithDaemon();

    void api.current.sendSeedTransition('s-review1', 'park', undefined, undefined, 'Waiting for input.', receipt);

    expect(await daemon.received('seed_transition')).toMatchObject({
      seed_id: 's-review1',
      verb: 'park',
      comment: 'Waiting for input.',
      review: { review_id: 'r-1', evidence_version: 'evidence-1' },
    });
  });

  it('keeps a reviewed seed growing with its evidence receipt', async () => {
    const { daemon, api } = await renderWithDaemon();
    daemon.on('seed_review_keep', () => ({
      event: 'seed_review_result', operation: 'keep', success: true, candidate_count: 0, review: review(),
    }));

    await expect(api.current.sendSeedReviewKeep('s-review1', receipt))
      .resolves.toEqual(expect.objectContaining({ candidateCount: 0 }));
    expect(await daemon.received('seed_review_keep')).toMatchObject({
      seed_id: 's-review1',
      review: { review_id: 'r-1', evidence_version: 'evidence-1' },
    });
  });

  it('returns an advisory handoff draft', async () => {
    const { daemon, api } = await renderWithDaemon();
    daemon.on('seed_review_draft', () => ({
      event: 'seed_review_draft_result', success: true, handoff: 'Inspect the remaining edge and run the focused test.',
    }));

    await expect(api.current.sendSeedReviewDraft('s-review1', receipt))
      .resolves.toBe('Inspect the remaining edge and run the focused test.');
    expect(await daemon.received('seed_review_draft')).toMatchObject({
      seed_id: 's-review1',
      review: { review_id: 'r-1', evidence_version: 'evidence-1' },
    });
  });

  it('sends a guarded seed to Chief with optional placement guidance', async () => {
    const { daemon, api } = await renderWithDaemon();
    const result = {
      seed: seed('s-review1', 'Review this seed'),
      chief_session_id: 'chief',
      delivery_status: 'queued' as const,
      detail: 'queued for Chief',
    };
    daemon.on('seed_send_to_chief', () => ({ event: 'seed_send_to_chief_result', success: true, result }));

    await expect(api.current.sendSeedToChief({
      seedId: 's-review1',
      expectedRev: 4,
      expectedTenderSession: 'sess-old',
      expectedTenderMember: '',
      sourceSessionId: 'sess-user',
      guidance: 'Use branch feature/special under /tmp/special.',
      review: receipt,
    })).resolves.toEqual(result);
    expect(await daemon.received('seed_send_to_chief')).toMatchObject({
      source_session_id: 'sess-user',
      seed_id: 's-review1',
      expected_rev: 4,
      expected_tender_session: 'sess-old',
      expected_tender_member: '',
      guidance: 'Use branch feature/special under /tmp/special.',
      review: { review_id: 'r-1', evidence_version: 'evidence-1' },
    });
  });
});
