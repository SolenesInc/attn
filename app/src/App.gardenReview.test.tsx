import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSeed, daemonSession, seedDocument } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { EventMessage } from './test/protocol';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type Review = EventMessage<'garden_review_updated'>['review'];
type ReviewItem = Review['items'][number];

const reviewedSeed = daemonSeed('s-review1', { title: 'Review this seed', tender_session: 'sess-old', rev: 4 });

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
    actions: ['handover', 'send_to_chief', 'keep_growing', 'park', 'harvest', 'wither'],
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

const receipt = { review_id: 'r-1', evidence_version: 'evidence-1' };

async function openGarden(shown: Review) {
  const { daemon } = await renderApp({
    initialState: { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')], seeds: [reviewedSeed] },
  });
  daemon.on('seed_review_show', () => ({
    event: 'seed_review_result',
    operation: 'show',
    success: true,
    candidate_count: shown.items.filter((item) => item.resolution === 'unresolved').length,
    review: shown,
  }));
  daemon.on('seed_document_get', () => ({
    event: 'seed_document_get_result',
    success: true,
    document: seedDocument(reviewedSeed),
  }));
  await click(daemon, 'Open s1');
  await gesture(daemon, () => pressShortcut('board.open'));
  return daemon;
}

async function click(daemon: ScriptedDaemon, name: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name })));
}

const advice = () => screen.getByText('Advisor suggests').closest('section');

describe('App garden review', () => {
  it('opens the review the daemon shows and keeps its progressive updates', async () => {
    const daemon = await openGarden(review());
    await click(daemon, 'Continue review');
    expect(advice()).toHaveTextContent('Park');

    daemon.emit({
      event: 'garden_review_updated',
      review: review([reviewItem({ recommendation: 'harvest', explanation: 'The work is done.' })]),
    });

    expect(advice()).toHaveTextContent('Harvest');
    expect(advice()).toHaveTextContent('The work is done.');
  });

  it('moves the overview to a newer review run and rejects an older broadcast', async () => {
    const daemon = await openGarden(review([], { id: 'r-old', status: 'completed' }));
    expect(screen.queryByTestId('garden-review-prompt')).toBeNull();

    daemon.emit({
      event: 'garden_review_updated',
      review: review([reviewItem({ id: 'r-new.s-review1', run_id: 'r-new' })], { id: 'r-new', captured_at: '2026-08-30T11:00:00Z' }),
    });
    expect(screen.getByTestId('garden-review-prompt')).toHaveTextContent('1 seed needs review');

    daemon.emit({
      event: 'garden_review_updated',
      review: review([reviewItem({ recommendation: 'wither' })], { id: 'r-old' }),
    });
    await click(daemon, 'Continue review');

    expect(advice()).toHaveTextContent('Park');
  });

  it('sends the review receipt with a lifecycle move', async () => {
    const daemon = await openGarden(review());
    await click(daemon, 'Continue review');

    await click(daemon, 'Park');
    fireEvent.change(screen.getByLabelText(/Why are you parking this/), { target: { value: 'Waiting for input.' } });
    await click(daemon, 'Park');

    expect(daemon.sentOf('seed_transition')).toEqual([expect.objectContaining({
      seed_id: 's-review1',
      verb: 'park',
      comment: 'Waiting for input.',
      review: receipt,
    })]);
  });

  it('keeps a reviewed seed growing with its evidence receipt', async () => {
    const daemon = await openGarden(review());
    daemon.on('seed_review_keep', () => ({
      event: 'seed_review_result', operation: 'keep', success: true, candidate_count: 0, review: review(),
    }));
    await click(daemon, 'Continue review');

    await click(daemon, 'Keep growing');

    expect(daemon.sentOf('seed_review_keep')).toEqual([
      expect.objectContaining({ seed_id: 's-review1', review: receipt }),
    ]);
  });

  it('fills the handover composer with the advisory draft', async () => {
    const daemon = await openGarden(review());
    daemon.on('seed_review_draft', () => ({
      event: 'seed_review_draft_result', success: true, handoff: 'Inspect the remaining edge and run the focused test.',
    }));
    await click(daemon, 'Continue review');

    await click(daemon, 'Handover');
    await click(daemon, 'Draft');

    expect(screen.getByLabelText(/What should the new agent know/))
      .toHaveValue('Inspect the remaining edge and run the focused test.');
    expect(daemon.sentOf('seed_review_draft')).toEqual([
      expect.objectContaining({ seed_id: 's-review1', review: receipt }),
    ]);
  });

  it('sends a guarded seed to Chief from the active session with optional guidance', async () => {
    const daemon = await openGarden(review());
    daemon.on('seed_send_to_chief', () => ({
      event: 'seed_send_to_chief_result',
      success: true,
      result: { seed: reviewedSeed, chief_session_id: 'chief', delivery_status: 'queued', detail: 'queued for Chief' },
    }));
    await click(daemon, 'Continue review');

    await click(daemon, 'Send to Chief');
    fireEvent.change(screen.getByLabelText(/What should Chief know/), {
      target: { value: 'Use branch feature/special under /tmp/special.' },
    });
    await click(daemon, 'Send to Chief');

    expect(screen.queryByLabelText(/What should Chief know/)).toBeNull();
    expect(daemon.sentOf('seed_send_to_chief')).toEqual([expect.objectContaining({
      source_session_id: 's1',
      seed_id: 's-review1',
      expected_rev: 4,
      expected_tender_session: 'sess-old',
      expected_tender_member: '',
      guidance: 'Use branch feature/special under /tmp/special.',
      review: receipt,
    })]);
  });
});
