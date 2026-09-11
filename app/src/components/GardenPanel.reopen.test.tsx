import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import type { Seed, SeedDocument } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  const now = new Date().toISOString();
  return {
    body: '',
    status: 'growing',
    state_changed_at: now,
    state_changed_at_exact: true,
    step_slug: overrides.title,
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    ready: false,
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    created_at: now,
    updated_at: now,
    ...overrides,
  };
}

function document(value: Seed, saved: NonNullable<Seed['continuation']>): SeedDocument {
  return {
    seed: { ...value, continuation: saved },
    tender_holds: false,
    children: [],
    notes: [],
    notes_total: 0,
    artifacts: [],
    references: [],
  };
}

function continuation(overrides: Partial<NonNullable<Seed['continuation']>> = {}): NonNullable<Seed['continuation']> {
  return {
    execution_id: 'sess-a',
    source: 'execution',
    session_live: false,
    native_conversation_id: 'native-a',
    agent: 'codex',
    cwd: '/tmp/work',
    host_kind: 'local',
    directory_state: 'present',
    resume_available: true,
    handover_placement: 'reuse_cwd',
    ...overrides,
  };
}

describe('GardenPanel continuation actions', () => {
  it('shows Resume only after the detailed read says the exact conversation is available', async () => {
    const value = seed({ id: 's-tend11', title: 'held work', tender_session: 'sess-a' });
    const onResumeSeed = vi.fn();
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[value]}
        fetchSeedDocument={vi.fn().mockResolvedValue(document(value, continuation()))}
        onResumeSeed={onResumeSeed}
      />,
    );

    fireEvent.click(screen.getByText('held work'));
    fireEvent.click(await screen.findByTestId('seed-resume-s-tend11'));

    expect(onResumeSeed).toHaveBeenCalledWith('s-tend11');
  });

  it('does not infer Resume from a tender or old resume fields', async () => {
    const value = seed({
      id: 's-missing11',
      title: 'missing conversation',
      tender_session: 'sess-a',
      resume_session_id: 'native-a',
    });
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[value]}
        fetchSeedDocument={vi.fn().mockResolvedValue(document(value, continuation({
          resume_available: false,
          resume_reason: 'the original conversation is no longer available',
        })))}
        onResumeSeed={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByText('missing conversation'));
    await waitFor(() => expect(screen.queryByTestId('seed-resume-s-missing11')).not.toBeInTheDocument());
  });

  it('starts Handover blank and sends the reviewed seed receipt', async () => {
    const value = seed({ id: 's-hand11', title: 'pass this on', tender_session: 'sess-a', rev: 7 });
    const onHandoverSeed = vi.fn().mockResolvedValue({ session_id: 'sess-b' });
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[value]}
        fetchSeedDocument={vi.fn().mockResolvedValue(document(value, continuation()))}
        onHandoverSeed={onHandoverSeed}
      />,
    );

    fireEvent.click(screen.getByText('pass this on'));
    fireEvent.click(await screen.findByTestId('seed-handover-s-hand11'));
    const handoff = screen.getByLabelText(/What should the new agent know/);
    expect(handoff).toHaveValue('');
    fireEvent.change(handoff, { target: { value: 'The parser is done; verify the renderer.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));

    await waitFor(() => expect(onHandoverSeed).toHaveBeenCalledWith({
      seedId: 's-hand11',
      requestId: expect.any(String),
      cwd: '/tmp/work',
      checkout: undefined,
      agent: 'codex',
      handoff: 'The parser is done; verify the renderer.',
    }));
  });

  it('waits for the current seed receipt before offering Handover', async () => {
    const original = seed({ id: 's-fresh11', title: 'fresh handover', tender_session: 'sess-a', rev: 1 });
    const current = { ...original, rev: 2 };
    let resolveCurrent: (value: SeedDocument) => void = () => {};
    const fetchSeedDocument = vi.fn()
      .mockResolvedValueOnce(document(original, continuation()))
      .mockImplementationOnce(() => new Promise<SeedDocument>((resolve) => {
        resolveCurrent = resolve;
      }));
    const onHandoverSeed = vi.fn().mockResolvedValue({ session_id: 'sess-b' });
    const { rerender } = render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[original]}
        fetchSeedDocument={fetchSeedDocument}
        onHandoverSeed={onHandoverSeed}
      />,
    );

    fireEvent.click(screen.getByText('fresh handover'));
    await screen.findByTestId('seed-handover-s-fresh11');
    rerender(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[current]}
        fetchSeedDocument={fetchSeedDocument}
        onHandoverSeed={onHandoverSeed}
      />,
    );

    await waitFor(() => expect(fetchSeedDocument).toHaveBeenCalledTimes(2));
    expect(screen.queryByTestId('seed-handover-s-fresh11')).not.toBeInTheDocument();
    await act(async () => resolveCurrent(document(current, continuation())));
    fireEvent.click(await screen.findByTestId('seed-handover-s-fresh11'));
    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));

    await waitFor(() => expect(onHandoverSeed).toHaveBeenCalledWith(expect.objectContaining({
      seedId: 's-fresh11',
      cwd: '/tmp/work',
    })));
  });

  it('reuses the durable request identity after a handover timeout', async () => {
    const value = seed({ id: 's-timeout11', title: 'retry this handover', tender_session: 'sess-a' });
    const onHandoverSeed = vi.fn()
      .mockRejectedValueOnce(new Error('Handover timed out'))
      .mockResolvedValueOnce({ session_id: 'sess-b' });
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[value]}
        fetchSeedDocument={vi.fn().mockResolvedValue(document(value, continuation()))}
        onHandoverSeed={onHandoverSeed}
      />,
    );

    fireEvent.click(screen.getByText('retry this handover'));
    fireEvent.click(await screen.findByTestId('seed-handover-s-timeout11'));
    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Handover timed out');
    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));

    await waitFor(() => expect(onHandoverSeed).toHaveBeenCalledTimes(2));
    expect(onHandoverSeed.mock.calls[1][0].requestId).toBe(onHandoverSeed.mock.calls[0][0].requestId);
  });

  it('offers manual handover placement and Chief as separate paths', async () => {
    const value = seed({ id: 's-place11', title: 'place this handover', tender_session: 'sess-a' });
    const onHandoverSeed = vi.fn().mockResolvedValue({ session_id: 'sess-b' });
    const onSendSeedToChief = vi.fn().mockResolvedValue({ chief_session_id: 'chief' });
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[value]}
        fetchSeedDocument={vi.fn().mockResolvedValue(document(value, continuation({
          resume_available: false,
          handover_placement: 'placement_required',
          placement_reason: 'the old directory is unavailable',
          repository_root: '/tmp/repo',
          branch: 'old-branch',
        })))}
        onHandoverSeed={onHandoverSeed}
        onSendSeedToChief={onSendSeedToChief}
        chiefAvailable
      />,
    );

    fireEvent.click(screen.getByText('place this handover'));
    fireEvent.click(await screen.findByTestId('seed-handover-s-place11'));
    fireEvent.change(screen.getByLabelText('Working folder'), { target: { value: '/tmp/new-home' } });
    fireEvent.change(screen.getByLabelText('Git checkout'), { target: { value: 'new_worktree' } });
    fireEvent.change(screen.getByLabelText('Branch'), { target: { value: 'feature/new-home' } });
    fireEvent.change(screen.getByLabelText('Start from'), { target: { value: 'origin/next' } });
    fireEvent.click(screen.getByLabelText('Allow sharing an occupied checkout'));
    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));

    await waitFor(() => expect(onHandoverSeed).toHaveBeenCalledWith({
      seedId: 's-place11',
      requestId: expect.any(String),
      cwd: '/tmp/new-home',
      checkout: { kind: 'new_worktree', branch: 'feature/new-home', from: 'origin/next' },
      allowWorktreeReuse: true,
      agent: 'codex',
      handoff: '',
    }));

    fireEvent.click(await screen.findByTestId('seed-send-to-chief-s-place11'));
    const guidance = screen.getByLabelText(/What should Chief know/);
    expect(guidance).toHaveValue('');
    fireEvent.change(guidance, { target: { value: 'Use feature/special in /tmp/new-home.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send to Chief' }));

    await waitFor(() => expect(onSendSeedToChief).toHaveBeenCalledWith({
      seedId: 's-place11',
      expectedRev: 1,
      expectedTenderSession: 'sess-a',
      expectedTenderMember: '',
      guidance: 'Use feature/special in /tmp/new-home.',
    }));
  });

  it('recreates a missing branch checkout from its repository context', async () => {
    const value = seed({ id: 's-recreate11', title: 'recreate this checkout', tender_session: 'sess-a' });
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[value]}
        fetchSeedDocument={vi.fn().mockResolvedValue(document(value, continuation({
          cwd: '/tmp/missing-worktree/subdir',
          directory_state: 'missing',
          handover_placement: 'recreate_branch',
          repository_root: '/tmp/repo',
          repository_subdir: 'subdir',
          branch: 'feature/recreate',
        })))}
        onHandoverSeed={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByText('recreate this checkout'));
    fireEvent.click(await screen.findByTestId('seed-handover-s-recreate11'));

    expect(screen.getByLabelText('Working folder')).toHaveValue('/tmp/repo/subdir');
    expect(screen.getByLabelText('Git checkout')).toHaveValue('existing_branch_worktree');
    expect(screen.getByLabelText('Branch')).toHaveValue('feature/recreate');
  });
});
