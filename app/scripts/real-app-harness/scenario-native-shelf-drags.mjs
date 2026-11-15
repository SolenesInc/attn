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
  scenarioId: 'NATIVE-SHELF-DRAGS', tier: 'tier1-local-shell', prefix: 'native-shelf-drags',
  metadata: { focus: 'In the native app: chips onto tiles, between chips and onto sidebar desktops, tiles beside a chip, column dividers past the narrowest and after a window resize, and stack chips onto a tile edge, with real shells and pointer input through the app' },
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

const WINDOW = { x: 0, y: 0, width: 1600, height: 900 };
let desktop;

const leavesOf = (node) => (node.type === 'split' ? node.children.flatMap(leavesOf) : [node.pane_id ?? node.tile_id]);
const columnsOf = (node) => (node.type === 'split' && node.direction === 'vertical' ? node.children.flatMap(columnsOf) : [node]);
const firstLeafOf = (node) => (node.type === 'split' ? firstLeafOf(node.children[0]) : node);
const treeOf = (entry) => JSON.parse(entry.tree_json || 'null');
const current = () => observer.desktop(desktop.id);
const row = () => leavesOf(treeOf(current()));
const columnOf = (entry, leaf) => columnsOf(treeOf(entry)).find((column) => leavesOf(column).includes(leaf));
const wantedIn = (entry, leaf) => firstLeafOf(columnOf(entry, leaf)).wanted_cols ?? 0;
const sameList = (left, right) => left.join() === right.join();
const sessionOf = (leaf) => current().panes.find((pane) => pane.pane_id === leaf)?.session_id;
const tile = (leaf) => ({ key: `tile.${leaf}` });
const chip = (leaf) => ({ key: `shelf.chip.${leaf}` });
const header = (leaf) => ({ key: `tile.${leaf}.header` });
const divider = (leaf) => ({ key: `divider.row:${leaf}` });
const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));
const widthOf = async (leaf) => (await driver.waitFor(tile(leaf))).rect[2];
const heightOf = async (leaf) => (await driver.waitFor(tile(leaf))).rect[3];

async function shown() {
  const found = [];
  for (const leaf of row()) {
    const hit = await driver.find(tile(leaf));
    if (hit) found.push([leaf, hit.rect[0], hit.rect[1]]);
  }
  return found.sort((a, b) => a[1] - b[1] || a[2] - b[2]).map(([leaf]) => leaf);
}

async function chips() {
  const on = await shown();
  return columnsOf(treeOf(current())).map(leavesOf).filter((column) => !column.some((leaf) => on.includes(leaf))).map((column) => column[0]);
}

async function keyboardOn(leaf) {
  await observer.waitForDesktopOf(sessionOf(leaf), (entry) => entry.active_pane_id === leaf, `${leaf} active`);
  const window = await driver.state('window', { includes: leaf, timeoutMs: 5000 }).catch(() => driver.state('window'));
  runner.assert(window.keyboard?.pane === leaf, `typing goes to ${leaf}`, { window, active: current().active_pane_id });
}

async function waitForRow(predicate, description) {
  const entry = await observer.waitFor(() => {
    const now = current();
    return now && predicate(leavesOf(treeOf(now))) && now;
  }, description, 20_000);
  return leavesOf(treeOf(entry));
}

const waitForWanted = (leaf, predicate) => observer.waitFor(() => {
  const now = current();
  return now && columnOf(now, leaf) && predicate(wantedIn(now, leaf)) && now;
}, `the dragged width of ${leaf}`, 20_000).then((entry) => wantedIn(entry, leaf));

async function clearDivider(leaf) {
  await driver.click(divider(leaf));
  await driver.click(divider(leaf));
}

async function clearDraggedWidthsFromThePalette() {
  await driver.pressKey(keys.palette);
  await driver.waitFor({ key: 'palette.dialog' });
  await driver.typeInto(null, 'clear dragged widths');
  await driver.waitFor({ key: 'palette.row.command:shelf-clear-dragged-widths' });
  await driver.pressKey('Enter');
  await driver.waitFor({ key: 'palette' }, { gone: true });
}

