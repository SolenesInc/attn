#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, dataDirForInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { workInFolder } from './workedFolder.mjs';
import { registeredAgentPid } from './workerRegistry.mjs';

const PANEL_TIMEOUT_MS = 30_000;
const RUN_DELIVERED_TIMEOUT_MS = 45_000;
const RUN_ERROR_TIMEOUT_MS = 20_000;
const RESTART_READY_TIMEOUT_MS = 60_000;
const API_VERSION = 'attn.dev/automations/v1alpha1';
const QUEUE_MODE = 'queue_mode_enabled';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('the native automation surface scenario needs a named non-production instance');
const binary = appCliInTree(resolveHarnessResources(instance).appPath);
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-AUTOMATION-SURFACE', allowRealAgents: false, tier: 'tier2-local-fake-agent', prefix: 'native-automation-surface',
  metadata: { instance, focus: 'The native automations drawer lists CLI definitions, keeps a stored route across a save, runs one at once and opens its session, shows the run on its desktop, in the palette, the queue sidebar and the queue bar, says why a disabled run is refused, shows a Codex launch that fails before its first turn, and keeps definitions and runs across a restart' },
});
const daemonEnv = instanceCliEnv(instance);
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const cli = (...args) => execFileSync(binary, args.map(String), { encoding: 'utf8', env: daemonEnv, stdio: ['ignore', 'pipe', 'pipe'], timeout: 30_000 });
const cliJSON = (...args) => JSON.parse(cli(...args));
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function poll(fn, description, timeoutMs = PANEL_TIMEOUT_MS) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(250);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

const drawer = () => driver.state('automations');
const definitionIn = (state, id) => (state?.definitions || []).find((row) => row.id === id) || null;
const runsIn = (state) => state?.runs || [];
const has = async (key) => Boolean(await driver.find({ key }));
function nodesWhere(node, test, found = []) {
  if (!node) return found;
  if (test(node)) found.push(node);
  for (const child of node.children ?? []) nodesWhere(child, test, found);
  return found;
}
const valueOf = async (key) => nodesWhere(await driver.tree(), (n) => n.key === key)[0]?.value ?? null;
const onScreen = async () => (await driver.state('window')).on_screen ?? null;
const setQueueMode = async (on) => {
  observer.send({ cmd: 'set_setting', key: QUEUE_MODE, value: on ? 'true' : 'false' });
  await observer.waitFor(() => observer.getSetting(QUEUE_MODE) === (on ? 'true' : 'false'), `queue flow ${on ? 'on' : 'off'}`);
};

async function openDrawer() {
  if (!(await drawer()).present) await driver.click({ key: 'titlebar.automations' });
  await driver.waitFor({ key: 'automations' });
}
async function closeDrawer() {
  await driver.click({ key: 'automations.close' });
  await driver.waitFor({ key: 'automations' }, { gone: true });
}
async function select(id) {
  if ((await drawer()).selected !== id) await driver.click({ key: `automations.select.${id}` });
  await poll(async () => (await drawer()).selected === id, `definition ${id} selected`);
}
const definitionShows = (id, timeoutMs = PANEL_TIMEOUT_MS) => poll(async () => { const s = await drawer(); return definitionIn(s, id) ? s : null; }, `definition ${id} in the drawer`, timeoutMs);
async function toggle(id, enabled) {
  await driver.click({ key: `automations.toggle.${id}` });
  await poll(async () => definitionIn(await drawer(), id)?.enabled === enabled, `definition ${id} ${enabled ? 'on' : 'off'}`);
}

async function showsAgentAfterHome(chord, sessionID, where) {
  await driver.run('session.goToDashboard');
  await poll(async () => (await driver.state('window')).view === 'Home', 'Home');
  runner.assert(await onScreen() === null, `the Home key leaves the run (${where})`);
  await driver.run(chord);
  await poll(async () => (await onScreen()) === sessionID, `next run to show ${sessionID} (${where})`);
}

