#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import crypto from 'node:crypto';
import {
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  queueDaemonSettingRestore,
  writeDaemonSettings,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, dataDirForInstance, defaultWSURLForInstance } from './harnessInstance.mjs';
import { captureWebKitPids, closeEmptiedDesktop, collectDescendantPids, createSlottedDesktop, isRelevantWebKitProcess, readLiveDaemonPid, readProcessTable, showNativeDesktop, stopDaemonKeepingTerminals } from './perfMeasure.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { createWindowDriver } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const WORKLOADS = ['hidden', 'drawn', 'unmounted'];
const STREAM_COMMAND = "sh -c 'while :; do seq 1 400; sleep 0.02; done'";
const WARMUP_MS = 2_000;
const PROCESS_CLASSES = ['app', 'webkit_webcontent', 'webkit_networking', 'webkit_gpu', 'daemon', 'pty_host'];

function parseArgs(argv) {
  const args = argv.filter((arg) => arg !== '--');
  const extras = { workload: 'drawn', streams: 3, seconds: 30, runs: 5, keys: 30, profileDir: null, pprofPort: null, sharedHost: 'true' };
  const passthrough = [];
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    const value = () => {
      const next = args[index + 1];
      if (next === undefined) throw new Error(`${arg} needs a value`);
      index += 1;
      return next;
    };
    if (arg === '--workload') extras.workload = value();
    else if (arg === '--streams') extras.streams = Number(value());
    else if (arg === '--seconds') extras.seconds = Number(value());
    else if (arg === '--runs') extras.runs = Number(value());
    else if (arg === '--keys') extras.keys = Number(value());
    else if (arg === '--profile-dir') extras.profileDir = path.resolve(value());
    else if (arg === '--pprof-port') extras.pprofPort = Number(value());
    else if (arg === '--shared-host') extras.sharedHost = value();
    else passthrough.push(arg);
  }
  if (!['true', 'false'].includes(extras.sharedHost)) throw new Error(`--shared-host must be true or false, got ${extras.sharedHost}`);
  if (!WORKLOADS.includes(extras.workload)) throw new Error(`--workload must be one of ${WORKLOADS.join(', ')}, got ${extras.workload}`);
  for (const key of ['streams', 'seconds', 'runs']) {
    if (!Number.isInteger(extras[key]) || extras[key] < 1) throw new Error(`--${key} must be a positive integer, got ${extras[key]}`);
  }
  if (!Number.isInteger(extras.keys) || extras.keys < 0) throw new Error(`--keys must be a non-negative integer, got ${extras.keys}`);
  if (extras.pprofPort !== null && !extras.profileDir) throw new Error('--pprof-port needs --profile-dir');
  if (extras.profileDir && extras.sharedHost === 'false') throw new Error('--profile-dir samples the shared PTY host, so it needs --shared-host true');
  return { options: parseCommonArgs(passthrough), extras, help: args.includes('--help') || args.includes('-h') };
}

function printHelp() {
  printCommonHelp('scripts/real-app-harness/scenario-perf-pty-stream.mjs');
  console.log(`
Measures what streaming terminal output costs, with the shared terminal host on unless --shared-host false.

  --workload hidden|drawn|unmounted
      hidden: the streaming shells are splits on the previous desktop; a quiet shell is drawn.
      drawn: the streaming shells and the quiet shell are splits on the current desktop.
      unmounted: the streaming shells are on a desktop that is neither current nor previous.
  --streams N     streaming shells (default 3)
  --seconds S     measured window per run (default 30)
  --runs N        runs, each with fresh sessions (default 5)
  --keys N        keystrokes for the key-to-paint probe after each window (default 30)
  --profile-dir D profile the daemon, the PTY host and the app during each window into D.
                  Profiling costs CPU, so profiled runs are not baselines.
  --pprof-port P  restart the instance's daemon with ATTN_PPROF=P so the daemon profile is taken too
  --shared-host true|false
                  pty_shared_host_enabled for the run (default true); it also turns on streaming only to drawn panes.
                  With false, pty_host CPU is the sum of the Go PTY workers.

Prints one {"type":"run"} JSON line per run, then one {"type":"summary"} line with the
median, min and max of each number.`);
}

function cpuSeconds(text) {
  const parts = text.trim().replace('-', ':').split(':').map(Number);
  return parts.reduce((total, part) => total * 60 + part, 0);
}

