import '@testing-library/jest-dom/vitest';
import { beforeEach, vi } from 'vitest';
import type * as Zustand from 'zustand';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from '../hooks/useWhatsNew';
import { forgetAppMemory } from './appMemory';

vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => false),
  convertFileSrc: vi.fn((filePath: string) => `asset://localhost/${filePath}`),
}));

vi.mock('@tauri-apps/api/app', () => ({
  getVersion: vi.fn(async () => '0.0.0'),
}));

vi.mock('@tauri-apps/api/event', () => ({
  emit: vi.fn(async () => {}),
  listen: vi.fn(async () => () => {}),
}));

vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: vi.fn(() => ({
    hide: vi.fn(async () => {}),
    isVisible: vi.fn(async () => true),
  })),
}));

vi.mock('@tauri-apps/api/path', () => ({
  downloadDir: vi.fn(async () => '/Users/me/Downloads'),
  homeDir: vi.fn(async () => '/Users/me'),
  join: vi.fn(async (...parts: string[]) => parts.join('/')),
}));

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async () => () => {}),
  getCurrent: vi.fn(async () => []),
}));

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(async () => {}),
  openPath: vi.fn(async () => {}),
  revealItemInDir: vi.fn(async () => {}),
}));

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(async () => null),
  save: vi.fn(async () => null),
}));

vi.mock('@tauri-apps/plugin-fs', () => ({
  BaseDirectory: { AppLocalData: 'AppLocalData' },
  exists: vi.fn(async () => false),
  mkdir: vi.fn(async () => {}),
  readTextFile: vi.fn(async () => ''),
  stat: vi.fn(async () => ({ size: 0 })),
  writeTextFile: vi.fn(async () => {}),
}));

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
globalThis.ResizeObserver = ResizeObserverStub;

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

beforeEach(() => {
  if (typeof window !== 'undefined') {
    window.localStorage.clear();
    window.localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
  }
  forgetAppMemory();
  vi.mocked(isTauri).mockReset();
  vi.mocked(invoke).mockReset();
});
