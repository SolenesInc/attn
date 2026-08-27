import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import {
  GARDEN_FRAME_MODE_STORAGE_KEY,
  GARDEN_FULLSCREEN_VIEW_STORAGE_KEY,
  readGardenFrameMode,
  readGardenFullscreenView,
  useGardenFullscreenView,
  useGardenPresentation,
} from './useGardenPresentation';

describe('Garden presentation preferences', () => {
  beforeEach(() => {
    window.localStorage.removeItem(GARDEN_FRAME_MODE_STORAGE_KEY);
    window.localStorage.removeItem(GARDEN_FULLSCREEN_VIEW_STORAGE_KEY);
  });

  it('falls back to the sidebar and list when storage is empty or invalid', () => {
    expect(readGardenFrameMode()).toBe('dock');
    expect(readGardenFullscreenView()).toBe('list');

    window.localStorage.setItem(GARDEN_FRAME_MODE_STORAGE_KEY, 'somewhere');
    window.localStorage.setItem(GARDEN_FULLSCREEN_VIEW_STORAGE_KEY, 'calendar');
    expect(readGardenFrameMode()).toBe('dock');
    expect(readGardenFullscreenView()).toBe('list');
  });

  it('persists the fullscreen view across hook mounts', () => {
    const first = renderHook(() => useGardenFullscreenView());
    act(() => first.result.current[1]('board'));
    first.unmount();

    const second = renderHook(() => useGardenFullscreenView());
    expect(second.result.current[0]).toBe('board');
  });

  it('opens where the user last left it and closes without changing that choice', () => {
    let dockOpen = false;
    const openDock = () => { dockOpen = true; };
    const closeDock = () => { dockOpen = false; };
    const hook = renderHook(
      ({ open }) => useGardenPresentation({ dockOpen: open, openDock, closeDock }),
      { initialProps: { open: dockOpen } },
    );
    const syncDock = () => hook.rerender({ open: dockOpen });

    act(() => hook.result.current.open());
    syncDock();
    expect(hook.result.current.mode).toBe('dock');

    act(() => hook.result.current.toggleFrame());
    expect(hook.result.current.mode).toBe('full');
    expect(window.localStorage.getItem(GARDEN_FRAME_MODE_STORAGE_KEY)).toBe('full');

    act(() => hook.result.current.close());
    syncDock();
    expect(hook.result.current.mode).toBe('closed');
    expect(window.localStorage.getItem(GARDEN_FRAME_MODE_STORAGE_KEY)).toBe('full');

    act(() => hook.result.current.open());
    expect(hook.result.current.mode).toBe('full');

    act(() => hook.result.current.toggleFrame());
    syncDock();
    expect(hook.result.current.mode).toBe('dock');
    expect(window.localStorage.getItem(GARDEN_FRAME_MODE_STORAGE_KEY)).toBe('dock');
  });

  it('uses the icon as open or close without turning a close into a frame change', () => {
    window.localStorage.setItem(GARDEN_FRAME_MODE_STORAGE_KEY, 'full');
    let dockOpen = false;
    const hook = renderHook(
      ({ open }) => useGardenPresentation({
        dockOpen: open,
        openDock: () => { dockOpen = true; },
        closeDock: () => { dockOpen = false; },
      }),
      { initialProps: { open: dockOpen } },
    );

    act(() => hook.result.current.toggleFromIcon());
    expect(hook.result.current.mode).toBe('full');

    act(() => hook.result.current.toggleFromIcon());
    hook.rerender({ open: dockOpen });
    expect(hook.result.current.mode).toBe('closed');
    expect(window.localStorage.getItem(GARDEN_FRAME_MODE_STORAGE_KEY)).toBe('full');

    act(() => hook.result.current.toggleFromIcon());
    expect(hook.result.current.mode).toBe('full');

    act(() => hook.result.current.toggleFrame());
    hook.rerender({ open: dockOpen });
    expect(hook.result.current.mode).toBe('dock');

    act(() => hook.result.current.close());
    hook.rerender({ open: dockOpen });
    act(() => hook.result.current.toggleFromIcon());
    hook.rerender({ open: dockOpen });
    expect(hook.result.current.mode).toBe('dock');
    expect(window.localStorage.getItem(GARDEN_FRAME_MODE_STORAGE_KEY)).toBe('dock');
  });
});
