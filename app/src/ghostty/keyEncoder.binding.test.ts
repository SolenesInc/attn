// @vitest-environment node
// @ts-expect-error -- @types/node is only a transitive peer here (see terminal.binding.test.ts)
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { beforeAll, describe, expect, it } from 'vitest';
import type { GhosttyExports } from './abi';
import { Ghostty } from './index';
import { GhosttyKeyEncoder, readGhosttyKeyAbi, type GhosttyKeyAbi } from './keyEncoder';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

let ghostty: Ghostty;
let exports: GhosttyExports;

function typeManifest(): Record<string, unknown> {
  const ptr = exports.ghostty_type_json();
  const memory = new Uint8Array(exports.memory.buffer);
  let end = ptr;
  while (memory[end] !== 0) end += 1;
  return JSON.parse(new TextDecoder().decode(memory.subarray(ptr, end)));
}

function exportsWithManifest(manifest: Record<string, unknown>): { exports: GhosttyExports; free: () => void } {
  const bytes = new TextEncoder().encode(`${JSON.stringify(manifest)}\0`);
  const ptr = exports.ghostty_wasm_alloc(bytes.length);
  new Uint8Array(exports.memory.buffer, ptr, bytes.length).set(bytes);
  return {
    exports: {
      ...exports,
      ghostty_type_json: () => ptr,
    } as GhosttyExports,
    free: () => exports.ghostty_wasm_free(ptr, bytes.length),
  };
}

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
  exports = instance.exports as GhosttyExports;
  ghostty = new Ghostty(instance);
});

