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

describe('Experimental settings for the Claude mod', () => {
  it('starts off and saves the setting in both directions', async () => {
    const daemon = await openSection('experimental');
    const toggle = () => screen.getByRole('switch', { name: 'Claude mod' });
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(toggle()));
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(toggle()));
    expect(savedSettings(daemon)).toEqual([['claude_mod_enabled', 'true'], ['claude_mod_enabled', 'false']]);
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

describe('Experimental settings for usage analytics', () => {
  it('starts off and saves the setting in both directions', async () => {
    const daemon = await openSection('experimental');
    const analytics = () => screen.getByRole('switch', { name: 'Usage analytics' });
    expect(analytics()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(analytics()));
    expect(analytics()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(analytics()));
    expect(analytics()).toHaveAttribute('aria-checked', 'false');
    expect(savedSettings(daemon)).toEqual([
      ['analytics.enabled', 'true'],
      ['analytics.enabled', 'false'],
    ]);
  });
});

describe('Experimental settings for crew v2', () => {
  it('starts off and saves the setting in both directions', async () => {
    const daemon = await openSection('experimental');
    const crewV2 = () => screen.getByRole('switch', { name: 'Crew v2' });
    expect(crewV2()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(crewV2()));
    expect(crewV2()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(crewV2()));
    expect(crewV2()).toHaveAttribute('aria-checked', 'false');
    expect(savedSettings(daemon)).toEqual([
      ['crew_v2_enabled', 'true'],
      ['crew_v2_enabled', 'false'],
    ]);
  });
});

describe('Experimental settings for Nisse', () => {
  it('starts off and saves the setting in both directions', async () => {
    const daemon = await openSection('experimental');
    const nisse = () => screen.getByRole('switch', { name: 'Nisse' });
    expect(nisse()).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(nisse()));
    expect(nisse()).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(nisse()));
    expect(nisse()).toHaveAttribute('aria-checked', 'false');
    expect(savedSettings(daemon)).toEqual([
      ['nisse_enabled', 'true'],
      ['nisse_enabled', 'false'],
    ]);
  });
});

describe('Experimental settings for the shelf layout', () => {
  const shelfLayout = () => screen.getByRole('switch', { name: 'Shelf layout' });
  const narrowestWidth = () => screen.queryByLabelText('Narrowest tile width (columns)');

  it('starts off with no width control, and turning it on saves the setting and shows the control', async () => {
    const daemon = await openSection('experimental', { settings: { narrowest_tile_cols: '66' } });
    expect(shelfLayout()).toHaveAttribute('aria-checked', 'false');
    expect(narrowestWidth()).not.toBeInTheDocument();

    await gesture(daemon, () => fireEvent.click(shelfLayout()));
    expect(savedSettings(daemon)).toEqual([['shelf_layout_enabled', 'true']]);
    expect(shelfLayout()).toHaveAttribute('aria-checked', 'true');
    expect(narrowestWidth()).toHaveValue(66);

    await gesture(daemon, () => fireEvent.click(shelfLayout()));
    expect(narrowestWidth()).not.toBeInTheDocument();
  });

  it('saves the narrowest width when focus leaves the field', async () => {
    const daemon = await openSection('experimental', { settings: { shelf_layout_enabled: 'true', narrowest_tile_cols: '66' } });
    const field = narrowestWidth()!;
    fireEvent.change(field, { target: { value: '86' } });
    await gesture(daemon, () => fireEvent.blur(field));
    expect(savedSettings(daemon)).toEqual([['narrowest_tile_cols', '86']]);
    expect(field).toHaveValue(86);
  });
});
