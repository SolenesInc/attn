import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { openSection, savedSettings } from './test/settings';

const rates = {
  input_usd_per_mtok: 3,
  output_usd_per_mtok: 15,
  cache_read_usd_per_mtok: 0.3,
  cache_write_5m_usd_per_mtok: 3.75,
  cache_write_1h_usd_per_mtok: 6,
};

const newRates = [
  ['input_usd_per_mtok', '2'],
  ['output_usd_per_mtok', '10'],
  ['cache_read_usd_per_mtok', '0.2'],
  ['cache_write_5m_usd_per_mtok', '2.5'],
  ['cache_write_1h_usd_per_mtok', '4'],
] as const;

const pricing = () => within(screen.getByTestId('settings-session-cost-prices'));
const settingsModal = () => within(screen.getByTestId('settings-modal'));

function openPricing(settings: Record<string, string> = {}, script?: (daemon: ScriptedDaemon) => void) {
  return openSection('agents', { settings }, script);
}

async function addOverride(daemon: ScriptedDaemon, model: string) {
  fireEvent.change(pricing().getByLabelText('Exact model ID'), { target: { value: model } });
  for (const [key, value] of newRates) {
    fireEvent.change(pricing().getByTestId(`settings-price-new-${key}`), { target: { value } });
  }
  await gesture(daemon, () => fireEvent.blur(pricing().getByTestId('settings-price-new-cache_write_1h_usd_per_mtok')));
}

const addedRates = '{"input_usd_per_mtok":2,"output_usd_per_mtok":10,"cache_read_usd_per_mtok":0.2,"cache_write_5m_usd_per_mtok":2.5,"cache_write_1h_usd_per_mtok":4}';

describe('App session cost prices', () => {
  it('lists saved overrides by model id and leaves out removed ones', async () => {
    await openPricing({
      'session_cost.price.z-model': JSON.stringify(rates),
      'session_cost.price.a-model': JSON.stringify({ ...rates, input_usd_per_mtok: 1 }),
      'session_cost.price.removed-model': '',
    });

    expect(pricing().getAllByText(/-model$/).map((node) => node.textContent)).toEqual(['a-model', 'z-model']);
    expect(pricing().getByTestId('settings-price-a-model-input_usd_per_mtok')).toHaveValue(1);
    expect(pricing().getByTestId('settings-price-z-model-cache_write_1h_usd_per_mtok')).toHaveValue(6);
  });

  it('adds a complete override under the exact model id and clears the form', async () => {
    const daemon = await openPricing();

    await addOverride(daemon, '  claude-new-exact  ');

    expect(savedSettings(daemon)).toEqual([['session_cost.price.claude-new-exact', addedRates]]);
    expect(pricing().getByLabelText('Exact model ID')).toHaveValue('');
    expect(pricing().getByText('claude-new-exact')).toBeInTheDocument();
  });

  it('keeps a new override in the form when its save fails, and retries it', async () => {
    let refusals = 1;
    const daemon = await openPricing({}, (scripted) => {
      let settings: Record<string, string> = {};
      scripted.on('set_setting', ({ key, value }) => {
        if (refusals-- > 0) return { event: 'settings_updated', success: false, error: 'Disconnected' };
        settings = { ...settings, [key]: value };
        return [
          { event: 'settings_updated', success: true, changed_key: key },
          { event: 'settings_updated', request_id: undefined, settings },
        ];
      });
    });

    await addOverride(daemon, 'new-model');
    expect(settingsModal().getByRole('alert')).toHaveTextContent('Disconnected');
    expect(pricing().getByLabelText('Exact model ID')).toHaveValue('new-model');
    expect(pricing().getByTestId('settings-price-new-input_usd_per_mtok')).toHaveValue(2);

    await gesture(daemon, () => fireEvent.click(settingsModal().getByRole('button', { name: 'Retry' })));

    expect(savedSettings(daemon)).toEqual([
      ['session_cost.price.new-model', addedRates],
      ['session_cost.price.new-model', addedRates],
    ]);
    expect(pricing().getByLabelText('Exact model ID')).toHaveValue('');
  });

  it('writes nothing for an incomplete or negative rate, or a model that already has an override', async () => {
    const daemon = await openPricing({ 'session_cost.price.existing-model': JSON.stringify(rates) });
    const lastRate = pricing().getByTestId('settings-price-new-cache_write_1h_usd_per_mtok');

    fireEvent.change(pricing().getByLabelText('Exact model ID'), { target: { value: 'new-model' } });
    await gesture(daemon, () => fireEvent.blur(lastRate));
    expect(pricing().getByText('Complete every rate with a non-negative number.')).toBeInTheDocument();

    for (const [key, value] of newRates) {
      fireEvent.change(pricing().getByTestId(`settings-price-new-${key}`), { target: { value } });
    }
    fireEvent.change(pricing().getByTestId('settings-price-new-cache_read_usd_per_mtok'), { target: { value: '-1' } });
    await gesture(daemon, () => fireEvent.blur(lastRate));

    fireEvent.change(pricing().getByTestId('settings-price-new-cache_read_usd_per_mtok'), { target: { value: '0.2' } });
    fireEvent.change(pricing().getByLabelText('Exact model ID'), { target: { value: 'existing-model' } });
    await gesture(daemon, () => fireEvent.blur(lastRate));
    expect(pricing().getByText('That model already has an override above.')).toBeInTheDocument();

    expect(savedSettings(daemon)).toEqual([]);
  });

  it('updates one rate of a saved override and removes it', async () => {
    const daemon = await openPricing({ 'session_cost.price.claude-priced': JSON.stringify(rates) });
    const cacheRead = pricing().getByTestId('settings-price-claude-priced-cache_read_usd_per_mtok');

    fireEvent.change(cacheRead, { target: { value: '0.25' } });
    await gesture(daemon, () => fireEvent.blur(cacheRead));
    await gesture(daemon, () => fireEvent.click(pricing().getByTestId('settings-price-claude-priced-remove')));

    expect(savedSettings(daemon)).toEqual([
      ['session_cost.price.claude-priced', JSON.stringify({ ...rates, cache_read_usd_per_mtok: 0.25 })],
      ['session_cost.price.claude-priced', ''],
    ]);
    expect(pricing().queryByText('claude-priced')).toBeNull();
  });

  it.each([
    ['a missing rate', '{"input_usd_per_mtok":3}'],
    ['a field the daemon does not know', JSON.stringify({ ...rates, currency: 'USD' })],
  ])('shows a saved override with %s as invalid, so it can be removed', async (_, raw) => {
    const daemon = await openPricing({ 'session_cost.price.broken-model': raw });

    const card = pricing().getByText('broken-model').closest<HTMLElement>('.settings-price-card')!;
    expect(within(card).getByText(/saved override is invalid/i)).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.click(pricing().getByTestId('settings-price-broken-model-remove')));
    expect(savedSettings(daemon)).toEqual([['session_cost.price.broken-model', '']]);
  });
});
