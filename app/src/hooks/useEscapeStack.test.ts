import { renderHook, fireEvent } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useEscapeStack, _resetEscapeStackForTest } from './useEscapeStack';

afterEach(() => {
  _resetEscapeStackForTest();
});

describe('useEscapeStack', () => {
  it('calls the handler when Escape is pressed', () => {
    const handler = vi.fn();
    renderHook(() => useEscapeStack(handler, true));

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('does not call the handler when disabled', () => {
    const handler = vi.fn();
    renderHook(() => useEscapeStack(handler, false));

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('ignores non-Escape keys', () => {
    const handler = vi.fn();
    renderHook(() => useEscapeStack(handler, true));

    fireEvent.keyDown(window, { key: 'Enter' });
    fireEvent.keyDown(window, { key: 'ArrowDown' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('calls only the top handler (LIFO)', () => {
    const first = vi.fn();
    const second = vi.fn();
    renderHook(() => useEscapeStack(first, true));
    renderHook(() => useEscapeStack(second, true));

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });

  it('falls through to the previous handler after the top is removed', () => {
    const first = vi.fn();
    const second = vi.fn();
    renderHook(() => useEscapeStack(first, true));
    const { unmount } = renderHook(() => useEscapeStack(second, true));

    unmount();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).not.toHaveBeenCalled();
  });

  it('always calls the latest handler reference', () => {
    let count = 0;
    const getHandler = () => () => { count++; };

    const { rerender } = renderHook(({ h }) => useEscapeStack(h, true), {
      initialProps: { h: getHandler() },
    });
    rerender({ h: getHandler() });
    rerender({ h: getHandler() });

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(count).toBe(1); // called exactly once, not three times
  });

  it('dismisses a passive preview without consuming the focused element key', () => {
    const handler = vi.fn();
    const target = document.createElement('input');
    const receiveKey = vi.fn();
    document.body.append(target);
    target.addEventListener('keydown', receiveKey);
    renderHook(() => useEscapeStack(handler, true, { consume: false }));

    expect(fireEvent.keyDown(target, { key: 'Escape' })).toBe(true);
    expect(handler).toHaveBeenCalledOnce();
    expect(receiveKey).toHaveBeenCalledOnce();
    expect(receiveKey.mock.calls[0][0].defaultPrevented).toBe(false);
    target.remove();
  });

  it('passes through passive previews to only the top consuming handler', () => {
    const calls: string[] = [];
    renderHook(() => useEscapeStack(() => calls.push('underlying'), true));
    renderHook(() => useEscapeStack(() => calls.push('dialog'), true));
    renderHook(() => useEscapeStack(() => calls.push('preview'), true, { consume: false }));

    expect(fireEvent.keyDown(window, { key: 'Escape' })).toBe(false);
    expect(calls).toEqual(['preview', 'dialog']);
  });

  it('uses the latest consume mode without reordering the stack', () => {
    const calls: string[] = [];
    const { rerender } = renderHook(({ consume }) => useEscapeStack(() => calls.push('preview'), true, { consume }), {
      initialProps: { consume: false },
    });
    renderHook(() => useEscapeStack(() => calls.push('dialog'), true));
    rerender({ consume: true });

    expect(fireEvent.keyDown(window, { key: 'Escape' })).toBe(false);
    expect(calls).toEqual(['dialog']);
  });
});
