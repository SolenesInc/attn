#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { waitForFirstDesktopPane, waitForPaneInputFocus } from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);
const args = process.argv.slice(2).filter((arg) => arg !== '--');
const options = parseCommonArgs(args);
if (args.includes('--help')) {
  printCommonHelp('scenario-callouts');
  process.exit(0);
}
const runner = createScenarioRunner(options, { scenarioId: 'CALLOUTS', tier: 'local', prefix: 'callouts', metadata: { nativeFocus: 'Activate the window so recordings observe presented frames' } });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const instance = currentHarnessInstance();
const agents = [];
const waitDom = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const cli = async (...args) => {
  const { stdout } = await execFileAsync(appDaemonInTree(options.appPath), args, {
    env: instanceCliEnv(instance, { ATTN_TERMINAL_ID: observer.terminalOf(agents[0].sessionId) }),
  });
  return stdout;
};
const show = async (...steps) => {
  const started = performance.now();
  const output = await cli('callout', 'show', ...steps);
  const replyMs = performance.now() - started;
  runner.assert(output.includes('showing a callout'), 'the real CLI receives app acceptance', { output, replyMs });
  await waitDom('.callout-card');
  return replyMs;
};
const shot = async (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const end = async () => {
  await driver.pressKey('Escape');
  await waitDom('.callout-card', { absent: true });
};
const select = async (agent) => {
  await client.request('select_session', { sessionId: agent.sessionId });
  await waitForPaneInputFocus(client, agent.sessionId, agent.paneId);
};
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_sessions', async () => {
  for (const agent of [...agents].reverse()) {
    if (observer.getSession(agent.sessionId)) await client.request('close_session', { sessionId: agent.sessionId });
  }
});

try {
  await runner.step('prepare_real_shell_sessions', async () => {
    await launchFreshAppAndConnect(client, observer);
    await driver.activateApp();
    const saved = ['callouts_enabled', 'queue_mode_enabled', 'auto_settle_enabled'].map((key) => [key, observer.getSetting(key)]);
    runner.registerCleanup('restore_settings', async () => {
      for (const [key, value] of saved) await client.request('set_setting', { key, value });
    });
    await client.request('set_setting', { key: 'callouts_enabled', value: 'true' });
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' });
    for (const label of ['pointing', 'target']) {
      const cwd = path.join(runner.sessionDir, label);
      fs.mkdirSync(cwd, { recursive: true });
      const sessionId = await createSessionAndWaitForInitialPane({ client, observer, cwd, label, agent: 'shell' });
      const pane = await waitForFirstDesktopPane(client, sessionId, `${label} shell`);
      agents.push({ sessionId, paneId: pane.paneId });
    }
    await select(agents[0]);
    await waitDom('[data-testid="queue-sidebar"]');
  });

  await runner.step('queue_sidebar_steps_and_terminal_focus', async () => {
    const anchors = await cli('callout', 'anchors', '--json');
    runner.writeJson('queue-anchors.json', JSON.parse(anchors));
    await show('sidebar', 'Your agents live here', 'queue', 'Waiting turns come first', 'desktops', 'Each desktop holds your tiles');
    await shot('queue-sidebar');
    await driver.pressEnter();
    await waitDom('.callout-card', { textIncludes: 'Waiting turns come first' });
    await driver.pressKey('ArrowRight');
    await waitDom('.callout-card', { textIncludes: 'Each desktop holds your tiles' });
    await end();
    await waitForPaneInputFocus(client, agents[0].sessionId, agents[0].paneId);
  });

  await runner.step('reveal_agent_and_go_there', async () => {
    await waitDom('[data-testid="queue-agent-list"]', { absent: true });
    const started = performance.now();
    const replyMs = await show(`agent:${agents[1].sessionId}`, 'This agent needs your attention');
    await waitDom('[data-testid="queue-agent-list"]');
    await waitDom('.callout-ring');
    const revealedMs = performance.now() - started;
    runner.writeJson('reveal-receipt.json', { cliAcceptanceMs: replyMs, revealedMs });
    await shot('agent-revealed');
    await driver.pressEnter();
    await waitDom('.callout-card', { absent: true });
    await waitForPaneInputFocus(client, agents[1].sessionId, agents[1].paneId);
    await select(agents[0]);
  });

  await runner.step('queue_bar_card_and_dynamic_fallback', async () => {
    await pressShortcutKeys(client, driver, 'session.toggleSidebar');
    await waitDom('[data-testid="queue-bar"]');
    runner.writeJson('queue-bar-anchors.json', JSON.parse(await cli('callout', 'anchors', '--json')));
    await show('sidebar', 'The queue can stay compact');
    await shot('queue-bar');
    await end();
    await show(`agent:${agents[1].sessionId}`, 'Enter still goes to this agent');
    await waitDom('.callout-unanchored', { textIncludes: "isn't on screen" });
    await driver.pressEnter();
    await waitForPaneInputFocus(client, agents[1].sessionId, agents[1].paneId);
    await select(agents[0]);
    await pressShortcutKeys(client, driver, 'session.toggleSidebar');
  });

  await runner.step('desktop_sidebar_and_rail', async () => {
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    await waitDom('.sidebar:not(.queue-sidebar):not(.collapsed)');
    runner.writeJson('desktop-anchors.json', JSON.parse(await cli('callout', 'anchors', '--json')));
    await show('sidebar', 'Arrange agents and tiles on desktops', 'garden', 'Keep durable work in the garden');
    await shot('desktop-sidebar');
    await driver.pressEnter();
    await waitDom('.callout-card', { textIncludes: 'Keep durable work' });
    await end();
    await pressShortcutKeys(client, driver, 'session.toggleSidebar');
    await waitDom('.sidebar.collapsed');
    runner.writeJson('rail-anchors.json', JSON.parse(await cli('callout', 'anchors', '--json')));
    await show('desktops', 'Your desktops stay one key away');
    await shot('desktop-rail');
    await end();
    await pressShortcutKeys(client, driver, 'session.toggleSidebar');
  });

  await runner.step('shortcuts_dismiss_the_callout', async () => {
    await show('sidebar', 'Commands are still available');
    await pressShortcutKeys(client, driver, 'ui.commandPalette');
    await waitDom('[role="combobox"][aria-label="Commands"]', { focused: true });
    await waitDom('.callout-card', { absent: true });
    await driver.pressKey('Escape');
    await waitForPaneInputFocus(client, agents[0].sessionId, agents[0].paneId);
  });

  await runner.step('browser_tile_gives_way_and_returns', async () => {
    await cli('browser', 'open', 'https://example.com/');
    await waitDom('[data-tile-kind="browser"]');
    await shot('browser-before');
    await show('sidebar', 'Browser tiles give way so this step remains readable');
    await shot('browser-hidden');
    const bounds = await client.request('dom_bounds', { selector: '.callout-overlay' });
    runner.assert(bounds.animations.every((animation) => animation.state !== 'running'), 'the open Card has no running animations', bounds);
    runner.writeJson('callout-idle.json', { bounds, snapshot: await client.request('capture_perf_snapshot', { includeMemory: false }) });
    await end();
    await shot('browser-restored');
    const desktop = observer.desktopOf(agents[0].sessionId);
    const browserTile = await client.request('dom_bounds', { selector: '[data-tile-kind="browser"]' });
    runner.assert(browserTile.bounds.width > 0, 'browser tile remains arranged after dismissal', { desktopId: desktop?.id });
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
