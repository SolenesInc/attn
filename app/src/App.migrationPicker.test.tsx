import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { importedGroup, migrationState } from './test/migration';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { MigrationPhase } from './types/generated';

function twin(id: string, desktopId: string, agent: string) {
  const group = importedGroup(id, 'exo', desktopId);
  return { ...group, panes: group.panes.map((pane) => ({ ...pane, title: agent })) };
}

describe('App migration picker', () => {
  it('counts agents and tiles separately on desktop cards', async () => {
    const agents = importedGroup('g1', 'Agents', 'd1', { agents: 2 });
    const mixed = importedGroup('g2', 'Mixed', 'd2');
    mixed.tree_json = JSON.stringify({ type: 'split', split_id: 'mixed-split', direction: 'vertical', ratio: 0.5, children: [
      { type: 'pane', pane_id: mixed.panes[0].pane_id }, { type: 'tile', tile_id: 'browser', tile_kind: 'browser' },
    ] });
    const tiles = { ...importedGroup('g3', 'Tiles', 'd3'), panes: [], tree_json: JSON.stringify({ type: 'tile', tile_id: 'garden', tile_kind: 'garden' }) };
    const state = migrationState({ groups: [agents, mixed, tiles] });
    const { daemon } = await renderApp({ initialState: { migration_phase: MigrationPhase.PlacementRequired },
      script: (scripted) => scripted.on('migration_get', () => ({ event: 'migration_result', action: 'migration_get', success: true, state })),
    });
    await daemon.idle();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Continue →' })));
    const desktops = screen.getByRole('region', { name: 'Desktops' });
    expect(within(desktops).getByRole('button', { name: 'Desktop 1, Agents' })).toHaveTextContent('2 agents');
    expect(within(desktops).getByRole('button', { name: 'Desktop 2, Mixed' })).toHaveTextContent('1 agent · 1 tile');
    expect(within(desktops).getByRole('button', { name: 'Desktop 10, Tiles' })).toHaveTextContent('1 tile');
  });

  it('names two workspaces that share a title apart by their first agent', async () => {
    const state = migrationState({
      groups: [twin('g1', 'd1', 'Chief'), twin('g2', 'd2', 'Delete X tweet history'), importedGroup('g3', 'exo · Chief', 'd3')],
    });
    const { daemon } = await renderApp({
      initialState: { migration_phase: MigrationPhase.PlacementRequired },
      script: (scripted) => scripted.on('migration_get', () => ({ event: 'migration_result', action: 'migration_get', success: true, state })),
    });
    await daemon.idle();
    expect(daemon.sentOf('migration_get')).toEqual([expect.objectContaining({ cmd: 'migration_get' })]);
    screen.getByRole('heading', { name: 'Your workspaces are now desktops.' });

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Continue →' })));
    const rows = screen.getByRole('listbox', { name: 'Workspaces' });
    expect(within(rows).getAllByRole('option').map((row) => row.querySelector('.mp-source-name')?.textContent))
      .toEqual(['exo · Chief', 'exo · Delete X tweet history', 'exo · Chief (2)']);

    await gesture(daemon, () => fireEvent.click(within(rows).getAllByRole('option')[2]));
    await gesture(daemon, () => fireEvent.keyDown(document, { key: 'm' }));
    const dialog = screen.getByRole('dialog', { name: 'Move exo · Chief (2) to…' });
    expect(within(dialog).getByRole('button', { name: /Desktop 1\b.*exo · Chief$/ })).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: /Desktop 2\b.*exo · Delete X tweet history$/ })).toBeInTheDocument();
    expect(daemon.sentOf('migration_get')).toHaveLength(1);
  });

  it('changes the saved UI scale with the font size shortcuts, saving only real changes', async () => {
    const { daemon } = await renderApp({
      initialState: { migration_phase: MigrationPhase.PlacementRequired, settings: { uiScale: '1.3' } },
      script: (scripted) => scripted.on('migration_get', () => ({ event: 'migration_result', action: 'migration_get', success: true, state: migrationState() })),
    });
    await daemon.idle();
    screen.getByRole('heading', { name: 'Your workspaces are now desktops.' });

    await gesture(daemon, () => pressShortcut('ui.increaseFontSize'));
    await gesture(daemon, () => pressShortcut('ui.decreaseFontSize'));
    await gesture(daemon, () => pressShortcut('ui.decreaseFontSize'));
    await gesture(daemon, () => pressShortcut('ui.resetFontSize'));
    await gesture(daemon, () => pressShortcut('ui.resetFontSize'));
    expect(daemon.sentOf('set_setting').map(({ key, value }) => [key, value]))
      .toEqual([['uiScale', '1.4'], ['uiScale', '1.3'], ['uiScale', '1.2'], ['uiScale', '1']]);
  });
});
