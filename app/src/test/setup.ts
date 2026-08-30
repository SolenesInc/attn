import '@testing-library/jest-dom/vitest';
import { beforeEach, vi } from 'vitest';
import { gardenScrollMemory, useGardenWalk } from '../store/gardenWalk';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from '../hooks/useWhatsNew';

vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => false),
  convertFileSrc: vi.fn((filePath: string) => `asset://localhost/${filePath}`),
}));

vi.mock('@tauri-apps/api/app', () => ({
  getVersion: vi.fn(async () => '0.0.0'),
}));

// happy-dom derives navigator.platform from an X11 user agent. attn ships as a macOS app, so
// the suite defaults to Mac glyphs and Cmd matching; non-mac tests override this per test.
if (typeof navigator !== 'undefined') {
  Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true });
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
(globalThis as typeof globalThis & { ResizeObserver: typeof ResizeObserver }).ResizeObserver =
  ResizeObserverStub as unknown as typeof ResizeObserver;

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

  // Counts the one-time "what's new" announcement as seen so it never renders
  // over unrelated tests.
  window.localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
}

// The garden walk is module state, so a test that drilled somewhere would hand
// its depth to the next one.
beforeEach(() => {
  useGardenWalk.setState({ trail: [] });
  gardenScrollMemory.clear();
});
