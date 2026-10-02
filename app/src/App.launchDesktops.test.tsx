import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { defaultProfile, emptyDesktop, crewMember, daemonSession, soloDesktop } from './test/daemonFixtures';
import { savedSettings, serveSettings } from './test/settings';
import { openSession } from './test/appFixtures';
import { LaunchDesktopKind, LaunchDesktopMode, MigrationPhase } from './types/generated';

const crewItem = (id: string): NonNullable<import('./test/protocol').EventMessage<'launch_desktop_result'>['item']> => ({ kind: LaunchDesktopKind.Crew, item_id: id, name: id, profile_id: 'default', confirmed: false, setting: { mode: LaunchDesktopMode.Own, desktop_name: id, destination_id: id, label: `${id} (no ⌘ number)`, pending: true } });

async function openMigration(items: ReturnType<typeof crewItem>[]) {
  let state: import('./test/protocol').EventMessage<'migration_result'>['state'] & {} = { groups: [], desktops: [], can_undo: false, suggestion_available: false, profile_id: 'default', phase: MigrationPhase.LaunchRequired, revision: 9, launch_items: items, launch_desktops: [{ ...emptyDesktop('d1', { shortcut_slot: 1 }), panes: [] }] };
  const view = await renderApp({ initialState: { migration_phase: MigrationPhase.LaunchRequired }, script(daemon) {
    daemon.on('migration_get', () => ({ event: 'migration_result', action: 'migration_get', success: true, state }));
    daemon.on('launch_desktop_set', ({ kind, item_id, setting }) => {
      const item = { ...state.launch_items!.find((candidate) => candidate.item_id === item_id)!, confirmed: true, setting: { ...setting, destination_id: setting.destination_id || `${item_id}-new`, pending: true, label: `${setting.desktop_name || 'Review'} (no ⌘ number)` } };
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
  it('names an own desktop, explicitly shares it and finishes against the shown revision', async () => {
    const { daemon } = await openMigration([crewItem('Alder'), crewItem('Keel')]);
    expect(screen.getByText('Change later in Manage crew')).toBeInTheDocument();
    expect(within(screen.getByRole('navigation', { name: 'Migration steps' })).getByText('Launch desktops')).toHaveAttribute('aria-current', 'step');
    expect(screen.queryByRole('heading', { name: 'Automations' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('New desktops for Keel')).not.toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getAllByRole('button', { name: 'Its own desktop…' })[0]));
    const dialog = screen.getByRole('dialog', { name: 'Its own desktop' });
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Name' }), { target: { value: 'Review' } });
    await gesture(daemon, () => fireEvent.click(within(dialog).getByRole('button', { name: 'Use this name' })));
    expect(screen.getByLabelText('New desktops for Keel')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.change(screen.getByLabelText('New desktops for Keel'), { target: { value: 'Alder-new' } }));
    expect(screen.getByLabelText('New desktops for Keel')).toHaveValue('Alder-new');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Finish setup/ })));
    expect(daemon.sentOf('launch_desktop_set').map(({ item_id, setting }) => ({ item_id, setting }))).toEqual([
      { item_id: 'Alder', setting: { mode: LaunchDesktopMode.Own, desktop_name: 'Review', shortcut_slot: 0 } },
      { item_id: 'Keel', setting: { mode: LaunchDesktopMode.Desktop, destination_id: 'Alder-new' } },
    ]);
    expect(daemon.sentOf('migration_finish').map(({ expected_revision }) => expected_revision)).toEqual([11]);
    expect(screen.getByRole('button', { name: 'Continue →' })).toBeInTheDocument();
  });

  it('omits the crew group when there are only automations', async () => {
    await openMigration([{ ...crewItem('Checks'), kind: LaunchDesktopKind.Automation }]);
    expect(screen.getByRole('heading', { name: 'Automations' })).toBeInTheDocument();
    expect(screen.queryByText('Change later in Manage crew')).not.toBeInTheDocument();
  });

  it('keeps arrivals inside the open Crew focus boundary and navigates from there', async () => {
    const { daemon } = await renderApp({ initialState: { crew: [crewMember('alder')], sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] } });
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('manage-crew')));
    await daemon.emit({ event: 'background_launch', session_id: 'keel-day', profile_id: 'profile-default', desktop_id: 'review', name: 'Keel', requested_by: 'Alder', desktop_label: 'Review', kind: LaunchDesktopKind.Crew });
    await daemon.idle();
    const dialog = screen.getByRole('dialog', { name: 'Manage crew' });
    const action = within(dialog).getByRole('button', { name: /Keel.*Click to go/ });
    action.focus();
    expect(document.activeElement).toBe(action);
    await gesture(daemon, () => fireEvent.click(action));
    expect(daemon.sentOf('desktop_show_session').map(({ session_id }) => session_id)).toEqual(['keel-day']);
    expect(screen.getByText('✓ Done')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Manage crew' })).not.toBeInTheDocument();
  });

  it.each([
    ['agent palette', 'ui.actionMenu', 'Agents'],
    ['command palette', 'ui.commandPalette', 'Commands'],
    ['fullscreen Garden', 'board.open', 'The garden'],
    ['shortcuts', 'ui.showShortcuts', 'Keyboard Shortcuts'],
    ['shortcut editor', 'ui.showShortcuts', 'Customize Shortcuts'],
    ['release notes', 'ui.commandPalette', "What's new"],
  ] as const)('shows an arrival above the %s and closes it when navigating', async (_surface, shortcut, title) => {
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] } });
    await gesture(daemon, () => pressShortcut(shortcut));
    if (title === 'Customize Shortcuts') await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Edit shortcuts' })));
    if (title === "What's new") {
      const search = screen.getByRole('combobox');
      fireEvent.change(search, { target: { value: ">What's new" } });
      await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));
    }
    if (title === 'The garden') await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Expand the garden' })));
    expect(screen.getByRole('dialog', { name: title })).toBeInTheDocument();
    await daemon.emit({ event: 'background_launch', session_id: 's1', profile_id: 'profile-default', desktop_id: 'review', name: 'Keel', requested_by: 'Alder', desktop_label: 'Review', kind: LaunchDesktopKind.Crew });
    await daemon.idle();
    const action = screen.getByRole('button', { name: /Keel.*Click to go/ });
    action.focus();
    expect(document.activeElement).toBe(action);
    await gesture(daemon, () => fireEvent.click(action));
    expect(screen.queryByRole('dialog', { name: title })).not.toBeInTheDocument();
    expect(daemon.sentOf('desktop_show_session').map(({ session_id }) => session_id)).toEqual(['s1']);
    expect(screen.getByText('✓ Done')).toBeInTheDocument();
  });

  it('closes Settings through its draft-saving close action when navigating an arrival', async () => {
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] } });
    await gesture(daemon, () => pressShortcut('ui.openSettings'));
    expect(screen.getByTestId('settings-modal')).toBeInTheDocument();
    serveSettings(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-agents')));
    const model = screen.getByTestId('settings-default-model-claude');
    model.focus();
    fireEvent.change(model, { target: { value: 'sonnet' } });
    await daemon.emit({ event: 'background_launch', session_id: 's1', profile_id: 'profile-default', desktop_id: 'review', name: 'Keel', requested_by: 'Alder', desktop_label: 'Review', kind: LaunchDesktopKind.Crew });
    await daemon.idle();
    await gesture(daemon, () => {
      const action = screen.getByRole('button', { name: /Keel.*Click to go/ });
      action.focus();
      fireEvent.click(action);
    });
    expect(screen.queryByTestId('settings-modal')).not.toBeInTheDocument();
    expect(savedSettings(daemon)).toEqual([['default_model_claude', 'sonnet']]);
    expect(daemon.sentOf('desktop_show_session').map(({ session_id }) => session_id)).toEqual(['s1']);
    expect(screen.getByText('✓ Done')).toBeInTheDocument();
  });

  it('dismisses a snooze chooser for a toast action', async () => {
    const { daemon } = await renderApp({ initialState: {
      settings: { queue_mode_enabled: 'true' },
      sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle', turn_owed: true })),
      desktops: ['s1', 's2'].map((id) => soloDesktop(id)),
    } });
    await openSession(daemon, 's1');
    await gesture(daemon, () => pressShortcut('session.snooze'));
    expect(screen.getByRole('menu', { name: 'Snooze s1' })).toBeInTheDocument();
    await daemon.emit({ event: 'background_launch', session_id: 's1', profile_id: 'profile-default', desktop_id: 'review', name: 'Keel', requested_by: 'Alder', desktop_label: 'Review', kind: LaunchDesktopKind.Crew });
    await daemon.idle();
    await gesture(daemon, () => {
      const action = screen.getByRole('button', { name: /Keel.*Click to go/ });
      action.focus();
      fireEvent.click(action);
    });
    expect(screen.queryByRole('menu', { name: 'Snooze s1' })).not.toBeInTheDocument();
    expect(screen.getByText('✓ Done')).toBeInTheDocument();
  });

  it('groups background arrivals, names the requester and goes only when clicked', async () => {
    const { daemon } = await renderApp({ initialState: { profiles: [defaultProfile('d1')], desktops: [emptyDesktop('d1')] } });
    await daemon.emit({ event: 'background_launch', session_id: 'alder-day', profile_id: 'profile-default', desktop_id: 'review', name: 'Alder', requested_by: 'Trellis', desktop_label: 'Review (no ⌘ number)', kind: LaunchDesktopKind.Crew });
    await daemon.emit({ event: 'background_launch', session_id: 'checks-run', profile_id: 'another-profile', desktop_id: 'checks', name: 'Checks', requested_by: 'automation', desktop_label: 'Checks (no ⌘ number)', kind: LaunchDesktopKind.Automation });
    await daemon.idle();
    expect(screen.getByText('2 notifications')).toBeInTheDocument();
    expect(screen.getByText('Trellis')).toBeInTheDocument();
    expect(daemon.sentOf('desktop_show_session')).toEqual([]);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Alder.*Click to go/ })));
    expect(daemon.sentOf('desktop_show_session').map(({ session_id }) => session_id)).toEqual(['alder-day']);
    expect(screen.getByText('✓ Done')).toBeInTheDocument();
    expect(screen.getByText('2 notifications')).toBeInTheDocument();
  });
});