async function readCpuSeconds(pids) {
  if (pids.length === 0) return new Map();
  const { stdout } = await execFileAsync('ps', ['-o', 'pid=,time=', '-p', pids.join(',')]);
  const seconds = new Map();
  for (const line of stdout.split('\n')) {
    const match = line.trim().match(/^(\d+)\s+(\S+)$/);
    if (match) seconds.set(Number(match[1]), cpuSeconds(match[2]));
  }
  return seconds;
}

function liveSharedHostPid(dataDir) {
  const root = path.join(dataDir, 'pty-hosts');
  const candidates = [];
  for (const daemonInstance of fs.existsSync(root) ? fs.readdirSync(root) : []) {
    const hostsDir = path.join(root, daemonInstance, 'hosts');
    for (const file of fs.existsSync(hostsDir) ? fs.readdirSync(hostsDir) : []) {
      try {
        const pid = JSON.parse(fs.readFileSync(path.join(hostsDir, file), 'utf8')).host_pid;
        process.kill(pid, 0);
        candidates.push(pid);
      } catch {}
    }
  }
  if (candidates.length !== 1) throw new Error(`Expected one live shared PTY host under ${root}, found ${JSON.stringify(candidates)}`);
  return candidates[0];
}

function liveGoWorkerPids(dataDir) {
  const pids = [];
  for (const daemonInstance of fs.existsSync(path.join(dataDir, 'workers')) ? fs.readdirSync(path.join(dataDir, 'workers')) : []) {
    const registry = path.join(dataDir, 'workers', daemonInstance, 'registry');
    for (const file of fs.existsSync(registry) ? fs.readdirSync(registry).filter((name) => name.endsWith('.json')) : []) {
      try {
        const pid = JSON.parse(fs.readFileSync(path.join(registry, file), 'utf8')).worker_pid;
        process.kill(pid, 0);
        pids.push(pid);
      } catch {}
    }
  }
  return pids;
}

function classifyProcess(proc, { appPid, daemonPid, ptyPids }) {
  if (proc.pid === appPid) return 'app';
  if (proc.pid === daemonPid) return 'daemon';
  if (ptyPids.has(proc.pid)) return 'pty_host';
  if (proc.command.includes('com.apple.WebKit.WebContent')) return 'webkit_webcontent';
  if (proc.command.includes('com.apple.WebKit.Networking')) return 'webkit_networking';
  if (proc.command.includes('com.apple.WebKit.GPU')) return 'webkit_gpu';
  return null;
}

async function webKitPidsLaunchedSince(webkitBaseline) {
  const table = await readProcessTable();
  return new Set(table.filter((proc) => isRelevantWebKitProcess(proc) && !webkitBaseline.has(proc.pid)).map((proc) => proc.pid));
}

async function appProcessTree({ appPid, daemonPid, ptyPids, appWebKitPids }) {
  const table = await readProcessTable();
  const appTree = collectDescendantPids(table, appPid);
  const tree = [];
  for (const proc of table) {
    if (!appTree.has(proc.pid) && !appWebKitPids.has(proc.pid) && proc.pid !== daemonPid && !ptyPids.has(proc.pid)) continue;
    const processClass = classifyProcess(proc, { appPid, daemonPid, ptyPids });
    if (processClass) tree.push({ pid: proc.pid, processClass });
  }
  return tree;
}

async function sampleTreeCpu(tree) {
  return { at: performance.now(), seconds: await readCpuSeconds(tree.map((entry) => entry.pid)) };
}

function treeCpuPercent(tree, before, after) {
  const elapsedSeconds = (after.at - before.at) / 1000;
  const byClass = Object.fromEntries(PROCESS_CLASSES.map((processClass) => [processClass, 0]));
  for (const { pid, processClass } of tree) {
    if (!before.seconds.has(pid) || !after.seconds.has(pid)) continue;
    byClass[processClass] += ((after.seconds.get(pid) - before.seconds.get(pid)) / elapsedSeconds) * 100;
  }
  const rounded = Object.fromEntries(Object.entries(byClass).map(([key, value]) => [key, round(value)]));
  return { ...rounded, total: round(Object.values(byClass).reduce((sum, value) => sum + value, 0)) };
}

function round(value, places = 1) {
  if (value === null || value === undefined || Number.isNaN(value)) return null;
  const scale = 10 ** places;
  return Math.round(value * scale) / scale;
}

