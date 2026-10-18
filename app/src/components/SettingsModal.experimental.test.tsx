import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSection, savedSettings } from '../test/settings';
import { gesture } from '../test/renderApp';

const sharedCodex = () => screen.getByRole('switch', { name: 'Shared Codex' });
const hiddenInQueue = () => screen.getByRole('switch', { name: 'Show hidden sessions in the queue' });
const directLaunch = () => screen.getByRole('switch', { name: 'Direct agent launches' });

describe('Experimental settings for shared Codex', () => {
  it('starts with Shared Codex off and saves its setting both ways', async () => {
    const daemon = await openSection('experimental');
    expect(sharedCodex()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(sharedCodex()));
    expect(sharedCodex()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(sharedCodex()));
    expect(savedSettings(daemon)).toEqual([['codex_shared_enabled', 'true'], ['codex_shared_enabled', 'false']]);
  });

  it('keeps hidden sessions in the queue by default, and lets that change while Shared Codex is off', async () => {
    const daemon = await openSection('experimental');
    expect(sharedCodex()).toHaveAttribute('aria-checked', 'false');
    expect(hiddenInQueue()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(hiddenInQueue()));
    expect(hiddenInQueue()).toHaveAttribute('aria-checked', 'false');
    expect(savedSettings(daemon)).toEqual([['queue_show_hidden_sessions', 'false']]);
  });
});

describe('Experimental settings for direct agent launches', () => {
  it('starts off and saves the setting in both directions', async () => {
    const daemon = await openSection('experimental');
    expect(screen.getByRole('button', { name: 'Experimental' })).toBeInTheDocument();
    expect(directLaunch()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(directLaunch()));
    expect(directLaunch()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(directLaunch()));
    expect(directLaunch()).toHaveAttribute('aria-checked', 'false');
    expect(savedSettings(daemon)).toEqual([
      ['direct_agent_launch_enabled', 'true'],
      ['direct_agent_launch_enabled', 'false'],
    ]);
  });
});

describe('Experimental settings for the one-screen launcher', () => {
  it('saves the launcher opt-in in both directions', async () => {
    const daemon = await openSection('experimental');
    const launcher = () => screen.getByRole('switch', { name: 'Launcher 2.0' });
    expect(launcher()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(launcher()));
    expect(launcher()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(launcher()));
    expect(launcher()).toHaveAttribute('aria-checked', 'false');
    expect(savedSettings(daemon)).toEqual([
      ['new_session_launcher_enabled', 'true'],
      ['new_session_launcher_enabled', 'false'],
    ]);
  });
});
