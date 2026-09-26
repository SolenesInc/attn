import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { EventMessage } from './test/protocol';
import { renderApp } from './test/renderApp';

type Definition = NonNullable<EventMessage<'automation_definitions_result'>['definitions']>[number];

const REVIEWER: Definition = {
  id: 'd1',
  name: 'PR reviewer',
  enabled: true,
  revision: 1,
  trigger_type: 'manual',
  updated_at: '2026-01-01T00:00:00Z',
};

async function renderAutomations() {
  const view = await renderApp();
  view.daemon.on('automation_definitions_get', () => ({ event: 'automation_definitions_result', success: true, definitions: [REVIEWER] }));
  view.daemon.on('automation_runs_get', ({ definition_id }) => ({ event: 'automation_runs_result', success: true, definition_id, runs: [] }));
  fireEvent.click(screen.getByRole('button', { name: 'Show Automations' }));
  await view.daemon.idle();
  return view;
}

describe('App automation actions', () => {
  it('lists only the definitions answering its own request', async () => {
    const { daemon } = await renderApp();
    fireEvent.click(screen.getByRole('button', { name: 'Show Automations' }));
    const request = await daemon.received('automation_definitions_get');
    expect(request.request_id).toMatch(/^automation_definitions_get:/);

    daemon.emit({
      event: 'automation_definitions_result',
      request_id: 'another-request',
      success: true,
      definitions: [{ ...REVIEWER, id: 'wrong', name: 'Not asked for' }],
    });
    await daemon.idle();
    expect(screen.queryByText('Not asked for')).toBeNull();

    daemon.emit({ event: 'automation_definitions_result', request_id: request.request_id, success: true, definitions: [REVIEWER] });
    await daemon.idle();
    expect(screen.getByText('PR reviewer')).toBeInTheDocument();
    expect(screen.queryByText('Not asked for')).toBeNull();
  });

  it('shows the daemon’s reason when it refuses to disable an automation', async () => {
    const { daemon } = await renderAutomations();
    daemon.on('automation_set_enabled', () => ({
      event: 'automation_set_enabled_result',
      success: false,
      error: 'automation definition is disabled elsewhere',
    }));

    fireEvent.click(screen.getByTestId('automation-toggle-d1'));
    await daemon.idle();

    expect(daemon.sentOf('automation_set_enabled')).toEqual([
      expect.objectContaining({ definition_id: 'd1', enabled: false }),
    ]);
    expect(screen.getByTestId('automation-toggle-error-d1')).toHaveTextContent('automation definition is disabled elsewhere');
  });

  it('runs a manual automation and is ready to run it again once the daemon answers', async () => {
    const { daemon } = await renderAutomations();
    daemon.on('automation_run', ({ definition_id }) => ({
      event: 'automation_run_result',
      success: true,
      run: {
        id: 'run-1',
        definition_id,
        state: 'delivered',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      },
    }));

    fireEvent.click(screen.getByTestId('automation-run-now-d1'));
    await daemon.idle();

    expect(daemon.sentOf('automation_run')).toEqual([
      expect.objectContaining({ definition_id: 'd1', request_id: expect.any(String) }),
    ]);
    expect(screen.getByTestId('automation-run-now-d1')).toBeEnabled();
    expect(screen.queryByTestId('automation-run-error-d1')).toBeNull();
  });

  it('says a run is still in flight when the daemon has not answered within thirty seconds', async () => {
    const { daemon } = await renderAutomations();

    fireEvent.click(screen.getByTestId('automation-run-now-d1'));
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(30_000));

    expect(screen.getByTestId('automation-run-error-d1')).toHaveTextContent(
      'Run request is still in flight — it will appear in run history; clicking again retries the same run.',
    );
  });
});
