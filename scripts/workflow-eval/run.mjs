#!/usr/bin/env node
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { checkGraders, grade, graderLabel } from './lib/graders.mjs';
import { answerAsks, type } from './lib/standin.mjs';
import { cleanWorld, createWorld, preflight, readAccessToken, repository, startWorld } from './lib/world.mjs';

const { values: options } = parseArgs({
  options: {
    base: { type: 'string' },
    candidate: { type: 'string' },
    only: { type: 'string' },
    runs: { type: 'string', default: '3' },
    level: { type: 'string' },
    history: { type: 'string' },
  },
});
if (!options.base || !options.candidate) {
  console.error(`usage: node scripts/workflow-eval/run.mjs --base <git ref> --candidate <git ref> [--only <case,...>] [--runs <n>] [--level 1|2] [--history <file>]

Builds attn at each ref, then runs every case once per run on each build, each
on a fresh instance with a fresh tool home, and prints passes per grader, cost
and minutes per arm. Each run appends one line to the history file: by default
workflow-sessions.jsonl in the artifact folder of seed s-4z5bcq.`);
  process.exit(2);
}

const pollMs = 15_000;
const casesDir = path.join(repository, 'scripts/workflow-eval/cases');
const runs = Number(options.runs);
let current = null;
const arms = [];

function loadCases() {
  return fs.readdirSync(casesDir)
    .filter(name => !options.only || options.only.split(',').includes(name))
    .map(name => ({ name, dir: path.join(casesDir, name), ...JSON.parse(fs.readFileSync(path.join(casesDir, name, 'case.json'), 'utf8')) }))
    .filter(testCase => !options.level || String(testCase.level) === options.level);
}

function historyFile() {
  if (options.history) return path.resolve(options.history);
  const { artifact_dir } = JSON.parse(execFileSync('attn', ['seed', 'show', 's-4z5bcq', '--json'], { encoding: 'utf8' }));
  return path.join(artifact_dir, 'workflow-sessions.jsonl');
}

function buildArm(root, label, ref) {
  const dir = path.join(root, `arm-${label}`);
  execFileSync('git', ['-C', repository, 'worktree', 'add', '--quiet', '--detach', dir, ref]);
  arms.push({ label, ref, dir, bin: dir, commit: execFileSync('git', ['-C', dir, 'rev-parse', '--short', 'HEAD'], { encoding: 'utf8' }).trim() });
  const log = path.join(root, `build-${label}.log`);
  const built = spawnSync('make', ['build'], { cwd: dir, stdio: ['ignore', fs.openSync(log, 'w'), fs.openSync(log, 'a')] });
  if (built.status !== 0) throw new Error(`make build at ${ref} failed; see ${log}`);
  return arms.at(-1);
}

function removeArms() {
  for (const arm of arms) spawnSync('git', ['-C', repository, 'worktree', 'remove', '--force', arm.dir], { stdio: 'ignore' });
}

function cleanUpAndExit(signal) {
  console.error(`\n${signal}: cleaning up`);
  try {
    if (current) cleanWorld(current);
  } finally {
    removeArms();
    process.exit(130);
  }
}
process.on('SIGINT', () => cleanUpAndExit('SIGINT'));
process.on('SIGTERM', () => cleanUpAndExit('SIGTERM'));

