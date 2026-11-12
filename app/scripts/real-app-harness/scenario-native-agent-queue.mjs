#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { writeQueueAgentFixture } from './scenarioAgents.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-AGENT-QUEUE', tier: 'tier2-local-mock-agent', prefix: 'native-agent-queue',
  metadata: { focus: 'In the native app the queue lists turns oldest first, a row opens its agent, settling or snoozing hands over to the next turn, the last one lands on home, a wake returns the agent, and the collapsed bar carries the queue, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_queue_mode', () => observer.send({ cmd: 'set_setting', key: 'queue_mode_enabled', value: 'false' }));

const QUEUE_ROW = /^queue-(turn|settled|snoozed|crew|chief)\.([^.]+)$/;

function rowsIn(node, found = []) {
  const match = QUEUE_ROW.exec(node?.key ?? '');
  if (match) found.push({ band: match[1], id: match[2] });
  for (const child of node?.children ?? []) rowsIn(child, found);
  return found;
}

async function turns() {
  return rowsIn(await driver.tree()).filter((row) => row.band === 'turn').map((row) => row.id);
}

async function waitForTurns(expected, description, { gone = [] } = {}) {
  for (const id of expected) await driver.waitFor({ key: `queue-turn.${id}` });
  for (const id of gone) await driver.waitFor({ key: `queue-turn.${id}` }, { gone: true });
  const shown = await turns();
  runner.assert(JSON.stringify(shown) === JSON.stringify(expected), `${description}: the queue lists turns ${JSON.stringify(expected)}`, shown);
}

async function waitForShown(expected, description) {
  const window = await driver.state('window', { includes: expected ?? 'Home' });
  runner.assert((window.on_screen ?? null) === expected, `${description}: the window shows ${expected ?? 'home'}`, window);
}

async function pressRowControl(row, control, description) {
  await driver.hover({ key: row });
  const receipt = await driver.click({ key: `${row}.${control}` });
  runner.assert(receipt.matched, description, receipt);
}

async function spawnAgent(label) {
  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, label);
  fs.mkdirSync(cwd, { recursive: true });
  writeQueueAgentFixture(cwd);
  const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `${label} spawned`, 60_000);
  observer.send({ cmd: 'spawn_session', id, agent: 'claude', cwd, profile_id: observer.profileId, placement: {}, label: `${label}-${runner.runId}`, cols: 100, rows: 30 });
  const result = await spawned;
  runner.assert(result.success, `spawn_session starts ${label}`, result);
  runner.registerCleanup(`unregister_${label}`, () => observer.unregisterSession(id));
  return id;
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;

  await runner.step('launch_app_with_queue_mode', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    await observer.closeLeftoverNumberedDesktops();
    observer.send({ cmd: 'set_setting', key: 'queue_mode_enabled', value: 'true' });
    await driver.waitFor({ key: 'sidebar.queue' });
  });

  let alpha;
  let beta;
  await runner.step('turns_list_oldest_first', async () => {
    alpha = await spawnAgent('alpha');
    await waitForTurns([alpha], 'alpha owes a turn from the moment it boots');
    beta = await spawnAgent('beta');
    await waitForTurns([alpha, beta], 'both turns, oldest first');
  });

  await runner.step('a_row_opens_its_agent', async () => {
    const receipt = await driver.click({ key: `queue-turn.${beta}` });
    runner.assert(receipt.matched, 'the press lands on beta\'s row', receipt);
    await waitForShown(beta, 'the row opens beta');
  });

  await runner.step('settling_hands_over_the_next_turn', async () => {
    await pressRowControl(`queue-turn.${beta}`, 'settle', 'the press lands on beta\'s settle control');
    await waitForTurns([alpha], 'beta settled', { gone: [beta] });
    await waitForShown(alpha, 'the queue moves on to alpha');
  });

  await runner.step('the_bar_carries_the_queue_when_collapsed', async () => {
    await driver.run('session.toggleSidebar');
    const pill = await driver.waitFor({ key: 'queue-bar.pill' });
    runner.assert(pill.name?.startsWith('1 waiting · alpha'), 'the collapsed bar counts the one turn left and names it', pill);
    await driver.run('session.toggleSidebar');
    await driver.waitFor({ key: 'sidebar.queue' });
  });

  await runner.step('snoozing_the_last_turn_lands_on_home', async () => {
    await pressRowControl(`queue-turn.${alpha}`, 'snooze', 'the press lands on alpha\'s snooze control');
    await driver.click({ key: 'snooze.menu.snooze.30m' });
    await waitForTurns([], 'alpha snoozed', { gone: [alpha] });
    await waitForShown(null, 'no turn is left, so the window goes home');
    await driver.waitFor({ key: 'home.all-settled' });
  });

  await runner.step('waking_returns_the_agent_to_the_queue', async () => {
    await driver.click({ key: 'home.group.snoozed.toggle' });
    await pressRowControl(`home.session.${alpha}`, 'unsnooze', 'the press lands on alpha\'s wake control');
    await waitForTurns([alpha], 'alpha woke');
  });

  const screenshot = path.join(runner.runDir, 'native-agent-queue.png');
  await runner.step('capture_window', () => driver.screenshot(screenshot));

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ alpha, beta, screenshot, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
