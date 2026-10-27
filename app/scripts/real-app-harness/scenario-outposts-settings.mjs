#!/usr/bin/env node
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import crypto from 'node:crypto';
import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { MOCK_AGENT_EXECUTABLE, writeMockAgentFixture } from './mockAgent.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver, delay } from './platform.mjs';
import { appPids, captureWebKitPids, readLiveDaemonPid, snapshot } from './perfMeasure.mjs';
import { waitForFirstDesktopPane, waitForPaneAttached, waitForPaneInputFocus, waitForPaneShellReady, waitForPaneText } from './scenarioAssertions.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Outposts settings verification requires a named instance');
const runner = createScenarioRunner(options, { scenarioId: 'OutpostsSettings', tier: 'local', prefix: 'outposts-settings', allowRealAgents: false });
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-bridge-'));
const socket = path.join(root, 'agent.sock');
const client = new UiAutomationClient({ ...options, launchEnv: { SSH_AUTH_SOCK: socket } });
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const binary = appDaemonInTree(options.appPath);
const exec = promisify(execFile);
const children = [];
const created = [];
const env = instanceCliEnv(instance, { SSH_AUTH_SOCK: socket });
const run = (command, args, childEnv = env) => exec(command, args, { env: childEnv });
const wait = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: observer.connectTimeoutMs, ...extra });
const text = async selector => (await client.request('dom_text', { selector })).text;
const studio = '.settings-outpost[aria-label="Studio"]';
const alias = '.settings-outpost[aria-label="Studio alias"]';
const toggle = '[role="switch"][aria-label="Outposts"]';
const shot = async name => driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
const receive = predicate => observer.waitForMessage(event => event.event === 'outposts_updated' && predicate(event.outposts) && event.outposts, 'outposts state', observer.connectTimeoutMs);
async function removeScenarioOutposts() {
  const rows = observer.outposts.filter(row => ['Studio', 'Studio alias'].includes(row.name) && /^(127\.0\.0\.1|localhost):/.test(row.address));
  if (rows.length === 0) return;
  const previous = observer.getSetting('outposts_enabled');
  await run(binary, ['settings', 'set', 'outposts_enabled', 'true']);
  for (const row of rows) {
    const removed = receive(rows => rows.every(item => item.id !== row.id));
    observer.send({ cmd: 'remove_outpost', id: row.id });
    await removed;
  }
  await run(binary, ['settings', 'set', 'outposts_enabled', previous]);
}
const webkitBaseline = await captureWebKitPids();
let callerEnv;
let caller;
let remoteAgent;
let remoteAgentPane;
let remote;
let remotePane;
let split;
let splitPane;
const cli = async (...args) => (await run(binary, args, callerEnv)).stdout;
const tile = pane => `[data-pane-id="${pane}"]`;
async function cpuTimes(pids) {
  const { stdout } = await run('ps', ['-p', pids.join(','), '-o', 'pid=,time=']);
  return Object.fromEntries(stdout.trim().split('\n').map(line => {
    const [pid, time] = line.trim().split(/\s+/);
    return [pid, time.split(':').reduce((seconds, part) => seconds * 60 + Number(part), 0)];
  }));
}
async function measureCpu(name, action = null) {
  const processes = await snapshot(client.readManifest().pid, readLiveDaemonPid(instance), webkitBaseline);
  const pids = [...appPids(processes), readLiveDaemonPid(instance)];
  const before = await cpuTimes(pids);
  const started = Date.now();
  for (let sample = 0; sample < 10; sample++) {
    if (action) await action();
    await delay(1000);
  }
  const after = await cpuTimes(pids);
  const durationMs = Date.now() - started;
  const cpuPct = Object.fromEntries(pids.map(pid => [pid, ((after[pid] ?? before[pid]) - before[pid]) * 100_000 / durationMs]));
  runner.writeJson(`${name}-cpu.json`, { durationMs, before, after, cpuPct, processes });
  runner.log(`${name} CPU`, { durationMs, totalCpuPct: Object.values(cpuPct).reduce((sum, value) => sum + value, 0), cpuPct });
}
async function port() {
  const server = net.createServer();
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const value = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return value;
}
function start(command, args, childEnv, stdio) {
  const child = spawn(command, args, { env: childEnv, stdio });
  children.push(child);
  return child;
}
async function press(selector, key = 'Enter') {
  await client.request('dom_focus', { selector });
  await driver.pressKey(key);
}
async function add(name, address) {
  await client.request('dom_focus', { selector: '#outpost-name' });
  await driver.typeText(name);
  await driver.pressKey('Tab');
  await wait('#outpost-address', { focused: true });
  await driver.typeText(address);
  await driver.pressKey('Tab');
  await wait('form[aria-label="Add an outpost"] button', { focused: true });
  await driver.pressEnter();
}
runner.registerCleanup('remove_scratch', () => fs.rmSync(root, { recursive: true, force: true }));
runner.registerCleanup('stop_children', async () => {
  for (const child of [...children].reverse()) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    const exited = once(child, 'exit');
    child.kill('SIGCONT');
    child.kill('SIGTERM');
    await exited;
  }
});
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());
try {
  const agent = start('ssh-agent', ['-D', '-a', socket], env, ['ignore', 'pipe', 'pipe']);
  await once(agent.stdout, 'data');
  const key = path.join(root, 'key');
  await run('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', key]);
  await run('ssh-add', [key]);
  const outpostData = path.join(root, 'outpost');
  fs.mkdirSync(outpostData, { mode: 0o700 });
  runner.registerCleanup('save_outpost_log', () => {
    const source = path.join(outpostData, 'daemon.log');
    if (fs.existsSync(source)) fs.copyFileSync(source, path.join(runner.runDir, 'outpost-daemon.log'));
  });
  const outpostEnv = instanceCliEnv(instance, {
    ATTN_DATA_DIR: outpostData,
    ATTN_HARNESS_DATA_DIR: outpostData,
    ATTN_HARNESS_NOTEBOOK_ROOT: path.join(outpostData, 'notebook'), ATTN_WS_PORT: String(await port()), ATTN_DAEMON_READY_FD: '3',
    ATTN_AUTHORIZED_KEYS_PATH: `${key}.pub`, ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off',
    ATTN_HARNESS_SKILL_SYNC: 'off', ATTN_PTY_BACKEND: 'embedded', ATTN_CACHED_SHELL_ENV: '[]',
    ATTN_TOOL_HOME: path.join(root, 'outpost-tools'),
  });
  const log = fs.openSync(path.join(runner.runDir, 'outpost.log'), 'w');
  const outpost = start(binary, ['daemon'], outpostEnv, ['ignore', log, log, 'pipe']);
  fs.closeSync(log);
  const [ready] = await once(outpost.stdio[3], 'data');
  runner.assert(ready.toString().includes('ready'), 'outpost reported readiness', ready.toString());
  const bridgePort = await port();
  const address = `127.0.0.1:${bridgePort}`;
  await run(binary, ['settings', 'set', 'outpost_listen', address], outpostEnv);
  await run(binary, ['daemon', 'stop']);
  await launchFreshAppAndConnect(client, observer);
  await removeScenarioOutposts();
  runner.registerCleanup('remove_scenario_outposts', removeScenarioOutposts);
  const window = await client.request('get_window_bounds');
  await client.request('set_window_bounds', { logicalBounds: { ...window.logicalBounds, x: 0, y: 0, width: 1200, height: 800 } });
  await driver.activateApp();
  const previous = observer.getSetting('outposts_enabled');
  runner.registerCleanup('restore_switch', () => client.request('set_setting', { key: 'outposts_enabled', value: previous }));
  await client.request('set_setting', { key: 'outposts_enabled', value: 'false' });
  runner.registerCleanup('close_created_terminals', async () => {
    outpost.kill('SIGCONT');
    for (const sessionId of created.toReversed()) {
      if (observer.getSession(sessionId)) await client.request('close_session', { sessionId });
    }
  });
  await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
  await runner.step('keyboard_opt_in_and_add', async () => {
    await wait('[data-testid="settings-nav-outposts"]', { absent: true });
    await press('[data-testid="settings-nav-experimental"]');
    await press(toggle, 'space');
    await wait('[data-testid="settings-nav-outposts"]');
    await shot('00-experimental');
    await press('[data-testid="settings-nav-outposts"]');
    const connected = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'connected'));
    await add('Studio', address);
    const rows = await connected;
    const row = rows.find(row => row.name === 'Studio');
    runner.assert(Boolean(row.host_key_fingerprint && row.build && row.daemon_id), 'first contact pins identity and build', row);
    await wait(`${studio} .settings-pill`, { textIncludes: 'connected' });
    await wait(studio, { textIncludes: row.host_key_fingerprint });
    await shot('01-connected');
  });
  await runner.step('remote_cli_shell_and_native_split', async () => {
    await driver.pressKey('Escape');
    writeMockAgentFixture(runner.sessionDir, { minimumWorkingMs: 0, turns: [{ includes: 'Local attention', actions: [{ type: 'reply', text: 'LOCAL_READY', state: 'waiting_input' }] }] });
    caller = await createSessionAndWaitForInitialPane({ client, observer, cwd: runner.sessionDir, label: 'Outpost operator', agent: 'claude' });
    created.push(caller);
    callerEnv = instanceCliEnv(instance, { SSH_AUTH_SOCK: socket, ATTN_TERMINAL_ID: observer.terminalOf(caller) });
    const startDirectory = path.join(fs.realpathSync(root), 'remote-project');
    fs.mkdirSync(startDirectory);
    remote = JSON.parse(await cli('terminal', 'open', '--host', 'Studio', '--cwd', startDirectory, '--json'));
    created.push(remote.session_id);
    remotePane = { sessionId: remote.session_id, paneId: remote.pane_id };
    await waitForPaneAttached(client, remote.session_id, remote.pane_id);
    await waitForPaneShellReady(client, remote.session_id, remote.pane_id);
    await wait(`${tile(remote.pane_id)} .desktop-pane-host`, { textIncludes: 'Studio' });
    const firstOutput = await cli('terminal', 'run', remote.terminal_id, 'printf "ATTN_HOST=%s\\n" "$ATTN_HOST"; pwd; cd /', '--wait');
    runner.assert(firstOutput.includes('ATTN_HOST=Studio') && firstOutput.replace(/\s+/g, '').includes(startDirectory), 'the CLI opens the shell on its outpost in the requested start directory', firstOutput);
    const spawned = observer.waitForMessage(message => message.event === 'session_registered' && message.session?.host && message.session.id !== remote.session_id && message.session, 'native Cmd+D remote split');
    await client.request('click_pane', remotePane);
    await waitForPaneInputFocus(client, remote.session_id, remote.pane_id);
    await pressShortcutKeys(client, driver, 'terminal.splitVertical');
    split = await spawned;
    created.push(split.id);
    const pane = await waitForFirstDesktopPane(client, split.id, 'remote split tile');
    splitPane = { sessionId: split.id, paneId: pane.paneId };
    await waitForPaneAttached(client, split.id, pane.paneId);
    await waitForPaneShellReady(client, split.id, pane.paneId);
    const splitOutput = await cli('terminal', 'run', observer.terminalOf(split.id), 'printf "ATTN_HOST=%s\\n" "$ATTN_HOST"; pwd', '--wait');
    runner.assert(split.host === observer.getSession(remote.session_id).host && splitOutput.includes('ATTN_HOST=Studio') && splitOutput.replace(/\s+/g, '').includes(startDirectory), 'native Cmd+D stays on the same outpost and uses the original start directory after cd /', { split, startDirectory, firstOutput, splitOutput });
    runner.writeJson('remote-split.json', { remote, split, startDirectory, firstOutput, splitOutput });
    await waitForPaneText(client, split.id, pane.paneId, value => value.includes('ATTN_HOST=Studio') && value.replace(/\s+/g, '').includes('remote-project'), 'split host and cwd rendered');
    await shot('02-remote-split');
    await measureCpu('online-idle');
  });
  await runner.step('remote_agent_and_queue', async () => {
    for (const [key, value] of [['queue_mode_enabled', 'true'], ['auto_settle_enabled', 'false']]) {
      const previousValue = observer.getSetting(key);
      runner.registerCleanup(`restore_${key}`, () => client.request('set_setting', { key, value: previousValue }));
      await client.request('set_setting', { key, value });
    }
    if (!observer.outposts.some(row => row.name === 'Studio' && row.notebook?.state === 'in_sync')) {
      await receive(rows => rows.some(row => row.name === 'Studio' && row.notebook?.state === 'in_sync'));
    }
    await run(binary, ['settings', 'set', 'claude_executable', MOCK_AGENT_EXECUTABLE], outpostEnv);
    const cwd = path.join(fs.realpathSync(root), 'remote-agent');
    writeMockAgentFixture(cwd, { minimumWorkingMs: 0, turns: [{ includes: 'Remote attention', actions: [{ type: 'reply', text: 'REMOTE_READY', state: 'waiting_input' }] }] });
    const id = crypto.randomUUID();
    const spawned = observer.waitForMessage(message => message.event === 'spawn_result' && message.id === id && message, 'remote Claude spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'claude', host: 'Studio', cwd, profile_id: observer.profileId, placement: {}, label: 'Outpost Claude', initial_prompt: 'Remote attention', cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success, 'spawn_session runs Claude on Studio', result);
    created.push(id);
    remoteAgent = id;
    remoteAgentPane = { sessionId: id, paneId: result.pane_id };
    await client.request('select_session', { sessionId: id });
    await waitForPaneAttached(client, id, result.pane_id);
    await waitForPaneText(client, id, result.pane_id, value => value.includes('REMOTE_READY'), 'remote Claude reply');
    await client.request('select_session', { sessionId: caller });
    const callerPane = await waitForFirstDesktopPane(client, caller, 'local operator tile');
    await waitForPaneInputFocus(client, caller, callerPane.paneId);
    await driver.typeText('Local attention');
    await driver.pressEnter();
    await waitForPaneText(client, caller, callerPane.paneId, value => value.includes('LOCAL_READY'), 'local operator reply');
    await wait('[data-testid="queue-waiting-card"][data-waiting="2"]');
    await shot('02-agent-online');
  });
  await runner.step('remote_caller_delegation_and_terminal_defaults', async () => {
    const remoteCallerEnv = instanceCliEnv(instance, { ...outpostEnv, ATTN_TERMINAL_ID: observer.terminalOf(remoteAgent) });
    const remoteCli = async (...args) => (await run(binary, args, remoteCallerEnv)).stdout;
    const host = observer.getSession(remoteAgent).host;
    const opened = JSON.parse(await remoteCli('terminal', 'open', '--json'));
    created.push(opened.session_id);
    await client.request('click_pane', { sessionId: opened.session_id, paneId: opened.pane_id });
    await waitForPaneAttached(client, opened.session_id, opened.pane_id);
    await wait(`${tile(opened.pane_id)} .desktop-pane-host`, { textIncludes: 'Studio' });
    runner.assert(observer.getSession(opened.session_id)?.host === host, 'an outpost agent opens a shell on its own outpost', opened);
    for (const target of ['outpost', 'home']) {
      const cwd = path.join(fs.realpathSync(root), `delegated-${target}`);
      writeMockAgentFixture(cwd, { minimumWorkingMs: 0, turns: [{ includes: 'Your assignment', actions: [{ type: 'reply', text: `DELEGATE_${target.toUpperCase()}_READY`, state: 'waiting_input' }] }] });
      const args = ['delegate', '--brief', `Show the ${target} delegate`, '--cwd', cwd, '--model', 'default'];
      if (target === 'home') args.push('--host', 'home');
      const result = JSON.parse(await remoteCli(...args));
      created.push(result.session_id);
      await client.request('click_pane', { sessionId: result.session_id, paneId: result.pane_id });
      await waitForPaneAttached(client, result.session_id, result.pane_id);
      await waitForPaneText(client, result.session_id, result.pane_id, value => value.includes(`DELEGATE_${target.toUpperCase()}_READY`), `${target} delegate reply`);
      const session = observer.getSession(result.session_id);
      runner.assert((session.host || '') === (target === 'home' ? '' : host), `${target} delegation runs on the selected machine`, { result, session });
      if (target === 'outpost') await wait(`${tile(result.pane_id)} .desktop-pane-host`, { textIncludes: 'Studio' });
      await shot(`02-delegate-${target}`);
      await client.request('close_session', { sessionId: result.session_id });
      created.splice(created.indexOf(result.session_id), 1);
    }
    await remoteCli('terminal', 'close', opened.terminal_id);
    created.splice(created.indexOf(opened.session_id), 1);
    await wait('[data-testid="queue-waiting-card"][data-waiting="2"]');
  });
  await runner.step('frozen_outpost_and_recovery', async () => {
    await client.request('click_pane', splitPane);
    await waitForPaneAttached(client, split.id, splitPane.paneId);
    const fifo = path.join(root, 'screen-update');
    await run('mkfifo', [fifo]);
    await cli('terminal', 'run', observer.terminalOf(split.id), `printf 'C13_%s\\n' WAITING; cat '${fifo}'`);
    await waitForPaneText(client, split.id, splitPane.paneId, value => value.includes('C13_WAITING'), 'outpost shell waiting for external output');
    await client.request('click_pane', splitPane);
    await waitForPaneInputFocus(client, split.id, splitPane.paneId);
    const before = await client.request('read_pane_text', splitPane);
    const stalled = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'stalled'));
    const lost = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'lost'));
    outpost.kill('SIGSTOP');
    await stalled;
    await wait(`${tile(splitPane.paneId)}[data-offline]`, { absent: true });
    await shot('03-stalled-shell');
    await lost;
    await wait(`${tile(splitPane.paneId)}[data-offline="true"] .desktop-pane-offline-strip`, { textIncludes: 'Studio unreachable · typing is not sent' });
    const offline = await client.request('read_pane_text', splitPane);
    runner.assert(JSON.stringify(before.size) === JSON.stringify(offline.size) && before.text === offline.text, 'going offline preserves the screen and terminal size', { before, offline });
    await wait(`[data-testid="queue-turn-${remoteAgent}"][data-offline="true"]`);
    await wait('[data-testid="queue-waiting-card"][data-waiting="1"]');
    await client.request('select_session', { sessionId: remoteAgent });
    await wait(`${tile(remoteAgentPane.paneId)} .desktop-pane-offline-strip`, { textIncludes: 'Studio unreachable' });
    await shot('04-agent-offline');
    await pressShortcutKeys(client, driver, 'session.jumpToWaiting');
    const callerPane = await waitForFirstDesktopPane(client, caller, 'queued local operator');
    await waitForPaneInputFocus(client, caller, callerPane.paneId);
    runner.assert(observer.getSession(remoteAgent).turn_owed, 'offline agent keeps its turn while Cmd+J selects the local operator');
    await shot('04-agent-queue-skip');
    await client.request('select_session', { sessionId: split.id });
    await client.request('click_pane', splitPane);
    await waitForPaneInputFocus(client, split.id, splitPane.paneId);
    const beforeRefusal = await client.request('read_pane_text', splitPane);
    await measureCpu('offline-idle');
    const flashing = wait(`${tile(splitPane.paneId)} .desktop-pane-refusal`);
    await driver.typeText('C13_REFUSED_INPUT');
    await flashing;
    await shot('04-refused-input');
    await driver.pressEnter();
    await measureCpu('offline-refused-input', () => driver.pressKey('x'));
    await wait(`${tile(splitPane.paneId)} .desktop-pane-refusal`, { absent: true });
    const bounds = await client.request('dom_bounds', { selector: tile(splitPane.paneId) });
    runner.assert(bounds.animations.every(animation => animation.state !== 'running'), 'the refusal flash finishes with no running tile animation', bounds);
    const afterRefusal = await client.request('read_pane_text', splitPane);
    runner.assert(afterRefusal.text === beforeRefusal.text, 'refused native typing does not change the visible offline screen', { beforeRefusal, afterRefusal });
    await fs.promises.writeFile(fifo, 'C13_SCREEN_CURRENT\n');
    const recovered = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'connected' && !row.last_error));
    outpost.kill('SIGCONT');
    await recovered;
    await wait(`${tile(splitPane.paneId)} .desktop-pane-offline-strip`, { absent: true });
    await waitForPaneText(client, split.id, splitPane.paneId, value => value.includes('C13_SCREEN_CURRENT'), 'current outpost screen after reconnect');
    await cli('terminal', 'wait', observer.terminalOf(split.id), '--until', 'prompt');
    await driver.typeText("printf 'C13_%s\\n' RECOVERED");
    await driver.pressEnter();
    const current = await waitForPaneText(client, split.id, splitPane.paneId, value => value.includes('C13_RECOVERED'), 'native typing after reconnect');
    runner.assert(!current.text.includes('C13_REFUSED_INPUT'), 'refused typing is never replayed', current);
    runner.writeJson('recovered-screen.json', current);
    await wait(`[data-testid="queue-turn-${remoteAgent}"][data-offline]`, { absent: true });
    await wait('[data-testid="queue-waiting-card"][data-waiting="2"]');
    runner.writeJson('remote-agent.json', { session: observer.getSession(remoteAgent), pane: remoteAgentPane });
    await shot('05-recovered-shell');
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await press('[data-testid="settings-nav-outposts"]');
    await wait(`${studio} .settings-pill`, { textIncludes: 'connected' });
  });
  await runner.step('full_refusal_keyboard_repair_and_remove', async () => {
    const refusal = receive(rows => rows.some(row => row.name === 'Studio alias' && row.state === 'refused'));
    await add('Studio alias', `localhost:${bridgePort}`);
    const rows = await refusal;
    const refused = rows.find(row => row.name === 'Studio alias');
    await wait(`${alias} .settings-pill`, { textIncludes: 'refused' });
    runner.assert((await text(`${alias} .settings-outpost-error p`)) === refused.last_error, 'refusal detail is shown in full', refused);
    await client.request('dom_scroll_into_view', { selector: alias });
    await shot('05-refused');
    const removed = receive(rows => !rows.some(row => row.name === 'Studio alias'));
    await press(`${alias} button:last-child`);
    await removed;
    await wait(alias, { absent: true });
    const before = rows.find(row => row.name === 'Studio').state_since;
    const repaired = receive(rows => rows.some(row => row.name === 'Studio' && row.state === 'connected' && row.state_since !== before));
    await press(`${studio} button:first-child`);
    await repaired;
    const empty = receive(rows => rows.length === 0);
    await press(`${studio} button:last-child`);
    await wait(`${studio} .settings-outpost-actions button:nth-child(2)`, { textIncludes: 'Remove and close 3 terminals' });
    await shot('06-remove-confirmation');
    await press(`${studio} .settings-outpost-actions button:nth-child(2)`);
    await empty;
    created.splice(created.indexOf(remote.session_id), 1);
    created.splice(created.indexOf(split.id), 1);
    await wait(studio, { absent: true });
    await press('[data-testid="settings-nav-experimental"]');
    await press(toggle, 'space');
    await wait('[data-testid="settings-nav-outposts"]', { absent: true });
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  await shot('failure').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
