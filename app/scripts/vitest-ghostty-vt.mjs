import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

export default function ensureGhosttyVtWasm() {
  execFileSync('bash', [fileURLToPath(new URL('./ensure-ghostty-vt-wasm.sh', import.meta.url))], { stdio: 'inherit' });
}
