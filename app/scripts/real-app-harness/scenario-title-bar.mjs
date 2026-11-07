#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { waitForSessionDesktop } from './scenarioAssertions.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const TILES = 5;
const BAND_PX = 32;
const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const runner = createScenarioRunner(options, {
  scenarioId: 'TITLE-BAR',
  tier: 'tier1-local-shell',
  prefix: 'title-bar',
  metadata: { agent: 'shell', focus: 'native clicks reach every control of the one toolbar, a chip opens its peek, a tile drops on the shelf, and tiles start below the band' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const request = (action, payload) => client.request(action, payload);
const waitDom = (selector, extra = {}) => request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const shot = async (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const sessions = [];
let firstSession;
let desktop;

const BAND = '[data-testid="title-bar"]';
const LEFT = '[data-testid="title-bar-left"]';
const RIGHT = '[data-testid="title-bar-right"]';
const PEEK = '[data-testid="shelf-peek"]';
const surface = () => `[data-session-terminal-desktop="${desktop}"]`;
const chip = (leafId) => `${BAND} [data-testid="shelf-chip-${leafId}"]`;
const leavesOf = (node) => (node.type === 'split' ? node.children.flatMap(leavesOf) : [node.paneId ?? node.tileId]);
const readRow = async () => leavesOf((await request('get_desktop', { sessionId: firstSession })).desktop.layoutTree);
const shownLeaves = async () => (await request('get_desktop_ui_state', { desktopId: desktop })).shownLeafIds;
const paneSelector = (leafId) => `${surface()} .desktop-pane[data-pane-id="${leafId}"]`;

const closeWithEscape = async (opened) => {
  await driver.pressKey('Escape');
  await waitDom(opened, { absent: true });
};
const CONTROLS = [
  { name: 'new-agent', button: `${LEFT} [aria-label="New agent"]`, opened: '.location-picker-overlay', close: closeWithEscape },
  { name: 'commands', button: `${LEFT} button[aria-label="Commands"]`, opened: '[role="combobox"][aria-label="Commands"]', close: closeWithEscape },
  { name: 'notifications', button: `${LEFT} [aria-label="Show Notifications"]`, opened: '.notifications-panel', close: async (opened) => {
    await driver.click({ selector: '.notifications-panel-backdrop', at: { x: 0.5, y: 0.9 } });
    await waitDom(opened, { absent: true });
  } },
  { name: 'garden', button: `${RIGHT} [aria-label="Show the garden"]`, opened: '.garden-frame.is-dock', close: async (opened) => {
    await driver.click(`${RIGHT} [aria-label="Hide the garden"]`);
    await waitDom(opened, { absent: true });
  } },
  { name: 'notebook', button: `${RIGHT} [aria-label^="Open Notebook"]`, opened: '.notebook-browser-shell', close: closeWithEscape },
  { name: 'ledger', button: `${RIGHT} [aria-label^="Open Ledger"]`, opened: '.ledger-panel', close: closeWithEscape },
  { name: 'automations', button: `${RIGHT} [aria-label="Show Automations"]`, opened: `${RIGHT} [aria-label="Hide Automations"]`, close: async (opened) => {
    await driver.click(opened);
    await waitDom(opened, { absent: true });
  } },
  { name: 'asks', button: `${RIGHT} [aria-label*="ask" i]`, opened: `${RIGHT} [aria-label*="ask" i][aria-pressed="true"]`, close: async (opened) => {
    await driver.click(opened);
    await waitDom(opened, { absent: true });
  } },
  { name: 'profile', button: `${RIGHT} .title-bar-profile-pill`, opened: '.profile-switcher', close: closeWithEscape },
];

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', async () => {
  await request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
  await request('set_setting', { key: 'asks_enabled', value: 'false' });
});
runner.registerCleanup('close_sessions', async () => {
  for (const sessionId of [...sessions].reverse()) await request('close_session', { sessionId }).catch(() => {});
});
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ band: BAND, shelf: '[data-testid="desktop-shelf"]' }) });

async function assertPopUpSitsUnderItsSelector(selector, popUp) {
  const pill = (await request('dom_bounds', { selector })).bounds;
  const panel = (await request('dom_bounds', { selector: popUp })).bounds;
  runner.assert(panel.x < pill.x + pill.width && panel.x + panel.width > pill.x && panel.y >= pill.y + pill.height, 'the profile list opens under its selector', { pill, panel });
}

