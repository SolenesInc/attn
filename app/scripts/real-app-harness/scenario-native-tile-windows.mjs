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
  scenarioId: 'NATIVE-TILE-WINDOWS', tier: 'tier1-local-shell', prefix: 'native-tile-windows',
  metadata: { focus: 'A document tile in the native app pops out into a window of its own, the window hides when the tile comes back, and the next pop-out reuses it, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

function leafOf(desktop, leafId) {
  const walk = (node) => {
    if (!node) return null;
    if (node.pane_id === leafId || node.tile_id === leafId) return node;
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

  let shell;
  const tile = `tile-${crypto.randomUUID()}`;
  await runner.step('a_shell_and_a_document_beside_it', async () => {
    const id = crypto.randomUUID();
    const cwd = path.join(runner.sessionDir, 'shell');
    fs.mkdirSync(cwd, { recursive: true });
    const notes = path.join(cwd, 'notes.md');
    fs.writeFileSync(notes, '# Popped out\n\nThis document shows in a window of its own.\n');
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'shell', cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
    shell = { id, pane: result.pane_id };
    await observer.profileCommand('desktop_dock_tile', { desktop_id: desktop.id, anchor_id: shell.pane, edge: 'right', tile_id: tile, tile_kind: 'markdown', tile_params: notes });
    await driver.waitFor({ key: `tile.${tile}` });
  });

  let window;
  await runner.step('pop_out_opens_a_window_of_its_own', async () => {
    await driver.click({ key: `tile.${tile}.popout` });
    const out = await observer.waitForDesktopOf(shell.id, (entry) => leafOf(entry, tile)?.window, 'the daemon keeps the tile popped out');
    runner.assert(leafOf(out, tile).window.width > 0, 'the daemon holds the window frame', out.tree_json);
    await driver.waitFor({ key: `tile.${tile}` }, { gone: true });
    window = await driver.waitForTileWindow({ shown: true });
  });

  await runner.step('bringing_it_back_hides_the_window', async () => {
    await observer.profileCommand('desktop_bring_back_leaf', { desktop_id: desktop.id, leaf_id: tile });
    await driver.waitFor({ key: `tile.${tile}` });
    const hidden = await driver.waitForTileWindow({ shown: false });
    runner.assert(hidden.id === window.id, 'the same window waits hidden', { window, hidden });
  });

  await runner.step('the_next_pop_out_reuses_the_window', async () => {
    await driver.click({ key: `tile.${tile}.popout` });
    await driver.waitFor({ key: `tile.${tile}` }, { gone: true });
    const again = await driver.waitForTileWindow({ shown: true });
    runner.assert(again.id === window.id, 'the same window shows again', { window, again });
    await observer.profileCommand('desktop_bring_back_leaf', { desktop_id: desktop.id, leaf_id: tile });
    await driver.waitFor({ key: `tile.${tile}` });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, shell, tile, window, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
