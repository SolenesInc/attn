import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from '../test/renderApp';
import { openSection, savedSettings } from '../test/settings';

const ownPullRequestComments = () => screen.getByRole('switch', { name: 'Ring on my own PR comments' });
const resumeChoice = (name: string) => within(screen.getByRole('group', { name: 'Resume for' })).getByRole('button', { name });
const verdicts = () =>
  screen
    .getAllByRole('listitem')
    .filter((item) => /^(Resumes|Waits)/.test(item.textContent ?? ''))
    .map((item) => item.textContent);

describe('Settings Agents: Seeds on standby', () => {
  it('rings on my own PR comments only after the switch is turned on, and can turn it off again', async () => {
    const daemon = await openSection('agents', { settings: { 'pull_requests.own_comments': 'quiet' } });

    expect(screen.getByRole('heading', { name: 'Seeds on standby' })).toBeInTheDocument();
    expect(ownPullRequestComments()).toHaveAccessibleDescription(
      'Enable if you want to give feedback to agents by writing comments on the PR',
    );
    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'false');

    await gesture(daemon, () => fireEvent.click(ownPullRequestComments()));

    expect(savedSettings(daemon)).toEqual([['pull_requests.own_comments', 'ring']]);
    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'true');

    await gesture(daemon, () => fireEvent.click(ownPullRequestComments()));

    expect(savedSettings(daemon)).toEqual([
      ['pull_requests.own_comments', 'ring'],
      ['pull_requests.own_comments', 'quiet'],
    ]);
    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'false');
  });

  it('shows the switch on when the profile rings, and turning it off saves quiet', async () => {
    const daemon = await openSection('agents', { settings: { 'pull_requests.own_comments': 'ring' } });

    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'true');

    await gesture(daemon, () => fireEvent.click(ownPullRequestComments()));

    expect(savedSettings(daemon)).toEqual([['pull_requests.own_comments', 'quiet']]);
    expect(ownPullRequestComments()).toHaveAttribute('aria-checked', 'false');
  });

  it('resumes a standby agent for any mail until the profile picks PRs and reminders, or never', async () => {
    const daemon = await openSection('agents', { settings: { 'garden.tender_resume': 'any' } });

    expect(resumeChoice('Any mail')).toHaveAttribute('aria-pressed', 'true');
    expect(verdicts()).toEqual([
      'ResumesPR #123 got approved',
      'ResumesIts reminder came up',
      'ResumesAnother agent messaged the seed',
      'ResumesYou answered its ask',
    ]);

    await gesture(daemon, () => fireEvent.click(resumeChoice('PRs and reminders')));

    expect(savedSettings(daemon)).toEqual([['garden.tender_resume', 'watches']]);
    expect(resumeChoice('PRs and reminders')).toHaveAttribute('aria-pressed', 'true');
    expect(verdicts()).toEqual([
      'ResumesPR #123 got approved',
      'ResumesIts reminder came up',
      'WaitsAnother agent messaged the seed',
      'WaitsYou answered its ask',
    ]);

    await gesture(daemon, () => fireEvent.click(resumeChoice('Never')));

    expect(savedSettings(daemon)).toEqual([
      ['garden.tender_resume', 'watches'],
      ['garden.tender_resume', 'off'],
    ]);
    expect(verdicts()).toEqual([
      'WaitsPR #123 got approved',
      'WaitsIts reminder came up',
      'WaitsAnother agent messaged the seed',
      'WaitsYou answered its ask',
    ]);
    expect(screen.getByText('attn always starts a session for a crew member who tends the seed, whatever you pick.')).toBeInTheDocument();
  });

  it('shows the saved resume choice, and treats a profile that has none as any mail', async () => {
    await openSection('agents', { settings: { 'garden.tender_resume': 'off' } });
    expect(resumeChoice('Never')).toHaveAttribute('aria-pressed', 'true');
    expect(resumeChoice('Any mail')).toHaveAttribute('aria-pressed', 'false');
  });

  it('starts on any mail when the profile has not chosen', async () => {
    await openSection('agents');
    expect(resumeChoice('Any mail')).toHaveAttribute('aria-pressed', 'true');
  });
});
