#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import {
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  queueDaemonSettingRestore,
  waitForAppCurrentDesktop,
  writeDaemonSettings,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, dataDirForInstance, defaultWSURLForInstance } from './harnessInstance.mjs';
import { captureWebKitPids, collectDescendantPids, isRelevantWebKitProcess, readLiveDaemonPid, readProcessTable, stopDaemonKeepingTerminals } from './perfMeasure.mjs';
import { waitForPaneAttached, waitForPaneShellReady, waitForPaneText } from './scenarioAssertions.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const WORKLOADS = ['hidden', 'drawn', 'unmounted'];
const STREAM_COMMAND = "sh -c 'while :; do seq 1 400; sleep 0.02; done'";
const WARMUP_MS = 2_000;
const PROCESS_CLASSES = ['app', 'webkit_webcontent', 'webkit_networking', 'webkit_gpu', 'daemon', 'pty_host'];

function parseArgs(argv) {
  const args = argv.filter((arg) => arg !== '--');
  const extras = { workload: 'drawn', streams: 3, seconds: 30, runs: 5, keys: 30, profileDir: null, pprofPort: null };
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
    else passthrough.push(arg);
  }
  if (!WORKLOADS.includes(extras.workload)) throw new Error(`--workload must be one of ${WORKLOADS.join(', ')}, got ${extras.workload}`);
  for (const key of ['streams', 'seconds', 'runs']) {
    if (!Number.isInteger(extras[key]) || extras[key] < 1) throw new Error(`--${key} must be a positive integer, got ${extras[key]}`);
  }
  if (!Number.isInteger(extras.keys) || extras.keys < 0) throw new Error(`--keys must be a non-negative integer, got ${extras.keys}`);
  if (extras.pprofPort !== null && !extras.profileDir) throw new Error('--pprof-port needs --profile-dir');
  return { options: parseCommonArgs(passthrough), extras, help: args.includes('--help') || args.includes('-h') };
}

function printHelp() {
  printCommonHelp('scripts/real-app-harness/scenario-perf-pty-stream.mjs');
  console.log(`
Measures what streaming terminal output costs, with the shared terminal host on.

  --workload hidden|drawn|unmounted
      hidden: the streaming shells are splits on the previous desktop; a quiet shell is drawn.
      drawn: the streaming shells and the quiet shell are splits on the current desktop.
      unmounted: the streaming shells are on a desktop that is neither current nor previous.
  --streams N     streaming shells (default 3)
  --seconds S     measured window per run (default 30)
  --runs N        runs, each with fresh sessions (default 5)
  --keys N        keystrokes for the key-to-echo probe after each window (default 30)
  --profile-dir D profile the daemon, the PTY host and WebContent during each window into D.
                  Profiling costs CPU, so profiled runs are not baselines.
  --pprof-port P  restart the instance's daemon with ATTN_PPROF=P so the daemon profile is taken too

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

function classifyProcess(proc, { appPid, daemonPid, hostPid }) {
  if (proc.pid === appPid) return 'app';
  if (proc.pid === daemonPid) return 'daemon';
  if (proc.pid === hostPid) return 'pty_host';
  if (proc.command.includes('com.apple.WebKit.WebContent')) return 'webkit_webcontent';
  if (proc.command.includes('com.apple.WebKit.Networking')) return 'webkit_networking';
  if (proc.command.includes('com.apple.WebKit.GPU')) return 'webkit_gpu';
  return null;
}

async function webKitPidsLaunchedSince(webkitBaseline) {
  const table = await readProcessTable();
  return new Set(table.filter((proc) => isRelevantWebKitProcess(proc) && !webkitBaseline.has(proc.pid)).map((proc) => proc.pid));
}

async function appProcessTree({ appPid, daemonPid, hostPid, appWebKitPids }) {
  const table = await readProcessTable();
  const appTree = collectDescendantPids(table, appPid);
  const tree = [];
  for (const proc of table) {
    if (!appTree.has(proc.pid) && !appWebKitPids.has(proc.pid) && proc.pid !== daemonPid && proc.pid !== hostPid) continue;
    const processClass = classifyProcess(proc, { appPid, daemonPid, hostPid });
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

async function waitForAppState(client, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_state');
    if (predicate(last)) return last;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last arrangement:\n${JSON.stringify(last?.arrangement, null, 2)}`);
}

