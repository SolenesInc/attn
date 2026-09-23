#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver, delay } from './platform.mjs';
import { captureWebKitPids, readLiveDaemonPid, readProcessTable, snapshot, readAppFootprint, readGraphicsRegions, appPids } from './perfMeasure.mjs';
import { MOCK_AGENT_MODEL, writeMockAgentFixture } from './mockAgent.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Delegation chain verification requires a named instance');
process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';
const runner = createScenarioRunner(options, { scenarioId: 'DelegationChain', tier: 'local', prefix: 'delegation-chain', allowRealAgents: false });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const created = [];
const popup = '.delegation-chain-popover';
const runAttn = args => execFileSync(appDaemonInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const text = selector => client.request('dom_text', { selector }).then(result => result.text);
const exists = selector => client.request('dom_text', { selector }).then(() => true, error => {
  if (String(error).includes('selector not found')) return false;
  throw error;
});
const preferencesRequest = (cmd, preferences) => {
  const request_id = crypto.randomUUID();
  return new Promise((resolve, reject) => {
    const receive = raw => {
      const result = JSON.parse(String(raw));
      if (result.request_id !== request_id) return;
      observer.ws.off('message', receive);
      if (result.success) resolve(result); else reject(new Error(result.error));
    };
    observer.ws.on('message', receive);
    observer.ws.send(JSON.stringify({ cmd, request_id, preferences }));
  });
};
const waitForSelector = async (selector, description, expectation = {}) => {
  const result = await client.request('dom_wait', { selector, timeoutMs: 10_000, ...expectation });
  runner.assert(result.matched, description);
};
const hold = () => process.env.ATTN_HARNESS_RECORD === '1' ? delay(1200) : Promise.resolve();
const screenshot = async name => {
  const windowId = await driver.mainWindowId();
  if (!windowId) throw new Error('The verification app has no capturable window');
  await driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId });
};
const waitForChainFocus = (id, description) => waitForSelector(`${popup} [data-chain-session="${id}"]:focus`, description);
const nativeTarget = async selector => {
  const [{ bounds }, { logicalBounds }, { innerWidth, innerHeight }] = await Promise.all([
    client.request('dom_hover', { selector, leave: true }),
    client.request('get_window_bounds'),
    client.request('get_terminal_context_menu_state'),
  ]);
  return {
    x: (Math.max(0, logicalBounds.width - innerWidth) / 2 + bounds.x + bounds.width / 2) / logicalBounds.width,
    y: (Math.max(0, logicalBounds.height - innerHeight) + bounds.y + bounds.height / 2) / logicalBounds.height,
  };
};

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, created.toReversed()));

