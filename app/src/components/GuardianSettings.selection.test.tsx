import { useState } from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { GuardianSettings } from './GuardianSettings';
import { createMockDaemon } from '../test/mocks/daemon';
import { clearDelegationModelCatalogs } from '../hooks/useDelegationModelCatalog';
import { ModelCapabilitySupport, type GuardianSelection } from '../types/generated';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import type { AutoModePolicyEdit } from '../hooks/daemonAutoModeEvents';

const model = { harness: 'pi', provider: 'fixture', id: 'review', name: 'Reviewer', description: '', detail: '', access: ModelCapabilitySupport.Supported, effort_support: ModelCapabilitySupport.Supported, effort_levels: ['off', 'low', 'high'] };
afterEach(() => { cleanup(); clearDelegationModelCatalogs(); });

function setup(initial: GuardianSelection = {}) {
  const daemon = createMockDaemon();
  daemon.setResponse('models', { models: [model, { ...model, id: 'plain', name: 'Plain', effort_support: ModelCapabilitySupport.Unsupported, effort_levels: [] }], detail: '' });
  daemon.setResponse('save', ([edit]: unknown[]) => (edit as AutoModePolicyEdit).guardian);
  const loadModels = daemon.createRequest<DelegationModelCatalog>('models');
  const write = daemon.createRequest<GuardianSelection>('save');
  function Harness() {
    const [value, setValue] = useState(initial);
    const [busy, setBusy] = useState(false);
    return <GuardianSettings value={value} loadModels={loadModels} policy={{ editing: busy ? 'policy' : null, setPolicy: async edit => {
      setBusy(true);
      try { setValue(await write(edit)); } finally { setBusy(false); }
    } }} />;
  }
  render(<Harness />);
  return daemon;
}

it('saves independent model and reasoning selections and restores the default', async () => {
  const daemon = setup();
  await screen.findByRole('option', { name: 'Reviewer' });
  fireEvent.change(screen.getByLabelText('Guardian model'), { target: { value: JSON.stringify(['fixture', 'review']) } });
  await waitFor(() => expect(screen.getByLabelText('Guardian model')).toHaveValue(JSON.stringify(['fixture', 'review'])));
  fireEvent.change(screen.getByLabelText('Guardian reasoning'), { target: { value: 'high' } });
  await waitFor(() => expect(screen.getByLabelText('Guardian reasoning')).toHaveValue('high'));
  fireEvent.click(screen.getByRole('button', { name: 'Reset guardian default' }));
  await waitFor(() => expect(screen.getByLabelText('Guardian model')).toHaveValue(''));
  expect(daemon.getCalls('save').map(call => call.args)).toEqual([
    [{ guardian: { provider: 'fixture', model: 'review', effort: undefined } }],
    [{ guardian: { provider: 'fixture', model: 'review', effort: 'high' } }],
    [{ guardian: {} }],
  ]);
  expect(daemon.getCalls('models').map(call => call.args)).toEqual([['pi']]);
});

it('clears incompatible reasoning when selecting a non-reasoning model', async () => {
  setup({ provider: 'fixture', model: 'review', effort: 'high' });
  await screen.findByRole('option', { name: 'Plain' });
  fireEvent.change(screen.getByLabelText('Guardian model'), { target: { value: JSON.stringify(['fixture', 'plain']) } });
  await waitFor(() => expect(screen.getByLabelText('Guardian reasoning')).toHaveValue(''));
  expect(screen.queryByRole('option', { name: 'high' })).not.toBeInTheDocument();
  expect(screen.getByRole('option', { name: 'off' })).toBeInTheDocument();
});

it('keeps an unavailable saved model visible and offers reset', async () => {
  setup({ provider: 'removed', model: 'missing', effort: 'high' });
  await screen.findByText('The saved guardian model is unavailable. Choose another model or restore Follow session model.');
  expect(screen.getByLabelText('Guardian model')).toHaveValue(JSON.stringify(['removed', 'missing']));
  fireEvent.click(screen.getByRole('button', { name: 'Reset guardian default' }));
  await waitFor(() => expect(screen.getByLabelText('Guardian model')).toHaveValue(''));
});

it('shows save failures without claiming the selection changed', async () => {
  const daemon = setup();
  daemon.setResponse('save', () => { throw new Error('The daemon refused the change'); });
  await screen.findByRole('option', { name: 'Reviewer' });
  fireEvent.change(screen.getByLabelText('Guardian model'), { target: { value: JSON.stringify(['fixture', 'review']) } });
  expect(await screen.findByRole('alert')).toHaveTextContent('The daemon refused the change');
  expect(screen.getByLabelText('Guardian model')).toHaveValue('');
});
