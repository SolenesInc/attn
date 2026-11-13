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
  scenarioId: 'NATIVE-DESKTOP-TILES', tier: 'tier1-local-shell', prefix: 'native-desktop-tiles',
  metadata: { focus: 'Two shells side by side in the native app: a divider drag saves their widths, its zoom, focus and history keys widen the active tile and give its width back, show it alone and back, and return to the tile shown before, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

function leafOf(desktop, leafId) {
  const walk = (node) => {
    if (!node) return null;
    if (node.pane_id === leafId) return node;
    for (const child of node.children ?? []) {
      const found = walk(child);
      if (found) return found;
    }
    return null;
  };
  return walk(JSON.parse(desktop.tree_json || 'null'));
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
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.setCurrentDesktop(before));
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
    await driver.waitFor({ key: `tile.${result.pane_id}` });
    return { id, pane: result.pane_id };
  };

  let left;
  let right;
  await runner.step('two_shells_side_by_side', async () => {
    left = await spawn('left', { desktop_id: desktop.id });
    right = await spawn('right', { desktop_id: desktop.id, direction: 'vertical', anchor_pane_id: left.pane });
    const shown = await observer.waitForDesktopOf(right.id, (entry) => entry.panes.length === 2, 'both shells on the desktop');
    runner.assert(shown.id === desktop.id, 'both shells share the new desktop', shown);
  });

  await runner.step('a_divider_drag_saves_both_widths', async () => {
    await driver.drag({ key: `divider.row:${left.pane}` }, { by: [90, 0] });
    const saved = await observer.waitForDesktopOf(left.id, (entry) => leafOf(entry, left.pane)?.wanted_cols && leafOf(entry, right.pane)?.wanted_cols, 'the dragged widths reach the daemon');
    runner.assert(leafOf(saved, left.pane).wanted_cols > leafOf(saved, right.pane).wanted_cols, 'the left tile took the width the drag gave it', saved.tree_json);
  });

  const width = async (pane) => (await driver.waitFor({ key: `tile.${pane}` })).rect[2];
  const keys = process.platform === 'darwin'
    ? { zoom: 'mod+shift+z', focus: 'mod+shift+enter', back: 'mod+[', forward: 'mod+]' }
    : { zoom: 'mod+alt+z', focus: 'mod+alt+enter', back: 'mod+shift+[', forward: 'mod+shift+]' };

  await runner.step('zoom_widens_the_active_tile_until_pressed_again', async () => {
    await driver.click({ key: `tile.${right.pane}.header` });
    await observer.waitForDesktopOf(right.id, (entry) => entry.active_pane_id === right.pane, 'the right tile is active');
    const before = await width(right.pane);
    await driver.pressKey(keys.zoom);
    const [zoomed, other] = [await width(right.pane), await width(left.pane)];
    runner.assert(zoomed > 2 * other, 'zoom widens the active tile past twice its neighbour', { zoomed, other });
    await driver.pressKey(keys.zoom);
    const after = await width(right.pane);
    runner.assert(Math.abs(after - before) < 1, 'pressing zoom again gives the tile back its width', { before, after });
  });

  await runner.step('focus_shows_the_tile_alone_and_back', async () => {
    await driver.pressKey(keys.focus);
    await driver.waitFor({ key: `tile.${left.pane}` }, { gone: true });
    await driver.pressKey(keys.focus);
    await driver.waitFor({ key: `tile.${left.pane}` });
  });

  await runner.step('history_returns_to_the_tile_shown_before', async () => {
    await driver.click({ key: `tile.${left.pane}.header` });
    await observer.waitForDesktopOf(left.id, (entry) => entry.active_pane_id === left.pane, 'the left tile is active');
    await driver.pressKey(keys.back);
    await observer.waitForDesktopOf(right.id, (entry) => entry.active_pane_id === right.pane, 'history goes back to the right tile');
    await driver.pressKey(keys.forward);
    await observer.waitForDesktopOf(left.id, (entry) => entry.active_pane_id === left.pane, 'and forward to the left one');
  });

  const screenshot = path.join(runner.runDir, 'native-desktop-tiles.png');
  await runner.step('capture_window', () => driver.screenshot(screenshot));

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, left, right, screenshot, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
