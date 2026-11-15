#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { PNG } from 'pngjs';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const HIGHLIGHT_STEP = 16;

function menuRows(node) {
  const own = node.role === 'menu-item' ? [node] : [];
  return own.concat((node.children ?? []).flatMap(menuRows));
}

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SIDEBAR-SESSION', tier: 'tier1-local-shell', prefix: 'native-sidebar-session',
  metadata: { focus: 'The native app launches parked, connects, lists a shell session spawned while it runs in its sidebar and takes a press on its row and paints the session menu row that keys reach, without taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, 'shop');
  fs.mkdirSync(cwd, { recursive: true });
  await runner.step('spawn_shell', async () => {
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: {}, label: 'shop', cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
  });

  const row = { key: `sidebar.session.${id}` };
  let listed;
  await runner.step('sidebar_lists_session', async () => {
    listed = await driver.waitFor(row);
    runner.assert(listed.role === 'button', 'the sidebar lists the session as a row that opens it', listed);
  });

  let receipt;
  await runner.step('press_row', async () => {
    receipt = await driver.click(row);
    runner.assert(receipt.matched, 'the press lands on the session row', receipt);
  });

  const screenshot = path.join(runner.runDir, 'native-window.png');
  await runner.step('capture_window', () => driver.screenshot(screenshot));

  const menuShot = path.join(runner.runDir, 'native-session-menu.png');
  await runner.step('the_menu_paints_the_row_keys_reach', async () => {
    await driver.click({ role: 'button', name: 'Actions for shop' });
    await driver.waitFor({ key: 'session.menu-menu' });
    await driver.pressKey('Down');
    const rows = menuRows(await driver.tree());
    const focused = rows.find((row) => row.states?.includes('focused'));
    const resting = rows.find((row) => row !== focused);
    runner.assert(focused && resting, 'Down focuses one of the menu rows', rows);
    await driver.screenshot(menuShot);
    const image = PNG.sync.read(fs.readFileSync(menuShot));
    const window = await driver.find({ key: 'window' });
    const scale = Math.max(1, Math.round(image.width / window.rect[2]));
    const fill = (row) => {
      const [x, y, , h] = row.bounds;
      const at = (Math.round((y + h / 2) * scale) * image.width + Math.round((x + 3) * scale)) * 4;
      return [...image.data.subarray(at, at + 3)];
    };
    const [lit, rest] = [fill(focused), fill(resting)];
    const step = Math.max(...lit.map((c, i) => Math.abs(c - rest[i])));
    runner.assert(step >= HIGHLIGHT_STEP, `the focused row's fill stands out from a resting row's by ${HIGHLIGHT_STEP} levels or more`, { focused: focused.key, scale, lit, rest, step });
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'session.menu-menu' }, { gone: true });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ sessionId: id, listed, receipt, screenshot, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