function percentile(values, fraction) {
  if (values.length === 0) return null;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * fraction))];
}

function spread(values) {
  const present = values.filter((value) => typeof value === 'number');
  if (present.length === 0) return null;
  return { median: round(percentile(present, 0.5), 2), min: round(Math.min(...present), 2), max: round(Math.max(...present), 2) };
}

function summarize(records) {
  const paths = new Set();
  const walk = (value, prefix) => {
    if (typeof value === 'number') paths.add(prefix);
    else if (value && typeof value === 'object' && !Array.isArray(value)) {
      for (const [key, child] of Object.entries(value)) walk(child, prefix ? `${prefix}.${key}` : key);
    }
  };
  for (const record of records) walk(record.metrics, '');
  const read = (record, keyPath) => keyPath.split('.').reduce((value, key) => value?.[key], record.metrics);
  return Object.fromEntries([...paths].sort().map((keyPath) => [keyPath, spread(records.map((record) => read(record, keyPath)))]));
}

function daemonLogSize(logPath) {
  try {
    return fs.statSync(logPath).size;
  } catch {
    return 0;
  }
}

function readLogSlice(logPath, start, end) {
  const fd = fs.openSync(logPath, 'r');
  try {
    const buffer = Buffer.alloc(Math.max(0, end - start));
    fs.readSync(fd, buffer, 0, buffer.length, start);
    return buffer.toString('utf8');
  } finally {
    fs.closeSync(fd);
  }
}

const FORWARD_LINE = /pty stream forward (start|stop): id=(\S+)/;

function streamedTerminals(logPath, runStart, windowStart, windowEnd) {
  const open = new Map();
  const apply = (text) => {
    for (const line of text.split('\n')) {
      const match = FORWARD_LINE.exec(line);
      if (!match) continue;
      const [, edge, id] = match;
      open.set(id, (open.get(id) ?? 0) + (edge === 'start' ? 1 : -1));
    }
  };
  apply(readLogSlice(logPath, runStart, windowStart));
  const streamed = new Set([...open].filter(([, count]) => count > 0).map(([id]) => id));
  const windowText = readLogSlice(logPath, windowStart, windowEnd);
  const starts = [];
  const stops = [];
  for (const line of windowText.split('\n')) {
    const match = FORWARD_LINE.exec(line);
    if (!match) continue;
    (match[1] === 'start' ? starts : stops).push(match[2]);
    if (match[1] === 'start') streamed.add(match[2]);
  }
  return { streamed: [...streamed], startsInWindow: starts, stopsInWindow: stops };
}

async function daemonScreen(observer, terminalId) {
  const reply = observer.waitForMessage(
    (data) => (data.event === 'get_screen_snapshot_result' && data.id === terminalId ? data : null),
    `get_screen_snapshot_result for ${terminalId}`,
    10_000,
  );
  observer.send({ cmd: 'get_screen_snapshot', id: terminalId });
  const data = await reply;
  if (!data.success) throw new Error(`get_screen_snapshot ${terminalId} failed: ${data.error || 'no error given'}`);
  return { lastSeq: data.last_seq ?? 0, text: Buffer.from(data.screen_snapshot || '', 'base64').toString('utf8') };
}

async function assertStreaming(observer, streams) {
  const before = await Promise.all(streams.map((pane) => daemonScreen(observer, pane.runtimeId)));
  await delay(500);
  const after = await Promise.all(streams.map((pane) => daemonScreen(observer, pane.runtimeId)));
  const idle = streams.filter((_, index) => after[index].lastSeq <= before[index].lastSeq);
  if (idle.length > 0) throw new Error(`Streaming shells produced no output in 500ms: ${idle.map((pane) => pane.runtimeId).join(', ')}`);
}

async function profileWindow({ extras, seconds, runIndex, daemonPid, hostPid, appPids }) {
  const dir = path.join(extras.profileDir, `${extras.workload}-run${runIndex + 1}`);
  fs.mkdirSync(dir, { recursive: true });
  const jobs = [];
  if (extras.pprofPort) {
    const url = `http://127.0.0.1:${extras.pprofPort}/debug/pprof/profile?seconds=${seconds}`;
    jobs.push(execFileAsync('curl', ['-sf', '-o', path.join(dir, 'daemon.pprof'), url], { timeout: (seconds + 30) * 1000 }));
  }
  const sample = (pid, name) => execFileAsync('sample', [String(pid), String(seconds), '-file', path.join(dir, `${name}.sample.txt`)], { timeout: (seconds + 60) * 1000 });
  jobs.push(sample(hostPid, 'pty-host'));
  for (const pid of appPids) jobs.push(sample(pid, `app-${pid}`));
  return { dir, done: Promise.all(jobs), daemonPid };
}

