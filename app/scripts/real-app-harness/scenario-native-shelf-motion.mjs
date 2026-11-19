#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const TILES = 4;
const NARROWEST_COLS = '66';
const WIDE = { x: 0, y: 0, width: 1600, height: 1000 };

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SHELF-MOTION', tier: 'tier1-local-shell', prefix: 'native-shelf-motion',
  metadata: { focus: 'A row of shells wider than the native window: each slide along the row and a closing tile come to rest with nothing left leaving and only the screen\'s tiles drawn, with a shot taken mid-move' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const keys = process.platform === 'darwin'
  ? { Left: 'mod+alt+left', Right: 'mod+alt+right' }
  : { Left: 'mod+shift+left', Right: 'mod+shift+right' };

function leavesOf(desktop) {
  const walk = (node) => (node ? (node.children ? node.children.flatMap(walk) : [node.pane_id ?? node.tile_id]) : []);
  return walk(JSON.parse(desktop.tree_json || 'null'));
}

function keyedNodes(node, found = []) {
  if (node.key) found.push(node);
  for (const child of node.children ?? []) keyedNodes(child, found);
  return found;
}

async function screen() {
  const nodes = keyedNodes(await driver.tree());
  return nodes.filter((node) => /^tile\.[^.]+$/.test(node.key)).sort((a, b) => a.bounds[0] - b.bounds[0]).map((node) => node.key.slice('tile.'.length));
}

try {
  await runner.step('launch_app', async () => {
    await launchFreshAppAndConnect(client, observer);
    queueDaemonSettingRestore(observer, 'narrowest_tile_cols');
    observer.send({ cmd: 'set_setting', key: 'narrowest_tile_cols', value: NARROWEST_COLS });
    await observer.waitFor(() => observer.getSetting('narrowest_tile_cols') === NARROWEST_COLS, `narrowest_tile_cols is ${NARROWEST_COLS}`);
    await driver.setWindowBounds(WIDE);
  });

  let desktop;
  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    const created = await observer.createDesktop();
    desktop = await observer.waitFor(() => observer.desktop(created.id), `${created.id} in the arrangement`);
    runner.registerCleanup('close_desktop', async () => {
      for (const pane of observer.desktop(desktop.id)?.panes ?? []) observer.unregisterSession(pane.session_id);
      await observer.waitFor(() => !observer.desktop(desktop.id) || observer.desktop(desktop.id).panes.length === 0, 'no terminals left', 20_000);
      const left = observer.desktop(desktop.id);
      if (left) await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const row = () => leavesOf(observer.desktop(desktop.id));
  const active = () => observer.desktop(desktop.id).active_pane_id;

  const restsOn = async (label, expected) => {
    await driver.atRest();
    const shown = await screen();
    runner.assert(shown.join() === expected.join(), `${label}: at rest, only the screen's tiles are drawn`, { shown, expected });
    return shown;
  };

  await runner.step('a_row_of_shells_past_the_screen', async () => {
    let anchor = null;
    for (let index = 0; index < TILES; index += 1) {
      const id = crypto.randomUUID();
      const cwd = path.join(runner.sessionDir, `shell-${index}`);
      fs.mkdirSync(cwd, { recursive: true });
      const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `shell ${index} spawned`);
      const placement = anchor ? { desktop_id: desktop.id, direction: 'vertical', anchor_pane_id: anchor } : { desktop_id: desktop.id };
      observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement, label: `shell ${index}`, cols: 80, rows: 24 });
      const result = await spawned;
      runner.assert(result.success && result.pane_id, `spawn_session starts shell ${index}`, result);
      anchor = result.pane_id;
    }
    await observer.waitFor(() => row().length === TILES, `${TILES} shells in the row`);
    await driver.waitFor({ key: 'shelf' });
    await driver.click({ key: `tile.${active()}.header` });
  });

  const slide = async (key, label) => {
    const before = await screen();
    const order = row();
    const next = order[order.indexOf(active()) + (key === keys.Right ? 1 : -1)];
    await driver.pressKey(key);
    await observer.waitFor(() => active() === next, `${label}: the row key makes ${next} active`);
    await driver.screenshot(path.join(runner.runDir, `${label}-mid-move.png`));
    await driver.waitFor({ key: `tile.${next}` });
    const shown = await screen();
    runner.assert(shown.length === before.length && shown.includes(next), `${label}: the screen keeps its tile count and shows ${next}`, { before, shown });
    await restsOn(label, shown);
    await driver.screenshot(path.join(runner.runDir, `${label}-at-rest.png`));
  };

  await runner.step('slides_along_the_row_come_to_rest', async () => {
    const order = row();
    while (active() !== order[0]) await slide(keys.Left, `to-start-${active()}`);
    for (let index = 1; index < order.length; index += 1) await slide(keys.Right, `right-${index}`);
    runner.assert((await screen()).at(-1) === order.at(-1), 'the right key slides to the row end', { order });
    for (let index = order.length - 2; index >= 0; index -= 1) await slide(keys.Left, `left-${index}`);
    runner.assert((await screen())[0] === order[0], 'the left key slides back to the row start', { order });
  });

  await runner.step('a_closing_tile_comes_to_rest', async () => {
    const shown = await screen();
    const closing = shown.at(-1);
    const pane = observer.desktop(desktop.id).panes.find((entry) => entry.pane_id === closing);
    observer.unregisterSession(pane.session_id);
    await observer.waitFor(() => !row().includes(closing), `${closing} leaves the row`);
    await driver.waitFor({ key: `tile.${closing}` }, { gone: true });
    await driver.screenshot(path.join(runner.runDir, 'close-mid-move.png'));
    const after = await screen();
    runner.assert(after.length === shown.length && !after.includes(closing), 'the next tile takes the closed one\'s place', { shown, after });
    await restsOn('close', after);
  });

  await runner.finishSuccess({ desktopId: desktop.id, row: row() });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
