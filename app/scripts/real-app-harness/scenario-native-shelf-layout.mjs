#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const TILES = 7;
const NARROWEST_COLS = '66';
const WIDE = { x: 0, y: 0, width: 1600, height: 1000 };
const NARROW = { ...WIDE, width: 1000 };

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SHELF-LAYOUT', tier: 'tier1-local-shell', prefix: 'native-shelf-layout',
  metadata: { focus: 'A row of shells wider than the native window: a window sweep returns to the same screen, the row keys slide the screen to each end with the sidebar open and hidden, the width keys and the width control step the tile count, and past the row end the right key goes to the next desktop' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const keys = process.platform === 'darwin'
  ? { Left: 'mod+alt+left', Right: 'mod+alt+right', fewer: 'mod+alt+-', more: 'mod+alt+=', sidebar: 'mod+b' }
  : { Left: 'mod+shift+left', Right: 'mod+shift+right', fewer: 'mod+alt+-', more: 'mod+alt+=', sidebar: 'mod+alt+b' };

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

async function setNarrowest(value) {
  observer.send({ cmd: 'set_setting', key: 'narrowest_tile_cols', value });
  await observer.waitFor(() => observer.getSetting('narrowest_tile_cols') === value, `narrowest_tile_cols is ${value}`);
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    queueDaemonSettingRestore(observer, 'narrowest_tile_cols');
    await setNarrowest(NARROWEST_COLS);
    await driver.setWindowBounds(WIDE);
  });

  let desktop;
  const made = [];
  const madeDesktop = async (name) => {
    const created = name
      ? (await observer.profileCommand('desktop_create', { profile_id: observer.profileId, name, shortcut_slot: 0 })).desktops[0]
      : await observer.createDesktop();
    made.push(created.id);
    return observer.waitFor(() => observer.desktop(created.id), `${created.id} in the arrangement`);
  };
  runner.registerCleanup('close_desktops', async () => {
    for (const id of made) {
      for (const pane of observer.desktop(id)?.panes ?? []) observer.unregisterSession(pane.session_id);
      await observer.waitFor(() => !observer.desktop(id) || observer.desktop(id).panes.length === 0, `no terminals left on ${id}`, 20_000);
      const left = observer.desktop(id);
      if (left) await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    }
  });

  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    desktop = await madeDesktop();
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const row = () => leavesOf(observer.desktop(desktop.id));
  const active = () => observer.desktop(desktop.id).active_pane_id;
  const keyboard = async () => (await driver.state('window')).keyboard?.pane ?? null;

  const landOn = async (leaf, why) => {
    await observer.waitFor(() => active() === leaf, why);
    await driver.waitFor({ key: `tile.${leaf}` });
    const holder = await keyboard();
    runner.assert(holder === leaf, `${why}: the keyboard goes with it`, { leaf, holder });
  };

  const assertNeighbours = async (label) => {
    const shown = await screen();
    const order = row();
    const start = order.indexOf(shown[0]);
    runner.assert(shown.length >= 1 && shown.length < order.length, `${label}: the screen shows part of the row`, { shown, order });
    runner.assert(shown.every((id, index) => order[start + index] === id), `${label}: the screen is neighbours in row order`, { shown, order });
    runner.assert(shown.includes(active()), `${label}: the active tile is on screen`, { shown, active: active() });
    return shown;
  };

  const walkTo = async (leaf) => {
    while (active() !== leaf) {
      const order = row();
      const step = order.indexOf(active()) < order.indexOf(leaf) ? 1 : -1;
      const next = order[order.indexOf(active()) + step];
      await driver.pressKey(step > 0 ? keys.Right : keys.Left);
      await landOn(next, `the row key shows ${next}`);
    }
  };

  const walkTheRow = async (label) => {
    const order = row();
    await walkTo(order[0]);
    await assertNeighbours(`${label} at the row start`);
    for (const leaf of order.slice(1)) {
      await driver.pressKey(keys.Right);
      await landOn(leaf, `${label}: the right key shows ${leaf}`);
      await assertNeighbours(`${label} walking right`);
    }
    const end = await assertNeighbours(`${label} at the row end`);
    runner.assert(end.at(-1) === order.at(-1), `${label}: the right key slides to the row's end`, { end, order });
    await driver.screenshot(path.join(runner.runDir, `${label}-row-end.png`));
    for (const leaf of order.slice(0, -1).reverse()) {
      await driver.pressKey(keys.Left);
      await landOn(leaf, `${label}: the left key shows ${leaf}`);
      await assertNeighbours(`${label} walking left`);
    }
    const start = await assertNeighbours(`${label} back at the row start`);
    runner.assert(start[0] === order[0], `${label}: the left key slides back to the row's start`, { start, order });
  };

  const stepTheWidth = async (label, fewer, more) => {
    const shown = (await screen()).length;
    runner.assert(shown >= 2, `${label}: two tiles fit, so one fewer can show`, { shown });
    await fewer();
    const narrower = await driver.waitFor({ key: 'shelf.width.label' });
    runner.assert(narrower.name === `${shown - 1} tile${shown - 1 === 1 ? '' : 's'}`, `${label}: the label names the tile count`, { narrower, shown });
    const steps = keyedNodes(await driver.tree()).find((node) => node.key === 'shelf.width.label');
    const words = JSON.stringify(steps);
    runner.assert(words.includes(`[${shown - 1}]`) && words.includes(String(shown)), `${label}: the label lists the tile counts and marks the one reached`, { words });
    runner.assert((await screen()).length === shown - 1, `${label}: one fewer tile shows`, { shown });
    await driver.screenshot(path.join(runner.runDir, `${label}-width-label.png`));
    await more();
    await driver.waitFor({ key: 'shelf.width.label' }, { gone: true });
    runner.assert((await screen()).length === shown, `${label}: more tiles returns to the same tile count`, { shown });
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
  });

  await runner.step('a_window_sweep_returns_to_the_same_screen', async () => {
    const order = row();
    await driver.click({ key: `tile.${active()}.header` });
    await landOn(active(), 'a header press gives the active tile the keyboard');
    await walkTo(order[3]);
    const wide = await assertNeighbours('wide window');
    const leaving = wide.find((id) => id !== order[3]);
    await driver.setWindowBounds(NARROW);
    await driver.waitFor({ key: `tile.${leaving}` }, { gone: true });
    await assertNeighbours('narrow window');
    await driver.setWindowBounds(WIDE);
    await driver.waitFor({ key: `tile.${leaving}` });
    const back = await screen();
    runner.assert(back.join() === wide.join(), 'a window that returns to its width shows the same screen', { wide, back });
  });

  await runner.step('sidebar_open', async () => {
    await driver.waitFor({ key: 'sidebar' });
    await walkTheRow('sidebar-open');
    await stepTheWidth('sidebar-open keys', () => driver.pressKey(keys.fewer), () => driver.pressKey(keys.more));
    const pressWidthButton = async (button) => {
      await driver.hover({ key: 'shelf.live' });
      await driver.waitFor({ key: 'shelf.width' });
      await driver.click({ key: `shelf.width.${button}` });
    };
    await stepTheWidth('sidebar-open control', () => pressWidthButton('fewer'), () => pressWidthButton('more'));
    const [shelf, sidebar] = await Promise.all([driver.waitFor({ key: 'shelf' }), driver.waitFor({ key: 'sidebar' })]);
    runner.assert(sidebar.rect[1] >= shelf.rect[1] + shelf.rect[3], 'the sidebar starts below the shelf row', { shelf: shelf.rect, sidebar: sidebar.rect });
    const tile = await driver.waitFor({ key: `tile.${active()}` });
    runner.assert(tile.rect[0] >= sidebar.rect[0] + sidebar.rect[2], 'the tiles sit beside the sidebar', { tile: tile.rect, sidebar: sidebar.rect });
  });

  await runner.step('sidebar_hidden', async () => {
    const before = (await screen()).length;
    await driver.pressKey(keys.sidebar);
    await driver.waitFor({ key: 'sidebar' }, { gone: true });
    runner.assert((await driver.state('window')).sidebar_hidden === true, 'the sidebar key hides the sidebar');
    await driver.waitFor({ key: 'shelf' });
    runner.log('sidebar hidden', { before, after: await screen() });
    await walkTheRow('sidebar-hidden');
    await stepTheWidth('sidebar-hidden keys', () => driver.pressKey(keys.fewer), () => driver.pressKey(keys.more));
    await driver.pressKey(keys.sidebar);
    await driver.waitFor({ key: 'sidebar' });
  });

  await runner.step('past_the_row_end_the_right_key_goes_to_the_next_desktop', async () => {
    const next = await madeDesktop('Empty neighbour');
    const byOrder = () => [...observer.desktops].sort((a, b) => (a.order_key < b.order_key ? -1 : a.order_key > b.order_key ? 1 : 0)).map((entry) => entry.id);
    const others = byOrder().filter((id) => id !== next.id);
    others.splice(others.indexOf(desktop.id) + 1, 0, next.id);
    await observer.profileCommand('desktop_set_order', { profile_id: observer.profileId, desktop_ids: others });
    await observer.waitFor(() => byOrder()[byOrder().indexOf(desktop.id) + 1] === next.id, 'the empty desktop right after the row');
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await observer.waitFor(() => observer.currentDesktopId() === desktop.id, 'the sidebar shows the row again');
    await driver.click({ key: `tile.${active()}.header` });
    await landOn(active(), 'a header press gives the active tile the keyboard');
    await walkTo(row().at(-1));
    await driver.pressKey(keys.Right);
    await observer.waitFor(() => observer.currentDesktopId() === next.id, 'the right key past the row end goes to the next desktop');
    await driver.waitFor({ key: 'launcher.path.field' });
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await observer.waitFor(() => observer.currentDesktopId() === desktop.id, 'back on the row');
    await driver.waitFor({ key: `tile.${row().at(-1)}` });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, row: row(), frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