function stopStreams(observer, streams) {
  for (const pane of streams) observer.send({ cmd: 'pty_input', id: pane.runtimeId, data: '\u0003', source: 'automation' });
}

async function spawnShellOn(observer, desktopId, cwd, label, createdSessionIds, placement = {}) {
  const id = crypto.randomUUID();
  const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, `${label} spawned`, 20_000);
  observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktopId, ...placement }, label, cols: 100, rows: 30 });
  const result = await spawned;
  if (!result.success) throw new Error(`spawn ${label} failed: ${JSON.stringify(result)}`);
  createdSessionIds.push(id);
  const runtimeId = await observer.waitFor(() => observer.terminalOf(id), `${label} terminal`, 20_000);
  const deadline = Date.now() + 30_000;
  while (!(await daemonScreen(observer, runtimeId)).text.trim()) {
    if (Date.now() > deadline) throw new Error(`${label} showed no prompt in 30 s`);
    await delay(100);
  }
  return { sessionId: id, paneId: result.pane_id, runtimeId };
}

async function buildWorkload({ client, observer, extras, cwd, runIndex, createdSessionIds, createdDesktops }) {
  const tag = `pstream-${runIndex}`;
  const slotted = async () => { const desktop = await createSlottedDesktop(observer); createdDesktops.push(desktop.id); return desktop; };
  const streamsDesktop = await slotted();
  const streams = [await spawnShellOn(observer, streamsDesktop.id, cwd, `${tag}-stream-0`, createdSessionIds)];
  for (let index = 1; index < extras.streams; index += 1) {
    const direction = index % 2 === 1 ? 'vertical' : 'horizontal';
    streams.push(await spawnShellOn(observer, streamsDesktop.id, cwd, `${tag}-stream-${index}`, createdSessionIds, { anchor_pane_id: streams[index - 1].paneId, direction }));
  }
  for (const pane of streams) observer.send({ cmd: 'pty_input', id: pane.runtimeId, data: `${STREAM_COMMAND}\r`, source: 'automation' });
  let quiet;
  let quietDesktopId = streamsDesktop.id;
  if (extras.workload === 'drawn') {
    quiet = await spawnShellOn(observer, streamsDesktop.id, cwd, `${tag}-quiet`, createdSessionIds, { anchor_pane_id: streams[0].paneId, direction: 'horizontal' });
    await showNativeDesktop(client, observer, streamsDesktop);
  } else {
    await showNativeDesktop(client, observer, streamsDesktop);
    if (extras.workload === 'unmounted') {
      const filler = await slotted();
      await spawnShellOn(observer, filler.id, cwd, `${tag}-filler`, createdSessionIds);
      await showNativeDesktop(client, observer, filler);
    }
    const quietDesktop = await slotted();
    quiet = await spawnShellOn(observer, quietDesktop.id, cwd, `${tag}-quiet`, createdSessionIds);
    quietDesktopId = quietDesktop.id;
    await showNativeDesktop(client, observer, quietDesktop);
  }
  await observer.waitFor(() => observer.currentDesktopId() === quietDesktopId, `desktop ${quietDesktopId} current`);
  return { streams, quiet, streamsDesktopId: streamsDesktop.id, quietDesktopId, arrangement: { current: observer.currentDesktopId() } };
}

async function keyToPaint(driver, observer, quiet, keys) {
  if (keys === 0) return null;
  observer.send({ cmd: 'pty_input', id: quiet.runtimeId, data: 'cat\r', source: 'automation' });
  await delay(500);
  await driver.click({ key: `tile.${quiet.paneId}.header` });
  const paints = [];
  for (let index = 0; index < keys; index += 1) {
    const token = `k${index.toString(36)}q`;
    const startedAt = performance.now();
    await driver.typeInto(null, token);
    await driver.state(`terminal:${quiet.runtimeId}`, { includes: token, timeoutMs: 10_000 });
    paints.push(performance.now() - startedAt);
    await driver.pressKey('Enter');
    await delay(150);
  }
  observer.send({ cmd: 'pty_input', id: quiet.runtimeId, data: '\u0003', source: 'automation' });
  return { keys, misses: 0, paintMs: { p50: round(percentile(paints, 0.5)), p90: round(percentile(paints, 0.9)) } };
}

