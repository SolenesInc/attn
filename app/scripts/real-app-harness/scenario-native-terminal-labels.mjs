#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-TERMINAL-LABELS', tier: 'tier2-local-mock-agent', prefix: 'native-terminal-labels',
  metadata: { focus: 'Holding Cmd+L (Ctrl+Shift+L on Linux) labels a live agent\'s id and a seed\'s in a terminal, letting go hides the labels, a label opens its target\'s card with the keyboard in it, no key reaches the shell, and on macOS no menu item takes a label\'s chord' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('let_go', () => driver.holdKeys(''));
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));
runner.registerFailureEvidence('native_tree', async () => fs.writeFileSync(path.join(runner.runDir, 'failure-tree.json'), JSON.stringify(await driver.tree(), null, 2)));

const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const onMac = process.platform === 'darwin';
const MODS = onMac ? 'mod' : 'mod+shift';
const LABEL_LETTERS = 'sdfgjkeruiopnbty';
const AGENT = `Retry uploader ${runner.runId}`;
const SEED = `Map the uploader ${runner.runId}`;

function nodesUnder(node, key, inside = false) {
  const within = inside || node.key === key;
  const own = within ? [node] : [];
  return own.concat((node.children ?? []).flatMap((child) => nodesUnder(child, key, within)));
}

try {
  const frontmostBefore = onMac ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshAppAndConnect(client, observer));
  await runner.step('widen_the_window', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));
  const launcher = observer.currentDesktopId();

  let desktop;
  await runner.step('make_a_desktop', async () => {
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => observer.closeDesktop(desktop.id));
    if (launcher) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(launcher));
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const spawn = async (id, agent, label) => {
    const cwd = path.join(runner.sessionDir, agent);
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `${agent} spawned`, 60_000);
    observer.send({ cmd: 'spawn_session', id, agent, cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label, cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success, `spawn_session starts the ${agent}`, result);
    runner.registerCleanup(`unregister_${agent}`, () => observer.unregisterSession(id));
    return (await observer.waitForTerminalPane(id)).runtime_id;
  };

  const named = crypto.randomUUID();
  const shell = crypto.randomUUID();
  let seedId;
  let runtimeId;
  await runner.step('start_an_agent_and_plant_a_seed_for_a_shell_to_name', async () => {
    await spawn(named, 'claude', AGENT);
    seedId = JSON.parse(attn('seed', 'plant', SEED, '-m', 'Read how uploads retry.', '--profile', observer.profileId, '--json')).id;
    runtimeId = await spawn(shell, 'shell', 'reader');
    await driver.waitFor({ key: `terminal.${runtimeId}` });
  });

  const terminal = { key: `terminal.${runtimeId}` };
  const hints = { key: `terminal.${runtimeId}.hints` };
  const list = { key: `terminal.${runtimeId}.labels` };
  const card = (kind) => ({ key: `terminal.${runtimeId}.${kind}-card` });
  const screen = (includes) => driver.state(`terminal:${runtimeId}`, includes ? { includes, timeoutMs: 15_000 } : {});
  const noted = async (key) => nodesUnder(await driver.tree(), key).filter((node) => node.role === 'note').map((node) => node.name);
  const press = (letter) => driver.pressKey(`${MODS}+${letter}`);
  const hold = async () => {
    await driver.holdKeys(MODS);
    await press('l');
    await driver.waitFor(hints);
  };
  const letGo = async () => {
    await driver.holdKeys('');
    await driver.waitFor(hints, { gone: true });
  };
  const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));

  let before;
  await runner.step('name_the_agent_and_the_seed_in_the_shell', async () => {
    await driver.click(terminal);
    await driver.typeInto(null, `clear; echo 'ask '${named} 'about '${seedId}; echo LABELS''_READY`);
    await driver.pressKey('Enter');
    await screen('LABELS_READY');
    before = await screen();
  });

  const agentLabel = (label) => label.includes(AGENT);
  const seedLabel = (label) => label.includes(SEED);
  await runner.step('holding_labels_both_and_letting_go_hides_them', async () => {
    await hold();
    const onIds = await noted(hints.key);
    runner.assert(onIds.some((label) => agentLabel(label) && label.startsWith('S ')) && onIds.some((label) => seedLabel(label) && label.startsWith('D ')), 'S labels the agent and D the seed', onIds);
    const rows = await noted(list.key);
    runner.assert(rows.length === 2 && rows.some(agentLabel) && rows.some(seedLabel), 'the list names both', rows);
    await shot('lantern');
    await press('l');
    const onIdsAgain = await noted(hints.key);
    runner.assert(JSON.stringify(onIdsAgain) === JSON.stringify(onIds), 'the chord again changes nothing', { onIds, onIdsAgain });
    await letGo();
  });

  await runner.step('a_label_opens_the_agents_card_that_stays_until_escape', async () => {
    await hold();
    await press('s');
    await driver.waitFor(card('agent'));
    await letGo();
    await driver.waitFor(card('agent'));
    await shot('agent-card');
    await driver.pressKey('Escape');
    await driver.waitFor(card('agent'), { gone: true });
  });

  await runner.step('names_stand_in_place_of_the_ids_without_a_list', async () => {
    queueDaemonSettingRestore(observer, 'id_labels');
    await driver.run('ui.openSettings');
    await driver.click({ key: 'settings.nav.general' });
    await driver.click({ role: 'radio-button', name: 'Name in place' });
    await observer.waitFor(() => observer.getSetting('id_labels') === 'name', 'Name in place is saved', 15_000);
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'settings.nav.general' }, { gone: true });
    await hold();
    await driver.waitFor(list, { gone: true });
    const onIds = await noted(hints.key);
    runner.assert(onIds.some(agentLabel) && onIds.some(seedLabel), 'both are named', onIds);
    await shot('name-in-place');
    await letGo();
  });

  await runner.step('a_label_opens_the_seeds_card_and_return_opens_the_seed', async () => {
    await hold();
    await press('d');
    await driver.waitFor(card('seed'));
    await letGo();
    await shot('seed-card');
    await driver.pressKey('Enter');
    const opened = await observer.waitForDesktopOf(shell, (entry) => entry.tree_json.includes(seedId) && entry, 'the seed opens in a tile');
    const tiles = [];
    const walk = (node) => {
      if (node?.tile_id) tiles.push(node.tile_id);
      (node?.children ?? []).forEach(walk);
    };
    walk(JSON.parse(opened.tree_json));
    for (const tile of tiles) await observer.profileCommand('desktop_remove_leaf', { desktop_id: desktop.id, leaf_id: tile });
    for (const tile of tiles) await driver.waitFor({ key: `tile.${tile}` }, { gone: true });
  });

  await runner.step('no_key_reached_the_shell', async () => {
    const said = (rows) => rows.map((row) => row.trim()).filter(Boolean).join('');
    const after = await screen('LABELS_READY');
    runner.assert(said(after) === said(before), 'the shell shows what it showed before the labels', { before, after });
  });

  if (onMac) {
    await runner.step('no_menu_item_takes_a_labels_chord', async () => {
      const keys = await driver.menuKeys();
      runner.assert(keys.some((item) => item.key.toLowerCase() === 'q' && item.modifiers === 0), 'the menu bar reads, with Quit on Command-Q', keys);
      const taken = keys.filter((item) => item.modifiers === 0 && LABEL_LETTERS.includes(item.key.toLowerCase()));
      runner.assert(taken.length === 0, 'no menu item has a label\'s Command chord', taken);
    });
  }

  const frontmostAfter = onMac ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ desktopId: desktop.id, sessionId: shell, seedId });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
