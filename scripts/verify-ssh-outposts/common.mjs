import fs from 'node:fs';
import path from 'node:path';
import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';

const exec = promisify(execFile);
export const quote = value => `'${String(value).replaceAll("'", "'\\''")}'`;

export async function world(o) {
  if (!/^[a-z0-9][a-z0-9-]*$/.test(o.instance) || o.instance === 'default') throw new Error('a named instance is required');
  fs.mkdirSync(o.out, { recursive: true });
  const baseEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('ATTN_') && key !== 'CODEX_THREAD_ID'));
  const env = { ...baseEnv, ATTN_INSTANCE: o.instance, ATTN_HARNESS_INSTANCE: o.instance, ATTN_HARNESS_BIN: o.macBin,
    ATTN_HEADLESS_TASKS: 'off', ATTN_GITHUB_POLLING: 'off', ATTN_HARNESS_SKILL_SYNC: 'off' };
  for (const key of Object.keys(process.env)) if (key.startsWith('ATTN_')) delete process.env[key];
  Object.assign(process.env, env);
  const { DaemonObserver } = await import('../../app/scripts/real-app-harness/daemonObserver.mjs');
  const started = performance.now();
  const receipts = [];
  const receipt = (what, data = {}) => {
    const record = { seconds: (performance.now() - started) / 1000, at: new Date().toISOString(), what, ...data };
    receipts.push(record); console.log(JSON.stringify(record));
    fs.writeFileSync(path.join(o.out, 'receipts.json'), JSON.stringify(receipts, null, 2));
    return record;
  };
  const run = (command, args, options = {}) => exec(command, args, { env, maxBuffer: 64 << 20, ...options });
  const attn = (...args) => run(o.macBin, args);
  const remote = async script => {
    const result = exec('ssh', ['-o', 'BatchMode=yes', o.ssh, 'bash', '-s'], { env: baseEnv, maxBuffer: 64 << 20 });
    result.child.stdin.end(`set -euo pipefail\n${script}\n`);
    return (await result).stdout.trim();
  };
  const W = { o, env, baseEnv, receipt, run, attn, remote, children: [], R: {}, home: null, observer: null, remoteReady: false };
  W.startOutpost = async () => {
    const remoteHome = await remote('printf "%s" "$HOME"');
    W.R.binary = `${remoteHome}/.local/share/attn-outpost-instances/${o.instance}/attn`;
    W.rcli = (...args) => remote(`ATTN_INSTANCE=${quote(o.instance)} ${quote(W.R.binary)} ${args.map(quote).join(' ')}`);
    const record = JSON.parse(await W.rcli('instance', 'resolve', '--json'));
    W.R.data = record.dataDir;
    W.R.notebook = `${remoteHome}/attn-notebook-${o.instance}`;
    W.R.root = o.scratch;
    await remote(`test ! -e ${quote(W.R.data)}; test ! -e ${quote(W.R.notebook)}; mkdir -p ${quote(W.R.root)}`);
    W.remoteReady = true;
    receipt('outpost binary placed by hand', { ...W.R, build: JSON.parse(await W.rcli('--build-info-json')) });
  };
  W.startHome = async notebookRoot => {
    const resolved = JSON.parse((await attn('instance', 'resolve', '--json')).stdout);
    W.macData = resolved.dataDir;
    if (fs.existsSync(W.macData)) throw new Error(`refusing existing home data: ${W.macData}`);
    const log = fs.openSync(path.join(o.out, 'home.stderr'), 'a');
    W.home = spawn(o.macBin, ['daemon'], { env: { ...env, ATTN_DAEMON_READY_FD: '3' }, stdio: ['ignore', log, log, 'pipe'] });
    fs.closeSync(log);
    const [ready] = await once(W.home.stdio[3], 'data');
    if (!ready.toString().includes('ready')) throw new Error('home did not signal readiness');
    W.observer = new DaemonObserver({ connectTimeoutMs: 120_000 });
    await W.observer.connect();
    fs.mkdirSync(notebookRoot, { recursive: true });
    await attn('settings', 'set', 'notebook.root', notebookRoot);
    await attn('settings', 'set', 'outposts_enabled', 'true');
    receipt('home started', { pid: W.home.pid, data: W.macData });
  };
  W.waitOutpost = async (predicate, description, timeout = 300_000) => W.observer.outposts.find(predicate) || W.observer.waitForMessage(message => message.event === 'outposts_updated' && message.outposts.find(predicate), description, timeout);
  W.pair = async () => {
    W.observer.send({ cmd: 'add_outpost', name: 'Sandbox', destination: o.ssh });
    W.outpost = await W.waitOutpost(row => row.name === 'Sandbox' && row.state === 'connected', 'Add by destination connected');
    const uid = process.getuid();
    W.controlPath = `/tmp/attn-${uid}/${o.instance}-${W.outpost.id.replaceAll('-', '').slice(0, 12)}.sock`;
    receipt('paired', { row: W.outpost, controlPath: W.controlPath });
    return W.outpost;
  };
  W.cleanup = async () => {
    await W.observer?.close();
    for (const child of W.children.toReversed()) if (child.exitCode === null && child.signalCode === null) { child.kill('SIGTERM'); await once(child, 'exit'); }
    if (W.home && W.home.exitCode === null && W.home.signalCode === null) { W.home.kill('SIGTERM'); await once(W.home, 'exit'); }
    if (W.macData && fs.existsSync(path.join(W.macData, 'daemon.log'))) fs.copyFileSync(path.join(W.macData, 'daemon.log'), path.join(o.out, 'home-daemon.log'));
    if (W.remoteReady) {
      fs.writeFileSync(path.join(o.out, 'outpost-daemon.log'), await remote(`cat ${quote(W.R.data + '/daemon.log')} || true`));
      await W.rcli('enrollment', 'leave');
      await W.rcli('instance', 'clean', o.instance);
      await remote(`rm -f ${quote(W.R.binary)}; rmdir ${quote(path.dirname(W.R.binary))}; rm -rf ${quote(W.R.root)} ${quote(W.R.notebook)}`);
      receipt('outpost removed', { data: W.R.data, binary: W.R.binary });
    }
    if (W.home) receipt('home cleaned', { output: (await attn('instance', 'clean', o.instance)).stdout });
  };
  return W;
}
