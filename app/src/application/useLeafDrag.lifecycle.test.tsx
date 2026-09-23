import { act, renderHook } from '@testing-library/react';
import { StrictMode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useLeafDrag } from './useLeafDrag';

function setup() {
  return renderHook(
    () =>
      useLeafDrag({
        currentDesktopIdRef: { current: 'desktop' },
        getDesktopLeafDropSnapshot: () => null,
      }),
    { wrapper: StrictMode },
  );
}

describe('leaf drag lifecycle', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('keeps a new gesture when the preceding drag has a deferred end', () => {
    const { result } = setup();
    act(() => {
      result.current.handleLeafDragStart('first');
      result.current.handleLeafDragEnd();
      result.current.handleLeafDragStart('second');
    });
    act(() => vi.runOnlyPendingTimers());
    expect(result.current.leafDragPreview?.draggingLeafId).toBe('second');
  });

  it('clears the preview after the drag ends', () => {
    const { result } = setup();
    act(() => {
      result.current.handleLeafDragStart('pane');
      result.current.handleLeafDragEnd();
    });
    act(() => vi.runOnlyPendingTimers());
    expect(result.current.leafDragPreview).toBeNull();
  });

  it('schedules nothing when the drag ends after unmount', () => {
    const { result, unmount } = setup();
    act(() => result.current.handleLeafDragStart('pane'));
    const endDrag = result.current.handleLeafDragEnd;
    unmount();
    act(() => endDrag());
    expect(vi.getTimerCount()).toBe(0);
  });
});
