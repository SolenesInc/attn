#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const TILES = 5;
const PEEK_OPEN_DELAY_MS = 180;
const STOPPED = 'Agent stopped. Open its tile to resume it; your reply stays there.';
const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SHELF-PEEK', tier: 'tier1-local-shell', prefix: 'native-shelf-peek',
  metadata: { focus: 'In the native app hovering an entry shows a live terminal in a panel, a reply reaches it at its size without moving the layout, Enter with no reply opens the tile, an entry that leaves the screen under a still pointer opens nothing, each tile of a stack peeks from its pill and its dots, and a stopped terminal keeps the reply, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));
runner.registerFailureEvidence('native_tree', async () => runner.writeJson('failure-tree.json', await driver.tree()));

const keys = process.platform === 'darwin'
  ? { left: 'mod+alt+left', right: 'mod+alt+right' }
  : { left: 'mod+shift+left', right: 'mod+shift+right' };
const PEEK = { key: 'shelf.peek' };
const entry = (leaf) => ({ key: `shelf.entry.${leaf}` });
const field = (leaf) => ({ key: `shelf.peek.${leaf}.field` });
const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));

function leavesOf(node) {
  if (!node) return [];
  if (node.type === 'split') return node.children.flatMap(leavesOf);
  return [node.pane_id ?? node.tile_id];
}

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
    return { id, label, pane: result.pane_id, runtime: (await observer.waitForTerminalPane(id)).runtime_id };
  };

  const current = () => observer.desktop(desktop.id);
  const readRow = () => leavesOf(JSON.parse(current().tree_json || 'null'));
  const active = () => current().active_pane_id;
  const shownLeaves = async () => {
    const shown = [];
    for (const leaf of readRow()) if (await driver.find({ key: `tile.${leaf}` })) shown.push(leaf);
    return shown;
  };
  const waitActive = (leaf) => observer.waitFor(() => active() === leaf || null, `tile ${leaf} is active`);
  const focusLeaf = async (leaf) => {
    while (active() !== leaf) {
      const row = readRow();
      const forward = row.indexOf(active()) < row.indexOf(leaf);
      const was = active();
      await driver.pressKey(forward ? keys.right : keys.left);
      await observer.waitFor(() => active() !== was || null, `focus moves ${forward ? 'right' : 'left'} from ${was}`);
    }
    await driver.waitFor({ key: `tile.${leaf}` });
  };
  const waitKeyboard = (pane) => driver.state('window', { expect: { keyboard: { pane } }, timeoutMs: 5_000 });
  const hoverAway = async () => {
    await driver.hover({ key: `tile.${(await shownLeaves())[0]}` });
    await driver.waitFor(PEEK, { gone: true });
  };
  const assertNothingMoved = async (name, shown, wasActive) => {
    runner.assert((await shownLeaves()).join() === shown.join(), `${name}: the screen did not change`, { shown });
    runner.assert(active() === wasActive, `${name}: the active tile stays`, { active: active(), wasActive });
  };
  const peekScreen = async () => nodeIn(await driver.tree(), 'shelf.peek.screen')?.children?.[0]?.name ?? null;
  const waitScreen = async (expected) => {
    await driver.waitFor({ key: 'shelf.peek.screen' });
    await driver.waitFor({ role: 'label', name: expected }).catch(async (error) => {
      throw new Error(`the peek screen never read ${JSON.stringify(expected)}; it reads ${JSON.stringify(await peekScreen())}: ${error.message}`);
    });
    runner.assert(await peekScreen() === expected, 'the panel shows the terminal\'s screen', { expected });
  };
  const fieldValue = async (leaf) => nodeIn(await driver.tree(), `shelf.peek.${leaf}.field`)?.value ?? '';
  const peekName = async () => (await driver.waitFor(PEEK)).name;

  const shells = [];
  let row;
  let target;
  let size;
  await runner.step('a_row_of_five_shells', async () => {
    let anchor;
    for (let n = 1; n <= TILES; n += 1) {
      const shell = await spawn(`peek-${n}`, anchor ? { desktop_id: desktop.id, direction: 'vertical', anchor_pane_id: anchor } : { desktop_id: desktop.id });
      shells.push(shell);
      anchor = shell.pane;
    }
    await observer.waitForDesktopOf(shells[0].id, (entry) => entry.panes.length === TILES, 'five shells on the desktop');
    row = readRow();
    runner.assert(row.join() === shells.map((shell) => shell.pane).join(), 'the shells sit in the row in the order they came', { row });
    target = shells.at(-1);
    await focusLeaf(target.pane);
    await driver.click({ key: `tile.${target.pane}.header` });
    await waitKeyboard(target.pane);
    const script = 'clear; printf "si""ze %s" "$(stty size)"; read line; clear; printf "peek""-reply %s %s" "$line" "$(stty size)"; read line; exit 1';
    await driver.typeInto(null, `exec sh -c '${script}'`);
    await driver.pressKey('Enter');
    const rows = await driver.state(`terminal:${target.runtime}`, { includes: 'size ', timeoutMs: 15_000});
    size = JSON.stringify(rows).match(/size (\d+ \d+)/)[1];
    runner.log('the target terminal before it leaves the screen', { target: target.pane, size });
    await focusLeaf(row[0]);
    await driver.waitFor({ key: `tile.${target.pane}` }, { gone: true });
    await driver.waitFor(entry(target.pane));
  });

  await runner.step('hover_shows_the_terminal_without_moving_anything', async () => {
    const shown = await shownLeaves();
    await driver.hover(entry(target.pane));
    runner.assert(await peekName() === target.label, 'the panel names the tile its entry goes to', { target });
    await waitScreen(`size ${size}`);
    await shot('peek-open');
    await assertNothingMoved('peek open', shown, row[0]);
    runner.assert(!(await driver.find({ key: `tile.${target.pane}` })), 'the peeked tile stays off screen');
    await driver.pressKey('Escape');
    await driver.waitFor(PEEK, { gone: true });
    await hoverAway();
    await assertNothingMoved('after Esc', shown, row[0]);
  });

  await runner.step('a_reply_reaches_the_terminal_at_its_size', async () => {
    const shown = await shownLeaves();
    await driver.hover(entry(target.pane));
    await driver.waitFor(PEEK);
    await driver.typeInto(field(target.pane), 'hello');
    await shot('peek-reply-typed');
    await driver.hover({ key: 'shelf.frame' });
    await driver.find(PEEK).then((open) => runner.assert(open, 'the panel stays while its field holds the keyboard'));
    await driver.pressKey('Enter');
    await driver.waitFor(PEEK, { gone: true });
    const keyboard = await driver.state('window');
    runner.assert(keyboard.keyboard?.pane === row[0], 'the keyboard goes back to the active tile', keyboard);
    await assertNothingMoved('after a reply', shown, row[0]);
    await driver.hover(entry(target.pane));
    await waitScreen(`peek-reply hello ${size}`);
    await shot('peek-reply-arrived');
    await driver.pressKey('Escape');
    await driver.waitFor(PEEK, { gone: true });
    await hoverAway();
  });

  await runner.step('enter_with_no_reply_opens_the_tile', async () => {
    await driver.hover(entry(target.pane));
    await driver.waitFor(PEEK);
    await driver.click(field(target.pane));
    await driver.pressKey('Enter');
    await driver.waitFor(PEEK, { gone: true });
    await waitActive(target.pane);
    await driver.waitFor({ key: `tile.${target.pane}` });
    const keyboard = await driver.state('window');
    runner.assert(keyboard.keyboard?.pane === target.pane, 'the opened tile takes the keyboard', keyboard);
    row = readRow();
    await focusLeaf(row[0]);
    await driver.waitFor({ key: `tile.${target.pane}` }, { gone: true });
  });

  await runner.step('an_entry_that_leaves_the_screen_under_a_still_pointer_opens_nothing', async () => {
    const pressed = row.at(-1);
    const receipt = await driver.click(entry(pressed));
    runner.assert(receipt.matched, 'the press lands on the entry', receipt);
    await waitActive(pressed);
    await driver.waitFor({ key: `tile.${pressed}` });
    runner.assert(readRow().join() === row.join(), 'going to the tile leaves the row alone', { row, now: readRow() });
    const [x, y, w, h] = receipt.target.rect;
    const point = [x + w / 2, y + h / 2];
    await focusLeaf(row[0]);
    await driver.waitFor({ key: `tile.${pressed}` }, { gone: true });
    const { rect: at } = await driver.waitFor(entry(pressed));
    runner.assert(point[0] >= at[0] && point[0] < at[0] + at[2] && point[1] >= at[1] && point[1] < at[1] + at[3], 'the entry is under the still pointer', { point, at });
    await driver.waitFor(PEEK, { timeoutMs: PEEK_OPEN_DELAY_MS * 5 }).then(
      () => { throw new Error('a peek opened under the still pointer'); },
      (error) => { if (!/to appear/.test(String(error))) throw error; },
    );
    await shot('still-pointer-after-entry-click');
    await hoverAway();
    await driver.hover(entry(pressed));
    runner.assert(await peekName() === shells.find((shell) => shell.pane === pressed).label, 'moving onto the entry peeks at its tile', { pressed });
    await driver.pressKey('Escape');
    await driver.waitFor(PEEK, { gone: true });
    await hoverAway();
  });

  await runner.step('each_tile_of_an_off_screen_stack_peeks_from_its_dot', async () => {
    const below = await spawn('peek-below', { desktop_id: desktop.id, direction: 'horizontal', anchor_pane_id: target.pane });
    shells.push(below);
    await observer.waitForDesktopOf(below.id, (entry) => entry.panes.length === TILES + 1, 'a shell stacked below the target');
    await focusLeaf(row[0]);
    await driver.waitFor({ key: `tile.${below.pane}` }, { gone: true });
    const shown = await shownLeaves();
    const dots = [
      { dot: { key: `shelf.entry.${target.pane}.above.0` }, pointed: target.label, inUse: below.label },
      { dot: { key: `shelf.entry.${target.pane}.below.1` }, pointed: below.label, inUse: target.label },
    ];
    const found = [];
    for (const { dot } of dots) found.push(await driver.find(dot));
    const stack = dots.find((_, i) => found[i]?.rect[2] > 0);
    runner.assert(stack, 'the pill draws a dot for the member that is not in use', { found });

    await driver.hover(entry(target.pane));
    runner.assert(await peekName() === stack.inUse, 'the pill peeks at the member in use', stack);
    await shot('peek-stack-in-use');
    const moved = await driver.hover(stack.dot);
    runner.log('moving onto the dot with the panel open', { landed: moved.landed, matched: moved.matched });
    await driver.waitFor({ role: 'dialog', name: stack.pointed });
    await shot('peek-stack-dot');
    await assertNothingMoved('peeking a stack', shown, row[0]);
    await driver.pressKey('Escape');
    await driver.waitFor(PEEK, { gone: true });
    await hoverAway();

    observer.unregisterSession(below.id);
    await observer.waitForDesktopOf(target.id, (entry) => entry.panes.length === TILES, 'the stacked shell closed');
    await driver.waitFor({ key: `shelf.entry.${target.pane}.slice.1` }, { gone: true });
  });

  await runner.step('a_stopped_terminal_keeps_the_reply_and_the_tile_offers_it', async () => {
    await driver.hover(entry(target.pane));
    await driver.waitFor(PEEK);
    await driver.typeInto(field(target.pane), 'exit 1');
    await driver.hover({ key: 'shelf.frame' });
    await driver.pressKey('Enter');
    await driver.waitFor(PEEK, { gone: true });
    await hoverAway();
    await observer.waitFor(() => observer.getSession(target.id)?.terminal_exit || null, 'the target terminal stops', 15_000);
    await observer.waitForDesktopOf(target.id, (entry) => entry.panes.some((pane) => pane.pane_id === target.pane), 'the stopped tile stays');

    await driver.hover(entry(target.pane));
    const notice = await driver.waitFor({ key: 'shelf.peek.notice' });
    runner.assert(notice.name === STOPPED, 'the panel says the agent stopped', notice);
    await driver.typeInto(field(target.pane), 'keep this reply');
    await driver.hover({ key: 'shelf.frame' });
    await driver.pressKey('Enter');
    await driver.waitFor(PEEK);
    runner.assert(await fieldValue(target.pane) === 'keep this reply', 'Enter keeps the reply when the agent stopped');
    await shot('peek-stopped-reply-kept');
    await driver.pressKey('Escape');
    await driver.waitFor(PEEK, { gone: true });
    await hoverAway();

    await driver.hover(entry(target.pane));
    await driver.waitFor(field(target.pane));
    runner.assert(await fieldValue(target.pane) === 'keep this reply', 'reopening the panel shows the kept reply');
    await driver.click({ key: 'shelf.peek.open' });
    await driver.waitFor(PEEK, { gone: true });
    await driver.waitFor({ key: `tile.${target.pane}` });
    await driver.state(`terminal:${target.runtime}`, { includes: 'exited with code 1', timeoutMs: 15_000 });
    const strip = nodeIn(await driver.tree(), `tile.unsent.${target.runtime}`);
    runner.assert(strip?.children?.[0]?.name === 'Unsent reply: keep this reply', 'the tile offers the unsent reply', strip);
    await shot('tile-offers-unsent-reply');
    await driver.click({ key: `tile.unsent.${target.runtime}.discard` });
    await driver.waitFor({ key: `tile.unsent.${target.runtime}` }, { gone: true });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, row, target, size, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
