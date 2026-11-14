#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
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
if (!instance) throw new Error('The native agent settings scenario changes settings, so it needs a named instance');
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-AGENT-SETTINGS', tier: 'tier1-local-shell', prefix: 'native-agent-settings',
  metadata: { focus: 'Native Agents and models saves its switches, the default agent, a harness executable and a context cap it first refuses beside the field, adds and removes a price override, and Background agents saves the headless cap, each read back from the shell' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const env = instanceCliEnv(instance);
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
const mac = process.platform === 'darwin';
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const MODEL = 'agent-settings-check-model';
const RATES = {
  input_usd_per_mtok: '2', output_usd_per_mtok: '10', cache_read_usd_per_mtok: '0.2', cache_write_5m_usd_per_mtok: '2.5', cache_write_1h_usd_per_mtok: '4',
};
const SETTINGS = ['direct_agent_launch_enabled', 'auto_approve_enabled', 'new_session_agent', 'codex_executable', 'default_context_window_cap_codex', `session_cost.price.${MODEL}`, 'headless_context_window_cap'];

const savedAs = (key, value) => observer.waitForMessage((m) => m.event === 'settings_updated' && m.settings?.[key] === value && m, `${key} saved as ${JSON.stringify(value)}`, 15_000);
const nodes = async () => {
  const all = [];
  const walk = (node) => { all.push(node); (node.children ?? []).forEach(walk); };
  walk(await driver.tree());
  return all;
};
const node = async (key) => (await nodes()).find((n) => n.key === key) ?? null;
const field = (setting) => `settings.input.${setting}.field`;
const replaceText = async (key, text) => {
  await driver.click({ key });
  await driver.pressKey('End');
  const typed = (await node(key))?.value ?? '';
  for (let i = 0; i < [...typed].length; i += 1) await driver.pressKey('Backspace');
  if (text) await driver.typeInto({ key }, text);
};
const section = async (id) => {
  await driver.click({ key: `settings.nav.${id}` });
  await driver.state('settings', { includes: id });
};
const expanded = async (key) => (await node(key))?.states?.includes('expanded');
const openAdvanced = async (agent) => {
  if (!(await expanded(`settings.agent.${agent}.toggle`))) await driver.click({ key: `settings.agent.${agent}.toggle` });
  if (!(await expanded(`settings.agent.${agent}.advanced`))) await driver.click({ key: `settings.agent.${agent}.advanced` });
};
const flip = async (setting) => {
  const key = `settings.toggle.${setting}`;
  const before = attn('settings', 'get', setting);
  const next = before === 'false' ? 'true' : 'false';
  const saved = savedAs(setting, next);
  await driver.click({ key });
  await saved;
  runner.assert(attn('settings', 'get', setting) === next, `the shell reads ${setting} as the switch saved it`, { before, next });
  const back = savedAs(setting, before || 'true');
  await driver.click({ key });
  await back;
};
const screenshot = (name) => driver.screenshot(path.join(runner.runDir, name));

try {
  const frontmostBefore = mac ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    const baseline = new Map(SETTINGS.map((key) => [key, observer.getSetting(key)]));
    runner.registerCleanup('restore_settings', () => Promise.all([...baseline].map(([key, value]) => observer.requestResult({ cmd: 'set_setting', key, value }, 'settings_updated').catch(() => {}))));
    await driver.run('ui.openSettings');
    await driver.waitFor({ key: 'settings-dialog' });
    await section('agents');
  });

  await runner.step('the_switches_save_both_ways', async () => {
    await flip('direct_agent_launch_enabled');
    await flip('auto_approve_enabled');
    await screenshot('native-agents.png');
  });

  await runner.step('the_default_agent_saves', async () => {
    const before = attn('settings', 'get', 'new_session_agent') || 'claude';
    const other = before === 'codex' ? 'claude' : 'codex';
    const saved = savedAs('new_session_agent', other);
    await driver.click({ key: `settings.default-agent.${other}` });
    await saved;
    runner.assert(attn('settings', 'get', 'new_session_agent') === other, 'the shell reads the default agent the dialog saved');
    const back = savedAs('new_session_agent', before);
    await driver.click({ key: `settings.default-agent.${before}` });
    await back;
  });

  await runner.step('a_harness_executable_saves_and_clears', async () => {
    await openAdvanced('codex');
    const executable = path.join(runner.sessionDir, 'bin', 'codex');
    fs.mkdirSync(path.dirname(executable), { recursive: true });
    fs.writeFileSync(executable, '#!/bin/sh\nexit 0\n', { mode: 0o755 });
    await replaceText(field('codex_executable'), executable);
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'settings.saved.codex_executable' });
    runner.assert(attn('settings', 'get', 'codex_executable') === executable, 'the shell reads the executable the dialog saved');
    await driver.waitFor({ key: 'settings.saved.codex_executable' }, { gone: true });
    await replaceText(field('codex_executable'), '');
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'settings.saved.codex_executable' });
    runner.assert(attn('settings', 'get', 'codex_executable') === '', 'clearing the field goes back to the executable on PATH');
  });

  await runner.step('a_refused_context_cap_stays_beside_its_field_then_saves', async () => {
    const cap = field('default_context_window_cap_codex');
    await replaceText(cap, '1');
    await driver.pressKey('Enter');
    const refusal = await driver.waitFor({ key: 'settings.refusal.default_context_window_cap_codex' });
    runner.assert(/context window cap must be between/.test(refusal.name ?? ''), 'the refusal says the allowed range', refusal);
    await driver.click({ key: 'settings.nav.general' });
    const { activeSection } = await driver.state('settings');
    runner.assert(activeSection === 'agents', 'a refused field keeps its page', { activeSection });
    await screenshot('native-agents-refusal.png');
    const good = attn('settings', 'get', 'default_context_window_cap_codex') === '192000' ? '256000' : '192000';
    const saved = savedAs('default_context_window_cap_codex', good);
    await replaceText(cap, good);
    await driver.pressKey('Enter');
    await saved;
    await driver.waitFor({ key: 'settings.refusal.default_context_window_cap_codex' }, { gone: true });
  });

  await runner.step('a_price_override_saves_and_removes', async () => {
    if (!(await expanded('settings.pricing'))) await driver.click({ key: 'settings.pricing' });
    await driver.scroll({ key: 'settings.body' }, 3000);
    const setting = `session_cost.price.${MODEL}`;
    const expected = JSON.stringify(Object.fromEntries(Object.entries(RATES).map(([k, v]) => [k, Number(v)])));
    await replaceText('settings.price-new.model', MODEL);
    for (const [rate, value] of Object.entries(RATES)) await replaceText(`settings.price-new.${rate}`, value);
    await screenshot('native-agents-pricing-form.png');
    const saved = observer.waitForMessage((m) => m.event === 'settings_updated' && m.settings?.[setting] && m, `${setting} saved`, 15_000);
    await driver.pressKey('Enter');
    const stored = (await saved).settings[setting];
    runner.assert(JSON.stringify(JSON.parse(stored)) === expected, 'the override saves its five rates', { stored, expected });
    await driver.waitFor({ key: `settings.price.${MODEL}` });
    await screenshot('native-agents-pricing.png');
    const removed = savedAs(setting, '');
    await driver.click({ key: `settings.price.${MODEL}.remove` });
    await removed;
    await driver.waitFor({ key: `settings.price.${MODEL}` }, { gone: true });
  });

  await runner.step('background_agents_save_the_headless_cap', async () => {
    await section('backgroundAgents');
    const cap = attn('settings', 'get', 'headless_context_window_cap') === '500000' ? '600000' : '500000';
    const saved = savedAs('headless_context_window_cap', cap);
    await replaceText(field('headless_context_window_cap'), cap);
    await driver.pressKey('Enter');
    await saved;
    runner.assert(attn('settings', 'get', 'headless_context_window_cap') === cap, 'the shell reads the headless cap');
  });

  await runner.step('close', async () => {
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'settings-dialog' }, { gone: true });
  });

  const frontmostAfter = mac ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ frontmostBefore, frontmostAfter });
} catch (error) {
  await driver.tree().then((tree) => runner.writeJson('failure-tree.json', tree)).catch(() => {});
  await screenshot('failure.png').catch(() => {});
  await runner.finishFailure(error);
  process.exitCode = 1;
}
