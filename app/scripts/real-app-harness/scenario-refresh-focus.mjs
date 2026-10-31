#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, queueDaemonSettingRestore } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { MOCK_AGENT_EXECUTABLE } from './mockAgent.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { stopDaemon } from './perfMeasure.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Refresh focus verification requires a named instance');
const runner = createScenarioRunner(options, { scenarioId: 'RefreshFocus', tier: 'local', prefix: 'refresh-focus', allowRealAgents: false });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const member = `focus-${runner.runId.replace(/[^a-z0-9]/gi, '').toLowerCase().slice(-6)}`;
const home = path.join(resolveHarnessResources(instance).dataDir, 'crew', member);
const gate = path.join(runner.sessionDir, 'discovery-gate');
const release = path.join(gate, 'release');
const popover = 'dialog[aria-label="Choose a model"]';
const crewRoute = '[data-testid="crew-route"]';
const popoverRefresh = `${popover} .route-kicker button`;
const tierRefresh = '[data-testid="settings-section-models"] section[aria-label="Claude Code models"] .model-tier-header button';
let waitingFor = '';

const attn = args => execFileSync(appDaemonInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance), timeout: 30_000 });
const waitDom = (selector, condition = {}, timeoutMs = 30_000) => {
  waitingFor = selector;
  return client.request('dom_wait', { selector, timeoutMs, ...condition }, { timeoutMs: timeoutMs + 1_000 });
};
const focusedOn = selector => client.request('dom_active_element', { selector });

function gatedDiscoverySource() {
  return `#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
const gate = ${JSON.stringify(gate)};
const args = process.argv.slice(2);
if (args.includes('--print')) {
  fs.writeFileSync(path.join(gate, 'held-' + process.pid), '');
  let answered = false;
  const answer = () => {
    if (answered || !fs.existsSync(path.join(gate, 'release'))) return;
    answered = true;
    watcher.close();
    process.stdin.once('data', () => {
      fs.writeFileSync(path.join(gate, 'answered-' + process.pid), '');
      console.log(JSON.stringify({ type: 'control_response', response: { subtype: 'success', request_id: 'attn-model-discovery', response: { models: [{ value: 'crew-claude', displayName: 'Crew Claude', supportsEffort: true, supportedEffortLevels: ['low', 'high'] }] } } }));
    });
  };
  const watcher = fs.watch(gate, answer);
  answer();
} else {
  const child = spawn(process.execPath, [${JSON.stringify(MOCK_AGENT_EXECUTABLE)}, ...args], { stdio: 'inherit', env: process.env });
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill(signal));
  child.on('exit', (code, signal) => signal ? process.kill(process.pid, signal) : process.exit(code ?? 1));
}
`;
}

function nextGateFile(prefix) {
  return new Promise((resolve, reject) => {
    const seen = new Set(fs.readdirSync(gate));
    const found = () => fs.readdirSync(gate).find(name => name.startsWith(prefix) && !seen.has(name));
    const finish = name => { clearTimeout(timer); watcher.close(); resolve(name); };
    const watcher = fs.watch(gate, () => { const name = found(); if (name) finish(name); });
    const timer = setTimeout(() => { watcher.close(); reject(new Error(`no ${prefix} discovery receipt in ${gate}: ${fs.readdirSync(gate).join(',')}`)); }, 30_000);
  });
}

async function holdDiscovery(action) {
  fs.rmSync(release, { force: true });
  const held = nextGateFile('held-');
  await action();
  await held;
  return async () => {
    const answered = nextGateFile('answered-');
    fs.writeFileSync(release, '');
    await answered;
  };
}

