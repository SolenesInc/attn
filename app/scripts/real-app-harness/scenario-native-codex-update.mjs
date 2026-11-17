#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { installMockCodex } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-CODEX-UPDATE', tier: 'tier2-local-mock-agent', prefix: 'native-codex-update',
  metadata: { focus: "The native app's Codex updates band offers a newer Codex, installs it in a tile, and reloads the tile still on the older Codex" },
});
const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));

const BAND = 'codex-updates';
const said = (name) => driver.waitFor({ key: `${BAND}.text`, name }, { timeoutMs: 60_000 });
const sessions = [];

async function setSetting(key, value) {
  queueDaemonSettingRestore(observer, key);
  observer.send({ cmd: 'set_setting', key, value });
  await observer.waitFor(() => observer.getSetting(key) === value, `${key}=${value}`);
}

try {
  const codex = installMockCodex(path.join(runner.sessionDir, 'codex-install'), { version: '1.0.0', latest: '1.1.0' });
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  let desktop;
  await runner.step('shared_codex_offers_the_newer_release', async () => {
    await setSetting('codex_executable', codex);
    await setSetting('auto_approve_enabled', 'false');
    const before = observer.currentDesktopId();
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => {
      const left = observer.desktop(desktop.id);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    const cwd = path.join(runner.sessionDir, 'shop');
    fs.mkdirSync(cwd, { recursive: true });
    const shop = crypto.randomUUID();
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === shop && m, 'the Codex agent spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: shop, agent: 'codex', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'shop', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session starts the Codex agent');
    sessions.push(shop);
    runner.registerCleanup('unregister_sessions', () => observer.unregisterMatchingSessions((s) => sessions.includes(s.id)));
    await said('Codex 1.1.0 is available.');
    await driver.screenshot(path.join(runner.runDir, '01-update-available.png'));
  });

  await runner.step('install_runs_in_a_tile_and_shared_codex_moves_to_it', async () => {
    const before = new Set(observer.sessionsById.keys());
    await driver.click({ key: `${BAND}.install` });
    const shell = await observer.waitFor(
      () => [...observer.sessionsById.values()].find((s) => !before.has(s.id) && s.agent === 'shell')?.id,
      "a shell tile running Codex's update",
      30_000,
    );
    sessions.push(shell);
    const runtime = await observer.waitFor(() => observer.terminalOf(shell), 'the update tile has a terminal', 30_000);
    await driver.click({ key: `sidebar.session.${shell}` });
    await driver.waitFor({ key: `terminal.${runtime}` });
    await driver.state(`terminal:${runtime}`, { includes: 'Codex updated to 1.1.0', timeoutMs: 30_000 });
    await said('Codex 1.1.0 is running. 1 tile still runs an older Codex.');
    const status = attn('codex', 'status');
    runner.assert(/^server\s+1\.1\.0$/m.test(status), 'attn codex status reports the app-server on 1.1.0', status);
    await driver.screenshot(path.join(runner.runDir, '02-old-tiles.png'));
  });

  await runner.step('reload_tiles_moves_the_old_tile_to_the_new_codex', async () => {
    await driver.click({ key: `${BAND}.reload` });
    await driver.waitFor({ key: 'shared-codex-confirm-dialog' });
    await driver.click({ key: 'shared-codex-confirm.confirm' });
    await driver.waitFor({ key: BAND }, { gone: true, timeoutMs: 30_000 });
    const status = attn('codex', 'status');
    runner.assert(/^server\s+1\.1\.0$/m.test(status) && !/^old tiles/m.test(status), 'no tile runs an older Codex after the reload', status);
  });

  await runner.finishSuccess({ sessions });
} catch (error) {
  await runner.finishFailure(error, { sessions });
  process.exitCode = 1;
}
