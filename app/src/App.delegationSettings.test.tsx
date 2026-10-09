import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { emptySelection, openDelegationSettings, type Role } from './test/delegationDaemon';
import type { CommandMessage, EventMessage } from './test/protocol';
import { gesture } from './test/renderApp';
import { openRoute, previewHarness, pickModel, pickEffort, enterModel, routeDialog } from './test/harnessRoute';
import type { Reply } from './test/scriptedDaemon';

type Selection = Role['choices'][number]['selection'];
type Model = EventMessage<'harness_models_result'>['models'][number];
const harnesses = [
  { id: 'claude', name: 'Claude', available: true, model_pin: true, effort_pin: true, discovery: true },
  { id: 'copilot', name: 'Copilot', available: true, model_pin: false, effort_pin: false, discovery: false },
  { id: 'pi', name: 'Pi', available: true, model_pin: true, effort_pin: true, discovery: false },
];
const model = (id: string, name: string, tier: Model['tier'], efforts: string[] = ['medium', 'high']): Model => ({ harness: 'claude', provider: '', id, name, tier, shipped_tier: tier, tier_source: id === 'default' ? 'alias' : tier ? 'shipped' : 'none', description: '', detail: '', effort_support: efforts.length ? 'supported' : 'unsupported', effort_levels: efforts, access: 'unknown' });
const catalog: Reply = { event: 'harness_models_result', success: true, tier_defaults: { deep: 'opus', standard: 'sonnet', light: 'haiku' }, models: [model('default', 'Default (recommended)', undefined), model('opus', 'Opus', 'deep'), model('deep-two', 'Second deep', 'deep'), model('haiku', 'Haiku', 'light', []), model('sonnet', 'Sonnet', 'standard', []), model('new-id', 'New release', undefined)], detail: 'Reported by Claude' };
const role = (selection: Partial<Selection>): Role => ({ id: 'build', name: 'Build', icon: 'code', enabled: true, description: 'Implement a change', instructions: 'Run relevant tests', stopping_point: 'Return for review', default_choice_id: 'default', choices: [{ id: 'default', name: 'Everyday', when: '', selection: { ...emptySelection(), ...selection } }] });
async function picker(selection: Partial<Selection> = {}, held = false) {
  const settings = await openDelegationSettings({ roles: [role(selection)], harnesses });
  const requests: CommandMessage<'harness_models'>[] = [];
  settings.daemon.on('harness_models', request => held ? void requests.push(request) : catalog);
  await openRoute(settings.daemon, 'Model for Build');
  return { ...settings, requests, selection: () => settings.lastSaved().roles[0].choices[0].selection };
}
const opener = () => screen.getByRole('button', { name: 'Model for Build' });
const close = (daemon: Awaited<ReturnType<typeof picker>>['daemon']) => gesture(daemon, () => fireEvent.keyDown(screen.getByRole('dialog', { name: 'Choose a model' }), { key: 'Escape' }));

