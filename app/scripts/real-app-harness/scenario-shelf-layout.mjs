#!/usr/bin/env node
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { promisify } from 'node:util';
import { PNG } from 'pngjs';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { waitForPaneInputFocus, waitForPaneState, waitForSessionDesktop } from './scenarioAssertions.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const TILES = 7;
const CROWDED_DESKTOPS = 11;
const DESKTOP_SLOTS = 9;
const SMALLEST_WINDOW_WIDTH = 800;
const WIDTH_THAT_HOLDS_EVERY_DESKTOP_BUTTON = 1500;
const BAND = '[data-testid="title-bar"]';
const SHELF = `${BAND} [data-testid="desktop-shelf"]`;
const SHELF_INSET_PX = 12;
const PIXEL_NOISE = 2;
const execFileAsync = promisify(execFile);
const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const runner = createScenarioRunner(options, {
  scenarioId: 'SHELF-LAYOUT',
  tier: 'tier1-local-shell',
  prefix: 'shelf-layout',
  metadata: { agent: 'shell', focus: 'the row, the shelf and slide through AppKit keys with real terminals, in every sidebar shape, with a browser tile staying aligned through a slide' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const instance = currentHarnessInstance();
const request = (action, payload) => client.request(action, payload);
const waitDom = (selector, extra = {}) => request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const press = (shortcutId) => pressShortcutKeys(client, driver, shortcutId);
const shot = async (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const sessions = [];
let firstSession;
let desktop;

let resolveClick = () => {};
let pageClick = new Promise((resolve) => { resolveClick = resolve; });
const server = http.createServer((incoming, response) => {
  const url = new URL(incoming.url, 'http://127.0.0.1');
  if (url.pathname === '/click') {
    resolveClick(Object.fromEntries([...url.searchParams].map(([key, value]) => [key, Number(value)])));
    response.end();
    return;
  }
  response.setHeader('content-type', 'text/html');
  response.end('<title>Shelf browser</title><body style="margin:0;background:#264f78;height:100vh"><script>document.addEventListener("click",(event)=>fetch(`/click?x=${event.clientX}&y=${event.clientY}&w=${innerWidth}&h=${innerHeight}`))</script>');
});

const leavesOf = (node) => (node.type === 'split' ? node.children.flatMap(leavesOf) : [node.paneId ?? node.tileId]);
const readRow = async () => leavesOf((await request('get_desktop', { sessionId: firstSession })).desktop.layoutTree);
const shownLeaves = async () => (await request('get_desktop_ui_state', { desktopId: desktop })).shownLeafIds;
const surface = () => `[data-session-terminal-desktop="${desktop}"]`;

async function waitForActive(leafId) {
  await waitDom(`${surface()}[data-active-leaf-id="${leafId}"] .desktop-pane[data-pane-id="${leafId}"]`);
}

async function focusLeaf(leafId) {
  const row = await readRow();
  const activeAt = async () => row.indexOf((await request('get_state')).activeLeaf.leafId);
  for (let at = await activeAt(); row[at] !== leafId; at = await activeAt()) {
    await press(at < row.indexOf(leafId) ? 'terminal.focusRight' : 'terminal.focusLeft');
    await waitForActive(row[at + (at < row.indexOf(leafId) ? 1 : -1)]);
  }
}

async function setWindowWidth(width) {
  const { logicalBounds } = await request('get_window_bounds');
  await request('set_window_bounds', { logicalBounds: { ...logicalBounds, width, height: 900 } });
}

async function assertColsOfShownTerminals(name, narrowestCols) {
  const desktopState = await request('get_desktop', { sessionId: firstSession });
  for (const leafId of await shownLeaves()) {
    const pane = desktopState.panes.find((entry) => entry.paneId === leafId);
    if (!pane) continue;
    const state = await waitForPaneState(client, firstSession, leafId, (current) => current.pane?.size?.cols >= narrowestCols, `${name}: ${leafId} has at least ${narrowestCols} cols`).catch((error) => {
      runner.log(`${name} cols`, { leafId, error: String(error) });
      return request('get_pane_state', { sessionId: firstSession, paneId: leafId });
    });
    const bounds = await request('dom_bounds', { selector: `${surface()} .desktop-pane[data-pane-id="${leafId}"]` });
    const container = await request('dom_bounds', { selector: `${surface()} .desktop-pane[data-pane-id="${leafId}"] .terminal-container` });
    runner.log(`${name} terminal`, { leafId, size: state.pane?.size, pane: bounds.bounds.width, container: container.bounds.width });
    runner.assert(state.pane?.size?.cols >= narrowestCols, `${name}: a shown terminal has at least ${narrowestCols} cols`, { leafId, size: state.pane?.size, pane: bounds.bounds.width, container: container.bounds.width });
  }
}

async function assertScreen(name, row) {
  const shown = await shownLeaves();
  const start = row.indexOf(shown[0]);
  runner.assert(shown.length >= 1 && shown.length < row.length, `${name}: the screen shows part of the row`, { shown, row });
  runner.assert(shown.every((id, index) => row[start + index] === id), `${name}: the screen is neighbours in row order`, { shown, row });
  await waitDom('[data-pane-suspended]', { absent: true });
  runner.log(name, { shown });
  return shown;
}

async function walkTheRow(label) {
  const row = await readRow();
  await focusLeaf(row[0]);
  await assertScreen(`${label} at the row start`, row);
  for (const leafId of row.slice(1)) {
    await press('terminal.focusRight');
    await waitForActive(leafId);
  }
  const end = await assertScreen(`${label} at the row end`, row);
  runner.assert(end.at(-1) === row.at(-1), `${label}: ⌘⌥→ slides to the row's end`, { end, row });
  await waitForPaneInputFocus(client, firstSession, row.at(-1));
  await shot(`${label}-row-end`);
  for (const leafId of row.slice(0, -1).reverse()) {
    await press('terminal.focusLeft');
    await waitForActive(leafId);
  }
  const start = await assertScreen(`${label} back at the row start`, row);
  runner.assert(start[0] === row[0], `${label}: ⌘⌥← slides back to the row's start`, { start, row });
}

async function assertShelfStartsAtTheLeft(label, shelfSelector) {
  const shelf = (await request('dom_bounds', { selector: shelfSelector })).bounds;
  const first = (await request('dom_bounds', { selector: `${shelfSelector} > :first-child` })).bounds;
  runner.assert(first.x - shelf.x <= SHELF_INSET_PX, `${label}: the shelf group starts at the row's left edge`, { shelf, first });
}

async function assertShelfRowSitsAboveTheSidebar(label) {
  const shelf = (await request('dom_bounds', { selector: SHELF })).bounds;
  const panes = (await request('dom_bounds', { selector: `${surface()} .session-terminal-panes` })).bounds;
  const capture = await request('capture_screenshot_data', { selector: '.sidebar' });
  runner.assert(capture.bounds.y >= shelf.y + shelf.height, `${label}: the sidebar and its divider start below the shelf row`, { shelf, sidebar: capture.bounds });
  const image = PNG.sync.read(Buffer.from(capture.pngBase64, 'base64'));
  const pixel = (x, y) => {
    const at = (Math.round(y) * image.width + x) * 4;
    return [...image.data.subarray(at, at + 3)];
  };
  const y = panes.y + panes.height / 2 - capture.bounds.y;
  const besideTiles = { edge: pixel(image.width - 1, y), inside: pixel(image.width - 2, y) };
  runner.log(`${label} sidebar edge`, { besideTiles });
  const same = besideTiles.edge.every((value, index) => Math.abs(value - besideTiles.inside[index]) <= PIXEL_NOISE);
  runner.assert(!same, `${label}: the divider stays beside the tiles`, besideTiles);
}

async function stepTheWidth(label) {
  const shown = (await shownLeaves()).length;
  runner.assert(shown >= 2, `${label}: two tiles fit, so one fewer can show`, { shown });
  const labelSelector = '[data-testid="shelf-width-label"]';
  await press('shelf.fewerTiles');
  await waitDom(labelSelector, { textIncludes: `${shown - 1} tile` });
  const steps = (await request('dom_text', { selector: `${labelSelector} .desktop-shelf-width-steps` })).text;
  const counts = Array.from(steps.trim(), Number);
  const current = (await request('dom_text', { selector: `${labelSelector} .desktop-shelf-width-steps .is-on` })).text.trim();
  runner.assert(counts.every((count) => count >= 1) && counts.includes(shown) && counts.includes(shown - 1), `${label}: the label lists tile counts, not column widths`, { steps });
  runner.assert(current === String(shown - 1), `${label}: the label highlights the current tile count`, { current, shown });
  await shot(`${label}-width-label`);
  await press('shelf.moreTiles');
  await waitDom(labelSelector, { textIncludes: `${shown} tile` });
  await waitDom(labelSelector, { absent: true });
  runner.assert((await shownLeaves()).length === shown, `${label}: ⌘⌥= returns to the same tile count`);
}

async function clickEveryDesktopButtonInACrowdedBar() {
  const row = await readRow();
  for (let extra = 0; observer.desktops.length < CROWDED_DESKTOPS; extra += 1) {
    const cwd = path.join(runner.sessionDir, `crowd-${extra}`);
    fs.mkdirSync(cwd, { recursive: true });
    sessions.push(await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `shelf-crowd-${extra}-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false }));
  }
  await request('select_desktop', { desktopId: desktop });
  await waitDom('[data-testid="queue-bar-desktop-extras"]');
  const shelf = '[data-testid="queue-bar"] [data-testid="desktop-shelf"]';
  const bounds = async (selector) => (await request('dom_bounds', { selector })).bounds;
  const assertShelfStopsBefore = async (button, width) => {
    const end = await bounds(`${shelf} > :last-child`);
    const first = await bounds(button);
    runner.assert(end.x + end.width <= first.x, `at ${width}px the shelf stops before the desktop buttons`, { end, first });
  };

  await setWindowWidth(WIDTH_THAT_HOLDS_EVERY_DESKTOP_BUTTON);
  await focusLeaf(row[2]);
  await waitDom(shelf);
  await assertShelfStopsBefore('[data-testid="queue-bar-desktop-1"]', WIDTH_THAT_HOLDS_EVERY_DESKTOP_BUTTON);
  for (let slot = 1; slot <= DESKTOP_SLOTS; slot += 1) {
    const button = `[data-testid="queue-bar-desktop-${slot}"]`;
    await driver.click(button);
    await waitDom(`${button}.is-current`);
    await waitDom('[data-testid="shelf-menu-after"]', { absent: true });
    await waitDom('[data-testid="shelf-menu-before"]', { absent: true });
    await request('select_desktop', { desktopId: desktop });
    await waitDom(shelf);
  }

  await setWindowWidth(SMALLEST_WINDOW_WIDTH);
  await waitDom('[data-testid="queue-bar-desktops"] .queue-bar-desktop:not(.is-current):not(.is-extra)', { absent: true });
  await waitDom(`${shelf} [data-testid="shelf-cap-before"]`);
  await waitDom(`${shelf} [data-testid="shelf-cap-after"]`);
  await shot('queue-bar-crowded');
  await waitDom('[data-testid="queue-bar-desktops"] .queue-bar-desktop.is-current');
  await assertShelfStopsBefore('[data-testid="queue-bar-desktops"] > :first-child', SMALLEST_WINDOW_WIDTH);
  const pill = await bounds('[data-testid="queue-bar-pill"]');
  runner.assert(pill.width >= 40, 'the crowded bar keeps the waiting pill readable', { pill });
  await driver.click('[data-testid="queue-bar-desktop-extras"]');
  await waitDom('.desktop-overview');
  await driver.pressKey('Escape');
  await waitDom('.desktop-overview', { absent: true });

  await setWindowWidth(1900);
  await focusLeaf(row[0]);
  await driver.hover(`${surface()} .desktop-pane[data-pane-id="${row[0]}"] .terminal-container`);
}

runner.registerCleanup('close_server', () => new Promise((resolve) => {
  server.close(resolve);
  server.closeAllConnections();
}));
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_sessions', async () => {
  for (const sessionId of [...sessions].reverse()) await request('close_session', { sessionId }).catch(() => {});
});
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ shelf: '[data-testid="desktop-shelf"]' }) });

try {
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));

  await runner.step('launch', async () => {
    await launchFreshAppAndConnect(client, observer);
    queueDaemonSettingRestore(observer, 'queue_mode_enabled');
    await request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
  });

  await runner.step('build_a_row_of_real_shells', async () => {
    const cwd = path.join(runner.sessionDir, 'row');
    fs.mkdirSync(cwd, { recursive: true });
    firstSession = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `shelf-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(firstSession);
    await request('select_session', { sessionId: firstSession });
    for (let count = 2; count <= TILES; count += 1) {
      await request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
      const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === count && current.panes.every((pane) => pane.runtimeId), `${count} shells`, 30_000);
      sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
    }
    desktop = (await request('get_desktop', { sessionId: firstSession })).desktopId;
    await setWindowWidth(1900);
  });

  await runner.step('a_window_sweep_returns_to_the_same_screen', async () => {
    const row = await readRow();
    await focusLeaf(row[3]);
    const wide = await assertScreen('wide window', row);
    await assertColsOfShownTerminals('wide window', 66);
    await setWindowWidth(1000);
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${wide.find((id) => id !== row[3])}"]`, { absent: true });
    await assertScreen('narrow window', row);
    await setWindowWidth(1900);
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${wide.find((id) => id !== row[3])}"]`);
    const back = await shownLeaves();
    runner.assert(back.join() === wide.join(), 'a window that returns to its width shows the same screen', { wide, back });
  });

  await runner.step('sidebar_open', async () => {
    await waitDom('.sidebar:not(.collapsed)');
    await waitDom(SHELF);
    await walkTheRow('sidebar-open');
    await stepTheWidth('sidebar-open');
    await assertShelfStartsAtTheLeft('sidebar-open', SHELF);
    await assertShelfRowSitsAboveTheSidebar('sidebar-open');
    const shown = (await shownLeaves()).length;
    const clickWidthButton = async (name) => {
      await driver.hover('[data-testid="shelf-live-dots"]');
      await waitDom('[data-testid="shelf-width-control"]');
      const control = (await request('dom_bounds', { selector: '[data-testid="shelf-width-control"]' })).bounds;
      const shelf = (await request('dom_bounds', { selector: SHELF })).bounds;
      runner.assert(control.x >= shelf.x, 'the width control is not cut off at the row start', { control, shelf });
      await driver.click(`[data-testid="shelf-width-control"] [aria-label="${name}"]`);
    };
    await clickWidthButton('Fewer, wider tiles');
    await waitDom('[data-testid="shelf-width-label"]', { textIncludes: `${shown - 1} tile` });
    await shot('sidebar-open-width-control');
    await clickWidthButton('More, narrower tiles');
    await waitDom('[data-testid="shelf-width-label"]', { textIncludes: `${shown} tile` });
    await waitDom('[data-testid="shelf-width-label"]', { absent: true });
    runner.assert((await shownLeaves()).length === shown, 'sidebar-open: the width buttons return to the same tile count');
  });

  await runner.step('sidebar_collapsed', async () => {
    await press('session.toggleSidebar');
    await waitDom('.sidebar.collapsed');
    await waitDom(SHELF);
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    await walkTheRow('sidebar-collapsed');
    await stepTheWidth('sidebar-collapsed');
    await assertShelfStartsAtTheLeft('sidebar-collapsed', SHELF);
    await assertShelfRowSitsAboveTheSidebar('sidebar-collapsed');
  });

  await runner.step('queue_bar', async () => {
    await request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await waitDom('[data-testid="queue-bar"] [data-testid="desktop-shelf"]');
    await waitDom(`${BAND} .title-bar-middle`, { absent: true });
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    await walkTheRow('queue-bar');
    await stepTheWidth('queue-bar');
    await assertShelfStartsAtTheLeft('queue-bar', '[data-testid="queue-bar"] [data-testid="desktop-shelf"]');
    await clickEveryDesktopButtonInACrowdedBar();
    await press('session.toggleSidebar');
    await waitDom('[data-testid="queue-bar"]', { absent: true });
    await waitDom(SHELF);
    await assertShelfRowSitsAboveTheSidebar('queue-sidebar');
    await request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    await waitDom('.sidebar:not(.collapsed)');
  });

  await runner.step('a_browser_tile_stays_aligned_through_a_slide', async () => {
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    const before = await readRow();
    await focusLeaf(before[0]);
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${before[1]}"]`);
    const env = instanceCliEnv(instance, { ATTN_TERMINAL_ID: observer.terminalOf(firstSession) });
    await execFileAsync(appDaemonInTree(options.appPath), ['browser', 'open', `http://127.0.0.1:${server.address().port}/`, '--session', firstSession], { env });
    const hostSelector = `${surface()} .browser-tile-host`;
    await waitDom(hostSelector);
    const row = await readRow();
    const browserLeaf = row.find((id) => !before.includes(id));
    runner.log('row with the browser tile', { row, browserLeaf });

    const clickCentre = async (when) => {
      pageClick = new Promise((resolve) => { resolveClick = resolve; });
      await driver.click({ selector: hostSelector, at: { x: 0.5, y: 0.5 } }, { withoutReceipt: 'the browser page takes it' });
      const noClick = new Promise((_, reject) => {
        setTimeout(() => reject(new Error(`${when}: the page saw no click within 15000ms, so the webview is not on its tile`)), 15_000).unref();
      });
      const { x, y, w, h } = await Promise.race([pageClick, noClick]);
      runner.log(when, { x, y, w, h });
      const { bounds: host } = await request('dom_bounds', { selector: hostSelector });
      runner.assert(Math.abs(w - host.width) <= 1 && Math.abs(h - host.height) <= 1, `${when}: the webview has its tile's size`, { w, h, host });
      runner.assert(Math.abs(x - w / 2) <= 2 && Math.abs(y - h / 2) <= 2, `${when}: the webview sits on its tile`, { x, y, w, h });
    };

    await focusLeaf(row[0]);
    const shownBefore = await shownLeaves();
    runner.assert(shownBefore[0] === row[0] && shownBefore[1] === browserLeaf, 'the browser tile starts in the second slot', { row, shownBefore });
    await clickCentre('in the second slot');
    await shot('browser-before-slide');

    await driver.click(`${surface()} .desktop-pane[data-pane-id="${row[0]}"] .terminal-container`);
    await waitForPaneInputFocus(client, firstSession, row[0]);
    const slideTarget = row[row.indexOf(browserLeaf) + shownBefore.length - 1];
    for (const leafId of row.slice(1, row.indexOf(slideTarget) + 1)) {
      await press('terminal.focusRight');
      await waitForActive(leafId);
    }
    const shownAfter = await shownLeaves();
    runner.assert(shownAfter[0] === browserLeaf, 'the slide moved the browser tile to the first slot', { shownBefore, shownAfter });
    await clickCentre('after the slide');
    await shot('browser-after-slide');

    const elsewhereCwd = path.join(runner.sessionDir, 'elsewhere');
    fs.mkdirSync(elsewhereCwd, { recursive: true });
    const elsewhere = await createSessionAndWaitForInitialPane({ client, observer, cwd: elsewhereCwd, label: `shelf-elsewhere-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(elsewhere);
    await request('select_session', { sessionId: elsewhere });
    await waitDom(`${surface()}[data-session-visible="0"]`);
    await request('select_session', { sessionId: firstSession });
    await waitDom(`${surface()}[data-session-visible="1"]`);
    await clickCentre('back from another desktop');

    await request('dom_click', { selector: `${surface()} [data-tile-kind="browser"] [aria-label="Close tile"]` });
    await waitDom(hostSelector, { absent: true });
  });

  await runner.step('past_the_row_end_goes_to_the_next_desktop', async () => {
    const cwd = path.join(runner.sessionDir, 'next');
    fs.mkdirSync(cwd, { recursive: true });
    const next = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `shelf-next-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(next);
    await request('select_session', { sessionId: firstSession });
    const row = await readRow();
    await focusLeaf(row.at(-1));
    await press('terminal.focusRight');
    await waitDom(`${surface()}[data-session-visible="0"]`);
    runner.assert((await request('get_state')).arrangement.currentDesktopId !== desktop, '⌘⌥→ past the row end leaves the desktop');
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
