#!/usr/bin/env node
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { execFile } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';
import { outpostSsh } from './outpostSsh.mjs';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Outpost update verification requires a named instance');
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-OUTPOST-UPDATE', tier: 'local', prefix: 'native-outpost-update', allowRealAgents: false,
  metadata: { focus: 'A failed first Add offers the update in the native Outpost updates band, which installs the home build and closes after its countdown; a missing build offers the row\'s Update to home build' },
});
const root = fs.mkdtempSync(path.join(os.platform() === 'darwin' ? '/private/tmp' : os.tmpdir(), 'attn-update-'));
const binary = appCliInTree(options.appPath);
const ssh = await outpostSsh(root);
const asset = path.join(root, 'home-build');
const hostAsset = path.join(root, 'home-host-build');
fs.writeFileSync(hostAsset, '#!/bin/sh\nexit 1\n', { mode: 0o755 });
const launchEnv = { ...ssh.env, ATTN_HARNESS_OUTPOST_BINARY: asset, ATTN_HARNESS_OUTPOST_HOST_BINARY: hostAsset };
const client = new UiAutomationClient({ appPath: options.appPath, launchEnv });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const exec = promisify(execFile);
const run = (command, args, env) => exec(command, args, { env });
const homeEnv = instanceCliEnv(instance, launchEnv);
const mac = process.platform === 'darwin';
const OPEN_SETTINGS = mac ? 'mod+,' : 'mod+shift+,';
const BANNER = { key: 'outpost-updates' };
const receive = (predicate) => observer.waitForMessage((m) => m.event === 'outposts_updated' && m.outposts.find(predicate), 'outpost update state', observer.connectTimeoutMs);
const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));
let remoteEnv, target, outpostId;
const phases = [];
const row = (part) => ({ key: `settings.outpost.${outpostId}.${part}` });
/** What the element `key` reads once the app holds `held` among its outposts. */
async function reads(key, held) {
  await driver.state('outposts', { includes: held });
  return (await driver.find({ key }))?.name ?? '';
}
async function update(press, name) {
  const done = receive((r) => r.id === outpostId && r.state === 'connected' && r.update?.phase === 'done');
  await driver.click(press);
  await driver.hover({ key: 'home' }, { allowMiss: true });
  const updated = await done;
  runner.assert(updated.build && !updated.last_error, 'Update connected on home build', updated);
  await shot(name);
}
async function openOutposts() {
  await driver.pressKey(OPEN_SETTINGS);
  await driver.waitFor({ key: 'settings-dialog' });
  await driver.click({ key: 'settings.nav.outposts' });
  await driver.state('settings', { expect: { activeSection: 'outposts' } });
}
runner.registerCleanup('remove_scratch', () => fs.rmSync(root, { recursive: true, force: true }));
runner.registerCleanup('close_ssh', () => ssh.close());
runner.registerCleanup('stop_remote', async () => {
  if (remoteEnv) await run(binary, ['daemon', 'stop'], remoteEnv);
});
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());
try {
  const data = path.join(root, 'outpost');
  fs.mkdirSync(data, { mode: 0o700 });
  const port = net.createServer();
  port.listen(0, '127.0.0.1');
  await once(port, 'listening');
  const wsPort = port.address().port;
  await new Promise((r) => port.close(r));
  remoteEnv = instanceCliEnv(instance, {
    ATTN_DATA_DIR: data, ATTN_HARNESS_DATA_DIR: data, ATTN_HARNESS_NOTEBOOK_ROOT: path.join(data, 'notebook'),
    ATTN_WS_PORT: String(wsPort), ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off', ATTN_HARNESS_SKILL_SYNC: 'off',
    ATTN_PTY_BACKEND: 'worker', ATTN_CACHED_SHELL_ENV: '[]', ATTN_CODEX_SHARED_PROFILE: '', CODEX_THREAD_ID: '',
  });
  target = await ssh.route('scenario-outpost', remoteEnv, binary);
  fs.unlinkSync(target);
  await run(binary, ['daemon', 'stop'], homeEnv);
  await launchFreshNativeAppAndConnect(client, observer);
  runner.assert(observer.outposts.length === 0, 'starts without other outposts', observer.outposts);
  observer.ws.on('message', (raw) => {
    try {
      const m = JSON.parse(raw.toString());
      if (m.event === 'outposts_updated') {
        const phase = m.outposts.find((r) => r.id === outpostId)?.update?.phase;
        if (phase && phase !== phases.at(-1)) phases.push(phase);
      }
    } catch {}
  });
  const previous = observer.getSetting('outposts_enabled');
  runner.registerCleanup('restore_switch', () => run(binary, ['settings', 'set', 'outposts_enabled', previous], homeEnv));
  runner.registerCleanup('remove_outpost', async () => {
    if (!outpostId || !observer.outposts.some((r) => r.id === outpostId)) return;
    const done = observer.waitForMessage((m) => m.event === 'outposts_updated' && m.outposts.length === 0, 'removed outpost');
    observer.send({ cmd: 'remove_outpost', id: outpostId });
    await done;
  });
  await run(binary, ['settings', 'set', 'outposts_enabled', 'true'], homeEnv);
  await openOutposts();
  await runner.step('failed_add_offers_banner_update', async () => {
    const refused = receive((r) => r.name === 'Update studio' && r.state === 'refused' && r.update?.phase === 'failed');
    await driver.typeInto({ key: 'settings.outposts.name' }, 'Update studio');
    await driver.typeInto({ key: 'settings.outposts.destination' }, 'scenario-outpost');
    await driver.click({ key: 'settings.outposts.submit' });
    const added = await refused;
    outpostId = added.id;
    runner.assert(!fs.existsSync(target), 'failed first install leaves no binary', added);
    fs.copyFileSync(path.join(path.dirname(binary), 'attnd'), path.join(root, 'attnd'));
    fs.copyFileSync(binary, asset);
    await driver.click({ role: 'button', name: 'Close' });
    await driver.waitFor({ key: 'settings-dialog' }, { gone: true });
    await driver.waitFor(BANNER);
    const said = await reads('outpost-updates.text', 'failed');
    runner.assert(said.startsWith('Couldn’t update Update studio.'), 'the band offers the update after a failed Add', said);
    await shot('00-offer');
    await update({ key: 'outpost-updates.update' }, '01-banner-updated');
  });
  await runner.step('missing_build_offers_row_update', async () => {
    await driver.waitFor({ key: 'outpost-updates.countdown' });
    await driver.waitFor(BANNER, { gone: true });
    await run(binary, ['settings', 'set', 'outposts_enabled', 'false'], homeEnv);
    fs.unlinkSync(target);
    const refused = receive((r) => r.id === outpostId && r.state === 'refused' && r.build === 'nothing installed');
    await run(binary, ['settings', 'set', 'outposts_enabled', 'true'], homeEnv);
    await refused;
    await openOutposts();
    const offered = await reads(`settings.outpost.${outpostId}.update-build`, 'nothing installed');
    runner.assert(offered === 'Update to home build', 'the row offers the home build', offered);
    const said = await reads('outpost-updates.text', 'nothing installed');
    runner.assert(said === 'Update studio runs a different build than home.', 'the band names the outpost on another build', said);
    await shot('02-missing-build');
    await update(row('update-build'), '03-row-updated');
    await driver.waitFor(BANNER, { gone: true });
    runner.writeJson('update-phases.json', { phases });
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  await shot('failure').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
