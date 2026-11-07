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
  scenarioId: 'SHELF-DRAGS',
  tier: 'tier1-local-shell',
  prefix: 'shelf-drags',
  metadata: { agent: 'shell', focus: 'chips onto tiles, between chips and onto sidebar desktops, tiles beside a chip, column dividers past the narrowest and back and after a window resize, a stack chip onto the top edge of a tile, and Esc, with real terminals and native pointer input' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const request = (action, payload) => client.request(action, payload);
const waitDom = (selector, extra = {}) => request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const press = (shortcutId) => pressShortcutKeys(client, driver, shortcutId);
const shot = async (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const hold = () => (process.env.ATTN_HARNESS_RECORD === '1' ? new Promise((resolve) => setTimeout(resolve, 1200)) : Promise.resolve());
const sessions = [];
let firstSession;
let desktop;

const leavesOf = (node) => (node.type === 'split' ? node.children.flatMap(leavesOf) : [node.paneId ?? node.tileId]);
const treeOf = async () => (await request('get_desktop', { sessionId: firstSession })).desktop.layoutTree;
const readRow = async () => leavesOf(await treeOf());
const firstLeafOf = (node) => (node.type === 'split' ? firstLeafOf(node.children[0]) : node);
const columnsOf = (node) => (node.type === 'split' && node.direction === 'vertical' ? node.children.flatMap(columnsOf) : [node]);
const wantedOf = async (leafId) => {
  const column = columnsOf(await treeOf()).find((candidate) => leavesOf(candidate).includes(leafId));
  return firstLeafOf(column).wantedCols;
};
const activeLeaf = async () => (await request('get_state')).activeLeaf.leafId;
const shownLeaves = async () => (await request('get_desktop_ui_state', { desktopId: desktop })).shownLeafIds;
const surface = () => `[data-session-terminal-desktop="${desktop}"]`;
const paneSelector = (leafId) => `${surface()} .desktop-pane[data-pane-id="${leafId}"]`;
const chipSelector = (leafId) => `[data-testid="shelf-chip-${leafId}"]`;
const chipIds = async () => {
  const row = await readRow();
  const shown = await shownLeaves();
  return row.filter((id) => !shown.includes(id));
};
const sessionOf = async (leafId) => (await request('get_desktop', { sessionId: firstSession })).desktop.panes.find((pane) => pane.paneId === leafId)?.sessionId;
const waitForActive = (leafId) => waitDom(`${surface()}[data-active-leaf-id="${leafId}"] .desktop-pane[data-pane-id="${leafId}"]`);
const assertActiveHasInput = async (leafId) => {
  await waitForActive(leafId);
  await waitForPaneInputFocus(client, await sessionOf(leafId), leafId);
};
const waitForRow = async (predicate, description) => {
  const state = await waitForSessionDesktop(client, firstSession, (current) => predicate(leavesOf(current.desktop.layoutTree)), description);
  return leavesOf(state.desktop.layoutTree);
};
const columnDivider = (leafId) => `${surface()} .desktop-split-divider[data-split-id="row:${leafId}"]`;
const headerGrip = (leafId) => ({ selector: `${paneSelector(leafId)} .desktop-pane-header`, at: { x: 0.7, y: 0.25 } });
const centreOf = (leafId) => ({ selector: paneSelector(leafId), at: { x: 0.5, y: 0.5 } });
const moveOffTheShelf = async () => {
  await driver.hover(headerGrip((await shownLeaves())[0]));
  await waitDom('[data-testid="shelf-peek"]', { absent: true });
};
const paneBox = async (leafId) => (await request('dom_bounds', { selector: paneSelector(leafId) })).bounds;
const paneWidth = async (leafId) => (await paneBox(leafId)).width;
const paneHeight = async (leafId) => (await paneBox(leafId)).height;
const DIVIDER_DRAG = { steps: 24, withoutReceipt: 'the drag\'s shield leaves the page under the pointer before the mouse-up' };
const sameList = (left, right) => left.join() === right.join();

async function growRowPastTheScreen() {
  for (let count = 2; ; count += 1) {
    await press('terminal.splitVertical');
    const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === count && current.panes.every((pane) => pane.runtimeId), `${count} shells`, 30_000);
    sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
    desktop = (await request('get_desktop', { sessionId: firstSession })).desktopId;
    const shown = await shownLeaves();
    if (count >= shown.length + 3) return;
  }
}

async function focusShownLeaf(leafId) {
  if ((await activeLeaf()) === leafId) return;
  await driver.click(centreOf(leafId));
  await waitForActive(leafId);
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

  await runner.step('build_a_row_past_the_screen', async () => {
    const cwd = path.join(runner.sessionDir, 'row');
    fs.mkdirSync(cwd, { recursive: true });
    firstSession = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `drags-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(firstSession);
    await request('select_session', { sessionId: firstSession });
    desktop = (await request('get_desktop', { sessionId: firstSession })).desktopId;
    await growRowPastTheScreen();
    await waitDom('[data-testid="desktop-shelf"]');
    runner.log('row', { row: await readRow(), shown: await shownLeaves() });
  });

  await runner.step('chip_onto_a_tile_centre_replaces_the_slot', async () => {
    const shownBefore = await shownLeaves();
    const chip = (await chipIds()).at(-1);
    const slot = shownBefore[0];
    await hold();
    await driver.drag(chipSelector(chip), centreOf(slot), { steps: 24, withoutReceipt: 'the swap removes the tile under the pointer before the mouse-up' });
    await waitForActive(chip);
    await waitDom(chipSelector(slot));
    const shown = await shownLeaves();
    runner.assert(shown[0] === chip, 'the chip takes the slot', { shown, chip });
    runner.assert(sameList(shown.slice(1), shownBefore.slice(1)), 'the other tiles stay where they were', { shown, shownBefore });
    await assertActiveHasInput(chip);
    await hold();
    await shot('chip-replaced-slot');
  });

  await runner.step('chip_between_chips_reorders_without_taking_focus', async () => {
    const active = await activeLeaf();
    const shownBefore = await shownLeaves();
    const chips = await chipIds();
    runner.assert(chips.length >= 2, 'there are chips to reorder', { chips });
    const moving = chips.at(-1);
    await hold();
    await driver.drag(chipSelector(moving), { selector: chipSelector(chips[0]), at: { x: 0.1, y: 0.5 } }, { steps: 24 });
    const row = await waitForRow((current) => current.indexOf(moving) === current.indexOf(chips[0]) - 1, 'the chip before the first chip');
    runner.log('reordered', { chips, row });
    runner.assert((await activeLeaf()) === active, 'focus stays on the active tile', { active });
    runner.assert(sameList(await shownLeaves(), shownBefore), 'the screen stays as it was', { shownBefore });
    await assertActiveHasInput(active);
    await hold();
    await shot('chips-reordered');
    await moveOffTheShelf();
  });

  await runner.step('esc_cancels_a_chip_drag', async () => {
    const rowBefore = await readRow();
    const active = await activeLeaf();
    const chip = (await chipIds())[0];
    const drag = await driver.holdDrag(chipSelector(chip), centreOf((await shownLeaves()).at(-1)), { steps: 16 });
    await waitDom(`${surface()} .desktop-dock-target`);
    await waitDom('.desktop-dock-ghost');
    await hold();
    await shot('chip-drag-preview');
    await driver.pressKey('Escape');
    await waitDom(`${surface()} .desktop-dock-target`, { absent: true });
    await waitDom('.desktop-dock-ghost', { absent: true });
    await drag.release();
    runner.assert(sameList(await readRow(), rowBefore), 'Esc leaves the row alone', { rowBefore });
    runner.assert((await activeLeaf()) === active, 'Esc leaves focus alone', { active });
  });

  await runner.step('tile_beside_a_chip_comes_on_screen_with_it', async () => {
    const active = await activeLeaf();
    const tile = (await shownLeaves()).find((id) => id !== active);
    const anchor = (await chipIds())[0];
    await hold();
    await driver.drag(headerGrip(tile), { selector: chipSelector(anchor), at: { x: 0.1, y: 0.5 } }, { steps: 24, withoutReceipt: 'a tile header cancels the press, so it has no mouse events' });
    const row = await waitForRow((current) => current.indexOf(tile) === current.indexOf(anchor) - 1, 'the tile just before the chip');
    await assertActiveHasInput(tile);
    await waitDom(paneSelector(anchor));
    const shown = await shownLeaves();
    runner.assert(shown.includes(tile) && shown.includes(anchor), 'the tile and the chip’s tile are on screen', { row, shown, tile, anchor });
    await hold();
    await shot('tile-beside-the-chip');
    await moveOffTheShelf();
  });

  await runner.step('column_divider_resizes_and_a_double_click_clears', async () => {
    const shown = await shownLeaves();
    const [left, right] = shown;
    await hold();
    await driver.drag(columnDivider(left), { by: { x: 60, y: 0 } }, DIVIDER_DRAG);
    const wantedLeft = await waitForWanted(left, (cols) => cols > 0);
    const wantedRight = await waitForWanted(right, (cols) => cols > 0);
    runner.log('dragged widths', { wantedLeft, wantedRight });
    runner.assert(wantedLeft > wantedRight, 'the left column grew past its neighbour', { wantedLeft, wantedRight });
    runner.assert(sameList(await shownLeaves(), shown), 'a small drag keeps the screen', { shown });
    await hold();
    await shot('column-divider-dragged');

    await driver.click(columnDivider(left));
    await driver.click(columnDivider(left), { clickCount: 2 });
    await waitForWanted(left, (cols) => !cols);
    await waitForWanted(right, (cols) => !cols);
    await hold();
  });

  await runner.step('divider_past_the_narrowest_and_back_in_one_drag', async () => {
    const shown = await shownLeaves();
    await focusShownLeaf(shown[0]);
    const first = shown[0];
    const far = shown.at(-1);
    const drag = await driver.holdDrag(columnDivider(first), { by: { x: 500, y: 0 } }, DIVIDER_DRAG);
    await waitDom(paneSelector(far), { absent: true });
    await waitDom(chipSelector(far));
    await hold();
    await shot('far-column-left');
    await drag.dragTo({ by: { x: 0, y: 0 } });
    await waitDom(paneSelector(far));
    await waitDom(chipSelector(far), { absent: true });
    await drag.release();
    runner.assert(sameList(await shownLeaves(), shown), 'dragging back restores the screen', { shown });
    runner.assert(!(await wantedOf(first)), 'dragging back asks for no width', { first });

    const cancelled = await driver.holdDrag(columnDivider(first), { by: { x: 500, y: 0 } }, DIVIDER_DRAG);
    await waitDom(paneSelector(far), { absent: true });
    await driver.pressKey('Escape');
    await waitDom(paneSelector(far));
    await cancelled.release();
    runner.assert(sameList(await shownLeaves(), shown), 'Esc restores the screen', { shown });
    runner.assert(!(await wantedOf(first)), 'Esc asks for no width', { first });
  });

  await runner.step('divider_follows_the_pointer_after_a_window_resize', async () => {
    const twoColumns = await shownLeaves();
    const row = await readRow();
    const third = row[row.indexOf(twoColumns.at(-1)) + 1] ?? row[row.indexOf(twoColumns[0]) - 1];
    const fitted = (await request('get_window_bounds')).logicalBounds;
    await request('set_window_bounds', { logicalBounds: { ...fitted, width: 1900 } });
    await request('set_setting', { key: 'narrowest_tile_cols', value: '57' });
    await waitDom(paneSelector(third));
    const shown = await shownLeaves();
    runner.assert(shown.length >= 3, 'three columns on screen', { shown });
    await focusShownLeaf(shown[0]);
    for (const leafId of shown.slice(0, -1)) {
      await driver.drag(columnDivider(leafId), { by: { x: 40, y: 0 } }, DIVIDER_DRAG);
      await waitForWanted(leafId, (cols) => cols > 0);
    }
    const last = shown.at(-1);
    const wantedLast = await waitForWanted(last, (cols) => cols > 0);

    const { logicalBounds } = await request('get_window_bounds');
    await request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: logicalBounds.width + 300 } });
    runner.assert(sameList(await shownLeaves(), shown), 'the wider window shows the same columns', { shown });
    const before = await Promise.all(shown.map(paneWidth));

    await driver.drag(columnDivider(shown[0]), { by: { x: 50, y: 0 } }, DIVIDER_DRAG);
    await waitForWanted(last, (cols) => cols !== wantedLast);
    const after = await Promise.all(shown.map(paneWidth));
    runner.log('divider after resize', { before, after });
    const oneColumnPx = before[0] / wantedLast;
    runner.assert(Math.abs(after[0] - before[0] - 50) <= 2 * oneColumnPx, 'the divider follows the pointer', { before, after, oneColumnPx });
    runner.assert(Math.abs(after.at(-1) - before.at(-1)) <= 2 * oneColumnPx, 'the far column keeps its width', { before, after, oneColumnPx });
    await hold();
    await shot('divider-after-window-resize');

    await request('set_window_bounds', { logicalBounds });
    for (const leafId of shown.slice(0, -1)) {
      await driver.click(columnDivider(leafId));
      await driver.click(columnDivider(leafId), { clickCount: 2 });
    }
    for (const leafId of shown) await waitForWanted(leafId, (cols) => !cols);
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    await request('set_window_bounds', { logicalBounds: fitted });
    await waitDom(paneSelector(last), { absent: true });
  });

  await runner.step('active_column_never_leaves', async () => {
    const shown = await shownLeaves();
    const far = shown.at(-1);
    await focusShownLeaf(far);
    await driver.drag(columnDivider(shown[0]), { selector: paneSelector(far), at: { x: 0.97, y: 0.5 } }, DIVIDER_DRAG);
    await waitForWanted(shown[0], (cols) => cols > 0);
    const after = await shownLeaves();
    runner.assert(after.includes(far), 'the active tile stays on screen', { after, far });
    runner.assert((await activeLeaf()) === far, 'the active tile stays active', { far });
    await assertActiveHasInput(far);
    await hold();
    await shot('active-column-stays');
    await driver.click(columnDivider(shown[0]));
    await driver.click(columnDivider(shown[0]), { clickCount: 2 });
    await waitForWanted(shown[0], (cols) => !cols);
  });

  await runner.step('stack_chip_to_a_sidebar_desktop_moves_its_whole_column', async () => {
    const created = await observer.profileCommand('desktop_create', { profile_id: observer.profileId, shortcut_slot: 0, name: 'Drop here' });
    const target = created.desktops[0].id;
    await waitDom(`[data-desktop-id="${target}"] .sidebar-row-select`);
    await request('select_session', { sessionId: firstSession });
    await waitDom(`${surface()}[data-session-visible="1"]`);

    const before = await readRow();
    await press('terminal.splitHorizontal');
    const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === before.length + 1 && current.panes.every((pane) => pane.runtimeId), 'a stacked shell', 30_000);
    sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
    const stacked = await activeLeaf();
    const column = columnsOf(await treeOf()).find((candidate) => leavesOf(candidate).includes(stacked));
    const stackLeaves = leavesOf(column);
    runner.assert(stackLeaves.length === 2, 'the tile sits in a stack', { stackLeaves });

    const stackSessions = await Promise.all(stackLeaves.map(sessionOf));
    await sendStackOffScreen(stackLeaves);
    await hold();
    await driver.drag(chipSelector(stackLeaves[0]), { selector: `[data-desktop-id="${target}"] .sidebar-row-select`, at: { x: 0.5, y: 0.5 } }, { steps: 24 });
    for (const sessionId of stackSessions) {
      await waitForSessionDesktop(client, sessionId, (current) => current?.desktopId === target, `session ${sessionId} on the dropped desktop`);
    }
    const landed = await request('get_desktop', { sessionId: stackSessions[0] });
    const landedColumns = columnsOf(landed.desktop.layoutTree);
    runner.log('landed', { columns: landedColumns.map(leavesOf) });
    runner.assert(landedColumns.some((candidate) => leavesOf(candidate).length === 2), 'the stack lands as one column', { columns: landedColumns.map(leavesOf) });
    await hold();
    await shot('stack-on-the-sidebar-desktop');
    firstSession = sessions.find((sessionId) => !stackSessions.includes(sessionId));
    await request('select_session', { sessionId: firstSession });
    await waitDom(`${surface()}[data-session-visible="1"]`);
  });

  await runner.step('stack_chip_on_a_tile_edge_keeps_every_tile_usable', async () => {
    const stackOnTheShelf = async () => {
      const before = await readRow();
      await press('terminal.splitHorizontal');
      const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === before.length + 1 && current.panes.every((pane) => pane.runtimeId), 'a stacked shell', 30_000);
      sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
      const stacked = await activeLeaf();
      const stackLeaves = leavesOf(columnsOf(await treeOf()).find((candidate) => leavesOf(candidate).includes(stacked)));
      await sendStackOffScreen(stackLeaves);
      return stackLeaves;
    };
    const dropOnTopEdge = async (stackLeaves) => {
      const anchor = (await shownLeaves()).find((id) => !stackLeaves.includes(id));
      const anchorHeight = await paneHeight(anchor);
      await hold();
      await driver.drag(chipSelector(stackLeaves[0]), { selector: paneSelector(anchor), at: { x: 0.5, y: 0.02 } }, { steps: 24 });
      return { anchor, anchorHeight };
    };
    const assertUsable = async (stackLeaves, { anchor, anchorHeight }) => {
      if (anchorHeight >= 960) {
        await waitForSessionDesktop(client, firstSession, (current) => columnsOf(current.desktop.layoutTree).some((column) => leavesOf(column).includes(anchor) && stackLeaves.every((id) => leavesOf(column).includes(id))), 'the stack on the tile');
        const heights = await Promise.all([...stackLeaves, anchor].map(paneHeight));
        runner.log('stack on the tile', { anchorHeight, heights });
        runner.assert(heights.every((height) => height >= 310), 'every tile keeps the minimum height', { heights });
        return 'stack-on-a-tall-tile';
      }
      await waitDom('.toast', { textIncludes: 'so it moved beside instead' });
      const besideAnchor = (tree) => {
        const columns = columnsOf(tree).map(leavesOf);
        return sameList(columns[columns.findIndex((column) => column.includes(anchor)) + 1] ?? [], stackLeaves);
      };
      await waitForSessionDesktop(client, firstSession, (current) => besideAnchor(current.desktop.layoutTree), 'the stack beside the tile');
      runner.log('stack beside the tile', { anchorHeight });
      return 'stack-beside-a-short-tile';
    };
    const dropAndCheck = async () => {
      const stackLeaves = await stackOnTheShelf();
      const name = await assertUsable(stackLeaves, await dropOnTopEdge(stackLeaves));
      await hold();
      await shot(name);
      return stackLeaves;
    };
    const { logicalBounds } = await request('get_window_bounds');

    await request('set_window_bounds', { logicalBounds: { ...logicalBounds, height: 1300 } });
    await dropAndCheck();
    await request('set_window_bounds', { logicalBounds });
    const columns = columnsOf(await treeOf()).map(leavesOf);
    const single = (await shownLeaves()).find((id) => columns.some((column) => column.length === 1 && column[0] === id));
    await focusShownLeaf(single);
    await dropAndCheck();
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}

async function waitForWanted(leafId, predicate) {
  const state = await waitForSessionDesktop(client, firstSession, (current) => {
    const column = columnsOf(current.desktop.layoutTree).find((candidate) => leavesOf(candidate).includes(leafId));
    return column && predicate(firstLeafOf(column).wantedCols);
  }, `the wanted width of ${leafId}`);
  const column = columnsOf(state.desktop.layoutTree).find((candidate) => leavesOf(candidate).includes(leafId));
  return firstLeafOf(column).wantedCols;
}

async function sendStackOffScreen(stackLeaves) {
  for (let brought = 0; brought < 8; brought += 1) {
    const shown = await shownLeaves();
    if (!stackLeaves.some((id) => shown.includes(id))) break;
    const other = shown.find((id) => !stackLeaves.includes(id));
    runner.assert(other !== undefined, 'the screen shows a tile outside the stack', { shown, stackLeaves });
    await focusShownLeaf(other);
    const chip = (await chipIds()).find((id) => !stackLeaves.includes(id));
    await driver.click(chipSelector(chip));
    await waitForActive(chip);
  }
  await waitDom(chipSelector(stackLeaves[0]));
}
