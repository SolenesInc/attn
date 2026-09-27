import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from './test/renderApp';
import { openSection, savedSettings } from './test/settings';

const headless = {
  codex_cap_headless_task: 'true',
  claude_cap_headless_task: 'true',
};

function openActivity(settings: Record<string, string> = {}) {
  return openSection('backgroundAgents', { settings: { ...headless, ...settings } });
}

const activity = () => within(screen.getByRole('heading', { name: 'Session activity' }).closest('section')!);
const field = (label: string) => activity().getByLabelText(label);
const toggle = () => activity().getByTestId('settings-activity-toggle');

describe('App session activity settings', () => {
  it('refuses to turn activity lines on until an agent is saved', async () => {
    const daemon = await openActivity();
    expect(toggle()).toBeDisabled();

    await gesture(daemon, () => fireEvent.change(field('Agent'), { target: { value: 'codex' } }));
    await gesture(daemon, () => fireEvent.click(toggle()));

    expect(savedSettings(daemon)).toEqual([
      ['activity.config', '{"agent":"codex"}'],
      ['activity.enabled', 'true'],
    ]);
    expect(toggle()).toHaveTextContent('Disable');
  });

  it('saves the agent, model and effort as one config and both cadences as one interval pair', async () => {
    const daemon = await openActivity();

    await gesture(daemon, () => fireEvent.change(field('Agent'), { target: { value: 'codex' } }));
    await gesture(daemon, () => fireEvent.change(field('Model'), { target: { value: 'gpt-5.6-luna' } }));
    await gesture(daemon, () => fireEvent.change(field('Reasoning effort'), { target: { value: 'low' } }));
    fireEvent.change(field('Refresh on home (seconds)'), { target: { value: '90' } });
    fireEvent.change(field('Refresh elsewhere in the app (seconds)'), { target: { value: '600' } });
    await gesture(daemon, () => fireEvent.blur(field('Refresh elsewhere in the app (seconds)')));

    expect(savedSettings(daemon)).toEqual([
      ['activity.config', '{"agent":"codex"}'],
      ['activity.config', '{"agent":"codex","model":"gpt-5.6-luna"}'],
      ['activity.config', '{"agent":"codex","model":"gpt-5.6-luna","effort":"low"}'],
      ['activity.intervals', '{"watching":90,"present":600}'],
    ]);
  });

  it('offers reasoning effort only for the agent where it changes anything', async () => {
    const daemon = await openActivity();

    await gesture(daemon, () => fireEvent.change(field('Agent'), { target: { value: 'claude' } }));
    expect(activity().queryByLabelText('Reasoning effort')).toBeNull();

    await gesture(daemon, () => fireEvent.change(field('Agent'), { target: { value: 'codex' } }));
    expect(field('Reasoning effort')).toBeInTheDocument();
  });

  it('drops the model when the agent changes', async () => {
    const daemon = await openActivity({ 'activity.config': '{"agent":"claude","model":"sonnet"}' });

    await gesture(daemon, () => fireEvent.change(field('Agent'), { target: { value: 'codex' } }));

    expect(savedSettings(daemon)).toEqual([['activity.config', '{"agent":"codex"}']]);
    expect(field('Model')).toHaveValue('');
  });

  it('saves a custom model once the user leaves the field', async () => {
    const daemon = await openActivity({ 'activity.config': '{"agent":"claude"}' });
    fireEvent.change(field('Model'), { target: { value: 'custom' } });
    fireEvent.change(field('Custom model'), { target: { value: 'claude-opus-5' } });

    await gesture(daemon, () => fireEvent.blur(field('Custom model')));

    expect(savedSettings(daemon)).toEqual([['activity.config', '{"agent":"claude","model":"claude-opus-5"}']]);
  });

  it('keeps settings open on a cadence out of range, saving nothing until it is fixed', async () => {
    const daemon = await openActivity();
    fireEvent.change(field('Refresh on home (seconds)'), { target: { value: '0' } });

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-close')));

    expect(screen.getByTestId('settings-modal')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('watching refresh must be a whole number');
    expect(field('Refresh on home (seconds)')).toHaveValue(0);
    expect(savedSettings(daemon)).toEqual([]);

    fireEvent.change(field('Refresh on home (seconds)'), { target: { value: '90' } });
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-close')));

    expect(screen.queryByTestId('settings-modal')).toBeNull();
    expect(savedSettings(daemon)).toEqual([['activity.intervals', '{"watching":90,"present":300}']]);
  });

  it('does not claim a presence tier from the settings it was handed', async () => {
    await openActivity({ 'activity.presence_tier': 'away' });

    expect(activity().queryByText(/away/i)).toBeNull();
  });
});
