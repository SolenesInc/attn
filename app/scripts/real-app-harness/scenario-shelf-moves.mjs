#!/usr/bin/env node
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { waitForPaneInputFocus, waitForPaneShellReady, waitForPaneText, waitForSessionDesktop } from './scenarioAssertions.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);
const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const runner = createScenarioRunner(options, {
  scenarioId: 'SHELF-MOVES',
  tier: 'tier1-local-shell',
  prefix: 'shelf-moves',
  metadata: { agent: 'shell', focus: 'opening at capacity, closing, carrying, sending to the shelf, an agent opening a document while the user types, and bringing it with the jump key, through AppKit keys with real terminals' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const instance = currentHarnessInstance();
const request = (action, payload) => client.request(action, payload);
const waitDom = (selector, extra = {}) => request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const press = (shortcutId) => pressShortcutKeys(client, driver, shortcutId);
const shot = async (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const hold = () => (process.env.ATTN_HARNESS_RECORD === '1' ? new Promise((resolve) => setTimeout(resolve, 1200)) : Promise.resolve());
const sessions = [];
let firstSession;
let desktop;

const leavesOf = (node) => (node.type === 'split' ? node.children.flatMap(leavesOf) : [node.paneId ?? node.tileId]);
const desktopState = () => request('get_desktop', { sessionId: firstSession });
const readRow = async () => leavesOf((await desktopState()).desktop.layoutTree);
const activeLeaf = async () => (await request('get_state')).activeLeaf.leafId;
const shownLeaves = async () => (await request('get_desktop_ui_state', { desktopId: desktop })).shownLeafIds;
const surface = () => `[data-session-terminal-desktop="${desktop}"]`;
const sessionOf = async (leafId) => (await desktopState()).desktop.panes.find((pane) => pane.paneId === leafId)?.sessionId;
const waitForActive = (leafId) => waitDom(`${surface()}[data-active-leaf-id="${leafId}"] .desktop-pane[data-pane-id="${leafId}"]`);
const waitForChip = (leafId, extra = '') => waitDom(`[data-testid="shelf-chip-${leafId}"]${extra}`);

async function assertActiveHasInput(leafId) {
  await waitForActive(leafId);
  await waitForPaneInputFocus(client, await sessionOf(leafId), leafId);
}

async function growRowPastTheScreen() {
  for (let count = 2; ; count += 1) {
    await press('terminal.splitVertical');
    const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === count && current.panes.every((pane) => pane.runtimeId), `${count} shells`, 30_000);
    sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
    desktop = (await desktopState()).desktopId;
    const shown = await shownLeaves();
    if (shown.length < count && count >= shown.length + 2) return;
  }
}

async function slideTo(leafId) {
  const row = await readRow();
  for (;;) {
    const at = row.indexOf(await activeLeaf());
    if (row[at] === leafId) return;
    const forward = at < row.indexOf(leafId);
    await press(forward ? 'terminal.focusRight' : 'terminal.focusLeft');
    await waitForActive(row[at + (forward ? 1 : -1)]);
  }
}

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', async () => {
  await request('set_setting', { key: 'shelf_layout_enabled', value: 'false' });
});
runner.registerCleanup('close_sessions', async () => {
  for (const sessionId of [...sessions].reverse()) await request('close_session', { sessionId }).catch(() => {});
});
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ shelf: '[data-testid="desktop-shelf"]' }) });

