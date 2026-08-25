// @vitest-environment node
import { describe, expect, it } from 'vitest';
// @types/node isn't a direct dependency of this package (only a transitive peer of
// vite/vitest), so these three Node APIs need a suppression.
// @ts-expect-error -- see above
import { spawn } from 'node:child_process';
// @ts-expect-error -- see above
import { execPath } from 'node:process';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';

// Guards the vendored ghostty-vt wasm against an infinite loop in its resize path: at the
// old pin, resizes never returned. Exit codes: 0 = returned, 1 = hang, 2 = trap.
const reproScript = fileURLToPath(
  new URL('../../scripts/repro-ghostty-vt-resize-hang.mjs', import.meta.url),
);

function runRepro(): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve, reject) => {
    const child = spawn(execPath, [reproScript], { stdio: ['ignore', 'pipe', 'pipe'] });
    let output = '';
    child.stdout.on('data', (chunk: unknown) => {
      output += String(chunk);
    });
    child.stderr.on('data', (chunk: unknown) => {
      output += String(chunk);
    });
    child.on('error', reject);
    child.on('close', (code: number | null) => resolve({ code, output }));
  });
}

describe('ghostty-vt wasm resize', () => {
  it(
    'completes consecutive narrowing resizes after hyperlink-heavy output',
    async () => {
      const { code, output } = await runRepro();

      expect(code, output).toBe(0);
    },
    60_000,
  );
});
