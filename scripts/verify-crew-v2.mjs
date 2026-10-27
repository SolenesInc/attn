#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawn, spawnSync, execFileSync } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';

const { values } = parseArgs({ options: {
  attn: { type: 'string' }, 'previous-attn': { type: 'string' },
  'crew-home': { type: 'string' }, receipts: { type: 'string' },
} });
for (const key of ['attn', 'previous-attn', 'crew-home', 'receipts']) assert(values[key], `--${key} is required`);
const binary = path.resolve(values.attn);
const previous = path.resolve(values['previous-attn']);
const source = fs.realpathSync(values['crew-home']);
const receipts = path.resolve(values.receipts);
fs.mkdirSync(receipts, { recursive: true });
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-crew-v2-'));
const data = path.join(root, 'data');
const member = path.basename(source);
const home = path.join(data, 'crew', member);
fs.cpSync(source, home, { recursive: true, dereference: true });
const mock = fileURLToPath(new URL('../app/scripts/real-app-harness/mockAgent.mjs', import.meta.url));
const output = [`Copied ${source} to ${home}. Source is read-only.`, `Runtime: ${root}`];
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('ATTN_') && !key.startsWith('CODEX_') && !key.startsWith('CLAUDE')));
Object.assign(env, {
  ATTN_DATA_DIR: data, ATTN_TOOL_HOME: path.join(root, 'tools'),
  CODEX_HOME: path.join(root, 'tools', '.codex'),
  ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off', ATTN_HARNESS_SKILL_SYNC: 'off',
  ATTN_PTY_BACKEND: 'worker', ATTN_CLAUDE_EXECUTABLE: mock,
  ATTN_WS_PORT: '0', ATTN_CONVERSATION_KEEP_QUIET: '0s',
});
let activeBinary = previous;
let daemon;
let ws;
const events = [];
const waiters = [];
function onEvent(event) {
  events.push(event);
  for (const waiter of [...waiters]) {
    if (!waiter.match(event)) continue;
    waiters.splice(waiters.indexOf(waiter), 1);
    waiter.resolve(event);
  }
}
function waitEvent(match) {
  return new Promise((resolve) => waiters.push({ match, resolve }));
}
function cli(args, { json = false, code = 0, quiet = false } = {}) {
  const command = [...args, ...(json ? ['--json'] : [])];
  const result = spawnSync(activeBinary, command, { env, encoding: 'utf8' });
  if (!quiet) output.push(`$ attn ${command.join(' ')}`, result.stdout.trim(), result.stderr.trim());
  assert.equal(result.status, code, result.stderr || result.error?.message);
  return json ? JSON.parse(result.stdout) : result.stdout;
}
async function start(executable) {
  activeBinary = executable;
  const log = fs.openSync(path.join(receipts, executable === previous ? 'previous-stderr.log' : 'daemon-stderr.log'), 'a');
  daemon = spawn(executable, ['daemon'], { env: { ...env, ATTN_DAEMON_READY_FD: '3' }, stdio: ['ignore', log, log, 'pipe'] });
  fs.closeSync(log);
  await Promise.race([
    once(daemon.stdio[3], 'data'),
    once(daemon, 'exit').then(([code]) => { throw new Error(`daemon exited before ready: ${code}`); }),
  ]);
  output.push(`Daemon ready (${executable}).`);
}
async function stop() {
  if (ws) { ws.close(); ws = undefined; }
  if (!daemon || daemon.exitCode !== null) return;
  const exited = once(daemon, 'exit');
  daemon.kill('SIGTERM');
  await exited;
  daemon = undefined;
}
async function connect() {
  const listening = execFileSync('lsof', ['-nP', '-a', '-p', String(daemon.pid), '-iTCP', '-sTCP:LISTEN', '-Fn'], { encoding: 'utf8' });
  const address = listening.split('\n').find((line) => line.startsWith('n127.0.0.1:'))?.slice(1);
  assert(address, listening);
  const version = fs.readFileSync(fileURLToPath(new URL('../internal/protocol/protocol_version.go', import.meta.url)), 'utf8').match(/ProtocolVersion = "([^"]+)"/)[1];
  ws = new WebSocket(`ws://${address}/ws`);
  ws.addEventListener('message', (message) => onEvent(JSON.parse(message.data)));
  await once(ws, 'open');
  const initial = waitEvent((e) => e.event === 'initial_state');
  ws.send(JSON.stringify({ cmd: 'client_hello', client_kind: 'crew-v2-proof', version: `protocol-${version}`, client_token: fs.readFileSync(path.join(data, 'client-token'), 'utf8').trim(), capabilities: [] }));
  await initial;
}
async function request(command, event) {
  const request_id = crypto.randomUUID();
  const result = waitEvent((e) => e.event === event && e.request_id === request_id);
  ws.send(JSON.stringify({ ...command, request_id }));
  const reply = await result;
  assert(reply.success !== false, JSON.stringify(reply));
  return reply;
}
async function keepPass() {
  while (true) {
    const changed = waitEvent((e) => e.event === 'tasks_changed');
    const listed = await request({ cmd: 'task_list' }, 'task_list_result');
    const task = listed.tasks?.find((entry) => entry.kind === 'conversation_keep');
    if (task?.state === 'done') return;
    assert(!['failed', 'dead'].includes(task?.state), JSON.stringify(task));
    await changed;
  }
}
async function wake() {
  const session = cli(['crew', 'wake', member], { json: true }).session_id;
  await waitEvent((e) => e.event === 'session_state_changed' && e.session?.id === session && e.session?.state === 'idle');
  return session;
}
try {
  await start(previous);
  cli(['settings', 'set', 'crew_memory_enabled', 'false']);
  cli(['settings', 'set', 'crew.heartbeat_enabled', 'false']);
  cli(['settings', 'set', 'crew.autosleep_enabled', 'false']);
  await stop();
  fs.writeFileSync(path.join(home, '.attn-mock-agent.json'), JSON.stringify({ version: 1, agent: 'claude', resumable: true, turns: [
    { includes: 'You have been woken', actions: [{ type: 'reply', text: 'Crew v2 copied-data transcript receipt.', state: 'idle' }] },
  ] }));
  await start(binary);
  output.push('PASS: startup with crew_memory_enabled=false saved by the previous binary.');
  cli(['memory'], { code: 1 });
  cli(['handoff', '--day', 'removed', '-m', 'removed'], { code: 2 });
  await connect();
  cli(['crew', 'set', member, '--cwd', home, '--agent', 'claude']);
  cli(['settings', 'set', 'crew_v2_enabled', 'true']);
  const enabled = await wake();
  const filed = cli(['handoff', '-m', 'The copied-data verification filed this letter without --day.', '--session', enabled], { json: true });
  assert.equal(filed.outcome, 'sleep');
  await keepPass();
  await stop();
  await start(binary);
  await connect();
  cli(['settings', 'set', 'crew_v2_enabled', 'false']);
  const disabled = await wake();
  cli(['handoff', '-m', 'This letter was filed with v2 off.', '--session', disabled], { json: true });
  await keepPass();
  const listing = cli(['conversation', 'list'], { json: true });
  assert.equal(listing.count, 1);
  assert(listing.rows[0].crew_letters.some((letter) => letter.member === member && letter.filename === path.basename(filed.path) && letter.occurred_at));
  assert(!listing.rows.some((row) => row.session_ids.includes(disabled)));
  const sources = fs.readdirSync(path.join(root, 'tools', '.claude', 'projects'), { recursive: true }).filter((name) => name.endsWith(`${listing.rows[0].resume_id}.jsonl`));
  assert.equal(sources.length, 1);
  fs.unlinkSync(path.join(root, 'tools', '.claude', 'projects', sources[0]));
  const transcript = cli(['session', 'transcript', enabled]);
  assert(transcript.includes('Crew v2 copied-data transcript receipt.'));
  output.push('PASS: v2-on letter keeps its transcript after restart, another keep pass, v2 off and source deletion.');
  output.push('PASS: v2-off crew launches and files a letter without recording a transcript reference.');
} finally {
  if (daemon && daemon.exitCode === null) {
    const members = cli(['crew', 'list'], { json: true, quiet: true });
    for (const entry of members) if (entry.binding_session) cli(['handoff', '-m', 'Verification cleanup.', '--session', entry.binding_session], { quiet: true });
  }
  await stop();
  if (fs.existsSync(path.join(data, 'daemon.log'))) fs.copyFileSync(path.join(data, 'daemon.log'), path.join(receipts, 'daemon.log'));
  fs.writeFileSync(path.join(receipts, 'commands.txt'), `${output.filter(Boolean).join('\n')}\n`);
}
console.log(fs.readFileSync(path.join(receipts, 'commands.txt'), 'utf8'));
