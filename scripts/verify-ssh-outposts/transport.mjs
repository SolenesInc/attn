import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawn } from 'node:child_process';
import { parseArgs } from 'node:util';
import { world, quote } from './common.mjs';

const { values: o } = parseArgs({ options: {
  repo: { type: 'string' }, macBin: { type: 'string' }, linuxBin: { type: 'string' },
  instance: { type: 'string', default: 'fc3c' }, ssh: { type: 'string', default: 'ai-sandbox' },
  listen: { type: 'string', default: '127.0.0.1:19437' }, dial: { type: 'string' }, stage: { type: 'string' }, pidfile: { type: 'string' },
  networkFlow: { type: 'string', default: '100.77.31.56:2222' }, scratch: { type: 'string' }, out: { type: 'string' },
  remoteNotebook: { type: 'string' }, notebook: { type: 'string' }, mode: { type: 'string' }, samples: { type: 'string', default: '1000' }, capMs: { type: 'string', default: String(20 * 60_000) },
} });
if (!['idle', 'bulk'].includes(o.mode)) throw new Error('--mode idle|bulk');
const W = await world(o);
const { receipt, remote, attn } = W;
receipt('driver started', { pid: process.pid });
let stopping = false;
process.once('SIGTERM', () => { stopping = true; });

const samples = [];
const sent = new Map();
const notebookEvents = [];
let output = '';
let echoTerminal = null;
let nettop = null;

const stats = list => {
  const v = [...list].sort((x, y) => x - y); const n = v.length;
  return n ? { samples: n, p50_ms: +v[Math.floor(n / 2)].toFixed(1), p99_ms: +v[Math.ceil(0.99 * n) - 1].toFixed(1), max_ms: +v[n - 1].toFixed(1) } : { samples: 0 };
};
async function socketStats() { return remote("ss -tinH state established '( sport = :22 )'"); }
async function context(label) { receipt('load', { label, mac: (await W.run('uptime', [])).stdout.trim(), sandbox: await remote('uptime') }); }

function watchEcho() {
  W.observer.waitForMessage(m => {
    if (m.event !== 'pty_output' || m.id !== echoTerminal) return false;
    output += Buffer.from(m.data, 'base64').toString('latin1');
    const now = performance.now();
    for (const match of output.matchAll(/<E(\d{6})\n>/g)) {
      const i = Number(match[1]);
      const at = sent.get(i);
      if (at !== undefined) { samples.push({ i, sent: at, ms: now - at }); sent.delete(i); }
    }
    const last = output.lastIndexOf('>');
    if (last >= 0) output = output.slice(last + 1);
    return false;
  }, 'echo output', 3_600_000).catch(() => {});
}
async function startShell() {
  const id = crypto.randomUUID();
  const result = W.observer.waitForMessage(m => m.event === 'spawn_result' && m.id === id && m, 'shell spawn', 120_000);
  W.observer.send({ cmd: 'spawn_session', id, host: W.outpost.id, agent: 'shell', cwd: o.scratch, profile_id: W.observer.profileId, placement: {}, label: 'LT echo', cols: 100, rows: 30 });
  const spawned = await result;
  if (!spawned.success) throw new Error(`shell spawn failed: ${spawned.error}`);
  echoTerminal = W.observer.terminalOf(id);
  const attached = W.observer.waitForMessage(m => m.event === 'attach_result' && m.id === echoTerminal && m, 'shell attach', 60_000);
  W.observer.send({ cmd: 'attach_session', id: echoTerminal });
  await attached;
  let screen = '';
  const ready = W.observer.waitForMessage(m => { if (m.event !== 'pty_output' || m.id !== echoTerminal) return false; screen += Buffer.from(m.data, 'base64').toString(); return screen.includes('echo_READY'); }, 'echo ready', 120_000);
  W.observer.send({ cmd: 'pty_input', id: echoTerminal, data: `python3 -c 'import os; print(os.getppid())' > ${o.scratch}/shell.pid; stty raw -echo; python3 -u -c 'import os,sys; print("echo_"+"READY",flush=True); [os.write(1,b"<"+line+b">") for line in sys.stdin.buffer]'\r` });
  await ready;
  watchEcho();
  receipt('echo shell ready', { session: id, terminal: echoTerminal });
  return id;
}
let seq = 0;
async function sampleUntil(stop) {
  let next = performance.now();
  while (!stop() && !stopping) {
    const i = seq++;
    sent.set(i, performance.now());
    W.controlSample(i);
    W.observer.send({ cmd: 'pty_input', id: echoTerminal, data: `E${String(i).padStart(6, '0')}\n` });
    next += 30;
    await new Promise(r => setTimeout(r, Math.max(0, next - performance.now())));
  }
  const drained = performance.now();
  while (sent.size && performance.now() - drained < 15_000) await new Promise(r => setTimeout(r, 50));
  if (stopping) throw new Error('measurement stopped by SIGTERM');
  return { unanswered: sent.size };
}
async function heartbeatGaps() {
  const closed = W.waitOutpost(r => r.id === W.outpost.id && r.reach !== 'reachable', 'bridge closed by outposts_enabled=false', 60_000);
  await attn('settings', 'set', 'outposts_enabled', 'false');
  await closed;
  const read = async () => ({
    home: fs.readFileSync(path.join(W.macData, 'daemon.log'), 'utf8').split('\n').filter(l => l.includes('bridge closed: largest heartbeat gap')),
    outpost: (await remote(`grep 'bridge closed: largest heartbeat gap' ${quote(W.R.data + '/daemon.log')} || true`)).split('\n').filter(Boolean) });
  let lines = await read();
  for (let tries = 0; tries < 20 && (!lines.home.length || !lines.outpost.length); tries++) { await new Promise(r => setTimeout(r, 500)); lines = await read(); }
  return lines;
}

