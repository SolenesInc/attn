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
const client = new UiAutomationClient({ ...options, launchEnv: { ATTN_WRAPPER_PATH: resources.appDaemon } });
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const runner = createScenarioRunner(options, { scenarioId: 'CHIEF-SLOT', tier: 'local', prefix: 'chief-slot' });
let profile;
let previousProfile;
let chief;
let firstDay;
let notebookRoot;
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
  await screenshot(`03-chief-actions-${(await state()).phase}`);
  await click('[role="menu"] button:first-of-type');
  await waitDom('[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', { focused: true });
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
    const cwd = path.join(runner.sessionDir, 'chief-notebook');
    notebookRoot = cwd;
    fs.mkdirSync(cwd, { recursive: true });
    if (process.env.ATTN_HARNESS_NOTEBOOK_FIXTURE) fs.cpSync(process.env.ATTN_HARNESS_NOTEBOOK_FIXTURE, cwd, { recursive: true, dereference: true });
    writeMockAgentFixture(cwd, { version: 1, agent: 'claude', turns: [
      { includes: 'You have been woken', actions: [{ type: 'reply', text: 'CHIEF_SLOT_READY', state: 'idle' }] },
      { includes: '📬 You have unread items', actions: [
        { type: 'attn', args: ['agent', 'inbox'] },
        { type: 'attn', args: ['handoff', '--sleep', '-m', 'The chief slot scenario left this letter.'] },
      ] },
    ] });
    if (!(await state()).setupOpen) await click('[data-testid="chief-slot"] .queue-row-select');
    await waitDom('[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', { focused: true });
    const body = await client.request('dom_bounds', { selector: '.chief-pop-body' });
    runner.writeJson('setup-body-bounds.json', body);
    runner.assert(body.scrollWidth === body.clientWidth && body.scrollHeight === body.clientHeight, 'first-run setup fits without clipping or scrollbars', body);
    await screenshot('01-chief-setup');
    await driver.pressKey('Escape');
    await waitDom('[data-testid="chief-slot"] .queue-row-select', { focused: true });
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    await click('[data-testid="chief-slot"] .queue-row-select');
    await waitDom('[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', { focused: true });
    await screenshot('01-chief-setup-desktop-flow');
    await driver.pressKey('Escape');
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await sampleIdle('unconfigured');
    await pressShortcutKeys(client, driver, 'session.toggleSidebar');
    await sampleIdle('collapsed-baseline');
    await pressShortcutKeys(client, driver, 'session.toggleSidebar');
  });
  await runner.step('no_harness_recheck', async () => {
    await click('[data-testid="chief-slot"] .queue-row-select');
    const originals = new Map(['claude', 'codex'].map(agent => [agent, observer.getSetting(`${agent}_executable`)]));
    const missing = [];
    for (const [agent] of originals) {
      queueDaemonSettingRestore(observer, `${agent}_executable`);
      const executable = path.join(runner.sessionDir, `missing-${agent}`);
      fs.writeFileSync(executable, '#!/bin/sh\nexit 1\n', { mode: 0o755 });
      await client.request('set_setting', { key: `${agent}_executable`, value: executable });
      missing.push(executable);
    }
    for (const executable of missing) fs.unlinkSync(executable);
    observer.send({ cmd: 'get_settings' });
    await waitDom('[data-testid="chief-setup"]', { textIncludes: 'No harness here can run the chief' });
    await screenshot('01-chief-no-harness');
    await click('[data-testid="chief-setup"] .chief-link');
    await waitDom('[data-testid="chief-setup"]', { textIncludes: 'No harness here can run the chief' });
    for (const [agent, executable] of originals) await client.request('set_setting', { key: `${agent}_executable`, value: executable });
    await waitDom('[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', { focused: true });
    await driver.pressKey('Escape');
  });
  await runner.step('restored_live_terminal_keeps_setup_closed', async () => {
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
    await waitDom('[data-testid="chief-setup"]', { absent: true });
    await screenshot('01-chief-agents-running');
    runner.writeJson('restored-terminal-focus.json', { chief: await state(), focus: await client.request('dom_active_element') });
  });
  await runner.step('enter_wakes_and_focuses', async () => {
    await click('[data-testid="chief-slot"] .queue-row-select');
    await waitDom('[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', { focused: true });
    await driver.pressKey('Escape');
    await waitDom('[data-testid="chief-slot"] .queue-row-select', { focused: true });
    await driver.pressEnter();
    await waitDom('[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', { focused: true });
    await screenshot('01-chief-setup-with-agent');
    await waitDom('[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', { focused: true });
    const folderSelector = '[data-testid="chief-setup"] input[aria-label="Notebook folder"]';
    const { value: defaultFolder } = await client.request('dom_value', { selector: folderSelector });
    runner.assert(defaultFolder === cli(['settings', 'get', 'notebook.root', '--profile', profile.id]).trim(), 'the folder is prefilled with the profile Notebook', { defaultFolder });
    const invalidRoot = path.join(runner.sessionDir, 'not-a-folder');
    fs.writeFileSync(invalidRoot, 'This file reproduces a Notebook folder save failure.');
    await click(folderSelector);
    await driver.pressKey('a', { command: true });
    await driver.typeText(invalidRoot);
    await driver.pressEnter();
    await waitDom('[data-testid="chief-setup"] [role="alert"]');
    await screenshot('02-chief-save-error');
    await click(folderSelector);
    await driver.pressKey('a', { command: true });
    await driver.typeText(notebookRoot);
    await screenshot('02-chief-model-and-folder');
    await driver.pressKey('Tab');
    await waitDom('[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', { focused: true });
    const waking = waitDom('[data-testid="chief-slot"][data-chief-phase="configuring"]').then(() => screenshot('02-chief-waking'));
    await driver.pressEnter();
    await waking;
    await waitDom('[data-testid="chief-slot"][data-chief-phase="awake"]');
    const awake = await state();
    firstDay = awake.sessionId;
    runner.assert(awake.shownAgentId === firstDay, 'Enter on Wake accepts the selected route and shows the chief', { awake });
    const configured = roster().find(member => member.chief);
    runner.assert(configured?.binding_session === firstDay, 'the roster binds the chief to the shown session');
    runner.assert(configured?.home_dir === path.join(notebookRoot, 'crew', 'chief') && configured?.cwd === notebookRoot, 'the chief has its final Notebook home and working directory', { configured });
    runner.assert(cli(['settings', 'get', 'notebook.root', '--profile', profile.id]).trim() === notebookRoot, 'setup saves the profile Notebook folder');
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
    runner.writeJson('before-configured-save-focus.json', await client.request('dom_active_element'));
    await driver.pressEnter();
    runner.writeJson('after-configured-save-focus.json', { state: await state(), focus: await client.request('dom_active_element') });
    await waitDom('[data-testid="chief-setup"]', { absent: true });
    await waitDom(`[data-testid="queue-crew-select-${chief.key}"]`, { focused: true });
    runner.writeJson('configured-save-focus.json', { state: await state(), focus: await client.request('dom_active_element') });
  });
  await runner.step('configured_command_expands_the_sidebar_and_focuses_save', async () => {
    await pressShortcutKeys(client, driver, 'session.toggleSidebar');
    await waitDom('[data-testid="chief-slot"]', { absent: true });
    await relaunchAppAndConnect(client, observer);
    await driver.activateApp();
    await pressShortcutKeys(client, driver, 'ui.commandPalette');
    await waitDom('[role="combobox"][aria-label="Commands"]', { focused: true });
    await driver.typeText("Change the chief's harness and model");
    await waitDom('.unified-palette-option[aria-selected="true"]', { textIncludes: "Change the chief's harness and model" });
    await driver.pressEnter();
    await waitDom('[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', { focused: true });
    await screenshot('03-chief-route-from-collapsed-sidebar');
    await driver.pressKey('Escape');
    await waitDom(`[data-testid="queue-crew-select-${chief.key}"]`, { focused: true });
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
  await runner.step('charter_opens_and_saves_in_the_notebook_editor', async () => {
    await pressShortcutKeys(client, driver, 'notebook.openFullscreen');
    await waitDom('.notebook-browser[role="dialog"]');
    await click('[role="treeitem"][title="crew"]');
    await waitDom('[role="treeitem"][title="crew/chief"]');
    await click('[role="treeitem"][title="crew/chief"]');
    await waitDom('[role="treeitem"][title="crew/chief/CHARTER.md"]');
    await click('[role="treeitem"][title="crew/chief/CHARTER.md"]');
    await waitDom('.notebook-browser-document', { textIncludes: 'CHARTER.md' });
    const editor = '.notebook-browser[role="dialog"] .cm-content[contenteditable="true"]';
    await click(editor);
    const marker = 'Notebook charter edit verified.';
    await driver.pressKey('End', { command: true });
    await driver.typeText('\n\n' + marker + '\n');
    await waitDom('.notebook-browser-save-status', { textIncludes: 'Saved' });
    const charter = fs.readFileSync(path.join(notebookRoot, 'crew', 'chief', 'CHARTER.md'), 'utf8');
    runner.assert(charter.trimEnd().endsWith(marker), 'the Notebook editor saved the actual crew charter');
    await screenshot('03-chief-charter-notebook');
    runner.writeJson('charter-editor.json', { home: path.join(notebookRoot, 'crew', 'chief'), edited: true });
    await driver.pressKey('Escape');
    await waitDom('.notebook-browser[role="dialog"]', { absent: true });
  });
  console.log(JSON.stringify(await runner.finishSuccess({ profile: profile.id, chief: chief.key, firstDay }), null, 2));
} catch (error) {
  if (chief) {
    try {
      runner.writeJson('chief-slot-failure-state.json', {
        chief: await state(),
        focus: await client.request('dom_active_element', { selector: '[data-testid="chief-setup"] [aria-label="Chief model"] [aria-checked="true"]' }),
        setup: await client.request('dom_text', { selector: '[data-testid="chief-setup"]' }).catch(error => ({ error: String(error) })),
        menu: await client.request('dom_text', { selector: '[role="menu"]' }).catch(error => ({ error: String(error) })),
      });
      await screenshot('chief-slot-failure-state');
    } catch (captureError) {
      runner.writeJson('chief-slot-failure-capture-error.json', { error: String(captureError) });
    }
  }
  console.error(error);
  console.error(JSON.stringify(await runner.finishFailure(error, { profile: profile?.id, chief: chief?.key, firstDay }), null, 2));
  process.exitCode = 1;
}
