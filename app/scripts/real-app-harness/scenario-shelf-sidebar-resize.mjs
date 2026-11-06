#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, relaunchAppAndConnect } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { appLocalDataDirForInstance, currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { createWindowDriver } from './platform.mjs';
import { waitForSessionDesktop } from './scenarioAssertions.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const runner = createScenarioRunner(options, {
  scenarioId: 'SHELF-SIDEBAR-RESIZE',
  tier: 'tier1-local-shell',
  prefix: 'shelf-sidebar-resize',
  metadata: { agent: 'shell', focus: 'dragging the open sidebar by native pointer input in desktop and queue flow: the frozen tile area, the limits, Esc, the width keys, double-click, collapse and relaunch' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const request = (action, payload) => client.request(action, payload);
const press = (shortcutId) => pressShortcutKeys(client, driver, shortcutId);
const shot = async (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const waitDom = (selector, extra = {}) => request('dom_wait', { selector, timeoutMs: 15_000, ...extra });

const KEY_CODE_DIGIT_8 = 28;
const KEY_CODE_BRACKET_LEFT = 33;
const HANDLE = '.sidebar-resize-handle';
const LABEL = '.sidebar-resize-label';
const DRAG = { steps: 24, withoutReceipt: 'pointer capture sends the mouse-up to the handle, which has moved by then' };
const sessions = [];
let firstSession;
let desktop;

const instance = currentHarnessInstance();
const storedWidth = () => execFileSync(resolveHarnessResources(instance).appDaemon, ['settings', 'get', 'sidebar_width'], { encoding: 'utf8', env: instanceCliEnv(instance) }).trim();
const boxOf = async (selector) => (await request('dom_bounds', { selector })).bounds;
const sidebarPx = async () => (await boxOf('.sidebar')).width;
const tileAreaPx = async () => (await boxOf('.view-stack')).width;
const waitForLabel = (text) => waitDom(LABEL, { textIncludes: text });

async function waitUntil(read, ok, description) {
  let seen;
  for (let tries = 0; tries < 150; tries += 1) {
    seen = await read();
    if (ok(seen)) return seen;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`${description}: last saw ${JSON.stringify(seen)}`);
}
const waitForSidebarPx = (px) => waitUntil(sidebarPx, (seen) => seen === px, `sidebar at ${px}px`);
const waitForStored = (value) => waitUntil(storedWidth, (seen) => seen === String(value), `sidebar_width "${value}"`);

const shownPanes = async () => {
  const { shownLeafIds } = await request('get_desktop_ui_state', { desktopId: desktop });
  const { panes } = (await request('get_desktop', { sessionId: firstSession })).desktop;
  return shownLeafIds.map((leafId) => panes.find((pane) => pane.paneId === leafId)).filter(Boolean);
};
const terminalCols = async () => Object.fromEntries(await Promise.all((await shownPanes()).map(async ({ sessionId, paneId }) => [
  paneId,
  (await request('get_pane_state', { sessionId, paneId })).pane?.size?.cols,
])));

const sortedEntries = (record) => Object.entries(record).sort(([left], [right]) => left.localeCompare(right));
const logPath = () => path.join(appLocalDataDirForInstance(currentHarnessInstance()), 'debug', 'terminal-diagnostics.jsonl');
function fitsSince(startedAt) {
  const sizes = {};
  for (const line of fs.readFileSync(logPath(), 'utf8').split('\n').filter(Boolean)) {
    let event;
    try {
      event = JSON.parse(line);
    } catch {
      continue;
    }
    if (event.at < startedAt || event.kind !== 'resize' || event.source !== 'fit' || event.bail || event.toCols == null) continue;
    (sizes[event.pane] ??= new Set()).add(`${event.toCols}x${event.toRows}`);
  }
  return Object.fromEntries(Object.entries(sizes).map(([pane, seen]) => [pane, seen.size]));
}

async function dragChangesWidthOnRelease({ by, px, flow }) {
  const colsBefore = await terminalCols();
  const areaBefore = await tileAreaPx();
  const startedAt = Date.now();
  const drag = await driver.holdDrag(HANDLE, { by: { x: by, y: 0 } }, DRAG);
  await waitForLabel(`${px}px`);
  await waitForSidebarPx(px);
  runner.assert(JSON.stringify(await terminalCols()) === JSON.stringify(colsBefore), `${flow}: terminal cols hold mid-drag`, { colsBefore });
  runner.assert((await tileAreaPx()) === areaBefore, `${flow}: the tile area keeps its width mid-drag`, { areaBefore });
  await shot(`${flow}-mid-drag`);
  await drag.release();
  await waitForStored(px);
  const colsAfter = await waitUntil(terminalCols, (cols) => Object.keys(colsBefore).every((pane) => cols[pane] !== colsBefore[pane]), `${flow}: terminals fit after release`);
  runner.log(`${flow} cols`, { colsBefore, colsAfter });
  const fits = fitsSince(startedAt);
  runner.assert(Object.values(fits).every((count) => count === 1), `${flow}: each drawn terminal fits once`, { fits });
  runner.assert((await sidebarPx()) === px, `${flow}: the sidebar stays at ${px}px`, { px });
  const active = (await request('get_state')).activeLeaf;
  runner.assert((await request('get_pane_state', { sessionId: active.sessionId, paneId: active.leafId })).inputFocused, `${flow}: the active terminal keeps its input`, { active });
}

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', async () => {
  for (const [key, value] of [['sidebar_width', ''], ['queue_mode_enabled', 'false'], ['shelf_layout_enabled', 'false']]) {
    await request('set_setting', { key, value }).catch(() => {});
  }
});
runner.registerCleanup('close_sessions', async () => {
  for (const sessionId of [...sessions].reverse()) await request('close_session', { sessionId }).catch(() => {});
});
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ handle: HANDLE, sidebar: '.sidebar' }) });

