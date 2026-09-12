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
  { id: 'plug', name: 'Plug', available: true, model_pin: false, effort_pin: true, discovery: false },
  { id: 'socket', name: 'Socket', available: true, model_pin: false, effort_pin: true, discovery: false },
];
const catalog: DelegationModelCatalog = { detail: 'Reported by Claude Code', models: [
  { harness: 'claude', provider: '', id: 'opus', name: 'Opus', description: 'Deep work', detail: '', effort_support: ModelCapabilitySupport.Supported, effort_levels: ['medium', 'high'], access: ModelCapabilitySupport.Unknown },
  { harness: 'claude', provider: '', id: 'haiku', name: 'Haiku', description: '', detail: '', effort_support: ModelCapabilitySupport.Unsupported, effort_levels: [], access: ModelCapabilitySupport.Unknown },
  { harness: 'claude', provider: '', id: 'sonnet', name: 'Sonnet', description: '', detail: '', effort_support: ModelCapabilitySupport.Supported, effort_levels: [], access: ModelCapabilitySupport.Unknown },
] };
const rect = { top: 100, bottom: 130, left: 40, right: 240 };
const anchor = document.createElement('button');
anchor.getBoundingClientRect = () => ({ ...rect, x: rect.left, y: rect.top, width: rect.right - rect.left, height: rect.bottom - rect.top, toJSON: () => rect });

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

it('shows the new model\'s effort after a switch instead of the previous model\'s typed value', async () => {
  const { onChange, rerender } = open({ harness: 'claude', provider: '', model: 'sonnet', effort: 'high' });
  await screen.findByRole('option', { name: /Sonnet/ });
  expect(screen.getByLabelText('Effort')).toHaveValue('high');
  rerender({ harness: 'claude', provider: '', model: 'custom-model', effort: '' });
  const effort = screen.getByLabelText('Effort');
  expect(effort).toHaveValue('');
  act(() => { effort.focus(); });
  fireEvent.blur(effort);
  expect(onChange).not.toHaveBeenCalled();
});

it('closes on a click outside that lands right after a re-render with a fresh onClose', async () => {
  const closed = vi.fn();
  function Host() {
    const [ticks, setTicks] = useState(0);
    return <><button type="button" onClick={() => setTicks(n => n + 1)}>tick {ticks}</button><DelegationModelPopover value={{ harness: 'claude', provider: '', model: '', effort: '' }} harnesses={harnesses} anchor={anchor} onChange={vi.fn()} onClose={() => closed(ticks)} loadModels={vi.fn(async () => structuredClone(catalog))} /></>;
  }
  render(<Host />);
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); });
  fireEvent.click(screen.getByRole('button', { name: /tick/ }));
  fireEvent.mouseDown(document.body);
  expect(closed).toHaveBeenCalledWith(1);
});

it('offers effort for a harness that picks its own model, and for a harness default', async () => {
  const { loadModels, onChange, rerender } = open({ harness: 'plug', provider: '', model: '', effort: '' });
  expect(screen.getByText(/Plug uses the model selected in its own settings/)).toBeInTheDocument();
  const effort = screen.getByLabelText('Effort');
  fireEvent.change(effort, { target: { value: 'high' } });
  fireEvent.blur(effort);
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'plug', provider: '', model: '', effort: 'high' });
  expect(loadModels).not.toHaveBeenCalled();

  rerender({ harness: 'claude', provider: '', model: '', effort: '' });
  await screen.findByRole('option', { name: /Opus/ });
  const claudeEffort = screen.getByLabelText('Effort');
  fireEvent.change(claudeEffort, { target: { value: 'max' } });
  fireEvent.blur(claudeEffort);
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'claude', provider: '', model: '', effort: 'max' });
});

it('closes when keyboard focus leaves it for a control behind it', async () => {
  const closed = vi.fn();
  render(<><button type="button">Behind</button><DelegationModelPopover value={{ harness: 'claude', provider: '', model: '', effort: '' }} harnesses={harnesses} anchor={anchor} onChange={vi.fn()} onClose={closed} loadModels={vi.fn(async () => structuredClone(catalog))} /></>);
  await screen.findByRole('option', { name: /Opus/ });
  const inside = screen.getByRole('option', { name: /Opus/ });
  act(() => { inside.focus(); });
  fireEvent.blur(inside, { relatedTarget: screen.getByRole('button', { name: 'refresh' }) });
  expect(closed).not.toHaveBeenCalled();
  fireEvent.blur(inside, { relatedTarget: screen.getByRole('button', { name: 'Behind' }) });
  expect(closed).toHaveBeenCalledTimes(1);
});

it('keeps focus inside when a click on its own padding drops focus to the body', async () => {
  const closed = vi.fn();
  render(<DelegationModelPopover value={{ harness: 'claude', provider: '', model: '', effort: '' }} harnesses={harnesses} anchor={anchor} onChange={vi.fn()} onClose={closed} loadModels={vi.fn(async () => structuredClone(catalog))} />);
  await screen.findByRole('option', { name: /Opus/ });
  const inside = screen.getByRole('option', { name: /Opus/ });
  act(() => { inside.focus(); });
  act(() => { inside.blur(); });
  expect(document.activeElement).toBe(document.body);
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); });
  expect(document.activeElement?.getAttribute('data-harness')).toBe('claude');
  expect(closed).not.toHaveBeenCalled();
});

