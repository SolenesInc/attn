import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { gesture, openSection, renderSettings, savedSettings } from '../test/settings';
import { assertValidSettingsSectionID } from './settingsAutomation';

const SECTION_IDS = [
  'general',
  'workspace',
  'hygiene',
  'agents',
  'backgroundAgents',
  'terminal',
  'autoMode',
  'connectivity',
  'plugins',
  'backgroundTasks',
  'eventBus',
  'data',
] as const;

const sharedHostSwitch = () => screen.getByRole('switch', { name: 'Shared PTY host (experimental)' });

describe('SettingsModal sections', () => {
  it('exposes theme selection and the projects directory through accessible controls', async () => {
    const daemon = await openSection('general', { settings: { theme: 'system' } });
    expect(screen.getByRole('button', { name: 'System', pressed: true })).toBeInTheDocument();
    expect(screen.getByRole('group', { name: 'Theme preference' })).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-workspace')));
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
    const daemon = await openSection('terminal', { settings: { pty_backend_mode: 'migrating' } }, (scripted) => {
      scripted.on('set_setting', () => {});
    });
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByTestId('settings-shared-pty-host-status')).toHaveTextContent('New sessions use dedicated Go workers.');

    await gesture(daemon, () => fireEvent.click(sharedHostSwitch()));
    expect(savedSettings(daemon)).toEqual([['pty_shared_host_enabled', 'true']]);
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'false');

    daemon.emit({ event: 'settings_updated', settings: { pty_backend_mode: 'migrating', pty_shared_host_enabled: 'true' } });
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'true');
  });

  it('can turn the shared host off without claiming existing terminals will move', async () => {
    const daemon = await openSection('terminal', { settings: {
      pty_backend_mode: 'migrating', pty_shared_host_enabled: 'true', pty_shared_host_active: 'true',
    } });
    expect(sharedHostSwitch()).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByTestId('settings-shared-pty-host-status')).toHaveTextContent('New sessions use the shared Rust host.');
    expect(screen.getByText(/Running sessions stay untouched/)).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(sharedHostSwitch()));
    expect(savedSettings(daemon)).toEqual([['pty_shared_host_enabled', 'false']]);
  });

  it('shows when a saved opt-in fell back to dedicated workers', async () => {
    await openSection('terminal', { settings: { pty_backend_mode: 'migrating', pty_shared_host_enabled: 'true', pty_shared_host_active: 'false' } });
    expect(screen.getByTestId('settings-shared-pty-host-status')).toHaveTextContent('Shared host unavailable.');
    expect(sharedHostSwitch()).toBeEnabled();
  });

  it.each(['worker', 'shared', 'embedded', 'unknown'])('disables the experiment control on the %s backend', async (mode) => {
    const daemon = await openSection('terminal', { settings: { pty_backend_mode: mode } });
    expect(sharedHostSwitch()).toBeDisabled();
    await gesture(daemon, () => fireEvent.click(sharedHostSwitch()));
    expect(savedSettings(daemon)).toEqual([]);
  });

  it('gives every published section id a nav item that renders a block', async () => {
    const { daemon } = await renderSettings();

    for (const id of SECTION_IDS) {
      assertValidSettingsSectionID(id);
      await gesture(daemon, () => fireEvent.click(screen.getByTestId(`settings-nav-${id}`)));
      expect(screen.getByTestId(`settings-section-${id}`).querySelector('.settings-block')).not.toBeNull();
    }
  });

  it('refuses the retired review id by name', () => {
    expect(() => assertValidSettingsSectionID('review')).toThrow(/unknown settings section "review"/);
  });

  it('separates background agents and removes the unused reviewer model', async () => {
    const daemon = await openSection('agents', { settings: { reviewer_model: 'claude-opus-4-6' } });
    expect(screen.getByRole('heading', { name: 'Agents and models', level: 1 })).toBeInTheDocument();
    expect(screen.queryByLabelText('Reviewer model')).toBeNull();
    expect(screen.queryByTestId('settings-chief-model-claude')).toBeNull();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-backgroundAgents')));
    expect(screen.getByTestId('settings-chief-model-claude')).toBeInTheDocument();
    expect(screen.getByTestId('settings-garden-advisor-agent')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^save$/i })).toBeNull();
  });

  it('puts the attention queue timings with the rest of hygiene', async () => {
    await openSection('hygiene');

    expect(screen.getByTestId('settings-auto-settle-arm')).toBeInTheDocument();
    expect(screen.getByTestId('settings-auto-settle-countdown')).toBeInTheDocument();
  });

  it('raises a saved mark when a blur-committed field lands, and takes it away', async () => {
    const daemon = await openSection('workspace');

    const input = screen.getByTestId('settings-projects-directory-input');
    fireEvent.change(input, { target: { value: '/Users/you/code' } });
    await gesture(daemon, () => fireEvent.blur(input));

    expect(savedSettings(daemon)).toEqual([['projects_directory', '/Users/you/code']]);
    expect(screen.getByTestId('settings-projects-directory-saved')).toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(screen.queryByTestId('settings-projects-directory-saved')).toBeNull();
  });

  it('says nothing when a blur-committed field has not changed', async () => {
    const daemon = await openSection('workspace', { settings: { projects_directory: '/Users/you/code' } });

    await gesture(daemon, () => fireEvent.blur(screen.getByTestId('settings-projects-directory-input')));

    expect(savedSettings(daemon)).toEqual([]);
    expect(screen.queryByTestId('settings-projects-directory-saved')).toBeNull();
  });
});
