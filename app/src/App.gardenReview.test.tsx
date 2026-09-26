import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSeed, type DaemonSeed, type DaemonSeedDocument } from './test/daemonFixtures';
import { renderGarden } from './test/garden';
import { gesture, pressShortcut } from './test/renderApp';
import type { EventMessage } from './test/protocol';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type Review = EventMessage<'garden_review_updated'>['review'];
type ReviewItem = Review['items'][number];
type Continuation = NonNullable<DaemonSeed['continuation']>;

const savedContext: Continuation = {
  execution_id: 'sess-old',
  source: 'execution',
  session_live: false,
  native_conversation_id: 'native-old',
  agent: 'codex',
  cwd: '/tmp/work',
  host_kind: 'local',
  directory_state: 'present',
  resume_available: true,
  handover_placement: 'reuse_cwd',
};

const reviewedSeed = daemonSeed('s-review1', {
  title: 'Review this seed',
  body: 'The implementation is complete. Verify the packaged app.',
  tender_session: 'sess-old',
  rev: 4,
});

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

interface GardenScript {
  seeds?: DaemonSeed[];
  documents?: Record<string, Partial<DaemonSeedDocument>>;
  candidates?: number;
}

async function openGarden(shown: Review | undefined, { seeds = [reviewedSeed], documents = {}, candidates }: GardenScript = {}) {
  const garden = await renderGarden(seeds);
  garden.documents = documents;
  const { daemon } = garden;
  daemon.on('seed_review_show', () => ({
    event: 'seed_review_result',
    operation: 'show',
    success: true,
    candidate_count: candidates ?? shown?.items.filter((item) => item.resolution === 'unresolved').length ?? 0,
    review: shown,
  }));
  daemon.on('seed_transition', ({ seed_id }) => ({
    event: 'seed_transition_result',
    success: true,
    seed: garden.seeds.find((seed) => seed.id === seed_id)!,
  }));
  await click(daemon, 'Open s1');
  await gesture(daemon, () => pressShortcut('board.open'));
  return daemon;
}

async function openReview(item: Partial<ReviewItem> = {}, script: GardenScript = {}) {
  const daemon = await openGarden(review([reviewItem(item)]), script);
  await click(daemon, 'Continue review');
  return daemon;
}

function withSavedContext(context: Partial<Continuation> = {}): GardenScript {
  return { seeds: [{ ...reviewedSeed, continuation: { ...savedContext, ...context } }] };
}

async function click(daemon: ScriptedDaemon, name: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name })));
}

const advice = () => screen.getByText('Advisor suggests').closest('section');
const decision = () => screen.queryByRole('heading', { name: 'What should happen?' });
const handoff = () => screen.getByLabelText(/What should the new agent know/);

