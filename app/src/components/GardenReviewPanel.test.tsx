import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { GardenReviewPanel } from './GardenReviewPanel';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import type { GardenReview, GardenReviewItem, Seed, SeedDocument } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-review1',
    title: 'Finish the seed review',
    body: 'The implementation is complete. Verify the packaged app.',
    status: 'growing',
    state_changed_at: '2026-08-20T09:00:00Z',
    state_changed_at_exact: true,
    step_slug: 'finish-the-seed-review',
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    ready: false,
    template: false,
    gate: false,
    vars: [],
    rev: 7,
    created_at: '2026-08-20T09:00:00Z',
    updated_at: '2026-08-20T09:00:00Z',
    continuation: {
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
    },
    ...overrides,
  };
}

function document(overrides: Partial<SeedDocument> = {}): SeedDocument {
  return {
    seed: seed(),
    tender_holds: false,
    children: [],
    notes: [],
    notes_total: 0,
    artifacts: [],
    ...overrides,
  };
}

function item(overrides: Partial<GardenReviewItem> = {}): GardenReviewItem {
  return {
    id: 'r-review1.s-review1',
    run_id: 'r-review1',
    seed_id: 's-review1',
    seed_rev: 7,
    evidence_version: 'evidence-1',
    title: 'Finish the seed review',
    body: 'The implementation is complete. Verify the packaged app.',
    evidence: [
      { label: 'Review signal', text: 'No active agent has held this seed for seven days.' },
      { label: 'Working directory', text: 'The saved local working directory is present.' },
    ],
    actions: ['resume', 'handover', 'keep_growing', 'park', 'harvest', 'wither'],
    status: 'ready',
    resolution: 'unresolved',
    recommendation: 'harvest',
    explanation: 'The stated outcome looks complete and the verification is recorded.',
    cited_evidence: ['The seed body says the implementation is complete.'],
    ...overrides,
  };
}

function review(value = item()): GardenReview {
  return {
    run: {
      id: 'r-review1',
      candidate_ids: [value.seed_id],
      recipe: { agent: 'codex', model: 'gpt-5.6-luna', effort: 'xhigh' },
      status: 'running',
      captured_at: '2026-08-30T09:00:00Z',
    },
    items: [value],
  };
}

function props(value = review()) {
  return {
    review: value,
    seeds: [seed()],
    frame: 'full' as const,
    onExit: vi.fn(),
    onClose: vi.fn(),
    onEscapeFloor: vi.fn(),
    fetchSeedDocument: vi.fn().mockImplementation(() => new Promise<SeedDocument>(() => {})),
    onMoveSeed: vi.fn().mockResolvedValue(seed()),
    onResumeSeed: vi.fn().mockResolvedValue({}),
    onKeepSeed: vi.fn().mockResolvedValue({}),
    onHandoverSeed: vi.fn().mockResolvedValue({ session_id: 'sess-new' }),
    onSendSeedToChief: vi.fn().mockResolvedValue({ chief_session_id: 'chief' }),
    onRetry: vi.fn().mockResolvedValue({}),
    onDraft: vi.fn().mockResolvedValue('Generated handoff'),
    onRefresh: vi.fn().mockResolvedValue({}),
  };
}

afterEach(() => _resetEscapeStackForTest());

