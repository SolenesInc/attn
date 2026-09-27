// @vitest-environment node
// Regenerate the fixture with ATTN_UPDATE_FIXTURES=1 go test ./internal/ghosttyvt.
// @ts-expect-error -- @types/node is only a transitive peer here
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { beforeAll, describe, expect, it } from 'vitest';
import { CellFlags, Ghostty } from './index';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));
const fixturePath = fileURLToPath(new URL('./testdata/native-snapshot.bin', import.meta.url));

let ghostty: Ghostty;
let snapshot: Uint8Array;

beforeAll(async () => {
  const mod = await WebAssembly.compile(readFileSync(wasmPath));
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(mod, {
    env: {
      log: (ptr: number, len: number) => {
        const memory = (instance.exports.memory as WebAssembly.Memory).buffer;
        console.log('[ghostty-vt]', new TextDecoder().decode(new Uint8Array(memory, ptr, len)));
      },
    },
  });
  ghostty = new Ghostty(instance);
  snapshot = new Uint8Array(readFileSync(fixturePath));
});

describe('adoptSnapshot', () => {
  it('carries styling, not just codepoints', () => {
    const terminal = ghostty.createTerminal(80, 24, {});
    terminal.write('this content belongs to the session being replaced\r\n');
    terminal.adoptSnapshot(snapshot);
    const styled = terminal.getLine(2)![0];
    expect(styled.flags & CellFlags.BOLD).toBeTruthy();
    expect(styled.flags & CellFlags.UNDERLINE).toBeTruthy();
    terminal.free();
  });

  it('preserves extended indexed colors when restoring a themed terminal', () => {
    const palette = Array.from({ length: 16 }, (_, index) => index === 3 ? 0x123456 : 0);
    const terminal = ghostty.createTerminal(80, 24, { palette });
    const historyDecoder = terminal.adoptSnapshot(snapshot);
    while (historyDecoder.decodeNextPage() !== null) { /* drain */ }

    terminal.write('m\x1b[38;5;196mR\x1b[48;5;46mG\x1b[33mY');
    const cells = terminal.getLine(5)!;
    expect(cells[8]).toMatchObject({ codepoint: 'R'.codePointAt(0), fg_r: 0xff, fg_g: 0x00, fg_b: 0x00 });
    expect(cells[9]).toMatchObject({ codepoint: 'G'.codePointAt(0), bg_r: 0x00, bg_g: 0xff, bg_b: 0x00 });
    expect(cells[10]).toMatchObject({ codepoint: 'Y'.codePointAt(0), fg_r: 0x12, fg_g: 0x34, fg_b: 0x56 });
    terminal.free();
  });
});
