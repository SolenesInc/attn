import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { EventMessage } from './test/protocol';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

type Definition = NonNullable<EventMessage<'automation_definitions_result'>['definitions']>[number];

function definition(id: string, name: string): Definition {
  return { id, name, enabled: true, revision: 1, trigger_type: 'manual', updated_at: '2026-01-01T00:00:00Z' };
}

describe('App automations panel', () => {
  it('lists the definitions again when the daemon says automations changed', async () => {
    const { daemon } = await renderApp({
      initialState: { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] },
    });
    let definitions = [definition('d1', 'PR reviewer')];
    daemon.on('automation_definitions_get', () => ({ event: 'automation_definitions_result', success: true, definitions }));
    daemon.on('automation_runs_get', ({ definition_id }) => ({ event: 'automation_runs_result', success: true, definition_id, runs: [] }));

    fireEvent.click(screen.getByRole('button', { name: 'Show Automations' }));
    await daemon.idle();
    expect(screen.getByText('PR reviewer')).toBeInTheDocument();
    const listed = daemon.sentOf('automation_definitions_get').length;

    definitions = [...definitions, definition('d2', 'Nightly sweep')];
    daemon.emit({ event: 'automations_changed', definition_ids: ['d2'] });
    await daemon.idle();

    expect(daemon.sentOf('automation_definitions_get')).toHaveLength(listed + 1);
    expect(screen.getByText('Nightly sweep')).toBeInTheDocument();
  });
});
