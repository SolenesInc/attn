// @vitest-environment node
// Spike 0 cross-check: decode a captured daemon snapshot with the web decoder
// and compare with the Rust client's grid. Not committed.
// @ts-expect-error -- @types/node is only a transitive peer here
import { readFileSync, writeFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { Ghostty } from './index';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

describe('spike0 cross-check', () => {
  it('decodes the captured snapshot like the Rust client', async () => {
    const dir = process.env.SPIKE0_OUT;
    if (!dir) return;
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
    const ghostty = new Ghostty(instance);
    const result = JSON.parse(readFileSync(`${dir}/attach_result.json`, 'utf8'));
    const bytes = new Uint8Array(readFileSync(`${dir}/snapshot.bin`));
    const terminal = ghostty.createTerminal(result.snapshot.cols, result.snapshot.rows);
    const history = terminal.adoptSnapshot(bytes);
    let pages = 0;
    while (history.decodeNextPage() !== null) pages += 1;
    history.close();
    const lines: string[] = [];
    for (let y = 0; y < terminal.rows; y++) {
      let line = '';
      for (const cell of terminal.getLine(y) ?? []) {
        if (cell.width === 0) continue;
        line += cell.codepoint ? String.fromCodePoint(cell.codepoint) : ' ';
      }
      lines.push(line.trimEnd());
    }
    const web = lines.join('\n') + '\n';
    writeFileSync(`${dir}/grid-web.txt`, web);
    console.log(`web decoder: ${terminal.cols}x${terminal.rows}, ${pages} history pages, scrollback ${terminal.getScrollbackLength()}`);
    expect(web).toBe(readFileSync(`${dir}/grid-base.txt`, 'utf8'));
  });
});