async function catchUp({ driver, observer, workload, runIndex }) {
  const token = `${Date.now().toString(36)}${runIndex}`;
  stopStreams(observer, workload.streams);
  for (const pane of workload.streams) {
    observer.send({ cmd: 'pty_input', id: pane.runtimeId, data: `printf 'SET''TLED_%s\\n' ${token}\r`, source: 'automation' });
  }
  const marker = `SETTLED_${token}`;
  for (const pane of workload.streams) {
    const deadline = Date.now() + 15_000;
    while (!(await daemonScreen(observer, pane.runtimeId)).text.includes(marker)) {
      if (Date.now() > deadline) throw new Error(`The daemon's screen of ${pane.runtimeId} never showed ${marker}`);
      await delay(50);
    }
  }
  const startedAt = performance.now();
  await observer.setCurrentDesktop(workload.streamsDesktopId);
  const caughtUp = await Promise.all(workload.streams.map(async (pane) => {
    await driver.state(`terminal:${pane.runtimeId}`, { includes: marker, timeoutMs: 15_000 });
    return performance.now() - startedAt;
  }));
  return { slowestPaneMs: round(Math.max(...caughtUp)), perPaneMs: caughtUp.map((ms) => round(ms)) };
}

async function measureRun({ client, driver, observer, extras, runner, runIndex, pids: launchPids, dataDir, logPath, cwd }) {
  const createdSessionIds = [];
  const createdDesktops = [];
  const runLogStart = daemonLogSize(logPath);
  try {
    const workload = await buildWorkload({ client, observer, extras, cwd, runIndex, createdSessionIds, createdDesktops });
    await assertStreaming(observer, workload.streams);
    await delay(WARMUP_MS);
    const hostPid = extras.sharedHost === 'true' ? liveSharedHostPid(dataDir) : null;
    const pids = { ...launchPids, hostPid, ptyPids: new Set(hostPid ? [hostPid] : liveGoWorkerPids(dataDir)) };

    const tree = await appProcessTree(pids);
    const appPids = tree.filter((entry) => entry.processClass === 'app').map((entry) => entry.pid);
    const panes = [...workload.streams, workload.quiet];
    const roleOf = (pane) => (pane === workload.quiet ? 'quiet' : `stream-${workload.streams.indexOf(pane)}`);

    const windowLogStart = daemonLogSize(logPath);
    const seqBefore = await Promise.all(panes.map((pane) => daemonScreen(observer, pane.runtimeId)));
    const cpuBefore = await sampleTreeCpu(tree);
    const profile = extras.profileDir
      ? await profileWindow({ extras, seconds: extras.seconds, runIndex, daemonPid: pids.daemonPid, hostPid: pids.hostPid, appPids })
      : null;
    await delay(extras.seconds * 1000);
    const cpuAfter = await sampleTreeCpu(tree);
    const seqAfter = await Promise.all(panes.map((pane) => daemonScreen(observer, pane.runtimeId)));
    const windowLogEnd = daemonLogSize(logPath);
    if (profile) await profile.done;

    const windowSeconds = (cpuAfter.at - cpuBefore.at) / 1000;
    const streamed = streamedTerminals(logPath, runLogStart, windowLogStart, windowLogEnd);
    const roleByRuntime = new Map(panes.map((pane) => [pane.runtimeId, roleOf(pane)]));
    const terminals = panes.map((pane, index) => ({
      role: roleOf(pane),
      runtimeId: pane.runtimeId,
      daemonEventsPerSecond: round((seqAfter[index].lastSeq - seqBefore[index].lastSeq) / windowSeconds),
    }));

    const keyProbe = await keyToPaint(driver, observer, workload.quiet, extras.keys);
    const caughtUp = extras.workload === 'hidden' ? await catchUp({ driver, observer, workload, runIndex }) : null;

    const sumStreams = (pick) => terminals.filter((terminal) => terminal.role !== 'quiet').reduce((sum, terminal) => sum + (pick(terminal) ?? 0), 0);
    const record = {
      type: 'run',
      workload: extras.workload,
      run: runIndex + 1,
      windowSeconds: round(windowSeconds, 2),
      arrangement: workload.arrangement,
      metrics: {
        cpuPercent: treeCpuPercent(tree, cpuBefore, cpuAfter),
        streams: {
          daemonEventsPerSecond: round(sumStreams((terminal) => terminal.daemonEventsPerSecond)),
          streamedToApp: streamed.streamed.filter((id) => roleByRuntime.get(id)?.startsWith('stream')).length,
        },
        keyToPaintMs: keyProbe ? keyProbe.paintMs : undefined,
        catchUpMs: caughtUp ? caughtUp.slowestPaneMs : undefined,
      },
      terminals,
      streamedTerminals: {
        ...streamed,
        roles: Object.fromEntries(streamed.streamed.map((id) => [id, roleByRuntime.get(id) ?? 'other'])),
      },
      keyProbe,
      catchUp: caughtUp,
      processes: tree,
      profileDir: profile?.dir ?? null,
    };
    runner.writeJson(`run-${runIndex + 1}.json`, record);
    return record;
  } finally {
    const close = async () => {
      await Promise.all(createdSessionIds.map((id) => observer.unregisterSession(id)));
      for (const id of createdDesktops) await closeEmptiedDesktop(observer, id);
    };
    await close().catch((error) => runner.log('close_sessions_failed', { error: String(error) }));
  }
}

