import { createContext, useContext, useLayoutEffect, useRef } from 'react';

declare global {
  interface Window {
    ResizeObserver: typeof ResizeObserver;
  }
}

const TileWindowContext = createContext<Window>(window);

export const TileWindowProvider = TileWindowContext.Provider;

export function useTileWindow(): Window {
  return useContext(TileWindowContext);
}

const nativeLabels = new WeakMap<Window, string>();

export function nameNativeWindow(tileWindow: Window, label: string): void {
  nativeLabels.set(tileWindow, label);
}

export function nativeWindowLabel(tileWindow: Window): string {
  return nativeLabels.get(tileWindow) ?? 'main';
}

export function usePortalTarget(): HTMLElement {
  return useTileWindow().document.body;
}

export interface FrameScheduler {
  request(callback: FrameRequestCallback): number;
  cancel(id: number): void;
}

interface PendingFrame {
  callback: FrameRequestCallback;
  owner: Window;
  handle: number;
}

function createFrameScheduler(initial: Window) {
  let current = initial;
  let nextId = 1;
  const pending = new Map<number, PendingFrame>();
  const schedule = (id: number, callback: FrameRequestCallback) => {
    const owner = current;
    const handle = owner.requestAnimationFrame((time) => {
      pending.delete(id);
      callback(time);
    });
    pending.set(id, { callback, owner, handle });
  };
  return {
    request(callback: FrameRequestCallback) {
      const id = nextId++;
      schedule(id, callback);
      return id;
    },
    cancel(id: number) {
      const frame = pending.get(id);
      if (!frame) return;
      pending.delete(id);
      frame.owner.cancelAnimationFrame(frame.handle);
    },
    moveTo(next: Window) {
      if (next === current) return;
      current = next;
      for (const [id, frame] of pending) {
        frame.owner.cancelAnimationFrame(frame.handle);
        schedule(id, frame.callback);
      }
    },
  };
}

export function useFrameScheduler(): FrameScheduler {
  const tileWindow = useTileWindow();
  const scheduler = useRef<ReturnType<typeof createFrameScheduler> | null>(null);
  scheduler.current ??= createFrameScheduler(tileWindow);
  useLayoutEffect(() => scheduler.current?.moveTo(tileWindow), [tileWindow]);
  return scheduler.current;
}

export interface ResizeWatch {
  observe(target: Element, callback: ResizeObserverCallback): void;
  disconnect(): void;
}

function createResizeWatch(initial: Window) {
  let current = initial;
  let watched: { target: Element; callback: ResizeObserverCallback } | null = null;
  let observer: ResizeObserver | null = null;
  const connect = () => {
    observer?.disconnect();
    observer = null;
    if (!watched) return;
    const next = new current.ResizeObserver(watched.callback);
    next.observe(watched.target);
    observer = next;
  };
  return {
    observe(target: Element, callback: ResizeObserverCallback) {
      watched = { target, callback };
      connect();
    },
    disconnect() {
      watched = null;
      connect();
    },
    moveTo(next: Window) {
      if (next === current) return;
      current = next;
      if (watched) connect();
    },
  };
}

export function useResizeWatch(): ResizeWatch {
  const tileWindow = useTileWindow();
  const watch = useRef<ReturnType<typeof createResizeWatch> | null>(null);
  watch.current ??= createResizeWatch(tileWindow);
  useLayoutEffect(() => watch.current?.moveTo(tileWindow), [tileWindow]);
  return watch.current;
}
