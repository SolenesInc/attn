#!/usr/bin/env node
import path from 'node:path';
import { launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessInstance } from './harnessInstance.mjs';
import { createWindowDriver } from './platform.mjs';
import { sleep } from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '0';
assertFreshWorldTargetSafe({ instance: currentHarnessInstance(), appPath: options.appPath });
const runner = createScenarioRunner(options, { scenarioId: 'HOME-DOCK', tier: 'local', prefix: 'home-dock' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });

// Native input in the existing harness takes 250ms per action; this is its
// standard 15s hung-UI guard, not a delay for panel transitions.
const waitDom = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const click = async selector => {
  const [{ bounds }, { logicalBounds }, { innerWidth, innerHeight }] = await Promise.all([
    client.request('dom_bounds', { selector }),
    client.request('get_window_bounds'),
    client.request('get_terminal_context_menu_state'),
  ]);
  await driver.clickWindow(
    (Math.max(0, logicalBounds.width - innerWidth) / 2 + bounds.x + bounds.width / 2) / logicalBounds.width,
    (Math.max(0, logicalBounds.height - innerHeight) + bounds.y + bounds.height / 2) / logicalBounds.height,
  );
};
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
    await click('[aria-label="Show Automations"]');
    await waitDom('.side-panel-shell.is-open [data-testid="automations-panel"]');
    const { innerWidth } = await client.request('get_terminal_context_menu_state');
    const fits = (rect) => rect.width > 0 && rect.x >= 0 && rect.x + rect.width <= innerWidth;
    const settleBy = Date.now() + 5_000;
    let { bounds } = await client.request('dom_bounds', { selector: '[data-testid="automations-panel"]' });
    while (!fits(bounds) && Date.now() < settleBy) {
      await sleep(50);
      ({ bounds } = await client.request('dom_bounds', { selector: '[data-testid="automations-panel"]' }));
    }
    runner.assert(fits(bounds), 'the panel slides in to fit inside the window', { bounds, innerWidth });
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
    await click('[data-testid="crew-panel-close"]');
    await waitDom('.crew-panel-layer.is-open', { absent: true });
    await waitDom('[aria-label="Hide Automations"]');
    await click('[data-testid="automation-new"]');
    await waitDom('[data-testid="automation-form-name"]');
    await click('[data-testid="automation-form-name"]');
    await waitDom('[data-testid="automation-form-name"]', { focused: true });
    await driver.typeText('Home dock draft');
    const value = await client.request('dom_value', { selector: '[data-testid="automation-form-name"]' });
    runner.assert(value.value === 'Home dock draft', 'the dock takes keyboard input', { value });
    await click('[data-testid="automation-form-close"]');
    await driver.pressKey('Escape');
    await waitDom('[aria-label="Show Automations"]');
    await screenshot('home-after-escape');
  });

  await runner.step('attention_also_opens_over_home', async () => {
    await pressShortcutKeys(client, driver, 'dock.attention');
    await waitDom('.side-panel-shell.is-open .attention-drawer-panel');
    await screenshot('attention-over-home');
    await click('.side-panel-shell.is-open .drawer-close');
    await waitDom('.side-panel-shell.is-open .attention-drawer-panel', { absent: true });
    runner.assert((await client.request('home_get_state')).onScreen, 'home remains usable after closing the dock');
  });

  await runner.step('garden_keeps_its_dismissal_order_after_fullscreen', async () => {
    await pressShortcutKeys(client, driver, 'dock.attention');
    await click('[aria-label="Show the garden"]');
    await waitDom('.garden-frame.is-dock');
    await click('[aria-label="Expand the garden"]');
    await waitDom('.garden-frame.is-full');
    await click('[aria-label="Return the garden to the dock"]');
    await waitDom('.garden-frame.is-dock');
    await driver.pressKey('Escape');
    await waitDom('[aria-label="Show the garden"]');
    await waitDom('[aria-label="Hide PRs Drawer"]');
    await screenshot('garden-closed-before-attention');
    await driver.pressKey('Escape');
    await waitDom('[aria-label="Show PRs Drawer"]');
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
