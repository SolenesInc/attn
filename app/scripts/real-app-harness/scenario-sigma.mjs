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
if (!executable || !fs.existsSync(executable)) {
  throw new Error('ATTN_SCENARIO_SIGMA_EXECUTABLE must name the Sigma fork build, such as <sigma checkout>/packages/sigma/dist/cli.js');
}
const instance = currentHarnessInstance();
assertFreshWorldTargetSafe({ instance, appPath: options.appPath });
const attnCli = appCliInTree(options.appPath);
const sigmaHome = fs.mkdtempSync('/tmp/sgsc-');
const sigmaEnv = { ATTN_SIGMA_EXECUTABLE: executable, SIGMA_HOME: sigmaHome, SIGMA_MODELS: 'scripted' };

const runner = createScenarioRunner(options, {
  scenarioId: 'SIGMA',
  tier: 'local',
  prefix: 'sigma',
  metadata: { agent: 'sigma', focus: 'a Sigma session runs, asks, detaches, reattaches and survives a daemon restart', instance, executable },
});
const client = new UiAutomationClient({ ...options, launchEnv: sigmaEnv });
const observer = new DaemonObserver(options);
const cli = (...args) => execFileSync(attnCli, args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const stopDaemon = () => execFileAsync(attnCli, ['daemon', 'stop'], { env: instanceCliEnv(instance) });
const baseline = new Map();
let sessionId = null;

async function set(key, value) {
  if (!baseline.has(key)) baseline.set(key, observer.getSetting(key));
  const request_id = crypto.randomUUID();
  const response = observer.receive(event => event.request_id === request_id, `set_setting ${key} response`);
  observer.send({ cmd: 'set_setting', key, value, request_id });
  const result = await response;
  if (!result.success) throw new Error(result.error);
}

function awaitState(states, description, timeoutMs = 30_000) {
  const wanted = [states].flat();
  return observer.waitFor(() => {
    const session = observer.getSession(sessionId);
    return session && wanted.includes(session.state) ? session : null;
  }, `${description} (${wanted.join(' or ')})`, timeoutMs);
}

async function view() {
  return waitForFirstDesktopPane(client, sessionId, 'the Sigma view', 20_000);
}

async function type(pane, text) {
  await client.request('write_pane', { sessionId, paneId: pane.paneId, text, submit: true });
}

async function approve(pane, command) {
  await type(pane, `sh ${command}`);
  await awaitState('pending_approval', `sh ${command} waits for approval`);
  await type(pane, 'y');
}

async function viewScreen(name) {
  const pane = await view();
  const snapshot = observer.receive(event => event.event === 'get_screen_snapshot_result' && event.id === pane.runtimeId, `${name} screen snapshot`);
  observer.send({ cmd: 'get_screen_snapshot', id: pane.runtimeId });
  const screen = Buffer.from((await snapshot).screen_snapshot || '', 'base64').toString('utf8');
  const text = screen.replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, '').replace(/\x1b\][^\x07\x1b]*(\x07|\x1b\\)/g, '');
  runner.writeText(`${name}.txt`, text);
  return text;
}

async function relaunch() {
  await client.quitApp();
  await observer.close();
  await stopDaemon();
  await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
}

runner.registerCleanup('remove_sigma_home', () => fs.rmSync(sigmaHome, { recursive: true, force: true }));
runner.registerCleanup('stop_sigma_server', () => {
  const lock = path.join(sigmaHome, 'server.lock');
  const pid = fs.existsSync(lock) ? Number(fs.readFileSync(lock, 'utf8')) : 0;
  if (pid > 0 && processCwd(pid) === fs.realpathSync(sigmaHome)) process.kill(pid, 'SIGTERM');
});
runner.registerCleanup('stop_daemon', () => stopDaemon());
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', () => Promise.all([...baseline].map(([key, value]) => set(key, value ?? ''))));
runner.registerCleanup('close_session', () => (sessionId && observer.getSession(sessionId) ? closeScenarioSessions(client, [sessionId]) : null));

