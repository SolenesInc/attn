#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { PNG } from 'pngjs';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

/** Colours no theme uses, which each shell paints a block of and its miniature shows. */
const PAINT = { one: [230, 30, 120], two: [20, 200, 90], three: [40, 110, 250], four: [240, 160, 20], never: [150, 40, 230] };
/** How far a channel may stand from the colour a shell painted. */
const NEAR = 12;
/** The least pixels of its colour a miniature shows; a painted block showed 15756 in a 1600 by 1000 window. */
const SEEN = 1000;

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-ROW-OVERVIEW', tier: 'tier1-local-shell', prefix: 'native-row-overview',
  metadata: { focus: 'In the native app the overview opens over a row with a stack and a browser tile, draws each terminal in its miniature, walks, previews another desktop and comes back, draws a terminal never shown from its snapshot and one with no snapshot as a card, and lands; no terminal is resized, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));
runner.registerFailureEvidence('native_tree', async () => runner.writeJson('failure-tree.json', await driver.tree()));

const overview = process.platform === 'darwin' ? 'mod+g' : 'mod+shift+g';
const OVERVIEW = { key: 'overview' };
const miniature = (leaf) => ({ key: `overview.miniature.${leaf}` });
const picture = (leaf) => ({ key: `overview.miniature.${leaf}.picture` });
const strip = (desktop) => ({ key: `overview.strip.${desktop.id}` });
const entry = (leaf) => ({ key: `shelf.entry.${leaf}` });

function nodeIn(node, key) {
  if (node?.key === key) return node;
  for (const child of node?.children ?? []) {
    const found = nodeIn(child, key);
    if (found) return found;
  }
  return null;
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_two_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  const before = observer.currentDesktopId();
  if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
  const makeDesktop = async () => {
    const desktop = await observer.createDesktop();
    runner.registerCleanup(`close_desktop_${desktop.id}`, () => observer.closeDesktop(desktop.id));
    return desktop;
  };

  const spawn = async (label, placement) => {
    const id = crypto.randomUUID();
    const cwd = path.join(runner.sessionDir, label);
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `${label} spawned`);
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement, label, cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, `spawn_session starts ${label}`, result);
    runner.registerCleanup(`unregister_${label}`, async () => {
      observer.unregisterSession(id);
      await observer.waitFor(() => !observer.desktopOf(id), `${label} leaves its desktop`);
    });
    return { id, label, pane: result.pane_id, runtime: (await observer.waitForTerminalPane(id)).runtime_id };
  };
  const snapshotOf = async (shell) => {
    const answer = observer.waitForMessage((message) => message.event === 'get_screen_snapshot_result' && message.id === shell.runtime && message, `the screen of ${shell.label}`);
    observer.send({ cmd: 'get_screen_snapshot', id: shell.runtime });
    return answer;
  };
  const paint = async (shell, [r, g, b]) => {
    const script = path.join(runner.sessionDir, shell.label, 'paint.sh');
    const block = Array.from({ length: 8 }, () => `\\033[48;2;${r};${g};${b}m%48s\\033[0m\\n`).join('');
    const lines = Array.from({ length: 6 }, (_, i) => `${shell.label}: the quick brown fox jumps over line ${i + 1}\\n`).join('');
    fs.writeFileSync(script, `clear; printf '${block}${lines}'; touch painted; exec cat\n`);
    observer.send({ cmd: 'pty_input', id: shell.runtime, data: 'sh paint.sh\r', source: 'automation' });
    await observer.waitFor(() => fs.existsSync(path.join(runner.sessionDir, shell.label, 'painted')) || null, `${shell.label} paints`, 15_000);
  };
  const sizes = async (shells) => {
    const read = [];
    for (const shell of shells) {
      const { cols, rows } = await snapshotOf(shell);
      read.push(`${shell.label} ${cols}x${rows}`);
    }
    return read;
  };

  const shot = async (name) => {
    const file = path.join(runner.runDir, `${name}.png`);
    await driver.screenshot(file);
    const image = PNG.sync.read(fs.readFileSync(file));
    const window = await driver.find({ key: 'window' });
    return { image, scale: image.width / window.rect[2] };
  };
  /** How many pixels of `rgb` the window shows inside `rect`. */
  const pixelsOf = ({ image, scale }, rect, rgb) => {
    const [left, top, width, height] = rect.map((n) => Math.round(n * scale));
    let seen = 0;
    for (let y = Math.max(0, top); y < Math.min(image.height, top + height); y += 1) {
      for (let x = Math.max(0, left); x < Math.min(image.width, left + width); x += 1) {
        const at = (y * image.width + x) * 4;
        if (rgb.every((want, channel) => Math.abs(image.data[at + channel] - want) <= NEAR)) seen += 1;
      }
    }
    return seen;
  };
  const shown = async (name, painted) => {
    for (const { shell } of painted) await driver.waitFor(picture(shell.pane));
    await driver.atRest();
    const window = await shot(name);
    const seen = [];
    for (const { shell, rgb } of painted) {
      const pixels = pixelsOf(window, (await driver.waitFor(picture(shell.pane))).rect, rgb);
      seen.push({ leaf: shell.label, pixels, right: pixels >= SEEN });
    }
    runner.assert(seen.every((s) => s.right), `${name}: each miniature shows its terminal's screen`, seen);
    return seen;
  };
  const heard = async (target) => {
    await driver.waitFor(target);
    return nodeIn(await driver.tree(), target.key)?.value ?? '';
  };
  const highlighted = async (leaf) => {
    const value = await heard(miniature(leaf));
    runner.assert(value.includes('highlighted'), 'the highlight stands on the tile', { leaf, value });
  };

  let here;
  let there;
  let shells;
  let stacked;
  let never;
  let stopped;
  const browser = `tile-${crypto.randomUUID()}`;
  await runner.step('a_row_with_a_stack_and_a_browser_tile_and_a_desktop_never_shown', async () => {
    here = await makeDesktop();
    there = await makeDesktop();
    await driver.click({ key: `sidebar.desktop.${here.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
    const one = await spawn('row-one', { desktop_id: here.id });
    const two = await spawn('row-two', { desktop_id: here.id, direction: 'vertical', anchor_pane_id: one.pane });
    const three = await spawn('row-three', { desktop_id: here.id, direction: 'vertical', anchor_pane_id: two.pane });
    const four = await spawn('row-four', { desktop_id: here.id, direction: 'horizontal', anchor_pane_id: three.pane });
    const pages = http.createServer((_, answer) => answer.setHeader('content-type', 'text/html').end('<title>Overview page</title>a page'));
    runner.registerCleanup('close_pages', () => new Promise((done) => { pages.close(done); pages.closeAllConnections(); }));
    await new Promise((listening) => pages.listen(0, '127.0.0.1', listening));
    await observer.profileCommand('desktop_dock_tile', { desktop_id: here.id, anchor_id: three.pane, edge: 'right', tile_id: browser, tile_kind: 'browser', tile_params: `http://127.0.0.1:${pages.address().port}/` });
    shells = [one, two, three, four];
    stacked = { above: three, below: four };
    never = await spawn('never-shown', { desktop_id: there.id });
    stopped = await spawn('never-shown-stopped', { desktop_id: there.id, direction: 'vertical', anchor_pane_id: never.pane });

    const paints = [PAINT.one, PAINT.two, PAINT.three, PAINT.four, PAINT.never];
    for (const [i, shell] of [...shells, never].entries()) await paint(shell, paints[i]);
    observer.send({ cmd: 'pty_input', id: stopped.runtime, data: 'exit 1\r', source: 'automation' });
    await observer.waitFor(() => observer.getSession(stopped.id)?.terminal_exit || null, 'the shell stops', 15_000);
    await observer.profileCommand('desktop_show_leaf', { desktop_id: here.id, leaf_id: one.pane });
    await driver.waitFor({ key: `tile.${one.pane}` });
    await driver.waitFor({ key: `tile.${four.pane}` }, { gone: true });
  });

  const [one, two, three, four] = shells;
  const all = [...shells, never];
  const sizesBefore = await sizes(all);
  const treeBefore = observer.desktop(here.id).tree_json;
  const painted = [
    { shell: one, rgb: PAINT.one },
    { shell: two, rgb: PAINT.two },
    { shell: three, rgb: PAINT.three },
    { shell: four, rgb: PAINT.four },
  ];

  await runner.step('the_overview_shows_each_terminal_of_the_row', async () => {
    await driver.pressKey(overview);
    await driver.waitFor(OVERVIEW);
    await driver.waitFor(miniature(browser));
    await highlighted(one.pane);
    runner.log('miniatures', await shown('row', painted));
  });

  await runner.step('the_highlight_walks_the_row_and_its_stack_and_the_shelf_follows', async () => {
    const walk = [['Right', two.pane], ['Right', stacked.below.pane], ['Up', stacked.above.pane], ['Right', browser], ['Left', stacked.above.pane], ['Down', stacked.below.pane]];
    for (const [key, leaf] of walk) {
      await driver.pressKey(key);
      await highlighted(leaf);
      await shot(`walk-${key}-${leaf}`);
    }
    const current = await heard(entry(stacked.above.pane));
    runner.assert(current.includes('current'), 'the stack off screen is the shelf\'s current entry', { current });
    runner.assert(observer.desktop(here.id).active_pane_id === one.pane && observer.desktop(here.id).tree_json === treeBefore, 'the walk moves nothing', observer.describeArrangement());
  });

  await runner.step('another_desktop_previews_a_terminal_never_shown_and_one_with_no_snapshot', async () => {
    await driver.click(strip(there));
    await driver.waitFor(miniature(never.pane));
    const snapshot = await snapshotOf(stopped);
    runner.assert(!snapshot.success || !snapshot.screen_snapshot, 'the stopped terminal has no snapshot', { ...snapshot, screen_snapshot: undefined });
    runner.log('the desktop never shown', await shown('preview', [{ shell: never, rgb: PAINT.never }]));
    await driver.waitFor({ key: `${miniature(stopped.pane).key}.card` });
    runner.assert(observer.currentDesktopId() === here.id, 'a preview switches nothing', observer.describeArrangement());
    await driver.click(strip(here));
    await driver.waitFor(miniature(one.pane));
    await driver.pressKey('Escape');
    await driver.waitFor(OVERVIEW, { gone: true });
    runner.assert(observer.desktop(here.id).active_pane_id === one.pane && observer.desktop(here.id).tree_json === treeBefore, 'leaving shows nothing new', observer.describeArrangement());
  });

  await runner.step('no_terminal_changed_size', async () => {
    const after = await sizes(all);
    runner.assert(after.join() === sizesBefore.join(), 'every terminal keeps its size', { sizesBefore, after });
    runner.log('sizes', after);
  });

  await runner.step('a_press_on_a_miniature_lands_on_its_tile', async () => {
    await driver.pressKey(overview);
    await driver.click(miniature(stacked.below.pane));
    await driver.waitFor(OVERVIEW, { gone: true });
    await driver.waitFor({ key: `tile.${stacked.below.pane}` });
    await observer.waitFor(() => observer.desktop(here.id)?.active_pane_id === stacked.below.pane, 'the pressed tile is active');
    await shot('landed');
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ here: here.id, there: there.id, sizes: sizesBefore });
} catch (error) {
  await runner.finishFailure(error, { arrangement: observer.describeArrangement() });
  process.exitCode = 1;
}
