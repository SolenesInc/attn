#!/usr/bin/env node
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { promisify } from 'node:util';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { runShellCommandInPane, waitForPaneInputFocus, waitForSessionDesktop } from './scenarioAssertions.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const TILES = 5;
const PEEK_OPEN_DELAY_MS = 180;
const execFileAsync = promisify(execFile);
const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const runner = createScenarioRunner(options, {
  scenarioId: 'SHELF-PEEK',
  tier: 'tier1-local-shell',
  prefix: 'shelf-peek',
  metadata: { agent: 'shell', focus: 'hovering a chip shows a live terminal in a panel, a reply reaches it without resizing it or moving the layout, a browser tile hides and returns, a chip that reflows under a still pointer opens nothing, and a stopped terminal keeps the reply' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const instance = currentHarnessInstance();
const request = (action, payload) => client.request(action, payload);
const waitDom = (selector, extra = {}) => request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const shot = async (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const sessions = [];
let firstSession;
let desktop;

let resolveClick = () => {};
let pageClick = new Promise((resolve) => { resolveClick = resolve; });
const server = http.createServer((incoming, response) => {
  if (new URL(incoming.url, 'http://127.0.0.1').pathname === '/click') {
    resolveClick();
    response.end();
    return;
  }
  response.setHeader('content-type', 'text/html');
  response.end('<title>Peek browser</title><body style="margin:0;background:#264f78;height:100vh"><script>document.addEventListener("click",()=>fetch("/click"))</script>');
});

const PEEK = '[data-testid="shelf-peek"]';
const INPUT = '[data-testid="shelf-peek-input"]';
const SCREEN = '[data-testid="shelf-peek-screen"]';
const NOTICE = '[data-testid="shelf-peek-notice"]';
const leavesOf = (node) => (node.type === 'split' ? node.children.flatMap(leavesOf) : [node.paneId ?? node.tileId]);
const readRow = async () => leavesOf((await request('get_desktop', { sessionId: firstSession })).desktop.layoutTree);
const shownLeaves = async () => (await request('get_desktop_ui_state', { desktopId: desktop })).shownLeafIds;
const surface = () => `[data-session-terminal-desktop="${desktop}"]`;
const chip = (leafId) => `[data-testid="shelf-chip-${leafId}"]`;

async function focusLeaf(leafId) {
  const leaves = await readRow();
  const activeAt = async () => leaves.indexOf((await request('get_state')).activeLeaf.leafId);
  for (let at = await activeAt(); leaves[at] !== leafId; at = await activeAt()) {
    const forward = at < leaves.indexOf(leafId);
    await request('dispatch_shortcut', { shortcutId: forward ? 'terminal.focusRight' : 'terminal.focusLeft' });
    await waitDom(`${surface()}[data-active-leaf-id="${leaves[at + (forward ? 1 : -1)]}"]`);
  }
  await waitDom(`${surface()}[data-active-leaf-id="${leafId}"] .desktop-pane[data-pane-id="${leafId}"]`);
}

async function hoverAway() {
  await driver.hover(`${surface()} .desktop-pane[data-pane-id="${await shownLeaves().then((shown) => shown[0])}"]`);
}

async function assertNothingMoved(name, shown, active) {
  runner.assert((await shownLeaves()).join() === shown.join(), `${name}: the screen did not change`, { shown });
  await waitDom(`${surface()}[data-active-leaf-id="${active}"]`);
}

runner.registerCleanup('close_server', () => new Promise((resolve) => {
  server.close(resolve);
  server.closeAllConnections();
}));
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', async () => {
  await request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
  await request('set_setting', { key: 'shelf_layout_enabled', value: 'false' });
});
runner.registerCleanup('close_sessions', async () => {
  for (const sessionId of [...sessions].reverse()) await request('close_session', { sessionId }).catch(() => {});
});
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ shelf: '[data-testid="desktop-shelf"]', peek: PEEK }) });

