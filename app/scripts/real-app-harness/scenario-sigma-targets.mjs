#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { launchFreshAppAndConnect, openHarnessDesktop, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree } from './platform.mjs';
import { processCwd } from './processCwd.mjs';
import { waitForFirstDesktopPane, waitForPaneText } from './scenarioAssertions.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);
const SCRIPTED_MODEL = 'faux/scripted';

const options = parseCommonArgs(process.argv.slice(2));
const executable = process.env.ATTN_SCENARIO_SIGMA_EXECUTABLE;
const plugin = process.env.ATTN_SCENARIO_SIGMA_PLUGIN_DIR;
if (!plugin || !fs.existsSync(plugin)) throw new Error("ATTN_SCENARIO_SIGMA_PLUGIN_DIR must name the extracted Sigma plugin package");
const project = process.env.ATTN_SCENARIO_SIGMA_DEVVM_PROJECT;
if (!executable || !fs.existsSync(executable)) {
  throw new Error('ATTN_SCENARIO_SIGMA_EXECUTABLE must name the Sigma fork build, such as <sigma checkout>/packages/sigma/dist/cli.js');
}
if (!project || !fs.existsSync(project)) {
  throw new Error('ATTN_SCENARIO_SIGMA_DEVVM_PROJECT must name a folder whose DevVM VM exists (run `devvm` in it first)');
}
const vm = JSON.parse(execFileSync('devvm', ['ls', '--json', '--no-input'], { encoding: 'utf8' })).vms
  .find(entry => entry.folder === fs.realpathSync(project))?.name;
if (!vm) throw new Error(`devvm on PATH knows no VM for ${project}`);

const instance = currentHarnessInstance();
assertFreshWorldTargetSafe({ instance, appPath: options.appPath });
const attnCli = appCliInTree(options.appPath);
const sigmaHome = fs.mkdtempSync('/tmp/sgtg-');
const sigma = path.join(sigmaHome, 'sigma');
fs.writeFileSync(sigma, `#!/bin/sh\nPATH=${JSON.stringify(path.dirname(execFileSync('which', ['devvm'], { encoding: 'utf8' }).trim()))}:$PATH exec ${JSON.stringify(executable)} "$@"\n`, { mode: 0o755 });
const sigmaEnv = { ATTN_SIGMA_EXECUTABLE: sigma, SIGMA_HOME: sigmaHome, SIGMA_MODELS: 'scripted' };

const runner = createScenarioRunner(options, {
  scenarioId: 'SIGMA-TARGETS',
  tier: 'local',
  prefix: 'sigma-targets',
  metadata: { agent: 'sigma', focus: 'Sigma sessions run on the execution target they launched with, and an unavailable target never runs locally', instance, executable, project, vm },
});
const client = new UiAutomationClient({ ...options, launchEnv: sigmaEnv });
const observer = new DaemonObserver(options);
const cli = (...args) => execFileSync(attnCli, args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const stopDaemon = () => execFileAsync(attnCli, ['daemon', 'stop'], { env: instanceCliEnv(instance) });
const baseline = new Map();
const sessions = [];
const noVm = path.join(runner.sessionDir, 'no-vm');
fs.mkdirSync(noVm, { recursive: true });

async function set(key, value) {
  if (!baseline.has(key)) baseline.set(key, observer.getSetting(key));
  const request_id = crypto.randomUUID();
  const response = observer.receive(event => event.request_id === request_id, `set_setting ${key} response`);
  observer.send({ cmd: 'set_setting', key, value, request_id });
  const result = await response;
  if (!result.success) throw new Error(result.error);
}

async function targets(cwd) {
  const request_id = crypto.randomUUID();
  const response = observer.receive(event => event.event === 'harness_targets_result' && event.request_id === request_id, `targets of ${cwd}`, 60_000);
  observer.send({ cmd: 'harness_targets', request_id, harness: 'sigma', cwd });
  const result = await response;
  runner.assert(result.success, `Sigma lists the targets of ${cwd}`, result);
  return Object.fromEntries(result.targets.map(target => [target.id, target]));
}

async function launch(cwd, target, label) {
  const id = crypto.randomUUID();
  const spawned = observer.receive(event => event.event === 'spawn_result' && event.id === id, `${label} spawned`, 60_000);
  observer.send({ cmd: 'spawn_session', id, agent: 'sigma', cwd, profile_id: observer.profileId, placement: {}, label, cols: 120, rows: 36, execution_target: target });
  const result = await spawned;
  runner.assert(result.success, `${label} launches on ${target}`, result);
  sessions.push(id);
  await observer.waitForSession({ id, timeoutMs: 30_000 });
  await client.request('select_session', { sessionId: id });
  return id;
}

function awaitSession(id, test, description, timeoutMs = 60_000) {
  return observer.waitFor(() => {
    const session = observer.getSession(id);
    return session && test(session) ? session : null;
  }, description, timeoutMs);
}

const awaitState = (id, states, description, timeoutMs) =>
  awaitSession(id, session => [states].flat().includes(session.state), `${description} (${[states].flat().join(' or ')})`, timeoutMs);

async function screen(id, name) {
  const pane = await waitForFirstDesktopPane(client, id, `${name} view`, 20_000);
  const snapshot = observer.receive(event => event.event === 'get_screen_snapshot_result' && event.id === pane.runtimeId, `${name} screen snapshot`);
  observer.send({ cmd: 'get_screen_snapshot', id: pane.runtimeId });
  const text = Buffer.from((await snapshot).screen_snapshot || '', 'base64').toString('utf8')
    .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, '').replace(/\x1b\][^\x07\x1b]*(\x07|\x1b\\)/g, '');
  runner.writeText(`${name}.txt`, text);
  return text;
}