const manualYAML = (locationPath) => `api_version: ${API_VERSION}
name: Native automations drawer proof (manual)
trigger:
  type: manual
prompt: |
  Native automations drawer proof. Do nothing.
launch:
  driver: codex
  model: native-manual-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
const scheduledYAML = (locationPath) => `api_version: ${API_VERSION}
name: Native automations drawer proof (scheduled)
trigger:
  type: scheduled
  schedule:
    cron: "0 0 1 1 *"
    time_zone: UTC
  continuity: fresh
  catch_up: latest
prompt: |
  Once a year, so it never fires during the scenario.
launch:
  driver: codex
  model: native-scheduled-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;

let manualID = 0;
let scheduledID = 0;
let failingID = 0;
let manualSessionID = '';
let firstRunID = '';
let fixturePath = null;

async function closeSession(sessionID) {
  if (!sessionID || !observer.getSession(sessionID)) return;
  const terminal = observer.terminalOf(sessionID);
  observer.send({ cmd: 'kill_session', id: sessionID });
  await observer.waitFor(() => (observer.getSession(sessionID)?.state !== 'working' ? true : null), `${sessionID} to stop working`, RESTART_READY_TIMEOUT_MS);
  observer.unregisterSession(sessionID);
  await observer.waitFor(() => (!observer.getSession(sessionID) ? true : null), `${sessionID} to close`, RESTART_READY_TIMEOUT_MS);
  if (terminal) await poll(() => (registeredAgentPid(dataDirForInstance(instance), terminal, fixturePath) === null ? true : null), `the agent of ${sessionID} to exit`, RESTART_READY_TIMEOUT_MS);
}

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_queue_mode', () => observer.send({ cmd: 'set_setting', key: QUEUE_MODE, value: 'false' }));
runner.registerCleanup('remove_fixtures', async () => {
  for (const id of [manualID, scheduledID]) if (id) { try { cli('automation', 'disable', id); } catch {} }
  const sessions = new Set(manualSessionID ? [manualSessionID] : []);
  for (const id of [manualID, failingID]) {
    if (!id) continue;
    try { for (const r of cliJSON('automation', 'runs', id) || []) if (r.session_id) sessions.add(r.session_id); } catch {}
  }
  for (const sessionID of sessions) {
    try {
      const garden = cliJSON('seed', 'ls', '--json') || {};
      const seed = (garden.seeds || []).find((s) => s.tender?.ref === `session:${sessionID}` || s.tender?.session_id === sessionID);
      await closeSession(sessionID).catch(() => {});
      if (seed && seed.status !== 'withered') cli('seed', 'wither', seed.id, '-m', 'Native automation surface harness fixture complete');
    } catch {}
  }
  for (const id of [manualID, scheduledID, failingID]) if (id) { try { cli('automation', 'delete', id); } catch {} }
  if (fixturePath) fs.rmSync(fixturePath, { recursive: true, force: true });
});