try {
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  let row;
  let target;
  let targetSize;
  let targetSession;

  await runner.step('build_a_row_with_the_setting_on', async () => {
    await launchFreshAppAndConnect(client, observer);
    await request('set_setting', { key: 'shelf_layout_enabled', value: 'true' });
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    const cwd = path.join(runner.sessionDir, 'row');
    fs.mkdirSync(cwd, { recursive: true });
    firstSession = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `peek-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(firstSession);
    await request('select_session', { sessionId: firstSession });
    for (let count = 2; count <= TILES; count += 1) {
      await request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
      const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === count && current.panes.every((pane) => pane.runtimeId), `${count} shells`, 30_000);
      sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
    }
    desktop = (await request('get_desktop', { sessionId: firstSession })).desktopId;
    const { logicalBounds } = await request('get_window_bounds');
    await request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1600, height: 900 } });
    row = await readRow();
    target = row.at(-1);
    await focusLeaf(target);
    await waitForPaneInputFocus(client, firstSession, target);
    targetSession = (await request('get_desktop', { sessionId: firstSession })).panes.find((pane) => pane.paneId === target).sessionId;
    const output = await runShellCommandInPane(client, { sessionId: targetSession, paneId: target }, 'echo "size $(stty size)"', 'size ');
    targetSize = String(output).match(/size (\d+ \d+)/)[1];
    runner.log('the target terminal before it leaves the screen', { target, targetSize });
    await focusLeaf(row[0]);
    await waitDom(chip(target));
  });

  await runner.step('hover_shows_the_terminal_without_moving_anything', async () => {
    const shown = await shownLeaves();
    await driver.hover(chip(target));
    await waitDom(PEEK);
    await waitDom(SCREEN, { textIncludes: `size ${targetSize}` });
    await shot('peek-open');
    await assertNothingMoved('peek open', shown, row[0]);
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${target}"]`, { absent: true });
    await driver.pressKey('Escape');
    await waitDom(PEEK, { absent: true });
    await hoverAway();
    await assertNothingMoved('after Esc', shown, row[0]);
  });

  await runner.step('a_reply_reaches_the_terminal_at_its_size', async () => {
    const shown = await shownLeaves();
    await driver.hover(chip(target));
    await waitDom(PEEK);
    await driver.typeInto(INPUT, 'echo "peek-reply $(stty size)"');
    await shot('peek-reply-typed');
    await driver.hover('[data-testid="shelf-live-dots"]');
    await driver.pressEnter();
    await waitDom(PEEK, { absent: true });
    await waitForPaneInputFocus(client, firstSession, row[0]);
    await assertNothingMoved('after a reply', shown, row[0]);
    await driver.hover(chip(target));
    await waitDom(SCREEN, { textIncludes: `peek-reply ${targetSize}` });
    await shot('peek-reply-arrived');
    await driver.pressKey('Escape');
    await waitDom(PEEK, { absent: true });
    await hoverAway();
  });

  await runner.step('enter_with_no_reply_opens_the_tile', async () => {
    await driver.hover(chip(target));
    await waitDom(PEEK);
    await driver.click(INPUT);
    await driver.pressEnter();
    await waitDom(PEEK, { absent: true });
    await waitDom(`${surface()}[data-active-leaf-id="${target}"] .desktop-pane[data-pane-id="${target}"]`);
    await waitForPaneInputFocus(client, firstSession, target);
    row = await readRow();
    target = row.at(-1);
    targetSession = (await request('get_desktop', { sessionId: firstSession })).panes.find((pane) => pane.paneId === target).sessionId;
    await focusLeaf(row[0]);
    await waitDom(chip(target));
  });

  await runner.step('a_chip_that_reflows_under_a_still_pointer_opens_nothing', async () => {
    await focusLeaf(row[2]);
    await waitDom(chip(row[0]));
    await waitDom(chip(row[3]));
    await driver.click(chip(row[4]));
    await waitDom(`${surface()}[data-active-leaf-id="${row[4]}"]`);
    await waitDom(`${chip(row[3])}:hover`);
    await request('dom_wait', { selector: PEEK, timeoutMs: PEEK_OPEN_DELAY_MS * 5 }).then(
      () => { throw new Error('a peek opened under the still pointer'); },
      (error) => { if (!String(error).includes('dom_wait timeoutMs')) throw error; },
    );
    await shot('still-pointer-after-chip-click');
    await hoverAway();
    await driver.hover(chip(row[3]));
    await waitDom(`${PEEK}[data-leaf-id="${row[3]}"]`);
    await driver.pressKey('Escape');
    await waitDom(PEEK, { absent: true });
    await hoverAway();
    row = await readRow();
    target = row.at(-1);
    targetSession = (await request('get_desktop', { sessionId: firstSession })).panes.find((pane) => pane.paneId === target).sessionId;
    await focusLeaf(row[0]);
    await waitDom(chip(target));
  });

  await runner.step('a_sidebar_row_of_an_off_screen_tile_peeks', async () => {
    await driver.hover(`[data-testid="sidebar-session-${targetSession}"]`);
    await waitDom(`${PEEK}[data-leaf-id="${target}"]`);
    await shot('peek-from-sidebar');
    await driver.pressKey('Escape');
    await waitDom(PEEK, { absent: true });
    await hoverAway();
  });

  await runner.step('a_chip_in_the_queue_bar_peeks', async () => {
    await request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await waitDom('.sidebar:not(.collapsed), [data-testid="queue-bar"]');
    await request('dispatch_shortcut', { shortcutId: 'session.toggleSidebar' });
    await waitDom(`[data-testid="queue-bar"] ${chip(target)}`);
    await driver.hover(`[data-testid="queue-bar"] ${chip(target)}`);
    await waitDom(`${PEEK}[data-leaf-id="${target}"]`);
    await shot('peek-from-queue-bar');
    await driver.pressKey('Escape');
    await waitDom(PEEK, { absent: true });
    await hoverAway();
    await request('dispatch_shortcut', { shortcutId: 'session.toggleSidebar' });
    await waitDom('[data-testid="queue-bar"]', { absent: true });
    await request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    await waitDom('.sidebar:not(.collapsed)');
    await waitDom('[data-testid="title-bar"] [data-testid="desktop-shelf"]');
  });

  await runner.step('a_browser_tile_hides_for_the_panel_and_returns', async () => {
    await focusLeaf(row[0]);
    const env = instanceCliEnv(instance, { ATTN_TERMINAL_ID: observer.terminalOf(firstSession) });
    await execFileAsync(appDaemonInTree(options.appPath), ['browser', 'open', `http://127.0.0.1:${server.address().port}/`, '--session', firstSession], { env });
    const host = `${surface()} .browser-tile-host`;
    await waitDom(host);
    await focusLeaf(row[0]);
    await waitDom(`${surface()} .desktop-pane[data-pane-id="${(await readRow()).find((id) => !row.includes(id))}"]`);
    await waitDom(chip(target));
    await shot('browser-before-peek');

    await driver.hover(chip(target));
    await waitDom(PEEK);
    await shot('browser-under-peek');
    const { receipt } = await driver.hover({ selector: host, at: { x: 0.5, y: 0.97 } });
    runner.assert(receipt.matches, 'with the panel open the app takes the pointer over the tile, so the webview is hidden', { receipt });
    await waitDom(PEEK, { absent: true });

    pageClick = new Promise((resolve) => { resolveClick = resolve; });
    await driver.click({ selector: host, at: { x: 0.5, y: 0.5 } }, { withoutReceipt: 'the browser page takes it' });
    await pageClick;
    runner.log('the page saw a click after the panel closed');
    await shot('browser-after-peek');
    await request('dom_click', { selector: `${surface()} [data-tile-kind="browser"] [aria-label="Close tile"]` });
    await waitDom(host, { absent: true });
  });

  await runner.step('a_stopped_terminal_keeps_the_reply_and_the_tile_offers_it', async () => {
    await focusLeaf(row[0]);
    await waitDom(chip(target));
    await driver.hover(chip(target));
    await waitDom(PEEK);
    await driver.typeInto(INPUT, 'exit 1');
    await driver.hover('[data-testid="shelf-live-dots"]');
    await driver.pressEnter();
    await waitDom(PEEK, { absent: true });
    await hoverAway();
    await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.some((pane) => pane.paneId === target), 'the stopped tile stays', 15_000);

    await driver.hover(chip(target));
    await waitDom(NOTICE, { textIncludes: 'Agent stopped', timeoutMs: 30_000 });
    await driver.typeInto(INPUT, 'keep this reply');
    await driver.hover('[data-testid="shelf-live-dots"]');
    await driver.pressEnter();
    await waitDom(`${PEEK} ${INPUT}`);
    runner.assert((await request('dom_value', { selector: INPUT })).value === 'keep this reply', 'Enter keeps the reply when the agent stopped');
    await shot('peek-stopped-reply-kept');
    await driver.pressKey('Escape');
    await waitDom(PEEK, { absent: true });
    await hoverAway();

    await driver.hover(chip(target));
    await waitDom(PEEK);
    runner.assert((await request('dom_value', { selector: INPUT })).value === 'keep this reply', 'reopening the panel shows the kept reply');
    await driver.click(`${PEEK} .shelf-peek-reply button:nth-of-type(1)`);
    await waitDom(PEEK, { absent: true });
    await waitDom(`.desktop-pane[data-pane-id="${target}"] .desktop-agent-stopped`);
    await waitDom('[data-testid="unsent-peek-reply"]', { textIncludes: 'keep this reply' });
    await shot('tile-offers-unsent-reply');
    await driver.click('[data-testid="unsent-peek-reply"] button:nth-of-type(2)');
    await waitDom('[data-testid="unsent-peek-reply"]', { absent: true });
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