try {
  await runner.step('launch_with_sigma', async () => {
    await client.quitApp();
    await stopDaemon();
    await launchFreshAppAndConnect(client, observer);
    await set('sigma_enabled', 'true');
  });

  await runner.step('pin_the_model', async () => {
    await set('default_model_sigma', SCRIPTED_MODEL);
  });

  await runner.step('launch_a_sigma_session', async () => {
    await openHarnessDesktop(client, observer);
    const created = await client.request('create_session', { cwd: runner.sessionDir, label: `sigma-${runner.runId}`, agent: 'sigma' });
    sessionId = created.sessionId;
    await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
    await awaitState(['idle', 'waiting_input'], 'the new session is ready');
  });

  await runner.step('input_runs_and_settles', async () => {
    const pane = await view();
    await type(pane, 'hello from the packaged app');
    await waitForPaneText(client, sessionId, pane.paneId, text => text.split('hello from the packaged app').length > 2, 'the reply that repeats the input', 30_000);
    await awaitState('idle', 'the reply settles');
  });

  await runner.step('approval_then_attn_command_from_this_conversation', async () => {
    const pane = await view();
    await approve(pane, `${attnCli} presence`);
    await waitForPaneText(client, sessionId, pane.paneId, text => text.includes(`session ${sessionId}`), 'attn presence names this session', 30_000);
    await awaitState('idle', 'the command settles');
  });

  await runner.step('a_question_waits_for_the_user_once', async () => {
    const pane = await view();
    await type(pane, 'Which index should the report use? <!-- attn:state=waiting_input -->');
    const waiting = await awaitState('waiting_input', 'the question waits for the user');
    runner.assert(waiting.turn_owed === true, 'the question is owed to the user', { waiting });
    observer.send({ cmd: 'settle_turn', session_id: sessionId });
    await observer.waitFor(() => observer.getSession(sessionId)?.turn_owed ? null : true, 'the user handled the question', 10_000);
  });

  await runner.step('handled_question_stays_handled_after_reconnect', async () => {
    await relaunch();
    const session = await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
    runner.assert(session.state === 'waiting_input' && !session.turn_owed, 'the restarted daemon does not reopen the handled question', { session });
  });

  await runner.step('interrupt_stops_a_run', async () => {
    const pane = await view();
    await approve(pane, 'sleep 60');
    await awaitState('working', 'the long command runs');
    await type(pane, '/interrupt');
    await awaitState(['idle', 'waiting_input'], 'the interrupted run settles', 15_000);
  });

  await runner.step('detach_and_reattach_from_the_ledger', async () => {
    const pane = await view();
    await approve(pane, 'sleep 6 && echo detached-run-finished');
    await awaitState('working', 'the run is going');
    await client.request('close_pane', { sessionId, paneId: pane.paneId });
    await observer.waitFor(() => (observer.getSession(sessionId) ? null : true), 'the closed tile ends the session', 15_000);
    runner.assert(cli('session', 'show', sessionId).includes(sessionId), 'the ledger keeps the closed session');
    cli('session', 'reopen', sessionId);
    await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
    await awaitState('working', 'the reopened session finds its run still going');
    await awaitState('idle', 'the run that kept going while detached settles');
    runner.assert((await viewScreen('reattached-view')).includes('detached-run-finished'), 'the reopened view shows the run that finished while detached');
  });

  await runner.step('a_run_survives_a_daemon_restart', async () => {
    const pane = await view();
    await approve(pane, 'sleep 5 && echo restart-run-finished');
    await awaitState('working', 'the run is going');
    await relaunch();
    await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
    await awaitState('idle', 'the run settles after the restart', 30_000);
    runner.assert((await viewScreen('restarted-view')).includes('restart-run-finished'), 'the view shows the run that finished across the restart');
  });

  console.log(JSON.stringify(await runner.finishSuccess({ sessionId }), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error, { sessionId }), null, 2));
  process.exitCode = 1;
}
