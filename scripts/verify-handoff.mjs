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
  attn: { type: 'string' }, 'crew-home': { type: 'string' }, receipts: { type: 'string' },
} });
for (const key of ['attn', 'crew-home', 'receipts']) assert(values[key], `--${key} is required`);
const binary = path.resolve(values.attn);
const source = fs.realpathSync(values['crew-home']);
const receipts = path.resolve(values.receipts);
fs.mkdirSync(receipts, { recursive: true });
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-handoff-'));
const data = path.join(root, 'data');
const member = path.basename(source);
const home = path.join(data, 'crew', member);
fs.cpSync(source, home, { recursive: true, dereference: true });
const mock = fileURLToPath(new URL('../app/scripts/real-app-harness/mockAgent.mjs', import.meta.url));
fs.writeFileSync(path.join(home, '.attn-mock-agent.json'), JSON.stringify({ version: 1, agent: 'claude', turns: [
  { includes: 'You have been woken', actions: [{ type: 'reply', text: 'HANDOFF_READY', state: 'idle' }] },
  { includes: 'Your predecessor closed', actions: [{ type: 'reply', text: 'NAP_READY', state: 'idle' }] },
] }));
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('ATTN_') && !key.startsWith('CODEX_') && !key.startsWith('CLAUDE')));
Object.assign(env, {
  ATTN_DATA_DIR: data, ATTN_TOOL_HOME: path.join(root, 'tools'),
  CODEX_HOME: path.join(root, 'tools', '.codex'), ATTN_WRAPPER_PATH: binary,
  ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off', ATTN_HARNESS_SKILL_SYNC: 'off',
  ATTN_PTY_BACKEND: 'worker', ATTN_CLAUDE_EXECUTABLE: mock, ATTN_WS_PORT: '0',
});
const output = [`Copied ${source} to ${home}. Source is read-only.`, `Runtime: ${root}`];
const log = fs.openSync(path.join(receipts, 'daemon-stderr.log'), 'a');
const daemon = spawn(binary, ['daemon'], { env: { ...env, ATTN_DAEMON_READY_FD: '3' }, stdio: ['ignore', log, log, 'pipe'] });
fs.closeSync(log);
let ws;
const events = [];
const waiters = [];
function waitEvent(match) {
  const seen = events.find(match);
  return seen ? Promise.resolve(seen) : new Promise(resolve => waiters.push({ match, resolve }));
}
function cli(args, { json = false, code = 0, quiet = false } = {}) {
  const command = [...args, ...(json ? ['--json'] : [])];
  const result = spawnSync(binary, command, { env, encoding: 'utf8' });
  if (!quiet) output.push(`$ attn ${command.join(' ')}`, result.stdout.trim(), result.stderr.trim());
  assert.equal(result.status, code, result.stderr || result.error?.message);
  return json ? JSON.parse(result.stdout) : result.stdout;
}
async function present() {
  ws.send(JSON.stringify({ cmd: 'set_client_presence', visible: true, dashboard_visible: false, idle_seconds: 0 }));
  const request_id = crypto.randomUUID();
  const synced = waitEvent(e => e.event === 'settings_updated' && e.request_id === request_id);
  ws.send(JSON.stringify({ cmd: 'set_setting', key: 'crew.heartbeat_enabled', value: 'false', request_id }));
  await synced;
  const status = cli(['activity'], { json: true });
  assert.equal(status.presence_tier, 'present');
}
async function ready(session) {
  await waitEvent(e => e.event === 'session_state_changed' && e.session?.id === session && e.session.state === 'idle');
}
function roster() {
  return cli(['crew', 'list'], { json: true }).find(entry => entry.id === member);
}
try {
  await Promise.race([
    once(daemon.stdio[3], 'data'),
    once(daemon, 'exit').then(([code]) => { throw new Error(`daemon exited before ready: ${code}`); }),
  ]);
  const listening = execFileSync('lsof', ['-nP', '-a', '-p', String(daemon.pid), '-iTCP', '-sTCP:LISTEN', '-Fn'], { encoding: 'utf8' });
  const address = listening.split('\n').find(line => line.startsWith('n127.0.0.1:'))?.slice(1);
  assert(address, listening);
  const version = fs.readFileSync(fileURLToPath(new URL('../internal/protocol/protocol_version.go', import.meta.url)), 'utf8').match(/ProtocolVersion = "([^"]+)"/)[1];
  ws = new WebSocket(`ws://${address}/ws`);
  ws.addEventListener('message', message => {
    const event = JSON.parse(message.data);
    events.push(event);
    for (const waiter of [...waiters]) {
      if (!waiter.match(event)) continue;
      waiters.splice(waiters.indexOf(waiter), 1);
      waiter.resolve(event);
    }
  });
  await once(ws, 'open');
  ws.send(JSON.stringify({ cmd: 'client_hello', client_kind: 'tauri-app', version: `protocol-${version}`, client_token: fs.readFileSync(path.join(data, 'client-token'), 'utf8').trim(), capabilities: [] }));
  await waitEvent(e => e.event === 'initial_state');
  cli(['settings', 'set', 'crew.autosleep_enabled', 'false']);
  cli(['crew', 'set', member, '--cwd', home, '--agent', 'claude']);
  for (const v2 of ['false', 'true']) {
    cli(['settings', 'set', 'crew_v2_enabled', v2]);
    await present();
    const first = cli(['crew', 'wake', member], { json: true }).session_id;
    await ready(first);
    const launches = events.filter(e => e.event === 'session_registered' && e.session?.crew_member === member).length;
    cli(['handoff', '-m', 'Plain handoff verification.', '--session', first]);
    assert(!roster().binding_session, 'plain handoff must leave the member asleep');
    assert.equal(events.filter(e => e.event === 'session_registered' && e.session?.crew_member === member).length, launches);
    output.push(`PASS: plain handoff with the user present sleeps (crew_v2_enabled=${v2}).`);
    const next = cli(['crew', 'wake', member], { json: true }).session_id;
    await ready(next);
    await present();
    const napped = cli(['handoff', '--nap', '-m', 'Explicit reload verification.', '--session', next], { json: true });
    assert.equal(napped.outcome, 'nap');
    assert(napped.session_id && napped.session_id !== next);
    await ready(napped.session_id);
    assert.equal(roster().binding_session, napped.session_id);
    output.push(`PASS: --nap starts a new session (crew_v2_enabled=${v2}).`);
    cli(['handoff', '-m', 'Verification cleanup.', '--session', napped.session_id]);
  }
  cli(['handoff', '--sleep', '-m', 'Removed flag'], { code: 2 });
  output.push('PASS: --sleep is rejected.');
} finally {
  if (ws) ws.close();
  if (daemon.exitCode === null) {
    const exited = once(daemon, 'exit');
    daemon.kill('SIGTERM');
    await exited;
  }
  if (fs.existsSync(path.join(data, 'daemon.log'))) fs.copyFileSync(path.join(data, 'daemon.log'), path.join(receipts, 'daemon.log'));
  fs.writeFileSync(path.join(receipts, 'commands.txt'), `${output.filter(Boolean).join('\n')}\n`);
}
console.log(fs.readFileSync(path.join(receipts, 'commands.txt'), 'utf8'));
