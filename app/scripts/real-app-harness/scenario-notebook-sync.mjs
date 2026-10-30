#!/usr/bin/env node
import fs from 'node:fs';
import { outpostSsh } from './outpostSsh.mjs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import crypto from 'node:crypto';
import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Notebook sync verification requires a named instance');
const runner = createScenarioRunner(options, { scenarioId: 'NotebookSync', tier: 'local', prefix: 'notebook-sync', allowRealAgents: false });
const root = fs.mkdtempSync(path.join(os.platform() === 'darwin' ? '/private/tmp' : os.tmpdir(), 'attn-nb-'));
const homeRoot = path.join(root, 'home-notebook');
const outpostRoot = path.join(root, 'outpost-data', 'notebook');
fs.mkdirSync(path.dirname(outpostRoot), { mode: 0o700 });
fs.mkdirSync(homeRoot);
fs.mkdirSync(outpostRoot);
fs.writeFileSync(path.join(homeRoot, 'conflict.md'), 'baseline');
fs.writeFileSync(path.join(outpostRoot, 'old.md'), 'saved before first sync');
const payload = crypto.randomBytes(16 * 1024 * 1024);
for (let file = 0; file < 16; file++) fs.writeFileSync(path.join(homeRoot, `payload-${file}.bin`), payload);
const ssh = await outpostSsh(root);
const env = instanceCliEnv(instance, ssh.env);
const exec = promisify(execFile);
const run = (command, args, childEnv = env) => exec(command, args, { env: childEnv });
const binary = appDaemonInTree(options.appPath);
const client = new UiAutomationClient({ ...options, launchEnv: ssh.env });
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const children = [];
const rows = [];
const selector = '.settings-outpost[aria-label="Studio"]';
const wait = (extra) => client.request('dom_wait', { selector, timeoutMs: observer.connectTimeoutMs, ...extra });
const receive = (predicate) => observer.waitForMessage(message => message.event === 'outposts_updated' && message.outposts.find(row => row.name === 'Studio' && predicate(row.notebook)), 'Notebook state', observer.connectTimeoutMs);
const shot = async name => {
  await client.request('dom_scroll_into_view', { selector: `${selector} .settings-outpost-notebook` });
  return driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
};
function start(command, args, childEnv, stdio) {
  const child = spawn(command, args, { env: childEnv, stdio });
  children.push(child);
  return child;
}
async function port() {
  const server = net.createServer();
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const value = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return value;
}
runner.registerCleanup('remove_scratch', () => fs.rmSync(root, { recursive: true, force: true }));
runner.registerCleanup('stop_children', async () => {
  for (const child of children.toReversed()) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    const exited = once(child, 'exit');
    child.kill('SIGCONT'); child.kill('SIGTERM'); await exited;
  }
});
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('close_ssh', () => ssh.close());
try {

  const data = path.join(root, 'outpost-data');
  const remoteEnv = instanceCliEnv(instance, {
    ATTN_DATA_DIR: data, ATTN_HARNESS_DATA_DIR: data, ATTN_HARNESS_NOTEBOOK_ROOT: outpostRoot,
    ATTN_WS_PORT: String(await port()), ATTN_DAEMON_READY_FD: '3',
    ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off', ATTN_HARNESS_SKILL_SYNC: 'off',
    ATTN_PTY_BACKEND: 'embedded', ATTN_CACHED_SHELL_ENV: '[]', ATTN_CODEX_SHARED_PROFILE: '', CODEX_THREAD_ID: '',
  });
  const log = fs.openSync(path.join(runner.runDir, 'outpost.log'), 'w');
  const outpost = start(binary, ['daemon'], remoteEnv, ['ignore', log, log, 'pipe']); fs.closeSync(log);
  const [ready] = await once(outpost.stdio[3], 'data');
  runner.assert(ready.toString().includes('ready'), 'outpost ready');
  const destination = 'scenario-outpost';
  await ssh.route(destination, remoteEnv, binary);
  await run(binary, ['daemon', 'stop']);
  await launchFreshAppAndConnect(client, observer);
  const previousRoot = observer.getSetting('notebook.root');
  await run(binary, ['settings', 'set', 'notebook.root', homeRoot]);
  runner.registerCleanup('restore_home_root', async () => {
    await client.request('set_setting', { key: 'outposts_enabled', value: 'true' });
    const owned = observer.outposts.find(row => row.destination === destination);
    if (owned) {
      const removed = observer.waitForMessage(message => message.event === 'outposts_updated' && !message.outposts.some(row => row.id === owned.id), 'removed scenario outpost');
      observer.send({ cmd: 'remove_outpost', id: owned.id });
      await removed;
    }
    await client.request('set_setting', { key: 'outposts_enabled', value: 'false' });
    await run(binary, ['settings', 'set', 'notebook.root', previousRoot]);
  });
  const bounds = await client.request('get_window_bounds');
  await client.request('set_window_bounds', { logicalBounds: { ...bounds.logicalBounds, x: 0, y: 0, width: 1200, height: 800 } });
  observer.ws.on('message', raw => {
    try { const message = JSON.parse(raw.toString()); if (message.event === 'outposts_updated') rows.push(message); } catch {}
  });
  await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
  await client.request('set_setting', { key: 'outposts_enabled', value: 'true' });
  await client.request('dom_focus', { selector: '[data-testid="settings-nav-outposts"]' });
  await driver.pressEnter();
  await runner.step('first_sync_and_saved_previous_folder', async () => {
    const first = receive(notebook => notebook?.state === 'first_sync' && notebook.need_items > 0 && notebook.previous_root).then(row => { outpost.kill('SIGSTOP'); return row; });
    observer.send({ cmd: 'add_outpost', name: 'Studio', destination });
    const row = await first;
    await wait({ textIncludes: 'Notebook: first sync' });
    runner.assert(row.notebook.need_bytes > 0, 'first sync names what is left', row);
    await wait({ textIncludes: 'Previous folder saved at' });
    await shot('01-first-sync');
    const synced = receive(notebook => notebook?.state === 'in_sync');
    outpost.kill('SIGCONT');
    await synced;
    await wait({ textIncludes: 'Notebook: in sync' });
    runner.assert(fs.readFileSync(path.join(outpostRoot, 'conflict.md'), 'utf8') === 'baseline', 'first sync reached outpost disk');
    await shot('02-in-sync');
  });
  await runner.step('conflicting_offline_edits_keep_both_versions', async () => {
    await client.request('set_setting', { key: 'outposts_enabled', value: 'false' });

    fs.writeFileSync(path.join(homeRoot, 'conflict.md'), 'home edit');
    fs.writeFileSync(path.join(outpostRoot, 'conflict.md'), 'outpost edit');
    const conflicted = receive(notebook => notebook?.conflicts === 1);

    await client.request('set_setting', { key: 'outposts_enabled', value: 'true' });
    await client.request('dom_focus', { selector: '[data-testid="settings-nav-outposts"]' }); await driver.pressEnter();
    await conflicted;
    await wait({ textIncludes: '1 conflict copy' });
    const copies = fs.readdirSync(homeRoot).filter(name => name.includes('.sync-conflict-'));
    const content = [fs.readFileSync(path.join(homeRoot, 'conflict.md'), 'utf8'), fs.readFileSync(path.join(homeRoot, copies[0]), 'utf8')];
    runner.assert(content.includes('home edit') && content.includes('outpost edit'), 'both edits survived', content);
    await shot('03-conflict');
  });
  await runner.step('removed_conflict_and_missing_marker', async () => {
    if (!observer.outposts.some(row => row.name === 'Studio' && row.notebook?.state === 'in_sync')) await receive(notebook => notebook?.state === 'in_sync');
    const cleared = receive(notebook => notebook?.conflicts === 0);
    const copy = fs.readdirSync(homeRoot).find(name => name.includes('.sync-conflict-'));
    fs.unlinkSync(path.join(homeRoot, copy));
    await cleared;
    await wait({ textIncludes: 'Notebook: in sync' });

    await client.request('set_setting', { key: 'outposts_enabled', value: 'false' });
    fs.unlinkSync(path.join(outpostRoot, '.attn', 'notebook-sync'));
    const failed = receive(notebook => notebook?.state === 'error' && notebook.error?.includes('folder marker missing'));

    await client.request('set_setting', { key: 'outposts_enabled', value: 'true' });
    const row = await failed;
    runner.assert(row.notebook.error.includes('folder marker missing'), 'sync error names the cause', row);
    await wait({ textIncludes: 'folder marker missing' });
    await shot('04-error');
  });
  runner.writeJson('daemon-notebook-events.json', rows);
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  runner.writeJson('daemon-notebook-events.json', rows);
  await shot('failure').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
