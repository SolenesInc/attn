import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useState } from 'react';
import { DelegationModelPopover } from './DelegationModelPopover';
import { clearDelegationModelCatalogs } from '../hooks/useDelegationModelCatalog';
import { ModelCapabilitySupport, type DelegationHarness, type DelegationSelection } from '../types/generated';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';

const harnesses: DelegationHarness[] = [
  { id: 'claude', name: 'Claude Code', available: true, model_pin: true, effort_pin: true, discovery: true },
  { id: 'copilot', name: 'Copilot', available: true, model_pin: false, effort_pin: false, discovery: false },
  { id: 'pi', name: 'Pi', available: true, model_pin: true, effort_pin: true, discovery: false },
];
const catalog: DelegationModelCatalog = { detail: 'Reported by Claude Code', models: [
  { harness: 'claude', provider: '', id: 'opus', name: 'Opus', description: 'Deep work', detail: '', effort_support: ModelCapabilitySupport.Supported, effort_levels: ['medium', 'high'], access: ModelCapabilitySupport.Unknown },
  { harness: 'claude', provider: '', id: 'haiku', name: 'Haiku', description: '', detail: '', effort_support: ModelCapabilitySupport.Unsupported, effort_levels: [], access: ModelCapabilitySupport.Unknown },
  { harness: 'claude', provider: '', id: 'sonnet', name: 'Sonnet', description: '', detail: '', effort_support: ModelCapabilitySupport.Supported, effort_levels: [], access: ModelCapabilitySupport.Unknown },
] };
const anchor = { top: 100, bottom: 130, left: 40, right: 240 };

function open(value: DelegationSelection) {
  const loadModels = vi.fn(async () => structuredClone(catalog));
  const onChange = vi.fn();
  const onClose = vi.fn();
  const view = render(<DelegationModelPopover value={value} harnesses={harnesses} anchor={anchor} onChange={onChange} onClose={onClose} loadModels={loadModels} />);
  return { loadModels, onChange, onClose, rerender: (next: DelegationSelection) => view.rerender(<DelegationModelPopover value={next} harnesses={harnesses} anchor={anchor} onChange={onChange} onClose={onClose} loadModels={loadModels} />) };
}

afterEach(() => { cleanup(); clearDelegationModelCatalogs(); });

it('discovers on open once per harness, refreshes on demand, and commits model then effort', async () => {
  const { loadModels, onChange, rerender } = open({ harness: 'claude', provider: '', model: '', effort: '' });
  expect(screen.getByLabelText('Discovering models')).toBeInTheDocument();
  await screen.findByRole('option', { name: /Opus/ });
  expect(loadModels).toHaveBeenCalledTimes(1);
  expect(screen.getByText('Reported by Claude Code')).toBeInTheDocument();
  expect(screen.queryByRole('group', { name: 'Effort' })).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole('option', { name: /Opus/ }));
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'claude', provider: '', model: 'opus', effort: '' });
  rerender({ harness: 'claude', provider: '', model: 'opus', effort: '' });
  expect(loadModels).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: 'high' }));
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'claude', provider: '', model: 'opus', effort: 'high' });
  rerender({ harness: 'claude', provider: '', model: 'opus', effort: 'high' });

  fireEvent.click(screen.getByRole('option', { name: /Haiku/ }));
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'claude', provider: '', model: 'haiku', effort: '' });
  rerender({ harness: 'claude', provider: '', model: 'haiku', effort: '' });
  expect(screen.queryByRole('group', { name: 'Effort' })).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: 'refresh' }));
  await waitFor(() => expect(loadModels).toHaveBeenCalledTimes(2));
});

it('reuses the catalog across reopen and lets an exact id through', async () => {
  const first = open({ harness: 'claude', provider: '', model: '', effort: '' });
  await screen.findByRole('option', { name: /Opus/ });
  cleanup();
  const { loadModels, onChange } = open({ harness: 'claude', provider: '', model: '', effort: '' });
  expect(screen.getByRole('option', { name: /Opus/ })).toBeInTheDocument();
  expect(loadModels).not.toHaveBeenCalled();
  expect(first.loadModels).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: 'Enter a model ID' }));
  fireEvent.change(screen.getByLabelText('Model ID'), { target: { value: ' claude-next ' } });
  fireEvent.click(screen.getByRole('button', { name: 'Use it' }));
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'claude', provider: '', model: 'claude-next', effort: '' });
});

