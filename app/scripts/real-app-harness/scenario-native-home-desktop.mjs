#!/usr/bin/env node

import path from 'node:path';
import { launchFreshAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-HOME-DESKTOP', tier: 'tier1-local-shell', prefix: 'native-home-desktop',
  metadata: { focus: 'With views on, Home is a desktop at the top of the sidebar: the Home key shows it and a digit leaves it, it moves in the sidebar and the palette puts it back, and a restart opens on it' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));
runner.registerFailureEvidence('native_tree', async () => runner.writeJson('failure-tree.json', await driver.tree()));

const home = () => observer.desktops.find((desktop) => desktop.kind === 'home');
const ordered = () => [...observer.desktops].sort((a, b) => (a.order_key < b.order_key ? -1 : 1)).map((d) => d.id);
const slotOne = () => observer.desktops.find((desktop) => desktop.shortcut_slot === 1)?.id;
const showsSlotOne = async (description) => {
  const id = await observer.waitFor(() => observer.currentDesktopId() === slotOne() && slotOne(), description);
  await driver.state('window', { expect: { view: 'Desktop', current_desktop: id } });
  return id;
};
const shows = async (desktopId, description) => {
  await observer.waitFor(() => observer.currentDesktopId() === desktopId, description);
  await driver.state('window', { expect: { view: 'Desktop', current_desktop: desktopId } });
};

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshAppAndConnect(client, observer));

  let homeId;
  let first;
  await runner.step('views_on_list_home_at_the_top', async () => {
    queueDaemonSettingRestore(observer, 'views_enabled');
    runner.registerCleanup('back_to_desktop_one', () => first && observer.returnToDesktop(first));
    observer.send({ cmd: 'set_setting', key: 'views_enabled', value: 'true' });
    homeId = (await observer.waitFor(home, 'the Home desktop listed')).id;
    runner.assert(ordered()[0] === homeId, 'Home is at the top of the desktops', ordered());
    first = slotOne();
    runner.assert(first, 'the profile still has Desktop 1', observer.desktops);
    const line = await driver.waitFor({ key: `sidebar.desktop.${homeId}` });
    runner.assert(line.name === 'Open Home', 'the sidebar lists Home among the desktops', line);
    await driver.waitFor({ key: 'sidebar.home' }, { gone: true });
  });

  await runner.step('home_key_and_digit', async () => {
    await driver.run('desktop.select1');
    await showsSlotOne('Desktop 1 current');
    await driver.run('session.goToDashboard');
    await shows(homeId, 'Home current after the Home key');
    await driver.waitFor({ key: 'tile.tile-view-attn' });
    await driver.waitFor({ key: 'tile.tile-view-sessions' });
    await driver.atRest();
    await driver.screenshot(path.join(runner.runDir, 'home.png'));
    await driver.run('desktop.select1');
    first = await showsSlotOne('Desktop 1 current after its digit');
  });

  await runner.step('move_home_in_the_sidebar', async () => {
    await driver.drag(
      { key: `sidebar.desktop.${homeId}` },
      { target: { key: `sidebar.desktop.${first}` }, at: [0.5, 0.9] },
    );
    await observer.waitFor(() => ordered()[0] !== homeId, 'Home moved below the top');
    await driver.atRest();
    await driver.screenshot(path.join(runner.runDir, 'home-moved.png'));
  });

  await runner.step('put_home_back_from_the_palette', async () => {
    await driver.run('ui.commandPalette');
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, 'Put Home back');
    await driver.waitFor({ key: 'palette.row.command:put-home-back' });
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'palette' }, { gone: true });
    await observer.waitFor(() => ordered()[0] === homeId, 'Home back at the top');
    const tree = JSON.parse(observer.desktop(homeId).tree_json);
    const leaves = (node) => (node.type === 'split' ? node.children.flatMap(leaves) : [node.tile_id ?? node.pane_id]);
    runner.assert(JSON.stringify(leaves(tree)) === JSON.stringify(['tile-view-attn', 'tile-view-sessions', 'tile-view-pull-requests']),
      'Home holds attn above Sessions, then Pull requests', tree);
  });

  await runner.step('restart_opens_on_home', async () => {
    await driver.run('session.goToDashboard');
    await shows(homeId, 'Home current before the restart');
    await client.quitApp();
    await launchFreshAppAndConnect(client, observer);
    await driver.state('window', { expect: { view: 'Desktop', current_desktop: homeId } });
    await driver.waitFor({ key: 'tile.tile-view-attn' });
    await driver.atRest();
    await driver.screenshot(path.join(runner.runDir, 'home-after-restart.png'));
    await driver.run('desktop.select1');
    first = await showsSlotOne('Desktop 1 current again');
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ homeId, first, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
