#!/usr/bin/env node
import fs from 'node:fs';
import { outpostSsh } from './outpostSsh.mjs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Outpost update verification requires a named instance');
const runner = createScenarioRunner(options, { scenarioId: 'OutpostUpdate', tier: 'local', prefix: 'outpost-update', allowRealAgents: false });
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-update-'));
const binary = appDaemonInTree(options.appPath);
const ssh = await outpostSsh(root);
const client = new UiAutomationClient({ ...options, launchEnv: ssh.env });
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const env = instanceCliEnv(instance, ssh.env);
const exec = promisify(execFile);
const run = (command, args, childEnv = env) => exec(command, args, { env: childEnv });
const children = [];
const phases = [];
const row = '.settings-outpost[aria-label="Update studio"]';
const banner = '[role="region"][aria-label="Outpost updates"]';
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ banner, row, settings: '[data-testid="settings-modal"]' }) });
const wait = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: observer.connectTimeoutMs, ...extra });
const shot = async name => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
let outpostEnv;
let outpostId;
let currentRemote;
let closing = false;
let restartAllowed = Promise.resolve();
let releaseRestart = () => {};
let restarted = Promise.resolve();
let completeRestart = () => {};
let failRestart = () => {};
let restartCount = 0;

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

async function press(selector) {
  await wait(selector);
  await client.request('dom_focus', { selector });
  await driver.pressEnter();
}

const receive = predicate => observer.waitForMessage(message => message.event === 'outposts_updated' && predicate(message.outposts) && message.outposts, 'outpost update state', observer.connectTimeoutMs);
const remoteBinary = path.join(root, 'attn');
const oldBinary = path.join(root, 'attn-old');

async function startRemote() {
  const log = fs.openSync(path.join(runner.runDir, 'outpost.log'), 'a');
  const child = start(remoteBinary, ['daemon'], outpostEnv, ['ignore', log, log, 'pipe']);
  fs.closeSync(log);
  currentRemote = child;
  child.once('exit', code => {
    if (code !== 75 || closing) return;
    restartCount++;
    restartAllowed.then(() => closing ? undefined : startRemote()).then(completeRestart, failRestart);
  });
  const [ready] = await once(child.stdio[3], 'data');
  runner.assert(ready.toString().includes('ready'), 'supervised outpost signals readiness', ready.toString());
}

async function stopRemote() {
  if (!currentRemote || currentRemote.exitCode !== null || currentRemote.signalCode !== null) return;
  const exited = once(currentRemote, 'exit');
  currentRemote.kill('SIGTERM');
  await exited;
}

async function update(selector, name, visibleBanner) {
  const done = receive(rows => rows.some(item => item.id === outpostId && item.update?.phase === 'failed'));
  await press(selector);
  const rows = await done;
  const outcome = rows.find(item => item.id === outpostId);
  runner.assert(outcome.update.outcome.includes('Update needs the ssh install, which is not available in this build'), 'Update names the interim hand install', outcome);
  await shot(`${name}-refused`);
}

runner.registerCleanup('remove_scratch', () => fs.rmSync(root, { recursive: true, force: true }));
runner.registerCleanup('stop_children', async () => {
  closing = true;
  releaseRestart();
  await restarted.catch(() => {});
  for (const child of children.toReversed()) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    const exited = once(child, 'exit');
    child.kill('SIGTERM');
    await exited;
  }
});
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('close_ssh', () => ssh.close());

