import { vi } from 'vitest';

const happyDomWithoutItsNodeTypes: string = 'happy-dom';

export async function letTileWindowsOpen(): Promise<ReadonlyMap<string, Window>> {
  const windows = new Map<string, Window>();
  const { Window: DetachedWindow } = await import(/* @vite-ignore */ happyDomWithoutItsNodeTypes);
  vi.spyOn(window, 'open').mockImplementation((_url, target) => {
    const tileWindow = new DetachedWindow({ url: 'http://localhost' });
    tileWindow.ResizeObserver = window.ResizeObserver;
    if (target) windows.set(target, tileWindow);
    return tileWindow;
  });
  return windows;
}