it('a popover reopened during discovery shows the models when that discovery settles', async () => {
  let settle: (catalog: DelegationModelCatalog) => void = () => {};
  const loadModels = vi.fn(() => new Promise<DelegationModelCatalog>(resolve => { settle = resolve; }));
  const props = { harnesses, anchor, onChange: vi.fn(), onClose: vi.fn(), loadModels, value: { harness: 'claude', provider: '', model: '', effort: '' } };
  render(<DelegationModelPopover {...props} />);
  expect(screen.getByLabelText('Discovering models')).toBeInTheDocument();
  cleanup();
  render(<DelegationModelPopover {...props} />);
  expect(screen.getByLabelText('Discovering models')).toBeInTheDocument();
  expect(loadModels).toHaveBeenCalledTimes(1);
  settle(structuredClone(catalog));
  await screen.findByRole('option', { name: /Opus/ });
  expect(screen.queryByLabelText('Discovering models')).not.toBeInTheDocument();
});

it('takes a provider with a manual model for a plugin harness only', () => {
  const claude = open({ harness: 'claude', provider: '', model: '', effort: '' });
  fireEvent.click(screen.getByRole('button', { name: 'Enter a model ID' }));
  expect(screen.queryByLabelText('Provider')).not.toBeInTheDocument();
  cleanup();

  const { onChange, loadModels } = open({ harness: 'pi', provider: '', model: '', effort: '' });
  expect(loadModels).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Enter a model ID' }));
  fireEvent.change(screen.getByLabelText('Provider'), { target: { value: ' openai ' } });
  fireEvent.change(screen.getByLabelText('Model ID'), { target: { value: 'gpt-next' } });
  fireEvent.click(screen.getByRole('button', { name: 'Use it' }));
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'pi', provider: 'openai', model: 'gpt-next', effort: '' });
  expect(claude.onChange).not.toHaveBeenCalled();
});

it('returns focus to the opener when Escape closes it', async () => {
  function Host() {
    const [shown, setShown] = useState(false);
    return <><button type="button" onClick={() => setShown(true)}>Model for Builder</button>{shown && <DelegationModelPopover value={{ harness: 'claude', provider: '', model: '', effort: '' }} harnesses={harnesses} anchor={anchor} onChange={vi.fn()} onClose={() => setShown(false)} loadModels={vi.fn(async () => structuredClone(catalog))} />}</>;
  }
  render(<Host />);
  const opener = screen.getByRole('button', { name: 'Model for Builder' });
  act(() => { opener.focus(); });
  fireEvent.click(opener);
  expect(document.activeElement?.getAttribute('data-harness')).toBe('claude');
  fireEvent.keyDown(document.activeElement ?? document, { key: 'Escape' });
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Choose a model' })).not.toBeInTheDocument());
  expect(document.activeElement).toBe(opener);
});

it('commits a typed effort when a click outside closes the popover', async () => {
  const { onChange, onClose } = open({ harness: 'claude', provider: '', model: 'sonnet', effort: '' });
  await screen.findByRole('option', { name: /Sonnet/ });
  const effort = screen.getByLabelText('Effort');
  act(() => { effort.focus(); });
  fireEvent.change(effort, { target: { value: ' max ' } });
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); });
  fireEvent.mouseDown(document.body);
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'claude', provider: '', model: 'sonnet', effort: 'max' });
  expect(onClose).toHaveBeenCalledTimes(1);
});

it('commits a pinned harness alone and never discovers for it', () => {
  const { loadModels, onChange } = open({ harness: '', provider: '', model: '', effort: '' });
  expect(screen.getByText('Pick a harness to see its models. None leaves this route unset.')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('option', { name: 'Copilot' }));
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'copilot', provider: '', model: '', effort: '' });
  expect(loadModels).not.toHaveBeenCalled();
});

it('clears an assigned harness back to an empty selection and closes', () => {
  const { onChange, onClose } = open({ harness: 'claude', provider: '', model: 'opus', effort: 'high' });
  fireEvent.click(screen.getByRole('option', { name: 'None' }));
  expect(onChange).toHaveBeenLastCalledWith({ harness: '', provider: '', model: '', effort: '' });
  expect(onClose).toHaveBeenCalledTimes(1);
});
