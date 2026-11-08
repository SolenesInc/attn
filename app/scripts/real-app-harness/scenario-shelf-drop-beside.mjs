#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { waitForPaneInputFocus, waitForSessionDesktop } from './scenarioAssertions.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const runner = createScenarioRunner(options, {
  scenarioId: 'SHELF-DROP-BESIDE',
  tier: 'tier1-local-shell',
  prefix: 'shelf-drop-beside',
  metadata: { agent: 'shell', focus: 'a tile dropped beside a chip comes on screen next to it, active, for every drop place on a four-tile row that shows two and when a saved width keeps the pair apart, with native pointer input' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const request = (action, payload) => client.request(action, payload);
const waitDom = (selector, extra = {}) => request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const press = (shortcutId) => pressShortcutKeys(client, driver, shortcutId);
const shot = async (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const sessions = [];
let firstSession;
let desktop;

const leavesOf = (node) => (node.type === 'split' ? node.children.flatMap(leavesOf) : [node.paneId ?? node.tileId]);
const firstLeafOf = (node) => (node.type === 'split' ? firstLeafOf(node.children[0]) : node);
const columnsOf = (node) => (node.type === 'split' && node.direction === 'vertical' ? node.children.flatMap(columnsOf) : [node]);
const wantedIn = (tree, leafId) => firstLeafOf(columnsOf(tree).find((column) => leavesOf(column).includes(leafId))).wantedCols;
const readRow = async () => leavesOf((await request('get_desktop', { sessionId: firstSession })).desktop.layoutTree);
const activeLeaf = async () => (await request('get_state')).activeLeaf.leafId;
const shownLeaves = async () => (await request('get_desktop_ui_state', { desktopId: desktop })).shownLeafIds;
const surface = () => `[data-session-terminal-desktop="${desktop}"]`;
const paneSelector = (leafId) => `${surface()} .desktop-pane[data-pane-id="${leafId}"]`;
const chipSelector = (leafId) => `[data-testid="shelf-chip-${leafId}"]`;
const headerGrip = (leafId) => ({ selector: `${paneSelector(leafId)} .desktop-pane-header`, at: { x: 0.7, y: 0.25 } });
const sessionOf = async (leafId) => (await request('get_desktop', { sessionId: firstSession })).desktop.panes.find((pane) => pane.paneId === leafId)?.sessionId;
const sameList = (left, right) => left.join() === right.join();
const LEFT_HALF = 0.15;
const RIGHT_HALF = 0.85;
const TILE_DRAG = { steps: 24, withoutReceipt: 'a tile header cancels the press, so it has no mouse events' };
const DIVIDER_DRAG = { steps: 24, withoutReceipt: 'the drag\'s shield leaves the page under the pointer before the mouse-up' };

async function waitForScreen(expected) {
  for (const leafId of expected) await waitDom(paneSelector(leafId));
  const shown = await shownLeaves();
  runner.assert(sameList(shown, expected), 'the screen shows the dropped tile beside the chip’s tile', { shown, expected });
}

async function dropBeside(tile, anchor, share) {
  const rowBefore = await readRow();
  const shownBefore = await shownLeaves();
  await driver.drag(headerGrip(tile), { selector: chipSelector(anchor), at: { x: share, y: 0.5 } }, TILE_DRAG);
  const state = await waitForSessionDesktop(client, firstSession, (current) => {
    const row = leavesOf(current.desktop.layoutTree);
    return row.indexOf(tile) - row.indexOf(anchor) === (share === LEFT_HALF ? -1 : 1) && current.desktop.daemonActivePaneId === tile;
  }, `${tile} ${share === LEFT_HALF ? 'before' : 'after'} ${anchor}, active`);
  const row = leavesOf(state.desktop.layoutTree);
  const expected = row.filter((id) => id === tile || id === anchor);
  await waitDom(`${surface()}[data-active-leaf-id="${tile}"]`);
  await waitForScreen(expected);
  await waitForPaneInputFocus(client, await sessionOf(tile), tile);
  const shown = await shownLeaves();
  runner.assert(!sameList(shown, shownBefore) || !sameList(row, rowBefore), 'the drop changes what the user sees', { shownBefore, shown, rowBefore, row });
  runner.log('dropped', { tile, anchor, side: share === LEFT_HALF ? 'left' : 'right', rowBefore, shownBefore, row, shown });
}

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_sessions', async () => {
  for (const sessionId of [...sessions].reverse()) await request('close_session', { sessionId }).catch(() => {});
});
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ shelf: '[data-testid="desktop-shelf"]' }) });

