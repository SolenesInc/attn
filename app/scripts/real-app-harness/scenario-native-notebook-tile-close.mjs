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
  scenarioId: 'NATIVE-NOTEBOOK-TILE-CLOSE', tier: 'tier1-local-shell', prefix: 'native-notebook-tile-close',
  metadata: { focus: 'The close key with an editor tile active closes the tile, not the shell beside it' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

function notebookTiles(desktop) {
  const tiles = [];
  const walk = (node) => {
    if (!node) return;
    if (node.tile_kind === 'notebook') tiles.push(node);
    for (const child of node.children ?? []) walk(child);
  };
  walk(JSON.parse(desktop?.tree_json || 'null'));
  return tiles;
}

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_two_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1900, height: 1000 }));

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

  const id = crypto.randomUUID();
  let pane;
  await runner.step('a_shell', async () => {
    const cwd = path.join(runner.sessionDir, 'close-ws');
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'close', cols: 80, rows: 24 });
    runner.assert((await spawned).success, 'spawn_session starts the shell');
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
    const desktops = await driver.state('desktops');
    pane = desktops.flatMap((d) => d.panes ?? []).find((p) => p.session_id === id);
    await driver.click({ key: `terminal.${pane.runtime_id}` });
  });

  let tile;
  await runner.step('dock_an_editor_tile_and_dismiss_its_finder', async () => {
    await driver.run('notebook.openTile');
    const entry = await observer.waitForDesktopOf(id, (d) => notebookTiles(d).length === 1 && d, 'an editor tile docks');
    [tile] = notebookTiles(entry);
    await driver.waitFor({ key: 'notebook.finder.field' });
    await driver.pressKey('escape');
    await driver.waitFor({ key: 'notebook.finder.field' }, { gone: true });
  });

  await runner.step('the_close_key_closes_the_editor_tile_and_keeps_the_shell', async () => {
    await driver.click({ key: `notebook.tile.${tile.tile_id}.surface` }, { allowMiss: true });
    await driver.run('session.close');
    const entry = await observer.waitForDesktopOf(id, (d) => notebookTiles(d).length === 0 && d, 'the editor tile closes');
    const panes = entry.panes ?? [];
    runner.assert(panes.some((p) => p.pane_id === pane.pane_id), 'the shell beside it stays', panes);
    await driver.waitFor({ key: `tile.${tile.tile_id}` }, { gone: true });
  });

  await runner.finishSuccess({ desktopId: desktop.id, sessionId: id });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
