#!/usr/bin/env node
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import crypto from 'node:crypto';
import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';
import { outpostSsh } from './outpostSsh.mjs';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { stepClock } from './stepClock.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { MOCK_AGENT_EXECUTABLE, writeMockAgentFixture } from './mockAgent.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Outposts settings verification requires a named instance');
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-OUTPOSTS-SETTINGS', tier: 'local', prefix: 'native-outposts-settings', allowRealAgents: false,
  metadata: { focus: 'In the native app: opt in and add an outpost, answer ssh in a connection terminal, a remote shell, its split and a remote agent, stalled, unreachable and connecting tiles with refused typing that is never replayed, offline queue, Home and palette, and Re-pair and Remove' },
});
const root = fs.mkdtempSync(path.join(os.platform() === 'darwin' ? '/private/tmp' : os.tmpdir(), 'attn-bridge-'));
const ssh = await outpostSsh(root);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const homeClock = await stepClock(path.join(root, 'home-clock.sock'), observer.connectTimeoutMs);
const outpostClock = await stepClock(path.join(root, 'outpost-clock.sock'), observer.connectTimeoutMs);
const client = new UiAutomationClient({ appPath: options.appPath, launchEnv: { ...ssh.env, ...homeClock.env } });
const driver = createWindowDriver({ appPath: options.appPath, client });
const binary = appCliInTree(options.appPath);
const exec = promisify(execFile);
const children = [];
const created = [];
const env = instanceCliEnv(instance, ssh.env);
const run = (command, args, childEnv = env) => exec(command, args, { env: childEnv });
const mac = process.platform === 'darwin';
const OPEN_SETTINGS = mac ? 'mod+,' : 'mod+shift+,';
const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));
const receive = (predicate) => observer.waitForMessage((event) => event.event === 'outposts_updated' && predicate(event.outposts) && event.outposts, 'outposts state', observer.connectTimeoutMs);
const named = async (target) => (await driver.find(target))?.name ?? '';
const flat = (value) => value.replace(/\s+/g, ' ').trim();
const studio = (rows) => rows.find((row) => row.name === 'Studio');
let studioId = '';
const row = (part, id = studioId) => ({ key: `settings.outpost.${id}${part ? `.${part}` : ''}` });

async function removeScenarioOutposts() {
  const rows = observer.outposts.filter((item) => ['Studio', 'Studio alias'].includes(item.name) && item.destination.startsWith('scenario-outpost'));
  if (rows.length === 0) return;
  const previous = observer.getSetting('outposts_enabled');
  await run(binary, ['settings', 'set', 'outposts_enabled', 'true']);
  for (const item of rows) {
    const removed = receive((list) => list.every((entry) => entry.id !== item.id));
    observer.send({ cmd: 'remove_outpost', id: item.id });
    await removed;
  }
  await run(binary, ['settings', 'set', 'outposts_enabled', previous]);
}
async function port() {
  const server = net.createServer();
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const value = server.address().port;
  await new Promise((resolve) => server.close(resolve));
  return value;
}
function start(command, args, childEnv, stdio) {
  const child = spawn(command, args, { env: childEnv, stdio });
  children.push(child);
  return child;
}
async function openOutposts() {
  if (!(await driver.find({ key: 'settings-dialog' }))) await driver.pressKey(OPEN_SETTINGS);
  await driver.waitFor({ key: 'settings-dialog' });
  await driver.click({ key: 'settings.nav.outposts' });
  await driver.state('settings', { expect: { activeSection: 'outposts' } });
}
async function closeSettings() {
  await driver.pressKey('Escape');
  await driver.waitFor({ key: 'settings-dialog' }, { gone: true });
}
async function add(name, address) {
  await driver.typeInto({ key: 'settings.outposts.name' }, name);
  await driver.typeInto({ key: 'settings.outposts.destination' }, address);
  await driver.click({ key: 'settings.outposts.submit' });
}
/** The pane `session` holds, once a desktop holds it with its terminal. */
const paneOf = (session) => observer.waitFor(() => {
  for (const desktop of observer.desktops) {
    const pane = desktop.panes.find((p) => p.session_id === session && p.runtime_id);
    if (pane) return { session, pane: pane.pane_id, runtime: pane.runtime_id, desktop: desktop.id };
  }
  return null;
}, `a tile for ${session}`, 30_000);
const screen = (tile, includes) => driver.state(`terminal:${tile.runtime}`, { includes, timeoutMs: 30_000 });
const text = async (tile) => ((await driver.state(`terminal:${tile.runtime}`)) ?? []).join('\n');
let slot = 0;
/** Shows the scenario's desktop, then gives `tile` the keyboard. */
async function focusTile(tile) {
  if (!(await driver.find({ key: `tile.${tile.pane}` }))) {
    await driver.click({ key: observer.getSetting('queue_mode_enabled') === 'true' ? `queue.desktop.${slot}` : `sidebar.desktop.${tile.desktop}` });
    await observer.waitFor(() => observer.currentDesktopId() === tile.desktop, `the desktop of ${tile.session}`);
  }
  if (!(await driver.find({ key: `tile.${tile.pane}` }))) {
    observer.send({ cmd: 'desktop_show_session', session_id: tile.session, request_id: crypto.randomUUID() });
  }
  await driver.waitFor({ key: `tile.${tile.pane}` });
  await driver.click({ key: `tile.${tile.pane}.header` });
  await observer.waitFor(() => observer.desktop(tile.desktop)?.active_pane_id === tile.pane, `${tile.pane} active`);
}
const strip = (tile) => named({ key: `tile.${tile.runtime}.reach` });
const offline = async (tile) => (await named({ key: `tile.${tile.pane}.state` })) === 'offline';
/** Whether `tile` plays the flash of a refused key. */
async function flashes(tile) {
  const keys = [];
  const walk = (node) => { if (node.key) keys.push(node.key); (node.children ?? []).forEach(walk); };
  walk(await driver.tree());
  return keys.some((key) => key.startsWith(`tile.refused.${tile.runtime}.flash.`));
}
/** Types `typed` into `tile`, whose machine does not take typing: the tile flashes and its
 * screen stays as it was. */