async function paneWithRuntime(client, sessionId, paneId) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const desktop = await client.request('get_desktop', { sessionId }).catch(() => null);
    const pane = desktop?.panes?.find((entry) => entry.paneId === paneId);
    if (pane?.runtimeId) return { sessionId: pane.sessionId, paneId, runtimeId: pane.runtimeId };
    await delay(100);
  }
  throw new Error(`Pane ${paneId} of session ${sessionId} never reported a runtime id`);
}

async function readyShell(client, pane, label) {
  await waitForPaneAttached(client, pane.sessionId, pane.paneId, 20_000);
  await waitForPaneShellReady(client, pane.sessionId, pane.paneId, { timeoutMs: 30_000, description: `${label} ready` });
}

async function openShellDesktop(client, observer, cwd, label, createdSessionIds) {
  const desktop = await observer.createDesktop();
  await observer.setCurrentDesktop(desktop.id);
  await waitForAppCurrentDesktop(client, desktop.id);
  const { sessionId } = await client.request('create_session', { cwd, agent: 'shell', label });
  createdSessionIds.push(sessionId);
  const state = await waitForAppState(
    client,
    (next) => next.arrangement.desktops.find((entry) => entry.id === desktop.id)?.panes.some((pane) => pane.sessionId === sessionId),
    `${label} placed on desktop ${desktop.id}`,
  );
  const paneId = state.arrangement.desktops.find((entry) => entry.id === desktop.id).panes.find((pane) => pane.sessionId === sessionId).paneId;
  const pane = await paneWithRuntime(client, sessionId, paneId);
  await readyShell(client, pane, label);
  return { desktopId: desktop.id, pane };
}

async function splitShell(client, desktopId, target, direction, label, createdSessionIds) {
  const before = new Set((await client.request('get_state')).arrangement.desktops.find((entry) => entry.id === desktopId).panes.map((pane) => pane.paneId));
  await client.request('split_pane', { sessionId: target.sessionId, targetPaneId: target.paneId, direction });
  const state = await waitForAppState(
    client,
    (next) => next.arrangement.desktops.find((entry) => entry.id === desktopId)?.panes.some((pane) => !before.has(pane.paneId)),
    `${label} split onto desktop ${desktopId}`,
    30_000,
  );
  const added = state.arrangement.desktops.find((entry) => entry.id === desktopId).panes.find((pane) => !before.has(pane.paneId));
  createdSessionIds.push(added.sessionId);
  const pane = await paneWithRuntime(client, added.sessionId, added.paneId);
  await readyShell(client, pane, label);
  return pane;
}

async function buildWorkload({ client, observer, extras, cwd, runIndex, createdSessionIds }) {
  const tag = `pstream-${runIndex}`;
  const first = await openShellDesktop(client, observer, cwd, `${tag}-stream-0`, createdSessionIds);
  const streamsDesktopId = first.desktopId;
  const streams = [first.pane];
  for (let index = 1; index < extras.streams; index += 1) {
    const direction = index % 2 === 1 ? 'vertical' : 'horizontal';
    streams.push(await splitShell(client, streamsDesktopId, streams[index - 1], direction, `${tag}-stream-${index}`, createdSessionIds));
  }
  for (const pane of streams) {
    await client.request('write_pane', { sessionId: pane.sessionId, paneId: pane.paneId, text: STREAM_COMMAND, submit: true });
  }

  let quiet;
  let quietDesktopId = streamsDesktopId;
  if (extras.workload === 'drawn') {
    quiet = await splitShell(client, streamsDesktopId, streams[0], 'horizontal', `${tag}-quiet`, createdSessionIds);
  } else {
    if (extras.workload === 'unmounted') {
      await openShellDesktop(client, observer, cwd, `${tag}-filler`, createdSessionIds);
    }
    const opened = await openShellDesktop(client, observer, cwd, `${tag}-quiet`, createdSessionIds);
    quiet = opened.pane;
    quietDesktopId = opened.desktopId;
  }
  await client.request('select_session', { sessionId: quiet.sessionId });

  const expectation = {
    drawn: (arrangement, streamsDesktop) => arrangement.currentDesktopId === streamsDesktopId && streamsDesktop?.visible,
    hidden: (arrangement, streamsDesktop) => arrangement.currentDesktopId === quietDesktopId
      && arrangement.previousDesktopId === streamsDesktopId && streamsDesktop?.mounted && !streamsDesktop?.visible,
    unmounted: (arrangement, streamsDesktop) => arrangement.currentDesktopId === quietDesktopId
      && arrangement.previousDesktopId !== streamsDesktopId && streamsDesktop && !streamsDesktop.mounted,
  }[extras.workload];
  const state = await waitForAppState(
    client,
    (next) => expectation(next.arrangement, next.arrangement.desktops.find((entry) => entry.id === streamsDesktopId)),
    `the ${extras.workload} arrangement`,
  );
  const streamsDesktop = state.arrangement.desktops.find((entry) => entry.id === streamsDesktopId);
  return {
    streams,
    quiet,
    streamsDesktopId,
    quietDesktopId,
    arrangement: {
      current: state.arrangement.currentDesktopId,
      previous: state.arrangement.previousDesktopId,
      streamsDesktopMounted: streamsDesktop.mounted,
      streamsDesktopVisible: streamsDesktop.visible,
    },
  };
}

