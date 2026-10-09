import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from './test/renderApp';
import { openSection, savedSettings } from './test/settings';
import { openRoute, previewHarness, pickModel, pickEffort, routeDialog, enterModel } from './test/harnessRoute';

const headless = { codex_cap_headless_task: 'true', claude_cap_headless_task: 'true' };
const openActivity = (settings: Record<string, string> = {}) => openSection('backgroundAgents', { settings: { ...headless, ...settings } });
const activity = () => within(screen.getByRole('heading', { name: 'Session activity' }).closest('section')!);
const toggle = () => activity().getByTestId('settings-activity-toggle');

describe('App session activity settings', () => {
  it('keeps activity off while browsing a harness and enables it after a saved light default', async () => {
    const daemon = await openActivity();
    expect(toggle()).toBeDisabled();
    await openRoute(daemon, 'Session activity model');
    await previewHarness(daemon, 'Codex');
    expect(savedSettings(daemon)).toEqual([]);
    await pickModel(daemon, 'Light default → gpt-6-luna');
    await gesture(daemon, () => fireEvent.click(toggle()));
    expect(savedSettings(daemon)).toEqual([['activity.config', '{"agent":"codex"}'], ['activity.enabled', 'true']]);
  });
  it('saves the route as one config and refresh cadences as one interval pair', async () => {
    const daemon = await openActivity();
    await openRoute(daemon, 'Session activity model');
    await previewHarness(daemon, 'Codex');
    await pickModel(daemon, 'gpt-6-luna');
    await pickEffort(daemon, 'low');
    fireEvent.change(activity().getByLabelText('Refresh on home (seconds)'), { target: { value: '90' } });
    fireEvent.change(activity().getByLabelText('Refresh elsewhere in the app (seconds)'), { target: { value: '600' } });
    await gesture(daemon, () => fireEvent.blur(activity().getByLabelText('Refresh elsewhere in the app (seconds)')));
    expect(savedSettings(daemon)).toEqual([['activity.config', '{"agent":"codex","model":"gpt-6-luna"}'], ['activity.config', '{"agent":"codex","model":"gpt-6-luna","effort":"low"}'], ['activity.intervals', '{"watching":90,"present":600}']]);
  });
  it('keeps Claude effort hidden and shows a light recommendation for a deep pick', async () => {
    const daemon = await openActivity({ 'activity.config': '{"agent":"claude","model":"opus"}' });
    expect(activity().getByText('Light recommended')).toBeInTheDocument();
    await openRoute(daemon, 'Session activity model');
    expect(routeDialog().queryByRole('group', { name: 'Effort' })).toBeNull();
    expect(routeDialog().getByText('Light recommended')).toBeInTheDocument();
    await pickModel(daemon, 'Haiku 5.5');
    expect(activity().queryByText('Light recommended')).toBeNull();
  });
  it('retains a stored model missing from discovery and permits an exact manual ID', async () => {
    const daemon = await openActivity({ 'activity.config': '{"agent":"codex","model":"retired-model","effort":"minimal"}' });
    expect(activity().getByRole('button', { name: 'Session activity model' })).toHaveTextContent('retired-model');
    await openRoute(daemon, 'Session activity model');
    expect(routeDialog().getByRole('option', { name: /retired-model/ })).toHaveAttribute('aria-selected', 'true');
    await enterModel(daemon, 'custom-model');
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['activity.config', '{"agent":"codex","model":"custom-model"}']);
  });
  it('does not replace stored configuration merely by opening the controls', async () => {
    const daemon = await openActivity({ 'activity.config': '{"agent":"codex","model":"retired-model","effort":"minimal"}' });
    await openRoute(daemon, 'Session activity model');
    expect(savedSettings(daemon)).toEqual([]);
  });
});
