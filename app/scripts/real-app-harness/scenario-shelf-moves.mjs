#!/usr/bin/env node
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import { PNG } from 'pngjs';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import { sleep, waitForFirstDesktopPane, waitForPaneInputFocus, waitForPaneShellReady, waitForPaneText, waitForSessionDesktop } from './scenarioAssertions.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const WARNING_RGB = [245, 158, 11];
const PIXEL_NOISE = 2;
const execFileAsync = promisify(execFile);
const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const runner = createScenarioRunner(options, {
  scenarioId: 'SHELF-MOVES',
  tier: 'tier1-local-shell',
  prefix: 'shelf-moves',
  metadata: { agent: 'shell', focus: 'opening at capacity, closing, carrying, sending to the shelf, an agent opening a document while the user types, across a width change, and while its desktop is unmounted, opening it again from the shelf, and bringing it with the jump key, and how chips that need the user look beside plain ones, through AppKit keys with real terminals' },
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
const shownInRowOrder = async () => {
  const [row, shown] = await Promise.all([readRow(), shownLeaves()]);
  return row.filter((id) => shown.includes(id));
};
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
runner.registerCleanup('close_row_desktop', async () => {
  if (!observer.desktop(desktop) || observer.desktops.length === 1) return;
  await observer.waitFor(() => observer.desktop(desktop).panes.length === 0, 'no terminals left on the row');
  const { revision } = observer.desktop(desktop);
  await observer.profileCommand('desktop_close', { desktop_id: desktop, expected_revision: revision });
});
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
    await slideTo((await shownLeaves()).at(-1));
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

  await runner.step('a_document_sent_to_the_shelf_waits_when_its_agent_opens_it_again', async () => {
    const owner = await offScreenLeaf();
    const documentLeaf = await agentOpensDocument(owner, 'again.md');
    await waitForChip(documentLeaf, '.is-needs');
    await press('session.jumpToWaiting');
    await waitForActive(documentLeaf);
    await press('shelf.sendToShelf');
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${documentLeaf}"]`, { absent: true });
    await waitForChip(documentLeaf, ':not(.is-needs)');
    await slideTo((await shownLeaves()).at(-1));
    const active = await activeLeaf();
    const shownBefore = await shownLeaves();

    await agentRunsOpen(owner, 'again.md');

    await waitForChip(documentLeaf, '.is-needs');
    runner.assert((await shownLeaves()).join() === shownBefore.join(), 'the screen stays as it was', { shownBefore });
    runner.assert((await activeLeaf()) === active, 'focus stays as it was', { active });
    await hold();
    await shot('document-waiting-again');

    await press('session.jumpToWaiting');
    await waitForActive(documentLeaf);
    const brought = await shownLeaves();
    runner.assert(brought.includes(active) && brought.includes(documentLeaf), '⌘J brings the document beside the active tile', { brought, active, documentLeaf });
    await press('terminal.close');
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${documentLeaf}"]`, { absent: true });
    await waitForActive(active);
  });

  await runner.step('more_tiles_reveal_an_agent_whose_document_then_shows', async () => {
    await narrowScreenToTwoTiles();
    await slideTo((await readRow())[0]);
    await slideTo((await shownLeaves()).at(-1));
    const active = await activeLeaf();
    const narrow = await shownLeaves();
    runner.assert(narrow.at(-1) === active, 'the active tile sits at the end of the screen', { narrow, active });
    await press('shelf.moreTiles');
    const wide = await waitForShownCount((count) => count === narrow.length + 1);
    const revealed = wide.find((id) => !narrow.includes(id));
    runner.assert(wide.indexOf(revealed) > wide.indexOf(active), 'more tiles reveals an agent after the active tile', { wide, active, revealed });
    const row = await readRow();
    const documentLeaf = await agentOpensDocument(revealed, 'revealed.md');
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${documentLeaf}"]`);
    const shown = await shownLeaves();
    runner.assert(shown.includes(active) && shown.includes(revealed) && shown.includes(documentLeaf), 'the document shows beside its newly revealed agent, with the active tile on screen', { shown, active, revealed, documentLeaf });
    runner.assert((await activeLeaf()) === active, 'the document takes no focus', { active });
    await shot('document-of-a-revealed-agent');
    await closeLeafThroughTheChipOrTile(documentLeaf);
    await waitForRow((current) => current.length === row.length);
    await waitForActive(active);
    await hold();
  });

  await runner.step('a_document_opened_while_the_desktop_is_unmounted_still_waits', async () => {
    const owner = await offScreenLeaf();
    const active = await activeLeaf();
    const firstAway = await observer.createDesktop();
    const secondAway = await observer.createDesktop();
    await request('select_desktop', { desktopId: firstAway.id });
    await waitDom(`[data-session-terminal-desktop="${firstAway.id}"]`);
    await request('select_desktop', { desktopId: secondAway.id });
    await waitDom(`[data-session-terminal-desktop="${secondAway.id}"]`);
    await waitDom(surface(), { absent: true });
    const documentLeaf = await agentOpensDocument(owner, 'unmounted.md');
    await request('select_desktop', { desktopId: desktop });
    await waitForActive(active);
    await waitForChip(documentLeaf, '.is-needs');
    await hold();
    await shot('document-waiting-after-unmount');
    await press('session.jumpToWaiting');
    await waitForActive(documentLeaf);
    const brought = await shownLeaves();
    runner.assert(brought.includes(active) && brought.includes(documentLeaf), '⌘J brings the document beside the active tile', { brought, active, documentLeaf });
    await hold();
    await shot('document-brought-after-unmount');
    await press('terminal.close');
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${documentLeaf}"]`, { absent: true });
  });

  await runner.step('a_brought_tile_replaces_a_neighbour_and_the_selected_tile_stays', async () => {
    await narrowScreenToTwoTiles();
    const [left] = await readRow();
    await slideTo(left);
    await waitForActive(left);
    await shot('bring-selected-on-the-left-before');
    const fromLeft = await offScreenLeaf();
    await driver.click(`[data-testid="shelf-chip-${fromLeft}"]`);
    await assertActiveHasInput(fromLeft);
    const afterLeft = await shownInRowOrder();
    runner.assert(afterLeft[0] === left && afterLeft[1] === fromLeft, 'the selected tile on the left stays and the right neighbour is replaced', { afterLeft, left, fromLeft });
    await hold();
    await shot('bring-selected-on-the-left-after');

    const right = fromLeft;
    await shot('bring-selected-on-the-right-before');
    const fromRight = await offScreenLeaf();
    await driver.click(`[data-testid="shelf-chip-${fromRight}"]`);
    await assertActiveHasInput(fromRight);
    const afterRight = await shownInRowOrder();
    runner.assert(afterRight[0] === fromRight && afterRight[1] === right, 'the selected tile on the right stays and the left neighbour is replaced', { afterRight, right, fromRight });
    await hold();
    await shot('bring-selected-on-the-right-after');
  });

  await runner.step('needing_the_user_shows_in_color_with_no_highlighted_border', async () => {
    const { logicalBounds } = await request('get_window_bounds');
    await request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1100 } });
    await slideTo((await readRow()).at(-1));
    const waitingAgent = await startAgentWaitingForTheUser();
    const row = await readRow();
    const beforeAgent = row[row.indexOf(waitingAgent) - 1];
    await slideTo(beforeAgent);
    await waitForChip(waitingAgent, '.is-needs');
    const secondFromTheScreen = row[row.indexOf(beforeAgent) - 2];
    for (const name of ['unseen-1.md', 'unseen-2.md', 'unseen-3.md', 'unseen-4.md']) await agentOpensDocument(secondFromTheScreen, name);
    const waitingChip = `[data-testid="shelf-chip-${waitingAgent}"]`;
    const unseenChip = '[data-testid="desktop-shelf"] > .desktop-shelf-chip.is-needs:has(.desktop-shelf-mark)';
    const needingCap = '.desktop-shelf-cap.is-needs';
    const plainChip = '[data-testid="desktop-shelf"] > .desktop-shelf-chip:not(.is-needs):not(.desktop-shelf-cap)';
    for (const selector of [unseenChip, needingCap, plainChip]) await waitDom(selector);
    await waitDom('[data-testid="desktop-shelf"] .is-arrival', { absent: true });
    runner.log('attention row', { row: await readRow(), shown: await shownLeaves(), waitingAgent });
    await shot('attention-on-the-shelf');

    const looks = await readShelfPixels();
    const plain = await looks.borderOf(plainChip);
    for (const [label, selector] of [['a waiting agent', waitingChip], ['an unseen document', unseenChip], ['a cap holding one', needingCap]]) {
      const border = await looks.borderOf(selector);
      runner.assert(sameColor(border, plain, PIXEL_NOISE), `${label} has the border of a plain chip`, { border, plain });
    }
    const agentDot = await looks.centerOf(`${waitingChip} .state-indicator`);
    runner.assert(sameColor(agentDot, WARNING_RGB), 'a waiting agent shows through its state dot', { agentDot });
    const documentMark = await looks.centerOf(`${unseenChip} .desktop-shelf-mark`);
    runner.assert(sameColor(documentMark, WARNING_RGB), 'an unseen document shows through its mark', { documentMark });
    runner.assert(await looks.hasColorIn(needingCap, WARNING_RGB), 'a cap holding one shows through its text', {});
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}

