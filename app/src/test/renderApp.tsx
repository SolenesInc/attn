// @ts-expect-error -- @types/node is not an app dependency
import { readFileSync } from 'node:fs';
import type { ReactNode } from 'react';
import { act, render, type RenderResult } from '@testing-library/react';
import { vi } from 'vitest';
import App from '../App';
import { useAppDaemon } from '../application/useAppDaemon';
import { DaemonApiProvider, type DaemonApi } from '../contexts/DaemonApiContext';
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

async function mountConnected(daemon: ScriptedDaemon, ui: ReactNode): Promise<RenderResult> {
  const result = render(ui);
  await act(() => daemon.connected());
  return result;
}

export async function renderApp(options: ScriptedDaemonOptions = {}): Promise<AppRender> {
  const daemon = installScriptedDaemon(options);
  return { daemon, ...(await mountConnected(daemon, <App />)) };
}

interface DaemonRender extends Omit<RenderResult, 'rerender'> {
  daemon: ScriptedDaemon;
  api: { current: DaemonApi };
  rerender: (ui: ReactNode) => void;
}

function DaemonHost({ onApi, children }: { onApi: (api: DaemonApi) => void; children: ReactNode }) {
  const daemonApi = useAppDaemon({});
  onApi(daemonApi);
  return <DaemonApiProvider api={daemonApi}>{children}</DaemonApiProvider>;
}

export async function renderWithDaemon(ui: ReactNode = null, options: ScriptedDaemonOptions = {}): Promise<DaemonRender> {
  const daemon = installScriptedDaemon(options);
  let latestApi: DaemonApi | undefined;
  const api = {
    get current(): DaemonApi {
      if (!latestApi) throw new Error('the daemon socket has not rendered');
      return latestApi;
    },
  };
  const host = (children: ReactNode) => (
    <DaemonHost onApi={(rendered) => { latestApi = rendered; }}>{children}</DaemonHost>
  );
  const result = await mountConnected(daemon, host(ui));
  return { ...result, daemon, api, rerender: (next) => result.rerender(host(next)) };
}
