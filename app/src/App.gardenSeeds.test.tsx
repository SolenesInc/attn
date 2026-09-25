import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSeed, daemonSession, type DaemonSeed } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';

const layout = { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] };

async function openGarden(seeds: DaemonSeed[]) {
  const view = await renderApp({ initialState: { ...layout, seeds } });
  await gesture(view.daemon, () => pressShortcut('board.open'));
  return view.daemon;
}


describe('App garden seeds', () => {
  it('seeds the garden from initial_state', async () => {
    await openGarden([daemonSeed('s-aaa111', { title: 'already planted' })]);

    expect(screen.getByText('already planted')).toBeInTheDocument();
  });

  it('replaces the garden on every planting broadcast', async () => {
    const daemon = await openGarden([daemonSeed('s-aaa111', { title: 'already planted' })]);

    daemon.emit({
      event: 'garden_seeds_updated',
      seeds: [daemonSeed('s-bbb222', { title: 'just planted' }), daemonSeed('s-aaa111', { title: 'already planted' })],
      total: 2,
    });

    expect(screen.getByText('just planted')).toBeInTheDocument();
    expect(screen.getByText('already planted')).toBeInTheDocument();
  });

  it('reads a garden-less daemon as an empty garden', async () => {
    const daemon = await openGarden([daemonSeed('s-aaa111', { title: 'already planted' })]);
    expect(screen.getByText('already planted')).toBeInTheDocument();

    daemon.on('client_hello', () => initialState(layout));
    await daemon.reconnect();

    expect(screen.queryByText('already planted')).toBeNull();
  });

  it('carries how many seeds the garden holds, not just the ones it sent', async () => {
    const daemon = await openGarden([]);

    daemon.emit({ event: 'garden_seeds_updated', seeds: [daemonSeed('s-bbb222', { title: 'the newest one' })], total: 1421 });

    expect(screen.getByText('The garden holds 1421 seeds; this panel has the newest 1.')).toBeInTheDocument();
  });
});
