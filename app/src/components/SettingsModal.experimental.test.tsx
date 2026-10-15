import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSection, savedSettings } from '../test/settings';
import { gesture } from '../test/renderApp';

const directLaunch = () => screen.getByRole('switch', { name: 'Direct agent launches' });

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
