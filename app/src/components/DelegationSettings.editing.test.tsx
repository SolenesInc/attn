import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, expect, it } from 'vitest';
import { DelegationSettings, DelegationSwitch } from './DelegationSettings';
import { clearDelegationModelCatalogs } from '../hooks/useDelegationModelCatalog';
import { useDelegationPreferences } from '../hooks/useDelegationPreferences';
import { createMockDaemon } from '../test/mocks/daemon';
import { useDelegationPreferencesPush } from '../store/delegationPreferences';
import type { DelegationSettingsState, DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import { BuiltinDelegationRole, type DelegationPreferences, type DelegationRole } from '../types/generated';

const selection = () => ({ harness: '', provider: '', model: '', effort: '' });
const custom: DelegationRole = { id: 'build', name: 'Build', icon: 'code', enabled: true, description: 'Implement a change', instructions: 'Run relevant tests', stopping_point: 'Return for review', default_choice_id: 'default', choices: [{ id: 'default', name: 'Everyday', when: '', selection: selection() }] };
const template: DelegationRole = { ...custom, id: 'builder', builtin: BuiltinDelegationRole.Builder, name: '', icon: '', description: '', instructions: '', stopping_point: '' };
const expandedTemplate: DelegationRole = { ...template, name: 'Builder', icon: 'code', description: 'Implement a change\nSecond line', instructions: 'Run relevant tests', stopping_point: 'Return for review' };

const expand = (role: DelegationRole): DelegationRole => role.builtin ? { ...role, name: expandedTemplate.name, icon: expandedTemplate.icon, description: expandedTemplate.description, instructions: expandedTemplate.instructions, stopping_point: expandedTemplate.stopping_point } : role;

function setup(roles: DelegationRole[] = [], enabled = roles.length > 0) {
  let state: DelegationSettingsState = {
    preferences: { enabled, revision: 0, workflow_skill_enabled: false, roles, fallback: { selection: selection(), instructions: '' } },
    templates: [template], expandedRoles: [...roles.map(expand), expandedTemplate], workflowSkillPaths: [],
    harnesses: [{ id: 'codex', name: 'Codex', available: true, model_pin: true, effort_pin: true, discovery: true }],
  };
  const daemon = createMockDaemon();
  daemon.setResponse('load', () => structuredClone(state));
  daemon.setResponse('save', (args: unknown[]) => {
    const value = args[0] as DelegationPreferences;
    if (value.revision !== state.preferences.revision) throw new Error('delegation preferences changed; reload before saving or choosing a role');
    state = { ...state, preferences: { ...structuredClone(value), revision: value.revision + 1 }, expandedRoles: [...value.roles.map(expand), expandedTemplate] };
    return structuredClone(state);
  });
  daemon.setResponse('models', { models: [{ harness: 'codex', provider: '', id: 'model-a', name: 'Everyday model', description: '', detail: '', effort_support: 'supported', effort_levels: ['medium', 'high'], access: 'unknown' }], detail: 'Reported by Codex' });
  const load = daemon.createRequest<DelegationSettingsState>('load');
  const save = daemon.createRequest<DelegationSettingsState>('save');
  const models = daemon.createRequest<DelegationModelCatalog>('models');
  function Harness() { const policy = useDelegationPreferences(true, load, save); return <><DelegationSwitch policy={policy} /><DelegationSettings policy={policy} loadModels={models} /></>; }
  render(<Harness />);
  const saved = () => waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  return { daemon, getState: () => state, saved, bump: () => { state = { ...state, preferences: { ...state.preferences, revision: state.preferences.revision + 1 } }; } };
}

const savesSoFar = (daemon: ReturnType<typeof createMockDaemon>) => daemon.getCalls('save').length;

afterEach(() => { cleanup(); useDelegationPreferencesPush.getState().clear(); clearDelegationModelCatalogs(); });

it('copies a maintained role with its configured model and state, not the template', async () => {
  const configured: DelegationRole = { ...template, enabled: false, choices: [{ id: 'default', name: 'Default', when: '', selection: { harness: 'codex', provider: '', model: 'model-a', effort: 'high' } }] };
  const { daemon, getState } = setup([configured]);
  fireEvent.click(await screen.findByRole('button', { name: 'Builder' }));
  fireEvent.click(screen.getByRole('button', { name: 'Make an editable copy' }));
  await waitFor(() => expect(savesSoFar(daemon)).toBe(1));
  const copy = getState().preferences.roles[1];
  expect(copy.builtin).toBeUndefined();
  expect(copy.name).toBe('Builder (custom)');
  expect(copy.instructions).toBe('Run relevant tests');
  expect(copy.enabled).toBe(false);
  expect(copy.choices[0].selection).toEqual({ harness: 'codex', provider: '', model: 'model-a', effort: 'high' });
});

it('keeps a focused draft when a change made elsewhere reloads the table, and saves it on blur', async () => {
  const { daemon, getState, bump } = setup([custom]);
  fireEvent.click(await screen.findByRole('button', { name: 'Build' }));
  const field = screen.getByLabelText('Instructions') as HTMLTextAreaElement;
  fireEvent.focus(field);
  fireEvent.change(field, { target: { value: 'Typed here' } });
  getState().preferences.roles[0].instructions = 'Changed elsewhere';
  bump();
  act(() => useDelegationPreferencesPush.getState().push(1));
  await waitFor(() => expect(daemon.getCalls('load')).toHaveLength(2));
  expect(field.value).toBe('Typed here');
  fireEvent.blur(field);
  await waitFor(() => expect(savesSoFar(daemon)).toBe(1));
  expect((daemon.getCalls('save')[0].args[0] as DelegationPreferences).revision).toBe(1);
  expect(getState().preferences.roles[0].instructions).toBe('Typed here');
});

it('adopts maintained roles from the empty state with the install flag and shows their guidance read-only', async () => {
  const { daemon, getState } = setup();
  await screen.findByText('No roles yet');
  expect(daemon.getCalls('models')).toHaveLength(0);
  fireEvent.click(screen.getByRole('button', { name: 'Add Attn roles' }));
  await waitFor(() => expect(savesSoFar(daemon)).toBe(1));
  expect(daemon.getCalls('save')[0].args[1]).toBe(true);
  expect(getState().preferences.workflow_skill_enabled).toBe(true);
  expect(getState().preferences.roles[0].builtin).toBe('builder');
  expect(getState().preferences.roles[0].instructions).toBe('');

  const row = await screen.findByRole('button', { name: 'Builder' });
  expect(screen.getByText('Implement a change')).toBeInTheDocument();
  expect(screen.getByText('Needs a model')).toBeInTheDocument();
  fireEvent.click(row);
  expect(screen.getByText('Run relevant tests')).toBeInTheDocument();
  expect(screen.queryByLabelText('Instructions')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Make an editable copy' })).toBeInTheDocument();
});

it('saves a model picked from the row, an alternative with its condition, and the switch', async () => {
  const { daemon, getState } = setup([custom]);
  fireEvent.click(await screen.findByRole('button', { name: 'Model for Build' }));
  expect(screen.getByText('Pick a harness to see its models. None leaves this route unset.')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('option', { name: 'Codex' }));
  await waitFor(() => expect(savesSoFar(daemon)).toBe(1));
  await screen.findByRole('option', { name: /Everyday model/ });
  expect(daemon.getCalls('models').map(c => c.args)).toEqual([['codex']]);
  fireEvent.click(screen.getByRole('option', { name: /Everyday model/ }));
  await waitFor(() => expect(savesSoFar(daemon)).toBe(2));
  fireEvent.click(screen.getByRole('button', { name: 'medium' }));
  await waitFor(() => expect(savesSoFar(daemon)).toBe(3));
  expect(getState().preferences.roles[0].choices[0].selection).toEqual({ harness: 'codex', provider: '', model: 'model-a', effort: 'medium' });
  expect(screen.queryByText('Needs a model')).not.toBeInTheDocument();
  fireEvent.mouseDown(document.body);
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());

  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  fireEvent.click(screen.getByRole('button', { name: '+ Alternative model' }));
  await waitFor(() => expect(savesSoFar(daemon)).toBe(4));
  fireEvent.change(screen.getByLabelText('Name', { selector: '#altname-' + getState().preferences.roles[0].choices[1].id }), { target: { value: 'Hard verification' } });
  fireEvent.blur(screen.getByLabelText('Name', { selector: 'input[id^="altname-"]' }));
  await waitFor(() => expect(savesSoFar(daemon)).toBe(5));
  const when = screen.getByLabelText('When to use this instead of the default');
  fireEvent.change(when, { target: { value: 'Verification is difficult\n\nOr requirements are ambiguous.' } });
  fireEvent.blur(when);
  await waitFor(() => expect(savesSoFar(daemon)).toBe(6));
  const [, alternative] = getState().preferences.roles[0].choices;
  expect(alternative.name).toBe('Hard verification');
  expect(alternative.when).toContain('ambiguous');
  expect(alternative.selection).toEqual({ harness: 'codex', provider: '', model: 'model-a', effort: 'medium' });
  expect(screen.getByText('Verification is difficult')).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  expect(screen.getByRole('button', { name: '+1 alternative' })).toBeInTheDocument();
  expect(screen.getByRole('status', { name: '' }).textContent).toContain('Agents see 1 of 1 role.');

  fireEvent.click(screen.getByRole('switch', { name: 'Delegation preferences' }));
  await waitFor(() => expect(getState().preferences.enabled).toBe(false));
  expect(screen.getByRole('switch', { name: 'Delegation preferences' })).toHaveAttribute('aria-checked', 'false');
  expect(getState().preferences.roles[0].choices).toHaveLength(2);
  expect(daemon.getCalls('models')).toHaveLength(1);
});

it('deletes a role with undo and reloads after a conflict', async () => {
  const { daemon, getState, bump } = setup([custom]);
  fireEvent.click(await screen.findByRole('button', { name: 'Build' }));
  fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
  await waitFor(() => expect(getState().preferences.roles).toHaveLength(0));
  expect(screen.getByText('No roles yet')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
  await waitFor(() => expect(getState().preferences.roles).toHaveLength(1));
  await screen.findByRole('button', { name: 'Build' });

  bump();
  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  fireEvent.click(screen.getByRole('button', { name: 'Turn off' }));
  await screen.findByRole('alert');
  expect(screen.getByRole('alert').textContent).toContain('reload before saving');
  expect(getState().preferences.roles[0].enabled).toBe(true);
  await waitFor(() => expect(daemon.getCalls('load').length).toBeGreaterThanOrEqual(2));
  expect(screen.queryByText('Off', { selector: '.delegation-tag' })).not.toBeInTheDocument();
});

it('renders a configured maintained role by built-in kind when a template shares its id', async () => {
  const configured: DelegationRole = { ...structuredClone(template), id: 'orchestrator', builtin: BuiltinDelegationRole.Pathfinder };
  const pathfinderView: DelegationRole = { ...configured, name: 'Pathfinder', icon: 'search', description: 'Investigate the path', instructions: 'Find the path', stopping_point: 'Return a plan' };
  const orchestratorView: DelegationRole = { ...structuredClone(template), id: 'orchestrator', builtin: BuiltinDelegationRole.Orchestrator, name: 'Orchestrator', icon: 'spark', description: 'Coordinate work', instructions: 'Coordinate', stopping_point: 'Return the outcome' };
  let state: DelegationSettingsState = { preferences: { enabled: true, revision: 0, workflow_skill_enabled: true, roles: [configured], fallback: { selection: selection(), instructions: '' } }, templates: [orchestratorView], expandedRoles: [pathfinderView, orchestratorView], workflowSkillPaths: [], harnesses: [] };
  const daemon = createMockDaemon();
  daemon.setResponse('load', () => structuredClone(state));
  const load = daemon.createRequest<DelegationSettingsState>('load');
  const save = daemon.createRequest<DelegationSettingsState>('save');
  function Harness() { const policy = useDelegationPreferences(true, load, save); return <DelegationSettings policy={policy} loadModels={daemon.createRequest('models')} />; }
  render(<Harness />);
  expect(await screen.findByRole('button', { name: 'Pathfinder' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Orchestrator' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Restore Attn roles (1)' })).toBeInTheDocument();
  await act(async () => { state = { ...state }; });
});

it('withdraws undo when a change made elsewhere reloads the table', async () => {
  const { daemon, getState, bump } = setup([custom]);
  fireEvent.click(await screen.findByRole('button', { name: 'Build' }));
  fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
  await waitFor(() => expect(getState().preferences.roles).toHaveLength(0));
  expect(screen.getByRole('button', { name: 'Undo' })).toBeInTheDocument();

  bump();
  act(() => useDelegationPreferencesPush.getState().push(getState().preferences.revision));
  await waitFor(() => expect(daemon.getCalls('load').length).toBeGreaterThanOrEqual(2));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Undo' })).not.toBeInTheDocument());
  expect(getState().preferences.roles).toHaveLength(0);
});

it('withdraws undo when the delegation switch saves', async () => {
  const { getState } = setup([custom]);
  fireEvent.click(await screen.findByRole('button', { name: 'Build' }));
  fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
  await waitFor(() => expect(getState().preferences.roles).toHaveLength(0));
  expect(screen.getByRole('button', { name: 'Undo' })).toBeInTheDocument();

  fireEvent.click(screen.getByRole('switch', { name: 'Delegation preferences' }));
  await waitFor(() => expect(getState().preferences.enabled).toBe(false));
  expect(screen.queryByRole('button', { name: 'Undo' })).not.toBeInTheDocument();
});

it('keeps the fallback row when the table has no roles', async () => {
  setup([], true);
  await screen.findByText('No roles yet');
  expect(screen.getByRole('button', { name: 'Model for anything else' })).toBeInTheDocument();
});

it('reloads the table, harnesses included, when the section is shown again while Settings stays open', async () => {
  const daemon = createMockDaemon();
  let harnesses = [{ id: 'codex', name: 'Codex', available: true, model_pin: true, effort_pin: true, discovery: true }];
  daemon.setResponse('load', () => ({ preferences: { enabled: true, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: selection(), instructions: '' } }, templates: [], expandedRoles: [], workflowSkillPaths: [], harnesses }));
  daemon.setResponse('models', { models: [], detail: '' });
  const load = daemon.createRequest<DelegationSettingsState>('load');
  const save = daemon.createRequest<DelegationSettingsState>('save');
  const models = daemon.createRequest<DelegationModelCatalog>('models');
  function Host() {
    const policy = useDelegationPreferences(true, load, save);
    const [shown, setShown] = useState(true);
    return <><button type="button" onClick={() => setShown(s => !s)}>toggle</button>{shown && <DelegationSettings policy={policy} loadModels={models} />}</>;
  }
  render(<Host />);
  await screen.findByRole('button', { name: 'Model for anything else' });
  expect(daemon.getCalls('load')).toHaveLength(1);
  harnesses = [...harnesses, { id: 'pi', name: 'Pi', available: true, model_pin: true, effort_pin: true, discovery: false }];
  fireEvent.click(screen.getByRole('button', { name: 'toggle' }));
  fireEvent.click(screen.getByRole('button', { name: 'toggle' }));
  await waitFor(() => expect(daemon.getCalls('load')).toHaveLength(2));
  fireEvent.click(await screen.findByRole('button', { name: 'Model for anything else' }));
  expect(await screen.findByRole('option', { name: 'Pi' })).toBeInTheDocument();
  expect(daemon.getCalls('save')).toHaveLength(0);
});
