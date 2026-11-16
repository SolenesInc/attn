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
  scenarioId: 'NATIVE-NOTEBOOK-LINK-NAV', tier: 'tier1-local-shell', prefix: 'native-notebook-link-nav',
  metadata: { focus: 'In an editor tile, ⌘ or Ctrl with a press follows a link to a sibling note, a note in another folder, a heading in the same note, and a note whose image draws from a folder beside it' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

// 1x1 transparent PNG, embedded so the scenario has no fixture file.
const PIXEL_PNG_BASE64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=';

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
  const cwd = path.join(runner.sessionDir, 'links-ws');
  const nav = `nav-${runner.runId}`;
  await runner.step('a_shell_beside_linked_notes', async () => {
    fs.mkdirSync(path.join(cwd, 'deep'), { recursive: true });
    fs.mkdirSync(path.join(cwd, 'assets'), { recursive: true });
    fs.writeFileSync(path.join(cwd, `${nav}.md`), '# nav probe\n\n[bar](bar.md)\n', 'utf8');
    fs.writeFileSync(path.join(cwd, 'bar.md'), '# bar\n\n[anchor](deep/anchor.md)\n', 'utf8');
    fs.writeFileSync(path.join(cwd, 'deep', 'anchor.md'), '# Anchor\n\n[picture](../image.md)\n', 'utf8');
    fs.writeFileSync(path.join(cwd, 'image.md'), [
      '# image probe',
      '',
      '![pixel](assets/pixel.png)',
      '',
      '[down](#down-below)',
      '',
      ...Array.from({ length: 80 }, (_, i) => `Filler line ${i}.`),
      '',
      '## Down below',
      '',
    ].join('\n'), 'utf8');
    fs.writeFileSync(path.join(cwd, 'assets', 'pixel.png'), Buffer.from(PIXEL_PNG_BASE64, 'base64'));
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'links', cols: 80, rows: 24 });
    runner.assert((await spawned).success, 'spawn_session starts the shell');
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
    const desktops = await driver.state('desktops');
    const pane = desktops.flatMap((d) => d.panes ?? []).find((p) => p.session_id === id);
    await driver.click({ key: `terminal.${pane.runtime_id}` });
  });

  let tile;
  await runner.step('dock_an_editor_tile_and_open_the_first_note', async () => {
    await driver.run('notebook.openTile');
    const entry = await observer.waitForDesktopOf(id, (d) => notebookTiles(d).length === 1 && d, 'an editor tile docks');
    [tile] = notebookTiles(entry);
    await driver.typeInto({ key: 'notebook.finder.field' }, nav);
    await driver.pressKey('enter');
  });

  const part = (name) => ({ key: `notebook.tile.${tile.tile_id}.${name}` });
  const titled = (name) => driver.waitFor({ key: `notebook.tile.${tile.tile_id}.document.title`, name });
  const follow = async (line) => {
    await driver.holdKeys('mod');
    try {
      await driver.click(part(`editor.line.${line}`), { at: [0.01, 0.5], allowMiss: true });
    } finally {
      await driver.holdKeys('');
    }
  };

  await runner.step('a_link_to_a_sibling_note_opens_it', async () => {
    await titled(nav);
    await follow(2);
    await titled('bar');
  });

  await runner.step('a_link_into_a_folder_opens_that_note', async () => {
    await follow(2);
    await titled('anchor');
  });

  await runner.step('a_link_up_a_folder_opens_a_note_whose_image_draws', async () => {
    await follow(2);
    await titled('image');
    await driver.waitFor({ role: 'image', name: 'pixel' });
    await driver.screenshot(path.join(runner.runDir, 'native-notebook-link-nav.png'));
  });

  await runner.step('a_link_to_a_heading_puts_the_caret_on_it', async () => {
    await follow(4);
    await driver.waitFor({ role: 'text', name: '## Down below' });
  });

  await runner.step('leave_no_tile_behind', async () => {
    await observer.profileCommand('desktop_remove_leaf', { desktop_id: desktop.id, leaf_id: tile.tile_id });
    await driver.waitFor({ key: `tile.${tile.tile_id}` }, { gone: true });
    await observer.waitForDesktopOf(id, (d) => notebookTiles(d).length === 0 && d, 'the daemon has the desktop without the tile');
  });

  await runner.finishSuccess({ desktopId: desktop.id, sessionId: id });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
