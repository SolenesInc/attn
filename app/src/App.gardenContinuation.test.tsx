import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, daemonSeed, daemonSession, seedDocument } from './test/daemonFixtures';
import type { CommandMessage, EventMessage } from './test/protocol';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';

type CrewMember = EventMessage<'crew_updated'>['members'][number];

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

const KEEL: CrewMember = {
  id: 'keel',
  charter_path: '/homes/keel/CHARTER.md',
  home_dir: '/homes/keel',
  awareness_dirs: [],
  resolved_agent: 'claude',
  revision: 1,
};

async function openSeedInGarden() {
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('tender', { state: 'idle' })],
      workspaces: [agentWorkspace('s1'), agentWorkspace('tender')],
      seeds: [PARSER],
    },
  });
  view.daemon.on('seed_document_get', () => ({
    event: 'seed_document_get_result',
    success: true,
    document: seedDocument(PARSER),
  }));
  fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
  await gesture(view.daemon, () => pressShortcut('board.open'));
  await gesture(view.daemon, () => fireEvent.click(document.querySelector('[data-seed-row="s-1"]')!));
  return view;
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
  await gesture(daemon, () => fireEvent.click(screen.getByTestId('seed-handover-s-1')));
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

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('seed-resume-s-1')));

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

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('seed-resume-s-1')));

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
});
