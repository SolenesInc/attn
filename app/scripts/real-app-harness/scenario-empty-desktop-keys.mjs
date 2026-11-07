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
  metadata: { agent: 'shell', focus: 'the row keys walk past a row end onto an empty desktop and off it again through AppKit' },
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
const keptEmpty = [];

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', async () => {
  await request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
});
runner.registerCleanup('close_sessions', async () => {
  for (const sessionId of [...sessions].reverse()) await request('close_session', { sessionId }).catch(() => {});
});
runner.registerCleanup('close_empty_desktops', async () => {
  for (const id of keptEmpty) {
    const desktop = observer.desktop(id);
    if (desktop) await observer.profileCommand('desktop_close', { desktop_id: desktop.id, expected_revision: desktop.revision });
  }
});
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ launcher: LAUNCHER }) });

function desktopIdsInOrder() {
  return [...observer.desktops].sort((a, b) => (a.order_key < b.order_key ? -1 : a.order_key > b.order_key ? 1 : 0)).map((desktop) => desktop.id);
}

function desktopAfter(desktopId, key) {
  const order = desktopIdsInOrder();
  return order[(order.indexOf(desktopId) + STEPS[key] + order.length) % order.length];
}

async function screenState() {
  const state = await request('get_state');
  const desktopId = state.arrangement.currentDesktopId;
  return { desktopId, tiles: observer.desktop(desktopId).panes.length, activeTile: state.activeLeaf?.leafId ?? null };
}

async function settledOn(desktopId, activeTile) {
  await observer.waitFor(() => observer.currentDesktopId() === desktopId, `current desktop ${desktopId}`);
  const desktop = observer.desktop(desktopId);
  if (!desktop.tree_json) {
    await waitDom(LAUNCHER);
    await waitDom(LAUNCHER_INPUT, { focused: true });
    return;
  }
  await waitDom(LAUNCHER, { absent: true });
  const pane = desktop.panes.find((entry) => entry.pane_id === (activeTile ?? desktop.active_pane_id));
  const holdsOnlyTiles = desktop.panes.length === 0;
  if (!holdsOnlyTiles) await waitForPaneInputFocus(client, pane.session_id, pane.pane_id);
}

async function createKeptEmptyDesktop(name) {
  const created = await observer.profileCommand('desktop_create', { profile_id: observer.profileId, name, shortcut_slot: 0 });
  const { id } = created.desktops[0];
  keptEmpty.push(id);
  await observer.waitFor(() => observer.desktop(id), `${name} in the arrangement`);
  return id;
}

async function press(key, expected) {
  const before = await screenState();
  await pressShortcutKeys(client, driver, `terminal.focus${key}`);
  await settledOn(expected.desktopId, expected.activeTile);
  const after = await screenState();
  walk.push({ key, before, after });
  runner.assert(after.desktopId === expected.desktopId, `${key} lands on the expected desktop`, { key, before, after, expected });
  if (expected.activeTile) runner.assert(after.activeTile === expected.activeTile, `${key} lands on the expected tile`, { key, before, after, expected });
}

try {
  let home;
  let empty;
  let beyond;
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
    empty = await createKeptEmptyDesktop('Empty neighbour');
    beyond = await createKeptEmptyDesktop('Far neighbour');
    const others = desktopIdsInOrder().filter((id) => id !== empty && id !== beyond);
    others.splice(others.indexOf(home) + 1, 0, empty, beyond);
    await observer.profileCommand('desktop_set_order', { profile_id: observer.profileId, desktop_ids: others });
    await observer.waitFor(() => desktopAfter(home, 'Right') === empty && desktopAfter(empty, 'Right') === beyond, 'the empty neighbours right after the row');
  });

  await runner.step('walk', async () => {
    await request('select_desktop', { desktopId: home });
    await settledOn(home);
    await waitDom(`[data-session-terminal-desktop="${home}"]`);
    if ((await screenState()).activeTile === firstTile) await press('Right', { desktopId: home, activeTile: lastTile });

    await press('Right', { desktopId: empty });
    await press('Right', { desktopId: beyond });
    await press('Left', { desktopId: empty });
    await press('Left', { desktopId: home, activeTile: lastTile });

    await press('Right', { desktopId: empty });
    await press('Up', { desktopId: home, activeTile: lastTile });
    await request('select_desktop', { desktopId: empty });
    await settledOn(empty);
    await press('Down', { desktopId: beyond });
  });

  runner.writeJson('walk.json', walk);
  await runner.finishSuccess({ keysPressed: walk.length });
} catch (error) {
  runner.writeJson('walk.json', walk);
  await runner.finishFailure(error);
  process.exitCode = 1;
}
