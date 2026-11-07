// @ts-expect-error -- @types/node is not an app dependency
import { readFileSync } from 'node:fs';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, vi } from 'vitest';
import { cleanup } from '@testing-library/react';
import type * as Zustand from 'zustand';
import { WHATS_NEW_BANNER_STORAGE_KEY, WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from '../hooks/useWhatsNew';
import { forgetAppMemory } from './appMemory';
import { resetTauriFakes } from './tauriFakes';

if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.showPopover = function showPopover(this: HTMLElement) {
    if (!this.isConnected) throw new DOMException('Element is not connected', 'InvalidStateError');
  };
  HTMLElement.prototype.hidePopover = function hidePopover() {};
}

vi.mock('@tauri-apps/api/core', async () => (await import('./tauriFakes')).tauriFakes.core);
vi.mock('@tauri-apps/api/app', async () => (await import('./tauriFakes')).tauriFakes.app);
vi.mock('@tauri-apps/api/event', async () => (await import('./tauriFakes')).tauriFakes.event);
vi.mock('@tauri-apps/api/window', async () => (await import('./tauriFakes')).tauriFakes.window);
vi.mock('@tauri-apps/api/path', async () => (await import('./tauriFakes')).tauriFakes.path);
vi.mock('@tauri-apps/plugin-deep-link', async () => (await import('./tauriFakes')).tauriFakes.deepLink);
vi.mock('@tauri-apps/plugin-opener', async () => (await import('./tauriFakes')).tauriFakes.opener);
vi.mock('@tauri-apps/plugin-dialog', async () => (await import('./tauriFakes')).tauriFakes.dialog);
vi.mock('@tauri-apps/plugin-fs', async () => (await import('./tauriFakes')).tauriFakes.fs);

vi.mock('../ghostty/wasm', async () => {
  const { Ghostty } = await import('../ghostty');
  let compiled: WebAssembly.Module | null = null;
  return {
    loadGhostty: async () => {
      compiled ??= new WebAssembly.Module(readFileSync('vendor/ghostty-vt/ghostty-vt.wasm'));
      return new Ghostty(new WebAssembly.Instance(compiled, { env: { log: () => {} } }));
    },
  };
});

vi.mock('zustand', async (importOriginal) => {
  const actual = await importOriginal<typeof Zustand>();
  const { storeResets } = await import('./storeResets');
  const create = ((initializer?: Zustand.StateCreator<unknown>) => {
    const track = (stateCreator: Zustand.StateCreator<unknown>) => {
      const store = actual.create(stateCreator);
      const initialState = store.getInitialState();
      storeResets.add(() => store.setState(initialState, true));
      return store;
    };
    return initializer ? track(initializer) : track;
  }) as typeof Zustand.create;
  return { ...actual, create };
});

function inertContext(overrides: Record<string, unknown>): unknown {
  const context: unknown = new Proxy(overrides, {
    get: (target, key) => {
      if (key in target) return target[key as string];
      if (typeof key === 'string' && /^[A-Z]/.test(key)) return 0;
      return () => context;
    },
    set: () => true,
  });
  return context;
}

const webgl2 = inertContext({ isContextLost: () => false });
const canvas2d = inertContext({
  measureText: (text: string) => ({ width: text.length * 8 }),
  getImageData: (_x: number, _y: number, width: number, height: number) =>
    ({ width, height, data: new Uint8ClampedArray(width * height * 4) }),
});
if (typeof HTMLCanvasElement !== 'undefined') {
  HTMLCanvasElement.prototype.getContext = function getContext(kind: string) {
    if (kind === 'webgl2') return webgl2;
    if (kind === '2d') return canvas2d;
    return null;
  } as typeof HTMLCanvasElement.prototype.getContext;
}

// happy-dom derives navigator.platform from an X11 user agent. attn ships as a macOS app, so
// the suite defaults to Mac glyphs and Cmd matching; non-mac tests override this per test.
if (typeof navigator !== 'undefined') {
  Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true });
}

if (typeof Element !== 'undefined') {
  const realAnimate = Element.prototype.animate;
  Element.prototype.animate = function animationThatFinishesAtOnce(...args) {
    const animation = realAnimate.apply(this, args);
    animation.finished.catch(() => {});
    queueMicrotask(() => animation.finish());
    return animation;
  };
}

if (typeof window !== 'undefined') {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

// A class, not a vi.fn() returning an object: an arrow function cannot be `new`-ed.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver = ResizeObserverStub;

const WINDOW_TILE_AREA = { width: 3840, height: 1200 };
if (typeof HTMLElement !== 'undefined') {
  const nativeRect = HTMLElement.prototype.getBoundingClientRect;
  HTMLElement.prototype.getBoundingClientRect = function (this: HTMLElement) {
    if (this.classList.contains('session-terminal-panes')) return new DOMRect(0, 0, WINDOW_TILE_AREA.width, WINDOW_TILE_AREA.height);
    return nativeRect.call(this);
  };
}

// Node 22+ ships a built-in `localStorage` that shadows happy-dom's Storage and
// lacks its methods unless --localstorage-file is set.
if (typeof window !== 'undefined') {
  const ensureLocalStorage = () => {
    const candidate = window.localStorage;
    if (candidate && typeof candidate.getItem === 'function') {
      return;
    }
    const data = new Map<string, string>();
    const storage: Storage = {
      get length() { return data.size; },
      clear: () => data.clear(),
      getItem: (key: string) => (data.has(key) ? data.get(key)! : null),
      key: (index: number) => Array.from(data.keys())[index] ?? null,
      removeItem: (key: string) => { data.delete(key); },
      setItem: (key: string, value: string) => { data.set(key, String(value)); },
    };
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get: () => storage,
    });
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get: () => storage,
    });
  };
  ensureLocalStorage();
}

beforeEach(async () => {
  if (typeof window !== 'undefined') {
    Reflect.deleteProperty(navigator, 'clipboard');
    await navigator.clipboard.write([]);
    window.localStorage.clear();
    window.localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    window.localStorage.setItem(WHATS_NEW_BANNER_STORAGE_KEY, WHATS_NEW_ID);
  }
  forgetAppMemory();
  resetTauriFakes();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});
