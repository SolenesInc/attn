import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { EventMessage } from './test/protocol';
import { soloDesktop, daemonSession } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

type WorkflowRun = EventMessage<'workflow_run_updated'>['run'];
type Call = NonNullable<WorkflowRun['agent_calls']>[number];

function workflowRun(status: WorkflowRun['status'], over: Partial<WorkflowRun> = {}): WorkflowRun {
  return {
    run_id: 'wr1',
    session_id: 's1',
    status,
    script_path: '/x/review.js',
    script_hash: 'hash',
    resumable: false,
    created_at: '2026-04-08T00:00:00Z',
    updated_at: '2026-04-08T00:00:00Z',
    ...over,
  };
}

const call = (ordinal: string, status: Call['status'], over: Partial<Call> = {}): Call => ({ ordinal, run_id: 'wr1', status, ...over });

async function openWorkflowRuns(listed: WorkflowRun[], hydrated: Record<string, Call[]> = {}) {
  const { daemon } = await renderApp({
    initialState: { sessions: [daemonSession('s1'), daemonSession('s2')], desktops: [soloDesktop('s1'), soloDesktop('s2')] },
  });
  daemon.on('workflow_run_list', () => ({ event: 'workflow_action_result', action: 'list', success: true, runs: listed }));
  daemon.on('workflow_run_get', ({ run_id }) => ({
    event: 'workflow_action_result',
    action: 'get',
    run_id,
    success: true,
    run: { ...listed.find((run) => run.run_id === run_id)!, agent_calls: hydrated[run_id] ?? [] },
  }));
  fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
  await daemon.idle();
  fireEvent.click(screen.getByRole('button', { name: 'Workflow Runs' }));
  await daemon.idle();
  return daemon;
}

const view = () => within(screen.getByTestId('workflow-run-view'));

describe('App workflow runs', () => {
  it('shows the session run the daemon lists, hydrated with its agent calls', async () => {
    const daemon = await openWorkflowRuns([workflowRun('running')], { wr1: [call('1', 'ok', { label: 'design step' })] });

    expect(daemon.sentOf('workflow_run_list')).toEqual([expect.objectContaining({ session_id: 's1' })]);
    expect(daemon.sentOf('workflow_run_get')).toEqual([expect.objectContaining({ run_id: 'wr1' })]);
    expect(view().getByText('review.js')).toBeInTheDocument();
    expect(view().getByText('Running')).toBeInTheDocument();
    expect(view().getByTestId('workflow-call-1')).toHaveTextContent('design step');

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(view().queryByRole('button', { name: 'Hide' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Workflow Runs' })).not.toHaveClass('active');
  });

  it('shows the latest run of the open session, never another session’s', async () => {
    const daemon = await openWorkflowRuns([
      workflowRun('completed', { run_id: 'wr1', script_path: '/x/first.js', created_at: '2026-01-01T00:00:00Z' }),
      workflowRun('running', { run_id: 'wr2', script_path: '/x/latest.js', created_at: '2026-01-03T00:00:00Z' }),
      workflowRun('completed', { run_id: 'wr3', script_path: '/x/middle.js', created_at: '2026-01-02T00:00:00Z' }),
      workflowRun('running', { run_id: 'other', session_id: 's2', script_path: '/x/other.js', created_at: '2026-12-31T00:00:00Z' }),
    ]);

    expect(view().getByText('latest.js')).toBeInTheDocument();
    expect(daemon.sentOf('workflow_run_get').map(({ run_id }) => run_id)).toEqual(['wr2']);
  });

  it('follows the run as the daemon pushes its progress, without asking for calls it already has', async () => {
    const daemon = await openWorkflowRuns([]);
    expect(view().getByText('No workflow run selected.')).toBeInTheDocument();

    daemon.emit({ event: 'workflow_run_updated', run: { ...workflowRun('completed'), agent_calls: [call('1', 'ok')] } });
    await daemon.idle();

    expect(view().getByText('Completed')).toBeInTheDocument();
    expect(daemon.sentOf('workflow_run_get')).toEqual([]);
  });

  it('shows the phase, the progress, and the call in flight as the current step', async () => {
    await openWorkflowRuns([workflowRun('running', { phase: 'review' })], {
      wr1: [
        call('1', 'ok', { label: 'plan' }),
        call('2', 'running', { label: 'review changes', phase: 'review', resolved_model: 'gpt-5-codex', started_at: new Date(Date.now() - 65_000).toISOString() }),
      ],
    });

    expect(view().getByText('phase: review')).toBeInTheDocument();
    expect(view().getByText('1/2 calls')).toBeInTheDocument();
    const current = view().getByTestId('workflow-current-step');
    expect(current).toHaveTextContent('review changes');
    expect(current).toHaveTextContent('gpt-5-codex');
    expect(current.textContent).toMatch(/1:0\d/);
    expect(view().getByTestId('workflow-call-2')).toHaveAttribute('data-running', 'true');
  });

  it('shows no current step once nothing is running', async () => {
    await openWorkflowRuns([workflowRun('completed')], { wr1: [call('1', 'ok', { label: 'plan' })] });

    expect(view().queryByTestId('workflow-current-step')).toBeNull();
  });

  it.each([
    ['why a failed run failed', workflowRun('failed', { last_error: 'boom: step 2 exploded' }), 'boom: step 2 exploded'],
    ['what a completed run returned', workflowRun('completed', { result_json: '{"ok":true,"summary":"all green"}' }), '{"ok":true,"summary":"all green"}'],
  ])('shows %s', async (_, run, text) => {
    await openWorkflowRuns([run]);

    expect(view().getByText(text)).toBeInTheDocument();
  });

  it('only watches the run: its one control hides the panel', async () => {
    const daemon = await openWorkflowRuns([workflowRun('running')], { wr1: [call('1', 'running', { label: 'step' })] });

    expect(view().getAllByRole('button').map((button) => button.textContent)).toEqual(['Hide']);
    expect(view().queryByRole('textbox')).toBeNull();
    expect(view().queryByRole('combobox')).toBeNull();

    await gesture(daemon, () => fireEvent.click(view().getByRole('button', { name: 'Hide' })));

    expect(screen.queryByRole('button', { name: 'Hide' })).toBeNull();
    expect(screen.getByTestId('workflow-run-view').closest('[aria-hidden="true"]')).not.toBeNull();
    expect(daemon.sentOf('workflow_run_cancel')).toEqual([]);
  });
});
