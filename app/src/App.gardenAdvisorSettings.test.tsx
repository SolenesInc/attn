import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSection, savedSettings } from './test/settings';
import { harnesses, serveHarnessCatalogs } from './test/harnessCatalogs';
import { gesture } from './test/renderApp';
import { openRoute, previewHarness, pickModel, pickEffort, enterModel, routeDialog } from './test/harnessRoute';

const initial = { codex_cap_headless_task: 'true', claude_cap_headless_task: 'true', copilot_cap_headless_task: 'true' };
const openAdvisor = (settings: Record<string,string> = {}) => openSection('backgroundAgents', { settings: { ...initial, ...settings } });
const advisor = () => within(screen.getByRole('heading', { name: 'Garden advisor' }).closest('section')!);

describe('App Garden advisor settings', () => {
  it.each([
    ['activity.config', 'Session activity', 'Session activity model'],
    ['garden.advisor', 'Garden advisor', 'Garden advisor model'],
  ])('keeps %s editable after a harness-list failure and retains edits through Retry', async (setting, title, label) => {
    const daemon = await openSection('backgroundAgents', { settings: { ...initial, [setting]: '{"agent":"codex","model":"saved-model","effort":"low"}' } }, daemon => {
      daemon.on('delegation_preferences_get', () => ({ event: 'delegation_preferences_result', success: false, error: 'Harness list unavailable', preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } }, harnesses: [], templates: [] }));
    });
    const section = within(screen.getByRole('heading', { name: title }).closest('section')!);
    expect(section.getByRole('alert')).toHaveTextContent('Harness list unavailable');
    await openRoute(daemon, label);
    await enterModel(daemon, 'manual-model');
    expect(JSON.parse(savedSettings(daemon).slice(-1)[0][1]).model).toBe('manual-model');
    await previewHarness(daemon, 'Claude');
    await pickModel(daemon, 'Light default → Haiku 5.5');
    if (screen.queryByRole('dialog', { name: 'Choose a model' })) await gesture(daemon, () => fireEvent.mouseDown(document.body));
    expect(JSON.parse(savedSettings(daemon).slice(-1)[0][1]).agent).toBe('claude');
    serveHarnessCatalogs(daemon);
    await gesture(daemon, () => fireEvent.click(section.getByRole('button', { name: 'Retry' })));
    expect(section.queryByRole('alert')).toBeNull();
    expect(section.getByRole('button', { name: label })).toHaveTextContent('Claude');
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(2);
  });
  it('keeps an unavailable saved advisor visible after a harness-list failure without enabling it', async () => {
    const daemon = await openSection('backgroundAgents', { settings: { ...initial, codex_available: 'false', 'garden.advisor': '{"agent":"codex","model":"saved-model"}' } }, daemon => {
      daemon.on('delegation_preferences_get', () => ({ event: 'delegation_preferences_result', success: false, error: 'Harness list unavailable', preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } }, harnesses: [], templates: [] }));
    });
    await openRoute(daemon, 'Garden advisor model');
    expect(routeDialog().getByRole('option', { name: /^Codex/ })).toBeDisabled();
    expect(advisor().getByRole('button', { name: 'Garden advisor model' })).toHaveTextContent('saved-model');
    expect(savedSettings(daemon)).toEqual([]);
  });
  it.each(['codex', 'claude'])('keeps the %s model unpinned when only effort changes', async agent => {
    const daemon = await openAdvisor({ 'garden.advisor': JSON.stringify({ agent, model: '', effort: '' }) });
    expect(savedSettings(daemon)).toEqual([]);
    await openRoute(daemon, 'Garden advisor model');
    await pickEffort(daemon, 'high');
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['garden.advisor', JSON.stringify({ agent, model: '', effort: 'high' })]);
  });
  it('clears existing model and effort pins back to their discovered defaults', async () => {
    const daemon = await openAdvisor({ 'garden.advisor': '{"agent":"codex","model":"gpt-6-luna","effort":"high"}' });
    await openRoute(daemon, 'Garden advisor model');
    await pickModel(daemon, 'Light default → gpt-6-luna');
    expect(JSON.parse(savedSettings(daemon).slice(-1)[0][1])).toEqual({ agent: 'codex', model: '', effort: 'high' });
    await pickEffort(daemon, 'default');
    expect(JSON.parse(savedSettings(daemon).slice(-1)[0][1])).toEqual({ agent: 'codex', model: '' });
  });
  it('shows the discovered light default at the retained Codex default effort', async () => {
    const daemon = await openAdvisor();
    expect(advisor().getByRole('button', { name: 'Garden advisor model' })).toHaveTextContent('Light default → gpt-6-luna');
    expect(advisor().getByRole('button', { name: 'Garden advisor model' })).toHaveTextContent('xhigh');
    expect(savedSettings(daemon)).toEqual([]);
  });
  it('browses Claude without changing the stored route and saves its selected light default', async () => {
    const daemon = await openAdvisor();
    await openRoute(daemon, 'Garden advisor model');
    await previewHarness(daemon, 'Claude');
    expect(savedSettings(daemon)).toEqual([]);
    await pickModel(daemon, 'Light default → Haiku 5.5');
    await pickEffort(daemon, 'default (medium)');
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['garden.advisor', '{"agent":"claude","model":""}']);
  });
  it('saves an explicit model and effort without changing the tier attached to the model', async () => {
    const daemon = await openAdvisor();
    await openRoute(daemon, 'Garden advisor model');
    await pickModel(daemon, 'gpt-6-luna');
    await pickEffort(daemon, 'max');
    const field = advisor().getByRole('button', { name: 'Garden advisor model' });
    expect(field).toHaveTextContent('light');
    expect(field).toHaveTextContent('max');
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['garden.advisor', '{"agent":"codex","model":"gpt-6-luna","effort":"max"}']);
  });
  it('keeps Copilot as free text with no discovered model or effort list', async () => {
    const daemon = await openSection('backgroundAgents', { settings: { ...initial, 'garden.advisor': '{"agent":"copilot","model":"custom-copilot","effort":"future-effort"}' } }, daemon => serveHarnessCatalogs(daemon, harnesses.map(harness => harness.id === 'copilot' ? { ...harness, model_pin: false, effort_pin: false } : harness)));
    await openRoute(daemon, 'Garden advisor model');
    expect(routeDialog().getByRole('textbox', { name: 'Effort' })).toHaveValue('future-effort');
    await enterModel(daemon, 'another-model');
    const effort = routeDialog().getByRole('textbox', { name: 'Effort' });
    fireEvent.change(effort, { target: { value: 'new-effort' } });
    await gesture(daemon, () => fireEvent.blur(effort));
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['garden.advisor', '{"agent":"copilot","model":"another-model","effort":"new-effort"}']);
  });
});