async function assertStreaming(observer, streams) {
  const before = await Promise.all(streams.map((pane) => daemonScreen(observer, pane.runtimeId)));
  await delay(500);
  const after = await Promise.all(streams.map((pane) => daemonScreen(observer, pane.runtimeId)));
  const idle = streams.filter((_, index) => after[index].lastSeq <= before[index].lastSeq);
  if (idle.length > 0) throw new Error(`Streaming shells produced no output in 500ms: ${idle.map((pane) => pane.runtimeId).join(', ')}`);
}

async function profileWindow({ extras, seconds, runIndex, daemonPid, hostPid, webContentPids }) {
  const dir = path.join(extras.profileDir, `${extras.workload}-run${runIndex + 1}`);
  fs.mkdirSync(dir, { recursive: true });
  const jobs = [];
  if (extras.pprofPort) {
    const url = `http://127.0.0.1:${extras.pprofPort}/debug/pprof/profile?seconds=${seconds}`;
    jobs.push(execFileAsync('curl', ['-sf', '-o', path.join(dir, 'daemon.pprof'), url], { timeout: (seconds + 30) * 1000 }));
  }
  const sample = (pid, name) => execFileAsync('sample', [String(pid), String(seconds), '-file', path.join(dir, `${name}.sample.txt`)], { timeout: (seconds + 60) * 1000 });
  jobs.push(sample(hostPid, 'pty-host'));
  for (const pid of webContentPids) jobs.push(sample(pid, `webcontent-${pid}`));
  return { dir, done: Promise.all(jobs), daemonPid };
}

function perTerminal(before, after, panes, roleOf) {
  const byRuntime = (snapshot) => new Map((snapshot.terminals || []).map((terminal) => [terminal.runtimeId, terminal]));
  const start = byRuntime(before);
  const end = byRuntime(after);
  return panes.map((pane) => {
    const a = start.get(pane.runtimeId);
    const b = end.get(pane.runtimeId);
    if (!a || !b) return { role: roleOf(pane), runtimeId: pane.runtimeId, inApp: false };
    return {
      role: roleOf(pane),
      runtimeId: pane.runtimeId,
      inApp: true,
      writesParsed: b.writeParsedCount - a.writeParsedCount,
      renders: b.renderCount - a.renderCount,
      renderCpuMs: round(b.renderCpuTotalMs - a.renderCpuTotalMs),
    };
  });
}