async function startAgentWaitingForTheUser() {
  const cwd = path.join(runner.sessionDir, 'waiting-agent');
  fs.mkdirSync(cwd, { recursive: true });
  writeMockAgentFixture(cwd, { name: 'waiting agent', turns: [{ includes: 'HAND_IT_BACK', actions: [{ type: 'reply', text: 'Your call', state: 'waiting_input' }] }] });
  const sessionId = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: 'Waiting agent', agent: 'claude', ownDesktop: false, promptReadyFn: ensureClaudePromptReadyViaPty });
  sessions.push(sessionId);
  const { paneId } = await waitForFirstDesktopPane(client, sessionId, 'waiting agent tile');
  await request('write_pane', { sessionId, paneId, text: 'HAND_IT_BACK', submit: true });
  await observer.waitFor(() => observer.getSession(sessionId)?.state === 'waiting_input', 'the agent waits for the user');
  return paneId;
}

function sameColor(actual, expected, tolerance = 40) {
  return actual.every((value, index) => Math.abs(value - expected[index]) <= tolerance);
}

async function readShelfPixels() {
  const capture = await request('capture_screenshot_data', { selector: '[data-testid="desktop-shelf"]' });
  const image = PNG.sync.read(Buffer.from(capture.pngBase64, 'base64'));
  const scale = image.width / capture.bounds.width;
  const pixel = (x, y) => {
    const at = (Math.floor((y - capture.bounds.y) * scale) * image.width + Math.floor((x - capture.bounds.x) * scale)) * 4;
    return [...image.data.subarray(at, at + 3)];
  };
  const boundsOf = async (selector) => (await request('dom_bounds', { selector })).bounds;
  return {
    borderOf: async (selector) => {
      const box = await boundsOf(selector);
      return pixel(box.x + 0.5, box.y + box.height / 2);
    },
    centerOf: async (selector) => {
      const box = await boundsOf(selector);
      return pixel(box.x + box.width / 2, box.y + box.height / 2);
    },
    hasColorIn: async (selector, color) => {
      const box = await boundsOf(selector);
      for (let y = box.y + 2; y < box.y + box.height - 2; y += 1 / scale) {
        for (let x = box.x + 2; x < box.x + box.width - 2; x += 1 / scale) {
          if (sameColor(pixel(x, y), color)) return true;
        }
      }
      return false;
    },
  };
}