try {
  await runner.step('launch_with_the_setting_on', async () => {
    await launchFreshAppAndConnect(client, observer);
    await request('set_setting', { key: 'shelf_layout_enabled', value: 'true' });
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    const { logicalBounds } = await request('get_window_bounds');
    await request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1700, height: 900 } });
  });

  await runner.step('open_two_terminals', async () => {
    const cwd = path.join(runner.sessionDir, 'row');
    fs.mkdirSync(cwd, { recursive: true });
    firstSession = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `sidebar-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(firstSession);
    await request('select_session', { sessionId: firstSession });
    desktop = (await request('get_desktop', { sessionId: firstSession })).desktopId;
    await press('terminal.splitVertical');
    const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === 2 && current.panes.every((pane) => pane.runtimeId), 'two shells', 30_000);
    sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
    await waitDom(HANDLE);
    await waitUntil(terminalCols, (cols) => new Set(Object.values(cols)).size === 1 && Object.keys(cols).length === 2, 'both terminals fit to half the row');
    runner.assert((await sidebarPx()) === 240, 'the sidebar starts at 240px');
  });

  await runner.step('drag_to_320_in_desktop_flow', async () => {
    await dragChangesWidthOnRelease({ by: 80, px: 320, flow: 'desktop' });
  });

  await runner.step('drag_stops_at_each_limit_and_esc_restores', async () => {
    const drag = await driver.holdDrag(HANDLE, { by: { x: -300, y: 0 } }, DRAG);
    await waitForLabel('200px is the narrowest');
    await waitForSidebarPx(200);
    await shot('narrowest');
    await drag.dragTo({ by: { x: 600, y: 0 } });
    await waitForLabel('600px is the widest');
    await waitForSidebarPx(600);
    await shot('widest');
    await driver.pressKey('Escape');
    await waitForSidebarPx(320);
    await drag.release();
    runner.assert(storedWidth() === '320', 'Esc saves nothing');

    const fitted = (await request('get_window_bounds')).logicalBounds;
    await request('set_window_bounds', { logicalBounds: { ...fitted, width: 900 } });
    const small = await driver.holdDrag(HANDLE, { by: { x: 300, y: 0 } }, DRAG);
    await waitForLabel('420px is the widest: tiles keep 480px');
    await waitForSidebarPx(420);
    await driver.pressKey('Escape');
    await small.release();
    await request('set_window_bounds', { logicalBounds: fitted });
    await waitForSidebarPx(320);
  });

  await runner.step('collapse_and_expand_return_to_the_width', async () => {
    await press('session.toggleSidebar');
    await waitDom('.sidebar.collapsed');
    await waitDom(HANDLE, { absent: true });
    await press('session.toggleSidebar');
    await waitDom(HANDLE);
    await waitForSidebarPx(320);
  });

  await runner.step('keys_step_the_width_from_a_terminal', async () => {
    const active = (await request('get_state')).activeLeaf;
    runner.assert((await request('get_pane_state', { sessionId: active.sessionId, paneId: active.leafId })).inputFocused, 'the terminal holds the keyboard', { active });
    await press('sidebar.narrower');
    await waitForStored(280);
    await waitForSidebarPx(280);
    await waitForLabel('280px');
    await press('sidebar.wider');
    await waitForStored(320);
    await waitForSidebarPx(320);
  });

  await runner.step('double_click_returns_to_240', async () => {
    await driver.click(HANDLE);
    await driver.click(HANDLE, { clickCount: 2 });
    await waitForSidebarPx(240);
    await waitForStored('');
  });

  await runner.step('drag_in_queue_flow', async () => {
    await request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await waitDom('[data-testid="sidebar-queue"]');
    await waitDom(HANDLE);
    await dragChangesWidthOnRelease({ by: 80, px: 320, flow: 'queue' });
    await request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    await waitDom('[data-testid="sidebar-queue"]', { absent: true });
    runner.assert((await sidebarPx()) === 320, 'switching flow keeps the width');
  });

  await runner.step('relaunch_shows_the_stored_width', async () => {
    const colsBefore = await terminalCols();
    const fitted = (await request('get_window_bounds')).logicalBounds;
    const startedAt = Date.now();
    await relaunchAppAndConnect(client, observer);
    await waitDom(HANDLE);
    await waitForSidebarPx(320);
    await request('set_window_bounds', { logicalBounds: fitted });
    await request('select_session', { sessionId: firstSession });
    const cols = await waitUntil(terminalCols, (seen) => Object.keys(colsBefore).every((pane) => seen[pane]), 'terminals drawn after relaunch');
    await shot('after-relaunch');
    runner.assert(JSON.stringify(sortedEntries(cols)) === JSON.stringify(sortedEntries(colsBefore)), 'terminals come back at the cols of the stored width', { colsBefore, cols });
    const fits = fitsSince(startedAt);
    runner.log('relaunch', { cols, fits });
    runner.assert(Object.values(fits).every((count) => count === 1), 'no terminal fits at a second size', { fits });
  });

  if (process.platform === 'darwin') {
    await runner.step('a_digit_that_types_a_bracket_moves_the_tile_and_the_bracket_key_steps_the_width', async () => {
      const option = ['command', 'option'];
      await request('native_key', { keyCode: KEY_CODE_BRACKET_LEFT, characters: '˙', modifiers: option });
      await waitForStored(280);
      const { sessionId } = (await request('get_state')).activeLeaf;
      await request('native_key', { keyCode: KEY_CODE_DIGIT_8, characters: '[', modifiers: option });
      await waitUntil(
        async () => {
          const { desktopId } = await request('get_desktop', { sessionId });
          return observer.desktops.find((candidate) => candidate.id === desktopId)?.shortcut_slot;
        },
        (slot) => slot === 8,
        'the tile on desktop 8',
      );
      runner.assert(storedWidth() === '280', 'the digit changes no width');
    });
  }

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
