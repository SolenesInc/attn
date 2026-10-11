#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, queueDaemonSettingRestore, relaunchAppAndConnect } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { MOCK_AGENT_MODEL, writeMockAgentFixture } from './mockAgent.mjs';
import { captureWebKitPids, readLiveDaemonPid, readProcessTable, snapshot } from './perfMeasure.mjs';
import { createWindowDriver, delay } from './platform.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Chief slot verification requires a named instance');
const resources = resolveHarnessResources(instance);
const env = { ...instanceCliEnv(instance), ATTN_WRAPPER_PATH: resources.appDaemon };
const cli = args => execFileSync(resources.appDaemon, args, { env, encoding: 'utf8', timeout: 30_000 });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const runner = createScenarioRunner(options, { scenarioId: 'CHIEF-SLOT', tier: 'local', prefix: 'chief-slot' });
let profile;
let previousProfile;
let chief;
let firstDay;
let liveAgent;
let webkitBaseline;
const state = () => client.request('chief_slot_get_state');
const waitDom = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const click = async selector => {
  const [{ bounds }, { logicalBounds }, { innerWidth, innerHeight }] = await Promise.all([
    client.request('dom_bounds', { selector }), client.request('get_window_bounds'), client.request('get_terminal_context_menu_state'),
  ]);
  const x = (Math.max(0, logicalBounds.width - innerWidth) / 2 + bounds.x + bounds.width / 2) / logicalBounds.width;
  const y = (Math.max(0, logicalBounds.height - innerHeight) + bounds.y + bounds.height / 2) / logicalBounds.height;
  runner.writeJson(`pointer-${selector.replace(/[^a-zA-Z0-9]/g, '')}.json`, { selector, bounds, logicalBounds, innerWidth, innerHeight, x, y });
  await driver.movePointerInWindow(x, y);
  runner.writeJson(`focus-${selector.replace(/[^a-zA-Z0-9]/g, '')}.json`, await client.request('dom_active_element'));
  await driver.clickWindow(x, y);
};
const screenshot = async name => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const openChiefRoute = async () => {
  await driver.pressKey('Tab');
  await driver.pressKey('Tab');
  await driver.pressEnter();
  await waitDom('[role="menu"] button:first-of-type', { textIncludes: 'Harness and model' });
  const phase = (await state()).phase;
  await screenshot(`03-chief-actions-${phase}`);
  await click('[role="menu"] button:first-of-type');
  try {
    await waitDom('[data-testid="chief-setup"] [aria-label="Chief model"] [aria-checked="true"]', { focused: true });
  } catch (cause) {
    runner.writeJson(`route-focus-failure-${phase}.json`, {
      chief: await state(),
      focus: await client.request('dom_active_element', { selector: '[data-testid="chief-setup"] [aria-label="Chief model"] [aria-checked="true"]' }),
      setup: await client.request('dom_text', { selector: '[data-testid="chief-setup"]' }).catch(error => ({ error: String(error) })),
      menu: await client.request('dom_text', { selector: '[role="menu"]' }).catch(error => ({ error: String(error) })),
    });
    await screenshot(`route-focus-failure-${phase}`);
    throw cause;
  }
};
const sampleIdle = async name => {
  const before = await snapshot(client.readManifest().pid, readLiveDaemonPid(instance), webkitBaseline);
  const pids = new Set(Object.values(before.byClass).flatMap(group => group.pids.map(entry => entry.pid)));
  const samples = [];
  for (let index = 0; index < 10; index += 1) {
    await delay(1000);
    samples.push((await readProcessTable()).filter(process => pids.has(process.pid)).map(({ pid, comm, cpuPct }) => ({ pid, comm, cpuPct })));
  }
  runner.writeJson(`idle-${name}.json`, { before, samples });
};
const roster = () => JSON.parse(cli(['crew', 'list', '--profile', profile.id, '--json']));
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_profile', async () => {
  const binding = chief && roster().find(member => member.key === chief.key)?.binding_session;
  if (binding) {
    await client.request('select_session', { sessionId: binding });
    await waitDom('[data-session-visible="1"] [aria-label="Terminal input"]', { focused: true });
    await driver.pressKey('d', { control: true });
    await waitDom('[data-testid="chief-slot"][data-chief-phase="asleep"]');
  }
  if (liveAgent) await observer.unregisterMatchingSessions(session => session.id === liveAgent);
  if (previousProfile) await observer.profileCommand('profile_select', { profile_id: previousProfile });
  if (profile) {
    const current = (await client.request('get_state')).arrangement.profiles.find(candidate => candidate.id === profile.id);
    if (current) await observer.profileCommand('profile_delete', { profile_id: current.id, expected_revision: current.revision });
  }
});

