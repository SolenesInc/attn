#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const TILES = 5;
const BAND_PX = 32;
const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-TITLE-BAR', tier: 'tier1-local-shell', prefix: 'native-title-bar',
  metadata: { focus: 'In the native app clicks reach every control of the title bar, a chip in it opens its peek, a tile dropped on a chip lands beside it, and tiles start below the band with the sidebar open or hidden, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const BAND = { key: 'titlebar' };
const PEEK = { key: 'shelf.peek' };
const chip = (leaf) => ({ key: `shelf.chip.${leaf}` });
const header = (leaf) => ({ key: `tile.${leaf}.header` });
const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));

function leavesOf(node) {
  if (!node) return [];
  if (node.type === 'split') return node.children.flatMap(leavesOf);
  return [node.pane_id ?? node.tile_id];
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_two_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  let desktop;
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

  const spawn = async (label, placement) => {
    const id = crypto.randomUUID();
    const cwd = path.join(runner.sessionDir, label);
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `${label} spawned`);
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement, label, cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, `spawn_session starts ${label}`, result);
    runner.registerCleanup(`unregister_${label}`, () => observer.unregisterSession(id));
    return { id, label, pane: result.pane_id };
  };

  const current = () => observer.desktop(desktop.id);
  const readRow = () => leavesOf(JSON.parse(current().tree_json || 'null'));
  const active = () => current().active_pane_id;
  const shownLeaves = async () => {
    const shown = [];
    for (const leaf of readRow()) if (await driver.find({ key: `tile.${leaf}` })) shown.push(leaf);
    return shown;
  };
  const assertTilesStartBelowTheBand = async (name) => {
    const band = (await driver.waitFor(BAND)).rect;
    const tile = (await driver.waitFor({ key: `tile.${(await shownLeaves())[0]}` })).rect;
    runner.log(`${name} geometry`, { band, tile });
    runner.assert(band[1] === 0 && band[3] === BAND_PX, `${name}: the band is ${BAND_PX}px at the window's top`, { band });
    runner.assert(tile[1] === BAND_PX, `${name}: the tile's top is ${BAND_PX}`, { tile });
  };

  const shells = [];
  await runner.step('a_row_of_five_shells', async () => {
    let anchor;
    for (let n = 1; n <= TILES; n += 1) {
      const shell = await spawn(`band-${n}`, anchor ? { desktop_id: desktop.id, direction: 'vertical', anchor_pane_id: anchor } : { desktop_id: desktop.id });
      shells.push(shell);
      anchor = shell.pane;
    }
    await observer.waitForDesktopOf(shells[0].id, (entry) => entry.panes.length === TILES, 'five shells on the desktop');
    await driver.waitFor({ key: 'shelf' });
    runner.assert(readRow().join() === shells.map((shell) => shell.pane).join(), 'the shells sit in the row in the order they came', { row: readRow() });
    await assertTilesStartBelowTheBand('sidebar open');
    await shot('band-sidebar-open');
  });

  await runner.step('native_clicks_reach_every_control', async () => {
    const press = async (key, name) => {
      const receipt = await driver.click({ key });
      runner.assert(receipt.matched, `${name}: the click lands on the control`, receipt);
    };

    await press('titlebar.new', 'new agent');
    await driver.waitFor({ key: 'new-session' });
    await shot('control-new-agent');
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'new-session' }, { gone: true });

    await press('titlebar.commands', 'commands');
    const palette = await driver.waitFor({ key: 'palette.dialog' });
    runner.assert(palette.name === 'Commands', 'the commands control opens the command palette', palette);
    await shot('control-commands');
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'palette' }, { gone: true });

    runner.registerCleanup('restore_desktop_flow', () => observer.send({ cmd: 'set_setting', key: 'queue_mode_enabled', value: 'false' }));
    await press('titlebar.flow', 'flow');
    await driver.waitFor({ role: 'button', name: 'Switch to desktop flow' });
    await observer.waitFor(() => observer.getSetting('queue_mode_enabled') === 'true' || null, 'queue flow is on');
    await shot('control-flow-queue');
    await press('titlebar.flow', 'flow back');
    await driver.waitFor({ role: 'button', name: 'Switch to queue flow' });
    await observer.waitFor(() => observer.getSetting('queue_mode_enabled') === 'false' || null, 'desktop flow is back');

    await press('titlebar.sidebar', 'sidebar');
    await driver.waitFor({ role: 'button', name: 'Show sidebar' });
    runner.assert((await driver.state('window')).sidebar_hidden === true, 'the sidebar hides');
    await assertTilesStartBelowTheBand('sidebar hidden');
    await shot('control-sidebar-hidden');
    await press('titlebar.sidebar', 'sidebar back');
    await driver.waitFor({ role: 'button', name: 'Collapse sidebar' });
    runner.assert((await driver.state('window')).sidebar_hidden === false, 'the sidebar shows again');
    await assertTilesStartBelowTheBand('sidebar back');
  });

  await runner.step('a_chip_opens_its_peek', async () => {
    const shown = await shownLeaves();
    const target = shells.find((shell) => !shown.includes(shell.pane));
    const { rect: band } = await driver.waitFor(BAND);
    const { rect: at } = await driver.waitFor(chip(target.pane));
    runner.assert(at[1] >= band[1] && at[1] + at[3] <= band[1] + band[3], 'the chip sits in the band', { band, at });
    await driver.hover(chip(target.pane));
    const peek = await driver.waitFor(PEEK);
    runner.assert(peek.name === target.label, 'the chip peeks at its tile', { peek, target });
    await shot('peek-from-band-chip');
    await driver.pressKey('Escape');
    await driver.waitFor(PEEK, { gone: true });
    await driver.hover({ key: `tile.${shown.at(-1)}` });
  });

  await runner.step('a_tile_drops_beside_a_chip', async () => {
    const shown = await shownLeaves();
    const tile = shown.find((leaf) => leaf !== active());
    const anchor = readRow().find((leaf) => !shown.includes(leaf));
    await driver.drag(header(tile), { target: chip(anchor), at: [0.1, 0.5] });
    await observer.waitFor(() => {
      const after = readRow();
      return (active() === tile && after.indexOf(tile) === after.indexOf(anchor) - 1) || null;
    }, `${tile} lands just before ${anchor} and is active`);
    await driver.waitFor({ key: `tile.${tile}` });
    await driver.waitFor({ key: `tile.${anchor}` });
    await shot('tile-beside-the-band-chip');
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, row: readRow(), frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
