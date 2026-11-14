#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-CLEAR-OPENS-SESSION', tier: 'tier2-local-mock-agent', prefix: 'native-clear-opens-session',
  metadata: { agent: 'claude', focus: 'In the native app /clear opens a new session in the same tile and sidebar row, typing goes on into it, and the cleared session is closed into the ledger, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const cli = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(currentHarnessInstance()) });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const label = `checkout-${runner.runId}`.slice(0, 40);
const run = { first: crypto.randomUUID(), next: null, runtimeId: null, paneId: null };
runner.registerCleanup('unregister_sessions', async () => {
  for (const id of [run.next, run.first]) if (id && observer.getSession(id)) await observer.unregisterSession(id);
});

const screen = (includes) => driver.state(`terminal:${run.runtimeId}`, { includes, timeoutMs: 15_000 });
const shown = (id, description) => driver.state('window', { includes: id, timeoutMs: 15_000 }).then((window) => {
  runner.assert(window.on_screen === id && window.keyboard?.pane === run.paneId, description, window);
  return window;
});
const offsetInDesktop = async (sessionId, desktopId) => {
  const row = await driver.waitFor({ key: `sidebar.session.${sessionId}` });
  const group = await driver.find({ key: `sidebar.desktop.${desktopId}` });
  return { row, offset: row.rect[1] - group.rect[1] };
};

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  await runner.step('take_a_turn', async () => {
    const cwd = path.join(runner.sessionDir, 'shop');
    writeMockAgentFixture(cwd, {
      name: 'clear mock',
      turns: [
        { includes: 'PRICE_THE_CART', actions: [{ type: 'reply', text: 'Priced the cart', state: 'waiting_input' }] },
        { includes: 'ADD_SHIPPING', actions: [{ type: 'reply', text: 'Shipping added', state: 'waiting_input' }] },
      ],
    });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === run.first && message, 'agent spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: run.first, agent: 'claude', cwd, profile_id: observer.profileId, placement: {}, label, cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, 'spawn_session starts the agent', result);
    run.paneId = result.pane_id;
    run.runtimeId = observer.terminalOf(run.first);
    await driver.click({ key: `sidebar.session.${run.first}` });
    await driver.waitFor({ key: `terminal.${run.runtimeId}` });
    await screen('❯');
    await shown(run.first, 'the agent is on screen with the keyboard in its tile');
    await driver.typeInto(null, 'PRICE_THE_CART');
    await driver.pressKey('Enter');
    await screen('Priced the cart');
    await observer.waitFor(() => observer.getSession(run.first)?.state === 'waiting_input', 'the first turn ended');
  });

  await runner.step('clear_replaces_the_row_in_place_and_keeps_focus', async () => {
    const desktopId = observer.desktopOf(run.first).id;
    const before = await offsetInDesktop(run.first, desktopId);
    runner.assert(before.row.name?.includes(label), 'the sidebar lists the session before /clear', before.row);
    await driver.typeInto(null, '/clear');
    await driver.pressKey('Enter');
    const successor = await observer.waitFor(
      () => [...observer.sessionsById.values()].find((session) => session.succeeds === run.first),
      'the session /clear opened',
      15_000,
    );
    run.next = successor.id;
    await observer.waitFor(() => !observer.getSession(run.first), 'the cleared session leaves the live sessions');
    runner.assert(observer.terminalOf(run.next) === run.runtimeId, 'the new session runs in the same terminal', { runtimeId: run.runtimeId, now: observer.terminalOf(run.next) });
    runner.assert(successor.label !== label, 'the new session gets a new name', { label: successor.label });
    await driver.waitFor({ key: `sidebar.session.${run.first}` }, { gone: true });
    const after = await offsetInDesktop(run.next, desktopId);
    runner.assert(!after.row.name?.includes(label) && Math.abs(after.offset - before.offset) <= 1, 'the new session takes the cleared one\'s sidebar row', { before, after });
    await shown(run.next, 'the new session stays on screen with the keyboard in the same tile');
  });

  await runner.step('typing_continues_into_the_new_session', async () => {
    await driver.typeInto(null, 'ADD_SHIPPING');
    await driver.pressKey('Enter');
    await screen('Shipping added');
    await observer.waitFor(() => observer.getSession(run.next)?.state === 'waiting_input', 'the new session\'s turn ended');
  });

  await runner.step('ledger_lists_the_cleared_session', async () => {
    const closed = cli('session', 'list', '--closed');
    runner.assert(closed.includes(run.first), 'session list --closed lists the cleared session', { closed });
    const shownByCli = cli('session', 'show', run.first);
    runner.assert(/^state\s+closed$/m.test(shownByCli), 'session show reports the cleared session closed', { shown: shownByCli });
    runner.writeText('session-show.txt', shownByCli);
  });

  const screenshot = path.join(runner.runDir, 'native-clear-opens-session.png');
  await runner.step('capture_window', () => driver.screenshot(screenshot));

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ ...run, screenshot, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
