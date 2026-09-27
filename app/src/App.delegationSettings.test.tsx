import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { emptySelection, openDelegationSettings, type Role } from './test/delegationDaemon';
import type { CommandMessage, EventMessage } from './test/protocol';
import { gesture } from './test/renderApp';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';

type Selection = Role['choices'][number]['selection'];
type Harness = NonNullable<EventMessage<'delegation_preferences_result'>['harnesses']>[number];
type Model = NonNullable<EventMessage<'delegation_models_result'>['models']>[number];

const harnesses: Harness[] = [
  { id: 'claude', name: 'Claude Code', available: true, model_pin: true, effort_pin: true, discovery: true },
  { id: 'copilot', name: 'Copilot', available: true, model_pin: false, effort_pin: false, discovery: false },
  { id: 'pi', name: 'Pi', available: true, model_pin: true, effort_pin: true, discovery: false },
  { id: 'plug', name: 'Plug', available: true, model_pin: false, effort_pin: true, discovery: false },
  { id: 'socket', name: 'Socket', available: true, model_pin: false, effort_pin: true, discovery: false },
];

function model(id: string, name: string, overrides: Partial<Model> = {}): Model {
  return { harness: 'claude', provider: '', id, name, description: '', detail: '', effort_support: 'supported', effort_levels: [], access: 'unknown', ...overrides };
}

const claudeModels = [
  model('opus', 'Opus', { description: 'Deep work', effort_levels: ['medium', 'high'] }),
  model('haiku', 'Haiku', { effort_support: 'unsupported' }),
  model('sonnet', 'Sonnet'),
];

const catalog = (): Reply => ({ event: 'delegation_models_result', success: true, models: claudeModels, detail: 'Reported by Claude Code' });

function role(selection: Partial<Selection> = {}): Role {
  return {
    id: 'build',
    name: 'Build',
    icon: 'code',
    enabled: true,
    description: 'Implement a change',
    instructions: 'Run relevant tests',
    stopping_point: 'Return for review',
    default_choice_id: 'default',
    choices: [{ id: 'default', name: 'Everyday', when: '', selection: { ...emptySelection(), ...selection } }],
  };
}

interface Discovery {
  held: CommandMessage<'delegation_models'>[];
  release(reply?: Reply): Promise<void>;
}

async function openPicker(selection: Partial<Selection> = {}, { holdDiscovery = false } = {}) {
  const settings = await openDelegationSettings({ roles: [role(selection)], harnesses });
  const { daemon } = settings;
  const held: CommandMessage<'delegation_models'>[] = [];
  daemon.on('delegation_models', (request) => (holdDiscovery ? void held.push(request) : catalog()));
  const discovery: Discovery = {
    held,
    release: (reply = catalog()) => gesture(daemon, () => {
      for (const request of held.splice(0)) daemon.replyTo(request, { ...reply, request_id: request.request_id } as Reply);
    }),
  };
  await openPopover(daemon);
  return { ...settings, discovery, selection: () => settings.lastSaved().roles[0].choices[0].selection };
}

const opener = () => screen.getByRole('button', { name: 'Model for Build' });
const picker = () => screen.queryByRole('dialog', { name: 'Choose a model' });
const inPicker = () => within(picker()!);
const discoveries = (daemon: ScriptedDaemon) => daemon.sentOf('delegation_models').map((request) => request.harness);

async function openPopover(daemon: ScriptedDaemon) {
  opener().focus();
  await gesture(daemon, () => fireEvent.click(opener()));
}

async function click(daemon: ScriptedDaemon, element: HTMLElement) {
  await gesture(daemon, () => fireEvent.click(element));
}

async function escape(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' }));
}

async function enterModel(daemon: ScriptedDaemon, id: string, provider?: string) {
  fireEvent.click(inPicker().getByRole('button', { name: 'Enter a model ID' }));
  if (provider !== undefined) fireEvent.change(inPicker().getByLabelText('Provider'), { target: { value: provider } });
  fireEvent.change(inPicker().getByLabelText('Model ID'), { target: { value: id } });
  const use = inPicker().getByRole('button', { name: 'Use it' });
  use.focus();
  await click(daemon, use);
}

async function typeEffort(daemon: ScriptedDaemon, effort: string) {
  const field = inPicker().getByLabelText('Effort');
  field.focus();
  fireEvent.change(field, { target: { value: effort } });
  await gesture(daemon, () => fireEvent.blur(field));
}