try {
  await runner.step('fresh_profile', async () => {
    webkitBaseline = await captureWebKitPids();
    await launchFreshAppAndConnect(client, observer);
    const { logicalBounds } = await client.request('get_window_bounds');
    await client.request('set_window_bounds', { logicalBounds: { ...logicalBounds, x: 0, y: 0 } });
    await driver.activateApp();
    queueDaemonSettingRestore(observer, 'queue_mode_enabled');
    queueDaemonSettingRestore(observer, 'model_tier_overrides');
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    previousProfile = (await client.request('get_state')).arrangement.selectedProfileId;
    profile = (await observer.profileCommand('profile_create', { name: `Chief slot ${runner.runId}` })).profile;
    await observer.profileCommand('profile_select', { profile_id: profile.id });
    await pressShortcutKeys(client, driver, 'ui.commandPalette');
    await waitDom('[role="combobox"][aria-label="Commands"]', { focused: true });
    await driver.typeText(`Switch to ${profile.name}`);
    await waitDom('.unified-palette-option[aria-selected="true"]', { textIncludes: `Switch to ${profile.name}` });
    await driver.pressEnter();
    await client.request('set_setting', { key: 'model_tier_overrides', value: JSON.stringify([{ harness: 'claude', provider: '', model: MOCK_AGENT_MODEL, tier: 'deep' }]) });
    await waitDom('[data-testid="chief-slot"][data-chief-phase="unconfigured"]');
    chief = roster().find(member => member.chief);
    runner.assert(chief && !chief.agent, 'a new profile has an unconfigured chief', { chief });
    const cwd = path.join(runner.sessionDir, 'chief-home');
    fs.mkdirSync(cwd, { recursive: true });
    writeMockAgentFixture(cwd, { version: 1, agent: 'claude', turns: [
      { includes: 'You have been woken', actions: [{ type: 'reply', text: 'CHIEF_SLOT_READY', state: 'idle' }] },
      { includes: '📬 You have unread items', actions: [
        { type: 'attn', args: ['agent', 'inbox'] },
        { type: 'attn', args: ['handoff', '--sleep', '-m', 'The chief slot scenario left this letter.'] },
      ] },
    ] });
    cli(['crew', 'set', 'chief', '--profile', profile.id, '--cwd', cwd, '--json']);
    await screenshot('01-chief-setup');
    await sampleIdle('unconfigured');
    await pressShortcutKeys(client, driver, 'session.toggleSidebar');
    await sampleIdle('collapsed-baseline');
    await pressShortcutKeys(client, driver, 'session.toggleSidebar');
  });
  await runner.step('restored_live_terminal_keeps_focus_with_automatic_setup', async () => {
    const cwd = path.join(runner.sessionDir, 'live-agent');
    fs.mkdirSync(cwd, { recursive: true });
    writeMockAgentFixture(cwd, { version: 1, agent: 'claude', turns: [] });
    liveAgent = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: 'Chief focus witness', agent: 'claude', ownDesktop: false });
    await client.request('select_session', { sessionId: liveAgent });
    await waitDom('[data-session-visible="1"] [aria-label="Terminal input"]', { focused: true });
    await relaunchAppAndConnect(client, observer);
    const { logicalBounds } = await client.request('get_window_bounds');
    await client.request('set_window_bounds', { logicalBounds: { ...logicalBounds, x: 0, y: 0 } });
    await driver.activateApp();
    await client.request('select_session', { sessionId: liveAgent });
    runner.writeJson('restored-view.json', { app: await client.request('get_state'), focus: await client.request('dom_active_element'), chief: await state() });
    await waitDom('[data-session-visible="1"] [aria-label="Terminal input"]', { focused: true });
    await waitDom('[data-testid="chief-slot"][data-chief-phase="unconfigured"]');
    runner.writeJson('restored-terminal-focus.json', { chief: await state(), focus: await client.request('dom_active_element') });
  });
  await runner.step('two_enters_wake_and_focus', async () => {
    await click('[data-testid="chief-setup"] [role="radiogroup"][aria-label="Chief harness"] [role="radio"]:first-child');
    await driver.pressEnter();
    await waitDom('[data-testid="chief-setup"] [aria-label="Chief model"] [aria-checked="true"]', { focused: true });
    await screenshot('02-chief-model');
    await driver.pressEnter();
    await waitDom('[data-testid="chief-slot"][data-chief-phase="awake"]');
    const awake = await state();
    firstDay = awake.sessionId;
    runner.assert(awake.shownAgentId === firstDay, 'Enter on the model wakes and shows the chief', { awake });
    runner.assert(roster().find(member => member.chief)?.binding_session === firstDay, 'the roster binds the chief to the shown session');
    await screenshot('03-chief-awake');
    runner.writeJson('first-wake.json', { state: awake, crew: roster() });
  });
  await runner.step('configured_escape_and_save_return_row_focus', async () => {
    await pressShortcutKeys(client, driver, 'sidebar.agentList');
    await waitDom('[data-testid="queue-agent-filter"]', { focused: true });
    await driver.pressKey('ArrowDown');
    await waitDom(`[data-testid="queue-crew-select-${chief.key}"]`, { focused: true });
    await openChiefRoute();
    await driver.pressKey('Escape');
    await waitDom(`[data-testid="queue-crew-select-${chief.key}"]`, { focused: true });
    await openChiefRoute();
    await screenshot('03-chief-route');
    await driver.pressEnter();
    await waitDom('[data-testid="chief-setup"]', { absent: true });
    await waitDom(`[data-testid="queue-crew-select-${chief.key}"]`, { focused: true });
    runner.writeJson('configured-save-focus.json', { state: await state(), focus: await client.request('dom_active_element') });
  });
  await runner.step('sleep_and_one_click_wake', async () => {
    await waitDom(`[data-testid="queue-crew-select-${chief.key}"]`, { focused: true });
    await driver.pressKey('Tab');
    await waitDom(`[data-testid="queue-crew-sleep-${chief.key}"]`, { focused: true });
    await driver.pressEnter();
    runner.writeJson('sleep-after-activation.json', await state());
    await waitDom('[data-testid="chief-slot"][data-chief-phase="asleep"]');
    await screenshot('04-chief-asleep');
    await sampleIdle('asleep');
    runner.assert(!roster().find(member => member.chief)?.binding_session, 'sleep releases the chief binding');
    await click('[data-testid="queue-agent-filter"]');
    await driver.pressKey('ArrowDown');
    await waitDom(`[data-testid="queue-crew-select-${chief.key}"]`, { focused: true });
    await openChiefRoute();
    await driver.pressEnter();
    await waitDom('[data-testid="chief-setup"]', { absent: true });
    await waitDom(`[data-testid="queue-crew-select-${chief.key}"]`, { focused: true });
    runner.writeJson('asleep-save-focus.json', { state: await state(), focus: await client.request('dom_active_element') });
    const wakeSelector = `[data-testid="queue-crew-select-${chief.key}"]`;
    await client.request('arm_native_pointer_witness', { selector: wakeSelector });
    await click(wakeSelector);
    const receipt = await client.request('wait_native_pointer_witness');
    runner.writeJson('wake-click-receipt.json', receipt);
    await waitDom('[data-testid="chief-slot"][data-chief-phase="awake"]');
    const awake = await state();
    runner.assert(awake.sessionId !== firstDay && awake.shownAgentId === awake.sessionId, 'one click wakes and shows a new day', { awake, firstDay });
    runner.writeJson('second-wake.json', { state: awake, crew: roster() });
    await screenshot('05-chief-awake-again');
  });
  console.log(JSON.stringify(await runner.finishSuccess({ profile: profile.id, chief: chief.key, firstDay }), null, 2));
} catch (error) {
  console.error(error);
  console.error(JSON.stringify(await runner.finishFailure(error, { profile: profile?.id, chief: chief?.key, firstDay }), null, 2));
  process.exitCode = 1;
}