async function keyToEcho(client, quiet, keys) {
  if (keys === 0) return null;
  await client.request('write_pane', { sessionId: quiet.sessionId, paneId: quiet.paneId, text: 'cat', submit: true });
  await delay(500);
  await client.request('click_pane', { sessionId: quiet.sessionId, paneId: quiet.paneId });
  const runtimeId = quiet.runtimeId;
  const echoes = [];
  const paints = [];
  let misses = 0;
  let extraWrites = 0;
  const terminalOf = (snapshot) => (snapshot.terminals || []).find((terminal) => terminal.runtimeId === runtimeId);
  for (let index = 0; index < keys; index += 1) {
    const before = terminalOf(await client.request('capture_perf_snapshot', { settleFrames: 0, includeMemory: false }));
    const startedAt = performance.now();
    await client.request('type_pane_via_ui', { sessionId: quiet.sessionId, paneId: quiet.paneId, text: 'a' });
    let echo = null;
    let paint = null;
    for (let poll = 0; poll < 400 && (echo === null || paint === null); poll += 1) {
      const snapshot = await client.request('capture_perf_snapshot', { settleFrames: 0, includeMemory: false });
      const terminal = terminalOf(snapshot);
      if (!terminal || !before) continue;
      if (echo === null && terminal.writeParsedCount > before.writeParsedCount && snapshot.pty?.lastPtyInputRuntimeId === runtimeId) {
        echo = terminal.lastWriteParsedAt - Date.parse(snapshot.pty.lastPtyInputAt);
        extraWrites += terminal.writeParsedCount - before.writeParsedCount - 1;
      }
      if (paint === null && terminal.renderCount > before.renderCount) paint = performance.now() - startedAt;
    }
    if (echo === null || paint === null) misses += 1;
    if (echo !== null) echoes.push(echo);
    if (paint !== null) paints.push(paint);
    await delay(150);
  }
  await client.request('write_pane', { sessionId: quiet.sessionId, paneId: quiet.paneId, text: '\u0003' });
  if (echoes.length < keys) throw new Error(`Key-to-echo saw ${echoes.length} of ${keys} echoes in the quiet pane`);
  return {
    keys,
    misses,
    extraWrites,
    echoMs: { p50: percentile(echoes, 0.5), p90: percentile(echoes, 0.9) },
    paintMs: { p50: round(percentile(paints, 0.5)), p90: round(percentile(paints, 0.9)) },
  };
}

function stopStreams(observer, streams) {
  for (const pane of streams) observer.send({ cmd: 'pty_input', id: pane.runtimeId, data: '\u0003', source: 'automation' });
}

async function hiddenCatchUp({ client, observer, workload, runIndex }) {
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
  await client.request('select_session', { sessionId: workload.streams[0].sessionId });
  const caughtUp = await Promise.all(workload.streams.map(async (pane) => {
    await waitForPaneText(client, pane.sessionId, pane.paneId, (text) => text.includes(marker), `${pane.runtimeId} shows the daemon's screen`, 15_000);
    return performance.now() - startedAt;
  }));
  return { slowestPaneMs: round(Math.max(...caughtUp)), perPaneMs: caughtUp.map((ms) => round(ms)) };
}