async function waitForShownCount(predicate) {
  for (const deadline = Date.now() + 15_000; Date.now() < deadline; await sleep(50)) {
    const shown = await shownLeaves();
    if (predicate(shown.length)) return shown;
  }
  throw new Error(`the screen never reached the expected tile count: ${JSON.stringify(await shownLeaves())}`);
}

async function narrowScreenToTwoTiles() {
  for (let shown = (await shownLeaves()).length; shown > 2; shown -= 1) {
    await press('shelf.fewerTiles');
    await waitForShownCount((count) => count === shown - 1);
  }
}

async function offScreenLeaf() {
  const shown = await shownLeaves();
  const owner = (await desktopState()).desktop.panes.map((pane) => pane.paneId).find((id) => !shown.includes(id));
  runner.assert(Boolean(owner), 'an agent sits on the shelf', { shown });
  return owner;
}

async function agentRunsOpen(ownerLeaf, fileName) {
  const planPath = path.join(runner.sessionDir, fileName);
  fs.writeFileSync(planPath, `# ${fileName}\n\nAn agent opened this.\n`);
  const owner = await sessionOf(ownerLeaf);
  const env = instanceCliEnv(instance, { ATTN_TERMINAL_ID: observer.terminalOf(owner) });
  await execFileAsync(appDaemonInTree(options.appPath), ['open', planPath, '--session', owner], { env });
}

async function agentOpensDocument(ownerLeaf, fileName) {
  const before = await readRow();
  await agentRunsOpen(ownerLeaf, fileName);
  const row = await waitForRow((current) => current.length === before.length + 1);
  return row.find((id) => !before.includes(id));
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
