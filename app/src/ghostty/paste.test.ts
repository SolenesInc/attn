// @vitest-environment node
// @ts-expect-error -- node types
import { readFileSync } from 'node:fs';
// @ts-expect-error -- node types
import { fileURLToPath } from 'node:url';
import { expect, it } from 'vitest';
import { Ghostty } from './index';
const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));
it('pasted text cannot end bracketed paste early or carry control bytes', async () => {
  const mod = await WebAssembly.compile(readFileSync(wasmPath));
  const inst = await WebAssembly.instantiate(mod, { env: { log: () => {} } });
  const t = new Ghostty(inst).createTerminal(80, 24);
  t.write('\x1b[?2004h');
  const out = t.formatPaste('x\x1b[201~\nrm -rf ~\n');
  expect(out.slice(6, -6)).not.toContain('\x1b');
});
