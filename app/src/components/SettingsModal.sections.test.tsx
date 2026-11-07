import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { gesture } from '../test/renderApp';
import { openSection, renderSettings, savedSettings } from '../test/settings';
import { assertValidSettingsSectionID } from './settingsAutomation';

const SECTION_IDS = [
  'general',
  'desktop',
  'hygiene',
  'agents',
  'models',
  'delegation',
  'backgroundAgents',
  'experimental',
  'connectivity',
  'plugins',
  'backgroundTasks',
  'eventBus',
] as const;

const sharedHostSwitch = () => screen.getByRole('switch', { name: 'Shared terminal host' });

describe('SettingsModal sections', () => {
  it('uses plain section names throughout the navigation', async () => {
    await renderSettings();

    const navigation = screen.getByRole('navigation', { name: 'Settings sections' });
    expect(within(navigation).getAllByRole('button').map(button => button.firstElementChild?.textContent)).toEqual([
      'Appearance',
      'Files and locations',
      'Attention queue',
      'Agents and models',
      'Models',
      'Background agents',
      'Delegation',
      'GitHub hosts',
      'Outposts',
      'Phones',
      'Paired senders',
      'Plugins',
      'Experimental',
      'Task runner',
      'Event bus',
      'Analytics',
    ]);
  });

  it('keeps Quick Capture off until enabled in Experimental and can turn it off again', async () => {
    const daemon = await openSection('experimental');
    const toggle = () => screen.getByRole('switch', { name: 'Quick Capture' });
    expect(toggle()).toHaveAccessibleDescription(expect.stringMatching(/^Send messages and files to Chief or a crew member from any app\. Default shortcut ⌃⌥⌘Space/));
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(toggle()));
    expect(savedSettings(daemon)).toEqual([['quick_capture_enabled', 'true']]);
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(toggle()));
    expect(savedSettings(daemon)).toEqual([['quick_capture_enabled', 'true'], ['quick_capture_enabled', 'false']]);
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
  });

  it('exposes theme selection and the projects directory through accessible controls', async () => {
    const daemon = await openSection('general', { settings: { theme: 'system' } });
    expect(screen.getByRole('button', { name: 'System', pressed: true })).toBeInTheDocument();
    expect(screen.getByRole('group', { name: 'Theme preference' })).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-desktop')));
    expect(screen.getByLabelText('Projects directory')).toBe(screen.getByTestId('settings-projects-directory-input'));
  });

  it('closes from the backdrop without closing when content is clicked', async () => {
    const { daemon } = await renderSettings();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-modal')));
    expect(screen.getByTestId('settings-modal')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Dismiss settings' })));
    expect(screen.queryByTestId('settings-modal')).toBeNull();
  });

  it('shows the shared host on when nothing is stored, and off only once the daemon confirms a stored false', async () => {
    const daemon = await openSection('agents', { settings: { pty_backend_mode: 'migrating' } }, (scripted) => {
      scripted.on('set_setting', () => {});
    });
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'true');

    await gesture(daemon, () => fireEvent.click(sharedHostSwitch()));
    expect(savedSettings(daemon)).toEqual([['pty_shared_host_enabled', 'false']]);
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'true');

    daemon.emit({ event: 'settings_updated', settings: { pty_backend_mode: 'migrating', pty_shared_host_enabled: 'false' } });
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'false');
  });

  it('can turn the shared host back on from a stored false', async () => {
    const daemon = await openSection('agents', { settings: {
      pty_backend_mode: 'migrating', pty_shared_host_enabled: 'false', pty_shared_host_active: 'false',
    } });
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(sharedHostSwitch()));
    expect(savedSettings(daemon)).toEqual([['pty_shared_host_enabled', 'true']]);
  });

  it('leaves the shared host out of Experimental', async () => {
    await openSection('experimental');
    expect(screen.queryByRole('switch', { name: 'Shared terminal host' })).toBeNull();
  });

  it.each(['worker', 'shared', 'unknown'])('disables the shared host control on the %s backend', async (mode) => {
    const daemon = await openSection('agents', { settings: { pty_backend_mode: mode } });
    expect(sharedHostSwitch()).toBeDisabled();
    await gesture(daemon, () => fireEvent.click(sharedHostSwitch()));
    expect(savedSettings(daemon)).toEqual([]);
  });

  it('uses the navigation name as the sole page heading in every section', async () => {
    const { daemon } = await renderSettings();

    for (const id of SECTION_IDS) {
      assertValidSettingsSectionID(id);
      const item = screen.getByTestId(`settings-nav-${id}`);
      const name = item.textContent!;
      await gesture(daemon, () => fireEvent.click(item));
      const body = within(screen.getByRole('main'));
      expect(body.getByRole('heading', { name, level: 1 })).toBeInTheDocument();
      expect(body.getAllByText(name, { exact: true })).toHaveLength(1);
    }
  });

  it('keeps distinct groups within a settings page', async () => {
    await openSection('general');
    expect(screen.getByRole('heading', { name: 'Theme' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Font Size' })).toBeInTheDocument();
  });

  it('saves the narrowest tile width from Appearance when focus leaves the field', async () => {
    const daemon = await openSection('general', { settings: { narrowest_tile_cols: '66' } });
    const field = screen.getByLabelText('Narrowest tile width (columns)');
    expect(field).toHaveValue(66);
    fireEvent.change(field, { target: { value: '86' } });
    await gesture(daemon, () => fireEvent.blur(field));
    expect(savedSettings(daemon)).toEqual([['narrowest_tile_cols', '86']]);
    expect(field).toHaveValue(86);
  });

  it('finds every Experimental feature by its name, including one whose own section is hidden while it is off', async () => {
    const { daemon } = await renderSettings();
    const search = screen.getByRole('searchbox', { name: 'Search settings' });
    const navigation = within(screen.getByRole('navigation', { name: 'Settings sections' }));
    for (const query of ['Quick Capture', 'Claude mod', 'Nisse', 'Outposts', 'Agent callouts', 'Phone access', 'Shared Codex', 'Usage analytics']) {
      await gesture(daemon, () => fireEvent.change(search, { target: { value: query } }));
      expect(navigation.getByRole('button', { name: 'Experimental' })).toBeInTheDocument();
    }
  });

  it('finds the shared host in Experimental when searching for terminal hosting', async () => {
    const { daemon } = await renderSettings();
    const search = screen.getByRole('searchbox', { name: 'Search settings' });
    const navigation = within(screen.getByRole('navigation', { name: 'Settings sections' }));
    await gesture(daemon, () => fireEvent.change(search, { target: { value: 'Launcher 2.0' } }));
    expect(navigation.getByRole('button', { name: 'Experimental' })).toBeInTheDocument();
    for (const query of ['terminal', 'pty', 'Shared terminal host']) {
      await gesture(daemon, () => fireEvent.change(search, { target: { value: query } }));
      expect(navigation.getByRole('button', { name: 'Agents and models' })).toBeInTheDocument();
    }
    await gesture(daemon, () => fireEvent.click(navigation.getByRole('button', { name: 'Agents and models' })));
    expect(sharedHostSwitch()).toBeInTheDocument();
    expect(screen.queryByText('Runtime mode')).toBeNull();
  });

  it.each(['review', 'terminal'])('refuses the retired %s section by name', (id) => {
    expect(() => assertValidSettingsSectionID(id)).toThrow(`unknown settings section "${id}"`);
  });

  it('separates background agents and removes the unused reviewer model', async () => {
    const daemon = await openSection('agents', { settings: { reviewer_model: 'claude-opus-4-6' } });
    expect(screen.getByRole('heading', { name: 'Agents and models', level: 1 })).toBeInTheDocument();
    expect(screen.queryByLabelText('Reviewer model')).toBeNull();
    expect(screen.queryByTestId('settings-chief-route-claude')).toBeNull();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-backgroundAgents')));
    expect(screen.getByTestId('settings-chief-route-claude')).toBeInTheDocument();
    expect(screen.getByTestId('settings-garden-advisor-route')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^save$/i })).toBeNull();
  });

  it('puts the attention queue timings with the rest of hygiene', async () => {
    await openSection('hygiene');

    expect(screen.getByTestId('settings-auto-settle-arm')).toBeInTheDocument();
    expect(screen.getByTestId('settings-auto-settle-countdown')).toBeInTheDocument();
  });

  it('keeps quiet notifications on by default in the attention queue settings, and lets that change', async () => {
    const daemon = await openSection('hygiene');
    const toggle = () => screen.getByRole('switch', { name: 'Quiet notifications in the queue' });

    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(toggle()));
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    expect(savedSettings(daemon)).toEqual([['queue_quiet_notifications', 'false']]);
  });

  it('keeps hidden sessions in the queue by default in the attention queue settings, and saves the switch both ways', async () => {
    const daemon = await openSection('experimental');
    const hiddenInQueue = () => screen.getByRole('switch', { name: 'Show hidden sessions in the queue' });
    expect(screen.queryByRole('switch', { name: 'Show hidden sessions in the queue' })).toBeNull();

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-hygiene')));
    expect(hiddenInQueue()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(hiddenInQueue()));
    expect(hiddenInQueue()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(hiddenInQueue()));
    expect(hiddenInQueue()).toHaveAttribute('aria-checked', 'true');
    expect(savedSettings(daemon)).toEqual([
      ['queue_show_hidden_sessions', 'false'],
      ['queue_show_hidden_sessions', 'true'],
    ]);
  });

  it('finds the crew switch by searching "crew" in the attention queue settings, and saves it both ways', async () => {
    const { daemon } = await renderSettings();
    const search = screen.getByRole('searchbox', { name: 'Search settings' });
    await gesture(daemon, () => fireEvent.change(search, { target: { value: 'crew' } }));
    const navigation = within(screen.getByRole('navigation', { name: 'Settings sections' }));
    await gesture(daemon, () => fireEvent.click(navigation.getByRole('button', { name: 'Attention queue' })));
    const crewInQueue = () => screen.getByRole('switch', { name: 'Put the crew in the queue' });

    expect(crewInQueue()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(crewInQueue()));
    expect(crewInQueue()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(crewInQueue()));
    expect(crewInQueue()).toHaveAttribute('aria-checked', 'false');
    expect(savedSettings(daemon)).toEqual([
      ['queue_crew_enabled', 'true'],
      ['queue_crew_enabled', 'false'],
    ]);
  });

  it('raises a saved mark when a blur-committed field lands, and takes it away', async () => {
    const daemon = await openSection('desktop');

    const input = screen.getByTestId('settings-projects-directory-input');
    fireEvent.change(input, { target: { value: '/Users/you/code' } });
    await gesture(daemon, () => fireEvent.blur(input));

    expect(savedSettings(daemon)).toEqual([['projects_directory', '/Users/you/code']]);
    expect(screen.getByTestId('settings-projects-directory-saved')).toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(screen.queryByTestId('settings-projects-directory-saved')).toBeNull();
  });

  it('says nothing when a blur-committed field has not changed', async () => {
    const daemon = await openSection('desktop', { settings: { projects_directory: '/Users/you/code' } });

    await gesture(daemon, () => fireEvent.blur(screen.getByTestId('settings-projects-directory-input')));

    expect(savedSettings(daemon)).toEqual([]);
    expect(screen.queryByTestId('settings-projects-directory-saved')).toBeNull();
  });
});
