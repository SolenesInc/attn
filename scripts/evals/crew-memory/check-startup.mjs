#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createHash } from 'node:crypto';
import { readFileSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { Trial, json, save } from './trial.mjs';

for (const key of Object.keys(process.env)) {
  if (/^(CLAUDE_|ANTHROPIC_)/.test(key) || /TOKEN|API_KEY/.test(key)) delete process.env[key];
}
const config = json(resolve(process.argv[2]));
const signal = AbortSignal.timeout(config.case_timeout_s.use * 1000);
const trial = new Trial(config, signal);
const started = performance.now();
const plain = text => text.replace(/\x1b\][\s\S]*?(?:\x07|\x1b\\)/g, '')
  .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, ' ').replace(/[\x00-\x08\x0b-\x1f]/g, '');
let output = '', terminal, session, initialConfig, authentication;
try {
  await trial.start({ charter: '# Startup check\nHelp with local tasks.\n', letters: [] });
  initialConfig = existsSync(join(trial.env.CLAUDE_CONFIG_DIR, '.claude.json'))
    ? json(join(trial.env.CLAUDE_CONFIG_DIR, '.claude.json')) : {};
  const auth = spawn('/usr/bin/sandbox-exec', ['-f', trial.policy,
    trial.env.ATTN_CLAUDE_EXECUTABLE, 'auth', 'status'], { cwd: trial.home, env: trial.env,
    signal, stdio: ['ignore', 'pipe', 'inherit'] });
  let authOutput = '';
  auth.stdout.on('data', bytes => { authOutput += bytes; });
  await once(auth, 'exit');
  authentication = JSON.parse(authOutput);
  assert.equal(authentication.loggedIn, false, 'startup check must have no authentication');

  trial.ws.addEventListener('message', event => {
    const frame = JSON.parse(event.data);
    if (frame.id !== terminal) return;
    if (frame.event === 'pty_output' && frame.data) output += Buffer.from(frame.data, 'base64').toString();
    if (frame.screen_snapshot) output += Buffer.from(frame.screen_snapshot, 'base64').toString();
    trial.emit('changed');
  });
  trial.on('changed', () => {
    if (terminal || !session || !trial.bindings.has(session)) return;
    terminal = trial.bindings.get(session);
    trial.ws.send(JSON.stringify({ cmd: 'attach_session', id: terminal }));
    trial.ws.send(JSON.stringify({ cmd: 'get_screen_snapshot', id: terminal }));
  });
  session = JSON.parse(await trial.cli(['crew', 'wake', 'member', '--json'])).session_id;
  trial.emit('changed');
  await trial.until(() => /Choose\s+the\s+text\s+style|trust this folder|Not\s+logged\s+in|run\s+\/login/i.test(plain(output)));
  const screen = plain(output);
  assert.doesNotMatch(screen, /Choose\s+the\s+text\s+style|trust this folder/i, 'member is blocked on first-launch onboarding');
  assert.match(screen, /Not\s+logged\s+in|run\s+\/login/i, 'member must reach its unauthenticated prompt');
  assert.equal(readFileSync(trial.attnBinary).equals(readFileSync(config.attn_binary)), true, 'trial attn bytes must match the pinned binary');
  const daemonLog = readFileSync(join(trial.data, 'daemon.log'), 'utf8');
  const spawns = [...daemonLog.matchAll(/worker backend spawn: .* binary=(.*?) socket=/g)].map(match => match[1]);
  assert.ok(spawns.length > 0 && spawns.every(path => path === trial.attnBinary), 'every worker must use the pinned trial attn');
  assert.doesNotMatch(daemonLog, /worker binary missing|worker binary re-resolved|worker swapped in place/);
  save(join(config.private_root, 'startup-check.json'), {
    at: new Date().toISOString(), model_calls: 0, authentication, initial_config: initialConfig,
    elapsed_s: (performance.now() - started) / 1000, instance: trial.instance,
    attn_sha256: createHash('sha256').update(readFileSync(trial.attnBinary)).digest('hex'),
    worker_binaries: spawns, selection: trial.selection, isolation: trial.isolation, screen,
  });
  console.log('PASS: pinned Claude reached its unauthenticated prompt; onboarding absent; every worker used the pinned attn; model calls=0');
} catch (error) {
  save(join(config.private_root, 'startup-check.json'), {
    at: new Date().toISOString(), model_calls: 0, authentication, initial_config: initialConfig,
    instance: trial.instance, error: error.message, screen: plain(output),
  });
  throw error;
} finally {
  await trial.stop();
}
