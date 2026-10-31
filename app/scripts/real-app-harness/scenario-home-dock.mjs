#!/usr/bin/env node
import path from 'node:path';
import { launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessInstance } from './harnessInstance.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '0';
assertFreshWorldTargetSafe({ instance: currentHarnessInstance(), appPath: options.appPath });
const runner = createScenarioRunner(options, { scenarioId: 'HOME-DOCK', tier: 'local', prefix: 'home-dock' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ garden: '.garden-frame', drawer: '.side-panel-shell.is-open' }) });

// The standard 15s hung-UI guard, not a delay for panel transitions.
const waitDom = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const screenshot = async name => {
  const windowId = await driver.mainWindowId();
  await driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId });
};

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

try {
  await runner.step('start_on_home', async () => {
    await launchFreshAppAndConnect(client, observer);
    const { logicalBounds } = await client.request('get_window_bounds');
    await client.request('set_window_bounds', { logicalBounds: { ...logicalBounds, x: 0, y: 0 } });
    await driver.activateApp();
    await pressShortcutKeys(client, driver, 'session.goToDashboard');
    await waitDom('.view-container.visible .dashboard');
    runner.writeJson('initial-home.json', {
      home: await client.request('home_get_state'),
      sessions: [...observer.sessionsById.keys()],
    });
  });

  await runner.step('star_opens_a_clickable_panel_over_home', async () => {
    await driver.click('[aria-label="Show Automations"]');
    await waitDom('.side-panel-shell.is-open [data-testid="automations-panel"]');
    const { innerWidth } = await client.request('get_terminal_context_menu_state');
    const { bounds } = await driver.pointOf('[data-testid="automations-panel"]');
    runner.assert(bounds.width > 0 && bounds.x >= 0 && Math.round(bounds.x + bounds.width) <= innerWidth, 'the panel slides in to fit inside the window', { bounds, innerWidth });
    await screenshot('automations-over-home');
    const home = await client.request('home_get_state');
    runner.assert(home.onScreen, 'opening the dock keeps home on screen', { home });
    await pressShortcutKeys(client, driver, 'ui.commandPalette');
    await waitDom('[role="combobox"][aria-label="Commands"]', { focused: true });
    await driver.typeText('Manage crew');
    await waitDom('.unified-palette-option', { textIncludes: 'Manage crew' });
    await driver.pressEnter();
    await waitDom('[data-testid="crew-panel-close"]', { focused: true });
    await screenshot('crew-over-dock');
    await driver.click('[data-testid="crew-panel-close"]');
    await waitDom('.crew-panel-layer.is-open', { absent: true });
    await waitDom('[aria-label="Hide Automations"]');
    await driver.click('[data-testid="automation-new"]');
    await driver.typeInto('[data-testid="automation-form-name"]', 'Home dock draft');
    await waitDom('[data-testid="automation-form-name"]', { valueEquals: 'Home dock draft' });
    await driver.click('[data-testid="automation-form-close"]');
    await driver.pressKey('Escape');
    await waitDom('[aria-label="Show Automations"]');
    await screenshot('home-after-escape');
  });

  await runner.step('attention_also_opens_over_home', async () => {
    await pressShortcutKeys(client, driver, 'dock.attention');
    await waitDom('.side-panel-shell.is-open .attention-drawer-panel');
    await screenshot('attention-over-home');
    await driver.click('.side-panel-shell.is-open .drawer-close');
    await waitDom('.side-panel-shell.is-open .attention-drawer-panel', { absent: true });
    runner.assert((await client.request('home_get_state')).onScreen, 'home remains usable after closing the dock');
  });

  await runner.step('garden_keeps_its_dismissal_order_after_fullscreen', async () => {
    await pressShortcutKeys(client, driver, 'dock.attention');
    await driver.click('[aria-label="Show the garden"]');
    await waitDom('.garden-frame.is-dock');
    await driver.click('[aria-label="Expand the garden"]');
    await waitDom('.garden-frame.is-full');
    await driver.click('[aria-label="Return the garden to the dock"]');
    await waitDom('.garden-frame.is-dock');
    await driver.pressKey('Escape');
    await waitDom('[aria-label="Show the garden"]');
    await waitDom('.side-panel-shell.is-open .attention-drawer-panel');
    await screenshot('garden-closed-before-attention');
    await driver.pressKey('Escape');
    await waitDom('.side-panel-shell.is-open .attention-drawer-panel', { absent: true });
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
