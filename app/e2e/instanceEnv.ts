import { execFileSync } from 'child_process';
import * as fs from 'fs';
import * as path from 'path';
import { fileURLToPath } from 'url';

const E2E_DIR = path.dirname(fileURLToPath(import.meta.url));

export function resolveAttnBinaryPath(): string {
  const candidates = [
    process.env.ATTN_E2E_BIN,
    path.resolve(E2E_DIR, '../../attn'),
  ].filter((candidate): candidate is string => Boolean(candidate));

  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  }

  throw new Error(
    `attn binary not found. Tried: ${candidates.join(', ')}. ` +
      `Set ATTN_E2E_BIN or build binary with 'go build -o ./attn ./cmd/attn'.`
  );
}

export const E2E_CLIENT_TOKEN = 'e2e-client-token';

export interface E2EPorts {
  instance: string;
  daemonPort: string;
  vitePort: string;
}

// Default instance keeps fixed ports (19849 / 1421) so a run needs no attn binary
// at config-load time; a named instance gets a disjoint band so agents can't collide.
export function e2ePorts(): E2EPorts {
  const instance = (process.env.ATTN_INSTANCE ?? '').trim();
  if (instance === '') {
    return { instance: '', daemonPort: '19849', vitePort: '1421' };
  }
  const attn = resolveAttnBinaryPath();
  const out = execFileSync(attn, ['instance', 'resolve', '--json'], {
    encoding: 'utf8',
  });
  const resolved = JSON.parse(out) as {
    instance: string;
    e2eDaemonPort: string;
    e2eVitePort: string;
  };
  return {
    instance: resolved.instance,
    daemonPort: resolved.e2eDaemonPort,
    vitePort: resolved.e2eVitePort,
  };
}
