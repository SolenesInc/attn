import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from '../test/renderApp';
import { openSection, savedSettings } from '../test/settings';

const ownPullRequestComments = () => screen.getByRole('switch', { name: 'Ring on my own PR comments' });

describe('Settings Agents: Seeds on standby', () => {
  it('rings on my own PR comments only after the switch is turned on', async () => {
    const daemon = await openSection('agents');

    expect(screen.getByRole('heading', { name: 'Seeds on standby' })).toBeInTheDocument();
    expect(ownPullRequestComments()).toHaveAccessibleDescription(
      'Enable if you want to give feedback to agents by writing comments on the PR',
    );
    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'false');

    await gesture(daemon, () => fireEvent.click(ownPullRequestComments()));

    expect(savedSettings(daemon)).toEqual([['pull_requests.own_comments', 'ring']]);
    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'true');
  });

  it('shows the switch on when the profile rings, and turning it off saves quiet', async () => {
    const daemon = await openSection('agents', { settings: { 'pull_requests.own_comments': 'ring' } });

    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'true');

    await gesture(daemon, () => fireEvent.click(ownPullRequestComments()));

    expect(savedSettings(daemon)).toEqual([['pull_requests.own_comments', 'quiet']]);
    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'false');
  });

  it('keeps the switch off when the profile is quiet', async () => {
    await openSection('agents', { settings: { 'pull_requests.own_comments': 'quiet' } });

    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'false');
  });
});
