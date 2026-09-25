import { fireEvent, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { emptySelection, openDelegationSettings, type DelegationTable, type Role } from '../test/delegationDaemon';
import { gesture } from '../test/settings';

const custom: Role = { id: 'build', name: 'Build', icon: 'code', enabled: true, description: 'Implement a change', instructions: 'Run relevant tests', stopping_point: 'Return for review', default_choice_id: 'default', choices: [{ id: 'default', name: 'Everyday', when: '', selection: emptySelection() }] };
const template: Role = { ...custom, id: 'builder', builtin: 'builder', name: '', icon: '', description: '', instructions: '', stopping_point: '' };
const expandedTemplate: Role = { ...template, name: 'Builder', icon: 'code', description: 'Implement a change\nSecond line', instructions: 'Run relevant tests', stopping_point: 'Return for review' };

const expand = (role: Role): Role => role.builtin ? { ...role, name: expandedTemplate.name, icon: expandedTemplate.icon, description: expandedTemplate.description, instructions: expandedTemplate.instructions, stopping_point: expandedTemplate.stopping_point } : role;

const openDelegation = (table: DelegationTable = {}) =>
  openDelegationSettings({ templates: [template], expanded: (roles) => [...roles.map(expand), expandedTemplate], ...table });

it('copies a maintained role with its configured model and state, not the template', async () => {
  const configured: Role = { ...template, enabled: false, choices: [{ id: 'default', name: 'Default', when: '', selection: { harness: 'codex', provider: '', model: 'model-a', effort: 'high' } }] };
  const { daemon, saves, lastSaved } = await openDelegation({ roles: [configured] });
  fireEvent.click(screen.getByRole('button', { name: 'Builder' }));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Make an editable copy' })));
  expect(saves()).toHaveLength(1);
  const copy = lastSaved().roles[1];
  expect(copy.builtin).toBeUndefined();
  expect(copy.name).toBe('Builder (custom)');
  expect(copy.instructions).toBe('Run relevant tests');
  expect(copy.enabled).toBe(false);
  expect(copy.choices[0].selection).toEqual({ harness: 'codex', provider: '', model: 'model-a', effort: 'high' });
});

it('keeps a focused draft when a change made elsewhere reloads the table, and saves it on blur', async () => {
  const { daemon, server, saves, loads } = await openDelegation({ roles: [custom] });
  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  const field = screen.getByLabelText('Instructions') as HTMLTextAreaElement;
  fireEvent.focus(field);
  fireEvent.change(field, { target: { value: 'Typed here' } });
  server.changeElsewhere((preferences) => { preferences.roles[0].instructions = 'Changed elsewhere'; });
  await gesture(daemon, () => server.announce());
  expect(loads()).toHaveLength(2);
  expect(field.value).toBe('Typed here');
  await gesture(daemon, () => fireEvent.blur(field));
  expect(saves()).toHaveLength(1);
  expect(saves()[0].preferences.revision).toBe(1);
  expect(server.preferences.roles[0].instructions).toBe('Typed here');
});

it('adopts maintained roles from the empty state with the install flag and shows their guidance read-only', async () => {
  const { daemon, saves } = await openDelegation();
  expect(screen.getByText('No roles yet')).toBeInTheDocument();
  expect(daemon.sentOf('delegation_models')).toHaveLength(0);
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Add Attn roles' })));
  expect(saves()).toHaveLength(1);
  const [adoption] = saves();
  expect(adoption.install_workflow_skill).toBe(true);
  expect(adoption.preferences.workflow_skill_enabled).toBe(true);
  expect(adoption.preferences.roles[0].builtin).toBe('builder');
  expect(adoption.preferences.roles[0].instructions).toBe('');

  const row = screen.getByRole('button', { name: 'Builder' });
  expect(screen.getByText('Implement a change')).toBeInTheDocument();
  expect(screen.getByText('Needs a model')).toBeInTheDocument();
  fireEvent.click(row);
  expect(screen.getByText('Run relevant tests')).toBeInTheDocument();
  expect(screen.queryByLabelText('Instructions')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Make an editable copy' })).toBeInTheDocument();
});

it('saves a model picked from the row, an alternative with its condition, and the switch', async () => {
  const { daemon, server, saves, lastSaved } = await openDelegation({ roles: [custom] });
  fireEvent.click(screen.getByRole('button', { name: 'Model for Build' }));
  expect(screen.getByText('Pick a harness to see its models. None leaves this route unset.')).toBeInTheDocument();
  await gesture(daemon, () => fireEvent.click(screen.getByRole('option', { name: 'Codex' })));
  expect(saves()).toHaveLength(1);
  expect(daemon.sentOf('delegation_models').map((command) => command.harness)).toEqual(['codex']);
  await gesture(daemon, () => fireEvent.click(screen.getByRole('option', { name: /Everyday model/ })));
  expect(saves()).toHaveLength(2);
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'medium' })));
  expect(saves()).toHaveLength(3);
  expect(lastSaved().roles[0].choices[0].selection).toEqual({ harness: 'codex', provider: '', model: 'model-a', effort: 'medium' });
  expect(screen.queryByText('Needs a model')).not.toBeInTheDocument();
  fireEvent.mouseDown(document.body);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: '+ Alternative model' })));
  expect(saves()).toHaveLength(4);
  const name = screen.getByLabelText('Name', { selector: '#altname-' + lastSaved().roles[0].choices[1].id });
  fireEvent.focus(name);
  fireEvent.change(name, { target: { value: 'Hard verification' } });
  await gesture(daemon, () => fireEvent.blur(name));
  expect(saves()).toHaveLength(5);
  const when = screen.getByLabelText('When to use this instead of the default');
  fireEvent.focus(when);
  fireEvent.change(when, { target: { value: 'Verification is difficult\n\nOr requirements are ambiguous.' } });
  await gesture(daemon, () => fireEvent.blur(when));
  expect(saves()).toHaveLength(6);
  const [, alternative] = server.preferences.roles[0].choices;
  expect(alternative.name).toBe('Hard verification');
  expect(alternative.when).toContain('ambiguous');
  expect(alternative.selection).toEqual({ harness: 'codex', provider: '', model: 'model-a', effort: 'medium' });
  expect(screen.getByText('Verification is difficult')).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  expect(screen.getByRole('button', { name: '+1 alternative' })).toBeInTheDocument();
  expect(screen.getByText('Agents see 1 of 1 role.', { exact: false })).toBeInTheDocument();

  await gesture(daemon, () => fireEvent.click(screen.getByRole('switch', { name: 'Delegation preferences' })));
  expect(server.preferences.enabled).toBe(false);
  expect(screen.getByRole('switch', { name: 'Delegation preferences' })).toHaveAttribute('aria-checked', 'false');
  expect(server.preferences.roles[0].choices).toHaveLength(2);
  expect(daemon.sentOf('delegation_models')).toHaveLength(1);
});

