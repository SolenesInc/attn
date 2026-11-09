import { EXPERIMENTAL_FEATURES } from '../utils/settings';
import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSection, savedSettings } from '../test/settings';
import { gesture } from '../test/renderApp';

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

describe('Experimental settings come from the declarations', () => {
  it('offers a switch for every feature and mentions timing only when a change does not apply at once', async () => {
    await openSection('experimental');
    for (const feature of EXPERIMENTAL_FEATURES) {
      const toggle = screen.getByRole('switch', { name: feature.name });
      expect(toggle).toHaveAttribute('aria-checked', 'false');
      expect(screen.getByTestId(`settings-feature-${feature.key}`)).toBe(toggle);
    }
    expect(screen.getByRole('switch', { name: 'Nisse' })).toHaveAccessibleDescription(expect.stringContaining('Starts with the next agent you open.'));
    expect(screen.getByRole('switch', { name: 'Launcher 2.0' })).not.toHaveAccessibleDescription(expect.stringContaining('Starts'));
    expect(screen.queryByText('Now')).toBeNull();
  });

  it('counts the features that are on in each group', async () => {
    const daemon = await openSection('experimental');
    const count = (group: string) => within(screen.getByTestId(`settings-feature-group-${group}`)).getByText((_, element) => element?.tagName === 'P' && / of \d+ on$/.test(element.textContent ?? ''));
    const agents = EXPERIMENTAL_FEATURES.filter(feature => feature.group === 'Agents').length;
    const interfaceCount = EXPERIMENTAL_FEATURES.filter(feature => feature.group === 'Interface').length;
    expect(count('Agents')).toHaveTextContent(`0 of ${agents} on`);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('switch', { name: 'Nisse' })));
    expect(count('Agents')).toHaveTextContent(`1 of ${agents} on`);
    expect(count('Interface')).toHaveTextContent(`0 of ${interfaceCount} on`);
  });

  it('says on the Phones page that turning phone access off disconnects every phone', async () => {
    await openSection('phones');
    expect(screen.getByRole('switch', { name: 'Phone access' })).toHaveAccessibleDescription(expect.stringContaining('Turning it off disconnects every phone.'));
  });

  it('finds a feature page with its flag off', async () => {
    await openSection('experimental');
    fireEvent.change(screen.getByLabelText('Search settings'), { target: { value: 'iphone' } });
    expect(screen.getByTestId('settings-nav-phones')).toBeInTheDocument();
  });
});
