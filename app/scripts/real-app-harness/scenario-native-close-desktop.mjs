#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-CLOSE-DESKTOP', tier: 'tier1-local-shell', prefix: 'native-close-desktop',
  metadata: { focus: 'The native close key closes an empty desktop while its launcher shows, and the app shows the desktop above it; a desktop holding a shell closes from its sidebar row only through a confirmation, which ends the shell' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const keys = process.platform === 'darwin'
  ? { select: (n) => `mod+${n}`, close: 'mod+w' }
  : { select: (n) => `mod+shift+${n}`, close: 'mod+shift+w' };

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  const made = [];
  const before = observer.currentDesktopId();
  if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
  runner.registerCleanup('close_desktops', async () => {
    for (const id of made) {
      const left = observer.desktop(id);
      if (!left) continue;
      await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
      await observer.waitFor(() => !observer.desktop(id), `desktop ${id} closed`);
    }
  });

  let keeper;
  let empty;
  await runner.step('an_empty_desktop_below_another_shows_its_launcher', async () => {
    for (const name of ['Keeper', 'Empty']) {
      const desktop = await observer.createDesktop();
      made.push(desktop.id);
      await observer.profileCommand('desktop_rename', { desktop_id: desktop.id, name });
    }
    [keeper, empty] = made.map((id) => observer.desktop(id));
    runner.assert(empty.shortcut_slot, `The run needs a free desktop shortcut:\n${observer.describeArrangement()}`, observer.describeArrangement());
    const order = observer.desktops.map((desktop) => desktop.id);
    runner.assert(order.indexOf(empty.id) === order.indexOf(keeper.id) + 1, 'the empty desktop sits right below the keeper', order);
    await observer.setCurrentDesktop(keeper.id);
    await driver.state('profiles', { includes: keeper.id });
    await driver.pressKey(keys.select(empty.shortcut_slot));
    await driver.state('profiles', { includes: empty.id });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  await runner.step('the_close_key_closes_it_and_shows_the_desktop_above', async () => {
    await driver.pressKey(keys.close);
    await observer.waitFor(() => !observer.desktop(empty.id), 'the empty desktop closed');
    await driver.waitFor({ key: `sidebar.desktop.${empty.id}` }, { gone: true });
    await driver.state('profiles', { includes: keeper.id });
    runner.assert(observer.currentDesktopId() === keeper.id, 'closing shows the desktop above', observer.describeArrangement());
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  await runner.step('close_a_nonempty_desktop_through_confirmation', async () => {
    const desktop = await observer.createDesktop();
    made.push(desktop.id);
    const id = crypto.randomUUID();
    const cwd = path.join(runner.sessionDir, 'held');
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'the shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'held', cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_held', () => observer.sessionsById.has(id) && observer.unregisterSession(id));
    await observer.waitForDesktopOf(id, (entry) => entry.id === desktop.id && entry.panes.length === 1, 'the shell on its desktop');
    await driver.hover({ key: `sidebar.desktop.${desktop.id}` });
    await driver.click({ key: `sidebar.desktop.${desktop.id}.close` });
    const dialog = await driver.waitFor({ key: 'close_desktop-dialog' });
    runner.assert(/^Close .+\?$/.test(dialog.name ?? ''), 'the confirmation names the desktop', dialog);
    runner.assert(observer.desktop(desktop.id), 'nothing closes before the confirmation', observer.describeArrangement());
    await driver.screenshot(path.join(runner.runDir, 'close-confirmation.png'));
    const ended = observer.waitForMessage((message) => message.event === 'session_unregistered' && message.session?.id === id && message, 'the shell ended');
    await driver.click({ key: 'close_desktop.confirm' });
    await observer.waitFor(() => !observer.desktop(desktop.id), 'the desktop closed');
    await ended;
    await driver.waitFor({ key: 'close_desktop-dialog' }, { gone: true });
    await driver.waitFor({ key: `sidebar.desktop.${desktop.id}` }, { gone: true });
  });

  const screenshot = path.join(runner.runDir, 'native-close-desktop.png');
  await runner.step('capture_window', () => driver.screenshot(screenshot));

  await runner.finishSuccess({ keeper: keeper.id, empty: empty.id, screenshot });
} catch (error) {
  await runner.finishFailure(error, { arrangement: observer.describeArrangement() });
  process.exitCode = 1;
}
