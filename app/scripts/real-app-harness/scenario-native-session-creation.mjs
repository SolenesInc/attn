#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SESSION-CREATION', tier: 'tier1-local-shell', prefix: 'native-session-creation',
  metadata: { focus: 'In the native app an empty desktop shows the launcher and the new-session key focuses it, the launcher starts a terminal in a typed folder, and on a desktop with a tile both new-session keys open the picker, the second splitting below, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

function nodeIn(node, key) {
  if (node?.key === key) return node;
  for (const child of node?.children ?? []) {
    const found = nodeIn(child, key);
    if (found) return found;
  }
  return null;
}

async function states(key) {
  return nodeIn(await driver.tree(), key)?.states ?? [];
}

function folder(name) {
  const dir = path.join(runner.sessionDir, name);
  fs.mkdirSync(dir, { recursive: true });
  return fs.realpathSync(dir);
}

async function launchTerminal(which, dir) {
  await driver.pressKey('alt+t');
  await driver.waitFor({ key: `${which}.agent.shell` });
  const terminal = await states(`${which}.agent.shell`);
  runner.assert(terminal.includes('checked'), 'alt+t chooses a terminal', terminal);
  await driver.typeInto({ key: `${which}.path.field` }, `${dir}/`);
  await driver.pressKey('Enter');
  const session = await observer.waitForSession({ directory: dir, timeoutMs: 30_000 });
  runner.registerCleanup(`unregister_${path.basename(dir)}`, () => observer.unregisterSession(session.id));
  runner.assert(session.agent === 'shell', 'the picker started a terminal', session);
  return session;
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    const savedAgent = observer.getSetting('new_session_agent') ?? '';
    runner.registerCleanup('restore_saved_agent', () => observer.send({ cmd: 'set_setting', key: 'new_session_agent', value: savedAgent }));
  });

  let desktop;
  await runner.step('an_empty_desktop_shows_the_launcher', async () => {
    const before = observer.currentDesktopId();
    desktop = await observer.createDesktop();
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    const receipt = await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    runner.assert(receipt.matched, 'the press lands on the new desktop\'s row', receipt);
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  let first;
  await runner.step('the_new_session_key_focuses_the_launcher_and_it_starts_a_terminal', async () => {
    await driver.run('session.new');
    await driver.waitFor({ key: 'launcher.path.field' });
    const field = await states('launcher.path.field');
    runner.assert(field.includes('focused'), 'the new-session key focuses the launcher on the desktop', field);
    runner.assert((await driver.find({ key: 'new-session' })) == null, 'no picker dialog opens over an empty desktop');
    first = await launchTerminal('launcher', folder('first'));
    await driver.waitFor({ key: 'launcher' }, { gone: true });
    await observer.waitForDesktopOf(first.id, (entry) => entry.id === desktop.id, 'the terminal lands on the empty desktop');
  });

  await runner.step('the_new_session_key_opens_the_picker_beside_a_tile', async () => {
    await driver.run('session.new');
    const dialog = await driver.waitFor({ key: 'new-session' });
    runner.assert(dialog.name === 'New Session Location', 'the picker is titled for a new session', dialog);
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'new-session' }, { gone: true });
  });

  let second;
  await runner.step('the_split_key_starts_a_terminal_below', async () => {
    await driver.run('session.newHorizontal');
    await driver.waitFor({ key: 'new-session' });
    await driver.screenshot(path.join(runner.runDir, 'native-new-session-picker.png'));
    second = await launchTerminal('new-session', folder('second'));
    await driver.waitFor({ key: 'new-session' }, { gone: true });
    const split = await observer.waitForDesktopOf(second.id, (entry) => entry.id === desktop.id && entry.tree_json.includes('horizontal'), 'the second terminal splits the desktop below');
    runner.assert(split.panes.length === 2, 'both terminals share the desktop', split);
  });

  const screenshot = path.join(runner.runDir, 'native-window.png');
  await runner.step('capture_window', () => driver.screenshot(screenshot));

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, first: first.id, second: second.id, screenshot, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
