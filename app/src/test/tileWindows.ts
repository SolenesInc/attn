import { vi } from 'vitest';

const happyDomWithoutItsNodeTypes: string = 'happy-dom';

export async function letTileWindowsOpen(): Promise<void> {
  const { Window: DetachedWindow } = await import(/* @vite-ignore */ happyDomWithoutItsNodeTypes);
  vi.spyOn(window, 'open').mockImplementation(() => {
    const tileWindow = new DetachedWindow({ url: 'http://localhost' });
    tileWindow.ResizeObserver = window.ResizeObserver;
    return tileWindow;
  });
}