async function setNarrowest(cols) {
  await observer.requestResult({ cmd: 'set_setting', key: 'narrowest_tile_cols', value: String(cols) }, 'settings_updated');
  await observer.waitFor(() => observer.getSetting('narrowest_tile_cols') === String(cols), `narrowest ${cols} cols`);
}

const keyboardPane = async () => (await driver.state('window')).keyboard?.pane;

async function focusShown(leaf) {
  if (current().active_pane_id === leaf && (await keyboardPane()) === leaf) return;
  await driver.click(header(leaf));
  await keyboardOn(leaf);
}

const keys = process.platform === 'darwin'
  ? { right: 'mod+d', below: 'mod+shift+d', palette: 'mod+shift+k' }
  : { right: 'mod+shift+d', below: 'mod+alt+d', palette: 'mod+alt+k' };
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

async function stackOnTheShelf() {
  const stacked = await split(keys.below);
  const column = leavesOf(columnOf(current(), stacked));
  runner.assert(column.length === 2, 'the new shell sits in a stack', { column });
  for (let brought = 0; brought < 8; brought += 1) {
    const on = await shown();
    if (!column.some((leaf) => on.includes(leaf))) break;
    const other = on.find((leaf) => !column.includes(leaf));
    runner.assert(other !== undefined, 'the screen shows a tile outside the stack', { on, column });
    await focusShown(other);
    const next = (await chips()).find((leaf) => !column.includes(leaf));
    await driver.click(chip(next));
    await driver.waitFor(tile(next));
    await keyboardOn(next);
  }
  await driver.waitFor(chip(column[0]));
  return column;
}

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('size_the_window_and_the_narrowest_width', async () => {
    const narrowest = observer.getSetting('narrowest_tile_cols');
    runner.registerCleanup('restore_narrowest', () => observer.send({ cmd: 'set_setting', key: 'narrowest_tile_cols', value: narrowest }));
    await setNarrowest(66);
    await driver.setWindowBounds(WINDOW);
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

  await runner.step('build_a_row_past_the_screen', async () => {
    await firstShell();
    for (let count = 2; ; count += 1) {
      await split(keys.right);
      if (count >= (await shown()).length + 3) break;
    }
    await driver.waitFor({ key: 'shelf' });
    runner.log('row', { row: row(), shown: await shown() });
  });

  await runner.step('chip_onto_a_tile_centre_takes_its_slot', async () => {
    const before = await shown();
    const moving = (await chips()).at(-1);
    const slot = before[0];
    await driver.drag(chip(moving), tile(slot));
    await driver.waitFor(tile(moving));
    await driver.waitFor(chip(slot));
    const after = await shown();
    runner.assert(after[0] === moving, 'the chip takes the slot', { after, moving });
    runner.assert(sameList(after.slice(1), before.slice(1)), 'the other tiles stay where they were', { after, before });
    await observer.waitFor(() => current().active_pane_id === moving, 'the chip tile is the active pane');
    await driver.waitFor({ key: `tile.${moving}.spotlight.top` });
    await shot('chip-took-the-slot');
  });

  await runner.step('chip_between_chips_reorders_without_taking_focus', async () => {
    await focusShown(current().active_pane_id);
    const active = current().active_pane_id;
    const before = await shown();
    const offscreen = await chips();
    runner.assert(offscreen.length >= 2, 'there are chips to reorder', { offscreen });
    const moving = offscreen.at(-1);
    await driver.drag(chip(moving), { target: chip(offscreen[0]), at: [0.1, 0.5] });
    await waitForRow((now) => now.indexOf(moving) === now.indexOf(offscreen[0]) - 1, 'the chip before the first chip');
    runner.assert(current().active_pane_id === active, 'the active tile stays active', { active });
    runner.assert(sameList(await shown(), before), 'the screen stays as it was', { before });
    await keyboardOn(active);
    await shot('chips-reordered');
  });

  await runner.step('tile_beside_a_chip_comes_on_screen_with_it', async () => {
    const active = current().active_pane_id;
    const moving = (await shown()).find((leaf) => leaf !== active);
    const anchor = (await chips())[0];
    await driver.drag(header(moving), { target: chip(anchor), at: [0.1, 0.5] });
    await waitForRow((now) => now.indexOf(moving) === now.indexOf(anchor) - 1, 'the tile just before the chip');
    await driver.waitFor(tile(anchor));
    await keyboardOn(moving);
    const after = await shown();
    runner.assert(after.includes(moving) && after.includes(anchor), 'the tile and the chip’s tile are on screen', { after, moving, anchor });
    await shot('tile-beside-the-chip');
  });

  await runner.step('column_divider_resizes_and_a_double_click_clears', async () => {
    const before = await shown();
    const [left, right] = before;
    await driver.drag(divider(left), { by: [60, 0] });
    const wantedLeft = await waitForWanted(left, (cols) => cols > 0);
    const wantedRight = await waitForWanted(right, (cols) => cols > 0);
    runner.assert(wantedLeft > wantedRight, 'the left column grew past its neighbour', { wantedLeft, wantedRight });
    runner.assert(sameList(await shown(), before), 'a small drag keeps the screen', { before });
    await shot('column-divider-dragged');
    await clearDivider(left);
    await waitForWanted(left, (cols) => !cols);
    await waitForWanted(right, (cols) => !cols);
  });

  await runner.step('divider_past_the_narrowest_shelves_the_far_column', async () => {
    const before = await shown();
    const [first] = before;
    const far = before.at(-1);
    await focusShown(first);
    await driver.drag(divider(first), { by: [500, 0] });
    await driver.waitFor(tile(far), { gone: true });
    await driver.waitFor(chip(far));
    await waitForWanted(first, (cols) => cols > 0);
    await shot('far-column-left');
    await clearDraggedWidthsFromThePalette();
    await waitForWanted(first, (cols) => !cols);
    await driver.waitFor(tile(far));
    runner.assert(sameList(await shown(), before), 'clearing the width restores the screen', { before });
  });

  await runner.step('divider_follows_the_pointer_after_a_window_resize', async () => {
    const two = await shown();
    const order = row();
    const third = order[order.indexOf(two.at(-1)) + 1] ?? order[order.indexOf(two[0]) - 1];
    await driver.setWindowBounds({ ...WINDOW, width: 1900 });
    await setNarrowest(57);
    await driver.waitFor(tile(third));
    const on = await shown();
    runner.assert(on.length >= 3, 'three columns on screen', { on });
    await focusShown(on[0]);
    for (const leaf of on.slice(0, -1)) {
      await driver.drag(divider(leaf), { by: [40, 0] });
      await waitForWanted(leaf, (cols) => cols > 0);
    }
    const last = on.at(-1);
    const wantedLast = await waitForWanted(last, (cols) => cols > 0);

    await driver.setWindowBounds({ ...WINDOW, width: 2200 });
    await driver.waitFor(tile(on[0]));
    runner.assert(sameList(await shown(), on), 'the wider window shows the same columns', { on });
    const before = await Promise.all(on.map(widthOf));

    await driver.drag(divider(on[0]), { by: [50, 0] });
    await waitForWanted(last, (cols) => cols !== wantedLast);
    const after = await Promise.all(on.map(widthOf));
    runner.log('divider after resize', { before, after });
    const oneColumnPx = before[0] / wantedIn(current(), on[0]);
    runner.assert(Math.abs(after[0] - before[0] - 50) <= 2 * oneColumnPx, 'the divider follows the pointer', { before, after, oneColumnPx });
    runner.assert(Math.abs(after.at(-1) - before.at(-1)) <= 2 * oneColumnPx, 'the far column keeps its width', { before, after, oneColumnPx });
    await shot('divider-after-window-resize');

    await driver.setWindowBounds({ ...WINDOW, width: 1900 });
    for (const leaf of on.slice(0, -1)) await clearDivider(leaf);
    for (const leaf of on) await waitForWanted(leaf, (cols) => !cols);
    await setNarrowest(66);
    await driver.setWindowBounds(WINDOW);
    await driver.waitFor(tile(last), { gone: true });
  });

  await runner.step('the_active_column_never_leaves', async () => {
    const on = await shown();
    const far = on.at(-1);
    await focusShown(far);
    await driver.drag(divider(on[0]), { target: tile(far), at: [0.97, 0.5] });
    await waitForWanted(on[0], (cols) => cols > 0);
    await driver.waitFor(tile(far));
    runner.assert((await shown()).includes(far), 'the active tile stays on screen', { far });
    await keyboardOn(far);
    await shot('active-column-stays');
    await clearDivider(on[0]);
    await waitForWanted(on[0], (cols) => !cols);
  });

  await runner.step('stack_chip_to_a_sidebar_desktop_moves_its_whole_column', async () => {
    const created = await observer.profileCommand('desktop_create', { profile_id: observer.profileId, name: 'Drop here' });
    const target = created.desktops[0].id;
    runner.registerCleanup('close_drop_desktop', () => {
      const left = observer.desktop(target);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    await driver.waitFor({ key: `sidebar.desktop.${target}` });
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await observer.waitFor(() => observer.currentDesktopId() === desktop.id, 'back on the scenario desktop');
    await driver.waitFor(tile(current().active_pane_id));
    const column = await stackOnTheShelf();
    const sessions = column.map(sessionOf);
    await driver.drag(chip(column[0]), { key: `sidebar.desktop.${target}` });
    for (const sessionId of sessions) {
      await observer.waitForDesktopOf(sessionId, (entry) => entry.id === target, `session ${sessionId} on the dropped desktop`);
    }
    const landed = columnsOf(treeOf(observer.desktop(target))).map(leavesOf);
    runner.assert(landed.some((leaves) => sameList(leaves, column)), 'the stack lands as one column', { landed, column });
    await shot('stack-on-the-sidebar-desktop');
    if (observer.currentDesktopId() !== desktop.id) {
      await driver.click({ key: `sidebar.desktop.${desktop.id}` });
      await observer.waitFor(() => observer.currentDesktopId() === desktop.id, 'back on the scenario desktop');
    }
  });

  await runner.step('stack_chip_on_a_tile_top_edge_keeps_every_tile_usable', async () => {
    const dropOnTopEdge = async () => {
      await focusShown((await shown()).find((leaf) => leavesOf(columnOf(current(), leaf)).length === 1));
      const column = await stackOnTheShelf();
      const anchor = (await shown()).find((leaf) => leavesOf(columnOf(current(), leaf)).length === 1);
      runner.assert(anchor !== undefined, 'a tile alone in its column is on screen', { column });
      const anchorHeight = await heightOf(anchor);
      await driver.drag(chip(column[0]), { target: tile(anchor), at: [0.5, 0.02] });
      return { column, anchor, anchorHeight };
    };

    const short = await dropOnTopEdge();
    runner.assert(short.anchorHeight < 960, 'the tile is too short to stack the column on', short);
    await observer.waitFor(() => {
      const columns = columnsOf(treeOf(current())).map(leavesOf);
      return sameList(columns[columns.findIndex((leaves) => leaves.includes(short.anchor)) + 1] ?? [], short.column);
    }, 'the stack beside the short tile');
    await driver.state('window', { includes: 'so it moved beside instead' });
    await shot('stack-beside-a-short-tile');

    await driver.setWindowBounds({ ...WINDOW, height: 1040 });
    const tall = await dropOnTopEdge();
    runner.assert(tall.anchorHeight >= 960, 'the tall tile has room for the stack', tall);
    await observer.waitFor(() => {
      const leaves = leavesOf(columnOf(current(), tall.anchor));
      return tall.column.every((leaf) => leaves.includes(leaf));
    }, 'the stack on the tile');
    for (const leaf of [...tall.column, tall.anchor]) await driver.waitFor(tile(leaf));
    const heights = await Promise.all([...tall.column, tall.anchor].map(heightOf));
    runner.log('stack on the tall tile', { anchorHeight: tall.anchorHeight, heights });
    runner.assert(heights.every((height) => height >= 320), 'every tile keeps the minimum height', { heights });
    await shot('stack-on-a-tall-tile');
    await driver.setWindowBounds(WINDOW);
  });

  await runner.finishSuccess({ desktopId: desktop.id, row: row() });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
