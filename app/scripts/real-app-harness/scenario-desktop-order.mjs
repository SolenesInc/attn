#!/usr/bin/env node
import path from 'node:path';
import { launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, printCommonHelp, queueDaemonSettingRestore } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  const options = parseCommonArgs(args);
  if (args.includes('--help') || args.includes('-h')) {
    printCommonHelp('scripts/real-app-harness/scenario-desktop-order.mjs');
    return;
  }
  const runner = createScenarioRunner(options, { scenarioId: 'DESKTOP-ORDER', tier: 'tier1-local-shell', prefix: 'desktop-order' });
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  let profile;
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('delete_profile', async () => {
    if (profile) {
      const state = await client.request('get_state');
      const current = state.arrangement.profiles.find((entry) => entry.id === profile.id);
      if (current) await observer.profileCommand('profile_delete', { profile_id: current.id, expected_revision: current.revision });
    }
  });
  const wait = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: observer.connectTimeoutMs, ...extra });
  const assertOrder = async (ids) => {
    for (let i = 1; i < ids.length; i++) {
      await wait(`[data-testid="sidebar-desktop-${ids[i-1]}"] + [data-testid="sidebar-desktop-${ids[i]}"]`);
    }
  };
  const capture = (name) => captureScreenshotData(path.join(runner.runDir, `${name}.png`), { client });
  try {
    await runner.step('create_desktops', async () => {
      await launchFreshAppAndConnect(client, observer);
      queueDaemonSettingRestore(observer, 'queue_mode_enabled');
      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
      profile = (await observer.profileCommand('profile_create', { name: `harness-order-${runner.runId}` })).profile;
      await observer.profileCommand('profile_select', { profile_id: profile.id });
      const one = `${profile.id}/desktop_1`;
      const create = async (slot, name = '') => (await observer.profileCommand('desktop_create', { profile_id: profile.id, shortcut_slot: slot, name })).desktops[0].id;
      await observer.profileCommand('desktop_rename', { desktop_id: one, name: 'One', expected_revision: 1 });
      const six = await create(6, 'Six');
      const nine = await create(9, 'Nine');
      const named10 = await create(0, 'Review 10');
      const named2 = await create(0, 'review 2');
      const alpha = await create(0, 'Alpha');
      runner.before = [nine, named10, six, named2, one, alpha];
      runner.sorted = [one, six, nine, alpha, named2, named10];
      runner.six = six;
      await observer.profileCommand('desktop_set_order', { profile_id: profile.id, desktop_ids: runner.before });
      await assertOrder(runner.before);
      await capture('before');
    });
    await runner.step('sort_from_palette', async () => {
      await pressShortcutKeys(client, driver, 'ui.commandPalette');
      await wait('[role="combobox"][aria-label="Commands"]', { focused: true });
      await driver.typeText('Sort desktops');
      await wait('.unified-palette-option[aria-selected="true"]', { textIncludes: 'Sort desktops' });
      const applied = observer.waitForMessage((m) => m.event === 'profile_arrangement_changed' && m.profile.id === profile.id && m, 'sorted desktop arrangement');
      await driver.pressEnter();
      const state = await applied;
      runner.assert(JSON.stringify(state.desktops.map((d) => d.id)) === JSON.stringify(runner.sorted), 'Sort applies numbered then case-insensitive numeric name order', state);
      await assertOrder(runner.sorted);
      await wait('.toast-row--action button small', { textIncludes: 'Undo', visible: true });
      await capture('sorted');
    });
    await runner.step('undo_restores_order', async () => {
      const restored = observer.waitForMessage((m) => m.event === 'profile_arrangement_changed' && m.profile.id === profile.id && m, 'Undo arrangement');
      const [{ bounds }, { logicalBounds }, { innerWidth, innerHeight }] = await Promise.all([
        client.request('dom_bounds', { selector: '.toast-row--action button' }),
        client.request('get_window_bounds'),
        client.request('get_terminal_context_menu_state'),
      ]);
      await driver.clickWindow(
        (Math.max(0, logicalBounds.width - innerWidth) / 2 + bounds.x + bounds.width / 2) / logicalBounds.width,
        (Math.max(0, logicalBounds.height - innerHeight) + bounds.y + bounds.height / 2) / logicalBounds.height,
      );
      const state = await restored;
      runner.assert(JSON.stringify(state.desktops.map((d) => d.id)) === JSON.stringify(runner.before), 'Undo restores the complete previous order', state);
      await assertOrder(runner.before);
      await capture('undo');
    });
    await runner.step('new_seven_follows_six', async () => {
      const seven = `${profile.id}/desktop_7`;
      const created = observer.waitForMessage((m) => m.event === 'profile_arrangement_changed' && m.profile.id === profile.id && m.desktops.some((d) => d.id === seven) && m, 'new desktop 7 arrangement');
      await pressShortcutKeys(client, driver, 'desktop.select7');
      const state = await created;
      const expected = [...runner.before];
      expected.splice(expected.indexOf(runner.six) + 1, 0, seven);
      runner.assert(JSON.stringify(state.desktops.map((d) => d.id)) === JSON.stringify(expected), 'new 7 lands immediately after 6 in the hand-arranged order', state);
      await assertOrder(expected);
      await capture('new-seven');
    });
    console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
  } catch (error) {
    await capture('failure').catch(() => {});
    const result = await runner.finishFailure(error);
    console.error(result.error);
    process.exitCode = 1;
  }
}
main().catch((error) => { console.error(error.stack || error); process.exitCode = 1; });