it('deletes a role with undo and reloads after a conflict', async () => {
  const { daemon, server, loads } = await openDelegation({ roles: [custom] });
  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Delete' })));
  expect(server.preferences.roles).toHaveLength(0);
  expect(screen.getByText('No roles yet')).toBeInTheDocument();
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Undo' })));
  expect(server.preferences.roles).toHaveLength(1);
  expect(screen.getByRole('button', { name: 'Build' })).toBeInTheDocument();

  server.changeElsewhere();
  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Turn off' })));
  expect(screen.getByRole('alert').textContent).toContain('reload before saving');
  expect(server.preferences.roles[0].enabled).toBe(true);
  expect(loads()).toHaveLength(2);
  expect(screen.queryByText('Off', { selector: '.delegation-tag' })).not.toBeInTheDocument();
});

it('renders a configured maintained role by built-in kind when a template shares its id', async () => {
  const configured: Role = { ...structuredClone(template), id: 'orchestrator', builtin: 'pathfinder' };
  const pathfinderView: Role = { ...configured, name: 'Pathfinder', icon: 'search', description: 'Investigate the path', instructions: 'Find the path', stopping_point: 'Return a plan' };
  const orchestratorView: Role = { ...structuredClone(template), id: 'orchestrator', builtin: 'orchestrator', name: 'Orchestrator', icon: 'spark', description: 'Coordinate work', instructions: 'Coordinate', stopping_point: 'Return the outcome' };
  await openDelegation({ roles: [configured], templates: [orchestratorView], expanded: () => [pathfinderView, orchestratorView], harnesses: [] });
  expect(screen.getByRole('button', { name: 'Pathfinder' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Orchestrator' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Restore Attn roles (1)' })).toBeInTheDocument();
});