try {
  await runner.step('restart_daemon_and_launch', async () => {
    fixturePath = fs.realpathSync(fs.mkdtempSync(path.join(runner.sessionDir, 'automations-surface-')));
    writeMockAgentFixture(fixturePath, { name: 'automation desktop proof', turns: [{
      includes: 'Native automations drawer proof',
      actions: [{ type: 'reply', text: 'Which change should I review next?', state: 'waiting_input' }],
    }] });
    await client.quitApp();
    try { cli('daemon', 'stop'); } catch {}
    cli('daemon', 'ensure');
    await launchFreshNativeAppAndConnect(client, observer);
    await setQueueMode(false);
  });

  await runner.step('work_in_the_automation_folder', () => workInFolder({ observer, cwd: fixturePath }));

  await runner.step('leg1_apply_manual_and_the_drawer_shows_it', async () => {
    const file = path.join(runner.sessionDir, 'manual.yml');
    fs.writeFileSync(file, manualYAML(fixturePath));
    manualID = cliJSON('automation', 'apply', '--file', file, '--launch-desktop', 'own', '--desktop-name', 'Testing automations').id;
    await openDrawer();
    const row = definitionIn(await definitionShows(manualID), manualID);
    runner.assert(row.trigger === 'Manual' && row.enabled === true && row.canRunNow === true, 'the manual definition shows Manual, on, with Run now', row);
    runner.assert(await has(`automations.run-now.${manualID}`), 'the row offers Run now');
  });

  await runner.step('a_stored_launch_route_survives_a_save', async () => {
    await driver.click({ key: `automations.edit.${manualID}` });
    const opened = await poll(async () => { const f = (await drawer()).form; return f?.status === 'ready' ? f : null; }, 'the edit form');
    runner.assert(opened.spec.launch.model === 'native-manual-probe' && opened.spec.launch.effort === 'high', 'the form keeps the stored, unreported route', opened.spec.launch);
    await driver.click({ key: 'automations.form.save' });
    await poll(async () => (await drawer()).form === null, 'the save to close the form');
    const launch = JSON.parse((await observer.requestResult({ cmd: 'automation_definition_get', definition_id: manualID }, 'automation_definition_result')).spec_json).launch;
    runner.assert(launch.model === 'native-manual-probe' && launch.effort === 'high', 'saving keeps the model and effort', launch);
  });

  await runner.step('leg2_run_now_and_open_its_session', async () => {
    await select(manualID);
    await driver.click({ key: `automations.run-now.${manualID}` });
    const state = await poll(async () => { const s = await drawer(); return runsIn(s).some((r) => r.state === 'delivered') ? s : null; }, 'the run to be delivered', RUN_DELIVERED_TIMEOUT_MS);
    const runs = runsIn(state);
    runner.assert(runs.length === 1 && runs[0].state === 'delivered', 'one press makes one delivered run', runs);
    firstRunID = runs[0].id;
    manualSessionID = runs[0].session_id;
    runner.assert(Boolean(manualSessionID), 'the delivered run names its session', runs[0]);
    runner.assert(await has(`automations.run-open.${firstRunID}`), 'the run offers to open its session');

    await closeDrawer();
    await openDrawer();
    await select(manualID);
    const again = await poll(async () => { const s = await drawer(); return runsIn(s).length ? s : null; }, 'the runs after reopening');
    runner.assert(runsIn(again).length === 1 && runsIn(again)[0].id === firstRunID, 'reopening the drawer shows the same single run', runsIn(again));
    await driver.click({ key: `automations.run-open.${firstRunID}` });
    await poll(async () => (await onScreen()) === manualSessionID, 'the run to open its session');
  });

  await runner.step('the_run_on_its_desktop_in_the_palette_the_queue_sidebar_and_the_queue_bar', async () => {
    await closeDrawer();
    const desktopId = cliJSON('automation', 'list').find((row) => row.id === manualID).launch_desktop.desktop_id;
    await driver.waitFor({ key: `sidebar.session.${manualSessionID}` });
    const desktop = await driver.waitFor({ key: `sidebar.desktop.${desktopId}` });
    runner.assert((desktop.name ?? '').includes('Testing automations'), 'the run sits on the named launch desktop', desktop);
    runner.assert(!(await has('queue.runs')), 'desktop flow has no automation runs block');
    await driver.screenshot(path.join(runner.runDir, 'run-desktop.png')).catch(() => {});

    await driver.run('ui.actionMenu');
    await driver.waitFor({ key: `palette.row.agent:${manualSessionID}` });
    await driver.screenshot(path.join(runner.runDir, 'run-palette.png')).catch(() => {});
    await driver.pressKey('Escape');

    await poll(() => (observer.getSession(manualSessionID)?.turn_owed ? true : null), 'the run to owe a turn', RUN_DELIVERED_TIMEOUT_MS);
    await showsAgentAfterHome('session.nextRun', manualSessionID, 'desktop flow');

    await setQueueMode(true);
    const group = `queue.runs.group.${manualID}`;
    await driver.waitFor({ key: `queue-run.${manualSessionID}` });
    runner.assert(await valueOf(group) === '1', 'the queue sidebar groups the run under its automation', { value: await valueOf(group) });
    runner.assert(!(await has(`queue-turn.${manualSessionID}`)) && !(await has(`queue-settled.${manualSessionID}`)), 'the run stays out of the queue bands');
    await driver.screenshot(path.join(runner.runDir, 'run-queue-sidebar.png')).catch(() => {});

    await driver.run('session.toggleSidebar');
    await poll(async () => (await valueOf('queue-bar.runs')) === '1 runs, 1 need you', 'the queue bar to count the run');
    await driver.hover({ key: 'queue-bar.runs' });
    await driver.waitFor({ key: `queue-bar.runs-peek.agent:${manualSessionID}` });
    await driver.screenshot(path.join(runner.runDir, 'run-queue-bar.png')).catch(() => {});
    await driver.hover({ key: 'queue-bar.pill' });
    await showsAgentAfterHome('session.nextRun', manualSessionID, 'queue flow');
    await driver.run('session.toggleSidebar');
    await setQueueMode(false);
  });

  await runner.step('leg3_a_refused_run_says_why', async () => {
    const file = path.join(runner.sessionDir, 'scheduled.yml');
    fs.writeFileSync(file, scheduledYAML(fixturePath));
    scheduledID = cliJSON('automation', 'apply', '--file', file).id;
    await openDrawer();
    const scheduled = definitionIn(await definitionShows(scheduledID), scheduledID);
    runner.assert(scheduled.trigger.startsWith('Scheduled') && scheduled.canRunNow === true, 'a scheduled definition shows its schedule and Run now', scheduled);

    await toggle(manualID, false);
    await select(manualID);
    await driver.click({ key: `automations.run-now.${manualID}` });
    const refused = await poll(async () => { const s = await drawer(); return s.runErrors?.[manualID] ? s : null; }, 'the refusal to show on the row', RUN_ERROR_TIMEOUT_MS);
    runner.assert(refused.runErrors[manualID].toLowerCase().includes('disabled'), 'the row says the automation is off', refused.runErrors);
    runner.assert(definitionIn(refused, manualID).canRunNow === true && await has(`automations.run-now.${manualID}`), 'Run now stays offered on the refused row');
    runner.assert(runsIn(refused).length === 1, 'the refused press started no run', runsIn(refused));
    await toggle(manualID, true);
  });

  await runner.step('leg3b_codex_that_exits_before_its_first_turn_stays_pending_then_fails', async () => {
    const release = path.join(runner.sessionDir, 'codex-exits.release');
    const exitingDir = path.join(runner.sessionDir, 'codex-exits');
    fs.mkdirSync(exitingDir, { recursive: true });
    await workInFolder({ observer, cwd: exitingDir });
    writeMockAgentFixture(exitingDir, { name: 'codex exits', turns: [], refuseThreadStart: { text: 'thread/start failed during TUI bootstrap: harness refusal (code -32603)', releasePath: release } });
    const file = path.join(runner.sessionDir, 'codex-exits.yml');
    fs.writeFileSync(file, `api_version: ${API_VERSION}\nname: Codex launch failure proof\ntrigger:\n  type: manual\nprompt: |\n  Codex launch failure proof.\nlaunch:\n  driver: codex\nlocation:\n  type: directory\n  path: ${JSON.stringify(exitingDir)}\n`);
    failingID = cliJSON('automation', 'apply', '--file', file).id;
    await definitionShows(failingID);
    await select(failingID);
    await driver.click({ key: `automations.run-now.${failingID}` });
    const pending = await poll(async () => { const s = await drawer(); return runsIn(s).length === 1 ? s : null; }, 'the failing launch to show a run');
    runner.assert(runsIn(pending)[0].state === 'pending', 'a Codex that has not started its first turn shows its run pending', runsIn(pending));
    await driver.screenshot(path.join(runner.runDir, 'codex-launch-pending.png')).catch(() => {});
    fs.writeFileSync(release, 'exit\n');
    const failed = await poll(async () => { const s = await drawer(); return runsIn(s)[0]?.state === 'failed' ? s : null; }, 'the failing launch to fail', RUN_ERROR_TIMEOUT_MS);
    const error = runsIn(failed)[0].last_error ?? '';
    runner.assert(error.includes('before its first turn') && error.includes('harness refusal'), 'the failed run carries the exit and the screen Codex left', runsIn(failed)[0]);
    await driver.waitFor({ key: `automations.failure.${failingID}` });
    await driver.screenshot(path.join(runner.runDir, 'codex-launch-failed.png')).catch(() => {});
    const failingSession = cliJSON('automation', 'runs', failingID)[0].session_id;
    await closeSession(failingSession);
    cli('automation', 'delete', failingID);
    failingID = 0;
  });

  await runner.step('leg4_restart_keeps_definitions_and_runs', async () => {
    await client.quitApp();
    await observer.close();
    try { cli('daemon', 'stop'); } catch {}
    cli('daemon', 'ensure');
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await observer.connect();
    await openDrawer();
    const state = await poll(async () => { const s = await drawer(); return definitionIn(s, manualID) && definitionIn(s, scheduledID) ? s : null; }, 'both definitions after the restart', RESTART_READY_TIMEOUT_MS);
    runner.assert(definitionIn(state, manualID).enabled && definitionIn(state, scheduledID).enabled, 'both definitions stay on across the restart', state.definitions);
    await select(manualID);
    const withRuns = await poll(async () => { const s = await drawer(); return runsIn(s).length ? s : null; }, 'the run history after the restart', RESTART_READY_TIMEOUT_MS);
    runner.assert(runsIn(withRuns).length === 1 && runsIn(withRuns)[0].id === firstRunID, 'the same single run survives the restart', runsIn(withRuns));
    runner.assert(await has(`automations.run-open.${firstRunID}`), 'the run still opens its session after the restart');
    runner.assert(Boolean(await observer.waitFor(() => observer.getSession(manualSessionID), 'the run\'s session after the restart', RESTART_READY_TIMEOUT_MS).catch(() => null)), 'the run\'s session comes back with the daemon', { manualSessionID });
    await closeDrawer();
  });

  await runner.step('cleanup_probe_session', async () => {
    cli('automation', 'disable', manualID);
    cli('automation', 'disable', scheduledID);
    const seedID = (cliJSON('automation', 'runs', manualID) || []).find((r) => r.id === firstRunID)?.seed_id;
    runner.assert(Boolean(seedID), 'the delivered manual run has a Garden seed', { firstRunID, seedID });
    await closeSession(manualSessionID);
    manualSessionID = '';
    cli('seed', 'wither', seedID, '-m', 'Native automation surface harness fixture complete');
    const seed = (cliJSON('seed', 'ls', '--json').seeds || []).find((x) => x.id === seedID);
    runner.assert(seed?.status === 'withered', 'the run\'s seed is withered after its session closes', seed);
    cli('automation', 'delete', manualID);
    cli('automation', 'delete', scheduledID);
    const left = cliJSON('automation', 'list') || [];
    runner.assert(!left.some((d) => d.id === manualID || d.id === scheduledID), 'both fixture definitions are gone', left);
    manualID = 0;
    scheduledID = 0;
  });

  await runner.finishSuccess({ instance, manualID, scheduledID, firstRunID });
} catch (error) {
  try { runner.writeJson('failure-automations-state.json', await drawer()); } catch {}
  try { await driver.screenshot(path.join(runner.runDir, 'failure.png')); } catch {}
  await runner.finishFailure(error, { instance, manualID, scheduledID, firstRunID });
  process.exitCode = 1;
}
