#!/usr/bin/env node
import assert from 'node:assert/strict';
import { EventEmitter, once } from 'node:events';
import { mkdirSync, readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { dirname, join, resolve } from 'node:path';
import { startStubModel } from '../../../app/scripts/real-app-harness/stubModel.mjs';
import { DEMO_ANTHROPIC_KEY } from '../../../app/scripts/demo-harness/agentHome.mjs';
import { Trial, json, save } from './trial.mjs';

for (const key of Object.keys(process.env)) {
  if (/^(CLAUDE_|ANTHROPIC_)/.test(key) || /TOKEN|API_KEY/.test(key)) delete process.env[key];
}
const config = json(resolve(process.argv[2]));
const proofDir = join(dirname(resolve(process.argv[2])), 'audit-lifecycle');
mkdirSync(proofDir, { recursive: true });
const input = json(config.cases_path).find(row => row.pilot && row.unit === 'use');
const requests = [], pending = [], observed = new EventEmitter();
const stub = await startStubModel(request => new Promise(answer => {
  requests.push(request);
  console.log(`mock request ${requests.length}: ${request.model}, ${request.tools.length} tools`);
  save(join(proofDir, `provider-request-${requests.length}.json`), request.body);
  pending.push({ request, answer });
  observed.emit('request');
}));
Object.assign(process.env, {
  ANTHROPIC_BASE_URL: stub.origin, ANTHROPIC_API_KEY: DEMO_ANTHROPIC_KEY,
  CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_AUTOUPDATER: '1',
  DISABLE_TELEMETRY: '1', DISABLE_ERROR_REPORTING: '1',
});
const abort = new AbortController();
process.once('SIGINT', () => abort.abort(new Error('unpaid lifecycle proof interrupted')));
const trial = new Trial({ ...config, local_provider_only: true }, AbortSignal.any([abort.signal, AbortSignal.timeout(config.case_timeout_s.use * 1000)]));
const take = async () => {
  while (true) {
    trial.signal.throwIfAborted();
    const index = pending.findIndex(({ request }) => request.model === config.member_model && request.tools.length);
    if (index >= 0) return pending.splice(index, 1)[0];
    if (pending.length) throw new Error(`unexpected native auxiliary request: ${pending[0].request.model}`);
    await once(observed, 'request', { signal: trial.signal });
  }
};
const calls = [];
try {
  console.log(`real-binary lifecycle proof pid=${process.pid}; paid calls=0; provider=${stub.origin}`);
  await trial.start({ ...input, files: { ...input.files, 'input.txt': 'provider-mock local file\n' } });
  console.log(`isolated instance=${trial.instance}`);
  const statePath = join(trial.env.CLAUDE_CONFIG_DIR, '.claude.json');
  const state = json(statePath);
  state.customApiKeyResponses = { approved: [DEMO_ANTHROPIC_KEY.slice(-20)], rejected: [] };
  save(statePath, state);
  const waking = trial.wake();
  waking.catch(() => {});
  const opening = await take();
  save(join(proofDir, 'opening-request.json'), opening.request.body);
  opening.answer({ tool: { id: 'proof-charter', name: 'Read', args: { file_path: join(trial.home, 'CHARTER.md') } } });
  const oriented = await take();
  assert.ok(oriented.request.toolResults.some(text => text.includes(input.day_brief)), 'public day scope must reach the real member through its charter');
  oriented.answer({ say: 'Ready for your next local task.' });
  const id = await waking;
  assert.equal(trial.sessions.get(id).state, 'idle', 'headless-off real Claude must settle idle');
  calls.push({ prompt: opening.request.prompt, completion: trial.exchange.completion });
  console.log('wake completed as idle');

  if (process.argv.includes('--omit-simulator-start')) {
    await once(trial.signal, 'abort');
    assert.equal(trial.signal.reason.failure_class, 'controller_handoff');
    assert.match(trial.signal.reason.message, /awaiting=simulator_start/);
    save(join(proofDir, 'handoff-timeout-proof.json'), {
      paid_calls: 0, error: trial.signal.reason.message, handoffs: trial.handoffs,
    });
    console.log('PASS: completed native turn without simulator start fails with its named measured limit');
  } else {
    const firstText = 'First local task: read input.txt.';
    await trial.transcript(id);
    trial.beginSimulation(id);
    trial.finishSimulation(id);
    let firstFinished = false;
    const first = trial.send(id, firstText).then(() => { firstFinished = true; });
    first.catch(() => {});
    const read = await take();
    assert.equal(read.request.prompt, firstText);
    read.answer({ tool: { id: 'proof-read', name: 'Read', args: { file_path: join(trial.home, 'input.txt') } } });
    const continuation = await take();
    assert.equal(firstFinished, false, 'intermediate tool-use response cannot finish the exchange');
    assert.ok(continuation.request.toolResults.some(text => text.includes('provider-mock local file')));
    continuation.answer({ say: 'First local task complete.' });
    await first;
    calls.push({ prompt: firstText, completion: trial.exchange.completion });
    console.log('first input and tool continuation completed');

    const secondText = 'Second local task: confirm the first task is done.';
    await trial.transcript(id);
    trial.beginSimulation(id);
    trial.finishSimulation(id);
    const second = trial.send(id, secondText);
    second.catch(() => {});
    const confirmation = await take();
    assert.equal(confirmation.request.prompt, secondText);
    confirmation.answer({ say: 'Confirmed. The first task is done.' });
    await second;
    calls.push({ prompt: secondText, completion: trial.exchange.completion });
    assert.equal(new Set(calls.map(call => call.completion.id)).size, calls.length, 'each input needs a distinct native completion');
    for (const text of [firstText, secondText])
      assert.equal(requests.filter(request => request.prompt === text && !request.toolResults.length).length, 1);
    console.log('second distinct input completed exactly once');

    const sleeping = trial.sleep(id);
    sleeping.catch(() => {});
    const closing = await take();
    closing.answer({ say: 'Ready to file the day letter.' });
    await trial.until(() => trial.sessions.get(id)?.state === 'idle');
    await trial.cli(['handoff', '--session', id, '--sleep', '-m', 'Unpaid real-binary proof: two local tasks completed.']);
    await sleeping;
    await trial.stop();
    assert.equal(pending.length, 0, 'no prompt suggestion or other extra provider call may remain');
    assert.ok(requests.every(request => request.model === config.member_model && request.tools.length), 'all provider calls must belong to the member; incidental title/suggestion calls are disabled');
    const receipt = {
      at: new Date().toISOString(), paid_calls: 0, provider_mock: true,
      claude_binary: config.claude_binary, attn_sha256: createHash('sha256').update(readFileSync(config.attn_binary)).digest('hex'),
      opening_state: 'idle', calls, handoffs: trial.handoffs, isolation: trial.isolation,
      provider_requests: requests.map(request => ({ model: request.model, prompt: request.prompt, tool_results: request.toolResults.length })),
      closure: 'real crew sleep and controller CLI handoff --sleep; model responses mocked',
    };
    save(join(proofDir, 'real-lifecycle-proof.json'), receipt);
    console.log('PASS: real Claude wake, two correlated inputs, tool continuation, sleep and cleanup; paid calls=0');
  }
} catch (error) {
  save(join(proofDir, 'real-lifecycle-failure.json'), {
    paid_calls: 0, error: error.message, instance: trial.instance, root: trial.root,
    phases: trial.handoffs, requests: requests.map(request => ({ model: request.model, prompt: request.prompt, tool_results: request.toolResults.length })),
  });
  throw error;
} finally {
  await trial.stop();
  await stub.close();
}
