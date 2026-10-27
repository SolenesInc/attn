#!/usr/bin/env node
import assert from 'node:assert/strict';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { startStubModel } from '../../../app/scripts/real-app-harness/stubModel.mjs';
import { DEMO_ANTHROPIC_KEY } from '../../../app/scripts/demo-harness/agentHome.mjs';
import { Trial, json, save } from './trial.mjs';

for (const key of Object.keys(process.env)) {
  if (/^(CLAUDE_|ANTHROPIC_)/.test(key) || /TOKEN|API_KEY/.test(key)) delete process.env[key];
}
const path = resolve(process.argv[2]);
const config = json(path);
const flow = dirname(path);
const toolsRoot = mkdtempSync(join(tmpdir(), 'crew-eval-fixture-tools-'));
const wrapper = join(toolsRoot, 'fixture-claude');
writeFileSync(wrapper, `#!${process.execPath}\n` + `
const fs = require('node:fs');
const file = process.env.CLAUDE_CONFIG_DIR + '/.claude.json';
const state = JSON.parse(fs.readFileSync(file));
state.customApiKeyResponses = { approved: [${JSON.stringify(DEMO_ANTHROPIC_KEY.slice(-20))}], rejected: [] };
fs.writeFileSync(file, JSON.stringify(state));
process.execve(${JSON.stringify(config.claude_binary)}, [${JSON.stringify(config.claude_binary)}, ...process.argv.slice(2)], process.env);
`, { mode: 0o700 });
const requests = [];
const stub = await startStubModel(request => {
  assert.equal(request.model, config.member_model);
  requests.push({ model: request.model, system: request.systemPrompt });
  return { say: 'Ready for the disposable fixture setup.' };
});
Object.assign(process.env, { ANTHROPIC_BASE_URL: stub.origin, ANTHROPIC_API_KEY: DEMO_ANTHROPIC_KEY,
  CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_AUTOUPDATER: '1',
  DISABLE_TELEMETRY: '1', DISABLE_ERROR_REPORTING: '1' });
const receipts = [];
try {
  for (const input of json(config.cases_path).filter(row => config.stale_probe_ids.includes(row.id))) {
    const trial = new Trial({ ...config, claude_binary: wrapper, local_provider_only: true, memory: true },
      AbortSignal.timeout(config.case_timeout_s.use * 1000));
    try {
      await trial.start({ ...input, frozen_memory: undefined });
      const session = await trial.wake();
      const add = JSON.parse(await trial.cli(['memory', 'add', input.stale_entry, '--session', session, '--json']));
      const recall = JSON.parse(await trial.cli(['memory', 'recall', '.', '--session', session, '--json']));
      const entries = readFileSync(join(trial.home, 'memory', 'log.jsonl'), 'utf8').trim().split('\n').map(line => JSON.parse(line));
      assert.equal(entries.length, 1);
      assert.equal(entries[0].text, input.stale_entry);
      assert.ok(!entries[0].day);
      mkdirSync(dirname(input.frozen_memory), { recursive: true });
      cpSync(join(trial.home, 'memory'), input.frozen_memory, { recursive: true, errorOnExist: true, force: false });
      receipts.push({ id: input.id, session, instance: trial.instance, add, recall, entries,
        entry_bytes: Buffer.byteLength(input.stale_entry), fixture: input.frozen_memory });
      console.log(`froze ${input.id}: one CLI-written entry, ${Buffer.byteLength(input.stale_entry)} bytes`);
    } finally { await trial.stop(); }
  }
  assert.equal(receipts.length, 3);
  save(join(flow, 'frozen-memory-receipts.json'), { paid_usd: 0, local_provider_only: true, requests, receipts });
} finally { await stub.close(); rmSync(toolsRoot, { recursive: true, force: true }); }
