#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
import { createWindowDriver, delay } from './platform.mjs';
import { captureWebKitPids, snapshot, readProcessTable, readLiveDaemonPid, readAppFootprint } from './perfMeasure.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';

process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';
const options = parseCommonArgs(process.argv.slice(2));
if (!currentHarnessProfile()) throw new Error('Agent settings verification requires a named profile');
const runner = createScenarioRunner(options, { scenarioId: 'AgentSettings', tier: 'local', prefix: 'agent-settings', allowRealAgents: false });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath });
const root = '[data-testid="settings-section-agents"]';
const click = selector => client.request('dom_click', { selector });
const type = (selector, text) => client.request('dom_type', { selector, text });
const select = (selector, value) => client.request('dom_select', { selector, value });
const section = sectionId => client.request('settings_select_section', { sectionId });
const text = async selector => (await client.request('dom_text', { selector })).text;
const baseline = new Map();

function receive(predicate) {
  return new Promise((resolve, reject) => {
    const signal = AbortSignal.timeout(observer.connectTimeoutMs);
    const cleanup = () => { observer.ws?.off('message', handler); signal.removeEventListener('abort', abort); };
    const abort = () => { cleanup(); reject(new Error('Settings receipt did not arrive within the observer connection budget')); };
    const handler = raw => {
      const event = JSON.parse(String(raw));
      if (predicate(event)) { cleanup(); resolve(event); }
    };
    observer.ws.on('message', handler);
    signal.addEventListener('abort', abort, { once: true });
  });
}
async function set(key, value) {
  if (!baseline.has(key)) baseline.set(key, observer.getSetting(key));
  const request_id = crypto.randomUUID();
  const response = receive(event => event.request_id === request_id);
  observer.send({ cmd: 'set_setting', key, value, request_id });
  const result = await response;
  if (!result.success) throw new Error(result.error);
}
async function saved(key, value, action) {
  if (!baseline.has(key)) baseline.set(key, observer.getSetting(key));
  const receipt = receive(event => event.event === 'settings_updated' && event.settings?.[key] === value);
  await action();
  await receipt;
  runner.assert(observer.getSetting(key) === value, `${key} persisted`);
}
let captureWindowId;
async function screenshot(name) {
  const outputPath = path.join(runner.runDir, name);
  if (process.platform === 'darwin') {
    captureWindowId ??= await driver.mainWindowId();
    if (!captureWindowId) throw new Error('No verification window to capture');
    execFileSync('/usr/sbin/screencapture', ['-x', '-l', String(captureWindowId), outputPath]);
  } else await captureFrontWindowScreenshot(outputPath, { client, appPath: options.appPath });
}
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
try {
  const webkitBaseline = await captureWebKitPids();
  await launchFreshAppAndConnect(client, observer);
  // Copied from an existing installation; a caller can supply its own read-only export.
  const fixture = process.env.ATTN_SETTINGS_FIXTURE
    ? JSON.parse(fs.readFileSync(process.env.ATTN_SETTINGS_FIXTURE, 'utf8'))
    : { 'activity.config': '{"agent":"codex","model":"gpt-5.6-luna","effort":"low"}', 'activity.intervals': '{"watching":120,"present":300}' };
  for (const [key, value] of Object.entries(fixture)) await set(key, value);
  for (const [key, value] of Object.entries({ default_model_codex: '', default_effort_codex: '', default_context_window_cap_codex: '', 'session_cost.price.settings-check-model': '', chief_model_claude: '', 'garden.advisor': '{"agent":"codex","model":"gpt-5.6-luna","effort":"xhigh"}', new_session_agent: 'codex' })) await set(key, value);
  await client.request('dismiss_whats_new');
  await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
  await runner.step('agent_sections_and_keyboard_save', async () => {
    await section('agents');
    runner.assert((await text('.settings-content-head h1')) === 'Agents and models', 'navigation and page agree');
    const content = await text(root);
    runner.assert(!content.includes('Review Models') && !content.includes('Chief of staff') && !content.includes('PTY Backend'), 'unrelated controls have moved');
    runner.assert(!/^Save$/m.test(content), 'no manual Save button');
    await screenshot('01-agents.png');
    const model = '#settings-default-model-codex';
    await type(model, 'gpt-5.6-luna');
    await client.request('dom_focus', { selector: model });
    await saved('default_model_codex', 'gpt-5.6-luna', () => driver.pressEnter());
    await saved('default_effort_codex', 'high', () => select('#settings-default-effort-codex', 'high'));
  });
  await runner.step('close_flushes_and_validation_retains_draft', async () => {
    const model = '#settings-default-model-codex';
    await type(model, 'gpt-5.4-mini');
    await client.request('dom_focus', { selector: model });
    await saved('default_model_codex', 'gpt-5.4-mini', () => client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' }));
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await section('agents');
    await click('.settings-agent[open] .settings-agent-advanced > summary');
    await type('#settings-default-context-cap-codex', '1');
    await client.request('dom_key', { selector: '#settings-default-context-cap-codex', key: 'Enter' });
    await section('general');
    runner.assert((await client.request('settings_get_state')).activeSection === 'agents', 'invalid draft prevents silent loss on navigation');
    runner.assert((await text('.settings-autosave-status')).includes('context window cap must be between'), 'validation error remains visible');
    await screenshot('02-validation.png');
    await type('#settings-default-context-cap-codex', '192000');
    await saved('default_context_window_cap_codex', '192000', () => client.request('dom_key', { selector: '#settings-default-context-cap-codex', key: 'Enter' }));
  });
  await runner.step('background_agents_autosave_valid_recipes', async () => {
    await section('backgroundAgents');
    runner.assert(!/^Save$/m.test(await text('[data-testid="settings-section-backgroundAgents"]')), 'background agents have no Save button');
    await saved('garden.advisor', '{"agent":"claude","model":"sonnet","effort":"medium"}', () => select('#settings-garden-advisor-agent', 'claude'));
    await saved('activity.config', '{"agent":"claude"}', () => select('#settings-activity-agent', 'claude'));
    await type('#settings-chief-model-claude', 'sonnet');
    await saved('chief_model_claude', 'sonnet', () => section('agents'));
    await section('backgroundAgents');
    await screenshot('03-background-agents.png');
  });
  await runner.step('pricing_autosaves_and_removes_overrides', async () => {
    await section('agents');
    await click('.settings-pricing > summary');
    await client.request('dom_scroll_into_view', { selector: '.settings-price-card--new' });
    await type('#settings-price-new-model', 'settings-check-model');
    const rates = { input_usd_per_mtok: 2, output_usd_per_mtok: 10, cache_read_usd_per_mtok: 0, cache_write_5m_usd_per_mtok: 0, cache_write_1h_usd_per_mtok: 0 };
    for (const [key, value] of Object.entries(rates)) await type(`#settings-price-new-${key}`, String(value));
    const setting = 'session_cost.price.settings-check-model';
    await saved(setting, JSON.stringify(rates), () => client.request('dom_key', { selector: '#settings-price-new-cache_write_1h_usd_per_mtok', key: 'Enter' }));
    await type('#settings-price-settings-check-model-output_usd_per_mtok', '12');
    await saved(setting, JSON.stringify({ ...rates, output_usd_per_mtok: 12 }), () => client.request('dom_key', { selector: '#settings-price-settings-check-model-output_usd_per_mtok', key: 'Enter' }));
    await screenshot('04-pricing.png');
    await saved(setting, '', () => click('[data-testid="settings-price-settings-check-model-remove"]'));
  });
  await runner.step('relocated_controls', async () => {
    await section('workspace');
    runner.assert((await text('[data-testid="settings-section-workspace"]')).includes('Editor'), 'editor belongs with file locations');
    await section('terminal');
    runner.assert((await text('[data-testid="settings-section-terminal"]')).includes('PTY Backend'), 'terminal hosting is under System');
    await section('delegation');
    runner.assert((await text('[data-testid="settings-section-delegation"]')).includes('Enable workflows'), 'workflows live beside delegation');
    await section('agents');
    await screenshot('04-finished.png');
  });
  await runner.step('idle_settings_measurement', async () => {
    const appPid = client.readManifest().pid;
    const daemonPid = readLiveDaemonPid(currentHarnessProfile());
    const before = await snapshot(appPid, daemonPid, webkitBaseline);
    const pids = new Set(Object.values(before.byClass).flatMap(value => value.pids.map(value => value.pid)));
    const samples = [];
    // Use the same observation window as the delegation settings scenario; no assertion waits on this delay.
    for (let i = 0; i < 10; i++) {
      await delay(1000);
      samples.push((await readProcessTable()).filter(process => pids.has(process.pid)));
    }
    const after = await snapshot(appPid, daemonPid, webkitBaseline);
    runner.writeJson('idle.json', { before, after, samples, footprint: await readAppFootprint(after) });
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  await screenshot('failure.png').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
} finally {
  if (observer.connected) {
    for (const [key, value] of baseline) {
      try { await set(key, value); } catch (error) { console.error(`Restore ${key}: ${error.message}`); process.exitCode = 1; }
    }
  }
  await observer.close().catch(() => {});
  await client.quitApp().catch(() => {});
}
