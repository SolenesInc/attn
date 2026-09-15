import { StrictMode } from 'react';
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDockPanels } from './useDockPanels';

describe('dock panel lifecycle', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('removes a toggled-closed panel after its exit under StrictMode', () => {
    const { result } = renderHook(useDockPanels, { wrapper: StrictMode });
    act(() => result.current.toggleDockPanel('garden'));
    expect(result.current.dockState.openPanels.garden).toBe(true);
    act(() => result.current.toggleDockPanel('garden'));
    expect(result.current.dockState.openPanels.garden).toBe(false);
    expect(result.current.dockState.stack).toEqual(['garden']);
    act(() => vi.runOnlyPendingTimers());
    expect(result.current.dockState.stack).toEqual([]);
  });

  it('preserves a reopened panel and independently finishes another exit', () => {
    const { result, unmount } = renderHook(useDockPanels, { wrapper: StrictMode });
    act(() => {
      result.current.openDockPanel('garden');
      result.current.openDockPanel('attention');
    });
    act(() => {
      result.current.closeDockPanel('garden');
      result.current.closeDockPanel('attention');
    });
    act(() => result.current.openDockPanel('garden'));
    act(() => vi.runOnlyPendingTimers());
    expect(result.current.dockState.stack).toEqual(['garden']);
    expect(result.current.dockState.openPanels.garden).toBe(true);
    act(() => result.current.closeDockPanel('garden'));
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('applies batched toggles to the pending state', () => {
    const { result } = renderHook(useDockPanels, { wrapper: StrictMode });
    act(() => {
      result.current.toggleDockPanel('garden');
      result.current.toggleDockPanel('garden');
    });
    expect(result.current.dockState.openPanels.garden).toBe(false);
    act(() => vi.runOnlyPendingTimers());
    expect(result.current.dockState.stack).toEqual([]);
  });
});
