#!/usr/bin/env node
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { execFile } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';
import { outpostSsh } from './outpostSsh.mjs';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  createScenarioRunner,
  registerNativeFailureEvidence,
} from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance)
  throw new Error('Outpost update verification requires a named instance');
const runner = createScenarioRunner(options, {
  scenarioId: 'OutpostUpdate',
  tier: 'local',
  prefix: 'outpost-update',
  allowRealAgents: false,
});
const root = fs.mkdtempSync(
  path.join(
    os.platform() === 'darwin' ? '/private/tmp' : os.tmpdir(),
    'attn-update-',
  ),
);
const binary = appDaemonInTree(options.appPath);
const ssh = await outpostSsh(root);
const asset = path.join(root, 'home-build');
const launchEnv = { ...ssh.env, ATTN_HARNESS_OUTPOST_BINARY: asset };
const client = new UiAutomationClient({ ...options, launchEnv });
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const exec = promisify(execFile);
const run = (command, args, env) => exec(command, args, { env });
const homeEnv = instanceCliEnv(instance, launchEnv);
const row = '.settings-outpost[aria-label="Update studio"]';
const banner = '[role="region"][aria-label="Outpost updates"]';
const wait = (selector, extra = {}) =>
  client.request('dom_wait', {
    selector,
    timeoutMs: observer.connectTimeoutMs,
    ...extra,
  });
const receive = (predicate) =>
  observer.waitForMessage(
    (m) => m.event === 'outposts_updated' && m.outposts.find(predicate),
    'outpost update state',
    observer.connectTimeoutMs,
  );
const shot = async (name) =>
  driver.screenshot(path.join(runner.runDir, `${name}.png`), {
    windowId: await driver.mainWindowId(),
  });
let remoteEnv, target, outpostId;
const phases = [];
registerNativeFailureEvidence(runner, {
  client,
  driver,
  selectors: () => ({ banner, row }),
});
async function press(selector) {
  await wait(selector);
  await client.request('dom_focus', { selector });
  await driver.pressEnter();
}
async function update(selector, name) {
  const done = receive(
    (r) =>
      r.id === outpostId &&
      r.state === 'connected' &&
      r.update?.phase === 'done',
  );
  await press(selector);
  const updated = await done;
  runner.assert(
    updated.build && !updated.last_error,
    'Update connected on home build',
    updated,
  );
  await shot(name);
}
runner.registerCleanup('remove_scratch', () =>
  fs.rmSync(root, { recursive: true, force: true }),
);
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
    ATTN_DATA_DIR: data,
    ATTN_HARNESS_DATA_DIR: data,
    ATTN_HARNESS_NOTEBOOK_ROOT: path.join(data, 'notebook'),
    ATTN_WS_PORT: String(wsPort),
    ATTN_HEADLESS_TASKS: 'off',
    ATTN_GITHUB_POLLING: 'off',
    ATTN_HARNESS_SKILL_SYNC: 'off',
    ATTN_PTY_BACKEND: 'embedded',
    ATTN_CACHED_SHELL_ENV: '[]',
    ATTN_CODEX_SHARED_PROFILE: '',
    CODEX_THREAD_ID: '',
  });
  target = await ssh.route('scenario-outpost', remoteEnv, binary);
  fs.unlinkSync(target);
  await run(binary, ['daemon', 'stop'], homeEnv);
  await launchFreshAppAndConnect(client, observer);
  runner.assert(
    observer.outposts.length === 0,
    'starts without other outposts',
    observer.outposts,
  );
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
  runner.registerCleanup('restore_switch', () =>
    client.request('set_setting', { key: 'outposts_enabled', value: previous }),
  );
  runner.registerCleanup('remove_outpost', async () => {
    if (!outpostId || !observer.outposts.some((r) => r.id === outpostId))
      return;
    const done = observer.waitForMessage(
      (m) => m.event === 'outposts_updated' && m.outposts.length === 0,
      'removed outpost',
    );
    observer.send({ cmd: 'remove_outpost', id: outpostId });
    await done;
  });
  await client.request('set_setting', {
    key: 'outposts_enabled',
    value: 'true',
  });
  await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
  await press('[data-testid="settings-nav-outposts"]');
  await runner.step('failed_add_offers_banner_update', async () => {
    const refused = receive(
      (r) =>
        r.name === 'Update studio' &&
        r.state === 'refused' &&
        r.update?.phase === 'failed',
    );
    await client.request('dom_focus', { selector: '#outpost-name' });
    await driver.typeText('Update studio');
    await client.request('dom_focus', { selector: '#outpost-destination' });
    await driver.typeText('scenario-outpost');
    await press('form[aria-label="Add an outpost"] button');
    const added = await refused;
    outpostId = added.id;
    runner.assert(
      !fs.existsSync(target),
      'failed first install leaves no binary',
      added,
    );
    fs.copyFileSync(binary, asset);
    await press('[aria-label="Close settings"]');
    await wait('[data-testid="settings-modal"]', { absent: true });
    await wait(banner, { textIncludes: "need your home's build" });
    await shot('00-offer');
    await update(`${banner} .banner-button`, '01-banner-updated');
  });
  await runner.step('missing_build_offers_row_update', async () => {
    await press('[aria-label="Dismiss outpost updates"]');
    await wait(banner, { absent: true });
    await client.request('set_setting', {
      key: 'outposts_enabled',
      value: 'false',
    });
    fs.unlinkSync(target);
    const refused = receive(
      (r) =>
        r.id === outpostId &&
        r.state === 'refused' &&
        r.build === 'nothing installed',
    );
    await client.request('set_setting', {
      key: 'outposts_enabled',
      value: 'true',
    });
    await refused;
    await client.request('dispatch_shortcut', {
      shortcutId: 'ui.openSettings',
    });
    await press('[data-testid="settings-nav-outposts"]');
    await wait(`${row} .settings-outpost-actions button:first-child`, {
      textIncludes: 'Update to home build',
    });
    await wait(banner, { absent: true });
    await shot('02-missing-build');
    await update(
      `${row} .settings-outpost-actions button:first-child`,
      '03-row-updated',
    );
    await wait(banner, { absent: true });
    runner.writeJson('update-phases.json', { phases });
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
