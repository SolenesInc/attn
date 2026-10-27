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
  'autoMode',
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
    expect(within(navigation).getAllByRole('button').map(button => button.textContent)).toEqual([
      'Appearance',
      'Files and locations',
      'Attention queue',
      'Agents and models',
      'Models',
      'Background agents',
      'Delegation',
      'Auto mode',
      'Mobile and hosts',
      'Plugins',
      'Experimental',
      'Task runner',
      'Event bus',
    ]);
  });

  it('keeps Quick Capture off until enabled in Experimental and can turn it off again', async () => {
    const daemon = await openSection('experimental');
    const toggle = () => screen.getByRole('switch', { name: 'Quick Capture' });
    expect(toggle()).toHaveAccessibleDescription('Send messages and files to Chief or a crew member from any app with a global shortcut (default to ⌃⌥⌘Space).');
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

  it('keeps the shared PTY experiment off until the daemon confirms opt-in', async () => {
    const daemon = await openSection('experimental', { settings: { pty_backend_mode: 'migrating' } }, (scripted) => {
      scripted.on('set_setting', () => {});
    });
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'false');

    await gesture(daemon, () => fireEvent.click(sharedHostSwitch()));
    expect(savedSettings(daemon)).toEqual([['pty_shared_host_enabled', 'true']]);
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'false');

    daemon.emit({ event: 'settings_updated', settings: { pty_backend_mode: 'migrating', pty_shared_host_enabled: 'true' } });
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'true');
  });

  it('can turn the shared host off', async () => {
    const daemon = await openSection('experimental', { settings: {
      pty_backend_mode: 'migrating', pty_shared_host_enabled: 'true', pty_shared_host_active: 'true',
    } });
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(sharedHostSwitch()));
    expect(savedSettings(daemon)).toEqual([['pty_shared_host_enabled', 'false']]);
  });

  it.each(['worker', 'shared', 'embedded', 'unknown'])('disables the experiment control on the %s backend', async (mode) => {
    const daemon = await openSection('experimental', { settings: { pty_backend_mode: mode } });
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

  it('finds the shared host in Experimental when searching for terminal hosting', async () => {
    const { daemon } = await renderSettings();
    const search = screen.getByRole('searchbox', { name: 'Search settings' });
    const navigation = within(screen.getByRole('navigation', { name: 'Settings sections' }));
    for (const query of ['terminal', 'pty', 'Launcher 2.0', 'Shared terminal host', 'Direct agent launches']) {
      await gesture(daemon, () => fireEvent.change(search, { target: { value: query } }));
      expect(navigation.getByRole('button', { name: 'Experimental' })).toBeInTheDocument();
    }
    await gesture(daemon, () => fireEvent.click(navigation.getByRole('button', { name: 'Experimental' })));
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
    expect(screen.queryByTestId('settings-chief-route-claude')).toBeNull();
    expect(screen.getByTestId('settings-garden-advisor-route')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^save$/i })).toBeNull();
  });

  it('puts the attention queue timings with the rest of hygiene', async () => {
    await openSection('hygiene');

    expect(screen.getByTestId('settings-auto-settle-arm')).toBeInTheDocument();
    expect(screen.getByTestId('settings-auto-settle-countdown')).toBeInTheDocument();
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