try {
  await W.startOutpost();
  await W.startHome(o.notebook);
  await context('before');
  const nettopLog = fs.openSync(path.join(o.out, 'nettop.csv'), 'w');
  nettop = spawn('nettop', ['-m', 'tcp', '-L', '0', '-s', '1', '-n', '-x'], { stdio: ['ignore', nettopLog, 'ignore'] });
  fs.closeSync(nettopLog);
  const before = await socketStats();
  const t0 = performance.now();
  W.observer.waitForMessage(m => {
    if (m.event === 'outposts_updated') for (const r of m.outposts) if (r.id === W.outpost?.id) {
      const last = notebookEvents.at(-1);
      const nb = { state: r.notebook?.state, need_bytes: r.notebook?.need_bytes, conflicts: r.notebook?.conflicts, bridge: r.state };
      if (!last || JSON.stringify(last.nb) !== JSON.stringify(nb)) notebookEvents.push({ t_ms: performance.now() - t0, at: new Date().toISOString(), nb });
    }
    return false;
  }, 'notebook watch', 3_600_000).catch(() => {});
  await W.pair();
  const connectedAt = performance.now() - t0;
  const shell = await startShell();
  if (o.mode === 'idle') {
    const beforeDaemon = await remote(`cat ${quote(W.R.data + '/attn.pid')}`);
    const beforeShell = await remote(`cat ${quote(o.scratch + '/shell.pid')}`);
    const lost = W.observer.waitForMessage(m => m.event === 'outposts_updated' && m.outposts.some(r => r.id === W.outpost.id && r.state === 'lost'), 'master exit lost');
    await W.run('ssh', ['-o', `ControlPath=${W.controlPath}`, '-o', 'BatchMode=yes', '-O', 'exit', o.ssh]);
    await lost;
    await W.waitOutpost(r => r.id === W.outpost.id && r.state === 'connected' && r.state_since !== W.outpost.state_since, 'master reconnect');
    const afterDaemon = await remote(`cat ${quote(W.R.data + '/attn.pid')}`);
    const afterShell = await remote(`cat ${quote(o.scratch + '/shell.pid')}`);
    if (beforeDaemon !== afterDaemon || beforeShell !== afterShell) throw new Error('daemon or shell PID changed after master exit');
    const attached = W.observer.waitForMessage(m => m.event === 'attach_result' && m.id === echoTerminal && m.success, 'reattach recovered shell');
    W.observer.send({cmd:'attach_session',id:echoTerminal}); await attached;
    receipt('master exit recovery', { daemon_pid: afterDaemon, shell_pid: afterShell });
  }
  const controls = Object.fromEntries(Object.entries({ mux: W.controlPath, separate: 'none' }).map(([name, controlPath]) => {
    const child = spawn('ssh', ['-o', `ControlPath=${controlPath}`, '-o', 'ControlMaster=no', '-o', 'BatchMode=yes', '-T', o.ssh, "sh -c 'exec cat'"], { env: W.baseEnv, stdio: ['pipe', 'pipe', 'pipe'] });
    W.children.push(child);
    const sent = new Map(), samples = [];
    let buffer = '';
    child.stdout.on('data', data => {
      buffer += data.toString();
      let end;
      while ((end = buffer.indexOf('\n')) >= 0) {
        const line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
        const token = Number(line.slice(1, 7)), point = sent.get(token);
        if (point) { samples.push([point.epoch, performance.now() - point.mono]); sent.delete(token); }
      }
    });
    return [name, { child, sent, samples }];
  }));
  W.controlSample = i => {
    for (const control of Object.values(controls)) {
      control.sent.set(i, { epoch: Date.now()/1000, mono: performance.now() });
      control.child.stdin.write(`C${String(i).padStart(6, '0')}${'x'.repeat(56)}\n`);
    }
  };
  W.saveControl = () => fs.writeFileSync(path.join(o.out, 'control-echo.json'), JSON.stringify({ routes: Object.fromEntries(Object.entries(controls).map(([name, control]) => [name, { samples: control.samples, unanswered: control.sent.size }])) }, null, 2));
  if (o.remoteNotebook) {
    await W.waitOutpost(r => r.id === W.outpost.id && r.notebook?.state === 'in_sync', 'empty baseline synced before reverse write');
    receipt('reverse Notebook written after pairing', { source: o.remoteNotebook, output: await remote(`cp -a ${quote(o.remoteNotebook + '/.')} ${quote(W.R.notebook + '/')}`) });
  }
  const synced = () => W.observer.outposts.find(r => r.id === W.outpost.id)?.notebook?.state === 'in_sync';
  let window;
  if (o.mode === 'idle') {
    await W.waitOutpost(r => r.id === W.outpost.id && r.notebook?.state === 'in_sync', 'first sync of the tiny Notebook');
    const start = performance.now() - t0;
    const target = Number(o.samples);
    await sampleUntil(() => seq >= target);
    window = { from_ms: start, to_ms: performance.now() - t0 };
  } else {
    const start = performance.now() - t0;
    let syncedAt = null;
    await sampleUntil(() => {
      if (syncedAt === null && notebookEvents.some(e => e.nb.need_bytes > 0) && synced()) syncedAt = performance.now() - t0;
      return syncedAt !== null && performance.now() - t0 > syncedAt + 3000 || performance.now() - t0 > Number(o.capMs);
    });
    window = { from_ms: start, to_ms: syncedAt, synced_ms: syncedAt };
  }
  const after = await socketStats();
  const inWindow = samples.filter(s => s.sent - t0 >= window.from_ms && (window.to_ms == null || s.sent - t0 <= window.to_ms));
  const outsideWindow = samples.filter(s => !inWindow.includes(s));
  const outpostNotebook = await remote(`cd ${quote(W.R.notebook)} && find . -path ./.attn -prune -o -type f -print | wc -l && du -sb --exclude=.attn . | cut -f1`);
  const localNotebook = (await W.run('/bin/sh', ['-c', `cd ${quote(o.notebook)} && find . -path ./.attn -prune -o -type f -print | wc -l && du -sk . | cut -f1`])).stdout.trim();
  W.saveControl();
  W.observer.send({ cmd: 'unregister', id: shell });
  const gaps = await heartbeatGaps();
  nettop.kill('SIGTERM');
  const flows = {};
  for (const line of fs.readFileSync(path.join(o.out, 'nettop.csv'), 'utf8').split('\n')) {
    const c = line.split(',');
    if (c.length > 9 && (c[1].includes(o.networkFlow))) flows[c[1]] = { time: c[0], bytes_in: c[4], bytes_out: c[5], rx_dupe: c[6], rx_ooo: c[7], re_tx: c[8], rtt_avg: c[9] };
  }
  fs.writeFileSync(path.join(o.out, 'samples.json'), JSON.stringify({ t0_epoch_ms: Date.now() - (performance.now() - t0), samples: samples.map(s => ({ i: s.i, t_ms: +(s.sent - t0).toFixed(1), ms: +s.ms.toFixed(1) })), notebookEvents }, null, 1));
  receipt('result', { mode: o.mode, connected_ms: connectedAt, window, echo_in_window: stats(inWindow.map(s => s.ms)), echo_outside_window: stats(outsideWindow.map(s => s.ms)),
    unanswered: sent.size, notebook_events: notebookEvents, heartbeat_gaps: gaps, outpost_socket_before: before, outpost_socket_after: after, mac_bridge_flows_last: flows,
    notebook_files: { home_files_and_kb: localNotebook, outpost_files_and_bytes: outpostNotebook } });
  await context('after');
} catch (error) {
  receipt('result', { outcome: 'driver error', error: String(error?.stack ?? error), notebookEvents });
  process.exitCode = 1;
} finally {
  if (nettop && nettop.exitCode === null) nettop.kill('SIGTERM');
  await W.cleanup();
}