describe('App delegation settings', () => {
  it('shows current availability while permitting role configuration for a missing executable', async () => {
    const { daemon } = await picker({ harness: 'claude' });
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { claude_available: 'true' } }));
    const reads = daemon.sentOf('delegation_preferences_get').length;
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { claude_available: 'false' } }));
    const option = within(routeDialog().getByRole('listbox', { name: 'Harness' })).getByRole('option', { name: /^Claude/ });
    expect(option).toBeEnabled();
    expect(within(option).getByTitle('Unavailable on this daemon')).toBeInTheDocument();
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(reads);
  });
  it('retains confirmed harness choices when a capability refresh fails', async () => {
    const { daemon, server, saves } = await picker({ harness: 'claude', model: 'opus', effort: 'high' });
    server.failLoads('Harness refresh unavailable');
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { claude_cap_model_discovery: 'false' } }));
    expect(within(routeDialog().getByRole('listbox', { name: 'Harness' })).getByRole('option', { name: /^Pi/ })).toBeInTheDocument();
    await previewHarness(daemon, 'Pi');
    expect(saves()).toEqual([]);
    await enterModel(daemon, 'openai/gpt-next');
    expect(server.preferences.roles[0].choices[0].selection).toEqual({ harness: 'pi', provider: 'openai', model: 'gpt-next', effort: '' });
  });
  it('uses one discovery across reopen, and Refresh sends an explicit rediscovery request', async () => {
    const { daemon } = await picker({ harness: 'claude' });
    expect(daemon.sentOf('harness_models')).toHaveLength(1);
    await close(daemon);
    await openRoute(daemon, 'Model for Build');
    expect(daemon.sentOf('harness_models')).toHaveLength(1);
    await gesture(daemon, () => fireEvent.click(routeDialog().getByRole('button', { name: 'Refresh' })));
    expect(daemon.sentOf('harness_models').slice(-1)[0]).toMatchObject({ harness: 'claude', refresh: true });
  });
  it('groups models by tier while preserving reported order inside each group, with the alias first', async () => {
    await picker({ harness: 'claude' });
    const options = within(routeDialog().getByRole('listbox', { name: 'Model' })).getAllByRole('option');
    expect(options).toHaveLength(7);
    expect(options[0]).toHaveTextContent('Claude default');
    expect(options[1]).toHaveTextContent('Default (recommended)');
    expect(options[2]).toHaveTextContent('Opus');
    expect(options[3]).toHaveTextContent('Second deep');
    expect(options[4]).toHaveTextContent('Sonnet');
    expect(options[5]).toHaveTextContent('Haiku');
    expect(options[6]).toHaveTextContent('New release');
    expect(options[6]).toHaveTextContent('no tier');
  });
  it('saves the model and effort as complete routes and closes after effort selection', async () => {
    const { daemon, selection } = await picker({ harness: 'claude' });
    await pickModel(daemon, 'Opus');
    expect(selection()).toEqual({ harness: 'claude', provider: '', model: 'opus', effort: '' });
    await pickEffort(daemon, 'high');
    expect(selection()).toEqual({ harness: 'claude', provider: '', model: 'opus', effort: 'high' });
    expect(screen.queryByRole('dialog', { name: 'Choose a model' })).toBeNull();
    expect(opener()).toHaveFocus();
  });
  it('does not save or wipe the model while browsing another harness', async () => {
    const { daemon, saves } = await picker({ harness: 'claude', model: 'opus', effort: 'high' });
    await previewHarness(daemon, 'Pi');
    expect(saves()).toEqual([]);
    await close(daemon);
    expect(opener()).toHaveTextContent('Opus');
    expect(opener()).toHaveTextContent('high');
  });
  it('keeps a stored missing model and effort visible without saving merely on open', async () => {
    const { saves } = await picker({ harness: 'claude', model: 'retired', effort: 'future-effort' });
    expect(routeDialog().getByRole('option', { name: /retired/ })).toHaveAttribute('aria-selected', 'true');
    expect(routeDialog().getByRole('textbox', { name: 'Effort' })).toHaveValue('future-effort');
    expect(saves()).toEqual([]);
  });
  it('uses the substring filter and can save an exact provider-qualified manual model', async () => {
    const { daemon, selection } = await picker({ harness: 'claude' });
    await previewHarness(daemon, 'Pi');
    await enterModel(daemon, 'openai/gpt-next');
    expect(selection()).toEqual({ harness: 'pi', provider: 'openai', model: 'gpt-next', effort: '' });
    expect(daemon.sentOf('harness_models').map(request => request.harness)).toEqual(['claude']);
  });
  it.each([
    ['button', 'vendor/blocked'], ['Enter', 'vendor/blocked'],
    ['button', 'blocked'], ['Enter', 'blocked'],
  ])('refuses a known unsupported manual model through %s for %s while accepting unknown provider identities', async (input, query) => {
    const settings = await openDelegationSettings({ roles: [role({ harness: 'house' })], harnesses: [{ id: 'house', name: 'House', available: true, model_pin: true, effort_pin: true, discovery: true }] });
    settings.daemon.on('harness_models', () => ({ ...catalog, models: [
      { ...model('blocked', 'Blocked model', 'deep'), harness: 'house', provider: 'vendor', access: 'unsupported' },
      { ...model('blocked', 'Other provider model', 'deep'), harness: 'house', provider: 'other', access: 'supported' },
    ], tier_defaults: {} }));
    await openRoute(settings.daemon, 'Model for Build');
    const filter = routeDialog().getByRole('textbox', { name: 'Filter models or enter an ID' });
    await gesture(settings.daemon, () => fireEvent.change(filter, { target: { value: query } }));
    if (query === 'blocked') await gesture(settings.daemon, () => fireEvent.change(routeDialog().getByRole('textbox', { name: 'Provider' }), { target: { value: 'vendor' } }));
    const submit = routeDialog().getByRole('button', { name: `Use “${query}”` });
    await gesture(settings.daemon, () => input === 'button' ? fireEvent.click(submit) : fireEvent.keyDown(filter, { key: 'Enter' }));
    expect(settings.saves()).toEqual([]);
    expect(submit).toBeDisabled();
    await gesture(settings.daemon, () => fireEvent.change(routeDialog().getByRole('textbox', { name: 'Provider' }), { target: { value: '' } }));
    const unknownQuery = query === 'blocked' ? 'blocked' : 'unknown/blocked';
    await gesture(settings.daemon, () => fireEvent.change(filter, { target: { value: unknownQuery } }));
    if (query === 'blocked') await gesture(settings.daemon, () => fireEvent.change(routeDialog().getByRole('textbox', { name: 'Provider' }), { target: { value: 'unknown' } }));
    await gesture(settings.daemon, () => input === 'button' ? fireEvent.click(routeDialog().getByRole('button', { name: `Use “${unknownQuery}”` })) : fireEvent.keyDown(filter, { key: 'Enter' }));
    expect(settings.lastSaved().roles[0].choices[0].selection).toEqual({ harness: 'house', provider: 'unknown', model: 'blocked', effort: '' });
  });
  it('commits a harness that chooses its model only when its default row is chosen', async () => {
    const { daemon, saves, selection } = await picker();
    await previewHarness(daemon, 'Copilot');
    expect(saves()).toEqual([]);
    await pickModel(daemon, 'Copilot default');
    expect(selection()).toEqual({ harness: 'copilot', provider: '', model: '', effort: '' });
    expect(daemon.sentOf('harness_models')).toEqual([]);
  });
  it.each(['locked', ''])('offers only the harness default when a plugin discovers models but cannot pin them (stored model %s)', async storedModel => {
    const settings = await openDelegationSettings({ roles: [role({ harness: 'house', model: storedModel, effort: 'high' })], harnesses: [{ id: 'house', name: 'House', available: true, model_pin: false, effort_pin: false, discovery: true }] });
    settings.daemon.on('harness_models', () => ({ ...catalog, models: [{ ...model('locked', 'Locked model', 'deep'), harness: 'house' }], tier_defaults: { deep: 'locked' } }));
    await openRoute(settings.daemon, 'Model for Build');
    expect(opener()).toHaveTextContent(storedModel ? 'Locked model' : 'House default');
    expect(settings.saves()).toEqual([]);
    const options = within(routeDialog().getByRole('listbox', { name: 'Model' })).getAllByRole('option');
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveTextContent('House default');
    expect(routeDialog().queryByRole('textbox', { name: 'Filter models or enter an ID' })).toBeNull();
    await pickModel(settings.daemon, 'House default');
    expect(settings.lastSaved().roles[0].choices[0].selection).toEqual({ harness: 'house', provider: '', model: '', effort: '' });
  });
  it.each([false, true])('keeps effort unconstrained when the harness chooses the model (alias %s)', async alias => {
    const settings = await openDelegationSettings({ roles: [role({ harness: 'house', model: 'pinned', effort: 'high' })], harnesses: [{ id: 'house', name: 'House', available: true, model_pin: true, effort_pin: true, discovery: true }] });
    const models = [model('first', 'First reported', undefined, []), model('pinned', 'Pinned model', 'deep', ['high']), ...(alias ? [model('default', 'Default alias', undefined, ['low'])] : [])].map(entry => ({ ...entry, harness: 'house' }));
    settings.daemon.on('harness_models', () => ({ ...catalog, models, tier_defaults: {} }));
    await openRoute(settings.daemon, 'Model for Build');
    await pickModel(settings.daemon, 'House default');
    expect(settings.lastSaved().roles[0].choices[0].selection).toEqual({ harness: 'house', provider: '', model: '', effort: 'high' });
    expect(routeDialog().getByRole('textbox', { name: 'Effort' })).toHaveValue('high');
    expect(routeDialog().queryByRole('group', { name: 'Effort' })).toBeNull();
  });
  it('shares an in-flight discovery over a close and reopen', async () => {
    const { daemon, requests } = await picker({ harness: 'claude' }, true);
    expect(routeDialog().getByRole('status')).toHaveTextContent('Discovering models');
    await close(daemon);
    await openRoute(daemon, 'Model for Build');
    expect(requests).toHaveLength(1);
    await gesture(daemon, () => daemon.replyTo(requests[0], { ...catalog, request_id: requests[0].request_id }));
    expect(routeDialog().getByRole('option', { name: /^Opus/ })).toBeInTheDocument();
  });
  it('keeps stored effort editable when discovery fails', async () => {
    const { daemon, requests } = await picker({ harness: 'claude', model: 'retired', effort: 'future' }, true);
    await gesture(daemon, () => daemon.replyTo(requests[0], { event: 'harness_models_result', request_id: requests[0].request_id, success: false, error: 'Catalog unavailable', models: [], tier_defaults: {}, detail: '' }));
    expect(routeDialog().getByRole('textbox', { name: 'Effort' })).toHaveValue('future');
    expect(routeDialog().getByRole('alert')).toHaveTextContent('Catalog unavailable');
  });
  it('distinguishes an empty catalog from a failed discovery', async () => {
    const { daemon, requests } = await picker({ harness: 'claude' }, true);
    await gesture(daemon, () => daemon.replyTo(requests[0], { event: 'harness_models_result', success: true, models: [], tier_defaults: {}, detail: 'No discovery API', request_id: requests[0].request_id }));
    expect(routeDialog().getByText('No discovery API')).toBeInTheDocument();
    expect(routeDialog().queryByRole('alert')).toBeNull();
    daemon.on('harness_models', () => ({ event: 'harness_models_result', success: false, error: 'Harness unavailable', models: [], tier_defaults: {}, detail: '' }));
    await gesture(daemon, () => fireEvent.click(routeDialog().getByRole('button', { name: 'Refresh' })));
    expect(routeDialog().getByRole('alert')).toHaveTextContent('Harness unavailable');
  });
  it('clears the route to None and closes', async () => {
    const { daemon, selection } = await picker({ harness: 'claude', model: 'opus' });
    await gesture(daemon, () => fireEvent.click(routeDialog().getByRole('option', { name: 'None' })));
    expect(selection()).toEqual(emptySelection());
    expect(screen.queryByRole('dialog', { name: 'Choose a model' })).toBeNull();
  });
  it('commits a typed effort when outside input closes the picker', async () => {
    const { daemon, selection } = await picker({ harness: 'claude', model: 'retired' });
    const effort = routeDialog().getByRole('textbox', { name: 'Effort' });
    effort.focus(); fireEvent.change(effort, { target: { value: ' max ' } });
    await gesture(daemon, () => fireEvent.mouseDown(document.body));
    expect(selection()).toEqual({ harness: 'claude', provider: '', model: 'retired', effort: 'max' });
    expect(screen.queryByRole('dialog', { name: 'Choose a model' })).toBeNull();
  });
});