describe('App delegation settings', () => {
  it('discovers a harness’s models once, refreshes on demand, and saves the model and then its effort', async () => {
    const { daemon, saves, selection } = await openPicker({ harness: 'claude' });
    expect(discoveries(daemon)).toEqual(['claude']);
    expect(inPicker().getByText('Reported by Claude Code')).toBeInTheDocument();
    expect(inPicker().queryByRole('group', { name: 'Effort' })).toBeNull();

    await click(daemon, inPicker().getByRole('option', { name: /Opus/ }));
    expect(selection()).toEqual({ harness: 'claude', provider: '', model: 'opus', effort: '' });
    await click(daemon, inPicker().getByRole('button', { name: 'high' }));
    expect(selection()).toEqual({ harness: 'claude', provider: '', model: 'opus', effort: 'high' });

    await click(daemon, inPicker().getByRole('option', { name: /Haiku/ }));
    expect(selection()).toEqual({ harness: 'claude', provider: '', model: 'haiku', effort: '' });
    expect(inPicker().queryByRole('group', { name: 'Effort' })).toBeNull();
    expect(saves()).toHaveLength(3);
    expect(discoveries(daemon)).toEqual(['claude']);

    await click(daemon, inPicker().getByRole('button', { name: 'refresh' }));
    expect(discoveries(daemon)).toEqual(['claude', 'claude']);
  });

  it('keeps what it discovered across a reopen, and lets an exact model id through', async () => {
    const { daemon, selection } = await openPicker({ harness: 'claude' });
    await escape(daemon);
    expect(picker()).toBeNull();

    await openPopover(daemon);
    expect(inPicker().getByRole('option', { name: /Opus/ })).toBeInTheDocument();
    expect(discoveries(daemon)).toEqual(['claude']);

    await enterModel(daemon, ' claude-next ');
    expect(selection()).toEqual({ harness: 'claude', provider: '', model: 'claude-next', effort: '' });
    expect(inPicker().queryByRole('button', { name: 'Use it' })).toBeNull();
    expect(picker()!.contains(document.activeElement)).toBe(true);
  });

  it('waits on the running discovery when reopened, and shows its models once it answers', async () => {
    const { daemon, discovery } = await openPicker({ harness: 'claude' }, { holdDiscovery: true });
    expect(inPicker().getByLabelText('Discovering models')).toBeInTheDocument();
    await escape(daemon);

    await openPopover(daemon);
    expect(inPicker().getByLabelText('Discovering models')).toBeInTheDocument();
    expect(discoveries(daemon)).toEqual(['claude']);

    await discovery.release();
    expect(inPicker().getByRole('option', { name: /Opus/ })).toBeInTheDocument();
    expect(inPicker().queryByLabelText('Discovering models')).toBeNull();
  });

  it('holds the effort levels of a pinned model until discovery says what they are', async () => {
    const { discovery } = await openPicker({ harness: 'claude', model: 'opus' }, { holdDiscovery: true });
    expect(inPicker().queryByLabelText('Effort')).toBeNull();

    await discovery.release();

    expect(inPicker().getByRole('button', { name: 'high' })).toBeInTheDocument();
  });

  it('says a harness has no models to report apart from failing to discover them', async () => {
    const empty = await openPicker({ harness: 'claude' }, { holdDiscovery: true });
    await empty.discovery.release({ event: 'delegation_models_result', success: true, models: [], detail: 'No discovery API' });
    expect(inPicker().getByText('No discovery API')).toBeInTheDocument();
    expect(inPicker().queryByRole('alert')).toBeNull();
  });

  it('shows why discovery failed', async () => {
    const failed = await openPicker({ harness: 'claude' }, { holdDiscovery: true });

    await failed.discovery.release({ event: 'delegation_models_result', success: false, error: 'Harness unavailable', models: [], detail: '' });

    expect(inPicker().getByRole('alert')).toHaveTextContent('Harness unavailable');
  });

  it('asks a plugin harness for a provider with a hand-entered model, and never discovers for it', async () => {
    const { daemon, selection } = await openPicker({ harness: 'claude' });
    fireEvent.click(inPicker().getByRole('button', { name: 'Enter a model ID' }));
    expect(inPicker().queryByLabelText('Provider')).toBeNull();

    await click(daemon, inPicker().getByRole('option', { name: 'Pi' }));
    await enterModel(daemon, 'gpt-next', ' openai ');

    expect(selection()).toEqual({ harness: 'pi', provider: 'openai', model: 'gpt-next', effort: '' });
    expect(discoveries(daemon)).toEqual(['claude']);
  });

  it('saves a harness that picks its own model on its own, without discovering', async () => {
    const { daemon, selection } = await openPicker();
    expect(inPicker().getByText('Pick a harness to see its models. None leaves this route unset.')).toBeInTheDocument();

    await click(daemon, inPicker().getByRole('option', { name: 'Copilot' }));

    expect(selection()).toEqual({ harness: 'copilot', provider: '', model: '', effort: '' });
    expect(discoveries(daemon)).toEqual([]);
  });

  it('clears an assigned harness back to nothing and closes', async () => {
    const { daemon, selection } = await openPicker({ harness: 'claude', model: 'opus', effort: 'high' });

    await click(daemon, inPicker().getByRole('option', { name: 'None' }));

    expect(selection()).toEqual(emptySelection());
    expect(picker()).toBeNull();
  });

  it('offers effort for a harness that picks its own model, and for a harness’s default model', async () => {
    const { daemon, selection } = await openPicker({ harness: 'plug' });
    expect(inPicker().getByText(/Plug uses the model selected in its own settings/)).toBeInTheDocument();

    await typeEffort(daemon, 'high');
    expect(selection()).toEqual({ harness: 'plug', provider: '', model: '', effort: 'high' });

    await click(daemon, inPicker().getByRole('option', { name: 'Claude Code' }));
    await typeEffort(daemon, 'max');
    expect(selection()).toEqual({ harness: 'claude', provider: '', model: '', effort: 'max' });
    expect(discoveries(daemon)).toEqual(['claude']);
  });

  it('starts the effort field empty when the harness changes under it', async () => {
    const { daemon, saves, selection } = await openPicker({ harness: 'plug' });
    fireEvent.change(inPicker().getByLabelText('Effort'), { target: { value: 'high' } });

    await click(daemon, inPicker().getByRole('option', { name: 'Socket' }));
    expect(selection()).toEqual({ harness: 'socket', provider: '', model: '', effort: '' });
    const effort = inPicker().getByLabelText('Effort');
    expect(effort).toHaveValue('');

    await gesture(daemon, () => fireEvent.blur(effort));
    expect(saves()).toHaveLength(1);
  });

  it('shows the new model’s effort after a switch instead of the one typed for the previous model', async () => {
    const { daemon, saves } = await openPicker({ harness: 'claude', model: 'sonnet', effort: 'high' });
    expect(inPicker().getByLabelText('Effort')).toHaveValue('high');

    await enterModel(daemon, 'custom-model');
    const effort = inPicker().getByLabelText('Effort');
    expect(effort).toHaveValue('');
    await gesture(daemon, () => fireEvent.blur(effort));

    expect(saves()).toHaveLength(1);
  });

  it('saves a typed effort when a click outside closes the picker', async () => {
    const { daemon, selection } = await openPicker({ harness: 'claude', model: 'sonnet' });
    const effort = inPicker().getByLabelText('Effort');
    effort.focus();
    fireEvent.change(effort, { target: { value: ' max ' } });

    await gesture(daemon, () => fireEvent.mouseDown(document.body));

    expect(selection()).toEqual({ harness: 'claude', provider: '', model: 'sonnet', effort: 'max' });
    expect(picker()).toBeNull();
  });

  it('keeps keyboard focus on the effort level just picked', async () => {
    const { daemon } = await openPicker({ harness: 'claude', model: 'opus' });
    const high = inPicker().getByRole('button', { name: 'high' });
    high.focus();

    await click(daemon, high);

    expect(inPicker().getByRole('button', { name: 'high' })).toHaveAttribute('aria-pressed', 'true');
    expect(inPicker().getByRole('button', { name: 'high' })).toHaveFocus();
  });

  it('gives focus back to the button that opened it when Escape closes it', async () => {
    const { daemon } = await openPicker({ harness: 'claude' });
    expect(inPicker().getByRole('option', { name: 'Claude Code' })).toHaveFocus();

    await escape(daemon);

    expect(picker()).toBeNull();
    expect(opener()).toHaveFocus();
  });

  it('closes when keyboard focus leaves it for a control behind it, and keeps focus when it only drops to the page', async () => {
    const { daemon } = await openPicker({ harness: 'claude' });
    const inside = inPicker().getByRole('option', { name: /Opus/ });
    inside.focus();

    fireEvent.blur(inside, { relatedTarget: inPicker().getByRole('button', { name: 'refresh' }) });
    expect(picker()).not.toBeNull();

    act(() => inside.blur());
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(inPicker().getByRole('option', { name: 'Claude Code' })).toHaveFocus();

    await gesture(daemon, () => fireEvent.blur(inPicker().getByRole('option', { name: 'Claude Code' }), { relatedTarget: opener() }));
    expect(picker()).toBeNull();
  });
});