async function refusesTyping(tile, typed, what) {
  const before = await text(tile);
  await driver.typeInto(null, typed);
  runner.assert(await flashes(tile), `${what}: a refused key flashes the tile`);
  runner.assert((await text(tile)) === before, `${what}: refused typing leaves the screen as it was`, { before });
}

runner.registerCleanup('remove_scratch', () => fs.rmSync(root, { recursive: true, force: true }));
runner.registerCleanup('close_clocks', async () => {
  await homeClock.close();
  await outpostClock.close();
});
runner.registerCleanup('stop_children', async () => {
  for (const child of [...children].reverse()) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    const exited = once(child, 'exit');
    child.kill('SIGCONT');
    child.kill('SIGTERM');
    await exited;
  }
});
runner.registerCleanup('stop_home_daemon', () => run(binary, ['daemon', 'stop']));
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('close_ssh', () => ssh.close());
try {
  const outpostData = path.join(root, 'outpost');
  fs.mkdirSync(outpostData, { mode: 0o700 });
  runner.registerCleanup('save_outpost_log', () => {
    const source = path.join(outpostData, 'daemon.log');
    if (fs.existsSync(source)) fs.copyFileSync(source, path.join(runner.runDir, 'outpost-daemon.log'));
  });
  const outpostEnv = instanceCliEnv(instance, {
    ...outpostClock.env,
    ATTN_DATA_DIR: outpostData,
    ATTN_HARNESS_DATA_DIR: outpostData,
    ATTN_HARNESS_NOTEBOOK_ROOT: path.join(outpostData, 'notebook'), ATTN_WS_PORT: String(await port()), ATTN_DAEMON_READY_FD: '3',
    ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off',
    ATTN_HARNESS_SKILL_SYNC: 'off', ATTN_PTY_BACKEND: 'worker', ATTN_CACHED_SHELL_ENV: '[]',
    ATTN_TOOL_HOME: path.join(root, 'outpost-tools'),
  });
  const log = fs.openSync(path.join(runner.runDir, 'outpost.log'), 'w');
  const outpost = start(binary, ['daemon'], outpostEnv, ['ignore', log, log, 'pipe']);
  fs.closeSync(log);
  const [ready] = await once(outpost.stdio[3], 'data');
  runner.assert(ready.toString().includes('ready'), 'outpost reported readiness', ready.toString());
  const freezeOutpostRightAfterHeartbeat = async () => {
    await homeClock.awaitNextArming('bridge-stall');
    outpost.kill('SIGSTOP');
  };
  const address = 'scenario-outpost';
  await ssh.route(address, outpostEnv, binary);
  ssh.requireConfirmation(address);
  await ssh.route('scenario-outpost-alias', outpostEnv, binary);
  await client.quitApp();
  await run(binary, ['daemon', 'stop']);
  await launchFreshNativeAppAndConnect(client, observer);
  await driver.setWindowBounds({ x: 0, y: 0, width: 1900, height: 1000 });
  await removeScenarioOutposts();
  runner.registerCleanup('remove_scenario_outposts', removeScenarioOutposts);
  for (const [key, value] of [['outposts_enabled', 'false'], ['queue_mode_enabled', 'false'], ['auto_settle_enabled', 'false']]) {
    const previous = observer.getSetting(key);
    runner.registerCleanup(`restore_${key}`, () => run(binary, ['settings', 'set', key, previous]));
    await run(binary, ['settings', 'set', key, value]);
  }
  runner.registerCleanup('close_created_sessions', async () => {
    outpost.kill('SIGCONT');
    for (const id of created.toReversed()) {
      if (observer.getSession(id)) observer.send({ cmd: 'unregister', id });
    }
  });
  slot = [1, 2, 3, 4, 5, 6, 7, 8, 9].find((n) => !observer.desktops.some((d) => d.shortcut_slot === n));
  const made = await observer.profileCommand('desktop_create', { profile_id: observer.profileId, shortcut_slot: slot });
  const desktopId = made.desktops?.[0]?.id;
  runner.assert(desktopId, 'the scenario makes its own desktop', made);
  runner.registerCleanup('close_scenario_desktop', () => {
    const left = observer.desktop(desktopId);
    return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
  });
  const startedOn = observer.currentDesktopId();
  if (startedOn) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(startedOn));
  await observer.returnToDesktop(desktopId);

  let caller, callerEnv, remote, split, agent;
  const cli = async (...args) => (await run(binary, args, callerEnv)).stdout;

  await runner.step('keyboard_opt_in_and_add', async () => {
    await driver.pressKey(OPEN_SETTINGS);
    await driver.waitFor({ key: 'settings.nav-off.outposts' });
    await driver.click({ key: 'settings.nav.experimental' });
    await driver.click({ key: 'settings.feature.outposts_enabled' });
    await driver.waitFor({ key: 'settings.nav-off.outposts' }, { gone: true });
    await shot('00-experimental');
    await openOutposts();
    const refused = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'refused' && item.connectable));
    await add('Studio', address);
    const refusal = studio(await refused);
    studioId = refusal.id;
    runner.assert(flat(await named(row('error.text'))) === flat(refusal.last_error), 'ssh stderr is shown whole', refusal);
    const registered = observer.waitForMessage((event) => event.event === 'session_registered' && event.session.command_terminal && event.session, 'connection terminal', observer.connectTimeoutMs);
    const connected = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'connected'));
    await driver.click(row('connect'));
    const terminal = await registered;
    created.push(terminal.id);
    await driver.waitFor({ key: 'settings-dialog' }, { gone: true });
    const tile = await paneOf(terminal.id);
    await screen(tile, 'Are you sure you want to continue connecting');
    await shot('00-ssh-prompt');
    await driver.typeInto(null, 'yes');
    await driver.pressKey('Enter');
    const studioRow = studio(await connected);
    await driver.run('session.goToDashboard');
    await driver.waitFor({ key: 'home' });
    runner.assert(!(await driver.find({ key: `home.session.${terminal.id}` })), 'the connection terminal is not on Home');
    await focusTile({ ...tile, session: terminal.id });
    const ledger = observer.waitForMessage((event) => event.event === 'session_list_result' && event.request_id === 'connection-ledger' && event, 'connection ledger', observer.connectTimeoutMs);
    observer.send({ cmd: 'session_list', all: true, request_id: 'connection-ledger' });
    runner.assert(!(await ledger).result.entries.some((entry) => entry.id === terminal.id), 'connection terminal has no ledger row');
    await shot('00-ssh-complete');
    await driver.run('session.close');
    await driver.waitFor({ key: `tile.${tile.pane}` }, { gone: true });
    created.splice(created.indexOf(terminal.id), 1);
    runner.assert(Boolean(studioRow.host_key_fingerprint && studioRow.build && studioRow.daemon_id), 'first contact pins identity and build', studioRow);
    await openOutposts();
    await driver.state('outposts', { includes: 'connected' });
    runner.assert((await named(row('state'))) === 'connected', 'the row reads connected');
    runner.assert((await named(row('host-key'))).includes(studioRow.host_key_fingerprint), 'the row shows the pinned host key');
    await shot('01-connected');
    await closeSettings();
  });

  await runner.step('remote_cli_shell_and_native_split', async () => {
    writeMockAgentFixture(runner.sessionDir, { minimumWorkingMs: 0, turns: [{ includes: 'Local attention', actions: [{ type: 'reply', text: 'LOCAL_READY', state: 'waiting_input' }] }] });
    const callerId = crypto.randomUUID();
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === callerId && m, 'operator spawned');
    observer.send({ cmd: 'spawn_session', id: callerId, agent: 'claude', cwd: runner.sessionDir, profile_id: observer.profileId, placement: { desktop_id: desktopId }, label: 'Outpost operator', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'the operator starts');
    created.push(callerId);
    caller = await paneOf(callerId);
    callerEnv = instanceCliEnv(instance, { ...ssh.env, ATTN_TERMINAL_ID: caller.runtime });
    const startDirectory = path.join(fs.realpathSync(root), 'remote-project');
    fs.mkdirSync(startDirectory);
    const opened = JSON.parse(await cli('terminal', 'open', '--host', 'Studio', '--cwd', startDirectory, '--json'));
    created.push(opened.session_id);
    remote = await paneOf(opened.session_id);
    await focusTile(remote);
    runner.assert((await named({ key: `tile.${remote.pane}.host` })) === 'Studio', 'the remote tile names its outpost');
    const firstOutput = await cli('terminal', 'run', opened.terminal_id, 'printf "ATTN_HOST=%s\\n" "$ATTN_HOST"; pwd; cd /', '--wait');
    runner.assert(firstOutput.includes('ATTN_HOST=Studio') && firstOutput.replace(/\s+/g, '').includes(startDirectory), 'the CLI opens the shell on its outpost in the requested start directory', firstOutput);
    const registered = observer.waitForMessage((m) => m.event === 'session_registered' && m.session?.host && m.session.id !== remote.session && m.session, 'native split on the outpost');
    await focusTile(remote);
    await driver.run('terminal.splitVertical');
    const splitSession = await registered;
    created.push(splitSession.id);
    split = await paneOf(splitSession.id);
    const splitOutput = await cli('terminal', 'run', split.runtime, 'printf "ATTN_HOST=%s\\n" "$ATTN_HOST"; pwd', '--wait');
    runner.assert(splitSession.host === observer.getSession(remote.session).host && splitOutput.includes('ATTN_HOST=Studio') && splitOutput.replace(/\s+/g, '').includes(startDirectory), 'the split stays on the same outpost and starts where the shell started', { splitSession, splitOutput });
    await screen(split, 'ATTN_HOST=Studio');
    runner.assert((await named({ key: `tile.${split.pane}.host` })) === 'Studio', 'the split names its outpost');
    runner.writeJson('remote-split.json', { remote, split, startDirectory, firstOutput, splitOutput });
    await shot('02-remote-split');
  });

  await runner.step('remote_agent_and_queue', async () => {
    await run(binary, ['settings', 'set', 'queue_mode_enabled', 'true']);
    if (!observer.outposts.some((item) => item.name === 'Studio' && item.notebook?.state === 'in_sync')) {
      await receive((rows) => rows.some((item) => item.name === 'Studio' && item.notebook?.state === 'in_sync'));
    }
    await run(binary, ['settings', 'set', 'claude_executable', MOCK_AGENT_EXECUTABLE], outpostEnv);
    const cwd = path.join(fs.realpathSync(root), 'remote-agent');
    writeMockAgentFixture(cwd, { minimumWorkingMs: 0, turns: [{ includes: 'Remote attention', actions: [{ type: 'reply', text: 'REMOTE_READY', state: 'waiting_input' }] }] });
    const id = crypto.randomUUID();
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, 'remote Claude spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'claude', host: 'Studio', cwd, profile_id: observer.profileId, placement: { desktop_id: desktopId }, label: 'Outpost Claude', initial_prompt: 'Remote attention', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session runs Claude on Studio');
    created.push(id);
    agent = await paneOf(id);
    await screen(agent, 'REMOTE_READY');
    await focusTile(caller);
    await driver.typeInto(null, 'Local attention');
    await driver.pressKey('Enter');
    await screen(caller, 'LOCAL_READY');
    await observer.waitFor(() => observer.getSession(caller.session)?.turn_owed && observer.getSession(agent.session)?.turn_owed, 'both turns owed');
    await driver.waitFor({ role: 'button', name: '2 waiting' });
    await shot('02-agent-online');
  });

  await runner.step('stalled_agent_notice_and_recovery', async () => {
    await focusTile(agent);
    const stalled = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'stalled'));
    await freezeOutpostRightAfterHeartbeat();
    await homeClock.advance('3s');
    await stalled;
    await driver.state('outposts', { includes: 'stalled' });
    runner.assert((await strip(agent)) === 'Studio not responding', 'a stalled agent says its outpost is not responding');
    runner.assert(!(await offline(agent)), 'a stalled agent is not offline');
    await shot('03-stalled-agent');
    const recovered = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'connected'));
    outpost.kill('SIGCONT');
    await recovered;
    await driver.waitFor({ key: `tile.${agent.runtime}.reach` }, { gone: true });
    await shot('03-stalled-agent-recovered');
  });

  await runner.step('frozen_outpost_and_recovery', async () => {
    await focusTile(split);
    const fifo = path.join(root, 'screen-update');
    await run('mkfifo', [fifo]);
    await cli('terminal', 'run', split.runtime, `printf 'C13_%s\\n' WAITING; cat '${fifo}'`);
    await screen(split, 'C13_WAITING');
    const before = await text(split);
    const stalled = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'stalled'));
    const lost = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'lost'));
    await freezeOutpostRightAfterHeartbeat();
    await homeClock.advance('3s');
    await stalled;
    await driver.state('outposts', { includes: 'stalled' });
    runner.assert(!(await offline(split)), 'a stalled shell is not offline');
    runner.assert((await strip(split)) === 'Studio not responding', 'a stalled shell says its outpost is not responding');
    await shot('03-stalled-shell');
    await homeClock.advance('57s');
    await lost;
    await homeClock.awaitTimer('bridge-answer');
    await homeClock.advance('10s');
    await homeClock.awaitTimer('bridge-retry');
    await driver.state('outposts', { includes: 'unreachable' });
    runner.assert((await strip(split)) === 'Studio unreachable · typing is not sent', 'an unreachable shell says typing is not sent');
    runner.assert(await offline(split), 'an unreachable shell reads offline');
    runner.assert((await text(split)) === before, 'going offline keeps the screen', { before });
    runner.assert((await named({ key: `queue-turn.${agent.session}.state` })) === 'offline', 'the remote agent keeps its queue row, marked offline');
    await driver.waitFor({ role: 'button', name: '1 waiting · 1 unreachable' });
    await shot('04-agent-offline');
    await driver.run('session.jumpToWaiting');
    await observer.waitFor(() => observer.desktop(desktopId)?.active_pane_id === caller.pane, 'the jump key opens the local operator');
    runner.assert(observer.getSession(agent.session).turn_owed, 'the offline agent keeps its turn');
    await driver.run('session.goToDashboard');
    runner.assert((await named({ key: `home.session.${agent.session}.state` })) === 'offline', 'Home marks the agent offline');
    runner.assert((await named({ key: 'home.group.turns.apart' })) === '1 unreachable', 'Home counts the turn apart');
    runner.assert(!(await driver.find({ key: 'home.all-settled' })), 'Home does not read all settled');
    await shot('04-offline-home');
    await driver.run('ui.actionMenu');
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, 'Outpost Claude');
    runner.assert((await named({ key: `palette.row.agent:${agent.session}.state` })) === 'unreachable', 'the palette reads the agent unreachable');
    const hints = [];
    for (let i = 0; ; i += 1) {
      const hint = await driver.find({ key: `palette.hint.${i}` });
      if (!hint) break;
      hints.push(hint.name);
    }
    runner.assert(!hints.some((h) => /settle|snooze/.test(h)), 'the palette offers no settle or snooze for it', hints);
    await driver.run('session.settle');
    await driver.run('session.snooze');
    runner.assert(observer.getSession(agent.session).turn_owed && !observer.getSession(agent.session).turn_snoozed_until, 'turn keys leave the offline turn as it was');
    await shot('04-offline-palette');
    await driver.pressKey('Escape');
    await focusTile(split);
    await refusesTyping(split, 'C13_REFUSED_INPUT', 'an unreachable shell');
    await shot('04-refused-input');
    await driver.pressKey('Enter');
    await fs.promises.writeFile(fifo, 'C13_SCREEN_CURRENT\n');
    const recovered = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'connected' && !item.last_error));
    outpost.kill('SIGCONT');
    await homeClock.advance('2s');
    await recovered;
    await driver.waitFor({ key: `tile.${split.runtime}.reach` }, { gone: true });
    await screen(split, 'C13_SCREEN_CURRENT');
    await cli('terminal', 'wait', split.runtime, '--until', 'prompt');
    await driver.typeInto(null, "printf 'C13_%s\\n' RECOVERED");
    await driver.pressKey('Enter');
    await screen(split, 'C13_RECOVERED');
    runner.assert(!(await text(split)).includes('C13_REFUSED_INPUT'), 'refused typing is never replayed');
    await driver.waitFor({ role: 'button', name: '2 waiting' });
    await shot('05-recovered-shell');
  });

  await runner.step('connecting_tiles_refuse_input_until_repaired', async () => {
    await openOutposts();
    const connecting = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'connecting'));
    const releaseRequests = ssh.holdRequests(address);
    runner.registerCleanup('release_connecting_requests', releaseRequests);
    await driver.click(row('repair'));
    await connecting;
    await closeSettings();
    for (const [name, tile] of [['shell', split], ['agent', agent]]) {
      await focusTile(tile);
      await driver.state('outposts', { includes: 'connecting' });
      runner.assert((await strip(tile)) === 'Connecting to Studio · typing is not sent', `the connecting ${name} says typing is not sent`);
      runner.assert(!(await offline(tile)), `the connecting ${name} is not offline`);
      await refusesTyping(tile, 'C13_CONNECTING_REFUSED', `a connecting ${name}`);
      await shot(`05-connecting-${name}`);
    }
    const recovered = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'connected' && !item.last_error));
    releaseRequests();
    await recovered;
    for (const [name, tile, marker] of [['shell', split, 'C13_RECOVERED'], ['agent', agent, 'REMOTE_READY']]) {
      await focusTile(tile);
      await driver.waitFor({ key: `tile.${tile.runtime}.reach` }, { gone: true });
      await screen(tile, marker);
      runner.assert(!(await text(tile)).includes('C13_CONNECTING_REFUSED'), `connecting ${name} typing is never replayed`);
      await shot(`05-connecting-${name}-recovered`);
    }
  });

  await runner.step('full_refusal_keyboard_repair_and_remove', async () => {
    await openOutposts();
    const refusal = receive((rows) => rows.some((item) => item.name === 'Studio alias' && item.state === 'refused'));
    await add('Studio alias', 'scenario-outpost-alias');
    const refused = (await refusal).find((item) => item.name === 'Studio alias');
    await driver.state('outposts', { includes: 'Studio alias' });
    runner.assert((await named(row('state', refused.id))) === 'refused', 'the alias reads refused');
    runner.assert(flat(await named(row('error.text', refused.id))) === flat(refused.last_error), 'the refusal is shown whole', refused);
    await shot('05-refused');
    const removed = receive((rows) => !rows.some((item) => item.name === 'Studio alias'));
    await driver.click(row('remove', refused.id));
    await removed;
    await driver.waitFor(row('', refused.id), { gone: true });
    const before = studio(observer.outposts).state_since;
    const repaired = receive((rows) => rows.some((item) => item.name === 'Studio' && item.state === 'connected' && item.state_since !== before));
    await driver.click(row('repair'));
    await repaired;
    const empty = receive((rows) => rows.length === 0);
    await driver.click(row('remove'));
    runner.assert((await named(row('remove'))) === 'Remove and close 3 terminals', 'Remove asks to close the outpost\'s terminals');
    await shot('06-remove-confirmation');
    await driver.click(row('remove'));
    await empty;
    for (const tile of [remote, split, agent]) created.splice(created.indexOf(tile.session), 1);
    await driver.waitFor(row(''), { gone: true });
    await driver.click({ key: 'settings.nav.experimental' });
    await driver.click({ key: 'settings.feature.outposts_enabled' });
    await driver.waitFor({ key: 'settings.nav-off.outposts' });
    await closeSettings();
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  await shot('failure').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
