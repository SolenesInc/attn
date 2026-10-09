#!/usr/bin/env node

import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'CLOSE-DESKTOP', tier: 'tier1-local-shell', prefix: 'close-desktop',
  metadata: { focus: 'Native close shortcut closes an empty desktop with the launcher focused and selects its neighbour' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

try {
  await runner.step('launch_app', () => launchFreshAppAndConnect(client, observer));
  const keeper = await observer.profileCommand('desktop_create', { profile_id: observer.profileId, shortcut_slot: 0 });
  const home = keeper.desktops[0].id;
  let empty;
  await runner.step('show_empty_desktop', async () => {
    const result = await observer.profileCommand('desktop_create', { profile_id: observer.profileId, shortcut_slot: 0 });
    empty = result.desktops[0];
    await client.request('select_desktop', { desktopId: empty.id });
    await client.request('dom_wait', { selector: '[data-testid="empty-desktop-launcher"]', timeoutMs: 5_000 });
    const input = await client.request('dom_wait', { selector: '[data-testid="location-picker-path-input"]', focused: true, timeoutMs: 5_000 });
    runner.writeJson('launcher-input.json', input);
  });
  await runner.step('native_close_shortcut', async () => {
    const closed = observer.waitForMessage((event) => event.event === 'profile_arrangement_changed'
      && !event.desktops.some((desktop) => desktop.id === empty.id) && event, 'empty desktop removed');
    await pressShortcutKeys(client, driver, 'session.close');
    await closed;
    await client.request('dom_wait', { selector: `[data-testid="close-desktop-${empty.id}"]`, absent: true, timeoutMs: 5_000 });
    runner.assert(observer.currentDesktopId() === home, 'closing selects the desktop above', { home, current: observer.currentDesktopId() });
    runner.writeJson('closed-arrangement.json', observer.describeArrangement());
    await driver.screenshot(`${runner.runDir}/closed-desktop.png`, { windowId: await driver.mainWindowId() });
  });
  await runner.step('close_a_nonempty_desktop_through_confirmation', async () => {
    const sessionId = await createSessionAndWaitForInitialPane({ client, observer, cwd: runner.sessionDir, label: 'Close desktop evidence', agent: 'shell', ownDesktop: false });
    const desktop = observer.desktopOf(sessionId);
    observer.send({ cmd: 'set_setting', key: 'queue_mode_enabled', value: 'false' });
    await client.request('dom_wait', { selector: `[data-testid="close-desktop-${desktop.id}"]`, timeoutMs: 5_000 });
    await client.request('dom_click', { selector: `[data-testid="close-desktop-${desktop.id}"]` });
    await client.request('dom_wait', { selector: '#desktop-close-title', timeoutMs: 5_000 });
    const text = await client.request('dom_text', { selector: '.mp-dialog' });
    runner.assert(text.text.includes('0 agents, 1 shell and 0 tiles'), 'confirmation counts the shell', text);
    await driver.screenshot(`${runner.runDir}/close-confirmation.png`, { windowId: await driver.mainWindowId() });
    const closed = observer.waitForMessage((event) => event.event === 'profile_arrangement_changed'
      && !event.desktops.some((entry) => entry.id === desktop.id) && event, 'nonempty desktop removed');
    const ended = observer.waitForMessage((event) => event.event === 'session_unregistered'
      && event.session?.id === sessionId && event, 'session closed');
    await client.request('dom_click', { selector: '.mp-dialog .mp-button.primary' });
    await Promise.all([closed, ended]);
  });
  await runner.finishSuccess({ desktopId: empty.id });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
