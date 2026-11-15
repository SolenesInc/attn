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
  scenarioId: 'NATIVE-TERMINAL-MD-LINK', tier: 'tier1-local-shell', prefix: 'native-terminal-md-link',
  metadata: { focus: 'A markdown path Cmd-clicked in a native terminal docks a tile bound to the terminal\'s session, a second path docks a second tile, and the first path again reuses its tile, while a plain click opens nothing' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

function markdownTiles(desktop) {
  const tiles = [];
  const walk = (node) => {
    if (!node) return;
    if (node.tile_kind === 'markdown') tiles.push(node);
    for (const child of node.children ?? []) walk(child);
  };
  walk(JSON.parse(desktop?.tree_json || 'null'));
  return tiles;
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_three_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1900, height: 1000 }));

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
  const cwd = path.join(runner.sessionDir, 'docs');
  fs.mkdirSync(cwd, { recursive: true });
  fs.writeFileSync(path.join(cwd, 'alpha.md'), '# Alpha Doc\n\nHello from **alpha**.\n');
  fs.writeFileSync(path.join(cwd, 'beta.md'), '# Beta Doc\n\nHello from *beta*.\n');
  let runtimeId;
  await runner.step('spawn_shell', async () => {
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'docs', cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
    const desktops = await driver.state('desktops');
    const pane = desktops.flatMap((d) => d.panes ?? []).find((p) => p.session_id === id);
    runner.assert(pane?.runtime_id, 'the desktop holds the shell with a terminal', desktops);
    runtimeId = pane.runtime_id;
    await driver.waitFor({ key: `terminal.${runtimeId}` });
  });

  const terminal = { key: `terminal.${runtimeId}` };
  const screen = (includes) => driver.state(`terminal:${runtimeId}`, { includes, timeoutMs: 15_000 });
  const tiles = () => markdownTiles(observer.desktop(desktop.id));

  await runner.step('echo_the_paths', async () => {
    await driver.click(terminal);
    await driver.typeInto(null, "clear; echo ./alpha''.md; echo ./beta''.md");
    await driver.pressKey('Enter');
    await screen('./beta.md');
  });

  let grid;
  await runner.step('measure_the_grid_while_the_terminal_holds_the_caret', async () => {
    const slot = await driver.waitFor(terminal);
    const caret = await driver.waitFor({ key: `terminal.${runtimeId}.caret` });
    const { cursor } = await driver.state(`terminal-view:${runtimeId}`);
    const [cx, cy, cw, ch] = caret.rect;
    grid = { dx: cx - cursor[0] * cw - slot.rect[0], dy: cy - cursor[1] * ch - slot.rect[1], cw, ch };
  });

  const pointAt = async (relPath) => {
    const rows = await screen(relPath);
    const row = rows.findLastIndex((text) => text.trim() === relPath);
    runner.assert(row >= 0, `a row reads ${relPath} alone`, rows);
    const { rect: [, , sw, sh] } = await driver.waitFor(terminal);
    const x = grid.dx + (rows[row].indexOf(relPath) + relPath.length / 2) * grid.cw;
    const y = grid.dy + (row + 0.5) * grid.ch;
    return [x / sw, y / sh];
  };
  const modClick = async (relPath) => {
    const at = await pointAt(relPath);
    await driver.holdKeys('mod');
    await driver.click(terminal, { at });
    await driver.holdKeys('');
  };
  const docked = (count, description) => observer.waitForDesktopOf(id, (entry) => markdownTiles(entry).length === count && entry, description);

  let alpha;
  await runner.step('a_plain_click_opens_nothing_and_a_mod_click_docks_alpha', async () => {
    await driver.click(terminal, { at: await pointAt('./beta.md') });
    await modClick('./alpha.md');
    const entry = await docked(1, 'one markdown tile docks');
    [alpha] = markdownTiles(entry);
    runner.assert(alpha.tile_params.endsWith('/alpha.md'), 'the tile shows alpha.md, not the plainly clicked beta.md', alpha);
    runner.assert(alpha.tile_session_id === id, 'the tile is bound to the terminal\'s session', alpha);
    await driver.waitFor({ key: 'alpha-doc' });
  });

  let beta;
  await runner.step('a_mod_click_on_beta_docks_a_second_tile', async () => {
    await modClick('./beta.md');
    const entry = await docked(2, 'a second markdown tile docks');
    beta = markdownTiles(entry).find((t) => t.tile_id !== alpha.tile_id);
    runner.assert(beta.tile_params.endsWith('/beta.md') && beta.tile_session_id === id, 'the second tile shows beta.md for the same session', beta);
  });

  await runner.step('a_mod_click_on_alpha_again_reuses_its_tile', async () => {
    await modClick('./alpha.md');
    const entry = await observer.waitForDesktopOf(id, (e) => e.active_pane_id === alpha.tile_id && e, 'the alpha tile becomes active');
    const now = markdownTiles(entry).map((t) => t.tile_id).sort();
    runner.assert(JSON.stringify(now) === JSON.stringify([alpha.tile_id, beta.tile_id].sort()), 'no third tile docks', now);
    await driver.screenshot(path.join(runner.runDir, 'native-md-link-tiles.png'));
  });

  await runner.step('leave_no_tile_behind', async () => {
    for (const tile of tiles()) await observer.profileCommand('desktop_remove_leaf', { desktop_id: desktop.id, leaf_id: tile.tile_id });
    await driver.waitFor({ key: `tile.${alpha.tile_id}` }, { gone: true });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, sessionId: id, alpha: alpha.tile_id, beta: beta.tile_id, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
