import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, crewMember, daemonSeed, daemonSession } from './test/daemonFixtures';
import { openRow, renderGarden } from './test/garden';
import type { CommandMessage } from './test/protocol';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';

const PARSER = daemonSeed('s-1', {
  title: 'fix the parser',
  status: 'tending',
  tender_session: 'tender',
  continuation: {
    agent: 'claude',
    cwd: '/tmp/repo',
    repository_root: '/tmp/repo',
    branch: 'feature',
    directory_state: 'present',
    execution_id: 'execution-1',
    handover_placement: 'reuse_cwd',
    host_kind: 'local',
    resume_available: true,
    session_live: false,
    source: 'tender',
  },
});

const KEEL = crewMember('keel');

type Seed = typeof PARSER;

async function openSeedInGarden(seed: Seed = PARSER, { chief = false } = {}) {
  const sessions = [daemonSession('s1', { state: 'idle' }), daemonSession('tender', { state: 'idle' })];
  if (chief) sessions.push(daemonSession('chief', { chief_of_staff: true }));
  const garden = await renderGarden([seed], { sessions });
  const { daemon } = garden;
  let heldFrom = 0;
  fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
  await gesture(daemon, () => pressShortcut('board.open'));
  await openRow(daemon, seed.title);
  return {
    daemon,
    holdReads() {
      heldFrom = daemon.sentOf('seed_document_get').length;
      daemon.on('seed_document_get', () => undefined);
    },
    push(next: Seed) {
      garden.push([next]);
    },
    async answerHeldReads() {
      daemon.on('seed_document_get', (read) => garden.answer(read));
      await gesture(daemon, () => {
        for (const read of daemon.sentOf('seed_document_get').slice(heldFrom)) daemon.replyTo(read, garden.answer(read));
      });
    },
  };
}

function continued(overrides: Partial<NonNullable<Seed['continuation']>>): Seed {
  return { ...PARSER, continuation: { ...PARSER.continuation!, ...overrides } };
}

const handedOver = ({ cwd }: CommandMessage<'delegate'>): Reply => ({
  event: 'delegate_result',
  success: true,
  result: {
    session_id: 'tender',
    workspace_id: 'workspace-tender',
    directory: cwd,
    agent: 'claude',
    checkout: 'none',
    effort: 'high',
    model: 'opus',
    seed_id: 's-1',
  },
});

async function composeHandover(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Handover' })));
  const note = screen.getByLabelText(/What should the new agent know\?/);
  fireEvent.change(note, { target: { value: 'Continue from the parser tests.' } });
  fireEvent.change(screen.getByLabelText('Working folder'), { target: { value: '/tmp/placed' } });
  return note.closest('form')!;
}

function selectedSessions(daemon: ScriptedDaemon) {
  return daemon.sentOf('session_selected').map((command) => command.id);
}

