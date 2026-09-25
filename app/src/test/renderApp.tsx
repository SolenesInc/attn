// @ts-expect-error -- @types/node is not an app dependency
import { readFileSync } from 'node:fs';
import { act, fireEvent, render, type RenderResult } from '@testing-library/react';
import { vi } from 'vitest';
import App from '../App';
import { defaultShortcut, type ShortcutId } from '../shortcuts/registry';
import { installScriptedDaemon, type ScriptedDaemon, type ScriptedDaemonOptions } from './scriptedDaemon';

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
HTMLCanvasElement.prototype.getContext = function getContext(kind: string) {
  if (kind === 'webgl2') return webgl2;
  if (kind === '2d') return canvas2d;
  return null;
} as typeof HTMLCanvasElement.prototype.getContext;

export interface AppRender extends RenderResult {
  daemon: ScriptedDaemon;
}

export async function renderApp(options: ScriptedDaemonOptions = {}): Promise<AppRender> {
  const daemon = installScriptedDaemon(options);
  const result = render(<App />);
  await act(() => daemon.connected());
  return { daemon, ...result };
}

export async function gesture(daemon: ScriptedDaemon, action: () => void) {
  action();
  await daemon.idle();
}

export function pressShortcut(id: ShortcutId, target: Window | Element = window) {
  const { key, code, meta, shift, alt, ctrl } = defaultShortcut(id);
  fireEvent.keyDown(target, { key, code, metaKey: !!meta, shiftKey: !!shift, altKey: !!alt, ctrlKey: !!ctrl });
}
