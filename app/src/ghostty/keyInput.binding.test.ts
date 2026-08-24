// @vitest-environment node
// @ts-expect-error -- @types/node is only a transitive peer here (see terminal.binding.test.ts)
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { beforeAll, describe, expect, it } from 'vitest';
import { Window } from 'happy-dom';
import { attachTerminalInput } from './input';
import { Ghostty } from './index';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

let ghostty: Ghostty;

beforeAll(async () => {
  const module = await WebAssembly.compile(readFileSync(wasmPath));
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(module, {
    env: {
      log: (ptr: number, len: number) => {
        const memory = (instance.exports.memory as WebAssembly.Memory).buffer;
        console.log('[ghostty-vt]', new TextDecoder().decode(new Uint8Array(memory, ptr, len)));
      },
    },
  });
  ghostty = new Ghostty(instance);
});

function encodeKey(code: 'ArrowUp' | 'ArrowDown', applicationCursor = false): string[] {
  const window = new Window();
  const container = window.document.createElement('div');
  const terminal = ghostty.createTerminal();
  if (applicationCursor) terminal.write('\x1b[?1h');
  const data: string[] = [];
  const dispose = attachTerminalInput({
    element: container as unknown as HTMLElement,
    terminal: () => terminal,
    send: (value) => data.push(value),
    interceptKey: () => false,
    onError: (_operation, error) => { throw error; },
  });

  container.dispatchEvent(new window.KeyboardEvent('keydown', {
    key: code,
    code,
    bubbles: true,
    cancelable: true,
  }));
  dispose();
  terminal.free();
  return data;
}

describe('first-party key input against the pinned libghostty-vt', () => {
  it('encodes up and down arrows in normal cursor mode', () => {
    expect(encodeKey('ArrowUp')).toEqual(['\x1b[A']);
    expect(encodeKey('ArrowDown')).toEqual(['\x1b[B']);
  });

  it('encodes up and down arrows in application cursor mode', () => {
    expect(encodeKey('ArrowUp', true)).toEqual(['\x1bOA']);
    expect(encodeKey('ArrowDown', true)).toEqual(['\x1bOB']);
  });
});
