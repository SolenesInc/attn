#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SHELF-DROP-BESIDE', tier: 'tier1-local-shell', prefix: 'native-shelf-drop-beside',
  metadata: { focus: 'In the native app, a tile dropped beside a chip comes on screen next to it, active, for every drop place on a four-tile row that shows two and when a saved width keeps the pair apart, with pointer input through the app' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('screenshot', async () => {
  const file = path.join(runner.runDir, 'failure-native.png');
  await driver.screenshot(file);
  return file;
});

const LEFT_HALF = 0.15;
const RIGHT_HALF = 0.85;
const keys = process.platform === 'darwin'
  ? { right: 'mod+d', below: 'mod+shift+d', focusRight: 'mod+alt+right', fewerTiles: 'mod+alt+-' }
  : { right: 'mod+shift+d', below: 'mod+alt+d', focusRight: 'mod+shift+right', fewerTiles: 'mod+alt+-' };
let desktop;

const leavesOf = (node) => (node.type === 'split' ? node.children.flatMap(leavesOf) : [node.pane_id ?? node.tile_id]);
const columnsOf = (node) => (node.type === 'split' && node.direction === 'vertical' ? node.children.flatMap(columnsOf) : [node]);
const firstLeafOf = (node) => (node.type === 'split' ? firstLeafOf(node.children[0]) : node);
const treeOf = (entry) => JSON.parse(entry.tree_json || 'null');
const current = () => observer.desktop(desktop.id);
const row = () => leavesOf(treeOf(current()));
const wantedIn = (entry, leaf) => firstLeafOf(columnsOf(treeOf(entry)).find((column) => leavesOf(column).includes(leaf))).wanted_cols ?? 0;
const sameList = (left, right) => left.join() === right.join();
const tile = (leaf) => ({ key: `tile.${leaf}` });
const chip = (leaf) => ({ key: `shelf.chip.${leaf}` });
const header = (leaf) => ({ key: `tile.${leaf}.header` });
const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));

async function shown() {
  const found = [];
  for (const leaf of row()) {
    const hit = await driver.find(tile(leaf));
    if (hit) found.push([leaf, hit.rect[0], hit.rect[1]]);
  }
  return found.sort((a, b) => a[1] - b[1] || a[2] - b[2]).map(([leaf]) => leaf);
}

async function keyboardOn(leaf) {
  await observer.waitFor(() => current().active_pane_id === leaf, `${leaf} active`, 20_000);
  const window = await driver.state('window', { includes: leaf, timeoutMs: 5000 }).catch(() => driver.state('window'));
  runner.assert(window.keyboard?.pane === leaf, `typing goes to ${leaf}`, { window, active: current().active_pane_id });
}

const sessions = new Set();
runner.registerCleanup('unregister_shells', () => { for (const id of sessions) observer.unregisterSession(id); });

async function firstShell() {
  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, 'row');
  fs.mkdirSync(cwd, { recursive: true });
  const answered = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'the first shell spawned');
  observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'row', cols: 80, rows: 24 });
  const result = await answered;
  runner.assert(result.success && result.pane_id, 'spawn_session starts the first shell', result);
  sessions.add(id);
  await driver.waitFor(tile(result.pane_id));
  await keyboardOn(result.pane_id);
  return result.pane_id;
}

async function split(chord) {
  const before = new Set(current().panes.map((pane) => pane.pane_id));
  await driver.pressKey(chord);
  const entry = await observer.waitFor(() => {
    const now = current();
    return now && now.panes.length > before.size && now.panes.every((pane) => pane.runtime_id) && now;
  }, 'a new shell', 30_000);
  const made = entry.panes.find((pane) => !before.has(pane.pane_id));
  sessions.add(made.session_id);
  await driver.waitFor(tile(made.pane_id));
  await keyboardOn(made.pane_id);
  return made.pane_id;
}

async function setNarrowest(cols) {
  await observer.requestResult({ cmd: 'set_setting', key: 'narrowest_tile_cols', value: String(cols) }, 'settings_updated');
  await observer.waitFor(() => observer.getSetting('narrowest_tile_cols') === String(cols), `narrowest ${cols} cols`);
}