async function measureRun({ client, observer, extras, runner, runIndex, pids: launchPids, dataDir, logPath, cwd }) {
  const createdSessionIds = [];
  const runLogStart = daemonLogSize(logPath);
  try {
    const workload = await buildWorkload({ client, observer, extras, cwd, runIndex, createdSessionIds });
    await assertStreaming(observer, workload.streams);
    await delay(WARMUP_MS);
    const pids = { ...launchPids, hostPid: liveSharedHostPid(dataDir) };

    const tree = await appProcessTree(pids);
    const webContentPids = tree.filter((entry) => entry.processClass === 'webkit_webcontent').map((entry) => entry.pid);
    const panes = [...workload.streams, workload.quiet];
    const roleOf = (pane) => (pane === workload.quiet ? 'quiet' : `stream-${workload.streams.indexOf(pane)}`);

    const windowLogStart = daemonLogSize(logPath);
    const seqBefore = await Promise.all(panes.map((pane) => daemonScreen(observer, pane.runtimeId)));
    const perfBefore = await client.request('capture_perf_snapshot', { settleFrames: 0, includeMemory: false });
    const cpuBefore = await sampleTreeCpu(tree);
    const profile = extras.profileDir
      ? await profileWindow({ extras, seconds: extras.seconds, runIndex, daemonPid: pids.daemonPid, hostPid: pids.hostPid, webContentPids })
      : null;
    await delay(extras.seconds * 1000);
    const cpuAfter = await sampleTreeCpu(tree);
    const perfAfter = await client.request('capture_perf_snapshot', { settleFrames: 0, includeMemory: false });
    const seqAfter = await Promise.all(panes.map((pane) => daemonScreen(observer, pane.runtimeId)));
    const windowLogEnd = daemonLogSize(logPath);
    if (profile) await profile.done;

    const windowSeconds = (cpuAfter.at - cpuBefore.at) / 1000;
    const streamed = streamedTerminals(logPath, runLogStart, windowLogStart, windowLogEnd);
    const roleByRuntime = new Map(panes.map((pane) => [pane.runtimeId, roleOf(pane)]));
    const terminals = perTerminal(perfBefore, perfAfter, panes, roleOf).map((terminal, index) => ({
      ...terminal,
      daemonEventsPerSecond: round((seqAfter[index].lastSeq - seqBefore[index].lastSeq) / windowSeconds),
      appWritesPerSecond: terminal.inApp ? round(terminal.writesParsed / windowSeconds) : null,
    }));

    const keyProbe = await keyToEcho(client, workload.quiet, extras.keys);
    const catchUp = extras.workload === 'hidden' ? await hiddenCatchUp({ client, observer, workload, runIndex }) : null;

    const sumStreams = (pick) => terminals.filter((terminal) => terminal.role !== 'quiet').reduce((sum, terminal) => sum + (pick(terminal) ?? 0), 0);
    const record = {
      type: 'run',
      workload: extras.workload,
      run: runIndex + 1,
      windowSeconds: round(windowSeconds, 2),
      arrangement: workload.arrangement,
      metrics: {
        cpuPercent: treeCpuPercent(tree, cpuBefore, cpuAfter),
        ws: {
          messagesPerSecond: round((perfAfter.pty.wsMessageCount - perfBefore.pty.wsMessageCount) / windowSeconds),
          bytesPerSecond: Math.round((perfAfter.pty.wsMessageBytes - perfBefore.pty.wsMessageBytes) / windowSeconds),
          ptyOutputPerSecond: round((perfAfter.pty.ptyOutputCount - perfBefore.pty.ptyOutputCount) / windowSeconds),
        },
        streams: {
          daemonEventsPerSecond: round(sumStreams((terminal) => terminal.daemonEventsPerSecond)),
          appWritesPerSecond: round(sumStreams((terminal) => terminal.appWritesPerSecond)),
          streamedToApp: streamed.streamed.filter((id) => roleByRuntime.get(id)?.startsWith('stream')).length,
        },
        keyToEchoMs: keyProbe ? keyProbe.echoMs : undefined,
        keyToPaintMs: keyProbe ? keyProbe.paintMs : undefined,
        catchUpMs: catchUp ? catchUp.slowestPaneMs : undefined,
      },
      terminals,
      streamedTerminals: {
        ...streamed,
        roles: Object.fromEntries(streamed.streamed.map((id) => [id, roleByRuntime.get(id) ?? 'other'])),
      },
      keyProbe,
      catchUp,
      processes: tree,
      profileDir: profile?.dir ?? null,
    };
    runner.writeJson(`run-${runIndex + 1}.json`, record);
    return record;
  } finally {
    await closeScenarioSessions(client, createdSessionIds).catch((error) => runner.log('close_sessions_failed', { error: String(error) }));
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
  const observer = new DaemonObserver({ wsUrl: options.wsUrl || defaultWSURLForInstance(instance) });
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  const records = [];
  try {
    const pids = await runner.step('launch_app_with_the_shared_host', async () => {
      if (extras.pprofPort) {
        await client.quitApp();
        await stopDaemonKeepingTerminals(instance, { appPath: options.appPath });
      }
      const webkitBaseline = await captureWebKitPids();
      await launchFreshAppAndConnect(client, observer);
      queueDaemonSettingRestore(observer, 'pty_shared_host_enabled');
      await writeDaemonSettings([{ key: 'pty_shared_host_enabled', value: 'true' }]);
      await observer.waitFor(() => observer.getSetting('pty_shared_host_active') === 'true', 'the shared terminal host active for new terminals', 20_000);
      const appPid = client.readManifest()?.pid;
      const daemonPid = readLiveDaemonPid(instance);
      if (!appPid || !daemonPid) throw new Error(`Missing pids: app=${appPid} daemon=${daemonPid}`);
      const appWebKitPids = await webKitPidsLaunchedSince(webkitBaseline);
      return { appPid, daemonPid, appWebKitPids };
    });

    const cwd = path.join(runner.sessionDir, 'shells');
    fs.mkdirSync(cwd, { recursive: true });
    for (let runIndex = 0; runIndex < extras.runs; runIndex += 1) {
      const record = await runner.step(`run_${runIndex + 1}`, () => measureRun({ client, observer, extras, runner, runIndex, pids, dataDir, logPath, cwd }));
      records.push(record);
      console.log(JSON.stringify(record));
    }
    const summary = { type: 'summary', workload: extras.workload, runs: records.length, seconds: extras.seconds, streams: extras.streams, profiled: Boolean(extras.profileDir), metrics: summarize(records) };
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
