import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SessionCostPriceSettings } from './SessionCostPriceSettings';

const rates = {
  input_usd_per_mtok: 3,
  output_usd_per_mtok: 15,
  cache_read_usd_per_mtok: 0.3,
  cache_write_5m_usd_per_mtok: 3.75,
  cache_write_1h_usd_per_mtok: 6,
};

const fillNewRates = () => {
  fireEvent.change(screen.getByTestId('settings-price-new-input_usd_per_mtok'), { target: { value: '2' } });
  fireEvent.change(screen.getByTestId('settings-price-new-output_usd_per_mtok'), { target: { value: '10' } });
  fireEvent.change(screen.getByTestId('settings-price-new-cache_read_usd_per_mtok'), { target: { value: '0.2' } });
  fireEvent.change(screen.getByTestId('settings-price-new-cache_write_5m_usd_per_mtok'), { target: { value: '2.5' } });
  fireEvent.change(screen.getByTestId('settings-price-new-cache_write_1h_usd_per_mtok'), { target: { value: '4' } });
};

describe('SessionCostPriceSettings', () => {
  it('lists exact-model overrides in order and ignores removed blank values', () => {
    render(
      <SessionCostPriceSettings
        settings={{
          'session_cost.price.z-model': JSON.stringify(rates),
          'session_cost.price.a-model': JSON.stringify({ ...rates, input_usd_per_mtok: 1 }),
          'session_cost.price.removed-model': '',
        }}
        onSetSetting={vi.fn()}
      />,
    );

    const modelNames = screen.getAllByText(/^[az]-model$/).map((node) => node.textContent);
    expect(modelNames).toEqual(['a-model', 'z-model']);
    expect(screen.queryByText('removed-model')).toBeNull();
    expect(screen.getByTestId('settings-price-a-model-input_usd_per_mtok')).toHaveValue(1);
    expect(screen.getByTestId('settings-price-z-model-cache_write_1h_usd_per_mtok')).toHaveValue(6);
  });

  it('adds a complete override under the exact model key', async () => {
    const onSetSetting = vi.fn();
    render(<SessionCostPriceSettings settings={{}} onSetSetting={onSetSetting} />);

    fireEvent.change(screen.getByTestId('settings-price-new-model'), {
      target: { value: '  claude-new-exact  ' },
    });
    fillNewRates();
    fireEvent.blur(screen.getByTestId('settings-price-new-cache_write_1h_usd_per_mtok'));

    expect(onSetSetting).toHaveBeenCalledWith(
      'session_cost.price.claude-new-exact',
      JSON.stringify({
        input_usd_per_mtok: 2,
        output_usd_per_mtok: 10,
        cache_read_usd_per_mtok: 0.2,
        cache_write_5m_usd_per_mtok: 2.5,
        cache_write_1h_usd_per_mtok: 4,
      }),
    );
  });

  it('keeps a new override editable after a failed save and retries it', async () => {
    const onSetSetting = vi.fn().mockRejectedValueOnce(new Error('Disconnected')).mockResolvedValue(undefined);
    render(<SessionCostPriceSettings settings={{}} onSetSetting={onSetSetting} />);
    fireEvent.change(screen.getByTestId('settings-price-new-model'), { target: { value: 'new-model' } });
    fillNewRates();
    await act(async () => { fireEvent.blur(screen.getByTestId('settings-price-new-cache_write_1h_usd_per_mtok')); });
    expect(screen.getByTestId('settings-price-new-model')).toHaveValue('new-model');
    expect(screen.getByTestId('settings-price-new-input_usd_per_mtok')).toHaveValue(2);
    expect(screen.getByRole('alert')).toHaveTextContent('Disconnected');
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Retry' })); });
    expect(onSetSetting).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId('settings-price-new-model')).toHaveValue('');
  });

  it('updates and removes an existing override', async () => {
    const onSetSetting = vi.fn();
    render(
      <SessionCostPriceSettings
        settings={{ 'session_cost.price.claude-priced': JSON.stringify(rates) }}
        onSetSetting={onSetSetting}
      />,
    );

    fireEvent.change(screen.getByTestId('settings-price-claude-priced-cache_read_usd_per_mtok'), {
      target: { value: '0.25' },
    });
    await act(async () => { fireEvent.blur(screen.getByTestId('settings-price-claude-priced-cache_read_usd_per_mtok')); });
    expect(onSetSetting).toHaveBeenCalledWith(
      'session_cost.price.claude-priced',
      JSON.stringify({ ...rates, cache_read_usd_per_mtok: 0.25 }),
    );

    fireEvent.click(screen.getByTestId('settings-price-claude-priced-remove'));
    expect(onSetSetting).toHaveBeenCalledWith('session_cost.price.claude-priced', '');
  });

  it('requires every non-negative rate and rejects duplicate model IDs', () => {
    render(
      <SessionCostPriceSettings
        settings={{ 'session_cost.price.existing-model': JSON.stringify(rates) }}
        onSetSetting={vi.fn()}
      />,
    );

    const rate = screen.getByTestId('settings-price-new-cache_read_usd_per_mtok');
    fireEvent.change(screen.getByTestId('settings-price-new-model'), { target: { value: 'new-model' } });
    fillNewRates();
    expect(screen.queryByRole('button', { name: /save/i })).toBeNull();

    fireEvent.change(screen.getByTestId('settings-price-new-cache_read_usd_per_mtok'), {
      target: { value: '-1' },
    });
    fireEvent.blur(rate);

    fireEvent.change(screen.getByTestId('settings-price-new-cache_read_usd_per_mtok'), {
      target: { value: '0.2' },
    });
    fireEvent.change(screen.getByTestId('settings-price-new-model'), { target: { value: 'existing-model' } });
    fireEvent.blur(rate);
    expect(screen.getByText('That model already has an override above.')).toBeInTheDocument();
  });

  it('keeps a malformed saved override visible so it can be repaired or removed', async () => {
    const onSetSetting = vi.fn();
    render(
      <SessionCostPriceSettings
        settings={{ 'session_cost.price.broken-model': '{"input_usd_per_mtok":3}' }}
        onSetSetting={onSetSetting}
      />,
    );

    const card = screen.getByText('broken-model').closest<HTMLElement>('.settings-price-card');
    expect(card).not.toBeNull();
    expect(within(card!).getByText(/saved override is invalid/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /save/i })).toBeNull();

    fireEvent.click(screen.getByTestId('settings-price-broken-model-remove'));
    expect(onSetSetting).toHaveBeenCalledWith('session_cost.price.broken-model', '');
  });

  it('matches the daemon by rejecting saved overrides with unknown fields', () => {
    render(
      <SessionCostPriceSettings
        settings={{
          'session_cost.price.extra-field-model': JSON.stringify({ ...rates, currency: 'USD' }),
        }}
        onSetSetting={vi.fn()}
      />,
    );

    const card = screen.getByText('extra-field-model').closest<HTMLElement>('.settings-price-card');
    expect(card).not.toBeNull();
    expect(within(card!).getByText(/saved override is invalid/i)).toBeInTheDocument();
  });
});
