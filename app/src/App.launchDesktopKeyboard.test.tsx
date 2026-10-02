import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, renderApp } from './test/renderApp';
import { emptyDesktop } from './test/daemonFixtures';
import { LaunchDesktopKind, LaunchDesktopMode, MigrationPhase } from './types/generated';

const crewItem = (id: string) => ({
  kind: LaunchDesktopKind.Crew,
  item_id: id,
  name: id,
  profile_id: 'default',
  confirmed: false,
  setting: { mode: LaunchDesktopMode.Own, desktop_name: id, destination_id: id, label: `${id} (no ⌘ number)`, pending: true },
});

async function openLedger() {
  let state: import('./test/protocol').EventMessage<'migration_result'>['state'] & {} = {
    groups: [], desktops: [], can_undo: false, suggestion_available: false, profile_id: 'default',
    phase: MigrationPhase.LaunchRequired, revision: 9, launch_items: [crewItem('Alder'), crewItem('Keel')],
    launch_desktops: [{ ...emptyDesktop('d1', { shortcut_slot: 1, profile_id: 'default' }), panes: [] }],
  };
  const { daemon } = await renderApp({ initialState: { migration_phase: MigrationPhase.LaunchRequired }, script(daemon) {
    daemon.on('migration_get', () => ({ event: 'migration_result', action: 'migration_get', success: true, state }));
    daemon.on('launch_desktop_set', ({ kind, item_id, setting }) => {
      const item = { ...state.launch_items!.find((candidate) => candidate.item_id === item_id)!, confirmed: true, setting: { ...setting, pending: false, label: 'Desktop 1' } };
      state = { ...state, revision: state.revision + 1, launch_items: state.launch_items!.map((candidate) => candidate.item_id === item_id ? item : candidate) };
      return { event: 'launch_desktop_result', action: 'launch_desktop_set', kind, item_id, success: true, item, items: state.launch_items };
    });
    daemon.on('migration_finish', () => ({ event: 'migration_result', action: 'migration_finish', success: true, state: { ...state, phase: MigrationPhase.Complete } }));
  } });
  await daemon.idle();
  return daemon;
}

describe('launch desktop ledger keyboard', () => {
  it('chooses a desktop for the selected agent and finishes without a click', async () => {
    const daemon = await openLedger();
    expect(screen.getByRole('heading', { name: 'Where should your agents start?' })).toBeInTheDocument();

    const press = (init: KeyboardEventInit) => gesture(daemon, () => fireEvent.keyDown(document.activeElement!, init));
    expect(document.activeElement).toHaveAttribute('data-launch-item', 'Alder');
    await press({ key: 'ArrowDown' });
    expect(document.activeElement).toHaveAttribute('data-launch-item', 'Keel');
    await press({ key: '1', metaKey: true });
    expect(daemon.sentOf('launch_desktop_set')).toEqual([]);
    await press({ key: '1' });
    await press({ key: 'Enter', metaKey: true });

    expect(daemon.sentOf('launch_desktop_set').map(({ item_id, setting }) => ({ item_id, setting }))).toEqual([
      { item_id: 'Keel', setting: { mode: LaunchDesktopMode.Desktop, desktop_id: 'd1' } },
    ]);
    expect(daemon.sentOf('migration_finish').map(({ expected_revision }) => expected_revision)).toEqual([10]);
  });

  it('gives the selected agent its own desktop on an empty slot by that slot\'s desktop id', async () => {
    const daemon = await openLedger();

    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: '5' }));
    const dialog = screen.getByRole('dialog', { name: 'Its own desktop' });
    expect(dialog).toHaveTextContent('On ⌘5.');
    await gesture(daemon, () => fireEvent.click(within(dialog).getByRole('button', { name: 'Use this name' })));

    expect(daemon.sentOf('launch_desktop_set').map(({ item_id, setting }) => ({ item_id, setting }))).toEqual([
      { item_id: 'Alder', setting: { mode: LaunchDesktopMode.Own, desktop_name: 'Alder', desktop_id: 'default/desktop_5' } },
    ]);
  });
});