try {
  await runner.step('launch', async () => {
    await launchFreshAppAndConnect(client, observer);
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    const { logicalBounds } = await request('get_window_bounds');
    await request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1600, height: 900 } });
  });

  await runner.step('build_four_tiles_with_two_on_screen', async () => {
    const cwd = path.join(runner.sessionDir, 'row');
    fs.mkdirSync(cwd, { recursive: true });
    firstSession = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `drop-beside-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(firstSession);
    await request('select_session', { sessionId: firstSession });
    desktop = (await request('get_desktop', { sessionId: firstSession })).desktopId;
    for (let count = 2; count <= 4; count += 1) {
      await press('terminal.splitVertical');
      const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === count && current.panes.every((pane) => pane.runtimeId), `${count} shells`, 30_000);
      sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
    }
    for (let shown = await shownLeaves(); shown.length > 2; shown = await shownLeaves()) {
      await press('shelf.fewerTiles');
      await waitDom(paneSelector(shown[0]), { absent: true });
    }
    const row = await readRow();
    const shown = await shownLeaves();
    runner.assert(row.length === 4 && shown.length === 2, 'four tiles, two on screen', { row, shown });
    runner.assert(sameList(shown, row.slice(2)) && (await activeLeaf()) === row[3], 'the newest tile is active at the end of the row', { row, shown });
    await shot('four-tiles-two-on-screen');
  });

  await runner.step('d_dropped_beside_a_chip_shows_a_and_d', async () => {
    const [a, , , d] = await readRow();
    await shot('before-the-drop');
    await dropBeside(d, a, RIGHT_HALF);
    runner.assert(sameList(await shownLeaves(), [a, d]), 'A|d on screen', { a, d });
    await shot('a-and-d-on-screen');
  });

  await runner.step('every_drop_place_changes_the_screen', async () => {
    for (const tileIs of ['active', 'inactive']) {
      for (const chipAt of [0, 1]) {
        for (const share of [LEFT_HALF, RIGHT_HALF]) {
          const shown = await shownLeaves();
          const active = await activeLeaf();
          const tile = tileIs === 'active' ? active : shown.find((id) => id !== active);
          const row = await readRow();
          const chips = row.filter((id) => !shown.includes(id));
          await dropBeside(tile, chips[chipAt], share);
        }
      }
    }
    await shot('after-every-drop-place');
  });

  await runner.step('a_drop_that_keeps_the_row_order_still_shows_the_pair', async () => {
    const row = await readRow();
    const shown = await shownLeaves();
    const tileAt = row.findIndex((id, index) => shown.includes(id) && row[index + 1] && !shown.includes(row[index + 1]));
    runner.assert(tileAt >= 0, 'a tile on screen sits just before a chip', { row, shown });
    await dropBeside(row[tileAt], row[tileAt + 1], LEFT_HALF);
    runner.assert(sameList(await readRow(), row), 'the row order is unchanged', { row });
    await shot('unchanged-order-drop');
  });

  await runner.step('a_saved_width_that_keeps_the_pair_apart_gives_way', async () => {
    const [wide, neighbour] = await shownLeaves();
    runner.assert((await activeLeaf()) === wide, 'the left tile on screen is active', { wide });
    await driver.drag({ selector: `${surface()} .desktop-split-divider[data-split-id="row:${wide}"]` }, { by: { x: 500, y: 0 } }, DIVIDER_DRAG);
    await waitForSessionDesktop(client, firstSession, (current) => wantedIn(current.desktop.layoutTree, wide) > 0, `${wide} saved wide`);
    await waitDom(paneSelector(neighbour), { absent: true });
    await waitDom(chipSelector(neighbour));
    await shot('saved-width-alone');
    const row = await readRow();
    await dropBeside(wide, neighbour, LEFT_HALF);
    runner.assert(sameList(await readRow(), row), 'the row order is unchanged', { row });
    await waitForSessionDesktop(client, firstSession, (current) => !wantedIn(current.desktop.layoutTree, wide), `${wide} gives up its saved width`);
    await shot('saved-width-cleared-pair-shown');
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