describe('App garden review', () => {
  it('counts the seeds that need review and starts a new review', async () => {
    const daemon = await openGarden(undefined, { candidates: 3 });
    daemon.on('seed_review_start', () => ({
      event: 'seed_review_result', operation: 'start', success: true, candidate_count: 3, review: review(),
    }));
    expect(screen.getByTestId('garden-review-prompt')).toHaveTextContent('3 seeds need review');

    await click(daemon, 'Review garden');

    expect(daemon.sentOf('seed_review_start')).toHaveLength(1);
    expect(decision()).not.toBeNull();
  });

  it('opens the review the daemon shows and keeps its progressive updates', async () => {
    const daemon = await openReview();
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

  it('offers only the actions the captured item allows', async () => {
    await openReview({ actions: ['handover', 'park'], recommendation: 'handover' });

    expect(screen.getByRole('button', { name: 'Handover' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Park' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Harvest' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Wither' })).toBeNull();
  });

  it('harvests with the advisor’s guidance when it recommended harvesting', async () => {
    const daemon = await openReview({ recommendation: 'harvest', explanation: 'The outcome is complete and verified.' });

    await click(daemon, 'Harvest');

    expect(screen.queryByLabelText('What was completed?')).toBeNull();
    expect(daemon.sentOf('seed_transition')).toEqual([expect.objectContaining({
      seed_id: 's-review1', verb: 'harvest', reason: 'The outcome is complete and verified.', review: receipt,
    })]);
  });

  it('harvests with the completion statement when the advisor recommended something else', async () => {
    const daemon = await openReview({ recommendation: 'park', explanation: 'Keep this seed for later.' });

    await click(daemon, 'Harvest');

    expect(daemon.sentOf('seed_transition')).toEqual([expect.objectContaining({
      verb: 'harvest', reason: "The seed's stated outcome and required verification are complete.", review: receipt,
    })]);
  });

  it.each([
    ['Park', 'park', /Why are you parking this/],
    ['Wither', 'wither', /Why should this be withered/],
  ] as const)('prefills the optional %s comment from matching advisor guidance', async (action, recommendation, label) => {
    const explanation = `${action} this seed because its saved context explains why.`;
    const daemon = await openReview({ recommendation, explanation });

    await click(daemon, action);

    expect(screen.getByLabelText(label)).toHaveValue(explanation);
  });

  it('sends the review receipt with a Park comment in one move', async () => {
    const daemon = await openReview();

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
    const daemon = await openReview();
    daemon.on('seed_review_keep', () => ({
      event: 'seed_review_result', operation: 'keep', success: true, candidate_count: 0, review: review(),
    }));

    await click(daemon, 'Keep growing');

    expect(daemon.sentOf('seed_review_keep')).toEqual([
      expect.objectContaining({ seed_id: 's-review1', review: receipt }),
    ]);
  });

  it('shows the advisor’s progress without making the user wait to act', async () => {
    await openReview({
      status: 'queued',
      recommendation: undefined,
      explanation: undefined,
      advisor_state: 'retrying',
      advisor_attempt: 2,
      advisor_max_attempts: 3,
      advisor_retry_at: '2099-08-30T09:02:00Z',
      advisor_error: 'The last answer did not match the expected format.',
    });

    expect(screen.getByText(/Attempt 2 of 3 did not produce usable advice/)).toBeInTheDocument();
    expect(screen.getByText('The last answer did not match the expected format.')).toBeInTheDocument();
    expect(screen.getByText('You can choose an action now. Advice will appear here if it arrives first.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Harvest' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Handover' })).toBeInTheDocument();
  });

  it('offers a retry for changed advice without lifecycle actions', async () => {
    const daemon = await openReview({
      status: 'invalidated', recommendation: undefined, explanation: undefined, error: 'The seed changed during classification.',
    });
    daemon.on('seed_review_retry', () => ({
      event: 'seed_review_result', operation: 'retry', success: true, candidate_count: 1, review: review(),
    }));
    expect(screen.getByText('This seed changed')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Harvest' })).toBeNull();

    await click(daemon, 'Try again');

    expect(daemon.sentOf('seed_review_retry')).toEqual([expect.objectContaining({ review_id: 'r-1', seed_id: 's-review1' })]);
  });

  it('opens the reviewed seed’s log, read-only', async () => {
    await openReview({}, {
      documents: {
        's-review1': {
          notes: [{
            id: 'n-review1',
            seed_id: 's-review1',
            kind: 'note',
            body: 'Reviewed the **live instance**.',
            author_session: '',
            author_member: 'alder',
            created_at: '2026-08-30T09:01:00Z',
          }],
          notes_total: 1,
        },
      },
    });

    expect(screen.getByText('live instance', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.getByText('Log').closest('details')).toHaveAttribute('open');
    expect(screen.queryByRole('button', { name: 'Overall note' })).toBeNull();
  });

  it('walks up and down a plot and returns to the reviewed seed', async () => {
    const partOfPlot = [{ kind: 'part-of', to: 's-plot11' }];
    const reviewed = { ...reviewedSeed, edges: partOfPlot };
    const sibling = daemonSeed('s-sibling', { title: 'Check the sibling', body: 'Sibling context.', edges: partOfPlot });
    const plot = daemonSeed('s-plot11', {
      title: 'Ship the review garden',
      body: 'The whole plot.',
      plot_progress: { total: 2, done: 0, withered: 0, growing: 2, dormant: 0, ready: 0, blocked: 0 },
    });
    const daemon = await openReview({}, {
      seeds: [reviewed, sibling, plot],
      documents: { 's-plot11': { children: [reviewed, sibling] } },
    });

    await click(daemon, 'Ship the review garden');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Check the sibling/ })));
    expect(screen.getByText('Sibling context.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'You are looking around the plot' }).closest('section'))
      .toHaveTextContent('The review decision still belongs to Review this seed.');
    const reads = daemon.sentOf('seed_document_get').length;

    await click(daemon, '‹ Review seed');

    expect(screen.getByText('The implementation is complete. Verify the packaged app.')).toBeInTheDocument();
    expect(daemon.sentOf('seed_document_get').slice(reads)).toEqual([expect.objectContaining({ seed_id: 's-review1' })]);
  });

  it('fills the handover composer with the advisory draft', async () => {
    const daemon = await openReview();
    daemon.on('seed_review_draft', () => ({
      event: 'seed_review_draft_result', success: true, handoff: 'Inspect the remaining edge and run the focused test.',
    }));

    await click(daemon, 'Handover');
    await click(daemon, 'Draft');

    expect(handoff()).toHaveValue('Inspect the remaining edge and run the focused test.');
    expect(daemon.sentOf('seed_review_draft')).toEqual([
      expect.objectContaining({ seed_id: 's-review1', review: receipt }),
    ]);
  });

  it('keeps the user’s edits when the advisory draft arrives late', async () => {
    const daemon = await openReview();
    await click(daemon, 'Handover');
    await click(daemon, 'Draft');
    fireEvent.change(handoff(), { target: { value: 'My own handoff' } });

    const [draft] = daemon.sentOf('seed_review_draft');
    await gesture(daemon, () => daemon.replyTo(draft, {
      event: 'seed_review_draft_result', request_id: draft.request_id, success: true, handoff: 'Late generated handoff',
    }));

    expect(handoff()).toHaveValue('My own handoff');
    expect(screen.getByText('Draft ready. Your edits were kept.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Use draft' }));
    expect(handoff()).toHaveValue('Late generated handoff');
  });

  it('starts a handover from the saved working context', async () => {
    const daemon = await openReview({}, withSavedContext({ repository_root: '/tmp/repo', branch: 'feature-x' }));

    await click(daemon, 'Handover');

    expect(screen.getByLabelText('Working folder')).toHaveValue('/tmp/work');
    expect(screen.getByLabelText('Git checkout')).toHaveValue('reuse');
    expect(screen.getByLabelText('Branch')).toHaveValue('feature-x');
    expect(screen.getByLabelText('Agent')).toHaveValue('codex');
  });

  it('asks for explicit placement when the saved working folder cannot be reused', async () => {
    const daemon = await openReview({}, withSavedContext({
      cwd: '/tmp/unavailable', handover_placement: 'placement_required', repository_root: '/tmp/repo', branch: 'feature-old',
    }));

    await click(daemon, 'Handover');

    expect(screen.getByLabelText('Working folder')).toHaveValue('');
    expect(screen.getByLabelText('Git checkout')).toHaveValue('none');
  });

  it('keeps the handoff text when the new agent fails to launch', async () => {
    const daemon = await openReview({}, withSavedContext());
    daemon.on('delegate', () => ({ event: 'delegate_result', success: false, error: 'Worker could not start' }));
    await click(daemon, 'Handover');
    fireEvent.change(handoff(), { target: { value: 'Keep this text' } });
    fireEvent.change(screen.getByLabelText('Working folder'), { target: { value: '/tmp/placed' } });
    fireEvent.change(screen.getByLabelText('Git checkout'), { target: { value: 'reuse' } });
    fireEvent.change(screen.getByLabelText('Branch'), { target: { value: 'feature/shared' } });
    fireEvent.click(screen.getByLabelText('Allow sharing an occupied checkout'));

    await click(daemon, 'Handover');

    expect(screen.getByText('Worker could not start')).toBeInTheDocument();
    expect(handoff()).toHaveValue('Keep this text');
    expect(daemon.sentOf('delegate')).toEqual([expect.objectContaining({
      assignment: { kind: 'seed', seed_id: 's-review1', handover: { note: 'Keep this text' } },
      cwd: '/tmp/placed',
      checkout: { kind: 'reuse', branch: 'feature/shared' },
      allow_worktree_reuse: true,
      review: receipt,
    })]);
  });

  it('sends a guarded seed to Chief from the active session with optional guidance', async () => {
    const daemon = await openReview();
    daemon.on('seed_send_to_chief', () => ({
      event: 'seed_send_to_chief_result',
      success: true,
      result: { seed: reviewedSeed, chief_session_id: 'chief', delivery_status: 'queued', detail: 'queued for Chief' },
    }));

    await click(daemon, 'Send to Chief');
    expect(screen.queryByLabelText('Working folder')).toBeNull();
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

  it('closes the composer before leaving the review on Escape', async () => {
    const daemon = await openReview({}, withSavedContext());
    await click(daemon, 'Handover');

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(screen.queryByLabelText(/What should the new agent know/)).toBeNull();
    expect(decision()).not.toBeNull();

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(decision()).toBeNull();
    expect(within(screen.getByRole('region', { name: 'The garden' })).getByTestId('garden-review-prompt')).toBeInTheDocument();
  });
});