describe('the first-party libghostty key encoder', () => {
  it('rejects an incomplete key ABI before creating a terminal', () => {
    expect(() => readGhosttyKeyAbi({
      ...exports,
      ghostty_key_encoder_encode: undefined,
    } as unknown as GhosttyExports)).toThrow(
      'libghostty-vt is missing required WASM export ghostty_key_encoder_encode',
    );
  });

  it('rejects an unknown manifest schema before creating a terminal', () => {
    const manifest = typeManifest();
    manifest.schema = 2;
    const replacement = exportsWithManifest(manifest);
    try {
      expect(() => readGhosttyKeyAbi(replacement.exports))
        .toThrow('Unsupported libghostty-vt type manifest schema: 2');
    } finally {
      replacement.free();
    }
  });

  it('rejects a missing required key name instead of using a stale number', () => {
    const manifest = typeManifest() as {
      types: { GhosttyKey: { values: Record<string, number> } };
    };
    delete manifest.types.GhosttyKey.values.ARROW_UP;
    const replacement = exportsWithManifest(manifest as unknown as Record<string, unknown>);
    try {
      expect(() => readGhosttyKeyAbi(replacement.exports))
        .toThrow('libghostty-vt type manifest is missing GhosttyKey.ARROW_UP');
    } finally {
      replacement.free();
    }
  });

  it('reads key values from the current type manifest instead of a copied enum', () => {
    const abi = readGhosttyKeyAbi(exports);
    expect(abi.keys.FN).toBeDefined();
    expect(abi.keys.FN_LOCK).toBeDefined();
    expect(abi.keys.FN_LOCK).not.toBe(abi.keys.FN);
  });

  it('encodes arrows from the terminal cursor mode', () => {
    const terminal = ghostty.createTerminal();
    expect(terminal.encodeKey({ action: 'press', key: 'ARROW_UP' })).toBe('\x1b[A');
    expect(terminal.encodeKey({ action: 'press', key: 'ARROW_DOWN' })).toBe('\x1b[B');

    terminal.write('\x1b[?1h');
    expect(terminal.encodeKey({ action: 'press', key: 'ARROW_UP' })).toBe('\x1bOA');
    expect(terminal.encodeKey({ action: 'press', key: 'ARROW_DOWN' })).toBe('\x1bOB');
    terminal.free();
  });

  it('encodes printable text through the same current ABI', () => {
    const terminal = ghostty.createTerminal();
    expect(terminal.encodeKey({
      action: 'press',
      key: 'A',
      utf8: 'a',
      unshiftedCodepoint: 'a'.codePointAt(0),
    })).toBe('a');
    expect(terminal.encodeKey({
      action: 'press',
      key: 'A',
      mods: 1,
      consumedMods: 1,
      utf8: 'A',
      unshiftedCodepoint: 'a'.codePointAt(0),
    })).toBe('A');
    terminal.free();
  });

  it('preserves macOS Option-as-Alt while synchronizing terminal Alt mode', () => {
    const terminal = ghostty.createTerminal();
    const optionF = {
      action: 'press' as const,
      key: 'F',
      mods: 4,
      unshiftedCodepoint: 'f'.codePointAt(0),
    };
    expect(terminal.encodeKey(optionF)).toBe('\x1bf');
    terminal.write('\x1b[?1036l');
    expect(terminal.encodeKey(optionF)).toBe('');
    terminal.write('\x1b[?1036h');
    expect(terminal.encodeKey(optionF)).toBe('\x1bf');
    terminal.free();
  });

  it('synchronizes modifyOtherKeys from the terminal', () => {
    const terminal = ghostty.createTerminal();
    terminal.write('\x1b[>4;2m');
    expect(terminal.encodeKey({
      action: 'press', key: 'I', mods: 3, consumedMods: 1, utf8: 'I', unshiftedCodepoint: 105,
    })).toBe('\x1b[27;6;73~');
    terminal.free();
  });

  it('encodes Kitty press, repeat, and release actions', () => {
    const terminal = ghostty.createTerminal();
    terminal.write('\x1b[>31u');
    expect(terminal.encodeKey({
      action: 'press', key: 'A', utf8: 'a', unshiftedCodepoint: 97,
    })).toBe('\x1b[97;;97u');
    expect(terminal.encodeKey({
      action: 'repeat', key: 'A', utf8: 'a', unshiftedCodepoint: 97,
    })).toBe('\x1b[97;1:2;97u');
    expect(terminal.encodeKey({
      action: 'release', key: 'A', unshiftedCodepoint: 97,
    })).toBe('\x1b[97;1:3u');
    terminal.free();
  });

  it('normalizes paste newlines and honors bracketed paste mode', () => {
    const terminal = ghostty.createTerminal();
    expect(terminal.formatPaste('one\r\ntwo\nthree')).toBe('one\rtwo\rthree');
    terminal.write('\x1b[?2004h');
    expect(terminal.formatPaste('one\r\ntwo\nthree'))
      .toBe('\x1b[200~one\rtwo\rthree\x1b[201~');
    terminal.free();
  });

  it('synchronizes application keypad mode from the terminal', () => {
    const terminal = ghostty.createTerminal();
    const key = {
      action: 'press' as const,
      key: 'NUMPAD_1',
      utf8: '1',
      unshiftedCodepoint: '1'.codePointAt(0),
    };
    expect(terminal.encodeKey(key)).toBe('1');
    // DEC 1035 must be off to observe application-keypad mode.
    terminal.write('\x1b[?1035l\x1b=');
    expect(terminal.encodeKey(key)).toBe('\x1bOq');
    terminal.free();
  });

  it('grows the output buffer when Kitty associated text exceeds the fast path', () => {
    const terminal = ghostty.createTerminal();
    terminal.write('\x1b[>31u');
    const encoded = terminal.encodeKey({
      action: 'press',
      key: 'A',
      utf8: 'a'.repeat(40),
      unshiftedCodepoint: 'a'.codePointAt(0),
    });
    expect(encoded.length).toBeGreaterThan(64);
    expect(encoded.startsWith('\x1b[')).toBe(true);
    terminal.free();
  });

  it('rejects use after the terminal frees its encoder', () => {
    const terminal = ghostty.createTerminal();
    terminal.encodeKey({ action: 'press', key: 'ARROW_LEFT' });
    terminal.free();
    expect(() => terminal.encodeKey({ action: 'press', key: 'ARROW_RIGHT' }))
      .toThrow('GhosttyTerminal is freed');
    terminal.free();
  });

  it('rolls back every resource when initial output allocation fails', () => {
    const calls: string[] = [];
    const memory = new WebAssembly.Memory({ initial: 1 });
    let allocation = 0;
    const instrumented = {
      memory,
      ghostty_wasm_alloc_opaque: () => 8,
      ghostty_wasm_free_opaque: (ptr: number) => calls.push(`opaque:${ptr}`),
      ghostty_wasm_alloc: (size: number) => {
        allocation += 1;
        if (allocation === 1) return 32;
        throw new Error(`allocation failed for ${size}`);
      },
      ghostty_wasm_free: (ptr: number, size: number) => calls.push(`memory:${ptr}:${size}`),
      ghostty_key_encoder_new: (_allocator: number, out: number) => {
        new DataView(memory.buffer).setUint32(out, 101, true);
        return 0;
      },
      ghostty_key_encoder_free: (encoder: number) => calls.push(`encoder:${encoder}`),
      ghostty_key_event_new: (_allocator: number, out: number) => {
        new DataView(memory.buffer).setUint32(out, 202, true);
        return 0;
      },
      ghostty_key_event_free: (event: number) => calls.push(`event:${event}`),
    } as unknown as GhosttyExports;
    const abi = {
      keys: {},
      actions: {},
      encoderOptions: {},
      optionAsAlt: {},
    } as GhosttyKeyAbi;

    expect(() => new GhosttyKeyEncoder(instrumented, 303, abi))
      .toThrow('allocation failed for 64');
    expect(calls).toEqual([
      'memory:32:4',
      'event:202',
      'encoder:101',
      'opaque:8',
    ]);
  });
});
