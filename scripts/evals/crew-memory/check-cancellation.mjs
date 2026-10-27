#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { existsSync, mkdirSync, readdirSync, readFileSync, watch } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startStubModel } from '../../../app/scripts/real-app-harness/stubModel.mjs';
import { DEMO_ANTHROPIC_KEY } from '../../../app/scripts/demo-harness/agentHome.mjs';
import { json, save } from './trial.mjs';

for (const key of Object.keys(process.env)) {
  if (/^(CLAUDE_|ANTHROPIC_)/.test(key) || /TOKEN|API_KEY/.test(key)) delete process.env[key];
}
const statePath = resolve(process.argv[2]);
const config = json(statePath), flow = dirname(statePath);
assert.equal(config.local_provider_only, true, 'use a separately reviewed localhost-only cancellation configuration');
assert.ok(config.harness_sha, 'the reviewer must approve this exact dummy-provider configuration first');
const baseline = join(flow, 'baseline'), out = join(baseline, 'out');
for (const file of ['out', 'results.jsonl', 'errors.jsonl', 'progress.txt'])
  assert.equal(existsSync(join(baseline, file)), false, 'the cancellation flow must have no previous attempts');
mkdirSync(out, { recursive: true });
const requests = [];
const stub = await startStubModel(request => {
  requests.push({ model: request.model, prompt: request.prompt });
  return { say: 'Unpaid cancellation proof.' };
});
let runtime, child, stdout = '', stderr = '';
const watching = watch(out, { recursive: true }, (_, file) => {
  if (runtime || !file?.endsWith('runtime.json')) return;
  runtime = json(join(out, file));
  child.kill('SIGINT');
});
try {
  child = spawn(process.execPath, [join(dirname(fileURLToPath(import.meta.url)), 'run.mjs'),
    '--flow', flow, '--variant', 'baseline', '--model', config.member_model,
    '--reps', String(config.reps), '--pilot-stage', 'units'], {
    env: { ...process.env, ANTHROPIC_BASE_URL: stub.origin,
      ANTHROPIC_API_KEY: DEMO_ANTHROPIC_KEY, CLAUDE_CODE_OAUTH_TOKEN: DEMO_ANTHROPIC_KEY,
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_AUTOUPDATER: '1',
      DISABLE_TELEMETRY: '1', DISABLE_ERROR_REPORTING: '1' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stdout.on('data', data => { stdout += data; });
  child.stderr.on('data', data => { stderr += data; });
  console.log(`unpaid CLI cancellation proof pid=${child.pid}; provider=${stub.origin}`);
  const [code, signal] = await once(child, 'exit');
  assert.equal(code, 130, `expected graceful interruption, got ${code ?? signal}: ${stderr}`);
  assert.ok(runtime, 'interrupt only after the real daemon-ready receipt');
  assert.equal(existsSync(runtime.data_dir), false, 'the named instance must be cleaned');
  const errors = readFileSync(join(baseline, 'errors.jsonl'), 'utf8').trim().split('\n').map(line => JSON.parse(line));
  assert.equal(errors.length, 1);
  assert.equal(errors[0].failure_class, 'interrupted');
  assert.equal(existsSync(join(baseline, 'results.jsonl')), false);
  const attempts = readdirSync(out);
  assert.equal(attempts.length, 1, 'the second unit case must not start');
  for (const file of ['runtime.json', 'partial-member-receipts.json', 'model-calls.json', 'events.json', 'controller-handoffs.json'])
    assert.ok(existsSync(join(out, attempts[0], file)), `${file} must survive interruption`);
  save(join(flow, 'cli-cancellation-proof.json'), {
    paid_calls: 0, surface: 'run.mjs CLI SIGINT after real daemon-ready receipt',
    code, signal, runtime, cleaned: true, attempts: attempts.length,
    errors, requests, stdout, stderr,
  });
  console.log('PASS: CLI SIGINT exits 130, preserves one interrupted attempt, cleans its instance and starts no further case; paid calls=0');
} finally {
  watching.close();
  if (child && child.exitCode === null && child.signalCode === null) child.kill('SIGINT');
  await stub.close();
}
