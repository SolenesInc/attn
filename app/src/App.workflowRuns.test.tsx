import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { EventMessage } from './test/protocol';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

type WorkflowRun = EventMessage<'workflow_run_updated'>['run'];

function workflowRun(status: WorkflowRun['status']): WorkflowRun {
  return {
    run_id: 'wr1',
    session_id: 's1',
    status,
    script_path: '/x/review.js',
    script_hash: 'hash',
    resumable: false,
    created_at: '2026-04-08T00:00:00Z',
    updated_at: '2026-04-08T00:00:00Z',
  };
}

async function openWorkflowRuns(listed: WorkflowRun[]) {
  const { daemon } = await renderApp({
    initialState: { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] },
  });
  daemon.on('workflow_run_list', () => ({ event: 'workflow_action_result', action: 'list', success: true, runs: listed }));
  daemon.on('workflow_run_get', ({ run_id }) => ({
    event: 'workflow_action_result', action: 'get', run_id, success: true, run: { ...listed[0], agent_calls: [] },
  }));
  fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
  await daemon.idle();
  fireEvent.click(screen.getByRole('button', { name: 'Workflow Runs' }));
  await daemon.idle();
  return daemon;
}

describe('App workflow runs', () => {
  it('shows the session run the daemon lists, hydrated with its agent calls', async () => {
    const daemon = await openWorkflowRuns([workflowRun('running')]);

    expect(daemon.sentOf('workflow_run_list')).toEqual([expect.objectContaining({ session_id: 's1' })]);
    expect(daemon.sentOf('workflow_run_get')).toEqual([expect.objectContaining({ run_id: 'wr1' })]);
    expect(screen.getByText('review.js')).toBeInTheDocument();
    expect(screen.getByText('Running')).toBeInTheDocument();
  });

  it('follows the run as the daemon pushes its progress', async () => {
    const daemon = await openWorkflowRuns([]);
    expect(screen.getByText('No workflow run selected.')).toBeInTheDocument();

    daemon.emit({ event: 'workflow_run_updated', run: { ...workflowRun('completed'), agent_calls: [] } });

    expect(screen.getByText('Completed')).toBeInTheDocument();
  });
});
