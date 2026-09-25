import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { EventMessage } from './test/protocol';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';

type Seed = EventMessage<'garden_seeds_updated'>['seeds'][number];

function seed(id: string, title: string): Seed {
  return {
    id,
    title,
    body: '',
    status: 'planted',
    step_slug: title,
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    ready: false,
    state_changed_at: '2026-08-12T10:00:00Z',
    state_changed_at_exact: true,
    created_at: '2026-08-12T10:00:00Z',
    updated_at: '2026-08-12T10:00:00Z',
  };
}

const layout = { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] };

async function openGarden(seeds: Seed[]) {
  const view = await renderApp({ initialState: { ...layout, seeds } });
  fireEvent.keyDown(window, { key: 'T', metaKey: true, shiftKey: true });
  await view.daemon.idle();
  return view.daemon;
}


describe('App garden seeds', () => {
  it('seeds the garden from initial_state', async () => {
    await openGarden([seed('s-aaa111', 'already planted')]);

    expect(screen.getByText('already planted')).toBeInTheDocument();
  });

  it('replaces the garden on every planting broadcast', async () => {
    const daemon = await openGarden([seed('s-aaa111', 'already planted')]);

    daemon.emit({
      event: 'garden_seeds_updated',
      seeds: [seed('s-bbb222', 'just planted'), seed('s-aaa111', 'already planted')],
      total: 2,
    });

    expect(screen.getByText('just planted')).toBeInTheDocument();
    expect(screen.getByText('already planted')).toBeInTheDocument();
  });

  it('reads a garden-less daemon as an empty garden', async () => {
    const daemon = await openGarden([seed('s-aaa111', 'already planted')]);
    expect(screen.getByText('already planted')).toBeInTheDocument();

    daemon.on('client_hello', () => initialState(layout));
    await daemon.reconnect();

    expect(screen.queryByText('already planted')).toBeNull();
  });

  it('carries how many seeds the garden holds, not just the ones it sent', async () => {
    const daemon = await openGarden([]);

    daemon.emit({ event: 'garden_seeds_updated', seeds: [seed('s-bbb222', 'the newest one')], total: 1421 });

    expect(screen.getByText('The garden holds 1421 seeds; this panel has the newest 1.')).toBeInTheDocument();
  });
});
