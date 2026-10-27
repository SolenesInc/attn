#!/usr/bin/env node
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { execFileSync } from 'node:child_process';
import { startStubModel } from '../../../app/scripts/real-app-harness/stubModel.mjs';
import { DEMO_ANTHROPIC_KEY } from '../../../app/scripts/demo-harness/agentHome.mjs';
import { runCase, gradeCase, recheckFidelity, regradeSaved, programmatic } from './eval.mjs';
import { json, save } from './trial.mjs';

for (const key of Object.keys(process.env)) {
  if (/^(CLAUDE_|ANTHROPIC_)/.test(key) || /TOKEN|API_KEY/.test(key)) delete process.env[key];
}
const config = json(resolve(process.argv[2]));
const root = mkdtempSync(join(config.private_root, 'print-proof-'));
const toolsRoot = mkdtempSync(join(tmpdir(), 'crew-eval-print-tools-'));
const fixture = join(toolsRoot, 'personal-config');
mkdirSync(join(fixture, 'commands'), { recursive: true });
writeFileSync(join(fixture, 'CLAUDE.md'), 'PERSONAL_CONTEXT_CANARY must never reach an evaluation controller.\n');
writeFileSync(join(fixture, 'commands', 'personal-command-canary.md'), 'PERSONAL_COMMAND_CANARY\n');
save(join(fixture, '.claude.json'), { customApiKeyResponses: { approved: [DEMO_ANTHROPIC_KEY.slice(-20)], rejected: [] } });
const policy = join(toolsRoot, 'localhost.sb');
writeFileSync(policy, '(version 1)\n(allow default)\n(deny network-outbound)\n(allow network-outbound (remote ip "localhost:*"))\n(allow network-outbound (remote unix-socket))\n');
const launchesPath = join(toolsRoot, 'launches.jsonl');
const controllerLaunchesPath = join(toolsRoot, 'controller-launches.jsonl');
const wrapper = verbose => {
  const path = join(toolsRoot, verbose ? 'claude-array' : 'claude-object');
  writeFileSync(path, `#!${process.execPath}\n` + `
const fs = require('node:fs');
const args = process.argv.slice(2);
const binary = ${JSON.stringify(config.claude_binary)};
if (args[0] === '-p') {
  process.env.CLAUDE_CONFIG_DIR ||= ${JSON.stringify(fixture)};
  fs.appendFileSync(${JSON.stringify(controllerLaunchesPath)}, JSON.stringify({ cwd: process.cwd(), config: process.env.CLAUDE_CONFIG_DIR,
    memory_disabled: process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY === '1', commands_disabled: args.includes('--disable-slash-commands'),
    chrome_disabled: args.includes('--no-chrome') }) + '\\n');
  process.execve('/usr/bin/sandbox-exec', ['sandbox-exec', '-f', ${JSON.stringify(policy)}, binary, ...${JSON.stringify(verbose ? ['--verbose'] : [])}, ...args], process.env);
}
const file = process.env.CLAUDE_CONFIG_DIR + '/.claude.json';
const state = JSON.parse(fs.readFileSync(file));
state.customApiKeyResponses = { approved: [${JSON.stringify(DEMO_ANTHROPIC_KEY.slice(-20))}], rejected: [] };
state.bypassPermissionsModeAccepted = true;
state.cachedGrowthBookFeatures = { tengu_virtual_pancake: true };
fs.writeFileSync(file, JSON.stringify(state));
const end = args.includes('--') ? args.indexOf('--') : args.length;
const index = args.findIndex((arg, i) => i < end && arg === '--permission-mode');
const terminal = Boolean(process.env.ATTN_TERMINAL_ID || process.env.ATTN_SESSION_ID);
const before = index >= 0 ? args[index + 1] : null;
if (terminal) {
  if (before !== 'auto') throw new Error('proof member must launch with --permission-mode auto before --');
  args[index + 1] = 'bypassPermissions';
}
fs.appendFileSync(${JSON.stringify(launchesPath)}, JSON.stringify({ terminal, separator: end < args.length, mode_before: before, mode_after: index >= 0 ? args[index + 1] : null }) + '\\n');
process.execve(binary, [binary, ...args], process.env);
`, { mode: 0o700 });
  return path;
};
const arrayBinary = wrapper(true), objectBinary = wrapper(false);
const trajectory = process.argv.includes('--trajectory');
const failDaySix = process.argv.includes('--fail-day-six');
const abortDiagnostic = process.argv.includes('--abort-trajectory-diagnostic');
const omitProbeField = process.argv.includes('--omit-probe-field');
const missingStale = process.argv.includes('--missing-stale-entry');
const staleMemory = process.argv.includes('--stale-memory') || missingStale;
const input = json(config.cases_path).find(row => trajectory ? row.id === config.trajectory_ids?.[process.argv.includes('--second-trajectory') ? 1 : 0] : staleMemory ? row.id === config.stale_probe_ids?.[0] : row.pilot && row.unit === 'use');
if (missingStale) delete input.frozen_memory;
if (!trajectory) input.days[0].task = [
  'Please complete this local provider-mock task in your member home.',
  'Write answer.json with the synthetic directory value supplied by the provider.',
  'Keep the task confined to this disposable instance and leave the historical letters unchanged.',
  'This multiline message reproduces an interactive paste; every line must reach the member unchanged.',
  'The provider will supply the response and closure steps, so no external model service is needed.',
  'When the file is ready, report that it exists and wait for the controller to close the day.',
].join('\n\n');
const abort = new AbortController();
process.once('SIGINT', () => abort.abort(new Error('unpaid model-call proof interrupted')));
const ctx = { flow: root, variant: staleMemory ? 'v1' : 'baseline', model: config.member_model, rep: 0, attempt: Date.now(),
  signal: AbortSignal.any([abort.signal, AbortSignal.timeout(config.case_timeout_s.use * 1000)]),
  calls: [], state: { ...config, claude_binary: arrayBinary, local_provider_only: true } };