async function main() {
  const { options, extras, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printHelp();
    return;
  }
  const instance = currentHarnessInstance();
  const dataDir = dataDirForInstance(instance);
  const logPath = path.join(dataDir, 'daemon.log');

  const runner = createScenarioRunner(options, {
    scenarioId: 'PERF-PTY-STREAM',
    tier: 'perf',
    prefix: `perf-pty-stream-${extras.workload}`,
    allowRealAgents: false,
    metadata: { ...extras, streamCommand: STREAM_COMMAND },
  });
  const launchEnv = extras.pprofPort ? { ATTN_PPROF: String(extras.pprofPort) } : null;
  const client = new UiAutomationClient({ appPath: options.appPath, launchEnv });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl || defaultWSURLForInstance(instance) });
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  const records = [];
  try {
    const pids = await runner.step('launch_app_with_the_shared_host', async () => {
      if (extras.pprofPort) {
        await client.quitApp();
        await stopDaemonKeepingTerminals(instance);
      }
      const webkitBaseline = await captureWebKitPids();
      await launchFreshAppAndConnect(client, observer);
      queueDaemonSettingRestore(observer, 'pty_shared_host_enabled');
      await writeDaemonSettings([{ key: 'pty_shared_host_enabled', value: extras.sharedHost }]);
      await observer.waitFor(() => observer.getSetting('pty_shared_host_active') === extras.sharedHost, `the shared terminal host active=${extras.sharedHost} for new terminals`, 20_000);
      const appPid = client.readManifest()?.pid;
      const daemonPid = readLiveDaemonPid(instance);
      if (!appPid || !daemonPid) throw new Error(`Missing pids: app=${appPid} daemon=${daemonPid}`);
      const appWebKitPids = await webKitPidsLaunchedSince(webkitBaseline);
      return { appPid, daemonPid, appWebKitPids };
    });

    const cwd = path.join(runner.sessionDir, 'shells');
    fs.mkdirSync(cwd, { recursive: true });
    for (let runIndex = 0; runIndex < extras.runs; runIndex += 1) {
      const record = await runner.step(`run_${runIndex + 1}`, () => measureRun({ client, driver, observer, extras, runner, runIndex, pids, dataDir, logPath, cwd }));
      records.push(record);
      console.log(JSON.stringify(record));
    }
    const summary = { type: 'summary', workload: extras.workload, sharedHost: extras.sharedHost, runs: records.length, seconds: extras.seconds, streams: extras.streams, profiled: Boolean(extras.profileDir), metrics: summarize(records) };
    runner.writeJson('summary-metrics.json', summary);
    console.log(JSON.stringify(summary));
    await runner.finishSuccess({ summary });
  } catch (error) {
    const result = await runner.finishFailure(error, { completedRuns: records.length, arrangement: observer.describeArrangement?.() });
    console.error(result.error);
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