describe('App garden continuation', () => {
  it('resumes a seed’s agent and goes to the session the daemon reopened', async () => {
    const { daemon } = await openSeedInGarden();
    daemon.on('seed_resume', () => ({
      event: 'seed_resume_result',
      success: true,
      session_id: 'tender',
      workspace_id: 'workspace-tender',
    }));

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Resume' })));

    expect(daemon.sentOf('seed_resume')).toEqual([expect.objectContaining({ seed_id: 's-1' })]);
    expect(selectedSessions(daemon).pop()).toBe('tender');
  });

  it('says why the daemon could not resume a seed’s agent', async () => {
    const { daemon } = await openSeedInGarden();
    daemon.on('seed_resume', () => ({
      event: 'seed_resume_result',
      success: false,
      error: 'seed has no agent session to reopen',
    }));

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Resume' })));

    expect(screen.getByText('seed has no agent session to reopen')).toBeInTheDocument();
    expect(selectedSessions(daemon)).not.toContain('tender');
  });

  it('hands a seed over to a new agent with the user’s note and goes to it', async () => {
    const { daemon } = await openSeedInGarden();
    daemon.on('delegate', handedOver);

    const form = await composeHandover(daemon);
    await gesture(daemon, () => fireEvent.submit(form));

    expect(daemon.sentOf('delegate')).toEqual([expect.objectContaining({
      source_session_id: 's1',
      assignment: { kind: 'seed', seed_id: 's-1', handover: { note: 'Continue from the parser tests.' } },
      cwd: '/tmp/placed',
      checkout: { kind: 'reuse', branch: 'feature' },
    })]);
    expect(selectedSessions(daemon).pop()).toBe('tender');
  });

  it('retries a timed-out handover under the same request, so the daemon can tell it from a second handover', async () => {
    const { daemon } = await openSeedInGarden();
    daemon.on('delegate', (command) => (daemon.sentOf('delegate').length > 1 ? handedOver(command) : undefined));

    const form = await composeHandover(daemon);
    await gesture(daemon, () => fireEvent.submit(form));
    await act(() => vi.advanceTimersByTimeAsync(120_000));
    await daemon.idle();
    expect(screen.getByText('Handover timed out')).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.submit(form));

    const [first, retry] = daemon.sentOf('delegate');
    expect(retry.request_id).toBe(first.request_id);
    expect(selectedSessions(daemon).pop()).toBe('tender');
  });

  it('opens a seed as a tile beside the session the user is in', async () => {
    const { daemon } = await openSeedInGarden();

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open as tile' })));

    const [open] = daemon.sentOf('open_seed');
    expect(open).toMatchObject({ seed_id: 's-1', session_id: 's1' });
    expect(open).not.toHaveProperty('standalone');
  });

  it('opens a crew member’s seed as a standalone reader when no session is bound to it', async () => {
    const { daemon } = await renderApp({
      initialState: {
        crew: [KEEL],
        sessions: [daemonSession('s1')],
        workspaces: [agentWorkspace('s1')],
        seeds: [daemonSeed('s-7k3f9m', { title: 'crew seed', status: 'planted', planter_member: 'keel' })],
      },
    });
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('manage-crew')));
    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Seeds' }));
    fireEvent.click(screen.getByRole('button', { name: /Planted/ }));

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /crew seed/ })));

    const [open] = daemon.sentOf('open_seed');
    expect(open).toMatchObject({ seed_id: 's-7k3f9m', standalone: true });
    expect(open).not.toHaveProperty('session_id');
  });

  it('offers Resume only when the daemon says the agent’s conversation is still there', async () => {
    await openSeedInGarden(continued({ resume_available: false, resume_reason: 'the original conversation is no longer available' }));

    expect(screen.getByRole('button', { name: 'Handover' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
  });

  it('waits for the seed’s current revision before offering Handover', async () => {
    const view = await openSeedInGarden();
    view.daemon.on('delegate', handedOver);
    expect(screen.getByRole('button', { name: 'Handover' })).toBeInTheDocument();

    view.holdReads();
    await gesture(view.daemon, () => view.push({ ...PARSER, rev: 2 }));
    expect(screen.queryByRole('button', { name: 'Handover' })).toBeNull();

    await view.answerHeldReads();
    const form = await composeHandover(view.daemon);
    await gesture(view.daemon, () => fireEvent.submit(form));

    expect(view.daemon.sentOf('delegate')).toEqual([expect.objectContaining({ cwd: '/tmp/placed' })]);
  });

  it('places a handover where the user says when the saved folder cannot be reused, and sends to Chief separately', async () => {
    const { daemon } = await openSeedInGarden(
      continued({ resume_available: false, handover_placement: 'placement_required', placement_reason: 'the old directory is unavailable', branch: 'old-branch' }),
      { chief: true },
    );
    daemon.on('delegate', handedOver);
    daemon.on('seed_send_to_chief', () => ({
      event: 'seed_send_to_chief_result',
      success: true,
      result: { seed: PARSER, chief_session_id: 'chief', delivery_status: 'queued', detail: 'queued for Chief' },
    }));

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Handover' })));
    expect(screen.getByLabelText('Working folder')).toHaveValue('');
    expect(screen.getByLabelText('Git checkout')).toHaveValue('none');
    fireEvent.change(screen.getByLabelText('Working folder'), { target: { value: '/tmp/new-home' } });
    fireEvent.change(screen.getByLabelText('Git checkout'), { target: { value: 'new_worktree' } });
    fireEvent.change(screen.getByLabelText('Branch'), { target: { value: 'feature/new-home' } });
    fireEvent.change(screen.getByLabelText('Start from'), { target: { value: 'origin/next' } });
    fireEvent.click(screen.getByLabelText('Allow sharing an occupied checkout'));
    await gesture(daemon, () => fireEvent.submit(screen.getByLabelText('Working folder').closest('form')!));

    expect(daemon.sentOf('delegate')).toEqual([expect.objectContaining({
      assignment: { kind: 'seed', seed_id: 's-1', handover: {} },
      cwd: '/tmp/new-home',
      checkout: { kind: 'new_worktree', branch: 'feature/new-home', from: 'origin/next' },
      allow_worktree_reuse: true,
      agent: 'claude',
    })]);

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Send to Chief' })));
    const guidance = screen.getByLabelText(/What should Chief know/);
    expect(guidance).toHaveValue('');
    fireEvent.change(guidance, { target: { value: 'Use feature/special in /tmp/new-home.' } });
    await gesture(daemon, () => fireEvent.submit(guidance.closest('form')!));

    expect(daemon.sentOf('seed_send_to_chief')).toEqual([expect.objectContaining({
      seed_id: 's-1',
      expected_rev: 1,
      expected_tender_session: 'tender',
      expected_tender_member: '',
      guidance: 'Use feature/special in /tmp/new-home.',
    })]);
  });

  it('recreates a missing branch checkout from its repository context', async () => {
    await openSeedInGarden(continued({
      cwd: '/tmp/missing-worktree/subdir',
      directory_state: 'missing',
      handover_placement: 'recreate_branch',
      repository_root: '/tmp/repo',
      repository_subdir: 'subdir',
      branch: 'feature/recreate',
    }));

    fireEvent.click(screen.getByRole('button', { name: 'Handover' }));

    expect(screen.getByLabelText('Working folder')).toHaveValue('/tmp/repo/subdir');
    expect(screen.getByLabelText('Git checkout')).toHaveValue('existing_branch_worktree');
    expect(screen.getByLabelText('Branch')).toHaveValue('feature/recreate');
  });
});