try {
  await runner.step('launch_with_the_setting_on', async () => {
    await launchFreshAppAndConnect(client, observer);
    await request('set_setting', { key: 'shelf_layout_enabled', value: 'true' });
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    const { logicalBounds } = await request('get_window_bounds');
    await request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1900, height: 900 } });
  });

  await runner.step('build_a_row_past_the_screen_with_native_keys', async () => {
    const cwd = path.join(runner.sessionDir, 'row');
    fs.mkdirSync(cwd, { recursive: true });
    firstSession = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `moves-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(firstSession);
    await request('select_session', { sessionId: firstSession });
    desktop = (await desktopState()).desktopId;
    await growRowPastTheScreen();
    await waitDom('[data-testid="desktop-shelf"]');
    runner.log('row', { row: await readRow(), shown: await shownLeaves() });
  });

  await runner.step('cmd_d_at_capacity_keeps_the_anchor_on_screen', async () => {
    const before = await readRow();
    await slideTo((await shownLeaves()).at(-1));
    const anchor = await activeLeaf();
    const shownBefore = await shownLeaves();
    await hold();
    await press('terminal.splitVertical');
    const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === before.length + 1 && current.panes.every((pane) => pane.runtimeId), 'one more shell', 30_000);
    sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
    const row = await readRow();
    const opened = row.find((id) => !before.includes(id));
    await assertActiveHasInput(opened);
    const shown = await shownLeaves();
    runner.assert(row[row.indexOf(anchor) + 1] === opened, '⌘D opens beside the active column', { row, anchor, opened });
    runner.assert(shown.includes(anchor) && shown.includes(opened), '⌘D keeps the anchor on screen beside the new tile', { shown, anchor, opened });
    runner.assert(shown.length === shownBefore.length, 'the screen keeps its tile count', { shown, shownBefore });
    await waitForChip(shownBefore[0]);
    await hold();
    await shot('cmd-d-at-capacity');
  });

  await runner.step('cmd_w_keeps_focus_in_the_slot', async () => {
    const row = await readRow();
    const closing = row[1];
    await slideTo(closing);
    await assertActiveHasInput(closing);
    const shownBefore = await shownLeaves();
    const slot = shownBefore.indexOf(closing);
    await hold();
    await press('terminal.close');
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${closing}"]`, { absent: true });
    const after = await shownLeaves();
    const active = await activeLeaf();
    runner.log('after ⌘W', { row, closing, shownBefore, after, active });
    runner.assert(after[slot] === active, '⌘W leaves focus in the slot', { after, slot, active });
    await assertActiveHasInput(active);
    await hold();
  });

  await runner.step('carry_and_send_through_native_keys', async () => {
    const before = await readRow();
    const active = await activeLeaf();
    const at = before.indexOf(active);
    runner.assert(at < before.length - 1, 'there is a column to carry past', { before, active });
    await press('shelf.carryRight');
    const carried = await waitForRow((current) => current.indexOf(active) === at + 1);
    runner.log('carried', { before, carried });
    await assertActiveHasInput(active);
    await hold();

    const shownBefore = await shownLeaves();
    const slot = shownBefore.indexOf(active);
    await press('shelf.sendToShelf');
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${active}"]`, { absent: true });
    await waitForChip(active);
    const next = await activeLeaf();
    const shown = await shownLeaves();
    runner.assert(shown[slot] === next, '⇧⌥⌘↑ leaves focus in the slot', { shown, slot, next });
    await assertActiveHasInput(next);
    await hold();
    await shot('sent-to-shelf');

    await driver.click(`[data-testid="shelf-chip-${active}"]`);
    await assertActiveHasInput(active);
    const brought = await shownLeaves();
    runner.assert(brought.includes(next) && brought.includes(active), 'a chip brings the tile beside the active one, which stays on screen', { brought, next, active });
    await hold();
  });

  await runner.step('attn_open_while_the_user_types_keeps_the_text_in_the_terminal', async () => {
    const typing = await activeLeaf();
    const typingSession = await sessionOf(typing);
    await waitForPaneShellReady(client, typingSession, typing);
    const planPath = path.join(runner.sessionDir, 'plan.md');
    fs.writeFileSync(planPath, '# The plan\n\nAn agent opened this.\n');
    const before = await readRow();
    const shownBefore = await shownLeaves();
    await driver.typeText('echo first');
    const env = instanceCliEnv(instance, { ATTN_TERMINAL_ID: observer.terminalOf(typingSession) });
    const opened = execFileAsync(appDaemonInTree(options.appPath), ['open', planPath, '--session', typingSession], { env });
    await driver.typeText('half');
    await opened;
    await driver.typeText('second');
    const row = await waitForRow((current) => current.length === before.length + 1);
    const documentLeaf = row.find((id) => !before.includes(id));
    runner.log('document opened', { documentLeaf, row, shownBefore, shown: await shownLeaves() });
    await waitForActive(typing);
    await waitForPaneInputFocus(client, typingSession, typing);
    await waitForPaneText(client, typingSession, typing, (text) => text.includes('echo firsthalfsecond'), 'every typed key landed in the original terminal');
    runner.assert((await activeLeaf()) === typing, 'the document takes no focus', { typing });
    await hold();
    await shot('document-beside-the-agent');
    await driver.pressKey('c', { control: true });

    await closeLeafThroughTheChipOrTile(documentLeaf);
    await waitForRow((current) => current.length === before.length);
    await waitForActive(typing);
    const shownAfter = await shownLeaves();
    runner.assert(shownAfter.join() === shownBefore.join(), 'closing the document restores the screen', { shownBefore, shownAfter });
    await hold();
  });

  await runner.step('cmd_j_brings_an_unseen_document_and_closing_restores_the_screen', async () => {
    const row = await readRow();
    const shownBefore = await shownLeaves();
    const offScreen = row.find((id) => !shownBefore.includes(id));
    runner.assert(Boolean(offScreen), 'a terminal sits on the shelf', { row, shownBefore });
    const active = await activeLeaf();
    const planPath = path.join(runner.sessionDir, 'waiting.md');
    fs.writeFileSync(planPath, '# Waiting\n\nFrom an agent on the shelf.\n');
    const owner = await sessionOf(offScreen);
    const env = instanceCliEnv(instance, { ATTN_TERMINAL_ID: observer.terminalOf(owner) });
    await execFileAsync(appDaemonInTree(options.appPath), ['open', planPath, '--session', owner], { env });
    const withDocument = await waitForRow((current) => current.length === row.length + 1);
    const documentLeaf = withDocument.find((id) => !row.includes(id));
    await waitForChip(documentLeaf, '.is-needs');
    runner.assert((await shownLeaves()).join() === shownBefore.join(), 'the screen stays as it was', { shownBefore });
    runner.assert((await activeLeaf()) === active, 'focus stays as it was', { active });
    await hold();
    await shot('document-waiting-on-the-shelf');

    await press('session.jumpToWaiting');
    await waitForActive(documentLeaf);
    const brought = await shownLeaves();
    runner.assert(brought.includes(active) && brought.includes(documentLeaf), '⌘J brings the document beside the active tile', { brought, active, documentLeaf });
    await waitDom(`[data-testid="shelf-chip-${documentLeaf}"]`, { absent: true });
    await hold();
    await shot('document-brought');

    await press('terminal.close');
    await waitForRow((current) => current.length === row.length);
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${documentLeaf}"]`, { absent: true });
    await waitForActive(active);
    const shownAfter = await shownLeaves();
    runner.assert(shownAfter.join() === shownBefore.join(), 'closing the document restores the screen', { shownBefore, shownAfter });
    await assertActiveHasInput(active);
    await hold();
    await shot('screen-restored');
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}

async function waitForRow(predicate) {
  const state = await waitForSessionDesktop(client, firstSession, (current) => predicate(leavesOf(current.desktop.layoutTree)), 'the expected row');
  return leavesOf(state.desktop.layoutTree);
}

async function closeLeafThroughTheChipOrTile(leafId) {
  const onScreen = (await shownLeaves()).includes(leafId);
  if (!onScreen) await driver.click(`[data-testid="shelf-chip-${leafId}"]`);
  await request('dom_click', { selector: `${surface()} .desktop-pane[data-pane-id="${leafId}"] [aria-label="Close tile"]` });
}
