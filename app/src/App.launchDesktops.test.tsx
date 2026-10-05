import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, renderApp } from './test/renderApp';
import { defaultProfile, emptyDesktop, crewMember, daemonSession, soloDesktop } from './test/daemonFixtures';
import { LaunchDesktopKind, MigrationPhase } from './types/generated';

const crewItem = (id: string): NonNullable<import('./test/protocol').EventMessage<'launch_desktop_result'>['item']> => ({ kind: LaunchDesktopKind.Crew, item_id: id, name: id, profile_id: 'default', confirmed: false, setting: { label: `${id} (new)` } });

async function openMigration(items: ReturnType<typeof crewItem>[]) {
  let state: import('./test/protocol').EventMessage<'migration_result'>['state'] & {} = { groups: [], desktops: [], can_undo: false, suggestion_available: false, profile_id: 'default', phase: MigrationPhase.LaunchRequired, revision: 9, launch_items: items, launch_desktops: [{ ...emptyDesktop('d1', { shortcut_slot: 1 }), panes: [] }] };
  const view = await renderApp({ initialState: { migration_phase: MigrationPhase.LaunchRequired }, script(daemon) {
    daemon.on('migration_get', () => ({ event: 'migration_result', action: 'migration_get', success: true, state }));
    daemon.on('launch_desktop_set', ({ kind, item_id, setting }) => {
      const desktopId = setting?.desktop_id ?? `desktop-${setting?.desktop_name}`;
      const item = { ...state.launch_items!.find((candidate) => candidate.item_id === item_id)!, confirmed: true, setting: { desktop_id: desktopId, label: `${desktopId} (no ⌘ number)` } };
      state = { ...state, revision: state.revision + 1, launch_items: state.launch_items!.map((candidate) => candidate.item_id === item_id ? item : candidate) };
      return { event: 'launch_desktop_result', action: 'launch_desktop_set', kind, item_id, success: true, item, items: state.launch_items };
    });
    daemon.on('migration_finish', () => ({ event: 'migration_result', action: 'migration_finish', success: true, state: { ...state, phase: MigrationPhase.Complete } }));
  } });
  await view.daemon.idle();
  expect(screen.getByRole('heading', { name: 'Where should your agents start?' })).toBeInTheDocument();
  return view;
}

describe('launch desktops', () => {
  it('names a new desktop, shares it and creates it only when finishing', async () => {
    const { daemon } = await openMigration([crewItem('Alder'), crewItem('Keel')]);
    expect(screen.getByText('Change later in Manage crew')).toBeInTheDocument();
    expect(within(screen.getByRole('navigation', { name: 'Migration steps' })).getByText('Launch desktops')).toHaveAttribute('aria-current', 'step');
    expect(screen.queryByRole('heading', { name: 'Automations' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('New desktops for Keel')).not.toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getAllByRole('button', { name: 'A new desktop…' })[0]));
    const dialog = screen.getByRole('dialog', { name: 'A new desktop' });
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Name' }), { target: { value: 'Review' } });
    await gesture(daemon, () => fireEvent.click(within(dialog).getByRole('button', { name: 'Use this name' })));
    expect(daemon.sentOf('launch_desktop_set')).toEqual([]);
    await gesture(daemon, () => fireEvent.change(screen.getByLabelText('New desktops for Keel'), { target: { value: '|Review' } }));
    const keel = screen.getByText('Keel').closest('[data-launch-item]') as HTMLElement;
    expect(within(keel).getByText('Review (new)')).toBeInTheDocument();
    expect(within(keel).getByText('also used by Alder')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Finish setup/ })));
    expect(daemon.sentOf('launch_desktop_set').map(({ item_id, setting }) => ({ item_id, setting }))).toEqual([
      { item_id: 'Alder', setting: { desktop_name: 'Review' } },
      { item_id: 'Keel', setting: { desktop_id: 'desktop-Review' } },
    ]);
    expect(daemon.sentOf('migration_finish').map(({ expected_revision }) => expected_revision)).toEqual([11]);
    expect(screen.getByRole('button', { name: 'Continue →' })).toBeInTheDocument();
  });

  it('omits the crew group when there are only automations', async () => {
    await openMigration([{ ...crewItem('Checks'), kind: LaunchDesktopKind.Automation }]);
    expect(screen.getByRole('heading', { name: 'Automations' })).toBeInTheDocument();
    expect(screen.queryByText('Change later in Manage crew')).not.toBeInTheDocument();
  });

  it.each([LaunchDesktopKind.Crew, LaunchDesktopKind.Automation])('keeps a %s background launch quiet while Manage crew is open', async (kind) => {
    const { daemon } = await renderApp({ initialState: { crew: [crewMember('alder')], sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] } });
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('manage-crew')));
    const dialog = screen.getByRole('dialog', { name: 'Manage crew' });
    const button = within(dialog).getAllByRole('button')[0];
    button.focus();
    const existingAlerts = screen.queryAllByRole('alert');
    await daemon.emit({ event: 'background_launch', session_id: 'keel-day', profile_id: 'profile-default', desktop_id: 'review', name: 'Keel', requested_by: 'Alder', desktop_label: 'Review', kind });
    await daemon.idle();
    expect(screen.queryByText('Keel')).not.toBeInTheDocument();
    expect(screen.queryAllByRole('alert')).toEqual(existingAlerts);
    expect(screen.getByRole('dialog', { name: 'Manage crew' })).toBe(dialog);
    expect(document.activeElement).toBe(button);
    expect(daemon.sentOf('desktop_show_session')).toEqual([]);
  });

  it('keeps background launches quiet across profiles without navigating', async () => {
    const { daemon } = await renderApp({ initialState: { profiles: [defaultProfile('d1')], desktops: [emptyDesktop('d1')] } });
    await daemon.emit({ event: 'background_launch', session_id: 'alder-day', profile_id: 'profile-default', desktop_id: 'review', name: 'Alder', requested_by: 'Trellis', desktop_label: 'Review', kind: LaunchDesktopKind.Crew });
    await daemon.emit({ event: 'background_launch', session_id: 'checks-run', profile_id: 'another-profile', desktop_id: 'checks', name: 'Checks', requested_by: 'automation', desktop_label: 'Checks', kind: LaunchDesktopKind.Automation });
    await daemon.idle();
    expect(screen.queryByText('2 notifications')).not.toBeInTheDocument();
    expect(screen.queryByText('Trellis')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(daemon.sentOf('desktop_show_session')).toEqual([]);
  });
});