async function dropBeside(moving, anchor, share) {
  const rowBefore = row();
  const shownBefore = await shown();
  const side = share === LEFT_HALF ? 'before' : 'after';
  await driver.drag(header(moving), { target: chip(anchor), at: [share, 0.5] });
  const entry = await observer.waitFor(() => {
    const now = current();
    const order = leavesOf(treeOf(now));
    return order.indexOf(moving) - order.indexOf(anchor) === (side === 'before' ? -1 : 1) && now.active_pane_id === moving && now;
  }, `${moving} ${side} ${anchor}, active`, 20_000);
  const order = leavesOf(treeOf(entry));
  const expected = order.filter((leaf) => leaf === moving || leaf === anchor);
  for (const leaf of expected) await driver.waitFor(tile(leaf));
  for (const leaf of order.filter((id) => !expected.includes(id))) await driver.waitFor(tile(leaf), { gone: true });
  const after = await shown();
  runner.assert(sameList(after, expected), 'the screen shows the dropped tile beside the chip’s tile', { after, expected });
  await keyboardOn(moving);
  runner.assert(!sameList(after, shownBefore) || !sameList(order, rowBefore), 'the drop changes what the user sees', { shownBefore, after, rowBefore, order });
  runner.log('dropped', { moving, anchor, side, rowBefore, shownBefore, order, after });
}

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('size_the_window_and_the_narrowest_width', async () => {
    const narrowest = observer.getSetting('narrowest_tile_cols');
    runner.registerCleanup('restore_narrowest', () => observer.send({ cmd: 'set_setting', key: 'narrowest_tile_cols', value: narrowest }));
    await setNarrowest(66);
    await driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 900 });
  });

  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => {
      const left = observer.desktop(desktop.id);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  await runner.step('build_four_tiles_with_two_on_screen', async () => {
    await firstShell();
    for (let count = 2; count <= 4; count += 1) await split(keys.right);
    for (let on = await shown(); on.length > 2; on = await shown()) {
      await driver.pressKey(keys.fewerTiles);
      await driver.waitFor(tile(on[0]), { gone: true });
    }
    const order = row();
    const on = await shown();
    runner.assert(order.length === 4 && on.length === 2, 'four tiles, two on screen', { order, on });
    runner.assert(sameList(on, order.slice(2)) && current().active_pane_id === order[3], 'the newest tile is active at the end of the row', { order, on });
    await shot('four-tiles-two-on-screen');
  });

  await runner.step('d_dropped_beside_a_chip_shows_a_and_d', async () => {
    const [a, , , d] = row();
    await dropBeside(d, a, RIGHT_HALF);
    runner.assert(sameList(await shown(), [a, d]), 'a and d on screen', { a, d });
    await shot('a-and-d-on-screen');
  });

  await runner.step('every_drop_place_changes_the_screen', async () => {
    for (const tileIs of ['active', 'inactive']) {
      for (const chipAt of [0, 1]) {
        for (const share of [LEFT_HALF, RIGHT_HALF]) {
          const on = await shown();
          const active = current().active_pane_id;
          const moving = tileIs === 'active' ? active : on.find((leaf) => leaf !== active);
          const offscreen = row().filter((leaf) => !on.includes(leaf));
          await dropBeside(moving, offscreen[chipAt], share);
        }
      }
    }
    await shot('after-every-drop-place');
  });

  await runner.step('a_drop_that_keeps_the_row_order_still_shows_the_pair', async () => {
    const order = row();
    const on = await shown();
    const at = order.findIndex((leaf, index) => on.includes(leaf) && order[index + 1] && !on.includes(order[index + 1]));
    runner.assert(at >= 0, 'a tile on screen sits just before a chip', { order, on });
    await dropBeside(order[at], order[at + 1], LEFT_HALF);
    runner.assert(sameList(row(), order), 'the row order is unchanged', { order });
    await shot('unchanged-order-drop');
  });

  await runner.step('a_saved_width_that_keeps_the_pair_apart_gives_way', async () => {
    const [wide, neighbour] = await shown();
    runner.assert(current().active_pane_id === wide, 'the left tile on screen is active', { wide });
    await driver.drag({ key: `divider.row:${wide}` }, { by: [500, 0] });
    await observer.waitFor(() => wantedIn(current(), wide) > 0, `${wide} saved wide`, 20_000);
    await driver.waitFor(tile(neighbour), { gone: true });
    await driver.waitFor(chip(neighbour));
    await shot('saved-width-alone');
    const order = row();
    await dropBeside(wide, neighbour, LEFT_HALF);
    runner.assert(sameList(row(), order), 'the row order is unchanged', { order });
    await observer.waitFor(() => !wantedIn(current(), wide), `${wide} gives up its saved width`, 20_000);
    await shot('saved-width-cleared-pair-shown');
  });

  await runner.step('a_selected_stacked_tile_dropped_on_the_next_chip_shows_beside_it', async () => {
    const [first, second, third, last] = row();
    await dropBeside(last, first, LEFT_HALF);
    await driver.pressKey(keys.focusRight);
    await keyboardOn(first);
    const stacked = await split(keys.below);
    runner.assert(sameList(await shown(), [last, first, stacked]), 'the stack sits just before the chip', { last, first, stacked });
    await dropBeside(stacked, second, LEFT_HALF);
    runner.assert(sameList(row(), [last, first, stacked, second, third]), 'the stacked tile has its own column before the chip', { stacked, second });
    await shot('stacked-tile-beside-chip');
  });

  await runner.finishSuccess({ desktopId: desktop.id, row: row() });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