try {
  const webkitBaseline = await captureWebKitPids();
  await launchFreshAppAndConnect(client, observer);
  await driver.activateApp();
  await client.request('dismiss_whats_new');
  const baseline = (await preferencesRequest('delegation_preferences_get')).preferences;
  runner.registerCleanup('restore_preferences', async () => {
    const current = (await preferencesRequest('delegation_preferences_get')).preferences;
    await preferencesRequest('delegation_preferences_save', { ...baseline, revision: current.revision });
  });
  const role = (id, name, icon) => ({
    id, name, icon, enabled: true, description: '', instructions: 'Wait for direction.', stopping_point: '', default_choice_id: 'default',
    choices: [{ id: 'default', name: 'Default', when: '', selection: { harness: 'codex', provider: '', model: '', effort: '' } }],
  });
  await preferencesRequest('delegation_preferences_save', {
    ...baseline, enabled: true, roles: [role('coordinate', 'Orchestrator', 'spark'), role('build', 'Builder', 'code')],
  });
  fs.mkdirSync(runner.sessionDir, { recursive: true });
  writeMockAgentFixture(runner.sessionDir, {
    name: 'Role identity check',
    turns: [{ includes: 'role identity', actions: [{ type: 'reply', text: 'Ready to inspect the delegation chain.', state: 'idle' }] }],
  });
  let root, builder, child;
  await runner.step('launch_roles_and_a_roleless_delegate', async () => {
    const source = await createSessionAndWaitForInitialPane({ client, observer, cwd: runner.sessionDir, label: 'Delegation setup', agent: 'shell' });
    created.push(source);
    const delegate = async (sourceId, name, roleId) => {
      const output = runAttn(['delegate', '--source-session', sourceId, ...(roleId ? ['--role', roleId] : ['--agent', 'codex', '--model', MOCK_AGENT_MODEL]),
        '--brief', 'Inspect role identity. Wait for direction.', '--cwd', runner.sessionDir, '--name', name]);
      const result = JSON.parse(output.slice(output.indexOf('{')));
      created.push(result.session_id);
      await observer.waitFor(() => observer.sessionsById.has(result.session_id), `${name} visible`);
      return result.session_id;
    };
    root = await delegate(source, 'Coordinate role identity', 'coordinate');
    builder = await delegate(root, 'Build chain navigator', 'build');
    child = await delegate(builder, 'Check keyboard flow');
    await observer.waitFor(() => observer.getSession(root)?.delegation_role?.name === 'Orchestrator'
      && observer.getSession(builder)?.delegation_role?.name === 'Builder', 'saved roles projected onto sessions');
    runner.assert(!observer.getSession(child)?.delegation_role, 'direct delegation remains roleless');
    await client.request('select_session', { sessionId: builder });
  });
  await runner.step('sidebar_hover_and_header_role', async () => {
    await driver.activateApp();
    const header = `.delegation-chain-trigger--header[data-delegation-session="${builder}"]`;
    await waitForSelector(header, 'Builder header');
    runner.assert((await text(header)) === 'Builder', 'header spells out the assigned role');
    const row = `[data-testid="sidebar-session-${builder}"]`;
    const rowTarget = await nativeTarget(row);
    await driver.movePointerInWindow(rowTarget.x, rowTarget.y);
    await waitForSelector(popup, 'hovered chain');
    await waitForChainFocus(builder, 'row hover focuses the current agent');
    runner.assert((await text(`${popup} .delegation-chain-heading`)).includes('Build chain navigator'), 'the shared card contains the complete title');
    const content = await text(popup);
    runner.assert(content.includes('Orchestrator') && content.includes('Builder') && content.includes('Check keyboard flow'), 'the chain shows ancestors, roles and roleless descendants', { content });
    runner.assert(!await exists('.kin-up, .kin-down, .sidebar-delegate-count, .sidebar-dispatcher, .session-label-reveal'), 'one shared card, without subtitles, related-row highlights or old count circles');
    await screenshot('hover-chain');
    await hold();
    await driver.pressKey('ArrowUp');
    await waitForChainFocus(root, 'row hover accepts native arrows');
    await driver.pressKey('Escape');
    await waitForSelector(popup, 'Escape closes the hover card', { absent: true });
    await driver.movePointerInWindow(rowTarget.x, rowTarget.y);
    runner.assert(!await exists(popup), 'dismissal does not reopen under a stationary pointer');
    const headerTarget = await nativeTarget(header);
    await driver.movePointerInWindow(headerTarget.x, headerTarget.y);
    await waitForChainFocus(builder, 'header hover focuses the current agent');
    await driver.pressKey('ArrowDown');
    await waitForChainFocus(child, 'header hover accepts native arrows');
    await driver.pressKey('Escape');
    await driver.clickWindow(headerTarget.x, headerTarget.y);
    await waitForChainFocus(builder, 'click focuses the current agent');
    await hold();
    await driver.pressKey('Escape');
    await waitForSelector(`${header}:focus`, 'Escape restores the header trigger');
  });
  await runner.step('native_action_menu_arrows_enter_and_escape', async () => {
    await driver.activateApp();
    await pressShortcutKeys(client, driver, 'ui.actionMenu');
    await waitForSelector('.action-menu input:focus', 'native action menu shortcut');
    await driver.typeText('delegation chain');
    await waitForSelector('.action-menu-results', 'chain command', { textIncludes: 'Show delegation chain' });
    await driver.pressKey('Enter');
    await waitForChainFocus(builder, 'command transfers focus into the chain');
    await screenshot('keyboard-chain');
    await hold();
    await driver.pressKey('ArrowUp');
    await waitForChainFocus(root, 'Up selects the orchestrator');
    await driver.pressKey('ArrowDown');
    await driver.pressKey('ArrowDown');
    await waitForChainFocus(child, 'Down selects the roleless descendant');
    await driver.pressKey('Enter');
    await waitForSelector(popup, 'selection dismisses the chain', { absent: true });
    runner.assert((await client.request('get_state')).activeSessionId === child, 'Enter opens the selected agent');
    runner.assert(!await exists(popup), 'selection closes the popup');
    await pressShortcutKeys(client, driver, 'ui.actionMenu');
    await waitForSelector('.action-menu input:focus', 'action menu reopens');
    await driver.typeText('delegation chain');
    await driver.pressKey('Enter');
    await waitForChainFocus(child, 'roleless session opens its chain');
    await driver.pressKey('Escape');
    await waitForSelector(popup, 'Escape dismisses the chain', { absent: true });
  });
  await runner.step('settings_changes_do_not_relabel_existing_agents', async () => {
    const current = (await preferencesRequest('delegation_preferences_get')).preferences;
    await preferencesRequest('delegation_preferences_save', { ...current, enabled: false, roles: [] });
    await client.request('select_session', { sessionId: root });
    await waitForSelector(`.delegation-chain-trigger--header[data-delegation-session="${root}"]`, 'orchestrator header after deletion');
    runner.assert(observer.getSession(root)?.delegation_role?.name === 'Orchestrator', 'launch identity survives deleting its role');
  });
  await runner.step('sample_idle_app_with_chain_open', async () => {
    await client.request('dom_click', { selector: `.delegation-chain-trigger--header[data-delegation-session="${root}"]` });
    await waitForChainFocus(root, 'idle chain focus');
    if (process.platform !== 'darwin') {
      runner.writeJson('idle-app.json', { supported: false, reason: 'Physical app footprint collection uses macOS vmmap; no Linux resource measurement is claimed.' });
      await screenshot('final-chain');
      return;
    }
    const appPid = client.readManifest().pid;
    const samples = [];
    for (let index = 0; index < 2; index += 1) {
      await delay(2000);
      const processes = await snapshot(appPid, readLiveDaemonPid(instance), webkitBaseline);
      const pids = new Set(appPids(processes));
      const cpu = (await readProcessTable()).filter(process => pids.has(process.pid));
      const graphics = await Promise.all((processes.byClass.webkit_gpu?.pids ?? []).map(async ({ pid }) => ({ pid, surfaces: await readGraphicsRegions(pid) })));
      samples.push({ processes, cpu, footprint: await readAppFootprint(processes), graphics });
    }
    runner.writeJson('idle-app.json', samples);
    await screenshot('final-chain');
  });
  console.log(JSON.stringify(await runner.finishSuccess({ sessions: created }), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