it('withdraws undo when a change made elsewhere reloads the table', async () => {
  const { daemon, server, loads } = await openDelegation({ roles: [custom] });
  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Delete' })));
  expect(server.preferences.roles).toHaveLength(0);
  expect(screen.getByRole('button', { name: 'Undo' })).toBeInTheDocument();

  server.changeElsewhere();
  await gesture(daemon, () => server.announce());
  expect(loads()).toHaveLength(2);
  expect(screen.queryByRole('button', { name: 'Undo' })).not.toBeInTheDocument();
  expect(server.preferences.roles).toHaveLength(0);
});

it('withdraws undo when the delegation switch saves', async () => {
  const { daemon, server } = await openDelegation({ roles: [custom] });
  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Delete' })));
  expect(screen.getByRole('button', { name: 'Undo' })).toBeInTheDocument();

  await gesture(daemon, () => fireEvent.click(screen.getByRole('switch', { name: 'Delegation preferences' })));
  expect(server.preferences.enabled).toBe(false);
  expect(screen.queryByRole('button', { name: 'Undo' })).not.toBeInTheDocument();
});

it('keeps the fallback row when the table has no roles', async () => {
  await openDelegation({ roles: [], enabled: true });
  expect(screen.getByText('No roles yet')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Model for anything else' })).toBeInTheDocument();
});

it('reloads the table, harnesses included, when the section is shown again while Settings stays open', async () => {
  const { daemon, server, saves, loads } = await openDelegation({ templates: [], expanded: () => [] });
  expect(loads()).toHaveLength(1);
  server.harnesses = [...server.harnesses, { id: 'pi', name: 'Pi', available: true, model_pin: true, effort_pin: true, discovery: false }];
  fireEvent.click(screen.getByTestId('settings-nav-connectivity'));
  await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-delegation')));
  expect(loads()).toHaveLength(2);
  fireEvent.click(screen.getByRole('button', { name: 'Model for anything else' }));
  expect(screen.getByRole('option', { name: 'Pi' })).toBeInTheDocument();
  expect(saves()).toHaveLength(0);
});

it('makes an alternative the default and keeps the former default as an alternative', async () => {
  const everyday = { harness: 'codex', provider: '', model: 'model-a', effort: '' };
  const careful = { harness: 'codex', provider: '', model: 'model-b', effort: 'high' };
  const role: Role = { ...custom, choices: [{ id: 'default', name: 'Everyday', when: '', selection: everyday }, { id: 'careful', name: 'Hard verification', when: 'Verification is difficult.', selection: careful }] };
  const { daemon, server, saves } = await openDelegation({ roles: [role] });
  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  fireEvent.click(screen.getByRole('button', { name: 'Hard verification' }));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Make default' })));
  expect(saves()).toHaveLength(1);
  const saved = server.preferences.roles[0];
  expect(saved.default_choice_id).toBe('careful');
  expect(saved.choices.map((choice) => choice.id)).toEqual(['default', 'careful']);
  expect(screen.getByRole('button', { name: 'Everyday' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Hard verification' })).not.toBeInTheDocument();
  expect(screen.getByText('No condition yet. The agent cannot pick this.')).toBeInTheDocument();
  expect(screen.getByText('Made Hard verification the default for Build.')).toBeInTheDocument();
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Undo' })));
  expect(saves()).toHaveLength(2);
  expect(server.preferences.roles[0].default_choice_id).toBe('default');
  expect(daemon.sentOf('delegation_models')).toHaveLength(0);
});
