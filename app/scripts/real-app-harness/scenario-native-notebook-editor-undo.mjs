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
  scenarioId: 'NATIVE-NOTEBOOK-EDITOR-UNDO', tier: 'tier1-local-shell', prefix: 'native-notebook-editor-undo',
  metadata: { focus: 'An editor tile docked beside a shell opens its finder, edits a note that saves itself, and the undo and redo keys reach the editor and the file, not the terminal' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const ORIGINAL = '# Undo probe\n\nThe original line.\n';
const TYPED = 'Typed in attn.';

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

/** Waits for the tile to say its edit is saved, then reads the file. */
async function savedFile(file, description) {
  await driver.waitFor({ role: 'status', name: 'Saved' }).catch((error) => {
    throw new Error(`${description}: ${error.message}`);
  });
  return fs.readFileSync(file, 'utf8');
}

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_two_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1900, height: 1000 }));

  let desktop;
  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', async () => {
      for (;;) {
        const left = observer.desktop(desktop.id);
        if (!left) return;
        const moved = observer.waitForMessage((m) => m.event === 'profile_arrangement_changed' && m, 'the desktop moves on', 10_000);
        try {
          await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
          moved.catch(() => {});
          return;
        } catch (error) {
          if (!/stale_revision/.test(error.message)) throw error;
          await moved;
        }
      }
    });
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, 'undo-ws');
  const basename = `undo-probe-${runner.runId}`;
  const file = path.join(cwd, `${basename}.md`);
  await runner.step('a_shell_beside_a_note', async () => {
    fs.mkdirSync(cwd, { recursive: true });
    fs.writeFileSync(file, ORIGINAL, 'utf8');
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'undo', cols: 80, rows: 24 });
    runner.assert((await spawned).success, 'spawn_session starts the shell');
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
    const desktops = await driver.state('desktops');
    const pane = desktops.flatMap((d) => d.panes ?? []).find((p) => p.session_id === id);
    await driver.click({ key: `terminal.${pane.runtime_id}` });
  });

  let tile;
  await runner.step('dock_an_editor_tile_on_the_shell_folder', async () => {
    await driver.run('notebook.openTile');
    const entry = await observer.waitForDesktopOf(id, (d) => notebookTiles(d).length === 1 && d, 'an editor tile docks');
    [tile] = notebookTiles(entry);
    runner.assert(JSON.parse(tile.tile_params).root === cwd, 'the tile roots at the shell folder', tile);
  });

  const tileKey = (part) => ({ key: `notebook.tile.${tile.tile_id}.${part}` });
  await runner.step('a_fresh_tile_opens_its_finder_and_escape_dismisses_it', async () => {
    await driver.waitFor({ key: 'notebook.finder.field' });
    await driver.pressKey('escape');
    await driver.waitFor({ key: 'notebook.finder.field' }, { gone: true });
  });

  await runner.step('the_finder_opens_the_note', async () => {
    await driver.click(tileKey('surface'), { allowMiss: true });
    await driver.run('file.open');
    await driver.typeInto({ key: 'notebook.finder.field' }, basename);
    await driver.pressKey('enter');
    const title = await driver.waitFor(tileKey('document.title'));
    runner.assert(title.name === basename, 'the note opens in the tile', title);
  });

  await runner.step('typing_saves_itself', async () => {
    await driver.click(tileKey('editor'), { allowMiss: true });
    await driver.pressKey('mod+end');
    await driver.typeInto(tileKey('editor'), TYPED);
    runner.assert((await savedFile(file, 'the edit saves')).includes(TYPED), 'the edit is in the file');
  });

  await runner.step('undo_reaches_the_editor_and_the_file', async () => {
    await driver.pressKey('mod+z');
    runner.assert(!(await savedFile(file, 'the undo saves')).includes(TYPED), 'the undo took the edit out of the file');
  });

  await runner.step('redo_brings_it_back', async () => {
    await driver.pressKey('mod+shift+z');
    runner.assert((await savedFile(file, 'the redo saves')).includes(TYPED), 'the redo put it back');
  });

  await runner.step('an_edit_on_disk_shows_in_the_open_note', async () => {
    const now = fs.readFileSync(file, 'utf8');
    fs.writeFileSync(file, `${now}\nExternal refresh probe.\n`, 'utf8');
    await driver.waitFor({ role: 'text', name: 'External refresh probe.' });
  });

  await runner.step('the_terminal_kept_its_zoom', async () => {
    const entry = observer.desktop(desktop.id);
    runner.assert(!entry?.zoomed_leaf_id, 'undo and redo zoomed no terminal', entry);
    await driver.screenshot(path.join(runner.runDir, 'native-notebook-editor-undo.png'));
  });

  await runner.step('leave_no_tile_behind', async () => {
    await observer.profileCommand('desktop_remove_leaf', { desktop_id: desktop.id, leaf_id: tile.tile_id });
    await driver.waitFor({ key: `tile.${tile.tile_id}` }, { gone: true });
    await observer.waitForDesktopOf(id, (d) => notebookTiles(d).length === 0 && d, 'the daemon has the desktop without the tile');
  });

  await runner.finishSuccess({ desktopId: desktop.id, sessionId: id, file });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
