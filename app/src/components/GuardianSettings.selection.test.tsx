import { fireEvent, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { renderApp } from '../test/renderApp';
import type { EventMessage } from '../test/protocol';
import type { ScriptedDaemon } from '../test/scriptedDaemon';

type AutoModeConfig = EventMessage<'automode_state_result'>['config'];
type Guardian = NonNullable<AutoModeConfig['guardian']>;

type Model = EventMessage<'delegation_models_result'>['models'][number];

const model: Model = { harness: 'pi', provider: 'fixture', id: 'review', name: 'Reviewer', description: '', detail: '', access: 'supported', effort_support: 'supported', effort_levels: ['off', 'low', 'high'] };
const plain: Model = { ...model, id: 'plain', name: 'Plain', effort_support: 'unsupported', effort_levels: [] };

function autoModeConfig(guardian: Guardian): AutoModeConfig {
  return {
    guardian,
    enabled_default: false,
    approval_policy: 'on-request',
    sandbox_mode: 'workspace-write',
    environment: { slots: [], notes: [] },
    rules: [],
    shipped_rules: [],
    network: { enabled: false, allowed_domains: [], denied_domains: [], allow_local_binding: false },
    shipped_denied_domains: [],
    legacy_patterns: [],
    presets: [],
  };
}

async function openGuardian(guardian: Guardian = {}, refusal = '') {
  const { daemon } = await renderApp();
  let config = autoModeConfig(guardian);
  daemon.on('automode_get', () => ({ event: 'automode_state_result', success: true, config, proposals: [], denials: [], environment_slots: [] }));
  daemon.on('delegation_models', () => ({ event: 'delegation_models_result', success: true, models: [model, plain], detail: '' }));
  daemon.on('automode_policy_set', ({ guardian: next }) => {
    if (refusal) return { event: 'automode_config_result', success: false, error: refusal };
    config = { ...config, guardian: next ?? {} };
    return { event: 'automode_config_result', success: true, config };
  });
  fireEvent.keyDown(window, { key: ',', metaKey: true });
  fireEvent.click(screen.getByTestId('settings-nav-autoMode'));
  await daemon.idle();
  return daemon;
}

async function choose(daemon: ScriptedDaemon, label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
  await daemon.idle();
}


it('saves independent model and reasoning selections and restores the default', async () => {
  const daemon = await openGuardian();
  await choose(daemon, 'Guardian model', JSON.stringify(['fixture', 'review']));
  expect(screen.getByLabelText('Guardian model')).toHaveValue(JSON.stringify(['fixture', 'review']));
  await choose(daemon, 'Guardian reasoning', 'high');
  expect(screen.getByLabelText('Guardian reasoning')).toHaveValue('high');
  fireEvent.click(screen.getByRole('button', { name: 'Reset guardian default' }));
  await daemon.idle();
  expect(screen.getByLabelText('Guardian model')).toHaveValue('');
  expect(daemon.sentOf('automode_policy_set').map((command) => command.guardian)).toEqual([
    { provider: 'fixture', model: 'review' },
    { provider: 'fixture', model: 'review', effort: 'high' },
    {},
  ]);
  expect(daemon.sentOf('delegation_models').map((command) => command.harness)).toEqual(['pi']);
});

it('clears incompatible reasoning when selecting a non-reasoning model', async () => {
  const daemon = await openGuardian({ provider: 'fixture', model: 'review', effort: 'high' });
  await choose(daemon, 'Guardian model', JSON.stringify(['fixture', 'plain']));
  expect(screen.getByLabelText('Guardian reasoning')).toHaveValue('');
  expect(screen.queryByRole('option', { name: 'high' })).not.toBeInTheDocument();
  expect(screen.getByRole('option', { name: 'off' })).toBeInTheDocument();
});

it('keeps an unavailable saved model visible and offers reset', async () => {
  const daemon = await openGuardian({ provider: 'removed', model: 'missing', effort: 'high' });
  expect(screen.getByText('The saved guardian model is unavailable. Choose another model or restore Follow session model.')).toBeInTheDocument();
  expect(screen.getByLabelText('Guardian model')).toHaveValue(JSON.stringify(['removed', 'missing']));
  fireEvent.click(screen.getByRole('button', { name: 'Reset guardian default' }));
  await daemon.idle();
  expect(screen.getByLabelText('Guardian model')).toHaveValue('');
});

it('shows save failures without claiming the selection changed', async () => {
  const daemon = await openGuardian({}, 'The daemon refused the change');
  await choose(daemon, 'Guardian model', JSON.stringify(['fixture', 'review']));
  expect(screen.getByRole('alert')).toHaveTextContent('The daemon refused the change');
  expect(screen.getByLabelText('Guardian model')).toHaveValue('');
});
