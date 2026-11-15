#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-CALLOUTS', tier: 'tier1-local-shell', prefix: 'native-callouts',
  metadata: { focus: 'An agent\'s callout in the native app steps through the queue sidebar, reveals and goes to an agent, falls back when its anchor is off screen, points in desktop flow, comes back after the palette, and leaves typing with the terminal when it ends, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const agents = [];
const cli = (...args) => execFileSync(appCliInTree(options.appPath), args, {
  encoding: 'utf8',
  env: instanceCliEnv(instance, { ATTN_TERMINAL_ID: observer.terminalOf(agents[0].id) }),
});
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('unregister_shells', async () => {
  for (const agent of [...agents].reverse()) if (observer.getSession(agent.id)) await observer.unregisterSession(agent.id);
});

const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));
async function show(...steps) {
  const output = cli('callout', 'show', ...steps);
  runner.assert(output.includes('showing a callout'), 'the CLI hears that the app shows the callout', { output });
  await driver.waitFor({ key: 'callout' });
}
const reads = (words) => driver.waitFor({ key: 'callout.text', name: words });
async function end() {
  await driver.pressKey('Escape');
  await driver.waitFor({ key: 'callout' }, { gone: true });
}
async function shown(agent, description) {
  const window = await driver.state('window', { includes: agent.id, timeoutMs: 15_000 });
  runner.assert(window.on_screen === agent.id && window.keyboard?.pane === agent.pane, description, window);
}
async function select(agent) {
  await driver.run('ui.actionMenu');
  await driver.waitFor({ key: 'palette.dialog' });
  await driver.typeInto(null, agent.label);
  await driver.waitFor({ key: `palette.row.agent:${agent.id}` });
  await driver.pressKey('Enter');
  await driver.waitFor({ key: 'palette' }, { gone: true });
  await shown(agent, `${agent.label} is on screen with the keyboard`);
}
const queueRowOf = async (agent) => {
  const found = [];
  const walk = (node) => { if (/^queue-[a-z]+\./.test(node.key ?? '') && node.key.endsWith(`.${agent.id}`)) found.push(node.key); (node.children ?? []).forEach(walk); };
  walk(await driver.tree());
  return found[0] ?? null;
};
const anchors = (name) => runner.writeJson(`${name}-anchors.json`, JSON.parse(cli('callout', 'anchors', '--json')));
function setSetting(key, value) {
  observer.send({ cmd: 'set_setting', key, value });
  return observer.waitFor(() => observer.getSetting(key) === value, `${key} set to ${value}`);
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;

  await runner.step('prepare_two_shells_in_queue_flow', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    const saved = ['queue_mode_enabled', 'auto_settle_enabled'].map((key) => [key, observer.getSetting(key) ?? '']);
    runner.registerCleanup('restore_settings', async () => { for (const [key, value] of saved) observer.send({ cmd: 'set_setting', key, value }); });
    await setSetting('queue_mode_enabled', 'true');
    await setSetting('auto_settle_enabled', 'false');
    for (const label of [`pointing-${runner.runId}`.slice(0, 40), `target-${runner.runId}`.slice(0, 40)]) {
      const id = crypto.randomUUID();
      const cwd = path.join(runner.sessionDir, label);
      fs.mkdirSync(cwd, { recursive: true });
      const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `${label} spawned`);
      observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: {}, label, cols: 100, rows: 30 });
      const result = await spawned;
      runner.assert(result.success && result.pane_id, `spawn_session starts ${label}`, result);
      agents.push({ id, pane: result.pane_id, label });
    }
    await driver.waitFor({ key: 'sidebar.queue' });
    await select(agents[0]);
  });

  await runner.step('queue_sidebar_steps_and_terminal_focus', async () => {
    anchors('queue');
    await show('sidebar', 'Your agents live here', 'queue', 'Waiting turns come first', 'desktops', 'Each desktop holds your tiles');
    await driver.waitFor({ key: 'callout.count', name: '1 of 3' });
    await shot('queue-sidebar');
    await driver.pressKey('Enter');
    await reads('Waiting turns come first');
    await driver.pressKey('Right');
    await reads('Each desktop holds your tiles');
    await end();
    await shown(agents[0], 'the pointing shell keeps the keyboard after the callout ends');
  });

  await runner.step('reveal_an_agent_and_go_there', async () => {
    runner.assert(await queueRowOf(agents[1]) === null, 'the queue lists no row for the target before the callout');
    await show(`agent:${agents[1].id}`, 'This agent needs your attention');
    await driver.waitFor({ key: 'callout.ring' });
    const revealed = await queueRowOf(agents[1]);
    runner.assert(revealed !== null, 'the callout opens the agent list on the target\'s row');
    await driver.waitFor({ key: revealed });
    await driver.waitFor({ key: 'callout.next', name: '↵ Go there' });
    await shot('agent-revealed');
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'callout' }, { gone: true });
    await shown(agents[1], 'Enter goes to the agent the callout pointed at');
    await select(agents[0]);
  });

  await runner.step('queue_bar_card_and_a_missing_anchor', async () => {
    await driver.run('session.toggleSidebar');
    await driver.waitFor({ key: 'queue-bar.pill' });
    anchors('queue-bar');
    await show('sidebar', 'The queue can stay compact');
    await shot('queue-bar');
    await end();
    await show(`agent:${agents[1].id}`, 'Enter still goes to this agent');
    await driver.waitFor({ key: 'callout.unanchored' });
    await driver.pressKey('Enter');
    await shown(agents[1], 'Enter goes to an agent whose anchor is off screen');
    await driver.run('session.toggleSidebar');
    await driver.waitFor({ key: 'sidebar.queue' });
    await select(agents[0]);
  });

  await runner.step('desktop_flow_sidebar', async () => {
    await setSetting('queue_mode_enabled', 'false');
    await driver.waitFor({ key: 'sidebar.queue' }, { gone: true });
    await driver.waitFor({ key: `sidebar.session.${agents[0].id}` });
    anchors('desktop');
    await show('sidebar', 'Arrange agents and tiles on desktops', 'garden', 'Keep durable work in the garden');
    await shot('desktop-sidebar');
    await driver.pressKey('Enter');
    await reads('Keep durable work in the garden');
    await end();
    await driver.run('session.toggleSidebar');
    await driver.waitFor({ key: 'rail' });
    anchors('rail');
    await show('desktops', 'Your desktops stay one key away');
    await shot('desktop-rail');
    await end();
    await driver.run('session.toggleSidebar');
    await driver.waitFor({ key: 'sidebar' });
  });

  await runner.step('the_callout_comes_back_after_the_palette', async () => {
    await show('sidebar', 'Commands are still available', 'garden', 'And the garden');
    await driver.run('ui.commandPalette');
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.waitFor({ key: 'callout' }, { gone: true });
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'palette' }, { gone: true });
    await reads('Commands are still available');
    await driver.pressKey('Enter');
    await reads('And the garden');
    await end();
  });

  await runner.step('typing_goes_to_the_terminal_after_the_callout', async () => {
    await select(agents[0]);
    await show('sidebar', 'The terminal keeps the keyboard');
    await end();
    await shown(agents[0], 'the pointing shell has the keyboard once the callout ends');
    const renamed = observer.waitForMessage((message) => message.event === 'session_state_changed'
      && message.session?.id === agents[0].id && message.session.label === 'Terminal keeps focus' && message,
    'typing reaches the pointing shell');
    await driver.typeInto(null, `attn session rename --session ${agents[0].id} "Terminal keeps focus"`);
    await driver.pressKey('Enter');
    await renamed;
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ agents, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