async function shows(id, wanted, name) {
  await client.request('select_session', { sessionId: id });
  const pane = await waitForFirstDesktopPane(client, id, `${name} view`, 20_000);
  await waitForPaneText(client, id, pane.paneId, text => text.replaceAll('\n', '').includes(wanted), `${name} shows ${wanted}`, 60_000);
  await screen(id, name);
}

async function run(id, command, wanted, name) {
  const pane = await waitForFirstDesktopPane(client, id, `${name} view`, 20_000);
  await awaitState(id, ['idle', 'waiting_input'], `${name} is ready for input`);
  await client.request('write_pane', { sessionId: id, paneId: pane.paneId, text: `sh ${command}`, submit: true });
  await awaitState(id, 'pending_approval', `${name}: sh ${command} waits for approval`);
  await client.request('write_pane', { sessionId: id, paneId: pane.paneId, text: 'y', submit: true });
  await waitForPaneText(client, id, pane.paneId, text => text.includes(wanted), `${name} shows ${wanted}`, 60_000);
  await awaitState(id, 'idle', `${name}: the command settles`);
}

async function relaunch() {
  await client.quitApp();
  await observer.close();
  await stopDaemon();
  await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
}

const guestProofDir = fs.mkdtempSync(path.join(project, '.attn-sigma-targets-'));
const guestProof = path.join(guestProofDir, 'guest-proof.txt');
runner.registerCleanup('remove_sigma_home', () => fs.rmSync(sigmaHome, { recursive: true, force: true }));
runner.registerCleanup('remove_guest_proof', () => fs.rmSync(guestProofDir, { recursive: true, force: true }));
runner.registerCleanup('stop_sigma_server', () => {
  const lock = path.join(sigmaHome, 'server.lock');
  const pid = fs.existsSync(lock) ? Number(fs.readFileSync(lock, 'utf8')) : 0;
  if (pid > 0 && processCwd(pid) === fs.realpathSync(sigmaHome)) process.kill(pid, 'SIGTERM');
});
runner.registerCleanup('stop_daemon', () => stopDaemon());
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', () => Promise.all([...baseline].map(([key, value]) => set(key, value ?? ''))));
runner.registerCleanup('uninstall_sigma_plugin', () => cli('plugin', 'uninstall', 'sigma'));
runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, sessions.filter(id => observer.getSession(id))));

