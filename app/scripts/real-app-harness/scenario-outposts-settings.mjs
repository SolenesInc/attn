#!/usr/bin/env node
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Outposts settings verification requires a named instance');
const runner = createScenarioRunner(options, { scenarioId: 'OutpostsSettings', tier: 'local', prefix: 'outposts-settings', allowRealAgents: false });
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-bridge-'));
const socket = path.join(root, 'agent.sock');
const client = new UiAutomationClient({ ...options, launchEnv: { SSH_AUTH_SOCK: socket } });
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const binary = appDaemonInTree(options.appPath);
const exec = promisify(execFile);
const children = [];
const env = instanceCliEnv(instance, { SSH_AUTH_SOCK: socket });
const run = (command, args, childEnv = env) => exec(command, args, { env: childEnv });
const wait = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: observer.connectTimeoutMs, ...extra });
const text = async selector => (await client.request('dom_text', { selector })).text;
const studio = '.settings-outpost[aria-label="Studio"]';
const alias = '.settings-outpost[aria-label="Studio alias"]';
const toggle = '[role="switch"][aria-label="Outposts"]';
const shot = async name => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const receive = predicate => observer.waitForMessage(event => event.event === 'outposts_updated' && predicate(event.outposts) && event.outposts, 'outposts state', observer.connectTimeoutMs);
async function port() {
  const server = net.createServer();
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const value = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return value;
}
function start(command, args, childEnv, stdio) {
  const child = spawn(command, args, { env: childEnv, stdio });
  children.push(child);
  return child;
}
async function press(selector, key = 'Enter') {
  await client.request('dom_focus', { selector });
  await driver.pressKey(key);
}
async function add(name, address) {
  await client.request('dom_focus', { selector: '#outpost-name' });
  await driver.typeText(name);
  await driver.pressKey('Tab');
  await wait('#outpost-address', { focused: true });
  await driver.typeText(address);
  await driver.pressKey('Tab');
  await wait('form[aria-label="Add an outpost"] button', { focused: true });
  await driver.pressEnter();
}
runner.registerCleanup('remove_scratch', () => fs.rmSync(root, { recursive: true, force: true }));
runner.registerCleanup('stop_children', async () => {
  for (const child of [...children].reverse()) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    const exited = once(child, 'exit');
    child.kill('SIGCONT');
    child.kill('SIGTERM');
    await exited;
  }
});
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());
try {
  const agent = start('ssh-agent', ['-D', '-a', socket], env, ['ignore', 'pipe', 'pipe']);
  await once(agent.stdout, 'data');
  const key = path.join(root, 'key');
  await run('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', key]);
  await run('ssh-add', [key]);
  const outpostData = path.join(root, 'outpost');
  fs.mkdirSync(outpostData);
  const outpostEnv = instanceCliEnv('', {
    ATTN_DATA_DIR: outpostData,
    ATTN_HARNESS_NOTEBOOK_ROOT: path.join(outpostData, 'notebook'), ATTN_WS_PORT: String(await port()), ATTN_DAEMON_READY_FD: '3',
    ATTN_AUTHORIZED_KEYS_PATH: `${key}.pub`, ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off',
    ATTN_HARNESS_SKILL_SYNC: 'off', ATTN_PTY_BACKEND: 'embedded', ATTN_CACHED_SHELL_ENV: '[]',
    ATTN_CODEX_SHARED_PROFILE: '', CODEX_THREAD_ID: '',
  });
  const log = fs.openSync(path.join(runner.runDir, 'outpost.log'), 'w');
  const outpost = start(binary, ['daemon'], outpostEnv, ['ignore', log, log, 'pipe']);
  fs.closeSync(log);
  const [ready] = await once(outpost.stdio[3], 'data');
  runner.assert(ready.toString().includes('ready'), 'outpost reported readiness', ready.toString());
  const bridgePort = await port();
  const address = `127.0.0.1:${bridgePort}`;
  await run(binary, ['settings', 'set', 'outpost_listen', address], outpostEnv);
  await run(binary, ['daemon', 'stop']);
  await launchFreshAppAndConnect(client, observer);
  const previous = observer.getSetting('outposts_enabled');
  runner.registerCleanup('restore_switch', () => client.request('set_setting', { key: 'outposts_enabled', value: previous }));
  await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
  await runner.step('keyboard_opt_in_and_add', async () => {
    await wait('[data-testid="settings-nav-outposts"]', { absent: true });
    await press('[data-testid="settings-nav-experimental"]');
    await press(toggle, 'space');
    await wait('[data-testid="settings-nav-outposts"]');
    await shot('00-experimental');
    await press('[data-testid="settings-nav-outposts"]');
    const connected = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'connected'));
    await add('Studio', address);
    const rows = await connected;
    const row = rows.find(row => row.name === 'Studio');
    runner.assert(Boolean(row.host_key_fingerprint && row.build && row.daemon_id), 'first contact pins identity and build', row);
    await wait(`${studio} .settings-pill`, { textIncludes: 'connected' });
    await wait(studio, { textIncludes: row.host_key_fingerprint });
    await shot('01-connected');
  });
  await runner.step('frozen_outpost_and_recovery', async () => {
    const stalled = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'stalled'));
    const lost = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'lost'));
    outpost.kill('SIGSTOP');
    await stalled;
    await wait(`${studio} .settings-pill`, { textIncludes: 'stalled' });
    await shot('02-stalled');
    await lost;
    await wait(`${studio} .settings-pill`, { textIncludes: 'lost' });
    await shot('03-lost');
    const recovered = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'connected' && !row.last_error));
    outpost.kill('SIGCONT');
    await recovered;
    await wait(`${studio} .settings-pill`, { textIncludes: 'connected' });
    await wait(`${studio} .settings-outpost-error`, { absent: true });
    await shot('04-recovered');
  });
  await runner.step('full_refusal_keyboard_repair_and_remove', async () => {
    const refusal = receive(rows => rows.some(row => row.name === 'Studio alias' && row.state === 'refused'));
    await add('Studio alias', `localhost:${bridgePort}`);
    const rows = await refusal;
    const refused = rows.find(row => row.name === 'Studio alias');
    await wait(`${alias} .settings-pill`, { textIncludes: 'refused' });
    runner.assert((await text(`${alias} .settings-outpost-error p`)) === refused.last_error, 'refusal detail is shown in full', refused);
    await client.request('dom_scroll_into_view', { selector: alias });
    await shot('05-refused');
    const removed = receive(rows => !rows.some(row => row.name === 'Studio alias'));
    await press(`${alias} button:last-child`);
    await removed;
    await wait(alias, { absent: true });
    const before = rows.find(row => row.name === 'Studio').state_since;
    const repaired = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'connected' && row.state_since !== before));
    await press(`${studio} button:first-child`);
    await repaired;
    const empty = receive(rows => rows.length === 0);
    await press(`${studio} button:last-child`);
    await empty;
    await wait(studio, { absent: true });
    await press('[data-testid="settings-nav-experimental"]');
    await press(toggle, 'space');
    await wait('[data-testid="settings-nav-outposts"]', { absent: true });
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  await shot('failure').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
