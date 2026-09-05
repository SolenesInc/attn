import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { SessionUsage } from '../../types/generated';
import { _resetEscapeStackForTest } from '../../hooks/useEscapeStack';
import { HeaderSessionUsage } from './SessionUsage';

const mixedUsage: SessionUsage = {
  total_tokens: 184_321,
  cost_usd: 1.234,
  has_unpriced_usage: true,
  models: [
    {
      model: 'claude-opus-5',
      input_tokens: 12_345,
      output_tokens: 6_789,
      cache_read_tokens: 150_000,
      cache_write_5m_tokens: 10_000,
      cache_write_1h_tokens: 5_000,
      cache_write_unclassified_tokens: 187,
      total_tokens: 184_321,
      cost_usd: 1.234,
      has_unpriced_usage: true,
      unpriced_reason: 'Cache write duration is unavailable.',
    },
  ],
};

afterEach(() => {
  vi.useRealTimers();
  _resetEscapeStackForTest();
});

describe('HeaderSessionUsage', () => {
  it('opens a receipt with exact per-model token counts', () => {
    render(<HeaderSessionUsage usage={mixedUsage} sessionId="session-1" pinned={false} onPopoverClosed={vi.fn()} />);
    fireEvent.focus(screen.getByTestId('session-usage-session-1'));

    const panel = screen.getByRole('dialog', { name: 'Session usage breakdown' });
    expect(within(panel).getByText('184,321 tokens')).toBeVisible();
    expect(within(panel).getByText('claude-opus-5')).toBeVisible();
    expect(within(panel).getByText('12,345')).toBeVisible();
    expect(within(panel).getByText('6,789')).toBeVisible();
    expect(within(panel).getByText('* Cache write duration is unavailable.')).toBeVisible();
  });

  it('keeps a hover preview open while the pointer moves into it', () => {
    vi.useFakeTimers();
    render(<HeaderSessionUsage usage={mixedUsage} sessionId="session-2" pinned={false} onPopoverClosed={vi.fn()} />);
    const badge = screen.getByTestId('session-usage-session-2');
    fireEvent.pointerEnter(badge);
    act(() => vi.advanceTimersByTime(160));
    const panel = screen.getByRole('dialog', { name: 'Session usage breakdown' });
    fireEvent.pointerLeave(badge);
    fireEvent.pointerEnter(panel);
    act(() => vi.advanceTimersByTime(240));
    expect(panel).toBeVisible();
  });

  it('pins from the external command request and closes with Escape', () => {
    const onPopoverClosed = vi.fn();
    render(<HeaderSessionUsage usage={mixedUsage} sessionId="session-3" pinned onPopoverClosed={onPopoverClosed} />);
    expect(screen.getByRole('dialog', { name: 'Session usage breakdown' })).toBeVisible();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onPopoverClosed).toHaveBeenCalledOnce();
  });

  it('does not start a pane drag and hides incomplete measurements', () => {
    const onPointerDown = vi.fn();
    const { rerender } = render(
      <div onPointerDown={onPointerDown}>
        <HeaderSessionUsage usage={mixedUsage} sessionId="session-4" pinned={false} onPopoverClosed={vi.fn()} />
      </div>,
    );
    fireEvent.pointerDown(screen.getByTestId('session-usage-session-4'));
    expect(onPointerDown).not.toHaveBeenCalled();

    rerender(
      <HeaderSessionUsage
        usage={{ ...mixedUsage, measurement_incomplete: true }}
        sessionId="session-4"
        pinned={false}
        onPopoverClosed={vi.fn()}
      />,
    );
    expect(screen.queryByTestId('session-usage-session-4')).not.toBeInTheDocument();
  });
});