try {
  await runner.step('launch_with_sigma', async () => {
    await client.quitApp();
    await stopDaemon();
    await launchFreshAppAndConnect(client, observer);
    cli('plugin', 'install', '--path', plugin);
    await observer.waitFor(() => observer.getSetting('sigma_available') === 'true', 'the Sigma plugin registers', 30_000);
    await set('default_model_sigma', SCRIPTED_MODEL);
    await openHarnessDesktop(client, observer);
  });

  await runner.step('list_the_targets_of_a_folder', async () => {
    const withVm = await targets(project);
    runner.assert(withVm.local?.available && withVm.devvm?.available && withVm.devvm.name === vm, `the project offers this machine and its VM ${vm}`, withVm);
    const without = await targets(noVm);
    runner.assert(without.local?.available && without.devvm && !without.devvm.available && without.devvm.detail.includes('devvm'), 'a folder without a VM offers DevVM as unavailable and says how to create one', without);
    runner.writeJson('targets.json', { project: withVm, noVm: without });
  });

  let onVm;
  await runner.step('launch_on_devvm_shows_the_target_and_runs_in_the_guest', async () => {
    onVm = await launch(project, 'devvm', 'on-devvm');
    await awaitState(onVm, ['idle', 'waiting_input'], 'the DevVM session is ready');
    await shows(onVm, `target: DevVM ${vm} (mounted`, 'devvm-launch');
    await shows(onVm, 'edits appear in the host worktree', 'devvm-launch');
    await run(onVm, `echo "guest-$(uname -s)" | tee ${path.basename(guestProofDir)}/guest-proof.txt`, 'guest-Linux', 'devvm');
    runner.assert(fs.readFileSync(guestProof, 'utf8').trim() === 'guest-Linux', 'the guest write appears in the host folder');
  });

  await runner.step('reattach_after_a_restart_keeps_the_target', async () => {
    await relaunch();
    await observer.waitForSession({ id: onVm, timeoutMs: 30_000 });
    await shows(onVm, `target: DevVM ${vm}`, 'devvm-reattached');
    await run(onVm, 'echo "after-restart-$(uname -s)"', 'after-restart-Linux', 'devvm after restart');
  });

  await runner.step('resume_from_the_ledger_keeps_the_target', async () => {
    const pane = await waitForFirstDesktopPane(client, onVm, 'devvm view', 20_000);
    await client.request('close_pane', { sessionId: onVm, paneId: pane.paneId });
    await observer.waitFor(() => (observer.getSession(onVm) ? null : true), 'the closed tile ends the session', 15_000);
    cli('session', 'reopen', onVm);
    await observer.waitForSession({ id: onVm, timeoutMs: 30_000 });
    await awaitState(onVm, ['idle', 'waiting_input'], 'the resumed session is ready');
    await shows(onVm, `target: DevVM ${vm}`, 'devvm-resumed');
    await run(onVm, 'echo "resumed-$(uname -s)"', 'resumed-Linux', 'devvm after resume');
  });

  await runner.step('launch_on_local_runs_on_this_machine', async () => {
    const local = await launch(project, 'local', 'on-local');
    await awaitState(local, ['idle', 'waiting_input'], 'the local session is ready');
    await shows(local, 'target: this machine', 'local-launch');
    await run(local, 'echo "host-$(uname -s)"', 'host-Darwin', 'local');
  });

  await runner.step('an_unavailable_target_is_reported_and_never_runs_locally', async () => {
    const refused = await launch(noVm, 'devvm', 'no-vm');
    const exited = await awaitSession(refused, session => session.terminal_exit, 'the view exits on the unavailable target');
    await shows(refused, 'execution target unavailable: No DevVM VM for', 'unavailable');
    const request_id = crypto.randomUUID();
    const reloaded = observer.receive(event => event.event === 'reload_session_result' && event.id === refused, 'resume of the refused session');
    observer.send({ cmd: 'reload_session', id: refused, cols: 120, rows: 36, request_id });
    runner.assert((await reloaded).success, 'resume starts the view again', await reloaded);
    await awaitSession(refused, session => session.terminal_exit && session.terminal_exit.at !== exited.terminal_exit.at, 'the resumed view exits on the same unavailable target');
    await shows(refused, 'execution target unavailable: No DevVM VM for', 'unavailable-resumed');
  });

  console.log(JSON.stringify(await runner.finishSuccess({ sessions, vm }), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error, { sessions, vm }), null, 2));
  process.exitCode = 1;
}
