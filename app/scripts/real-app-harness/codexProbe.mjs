import fs from 'node:fs';
import path from 'node:path';

export function createCodexProbe(root) {
  const log = path.join(root, 'codex-invocations.jsonl');
  const executable = path.join(root, 'codex-probe.mjs');
  fs.writeFileSync(
    executable,
    `#!/usr/bin/env node
import fs from 'node:fs';
import { spawnSync } from 'node:child_process';
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({argv: process.argv.slice(2), at: new Date().toISOString()}) + '\\n');
const hook = (args, input) => spawnSync(process.env.ATTN_WRAPPER_PATH || 'attn', args, { env: process.env, input: JSON.stringify(input), stdio: ['pipe', 'ignore', 'inherit'] });
hook(['_hook-state', 'working'], {});
hook(['_hook-state', 'waiting_input'], {});
setInterval(() => {}, 1000);
`,
    { mode: 0o700 },
  );
  return { executable, log };
}

export function codexInvocations(log) {
  if (!fs.existsSync(log)) return [];
  return fs.readFileSync(log, 'utf8').trim().split('\n').filter(Boolean).map((line) => JSON.parse(line));
}
