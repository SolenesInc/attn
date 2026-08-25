// Repro for a ghostty-vt wasm resize hang: exit 0 = fixed, 1 = hang, 2 = trap.
// Needs a widen of >=+10 cols (59->69) then two narrow-by-1 resizes to reproduce.

import { Worker, isMainThread, parentPort } from 'node:worker_threads';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const APP_ROOT = fileURLToPath(new URL('..', import.meta.url));
const WASM_PATH = `${APP_ROOT}/vendor/ghostty-vt/ghostty-vt.wasm`;

// The malformed-looking OSC 8 fragments are load-bearing; plain ASCII of the
// same length does not reproduce.
const PAYLOAD =
  '\x1b]8;;/\x1bine 31 ┌──┐│\x1b]8;;2\x1b\\entry 32\x1b]8;;7\x1b\\entry ' + 'w'.repeat(104);

const WATCHDOG_MS = 2500;

if (isMainThread) {
  main();
} else {
  runInWorker();
}

async function runInWorker() {
  const bytes = readFileSync(WASM_PATH);
  const mod = await WebAssembly.compile(bytes);
  const instance = await WebAssembly.instantiate(mod, { env: { log: () => {} } });
  const e = instance.exports;
  const dv = () => new DataView(e.memory.buffer);

  const out = e.ghostty_wasm_alloc_opaque();
  e.ghostty_terminal_new(0, out, 59, 58);
  const term = dv().getUint32(out, true);

  const write = (text) => {
    const payload = new TextEncoder().encode(text);
    const ptr = e.ghostty_wasm_alloc(payload.length);
    new Uint8Array(e.memory.buffer).set(payload, ptr);
    e.ghostty_terminal_vt_write(term, ptr, payload.length);
    e.ghostty_wasm_free(ptr, payload.length);
  };

  const steps = [
    ['write payload', () => write(PAYLOAD)],
    ['resize(69, 58) [widen]', () => e.ghostty_terminal_resize(term, 69, 58)],
    ['resize(68, 58) [narrow #1]', () => e.ghostty_terminal_resize(term, 68, 58)],
    ['resize(67, 58) [narrow #2 -- expected to hang]', () => e.ghostty_terminal_resize(term, 67, 58)],
  ];

  for (const [label, fn] of steps) {
    parentPort.postMessage({ type: 'starting', label });
    fn();
    parentPort.postMessage({ type: 'done', label });
  }
  parentPort.postMessage({ type: 'all-done' });
}

async function main() {
  const worker = new Worker(new URL(import.meta.url), { workerData: {} });
  let lastLabel = '(not started)';
  let settled = false;

  const finish = (code, message) => {
    if (settled) return;
    settled = true;
    console.log(message);
    clearTimeout(watchdog);
    worker.terminate().finally(() => process.exit(code));
  };

  let watchdog = setTimeout(() => {
    finish(1, `HANG: step "${lastLabel}" did not return within ${WATCHDOG_MS}ms (wasm-level infinite loop). This is expected -- confirms the repro.`);
  }, WATCHDOG_MS);

  worker.on('message', (msg) => {
    if (msg.type === 'starting') {
      lastLabel = msg.label;
      console.log('>>> ' + msg.label);
      clearTimeout(watchdog);
      watchdog = setTimeout(() => {
        finish(1, `HANG: step "${lastLabel}" did not return within ${WATCHDOG_MS}ms (wasm-level infinite loop). This is expected -- confirms the repro.`);
      }, WATCHDOG_MS);
    } else if (msg.type === 'done') {
      console.log('<<< ' + msg.label);
    } else if (msg.type === 'all-done') {
      finish(0, 'NO_HANG: all steps completed -- repro did not reproduce this run.');
    }
  });

  worker.on('error', (err) => {
    finish(2, 'EXCEPTION/TRAP: ' + (err && (err.stack || err.message || String(err))));
  });

  worker.on('exit', (code) => {
    if (!settled) {
      settled = true;
      clearTimeout(watchdog);
      console.log(`worker exited unexpectedly with code ${code}`);
      process.exit(2);
    }
  });
}
