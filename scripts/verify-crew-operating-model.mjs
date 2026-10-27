#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawn, spawnSync, execFileSync } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';

const { values } = parseArgs({ options: {
  attn: { type: 'string' }, 'crew-home': { type: 'string' }, receipts: { type: 'string' },
} });
for (const key of ['attn', 'crew-home', 'receipts']) assert(values[key], `--${key} is required`);
const evalToken = process.env.CLAUDE_CODE_OAUTH_TOKEN;
assert(evalToken, 'CLAUDE_CODE_OAUTH_TOKEN is required; inject an approved eval token');
const binary = path.resolve(values.attn);
const source = fs.realpathSync(values['crew-home']);
const receipts = path.resolve(values.receipts);
fs.mkdirSync(receipts, { recursive: true });
const root = fs.mkdtempSync('/tmp/attn-crew-model-');
const data = path.join(root, 'data');
const work = path.join(root, 'work');
const member = path.basename(source);
const home = path.join(data, 'crew', member);
const tools = path.join(root, 'tools');
const config = path.join(tools, '.claude');
for (const dir of [home, work, config, path.join(root, 'notebook')]) fs.mkdirSync(dir, { recursive: true });
fs.copyFileSync(path.join(source, 'CHARTER.md'), path.join(home, 'CHARTER.md'));
fs.copyFileSync(fileURLToPath(new URL('../internal/crew/priming.go', import.meta.url)), path.join(work, 'priming.go'));
fs.writeFileSync(path.join(work, 'AGENTS.md'), `This is a non-production verification copy. Work stays within ${root}. Read the current sources to discuss the work. No changes to the work are requested. Do not delegate, contact other sessions, or change production data.\n`);
fs.writeFileSync(path.join(config, '.claude.json'), JSON.stringify({
  hasCompletedOnboarding: true, theme: 'dark',
  projects: { [work]: { hasTrustDialogAccepted: true }, [fs.realpathSync(work)]: { hasTrustDialogAccepted: true } },
}));
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('ATTN_') && !key.startsWith('CODEX_') && !key.startsWith('CLAUDE')));
Object.assign(env, {
  ATTN_DATA_DIR: data, ATTN_TOOL_HOME: tools, CLAUDE_CONFIG_DIR: config,
  ATTN_HARNESS_DATA_DIR: root, ATTN_HARNESS_NOTEBOOK_ROOT: path.join(root, 'notebook'),
  CLAUDE_CODE_OAUTH_TOKEN: evalToken,
  CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION: 'false',
  ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off',
  ATTN_PTY_BACKEND: 'worker', ATTN_WS_PORT: '0',
});
const output = [`Copied ${source}/CHARTER.md to ${home}/CHARTER.md. Source is read-only.`, `Runtime: ${root}`];
console.log(output.join('\n'));
let daemon, ws, session;
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
  const existing = events.find(match);
  return existing ? Promise.resolve(existing) : new Promise((resolve) => waiters.push({ match, resolve }));
}
function cli(args, { json = false, input, quiet = false } = {}) {
  const command = [...args, ...(json ? ['--json'] : [])];
  const result = spawnSync(binary, command, { env, input, encoding: 'utf8' });
  if (!quiet) output.push(`$ attn ${command.join(' ')}`, result.stdout.trim(), result.stderr.trim());
  assert.equal(result.status, 0, result.stderr || result.error?.message);
  return json ? JSON.parse(result.stdout) : result.stdout;
}
async function prime(sessionID) {
  const socket = net.createConnection(path.join(data, 'attn.sock'));
  await once(socket, 'connect');
  const response = new Promise((resolve, reject) => {
    let body = '';
    socket.on('error', reject);
    socket.on('data', (chunk) => {
      body += chunk;
      if (body.includes('\n')) resolve(JSON.parse(body));
    });
  });
  socket.write(`${JSON.stringify({ cmd: 'crew_prime', session_id: sessionID })}\n`);
  const reply = await response;
  socket.end();
  assert(reply.ok, JSON.stringify(reply));
  return reply.crew_prime_result.guidance;
}
try {
  const log = fs.openSync(path.join(receipts, 'daemon-stderr.log'), 'a');
  daemon = spawn(binary, ['daemon'], { env: { ...env, ATTN_DAEMON_READY_FD: '3' }, stdio: ['ignore', log, log, 'pipe'] });
  fs.closeSync(log);
  await Promise.race([
    once(daemon.stdio[3], 'data'),
    once(daemon, 'exit').then(([code]) => { throw new Error(`daemon exited before ready: ${code}`); }),
  ]);
  const listening = execFileSync('lsof', ['-nP', '-a', '-p', String(daemon.pid), '-iTCP', '-sTCP:LISTEN', '-Fn'], { encoding: 'utf8' });
  const address = listening.split('\n').find((line) => line.startsWith('n127.0.0.1:'))?.slice(1);
  assert(address, listening);
  const version = fs.readFileSync(fileURLToPath(new URL('../internal/protocol/protocol_version.go', import.meta.url)), 'utf8').match(/ProtocolVersion = "([^"]+)"/)[1];
  ws = new WebSocket(`ws://${address}/ws`);
  ws.addEventListener('message', (message) => onEvent(JSON.parse(message.data)));
  await once(ws, 'open');
  ws.send(JSON.stringify({ cmd: 'client_hello', client_kind: 'crew-model-proof', version: `protocol-${version}`, client_token: fs.readFileSync(path.join(data, 'client-token'), 'utf8').trim(), capabilities: [] }));
  await waitEvent((event) => event.event === 'initial_state');
  cli(['settings', 'set', 'crew_v2_enabled', 'true']);
  cli(['settings', 'set', 'crew.heartbeat_enabled', 'false']);
  cli(['settings', 'set', 'crew.autosleep_enabled', 'false']);
  cli(['settings', 'set', 'auto_approve_enabled', 'true']);
  cli(['crew', 'set', member, '--cwd', work, '--awareness-dir', root, '--agent', 'claude', '--model', 'sonnet']);
  const parent = cli(['seed', 'plant', 'Current crew priming', '-m', `Discuss the current priming implementation in ${work}/priming.go. This is a read-only discussion of a verification copy.`], { json: true }).id;
  const seedID = cli(['seed', 'plant', 'Explain the current flag flow', '--part-of', parent, '-m', `Inspect ${work}/priming.go and explain whether the crew_v2_enabled flag reaches both Block and GardenSection. Read the seed notes and parent for the discussion context. The outcome is a precise greeting with the current state and a useful next step. Do not implement anything.`], { json: true }).id;
  cli(['seed', 'note', seedID, '-m', 'The last discussion suspected that GardenSection lost the flag. Check the current work before carrying that claim forward.']);
  const letterDir = path.join(home, 'handoffs');
  fs.mkdirSync(letterDir);
  const letter = `Dear successor,\n\nWe are discussing current crew priming on ${seedID}, under ${parent}. The work lives at ${work}/priming.go. I suspected the garden did not receive the v2 flag, but I had not checked the current file. Pick up the discussion from those pointers. No implementation is requested.\n`;
  fs.writeFileSync(path.join(letterDir, '2026-10-27T00-00Z-verification.md'), letter);
  fs.writeFileSync(path.join(receipts, 'letter.md'), letter);
  session = cli(['crew', 'wake', member], { json: true }).session_id;
  console.log(`Woke ${member}: ${session}`);
  fs.writeFileSync(path.join(receipts, 'runtime.json'), JSON.stringify({ root, session, member, seed: seedID, plot: parent }, null, 2));
  const priming = await prime(session);
  assert(priming.includes(letter.trim()), 'the named letter was not delivered in priming');
  fs.writeFileSync(path.join(receipts, 'delivered-priming.txt'), priming);
  await waitEvent((event) => event.event === 'session_state_changed' && event.session?.id === session && ['idle', 'waiting'].includes(event.session?.state));
  const transcript = cli(['session', 'transcript', session], { quiet: true });
  fs.writeFileSync(path.join(receipts, 'transcript.txt'), transcript);
  const raw = cli(['session', 'transcript', session, '--json'], { quiet: true });
  fs.writeFileSync(path.join(receipts, 'transcript.jsonl'), raw);
  const rows = raw.trim().split('\n').map((line) => JSON.parse(line));
  const greeting = rows.findIndex((event) => event.kind === 'assistant');
  assert(greeting >= 0, 'the wake produced no greeting');
  const before = rows.slice(0, greeting);
  const calls = before.filter((event) => event.kind === 'tool_call');
  for (const target of [seedID, parent]) assert(calls.some((event) => event.text.includes(`attn seed show ${target}`)), `${target} was not read before greeting`);
  assert(calls.some((event) => event.text.includes(path.join(work, 'priming.go'))), 'the work was not read before greeting');
  assert(before.some((event) => event.kind === 'tool_result' && event.text.includes('V2Enabled')), 'the work read returned no current source');
  fs.copyFileSync(path.join(home, 'CHARTER.md'), path.join(receipts, 'charter-after.md'));
  const proof = `# Real crew wake\n\nMember: ${member}. Session: ${session}.\n\nMeasured: the letter was delivered inline in priming. The member read the named seed ${seedID}, its parent ${parent}, and the current work before the first greeting (${rows[greeting].timestamp}). See delivered-priming.txt, letter.md, transcript.jsonl and transcript.txt.\n\n${calls.map((event) => `- ${event.timestamp}: ${event.tool_name} ${event.text}`).join('\n')}\n\nThe copied charter after the wake is charter-after.md.\n`;
  fs.writeFileSync(path.join(receipts, 'proof.md'), proof);
  output.push('PASS: letter delivered inline; named seed, parent plot and current work read before greeting.');
  console.log(transcript);
} finally {
  if (session && daemon?.exitCode === null) cli(['handoff', '--session', session, '-m', 'Verification finished. No further work is requested.'], { quiet: true });
  if (ws) ws.close();
  if (daemon?.exitCode === null) {
    const stopped = once(daemon, 'exit');
    daemon.kill('SIGTERM');
    await stopped;
  }
  if (fs.existsSync(path.join(data, 'daemon.log'))) fs.copyFileSync(path.join(data, 'daemon.log'), path.join(receipts, 'daemon.log'));
  fs.writeFileSync(path.join(receipts, 'commands.txt'), `${output.filter(Boolean).join('\n')}\n`);
}
