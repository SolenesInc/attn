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

  it('applies the saved UI scale and changes it with the font size shortcuts', async () => {
    const { daemon } = await renderApp({
      initialState: { migration_phase: MigrationPhase.PlacementRequired, settings: { uiScale: '1.3' } },
      script: (scripted) => scripted.on('migration_get', () => ({ event: 'migration_result', action: 'migration_get', success: true, state: migrationState() })),
    });
    await daemon.idle();
    screen.getByRole('heading', { name: 'Your workspaces are now desktops.' });
    expect(document.documentElement.style.getPropertyValue('--ui-scale')).toBe('1.3');

    await gesture(daemon, () => pressShortcut('ui.increaseFontSize'));
    expect(document.documentElement.style.getPropertyValue('--ui-scale')).toBe('1.4');
    await gesture(daemon, () => pressShortcut('ui.decreaseFontSize'));
    await gesture(daemon, () => pressShortcut('ui.decreaseFontSize'));
    expect(document.documentElement.style.getPropertyValue('--ui-scale')).toBe('1.2');
    expect(daemon.sentOf('set_setting').slice(-1)).toEqual([{ cmd: 'set_setting', key: 'uiScale', value: '1.2' }]);
  });
});