const artifact = join(root, ctx.variant, 'out', `${input.id}_rep0_${ctx.attempt}`);
const requests = [];
let memberCalls = 0, simulatorCalls = 0;
const refuseSleep = process.argv.includes('--refuse-sleep') || process.argv.includes('--never-sleep');
const memberCallCount = trajectory ? input.days.length * 5 : refuseSleep ? 6 : 5;
let fidelityMode = 'faithful', judgeYes = true;
const fidelityAbort = new AbortController();
const stub = await startStubModel(request => {
  requests.push({ model: request.model, tools: request.tools, body: request.body });
  save(join(root, `provider-request-${requests.length}.json`), request.body);
  console.log(`mock request ${requests.length}: ${request.model}`);
  try {
    if (request.model === config.member_model) {
      memberCalls++;
      if (staleMemory && memberCalls === 1) {
        assert.ok(request.systemPrompt.includes('## Your memory'));
        assert.equal(request.systemPrompt.includes(input.stale_entry), !missingStale);
      }
      const home = join(json(join(artifact, 'runtime.json')).data_dir, 'crew', 'member');
      if (trajectory) {
        const number = Math.floor((memberCalls - 1) / 5) + 1;
        const phase = (memberCalls - 1) % 5 + 1;
        const day = input.days[number - 1];
        if (phase === 1) return { tool: { id: `read-charter-${number}`, name: 'Read', args: { file_path: join(home, 'CHARTER.md') } } };
        if (phase === 2) return { say: 'Ready for the next local day.' };
        if (phase === 3) {
          assert.ok(JSON.stringify(request.body.messages).includes(JSON.stringify(day.scripted_message).slice(1, -1)));
          if (failDaySix && number === 6) throw new Error('synthetic day-six failure');
          const value = Object.fromEntries(day.required_fields.map(field => [field, `mock day ${number} ${field}`]));
          value[day.required_fields.at(-1)] = null;
          if (omitProbeField && number === 5) delete value[day.required_fields[0]];
          const content = JSON.stringify(value) + '\n';
          if (number === 6) return { tool: { id: 'write-final-and-edit-old', name: 'Bash', args: {
            command: `node --input-type=module <<'NATIVE_PROOF'\nimport {writeFileSync} from 'node:fs';\nwriteFileSync(${JSON.stringify(join(home, day.output))}, ${JSON.stringify(content)});\nwriteFileSync(${JSON.stringify(join(home, 'day-5.json'))}, '{"later_edit":true}');\nNATIVE_PROOF`,
            description: 'Write final task and deliberately edit an older task file',
          } } };
          return { tool: { id: `write-day-${number}`, name: 'Write', args: { file_path: join(home, day.output), content } } };
        }
        if (phase === 4) return { say: `Day ${number} file is ready.` };
        return { tool: { id: `file-day-${number}`, name: 'Bash', args: {
          command: `"$ATTN_WRAPPER_PATH" handoff --sleep -m "Unpaid trajectory day ${number} completed."`, description: 'File the day and sleep',
        } } };
      }
      if (memberCalls === 1) return { tool: { id: 'read-charter', name: 'Read', args: { file_path: join(home, 'CHARTER.md') } } };
      if (memberCalls === 2) return { say: 'Ready for your local task.' };
      if (memberCalls === 3) {
        const content = request.body.messages.findLast(message => message.role === 'user').content;
        const text = typeof content === 'string' ? content : content.filter(part => part.type === 'text').map(part => part.text).join('');
        assert.ok(text.includes(input.days[0].task));
        return { tool: { id: 'write-answer', name: 'Write', args: { file_path: join(home, 'answer.json'), content: '{"directory":"provider-mock-answer"}\n' } } };
      }
      if (memberCalls === 4) return { say: 'The answer is in answer.json.' };
      if (refuseSleep && memberCalls === 5) return { say: 'An inbox note is not direct confirmation. Reply sleep and I will file.' };
      if (process.argv.includes('--never-sleep') && memberCalls === 6) return { say: 'I am still not closing this day.' };
      if (memberCalls === memberCallCount) return { tool: { id: 'file-letter', name: 'Bash', args: {
        command: staleMemory
          ? `"$ATTN_WRAPPER_PATH" handoff --sleep --day "Mock task completed." -m "Unpaid provider mock: local task completed."; printf '%s\\n' '0-1 Fixture and mock task completed.' | "$ATTN_WRAPPER_PATH" memory summarize -`
          : '"$ATTN_WRAPPER_PATH" handoff --sleep -m "Unpaid provider mock: local task completed."',
        description: 'File the proof letter and sleep',
      } } };
      throw new Error(`unexpected member request ${memberCalls}`);
    }
    assert.ok(request.tools.includes('StructuredOutput'));
    const prompt = JSON.parse(request.prompt);
    if (request.model === config.simulator_model) {
      simulatorCalls++;
      return { tool: { id: `simulator-${simulatorCalls}`, name: 'StructuredOutput', args: {
        done: simulatorCalls > 1, message: simulatorCalls === 1 ? input.days[0].task : '',
      } } };
    }
    assert.equal(request.model, config.judge_model);
    if (prompt.fact_ledger && prompt.days) {
      if (abortDiagnostic) abort.abort(Object.assign(new Error('synthetic diagnostic deadline'), { failure_class: 'timeout' }));
      return { tool: { id: `carry-${requests.length}`, name: 'StructuredOutput', args: {
      days: prompt.days.map(day => ({ day: day.day, retained: prompt.fact_ledger.filter(fact => fact.plant_day <= day.day).map(fact => fact.id), omitted: [], unsupported: [], memory_entries: 'not applicable: letters-only' })),
    } } };
    }
    if (prompt.turns) {
      assert.equal(prompt.turns.length, 1);
      assert.equal(prompt.turns[0].message, input.days[0].task);
      assert.equal(prompt.day_brief, input.day_brief);
      if (fidelityMode === 'abort') fidelityAbort.abort(Object.assign(new Error('synthetic shared case deadline during post-case fidelity'), { failure_class: 'timeout' }));
      return { tool: { id: `fidelity-${requests.length}`, name: 'StructuredOutput', args: {
        violations: fidelityMode === 'faithful' ? [] : [{ turn: fidelityMode === 'unfaithful' ? 0 : -1,
          assertion: 'Synthetic unsupported assertion.', reason: 'Synthetic fidelity verdict for protocol verification.' }],
      } } };
    }
    if (prompt.A && prompt.B) {
      assert.deepEqual(Object.keys(prompt.A), ['files']);
      assert.deepEqual(Object.keys(prompt.B), ['files']);
      return { tool: { id: `pairwise-${requests.length}`, name: 'StructuredOutput', args: { winner: 'tie', reason: 'Synthetic identical persisted outcomes.' } } };
    }
    if (!trajectory) assert.equal(prompt.candidate.files['answer.json'], '{"directory":"provider-mock-answer"}\n');
    return { tool: { id: `judge-${requests.length}`, name: 'StructuredOutput', args: {
      verdicts: prompt.claims.map(claim => ({ id: claim.id, yes: judgeYes, reason: 'Synthetic provider response for protocol verification.' })),
    } } };
  } catch (error) {
    abort.abort(error);
    return { say: 'The local proof failed.' };
  }
});
Object.assign(process.env, {
  ANTHROPIC_BASE_URL: stub.origin, ANTHROPIC_API_KEY: DEMO_ANTHROPIC_KEY,
  CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_AUTOUPDATER: '1',
  DISABLE_TELEMETRY: '1', DISABLE_ERROR_REPORTING: '1',
  CLAUDE_CODE_GB_DISK_CACHE_WHEN_TELEMETRY_OFF: '1',
});
try {
  console.log(`unpaid full model-call proof pid=${process.pid}; provider=${stub.origin}; root=${root}`);
  const run = await runCase(input, ctx);
  assert.equal(run.output.letters.length, trajectory ? 6 : 1);
  assert.equal(run.sleep_confirmation_needed, refuseSleep);
  const sleepConfirmations = json(join(root, run.artifact_dir, 'sleep-confirmations.json'));
  assert.equal(sleepConfirmations.length, refuseSleep ? 1 : 0);
  if (refuseSleep) assert.equal(sleepConfirmations[0].message, 'sleep');
  assert.ok(run.transcript.some(turn => turn.role === 'tool_call' && turn.name === 'Write'));
  const memberLaunches = readFileSync(launchesPath, 'utf8').trim().split('\n').map(line => JSON.parse(line)).filter(launch => launch.terminal);
  assert.ok(memberLaunches.length);
  assert.ok(memberLaunches.every(launch => launch.mode_before === 'auto' && launch.mode_after === 'bypassPermissions'));
  const nativeFiles = readdirSync(join(root, run.artifact_dir)).filter(file => file.endsWith('.jsonl.json'));
  assert.ok(nativeFiles.length);
  for (const file of nativeFiles) {
    const records = json(join(root, run.artifact_dir, file));
    assert.equal(records.find(record => record.type === 'user').permissionMode, 'bypassPermissions');
    assert.ok(records.some(record => record.type === 'user' && typeof record.message?.content === 'string' &&
      record.message.content.includes('<pasted_content id=') && input.days.some(day => record.message.content.includes(day.task))));
  }
  if (trajectory) {
    const delivered = json(join(root, run.artifact_dir, 'scripted-turns.json'));
    assert.deepEqual(delivered.map(turn => turn.message), input.days.map(day => day.scripted_message));
    assert.equal(run.output.day_outputs.length, 6);
    assert.deepEqual(json(join(root, run.artifact_dir, 'day-snapshots.json')), run.output.day_outputs);
    const five = JSON.parse(run.output.day_outputs[4].files['day-5.json']);
    assert.equal(five[input.days[4].required_fields.at(-1)], null);
    assert.equal(run.output.files['day-5.json'], '{"later_edit":true}');
    assert.notEqual(run.output.day_outputs[4].files['day-5.json'], run.output.files['day-5.json']);
    const grade = await gradeCase(input, run, null, ctx);
    assert.equal(grade.grade.required_probe_fields, omitProbeField ? 0 : 1);
    assert.equal(grade.grade.pass, omitProbeField ? 0 : 1);
    assert.equal(grade.meta.probe_omissions.length, omitProbeField ? 1 : 0);
    assert.equal(grade.meta.diagnostics.length, abortDiagnostic ? 0 : 2);
    assert.equal(grade.meta.diagnostics_status, abortDiagnostic ? 'unverified' : 'verified');
    if (abortDiagnostic) {
      assert.match(grade.meta.diagnostics_error, /synthetic diagnostic deadline/);
      assert.equal(grade.meta.calls_spend_incomplete, true);
    }
    assert.ok(grade.meta.diagnostics.every(vote => vote.days.length === 6));
    assert.equal(grade.meta.simulator_fidelity.mode, 'fixed_messages');
    assert.equal(simulatorCalls, 0);
    assert.equal(memberCalls, memberCallCount);
    save(join(root, 'trajectory-proof.json'), { paid_calls: 0, run, grade, delivered, requests });
    console.log(`PASS: six native days, exact scripts, stable day-five snapshot and omission grading; ${join(root, 'trajectory-proof.json')}`);
  } else if (staleMemory) {
    assert.equal(run.engagement.wake_view, true);
    const proof = json(join(root, run.artifact_dir, 'wake-view.json'));
    assert.equal(proof.length, 1);
    assert.ok(proof[0].shape_hash);
    const grade = await gradeCase(input, run, JSON.stringify(run.output), ctx);
    assert.equal(grade.meta.judge_passes.length, 2);
    assert.equal(grade.meta.pairwise.length, 2);
    assert.ok(grade.meta.pairwise.every(vote => vote.winner === 'tie' && ['A', 'B'].includes(vote.memory_candidate)));
    assert.equal(grade.meta.simulator_fidelity.status, 'faithful');
    save(join(root, 'stale-wake-proof.json'), { paid_calls: 0, run, proof, grade, requests });
    console.log(`PASS: CLI-written stale entry reached the native wake request and actual localhost provider; ${join(root, 'stale-wake-proof.json')}`);
  } else {
  const grade = await gradeCase(input, run, null, ctx);
  assert.equal(memberCalls, memberCallCount);
  assert.equal(simulatorCalls, 2);
  assert.equal(grade.meta.judge_passes.length, 2);
  assert.equal(grade.meta.simulator_fidelity.status, 'faithful');
  assert.ok(grade.meta.simulator_fidelity_cost_usd > 0);
  const arrayCalls = [...ctx.calls];
  assert.equal(arrayCalls.length, 5);
  for (const call of arrayCalls) {
    assert.ok(Array.isArray(call.events), 'verbose native print must produce an event array');
    assert.equal(call.response.type, 'result');
    assert.equal(call.response.subtype, 'success');
    assert.ok(call.response.total_cost_usd > 0);
    const init = call.events.find(event => event.type === 'system' && event.subtype === 'init');
    assert.equal(init.skills?.length ?? 0, 0);
    assert.ok(!(init.slash_commands ?? []).includes('personal-command-canary'));
  }
  const simulatorReceipts = arrayCalls.filter(call => call.purpose === 'simulator');
  const objectCtx = { ...ctx, calls: [...simulatorReceipts], state: { ...ctx.state, claude_binary: objectBinary } };
  await gradeCase(input, run, null, objectCtx);
  const objectCalls = objectCtx.calls.slice(simulatorReceipts.length);
  assert.equal(objectCalls.length, 3);
  for (const call of objectCalls) {
    assert.equal(call.events, undefined);
    assert.equal(call.response.type, 'result');
    assert.ok(call.response.total_cost_usd > 0);
  }
  fidelityMode = 'unfaithful';
  judgeYes = false;
  const flaggedCtx = { ...objectCtx, calls: [...simulatorReceipts] };
  const flaggedGrade = await gradeCase(input, run, null, flaggedCtx);
  assert.equal(flaggedGrade.grade.pass, 0);
  assert.equal(flaggedGrade.meta.simulator_fidelity.status, 'unfaithful');
  fidelityMode = 'unknown-turn';
  judgeYes = true;
  const uncheckedCtx = { ...objectCtx, calls: [...simulatorReceipts] };
  const uncheckedGrade = await gradeCase(input, run, null, uncheckedCtx);
  assert.equal(uncheckedGrade.grade.pass, 1);
  assert.equal(uncheckedGrade.meta.simulator_fidelity.status, 'unverified');
  fidelityMode = 'abort';
  const abortedCtx = { ...objectCtx, calls: [...simulatorReceipts], signal: AbortSignal.any([ctx.signal, fidelityAbort.signal]) };
  const abortedGrade = await gradeCase(input, run, null, abortedCtx);
  assert.equal(abortedGrade.grade.pass, 1);
  assert.equal(abortedGrade.meta.simulator_fidelity.status, 'unverified');
  assert.match(abortedGrade.meta.simulator_fidelity.error, /shared case deadline/);
  assert.equal(json(join(root, run.artifact_dir, 'grading.json')).simulator_fidelity.status, 'unverified');
  fidelityMode = 'faithful';
  save(join(root, run.artifact_dir, 'model-calls.json'), flaggedCtx.calls);
  const beforeReplay = requests.length;
  const replayRow = { prompt_id: input.id, rep: 0, artifact_dir: run.artifact_dir,
    grade: flaggedGrade.grade, evaluation: flaggedGrade.meta, wall_clock_s: 1, simulator_unfaithful: true };
  const replayCtx = { ...objectCtx, attempt: Date.now() };
  const replayed = await recheckFidelity(input, replayRow, replayCtx);
  assert.equal(requests.length, beforeReplay + 1);
  assert.equal(memberCalls, memberCallCount);
  assert.equal(simulatorCalls, 2);
  assert.deepEqual(replayed.grade, flaggedGrade.grade);
  assert.equal(replayed.simulator_unfaithful, false);
  assert.equal(replayed.evaluation.simulator_fidelity.status, 'faithful');
  assert.ok(replayed.evaluation.calls_cost_usd > flaggedGrade.meta.calls_cost_usd);
  const replayReceipt = json(join(root, run.artifact_dir, `fidelity-recheck-${replayCtx.attempt}.json`));
  assert.deepEqual(replayReceipt.before, JSON.parse(JSON.stringify(replayRow)));
  assert.equal(replayReceipt.calls.length, 1);
  assert.equal(replayReceipt.calls[0].purpose, 'simulator_fidelity');
  const beforeRegrade = requests.length;
  const regradeCtx = { ...objectCtx, attempt: Date.now() };
  const regraded = await regradeSaved(input, replayed, regradeCtx);
  assert.equal(requests.length, beforeRegrade + 2);
  assert.equal(memberCalls, memberCallCount);
  assert.equal(simulatorCalls, 2);
  assert.equal(regraded.grade.pass, 1);
  assert.deepEqual(regraded.evaluation.simulator_fidelity, replayed.evaluation.simulator_fidelity);
  assert.equal(regradeCtx.calls.length, 2);
  assert.ok(regradeCtx.calls.every(call => call.purpose === 'judge'));
  const regradeReceipt = json(join(root, run.artifact_dir, `regrade-${regradeCtx.attempt}.json`));
  assert.deepEqual(regradeReceipt.before.grade, replayed.grade);
  assert.equal(regradeReceipt.after.grade.pass, 1);
  const summaryFlow = join(root, 'summary-proof');
  mkdirSync(join(summaryFlow, 'baseline'), { recursive: true });
  writeFileSync(join(summaryFlow, 'baseline', 'results.jsonl'), [grade, flaggedGrade, uncheckedGrade].map((value, index) => JSON.stringify({
    prompt_id: `synthetic-fidelity-${index}`, tags: ['use'], status: 'ok', grade: value.grade, evaluation: value.meta,
    member_cost_usd: run.member_cost_usd, wall_clock_s: 1,
  })).join('\n') + '\n');
  execFileSync(process.execPath, [resolve('scripts/evals/crew-memory/summarize.mjs'), summaryFlow]);
  const summary = json(join(summaryFlow, 'measurements.json')).find(item => item.unit === 'use');
  assert.equal(summary.cases, 3);
  assert.equal(summary.score, 2 / 3);
  assert.equal(summary.simulator_unfaithful_reps, 1);
  assert.equal(summary.simulator_unchecked_reps, 1);
  assert.equal(summary.simulator_faithful.cases, 1);
  assert.equal(summary.simulator_faithful.score, 1);
  assert.equal(summary.spend_incomplete, true);
  assert.ok(summary.known_spend_usd > 0);
  const retiredCost = run.member_cost_usd + flaggedGrade.meta.calls_cost_usd;
  writeFileSync(join(summaryFlow, 'baseline', 'retired.jsonl'), JSON.stringify({
    prompt_id: 'synthetic-retired', tags: ['use'], status: 'ok', grade: flaggedGrade.grade,
    member_cost_usd: run.member_cost_usd, evaluation: flaggedGrade.meta, wall_clock_s: 1,
  }) + '\n');
  execFileSync(process.execPath, [resolve('scripts/evals/crew-memory/summarize.mjs'), summaryFlow]);
  const withRetired = json(join(summaryFlow, 'measurements.json')).find(item => item.unit === 'use');
  assert.equal(withRetired.cases, summary.cases);
  assert.equal(withRetired.score, summary.score);
  assert.equal(withRetired.retired_reps, 1);
  assert.ok(Math.abs(withRetired.known_spend_usd - summary.known_spend_usd - retiredCost) < 1e-9);
  save(join(summaryFlow, 'spend-ledger.json'), { known_spend_usd: withRetired.known_spend_usd,
    known_flow_spend_at_checkpoint_usd: withRetired.known_spend_usd, hard_total_cap_usd: 80,
    billing_basis: 'Synthetic provider usage for report verification; paid calls=0' });
  execFileSync(process.execPath, [resolve('scripts/evals/crew-memory/summarize.mjs'), summaryFlow]);
  const ledgerSummary = json(join(summaryFlow, 'spend-summary.json'));
  assert.equal(ledgerSummary.known_spend_since_checkpoint_usd, 0);
  assert.ok(Math.abs(ledgerSummary.current_eval_known_usd - withRetired.known_spend_usd) < 1e-8);
  const controllerLaunches = readFileSync(controllerLaunchesPath, 'utf8').trim().split('\n').map(line => JSON.parse(line));
  assert.equal(controllerLaunches.length, requests.filter(request => request.model !== config.member_model).length);
  assert.equal(new Set(controllerLaunches.map(launch => launch.cwd)).size, controllerLaunches.length);
  for (const launch of controllerLaunches) {
    assert.ok(launch.cwd.startsWith(join(config.private_root, 'controller-')));
    assert.equal(launch.config, join(launch.cwd, '.claude'));
    assert.ok(launch.memory_disabled && launch.commands_disabled && launch.chrome_disabled);
    assert.equal(existsSync(launch.cwd), false, 'each completed print call must clean its configuration');
  }
  const controllerRequests = requests.filter(request => request.model !== config.member_model);
  assert.ok(controllerRequests.every(request => !JSON.stringify(request.body).includes('PERSONAL_CONTEXT_CANARY')));
  assert.ok(controllerRequests.every(request => !JSON.stringify(request.body).includes('PERSONAL_COMMAND_CANARY')));
  const receipt = { at: new Date().toISOString(), paid_calls: 0, provider_mock: true,
    claude_binary: config.claude_binary, real_member_calls: memberCalls, real_simulator_calls: simulatorCalls, memberLaunches,
    array_calls: arrayCalls, object_calls: objectCalls, controllerLaunches,
    fidelity_flagged: flaggedGrade.meta, fidelity_unverified: uncheckedGrade.meta, fidelity_aborted: abortedGrade.meta,
    fidelity_replay: replayReceipt, judge_only_replay: regradeReceipt,
    sleep_confirmation_needed: run.sleep_confirmation_needed, sleep_confirmations: sleepConfirmations,
    summary, summary_with_retired: withRetired, cumulative_spend_summary: ledgerSummary,
    personal_context_canary_absent: true, personal_commands_absent: true,
    member_permission_mode: 'bypassPermissions for deterministic mock tools; localhost/filesystem sandbox remains active',
    closure: 'real member Bash tool files a synthetic letter with attn handoff --sleep',
    requests, output: run.output, trace_limitations: run.trace_limitations,
    limitation: 'Provider answers, usage and grades are synthetic; no model quality or paid outcome is measured.' };
  save(join(root, 'model-calls-proof.json'), receipt);
  console.log(`PASS: real member, simulator and judge paths, native array/object results, receipts and isolated context; ${join(root, 'model-calls-proof.json')}`);
  }
} catch (error) {
  save(join(root, 'model-calls-failure.json'), { error: error.message, failure_class: error.failure_class, calls: ctx.calls, requests,
    launches: existsSync(launchesPath) ? readFileSync(launchesPath, 'utf8') : '' });
  if (failDaySix && /synthetic day-six failure/.test(error.message)) {
    const snapshots = json(join(artifact, 'day-snapshots.json'));
    assert.equal(snapshots.length, 5);
    assert.ok(snapshots[4].files['day-5.json']);
    assert.equal(json(join(artifact, 'sessions.json')).length, 5);
    assert.ok(!requests.some(request => request.model === config.judge_model));
    console.log(`PASS: first five daily snapshots and sessions survive day-six failure; ${join(root, 'model-calls-failure.json')}`);
  } else if (missingStale && error.failure_class === 'harness' && /stale entry missing from native wake/.test(error.message)) {
    assert.equal(simulatorCalls, 0);
    console.log(`PASS: missing stale wake evidence fails before task or grading; ${join(root, 'model-calls-failure.json')}`);
  } else if (process.argv.includes('--never-sleep') && error.failure_class === 'controller_handoff' && /awaiting=crew_sleep/.test(error.message)) {
    assert.equal(memberCalls, memberCallCount);
    assert.equal(error.sleep_confirmation_needed, true);
    console.log(`PASS: refusal after direct sleep confirmation fails with the measured crew_sleep watchdog; ${join(root, 'model-calls-failure.json')}`);
  } else {
    console.error(error.stack);
    process.exitCode = 1;
  }
} finally {
  await stub.close();
  rmSync(toolsRoot, { recursive: true, force: true });
}
