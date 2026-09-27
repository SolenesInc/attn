import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Toast } from './Toast';

afterEach(() => {
  vi.useRealTimers();
});

describe('Toast', () => {
  it('keeps an explicitly extended recovery notice visible for its requested duration', () => {
    vi.useFakeTimers();
    const onDone = vi.fn();
    render(
      <Toast
        toast={{
          message: 'Terminal issue recovered. Diagnostics were saved for Victor.',
          tone: 'error',
          durationMs: 12_000,
        }}
        onDone={onDone}
      />,
    );

    expect(screen.getByRole('alert')).toHaveTextContent('Terminal issue recovered');
    act(() => {
      vi.advanceTimersByTime(11_999);
    });
    expect(onDone).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(screen.getByRole('alert')).not.toHaveClass('visible');
    expect(onDone).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it('keeps a toast that arrives while the previous one fades out', () => {
    vi.useFakeTimers();
    const onDone = vi.fn();
    const { rerender } = render(
      <Toast toast={{ message: 'first', tone: 'notice', durationMs: 1_000 }} onDone={onDone} />,
    );
    act(() => {
      vi.advanceTimersByTime(1_100);
    });
    rerender(<Toast toast={{ message: 'second', tone: 'notice', durationMs: 1_000 }} onDone={onDone} />);
    act(() => {
      vi.advanceTimersByTime(500);
    });

    expect(onDone).not.toHaveBeenCalled();
    expect(screen.getByRole('status')).toHaveTextContent('second');
  });
});
