#!/usr/bin/env node

import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { waitForPaneInputFocus } from './scenarioAssertions.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const runner = createScenarioRunner(options, {
  scenarioId: 'EMPTY-DESKTOP-KEYS',
  tier: 'tier1-local-shell',
  prefix: 'empty-desktop-keys',
  metadata: { agent: 'shell', focus: 'the row keys walk past a row end onto an empty desktop and off it again through AppKit, with the shelf layout off and on' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const request = (action, payload) => client.request(action, payload);
const waitDom = (selector, extra = {}) => request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const LAUNCHER = '[data-testid="empty-desktop-launcher"]';
const LAUNCHER_INPUT = '[data-testid="location-picker-path-input"]';
const STEPS = { Left: -1, Up: -1, Right: 1, Down: 1 };
const sessions = [];
const walk = [];
let keptEmpty;

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', async () => {
  await request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
  await request('set_setting', { key: 'shelf_layout_enabled', value: 'false' });
});
runner.registerCleanup('close_sessions', async () => {
  for (const sessionId of [...sessions].reverse()) await request('close_session', { sessionId }).catch(() => {});
});
runner.registerCleanup('close_empty_desktop', async () => {
  const desktop = observer.desktop(keptEmpty);
  if (desktop) await observer.profileCommand('desktop_close', { desktop_id: desktop.id, expected_revision: desktop.revision });
});
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ launcher: LAUNCHER }) });

function desktopAfter(desktopId, key) {
  const order = [...observer.desktops].sort((a, b) => (a.order_key < b.order_key ? -1 : a.order_key > b.order_key ? 1 : 0));
  const at = order.findIndex((desktop) => desktop.id === desktopId);
  return order[(at + STEPS[key] + order.length) % order.length].id;
}

async function screenState() {
  const state = await request('get_state');
  const desktopId = state.arrangement.currentDesktopId;
  return { desktopId, tiles: observer.desktop(desktopId).panes.length, activeTile: state.activeLeaf?.leafId ?? null };
}

async function settledOn(desktopId, activeTile) {
  await observer.waitFor(() => observer.currentDesktopId() === desktopId, `current desktop ${desktopId}`);
  const desktop = observer.desktop(desktopId);
  if (desktop.panes.length === 0) {
    await waitDom(LAUNCHER);
    await waitDom(LAUNCHER_INPUT, { focused: true });
    return;
  }
  await waitDom(LAUNCHER, { absent: true });
  const pane = desktop.panes.find((entry) => entry.pane_id === (activeTile ?? desktop.active_pane_id));
  await waitForPaneInputFocus(client, pane.session_id, pane.pane_id);
}

async function press(layout, key, expected) {
  const before = await screenState();
  await pressShortcutKeys(client, driver, `terminal.focus${key}`);
  await settledOn(expected.desktopId, expected.activeTile);
  const after = await screenState();
  walk.push({ layout, key, before, after });
  runner.assert(after.desktopId === expected.desktopId, `${key} lands on the expected desktop`, { layout, key, before, after, expected });
  if (expected.activeTile) runner.assert(after.activeTile === expected.activeTile, `${key} lands on the expected tile`, { layout, key, before, after, expected });
}

try {
  let home;
  let empty;
  let firstTile;
  let lastTile;

  await runner.step('launch_with_queue_flow_off', async () => {
    await launchFreshAppAndConnect(client, observer);
    await request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
  });

  await runner.step('build_a_row_beside_an_empty_desktop', async () => {
    const first = await createSessionAndWaitForInitialPane({ client, observer, cwd: runner.sessionDir, label: `row-${runner.runId}`, agent: 'shell' });
    sessions.push(first);
    home = observer.desktopOf(first).id;
    await request('select_session', { sessionId: first });
    await request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
    await observer.waitFor(() => observer.desktop(home).panes.length === 2, 'second tile on the row');
    sessions.push(observer.desktop(home).panes.find((pane) => pane.session_id !== first).session_id);
    firstTile = observer.desktop(home).panes.find((pane) => pane.session_id === first).pane_id;
    lastTile = observer.desktop(home).panes.find((pane) => pane.session_id !== first).pane_id;
    const created = await observer.profileCommand('desktop_create', { profile_id: observer.profileId, name: 'Empty neighbour', shortcut_slot: 0 });
    keptEmpty = created.desktops[0].id;
    await observer.waitFor(() => observer.desktop(keptEmpty), 'empty neighbour in the arrangement');
    empty = desktopAfter(home, 'Right');
    runner.assert(observer.desktop(empty).panes.length === 0, 'an empty desktop follows the row', JSON.parse(observer.describeArrangement()));
  });

  for (const [layout, setting] of [['off', 'false'], ['on', 'true']]) {
    await runner.step(`walk_with_shelf_layout_${layout}`, async () => {
      await request('set_setting', { key: 'shelf_layout_enabled', value: setting });
      await request('select_desktop', { desktopId: home });
      await settledOn(home);
      await waitDom(`[data-session-terminal-desktop="${home}"].shelf-layout`, layout === 'on' ? {} : { absent: true });
      if ((await screenState()).activeTile === firstTile) await press(layout, 'Right', { desktopId: home, activeTile: lastTile });

      await press(layout, 'Right', { desktopId: empty });
      const beyond = desktopAfter(empty, 'Right');
      await press(layout, 'Right', { desktopId: beyond });
      await press(layout, 'Left', { desktopId: empty });
      await press(layout, 'Left', { desktopId: home, activeTile: lastTile });

      await press(layout, 'Right', { desktopId: empty });
      await press(layout, 'Up', { desktopId: desktopAfter(empty, 'Up') });
      await request('select_desktop', { desktopId: empty });
      await settledOn(empty);
      await press(layout, 'Down', { desktopId: desktopAfter(empty, 'Down') });
    });
  }

  runner.writeJson('walk.json', walk);
  await runner.finishSuccess({ keysPressed: walk.length });
} catch (error) {
  runner.writeJson('walk.json', walk);
  await runner.finishFailure(error);
  process.exitCode = 1;
}
