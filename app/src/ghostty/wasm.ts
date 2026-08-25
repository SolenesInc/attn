import ghosttyWasmUrl from '../../vendor/ghostty-vt/ghostty-vt.wasm?url';
import { Ghostty } from './index';

export { ghosttyWasmUrl };

let compiledGhosttyModule: Promise<WebAssembly.Module> | null = null;

async function compileGhosttyModule(): Promise<WebAssembly.Module> {
  const response = await fetch(ghosttyWasmUrl);
  if (!response.ok) {
    throw new Error(`Failed to fetch Ghostty WASM: ${response.status} ${response.statusText}`);
  }
  const bytes = await response.arrayBuffer();
  if (bytes.byteLength === 0) {
    throw new Error(`Ghostty WASM file is empty: ${ghosttyWasmUrl}`);
  }
  return WebAssembly.compile(bytes);
}

async function getCompiledGhosttyModule(): Promise<WebAssembly.Module> {
  if (!compiledGhosttyModule) {
    compiledGhosttyModule = compileGhosttyModule().catch((error) => {
      compiledGhosttyModule = null;
      throw error;
    });
  }
  return compiledGhosttyModule;
}

// A fresh instance per terminal keeps pane-level crash isolation; only the
// immutable WebAssembly.Module is shared.
export async function loadGhostty(): Promise<Ghostty> {
  const module = await getCompiledGhosttyModule();
  let instance: WebAssembly.Instance | null = null;
  const instantiated = await WebAssembly.instantiate(module, {
    env: {
      log: (ptr: number, len: number) => {
        if (!instance) return;
        const memory = instance.exports.memory as WebAssembly.Memory;
        const bytes = new Uint8Array(memory.buffer, ptr, len);
        console.log('[ghostty-vt]', new TextDecoder().decode(bytes));
      },
    },
  });
  instance = instantiated;
  return new Ghostty(instance);
}

export function resetGhosttyModuleCacheForTests(): void {
  compiledGhosttyModule = null;
}
