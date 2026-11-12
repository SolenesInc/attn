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
  scenarioId: 'NATIVE-PALETTE-ORDER', tier: 'tier1-local-shell', prefix: 'native-palette-order',
  metadata: { focus: 'In the native app the agent palette finds an agent and shows it, a picked command rises above unused ones under the pinned commands, > switches to commands, and priority and rename run from the palette, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const ROW = /^palette\.row\.([^.]+)$/;

function rowsIn(node, found = []) {
  const match = ROW.exec(node?.key ?? '');
  if (match) found.push({ id: match[1], selected: JSON.stringify(node.states ?? '').includes('selected') });
  for (const child of node?.children ?? []) rowsIn(child, found);
  return found;
}

function nodeIn(node, key) {
  if (node?.key === key) return node;
  for (const child of node?.children ?? []) {
    const found = nodeIn(child, key);
    if (found) return found;
  }
  return null;
}

async function rows() {
  return rowsIn(await driver.tree());
}

async function waitForHighlight(id, description) {
  await driver.waitFor({ key: `palette.row.${id}` });
  const shown = await rows();
  runner.assert(shown.find((row) => row.selected)?.id === id, `${description}: ${id} is highlighted`, shown);
}

async function openPalette(face) {
  await driver.run(face === 'commands' ? 'ui.commandPalette' : 'ui.actionMenu');
  const dialog = await driver.waitFor({ key: 'palette.dialog' });
  runner.assert(dialog.name === (face === 'commands' ? 'Commands' : 'Agents'), `the palette opens on ${face}`, dialog);
}

async function closePalette() {
  await driver.pressKey('Escape');
  await driver.waitFor({ key: 'palette' }, { gone: true });
}

async function runCommand(query, id) {
  await openPalette('agents');
  await driver.typeInto(null, `>${query}`);
  await waitForHighlight(`command:${id}`, `>${query} switches to commands`);
  const dialog = await driver.find({ key: 'palette.dialog' });
  runner.assert(dialog.name === 'Commands', '> switches the palette to commands', dialog);
  await driver.pressKey('Enter');
  await driver.waitFor({ key: 'palette' }, { gone: true });
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  const id = crypto.randomUUID();
  const label = `palette-${runner.runId}`.slice(0, 40);
  await runner.step('spawn_shell', async () => {
    const cwd = path.join(runner.sessionDir, 'agent');
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: {}, label, cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
  });

  await runner.step('the_agent_palette_finds_and_shows_the_agent', async () => {
    await openPalette('agents');
    await driver.typeInto(null, label);
    await waitForHighlight(`agent:${id}`, 'the query finds the shell');
    const count = await driver.find({ key: 'palette.count' });
    runner.assert(/^1 of \d+$/.test(count.name ?? ''), 'the count reads one shown', count);
    await driver.screenshot(path.join(runner.runDir, 'native-agent-palette.png'));
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'palette' }, { gone: true });
    const window = await driver.state('window', { includes: id, timeoutMs: 15_000 });
    runner.assert(window.on_screen === id, 'Enter shows the agent', window);
  });

  await runner.step('a_picked_command_rises_above_unused_ones', async () => {
    await openPalette('commands');
    await driver.typeInto(null, 'keyboard shortcuts');
    await waitForHighlight('command:keyboard-shortcuts', 'the search highlights Keyboard shortcuts');
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'shortcuts' });
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'shortcuts' }, { gone: true });

    await openPalette('commands');
    await driver.waitFor({ key: 'palette.row.command:new-agent' });
    const order = (await rows()).map((row) => row.id);
    const at = (command) => order.indexOf(`command:${command}`);
    runner.assert(at('reload-active-session') >= 0 && at('reload-active-session') < at('keyboard-shortcuts'), 'the agent on screen keeps its pinned commands first', order);
    runner.assert(at('keyboard-shortcuts') < at('new-agent'), 'the picked command rises above an unused one', order);
    await driver.screenshot(path.join(runner.runDir, 'native-learned-order.png'));
    await closePalette();
  });

  await runner.step('the_agent_palette_switches_to_commands_on_a_greater_than_sign', async () => {
    await runCommand('keyboard shortcuts', 'keyboard-shortcuts');
    await driver.waitFor({ key: 'shortcuts' });
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'shortcuts' }, { gone: true });
  });

  await runner.step('priority_marks_and_clears_from_the_palette', async () => {
    for (const priority of [true, false]) {
      await runCommand('priority', 'toggle-session-priority');
      await observer.waitFor(() => Boolean(observer.getSession(id)?.priority) === priority, `the shell's priority is ${priority}`, 15_000);
    }
  });

  await runner.step('rename_from_the_palette_saves_cancels_and_refuses', async () => {
    const openRename = async (current) => {
      await runCommand('rename', 'rename-active-session');
      await driver.waitFor({ key: 'session.rename.field' });
      const field = nodeIn(await driver.tree(), 'session.rename.field');
      runner.assert(field?.value === current, 'the rename field holds the current name', field);
    };
    await openRename(label);
    await driver.typeInto(null, 'Renamed palette agent');
    await driver.pressKey('Enter');
    await observer.waitForSession({ id, label: 'Renamed palette agent' });
    await driver.waitFor({ key: 'session.rename' }, { gone: true });

    await openRename('Renamed palette agent');
    await driver.typeInto(null, 'Cancelled palette name');
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'session.rename' }, { gone: true });
    runner.assert(observer.getSession(id)?.label === 'Renamed palette agent', 'Escape keeps the name', observer.getSession(id));

    await openRename('Renamed palette agent');
    await driver.typeInto(null, 'x'.repeat(49));
    await driver.pressKey('Enter');
    const refused = await driver.waitFor({ key: 'session.rename.refused' });
    runner.assert(/48/.test(refused.name ?? ''), 'the daemon\'s refusal names the limit', refused);
    await driver.screenshot(path.join(runner.runDir, 'native-rename-refused.png'));
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'session.rename' }, { gone: true });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ sessionId: id, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