describe('GardenReviewPanel', () => {
  it('shows only the actions supplied by the captured item', () => {
    render(<GardenReviewPanel {...props(review(item({ actions: ['handover', 'park'], recommendation: 'handover' })))} />);

    expect(screen.getByRole('button', { name: 'Handover' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Park' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resume' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Harvest' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Wither' })).not.toBeInTheDocument();
  });

  it('keeps the seed growing from the decision column', async () => {
    const options = props();
    render(<GardenReviewPanel {...options} />);

    const actions = screen.getByRole('heading', { name: 'What should happen?' }).closest('section');
    expect(actions?.closest('.garden-review__decision')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Keep growing' }));

    await waitFor(() => expect(options.onKeepSeed).toHaveBeenCalledWith(
      's-review1', { reviewId: 'r-review1', evidenceVersion: 'evidence-1' },
    ));
  });

  it('harvests with the advisor guidance without asking for another reason', async () => {
    const options = props();
    render(<GardenReviewPanel {...options} />);

    fireEvent.click(screen.getByRole('button', { name: 'Harvest' }));

    expect(screen.queryByLabelText('What was completed?')).not.toBeInTheDocument();
    await waitFor(() => expect(options.onMoveSeed).toHaveBeenCalledWith(
      's-review1',
      'harvest',
      'The stated outcome looks complete and the verification is recorded.',
      undefined,
      undefined,
      { reviewId: 'r-review1', evidenceVersion: 'evidence-1' },
    ));
  });

  it('uses the completion statement when Harvest was not the advisor recommendation', async () => {
    const options = props(review(item({ recommendation: 'park', explanation: 'Keep this seed for later.' })));
    render(<GardenReviewPanel {...options} />);

    fireEvent.click(screen.getByRole('button', { name: 'Harvest' }));

    await waitFor(() => expect(options.onMoveSeed).toHaveBeenCalledWith(
      's-review1',
      'harvest',
      "The seed's stated outcome and required verification are complete.",
      undefined,
      undefined,
      { reviewId: 'r-review1', evidenceVersion: 'evidence-1' },
    ));
  });

  it.each([
    ['Park', 'Why are you parking this?'],
    ['Wither', 'Why should this be withered?'],
  ])('prefills the optional %s comment from matching advisor guidance', (action, label) => {
    const recommendation = action.toLowerCase() as 'park' | 'wither';
    const explanation = `${action} this seed because its saved context explains why.`;
    render(<GardenReviewPanel {...props(review(item({ recommendation, explanation })))} />);

    fireEvent.click(screen.getByRole('button', { name: action }));

    expect(screen.getByLabelText(new RegExp(label))).toHaveValue(explanation);
  });

  it('shows real advisor progress without making the user wait to act', () => {
    render(<GardenReviewPanel {...props(review(item({
      status: 'queued',
      recommendation: undefined,
      explanation: undefined,
      advisor_state: 'retrying',
      advisor_attempt: 2,
      advisor_max_attempts: 3,
      advisor_retry_at: '2099-08-30T09:02:00Z',
      advisor_error: 'The last answer did not match the expected format.',
    })))} />);

    expect(screen.getByText(/Attempt 2 of 3 did not produce usable advice/)).toBeInTheDocument();
    expect(screen.getByText('The last answer did not match the expected format.')).toBeInTheDocument();
    expect(screen.getByText('You can choose an action now. Advice will appear here if it arrives first.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Harvest' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Handover' })).toBeInTheDocument();
  });

  it('opens the reviewed seed log', async () => {
    const options = props();
    options.fetchSeedDocument = vi.fn().mockResolvedValue(document({
      notes: [{
        id: 'n-review1',
        seed_id: 's-review1',
        kind: 'note',
        body: 'Reviewed the **live profile**.',
        author_session: '',
        author_member: 'alder',
        created_at: '2026-08-30T09:01:00Z',
      }],
      notes_total: 1,
    }));
    render(<GardenReviewPanel {...options} />);

    expect(await screen.findByText('live profile', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.getByText('Log').closest('details')?.open).toBe(true);
  });

  it('walks up and down a plot and returns to the reviewed seed', async () => {
    const partOfPlot = [{ kind: 'part-of' as const, to: 's-plot11' }];
    const reviewed = seed({ edges: partOfPlot });
    const sibling = seed({
      id: 's-sibling',
      title: 'Check the sibling',
      body: 'Sibling context.',
      edges: partOfPlot,
    });
    const plot = seed({
      id: 's-plot11',
      title: 'Ship the review garden',
      body: 'The whole plot.',
      plot_progress: { total: 2, done: 0, withered: 0, growing: 2, dormant: 0, ready: 0, blocked: 0 },
    });
    const documents = new Map<string, SeedDocument>([
      [reviewed.id, document({ seed: reviewed })],
      [sibling.id, document({ seed: sibling })],
      [plot.id, document({ seed: plot, children: [reviewed, sibling] })],
    ]);
    const options = props();
    options.seeds = [reviewed, sibling, plot];
    options.fetchSeedDocument = vi.fn().mockImplementation(async (seedId: string) => documents.get(seedId)!);
    render(<GardenReviewPanel {...options} />);

    fireEvent.click(await screen.findByRole('button', { name: 'Ship the review garden' }));
    fireEvent.click(await screen.findByRole('button', { name: /Check the sibling/ }));
    expect(await screen.findByText('Sibling context.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'You are looking around the plot' }).closest('section'))
      .toHaveTextContent('The review decision still belongs to Finish the seed review.');

    fireEvent.click(screen.getByRole('button', { name: '‹ Review seed' }));
    expect(await screen.findByText('The implementation is complete. Verify the packaged app.')).toBeInTheDocument();
    expect(options.fetchSeedDocument).toHaveBeenCalledWith('s-review1');
  });

  it('keeps user edits when an advisory draft arrives late', async () => {
    let finishDraft: (value: string) => void = () => {};
    const options = props();
    options.onDraft = vi.fn().mockImplementation(() => new Promise<string>((resolve) => {
      finishDraft = resolve;
    }));
    render(<GardenReviewPanel {...options} />);

    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));
    fireEvent.click(screen.getByRole('button', { name: 'Draft' }));
    const handoff = screen.getByLabelText(/What should the new agent know/);
    fireEvent.change(handoff, { target: { value: 'My own handoff' } });
    await act(async () => finishDraft('Late generated handoff'));

    expect(handoff).toHaveValue('My own handoff');
    expect(screen.getByText('Draft ready. Your edits were kept.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Use draft' }));
    expect(handoff).toHaveValue('Late generated handoff');
  });

  it('sends optional guidance to Chief without asking for placement', async () => {
    const options = props(review(item({ actions: ['send_to_chief', 'park'] })));
    options.fetchSeedDocument = vi.fn().mockResolvedValue(document({
      seed: seed({
        continuation: {
          ...seed().continuation!,
          resume_available: false,
          handover_placement: 'placement_required',
          placement_reason: 'the old directory is unavailable',
        },
      }),
    }));
    render(<GardenReviewPanel {...options} />);

    fireEvent.click(screen.getByRole('button', { name: 'Send to Chief' }));
    const guidance = screen.getByLabelText(/What should Chief know/);
    expect(guidance).toHaveValue('');
    expect(screen.queryByRole('button', { name: 'Choose directory' })).not.toBeInTheDocument();
    fireEvent.change(guidance, { target: { value: 'Use feature/special.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send to Chief' }));

    await waitFor(() => expect(options.onSendSeedToChief).toHaveBeenCalledWith({
      seedId: 's-review1',
      expectedRev: 7,
      expectedTenderSession: '',
      expectedTenderMember: '',
      guidance: 'Use feature/special.',
      review: { reviewId: 'r-review1', evidenceVersion: 'evidence-1' },
    }));
  });

  it('keeps the handoff text when launch fails', async () => {
    const options = props();
    options.fetchSeedDocument = vi.fn().mockResolvedValue(document());
    options.onHandoverSeed = vi.fn().mockRejectedValue(new Error('Worker could not start'));
    render(<GardenReviewPanel {...options} />);

    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));
    const handoff = screen.getByLabelText(/What should the new agent know/);
    fireEvent.change(handoff, { target: { value: 'Keep this text' } });
    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));

    expect(await screen.findByText('Worker could not start')).toBeInTheDocument();
    expect(handoff).toHaveValue('Keep this text');
    expect(options.onHandoverSeed).toHaveBeenCalledWith(expect.objectContaining({
      seedId: 's-review1',
      expectedRev: 7,
      handoff: 'Keep this text',
      review: { reviewId: 'r-review1', evidenceVersion: 'evidence-1' },
    }));
  });

  it('sends the review receipt and optional Park comment together', async () => {
    const options = props();
    render(<GardenReviewPanel {...options} />);

    fireEvent.click(screen.getByRole('button', { name: 'Park' }));
    fireEvent.change(screen.getByLabelText(/Why are you parking this/), { target: { value: 'Waiting for product input.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Park' }));

    await waitFor(() => expect(options.onMoveSeed).toHaveBeenCalledWith(
      's-review1',
      'park',
      undefined,
      undefined,
      'Waiting for product input.',
      { reviewId: 'r-review1', evidenceVersion: 'evidence-1' },
    ));
  });

  it('offers retry for changed advice without showing lifecycle actions', async () => {
    const options = props(review(item({
      status: 'invalidated',
      recommendation: undefined,
      explanation: undefined,
      error: 'The seed changed during classification.',
    })));
    render(<GardenReviewPanel {...options} />);

    expect(screen.getByText('This seed changed')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Harvest' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(options.onRetry).toHaveBeenCalledWith('r-review1', 's-review1'));
  });

  it('closes the composer before leaving the review on Escape', () => {
    const options = props();
    render(<GardenReviewPanel {...options} />);
    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByLabelText(/What should the new agent know/)).not.toBeInTheDocument();
    expect(options.onExit).not.toHaveBeenCalled();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(options.onExit).toHaveBeenCalledOnce();
  });
});