async function assertFocusThroughRefresh(selector, label, startRefresh, settled) {
  const releaseDiscovery = await holdDiscovery(startRefresh);
  if (settled.busy) await waitDom(settled.busy);
  const during = await focusedOn(selector);
  runner.writeJson(`${label}-during.json`, during);
  runner.assert(during.matches === true, `${label}: focus stays on the control while the refresh runs`, during);
  await releaseDiscovery();
  await waitDom(settled.selector, { textIncludes: settled.textIncludes });
  const after = await focusedOn(selector);
  runner.writeJson(`${label}-after.json`, after);
  runner.assert(after.matches === true, `${label}: focus stays on the control after the refresh`, after);
}

registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ waitingFor }) });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('release_discovery', () => fs.writeFileSync(release, ''));
runner.registerCleanup('remove_member_home', () => fs.rmSync(home, { recursive: true, force: true }));
runner.registerCleanup('delete_member', () => attn(['doc', 'delete', 'core/crew', 'members', member]));

try {
  fs.mkdirSync(gate, { recursive: true });
  fs.mkdirSync(home, { recursive: true });
  fs.writeFileSync(path.join(home, 'CHARTER.md'), `# ${member}\n\nKeeps keyboard focus steady.\n`);
  fs.writeFileSync(release, '');
  const wrapper = path.join(runner.sessionDir, 'gated-discovery.mjs');
  fs.writeFileSync(wrapper, gatedDiscoverySource(), { mode: 0o755 });
  await client.quitApp();
  attn(['daemon', 'stop']);
  await launchFreshAppAndConnect(client, observer, { agentExecutables: { claude: wrapper } });
  await client.request('dismiss_whats_new');
  queueDaemonSettingRestore(observer, 'crew_v2_enabled');
  attn(['settings', 'set', 'crew_v2_enabled', 'true']);
  attn(['crew', 'set', member, '--cwd', home, '--agent', 'claude', '--model', 'crew-claude']);

  await runner.step('crew_launch_route_keeps_focus_through_a_reconnect', async () => {
    await pressShortcutKeys(client, driver, 'ui.commandPalette');
    await waitDom('[role="combobox"][aria-label="Commands"]', { focused: true });
    await driver.typeText('Manage crew');
    await waitDom('.unified-palette-option[aria-selected="true"]', { textIncludes: 'Manage crew' });
    await driver.pressEnter();
    await waitDom(`[data-testid="crew-roster-${member}"]`);
    await client.request('dom_click', { selector: `[data-testid="crew-roster-${member}"]` });
    await waitDom(crewRoute, { textIncludes: 'Crew Claude' });
    await client.request('dom_focus', { selector: crewRoute });
    await assertFocusThroughRefresh(crewRoute, 'crew-route', async () => {
      const disconnected = observer.waitForDisconnect();
      runner.assert(Number.isInteger(await stopDaemon(instance)), 'the scenario stopped its isolated daemon');
      await disconnected;
      attn(['daemon', 'ensure']);
      await observer.connect();
    }, { selector: crewRoute, textIncludes: 'Crew Claude' });
  });

  await runner.step('route_refresh_keeps_keyboard_focus', async () => {
    await driver.pressEnter();
    await waitDom(`${popover} [data-model="crew-claude"]`);
    await client.request('dom_focus', { selector: popoverRefresh });
    await assertFocusThroughRefresh(popoverRefresh, 'route-refresh', () => driver.pressEnter(), { busy: `${popoverRefresh}[aria-busy="true"]`, selector: `${popoverRefresh}[aria-busy="false"]` });
    await driver.pressKey('Escape');
    await waitDom(crewRoute, { focused: true });
    await driver.pressKey('Escape');
    await waitDom('[data-testid="crew-panel"][open]', { absent: true });
  });

  await runner.step('model_tier_refresh_keeps_keyboard_focus', async () => {
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await client.request('settings_select_section', { sectionId: 'models' });
    await waitDom(tierRefresh);
    await client.request('dom_focus', { selector: tierRefresh });
    await assertFocusThroughRefresh(tierRefresh, 'tier-refresh', () => driver.pressEnter(), { busy: `${tierRefresh}[aria-busy="true"]`, selector: `${tierRefresh}[aria-busy="false"]` });
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