it('follows its cell when the settings body scrolls or the window resizes', async () => {
  render(<DelegationModelPopover value={{ harness: 'claude', provider: '', model: '', effort: '' }} harnesses={harnesses} anchor={anchor} onChange={vi.fn()} onClose={vi.fn()} loadModels={vi.fn(async () => structuredClone(catalog))} />);
  const dialog = await screen.findByRole('dialog', { name: 'Choose a model' });
  expect(dialog.style.top).toBe('136px');
  rect.top = 40; rect.bottom = 70;
  act(() => { document.body.dispatchEvent(new Event('scroll', { bubbles: false })); });
  expect(dialog.style.top).toBe('76px');
  rect.left = 90;
  act(() => { window.dispatchEvent(new Event('resize')); });
  expect(dialog.style.left).toBe('90px');
  rect.top = 100; rect.bottom = 130; rect.left = 40;
});

it('keeps focus on the effort level a keyboard user just picked', async () => {
  const { onChange, rerender } = open({ harness: 'claude', provider: '', model: 'opus', effort: '' });
  await screen.findByRole('option', { name: /Opus/ });
  const high = screen.getByRole('button', { name: 'high' });
  act(() => { high.focus(); });
  fireEvent.click(high);
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'claude', provider: '', model: 'opus', effort: 'high' });
  rerender({ harness: 'claude', provider: '', model: 'opus', effort: 'high' });
  const pressed = screen.getByRole('button', { name: 'high' });
  expect(pressed).toHaveAttribute('aria-pressed', 'true');
  expect(document.activeElement).toBe(pressed);
});

it('starts the effort field empty when the harness changes under it', async () => {
  const { onChange, rerender } = open({ harness: 'plug', provider: '', model: '', effort: '' });
  fireEvent.change(screen.getByLabelText('Effort'), { target: { value: 'high' } });
  fireEvent.click(screen.getByRole('option', { name: 'Socket' }));
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'socket', provider: '', model: '', effort: '' });
  rerender({ harness: 'socket', provider: '', model: '', effort: '' });
  const socketEffort = screen.getByLabelText('Effort');
  expect(socketEffort).toHaveValue('');
  fireEvent.blur(socketEffort);
  expect(onChange).toHaveBeenCalledTimes(1);

  rerender({ harness: 'claude', provider: '', model: '', effort: '' });
  await screen.findByRole('option', { name: /Opus/ });
  fireEvent.change(screen.getByLabelText('Effort'), { target: { value: 'max' } });
  fireEvent.click(screen.getByRole('option', { name: 'Pi' }));
  rerender({ harness: 'pi', provider: '', model: '', effort: '' });
  const piEffort = screen.getByLabelText('Effort');
  expect(piEffort).toHaveValue('');
  fireEvent.blur(piEffort);
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'pi', provider: '', model: '', effort: '' });
});

it('keeps focus inside after a manual model is submitted', async () => {
  const { onChange, onClose, rerender } = open({ harness: 'claude', provider: '', model: '', effort: '' });
  await screen.findByRole('option', { name: /Opus/ });
  fireEvent.click(screen.getByRole('button', { name: 'Enter a model ID' }));
  fireEvent.change(screen.getByLabelText('Model ID'), { target: { value: 'claude-next' } });
  const use = screen.getByRole('button', { name: 'Use it' });
  act(() => { use.focus(); });
  fireEvent.click(use);
  expect(onChange).toHaveBeenLastCalledWith({ harness: 'claude', provider: '', model: 'claude-next', effort: '' });
  rerender({ harness: 'claude', provider: '', model: 'claude-next', effort: '' });
  expect(screen.queryByRole('button', { name: 'Use it' })).toBeNull();
  expect(screen.getByRole('dialog', { name: 'Choose a model' }).contains(document.activeElement)).toBe(true);
  expect(onClose).not.toHaveBeenCalled();
});

it('holds the effort editor until discovery settles for a model that is already pinned', async () => {
  let resolveCatalog: (next: typeof catalog) => void = () => {};
  const loadModels = vi.fn(() => new Promise<typeof catalog>(resolve => { resolveCatalog = resolve; }));
  render(<DelegationModelPopover value={{ harness: 'claude', provider: '', model: 'opus', effort: '' }} harnesses={harnesses} anchor={anchor} onChange={vi.fn()} onClose={vi.fn()} loadModels={loadModels} />);
  expect(loadModels).toHaveBeenCalledTimes(1);
  expect(screen.queryByLabelText('Effort')).toBeNull();
  await act(async () => { resolveCatalog(structuredClone(catalog)); });
  expect(screen.getByRole('button', { name: 'high' })).toBeInTheDocument();
});