async function assertTilesStartBelowTheBand(name) {
  const leaf = (await shownLeaves())[0];
  await request('dom_native_point', { selector: paneSelector(leaf) });
  const { bounds } = await request('dom_bounds', { selector: paneSelector(leaf) });
  const band = (await request('dom_bounds', { selector: BAND })).bounds;
  runner.log(`${name} geometry`, { tileTop: bounds.y, bandTop: band.y, bandHeight: band.height });
  runner.assert(band.y === 0 && band.height === BAND_PX, `${name}: the band is ${BAND_PX}px at the window's top`, { band });
  runner.assert(bounds.y === BAND_PX, `${name}: tile top is ${BAND_PX}`, { bounds });
}

try {
  let row;

  await runner.step('build_a_row', async () => {
    await launchFreshAppAndConnect(client, observer);
    await request('set_setting', { key: 'asks_enabled', value: 'true' });
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    const cwd = path.join(runner.sessionDir, 'row');
    fs.mkdirSync(cwd, { recursive: true });
    firstSession = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `band-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(firstSession);
    await request('select_session', { sessionId: firstSession });
    for (let count = 2; count <= TILES; count += 1) {
      await request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
      const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === count && current.panes.every((pane) => pane.runtimeId), `${count} shells`, 30_000);
      sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
    }
    desktop = (await request('get_desktop', { sessionId: firstSession })).desktopId;
    const { logicalBounds } = await request('get_window_bounds');
    await request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1500, height: 900 } });
    row = await readRow();
    await waitDom(`${BAND} [data-testid="desktop-shelf"]`);
    await assertTilesStartBelowTheBand('sidebar open');
    await shot('band-sidebar-open');
  });

  await runner.step('native_clicks_reach_every_control', async () => {
    for (const { name, button, opened, close } of CONTROLS) {
      const receipt = await driver.click(button);
      runner.assert(receipt?.matches !== false, `${name}: the click lands on the control`, { receipt });
      await waitDom(opened);
      await shot(`control-${name}`);
      if (name === 'profile') await assertPopUpSitsUnderItsSelector(button, opened);
      await close(opened);
      await waitDom(button);
    }
  });

  await runner.step('a_chip_opens_its_peek', async () => {
    const shown = await shownLeaves();
    const target = row.find((id) => !shown.includes(id));
    await driver.hover(chip(target));
    await waitDom(PEEK);
    await shot('peek-from-band-chip');
    await driver.pressKey('Escape');
    await waitDom(PEEK, { absent: true });
    await driver.hover({ selector: `${paneSelector(shown.at(-1))} .desktop-pane-header`, at: { x: 0.7, y: 0.25 } });
  });

  await runner.step('a_tile_drops_on_the_shelf', async () => {
    const shown = await shownLeaves();
    const active = (await request('get_state')).activeLeaf.leafId;
    const tile = shown.find((id) => id !== active);
    const anchor = (await readRow()).find((id) => !shown.includes(id));
    await driver.drag(
      { selector: `${paneSelector(tile)} .desktop-pane-header`, at: { x: 0.7, y: 0.25 } },
      { selector: chip(anchor), at: { x: 0.1, y: 0.5 } },
      { steps: 24, withoutReceipt: 'a tile header cancels the press, so it has no mouse events' },
    );
    await waitDom(chip(tile));
    await waitDom(paneSelector(tile), { absent: true });
    const after = await readRow();
    runner.assert(after.indexOf(tile) === after.indexOf(anchor) - 1, 'the tile lands at that place in the row', { after, tile, anchor });
    await shot('tile-on-the-band-shelf');
  });

  await runner.step('the_toggle_and_flow_change_shape_in_the_band', async () => {
    await driver.click(`${LEFT} [aria-label="Collapse sidebar"]`);
    await waitDom('.sidebar.collapsed');
    await assertTilesStartBelowTheBand('rail');
    await shot('band-rail');
    await driver.click(`${LEFT} button[aria-label="Switch to queue flow"]`);
    await waitDom(`${BAND} [data-testid="queue-bar"] [data-testid="desktop-shelf"]`);
    await assertTilesStartBelowTheBand('queue bar');
    await shot('band-queue-bar');
    await driver.click(`${LEFT} [aria-label="Show sidebar"]`);
    await waitDom('[data-testid="queue-sidebar"]');
    await driver.click(`${LEFT} button[aria-label="Switch to desktop flow"]`);
    await waitDom('.sidebar:not(.collapsed):not(.queue-sidebar)');
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