function delegate(world, testCase) {
  const { opening } = testCase;
  const args = ['delegate', '--seed', world.seeds[opening.seed], '--role', testCase.role, '--cwd', world.repo, '--reuse-checkout', '--branch', 'main'];
  if (opening.authorization) args.push('--authorization', opening.authorization);
  const result = spawnSync(path.join(world.arm.bin, 'attn'), args, { env: world.env, encoding: 'utf8' });
  const session = `${result.stdout}${result.stderr}`.match(/session_id[=":\s]+([0-9a-f-]{36})/)?.[1];
  if (result.status !== 0 || !session) throw new Error(`attn delegate failed: ${`${result.stderr}${result.stdout}`.trim().split('\n').slice(-3).join(' ')}`);
  return session;
}

function sessions(world) {
  return world.attnJson(['session', 'list', '--all']).entries;
}

function spent(world) {
  return sessions(world).reduce((sum, entry) => sum + (entry.usage?.cost_usd ?? 0), 0);
}

function agentState(world, session) {
  return world.attnJson(['agent', 'list']).find(row => row.id === session)?.state ?? 'gone';
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

async function waitForStop(world, session, testCase, log) {
  const script = [...(testCase.standin ?? [])];
  const started = Date.now();
  let quiet = 0;
  for (;;) {
    await sleep(pollMs);
    const answered = answerAsks(world, session);
    for (const ask of answered) log(`answered ask ${ask.id} "${ask.subject}": ${JSON.stringify(ask.answers)}`);
    const state = agentState(world, session);
    quiet = answered.length || state === 'launching' || state === 'working' ? 0 : quiet + 1;
    if (quiet >= 2) {
      const next = script.shift();
      if (!next) return '';
      log(`typing: ${next.type}`);
      type(world, session, next.type);
      quiet = 0;
    }
    if (Date.now() - started > testCase.limits.minutes * 60_000) return `passed the ${testCase.limits.minutes}-minute limit`;
    if (spent(world) > testCase.limits.usd) return `passed the $${testCase.limits.usd} limit`;
  }
}

function transcriptOf(world, session) {
  return world.attn(['session', 'transcript', session, '--json']).split('\n')
    .filter(line => line.startsWith('{')).map(line => JSON.parse(line)).filter(event => event.kind);
}

function historyLine({ world, testCase, arm, run, session, transcript, started, closed, graded, limit }) {
  const entry = sessions(world).find(e => e.id === session);
  const calls = transcript.filter(event => event.kind === 'tool_call');
  const loaded = calls.findIndex(event => event.tool_name === 'Skill' && /"attn-workflow"/.test(event.text));
  const read = kind => [...new Set(calls.flatMap(event => [...event.text.matchAll(new RegExp(`attn-workflow/${kind}/([\\w-]+)\\.md`, 'g'))].map(m => m[1])))];
  return {
    session, host: os.hostname(), agent: entry?.agent, model: entry?.usage?.models?.[0]?.model, role: testCase.role,
    seed: world.seeds[testCase.opening.seed], started, closed, tool_calls: calls.length, cost_usd: entry?.usage?.cost_usd ?? 0,
    loaded_skill_at_call: loaded < 0 ? null : loaded + 1, references: read('references'), principles: read('principles'),
    eval: { case: testCase.name, arm: arm.label, ref: arm.ref, commit: arm.commit, run, instance: world.instance, limit: limit || null,
      graders: Object.fromEntries(graded.map(g => [g.label, g.pass])) },
  };
}

async function runCase(root, testCase, arm, run, history) {
  const prefix = `${testCase.name} ${arm.label} ${run}/${runs}`;
  const log = message => console.log(`[${prefix}] ${message}`);
  const world = createWorld({ root, arm, accessToken: readAccessToken(testCase.limits.minutes) });
  world.wsctl = path.join(root, 'wsctl');
  current = world;
  const started = new Date();
  try {
    startWorld(world, testCase);
    const session = delegate(world, testCase);
    log(`instance ${world.instance}, session ${session}`);
    const limit = await waitForStop(world, session, testCase, log);
    const closed = new Date();
    const transcript = transcriptOf(world, session);
    fs.writeFileSync(path.join(world.dir, 'transcript.jsonl'), transcript.map(event => JSON.stringify(event)).join('\n'));
    const graded = grade(testCase.graders, { world, testCase, transcript });
    const usd = spent(world);
    const line = historyLine({ world, testCase, arm, run, session, transcript, started: started.toISOString(), closed: closed.toISOString(), graded, limit });
    fs.appendFileSync(history, `${JSON.stringify(line)}\n`);
    fs.writeFileSync(path.join(world.dir, 'result.json'), `${JSON.stringify(line, null, 2)}\n`);
    log(`${limit ? `stopped: ${limit}, ` : ''}$${usd.toFixed(2)}, ${((closed - started) / 60_000).toFixed(1)} min, ${graded.filter(g => g.pass).length}/${graded.length} graders`);
    for (const g of graded.filter(g => !g.pass)) log(`  failed: ${g.label}${g.error ? ` (${g.error})` : ''}`);
    return { case: testCase.name, arm: arm.label, graded, usd, minutes: (closed - started) / 60_000 };
  } catch (error) {
    log(`error: ${error.message}`);
    return { case: testCase.name, arm: arm.label, graded: testCase.graders.map(g => ({ label: graderLabel(g), pass: false })), usd: 0, minutes: 0, error: error.message };
  } finally {
    cleanWorld(world);
    current = null;
  }
}

const median = values => [...values].sort((a, b) => a - b)[Math.floor((values.length - 1) / 2)] ?? 0;

function printTable(cases, results) {
  const labels = arms.map(arm => arm.label);
  for (const testCase of cases) {
    const mine = label => results.filter(r => r.case === testCase.name && r.arm === label);
    const rows = [[testCase.name, ...labels]];
    for (const grader of testCase.graders) {
      const label = graderLabel(grader);
      rows.push([`  ${label}`, ...labels.map(arm => {
        const passes = mine(arm).filter(r => r.graded.find(g => g.label === label)?.pass).length;
        return `${passes}/${mine(arm).length}${passes * 2 <= mine(arm).length ? ' ✗' : ''}`;
      })]);
    }
    rows.push(['  cost, median', ...labels.map(arm => `$${median(mine(arm).map(r => r.usd)).toFixed(2)}`)]);
    rows.push(['  minutes, median', ...labels.map(arm => median(mine(arm).map(r => r.minutes)).toFixed(1))]);
    const width = Math.max(...rows.map(row => row[0].length)) + 2;
    console.log(`\n${rows.map(([first, ...rest]) => first.padEnd(width) + rest.map(cell => cell.padEnd(10)).join('')).join('\n')}`);
  }
}

preflight();
const cases = loadCases();
if (!cases.length) throw new Error('no case matches --only and --level');
for (const testCase of cases) checkGraders(testCase.graders);
const history = historyFile();
fs.mkdirSync(path.dirname(history), { recursive: true });
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'workflow-eval-'));
execFileSync('go', ['build', '-o', path.join(root, 'wsctl'), './scripts/wsctl'], { cwd: repository });
const results = [];
try {
  for (const [label, ref] of [['base', options.base], ['candidate', options.candidate]]) {
    const arm = buildArm(root, label, ref);
    console.log(`arm ${label}: ${ref} at ${arm.commit}`);
  }
  for (const testCase of cases) {
    for (let run = 1; run <= runs; run++) {
      for (const arm of arms) results.push(await runCase(root, testCase, arm, run, history));
    }
  }
} finally {
  removeArms();
}
printTable(cases, results);
const total = results.reduce((sum, r) => sum + r.usd, 0);
console.log(`\n${results.length} runs, $${total.toFixed(2)} in all. History: ${history}. Logs: ${root}`);
