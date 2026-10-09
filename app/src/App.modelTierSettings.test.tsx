import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSection, savedSettings } from './test/settings';
import { reportedModel, harnesses, serveHarnessCatalogs } from './test/harnessCatalogs';
import { gesture } from './test/renderApp';

const codex = () => within(screen.getByRole('region', { name: 'Codex models' }));
const radio = (name: string, tier: string) => within(screen.getByRole('radiogroup', { name: `Tier for ${name}` })).getByRole('radio', { name: tier });

describe('App model tier settings', () => {
  it('shows reported order, flags untiered models, and names live light/deep defaults', async () => {
    const daemon = await openSection('models');
    const controls = codex().getAllByRole('radiogroup');
    expect(controls.map(control => control.getAttribute('aria-label'))).toEqual(['Tier for gpt-6.1-sol', 'Tier for gpt-6-astra', 'Tier for gpt-6-luna', 'Tier for gpt-5.6-terra', 'Tier for gpt-5.6-luna', 'Tier for New model']);
    expect(codex().getByText('Light default → gpt-6-luna')).toBeInTheDocument();
    expect(codex().getByText('Deep default → gpt-6.1-sol')).toBeInTheDocument();
    expect(codex().getByText('New model').parentElement).toHaveTextContent('no tier');
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(1);
    expect(daemon.sentOf('harness_models').map(request => request.harness)).toEqual(['claude', 'codex', 'pi']);
  });
  it('updates the mapping and live default, then Reset returns to attn’s shipped tier', async () => {
    const daemon = await openSection('models');
    await gesture(daemon, () => fireEvent.click(radio('gpt-6-luna', 'Standard')));
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['model_tier_overrides', '[{"harness":"codex","provider":"","model":"gpt-6-luna","tier":"standard"}]']);
    expect(codex().getByText('Light default → gpt-5.6-luna')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Reset tier for gpt-6-luna' })));
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['model_tier_overrides', '[]']);
    expect(codex().getByText('Light default → gpt-6-luna')).toBeInTheDocument();
  });
  it('keeps missing-model overrides visible and removes only the selected override', async () => {
    const overrides = [{ harness: 'codex', provider: '', model: 'retired-model', tier: 'deep' }, { harness: 'codex', provider: '', model: 'gpt-6-luna', tier: 'standard' }];
    const daemon = await openSection('models', { settings: { model_tier_overrides: JSON.stringify(overrides) } });
    const row = codex().getByText('retired-model').closest('.model-tier-row')!;
    expect(row).toHaveTextContent('Your tier: deep');
    await gesture(daemon, () => fireEvent.click(within(row as HTMLElement).getByRole('button', { name: 'Remove' })));
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['model_tier_overrides', JSON.stringify([overrides[1]])]);
  });
  it('Delete resets a model without changing its neighboring overrides', async () => {
    const daemon = await openSection('models', { settings: { model_tier_overrides: '[{"harness":"codex","provider":"","model":"gpt-6-luna","tier":"deep"}]' } });
    await gesture(daemon, () => fireEvent.keyDown(radio('gpt-6-luna', 'Deep'), { key: 'Delete' }));
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['model_tier_overrides', '[]']);
    expect(radio('gpt-6-luna', 'Light')).toBeChecked();
  });
  it('removes overrides for a known harness without discovery', async () => {
    const daemon = await openSection('models', { settings: { model_tier_overrides: '[{"harness":"copilot","provider":"","model":"old-model","tier":"deep"}]' } });
    const row = screen.getByText('old-model').closest('.model-tier-row')!;
    await gesture(daemon, () => fireEvent.click(within(row as HTMLElement).getByRole('button', { name: 'Remove' })));
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['model_tier_overrides', '[]']);
  });

  it('keeps saved overrides removable when model discovery fails', async () => {
    const overrides = [{ harness: 'codex', provider: '', model: 'gpt-6-luna', tier: 'deep' }];
    const daemon = await openSection('models', { settings: { model_tier_overrides: JSON.stringify(overrides) } });
    daemon.on('harness_models', () => ({ event: 'harness_models_result', success: false, models: [], tier_defaults: {}, detail: 'The configured executable cannot report models.' }));
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { model_tier_overrides: JSON.stringify(overrides), codex_executable: '/missing/codex', codex_available: 'false' } }));
    expect(codex().getByText('Saved overrides')).toBeInTheDocument();
    const row = codex().getByText('gpt-6-luna').closest('.model-tier-row')!;
    await gesture(daemon, () => fireEvent.click(within(row as HTMLElement).getByRole('button', { name: 'Remove' })));
    expect(savedSettings(daemon).slice(-1)[0]).toEqual(['model_tier_overrides', '[]']);
  });
  it('rediscovers models when the harness executable changes', async () => {
    const daemon = await openSection('models');
    const before = daemon.sentOf('harness_models').filter(request => request.harness === 'codex').length;
    daemon.on('harness_models', ({ harness }) => ({ event: 'harness_models_result', success: true, models: [reportedModel(harness, 'replacement-luna', 'Replacement Luna', 'light')], tier_defaults: { light: 'replacement-luna' }, detail: '' }));
    daemon.emit({ event: 'settings_updated', settings: { codex_executable: '/another/codex' } });
    await daemon.idle();
    expect(daemon.sentOf('harness_models').filter(request => request.harness === 'codex')).toHaveLength(before + 1);
    expect(codex().getByText('Light default → Replacement Luna')).toBeInTheDocument();
    expect(codex().queryByText('gpt-6-luna')).toBeNull();
  });
  it('refreshes harnesses when plugins change, while health timestamps do not trigger more reads', async () => {
    const daemon = await openSection('models');
    const plugin = { name: 'demo', dir: '/tmp/demo', version: '1', availability: 'available', connected: true, running: true, installation_state: 'installed', runtime_state: 'running', can_install: false, can_uninstall: true, priority: 0 };
    serveHarnessCatalogs(daemon, [...harnesses, { id: 'demo', name: 'Demo', available: true, model_pin: true, effort_pin: true, discovery: false }]);
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [plugin], issues: [] }));
    expect(screen.getByRole('region', { name: 'Demo models' })).toBeInTheDocument();
    const reads = daemon.sentOf('delegation_preferences_get').length;
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [{ ...plugin, last_health_at: '2026-10-09T02:00:00Z' }], issues: [] }));
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(reads);
    serveHarnessCatalogs(daemon);
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [], issues: [] }));
    expect(screen.queryByRole('region', { name: 'Demo models' })).toBeNull();
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(reads + 1);
  });
  it('refreshes harness membership when a driver registers on an already-connected plugin', async () => {
    const daemon = await openSection('models');
    serveHarnessCatalogs(daemon, [...harnesses, { id: 'new-driver', name: 'New driver', available: true, model_pin: true, effort_pin: true, discovery: false }]);
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { 'new-driver_available': 'true', 'new-driver_cap_model_pin': 'true' } }));
    expect(screen.getByRole('region', { name: 'New driver models' })).toBeInTheDocument();
  });

  it('refreshes harness membership when an existing driver loses initial-prompt support', async () => {
    const daemon = await openSection('models', { settings: { pi_available: 'true', pi_cap_initial_prompt: 'true' } });
    expect(screen.getByRole('region', { name: 'Pi models' })).toBeInTheDocument();
    serveHarnessCatalogs(daemon, harnesses.filter(harness => harness.id !== 'pi'));
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { pi_cap_initial_prompt: 'false' } }));
    expect(screen.queryByRole('region', { name: 'Pi models' })).toBeNull();
  });

});