try {

  const info = JSON.parse((await run(binary, ['--build-info-json'])).stdout);
  await run('make', ['-C', fileURLToPath(new URL('../../..', import.meta.url)), 'build', `OUTPUT=${oldBinary}`, 'VERSION=before-outpost-update', `SOURCE_FINGERPRINT=${info.sourceFingerprint}`]);
  fs.copyFileSync(oldBinary, remoteBinary);
  const data = path.join(root, 'outpost');
  fs.mkdirSync(data, { mode: 0o700 });
  outpostEnv = instanceCliEnv(instance, {
    ATTN_DATA_DIR: data, ATTN_HARNESS_DATA_DIR: data, ATTN_HARNESS_NOTEBOOK_ROOT: path.join(data, 'notebook'),
    ATTN_WS_PORT: String(await port()), ATTN_DAEMON_READY_FD: '3', ATTN_AUTHORIZED_KEYS_PATH: `${key}.pub`,
    ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off', ATTN_HARNESS_SKILL_SYNC: 'off',
    ATTN_PTY_BACKEND: 'embedded', ATTN_CACHED_SHELL_ENV: '[]', INVOCATION_ID: 'scenario-supervisor',
  });
  await startRemote();
  const address = 'scenario-outpost';
  await ssh.route(address, outpostEnv, remoteBinary);
  await run(binary, ['daemon', 'stop']);
  await launchFreshAppAndConnect(client, observer);
  runner.assert(observer.outposts.length === 0, 'scenario starts without other outposts', observer.outposts);
  observer.ws.on('message', raw => {
    const message = JSON.parse(raw.toString());
    if (message.event !== 'outposts_updated') return;
    const phase = message.outposts.find(item => item.id === outpostId)?.update?.phase;
    if (phase && phase !== phases.at(-1)) phases.push(phase);
  });
  const bounds = await client.request('get_window_bounds');
  await client.request('set_window_bounds', { logicalBounds: { ...bounds.logicalBounds, x: 0, y: 0, width: 1200, height: 800 } });
  const previous = observer.getSetting('outposts_enabled');
  runner.registerCleanup('restore_switch', () => client.request('set_setting', { key: 'outposts_enabled', value: previous }));
  runner.registerCleanup('remove_outpost', async () => {
    if (!outpostId || !observer.outposts.some(item => item.id === outpostId)) return;
    const removed = receive(rows => !rows.some(item => item.id === outpostId));
    observer.ws.send(JSON.stringify({ cmd: 'remove_outpost', id: outpostId }));
    await removed;
  });
  await client.request('set_setting', { key: 'outposts_enabled', value: 'true' });
  await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
  await press('[data-testid="settings-nav-outposts"]');
  await runner.step('offer_and_banner_update', async () => {
    const refused = receive(rows => rows.some(item => item.name === 'Update studio' && item.state === 'refused'));
    await client.request('dom_focus', { selector: '#outpost-name' });
    await driver.typeText('Update studio');
    await client.request('dom_focus', { selector: '#outpost-destination' });
    await driver.typeText(address);
    await press('form[aria-label="Add an outpost"] button');
    const rows = await refused;
    outpostId = rows.find(item => item.name === 'Update studio').id;
    await press('[aria-label="Close settings"]');
    await wait('[data-testid="settings-modal"]', { absent: true });
    await wait(banner, { textIncludes: 'different build' });
    await shot('00-offer');
    await update(`${banner} .banner-button`, '01-banner', true);
  });
  await runner.step('dismissal_and_row_update', async () => {
    await press('[aria-label="Dismiss outpost updates"]');
    await wait(banner, { absent: true });
    const refused = receive(rows => rows.some(item => item.id === outpostId && item.state === 'refused'));
    await stopRemote();
    fs.copyFileSync(oldBinary, `${remoteBinary}.next`);
    fs.renameSync(`${remoteBinary}.next`, remoteBinary);
    await startRemote();
    await refused;
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await press('[data-testid="settings-nav-outposts"]');
    await wait(`${row} .settings-outpost-actions button:first-child`, { textIncludes: 'Update to home build' });
    await client.request('dom_scroll_into_view', { selector: `${row} .settings-outpost-actions` });
    await wait(banner, { absent: true });
    await shot('02-dismissal-retained');
    await update(`${row} .settings-outpost-actions button:first-child`, '03-row', false);
    await wait(banner, { absent: true });
    runner.assert(restartCount === 0, 'the interim Update refusal leaves the outpost running', { restartCount, phases });
    runner.writeJson('update-phases.json', { phases, restartCount });
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  releaseRestart();
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
