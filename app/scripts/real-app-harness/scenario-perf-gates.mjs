#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync, spawn } from 'node:child_process';
import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { dataDirForInstance } from './harnessInstance.mjs';
import { appShellInTree } from './platform.mjs';
import { closeEmptiedDesktop, createSlottedDesktop, showNativeDesktop } from './perfMeasure.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const WORKLOADS = {
  idle1: { desk: 'one', window: 'idle' },
  quiet4: { desk: 'four', window: 'idle' },
  paint1: { desk: 'one', paint: 'all' },
  paint4: { desk: 'four', paint: 'all' },
  stream: { desk: 'four', paint: 'three' },
  flood: { desk: 'four', paint: 'flood' },
  switches: { desk: 'four', switches: 10 },
  rest: { desk: 'four', sample: 8 },
  launch: { desk: 'four', launch: true },
};
const PAINT_COMMAND = "sh -c 'while :; do seq 1 400; sleep 0.02; done'";
const FLOOD_COMMAND = "yes 'the quick brown fox jumps over the lazy dog 0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ abcdefgh'";
const MARK_COMMAND = "clear; printf 'GATE%s\\n' MARK";
const MARK = 'GATEMARK';
const VIEW = { width: 1710, height: 956 };
const VISIBLE_PX = 20;
const OWN_ROLES = ['daemon', 'pty-host'];
const REST_SIGNS = {
  displayLink: /CVDisplayLink|CADisplayLink|CA::Display::DisplayLink/,
  timer: /__CFRunLoopDoTimer|_dispatch_source_latch_and_call|dispatch_after/,
  gpuSubmit: /MTLCommandBuffer commit|-\[_MTLCommandBuffer commit\]|presentDrawable/,
};

function parseArgs(argv) {
  const args = argv.filter((arg) => arg !== '--');
  const extras = { workloads: Object.keys(WORKLOADS), runs: 5, seconds: 30, settle: 20, out: null, profile: false };
  const passthrough = [];
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    const value = () => {
      if (args[index + 1] === undefined) throw new Error(`${arg} needs a value`);
      index += 1;
      return args[index];
    };
    if (arg === '--workloads') extras.workloads = value().split(',');
    else if (arg === '--runs') extras.runs = Number(value());
    else if (arg === '--seconds') extras.seconds = Number(value());
    else if (arg === '--settle') extras.settle = Number(value());
    else if (arg === '--profile') extras.profile = true;
    else passthrough.push(arg);
  }
  for (const name of extras.workloads) if (!WORKLOADS[name]) throw new Error(`--workloads takes ${Object.keys(WORKLOADS).join(',')}; got ${name}`);
  for (const key of ['runs', 'seconds', 'settle']) {
    if (!Number.isInteger(extras[key]) || extras[key] < 1) throw new Error(`--${key} must be a positive integer, got ${extras[key]}`);
  }
  return { options: parseCommonArgs(passthrough), extras, help: args.includes('--help') || args.includes('-h') };
}

function printHelp() {
  printCommonHelp('scripts/real-app-harness/scenario-perf-gates.mjs');
  console.log(`
Measures the cut-over gates on the installed app, native or Tauri, whichever the
instance's tree holds. Input reaches the shells only through the daemon, so both
apps get the same work. Each run launches the app fresh, parks it unfocused at
${VIEW.width}x${VIEW.height} with ${VISIBLE_PX} px on screen, waits --settle seconds, then measures.
CPU and footprint come from proc_pid_rusage once a second, per process role.

  --workloads a,b   ${Object.keys(WORKLOADS).join(',')} (default all)
      idle1, quiet4    one shell, or four in a 2x2 grid, doing nothing
      paint1, paint4   every shown shell prints seq 1 400 every 20 ms
      stream           three of the four print, the fourth is quiet
      flood            yes in all four
      switches         footprint before and after 10 switches between the two desktops
      rest             an 8 s \`sample\` of the app at quiet4, read for display links, timers and GPU submits
      launch           launch to the first frame with terminal content, four shells
  --runs N          fresh launches per workload (default 5)
  --seconds S       measured window (default 30)
  --settle S        wait after launch before measuring (default 20)
  --profile         after a painting window, an 8 s \`sample\` of the app while it still paints

The native app's frame recorder writes perf.json when it quits; painting
workloads quit it while still painting and report its frame-time distribution.
Prints one {"type":"run"} JSON line per run and a {"type":"summary"} line per
workload with the median, min and max over runs.`);
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const median = (values) => {
  const sorted = values.filter((v) => v != null).sort((a, b) => a - b);
  return sorted.length ? sorted[Math.floor(sorted.length / 2)] : null;
};
const round = (value, digits = 1) => (value == null ? null : Number(value.toFixed(digits)));
const mib = (bytes) => (bytes == null ? null : round(bytes / 1048576));

function usageProbe(runDir) {
  const source = new URL('./procUsage.c', import.meta.url).pathname;
  const hash = crypto.createHash('sha256').update(fs.readFileSync(source)).digest('hex').slice(0, 12);
  const binary = path.join(os.tmpdir(), `attn-proc-usage-${hash}`);
  if (!fs.existsSync(binary)) execFileSync('cc', ['-O2', '-o', binary, source]);
  fs.copyFileSync(source, path.join(runDir, 'procUsage.c'));
  return () => {
    const rows = new Map();
    for (const line of execFileSync(binary, { encoding: 'utf8', maxBuffer: 1 << 24 }).trim().split('\n')) {
      const [pid, ppid, responsible, footprint, cpuNs, instructions, pCycles, cycles] = line.split(' ').map(Number);
      rows.set(pid, { pid, ppid, responsible, footprint, cpuNs, instructions, pCycles, cycles });
    }
    return rows;
  };
}

function commands() {
  const out = new Map();
  for (const line of execFileSync('ps', ['-axww', '-o', 'pid=,command='], { encoding: 'utf8', maxBuffer: 1 << 26 }).split('\n')) {
    const match = /^\s*(\d+)\s+(.*)$/.exec(line);
    if (match) out.set(Number(match[1]), match[2]);
  }
  return out;
}

function roleOf(appPid, pid, command) {
  if (pid === appPid) return 'app';
  const webkit = /com\.apple\.WebKit\.(WebContent|GPU|Networking)/.exec(command);
  return webkit ? `webkit_${webkit[1].toLowerCase()}` : 'app_other';
}

function processRoles(probe, { pid: appPid, before }, daemonPid) {
  const rows = probe();
  const cmds = commands();
  const roles = new Map();
  const launcher = rows.get(appPid)?.responsible;
  const startedWithApp = (row) => !before.has(row.pid) && row.responsible === launcher && /com\.apple\.WebKit\./.test(cmds.get(row.pid) ?? '');
  for (const row of rows.values()) {
    if (row.pid === appPid || row.responsible === appPid || startedWithApp(row)) roles.set(row.pid, roleOf(appPid, row.pid, cmds.get(row.pid) ?? ''));
  }
  roles.set(daemonPid, 'daemon');
  for (const row of rows.values()) {
    if (row.ppid === daemonPid && /attn-pty-host/.test(cmds.get(row.pid) ?? '')) roles.set(row.pid, 'pty-host');
  }
  return roles;
}

async function measureWindow(probe, roles, seconds) {
  const read = () => {
    const rows = probe();
    const at = performance.now();
    const byRole = {};
    const cpu = new Map();
    for (const [pid, role] of roles) {
      const row = rows.get(pid);
      if (!row) continue;
      byRole[role] = (byRole[role] ?? 0) + row.footprint;
      cpu.set(pid, row);
    }
    return { at, byRole, cpu };
  };
  const samples = [read()];
  for (let second = 0; second < seconds; second += 1) {
    await sleep(1000);
    samples.push(read());
  }
  const first = samples[0];
  const last = samples.at(-1);
  const wallS = (last.at - first.at) / 1000;
  const result = { seconds: round(wallS), roles: {} };
  for (const role of new Set(roles.values())) {
    const spent = { cpuNs: 0, instructions: 0, pCycles: 0, cycles: 0 };
    for (const [pid, r] of roles) {
      if (r !== role || !last.cpu.has(pid) || !first.cpu.has(pid)) continue;
      for (const key of Object.keys(spent)) spent[key] += last.cpu.get(pid)[key] - first.cpu.get(pid)[key];
    }
    result.roles[role] = {
      cpuPct: round(spent.cpuNs / 1e7 / wallS, 2),
      mInstructionsPerS: round(spent.instructions / 1e6 / wallS),
      pCoreCyclesPct: spent.cycles ? round((100 * spent.pCycles) / spent.cycles) : null,
      footprintMiB: mib(median(samples.map((s) => s.byRole[role]))),
    };
  }
  const appSide = [...new Set(roles.values())].filter((role) => !OWN_ROLES.includes(role));
  result.app = {
    cpuPct: round(appSide.reduce((sum, role) => sum + (result.roles[role]?.cpuPct ?? 0), 0), 2),
    mInstructionsPerS: round(appSide.reduce((sum, role) => sum + (result.roles[role]?.mInstructionsPerS ?? 0), 0)),
    footprintMiB: mib(median(samples.map((s) => appSide.reduce((sum, role) => sum + (s.byRole[role] ?? 0), 0)))),
  };
  return result;
}

function screenWidth() {
  const bounds = execFileSync('osascript', ['-e', 'tell application "Finder" to get bounds of window of desktop'], { encoding: 'utf8' });
  return Number(bounds.split(',')[2]);
}

function loadAverage() {
  return execFileSync('sysctl', ['-n', 'vm.loadavg'], { encoding: 'utf8' }).trim();
}

function readRestSample(file) {
  const text = fs.readFileSync(file, 'utf8');
  const graph = text.split('Call graph:')[1]?.split('Total number in stack')[0] ?? '';
  return Object.fromEntries(Object.entries(REST_SIGNS).map(([name, pattern]) => [name, graph.split('\n').filter((line) => pattern.test(line)).length]));
}

const { options, extras, help } = parseArgs(process.argv.slice(2));
if (help) {
  printHelp();
  process.exit(0);
}

const shell = appShellInTree(options.appPath);
const runner = createScenarioRunner(options, {
  scenarioId: 'PERF-GATES', tier: 'perf', prefix: `perf-gates-${shell}`, allowRealAgents: false,
  metadata: { focus: `The cut-over performance gates on the ${shell} app`, shell, extras },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const probe = usageProbe(runner.runDir);
const dataDir = dataDirForInstance();
const debugDir = path.dirname(client.manifestPath);
const perfJson = path.join(debugDir, 'perf', 'perf.json');
const daemonPid = () => Number(fs.readFileSync(path.join(dataDir, 'attn.pid'), 'utf8').trim());
const type = (shellPane, data) => observer.send({ cmd: 'pty_input', id: shellPane.runtimeId, data, source: 'automation' });

async function spawnShell(desktopId, label, cwd, placement = {}) {
  const id = crypto.randomUUID();
  const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, `${label} spawned`, 20_000);
  observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktopId, ...placement }, label, cols: 100, rows: 30 });
  const result = await spawned;
  if (!result.success) throw new Error(`spawn ${label} failed: ${JSON.stringify(result)}`);
  runner.registerCleanup(`unregister_${label}`, () => observer.unregisterSession(id));
  const runtimeId = await observer.waitFor(() => { try { return observer.terminalOf(id); } catch { return null; } }, `${label} terminal`, 20_000);
  return { sessionId: id, paneId: result.pane_id, runtimeId };
}

async function makeDesks() {
  const cwd = path.join(runner.sessionDir, 'gates');
  fs.mkdirSync(cwd, { recursive: true });
  const slotted = async () => {
    const desktop = await createSlottedDesktop(observer);
    runner.registerCleanup(`close_desktop_${desktop.slot}`, () => closeEmptiedDesktop(observer, desktop.id));
    return desktop;
  };
  const one = await slotted();
  const four = await slotted();
  const desks = { one: { id: one.id, slot: one.slot, shells: [await spawnShell(one.id, 'gate-1', cwd)] }, four: { id: four.id, slot: four.slot, shells: [] } };
  const topLeft = await spawnShell(four.id, 'gate-4a', cwd);
  const topRight = await spawnShell(four.id, 'gate-4b', cwd, { anchor_pane_id: topLeft.paneId, direction: 'vertical' });
  const bottomLeft = await spawnShell(four.id, 'gate-4c', cwd, { anchor_pane_id: topLeft.paneId, direction: 'horizontal' });
  const bottomRight = await spawnShell(four.id, 'gate-4d', cwd, { anchor_pane_id: topRight.paneId, direction: 'horizontal' });
  desks.four.shells = [topLeft, topRight, bottomLeft, bottomRight];
  return desks;
}

async function launch(desk) {
  await client.quitApp();
  fs.rmSync(perfJson, { force: true });
  await observer.setCurrentDesktop(desk.id);
  const before = new Set(probe().keys());
  const startedAt = performance.now();
  await client.launchFreshApp();
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
  if (shell === 'native') {
    await showNativeDesktop(client, observer, desk);
  } else {
    await client.waitForFrontendResponsive(20_000);
    await client.request('dismiss_whats_new', {}).catch(() => {});
    await client.request('select_desktop', { desktopId: desk.id });
  }
  const bounds = await client.request('set_window_bounds', { logicalBounds: { x: screenWidth() - VISIBLE_PX, y: 156, ...VIEW } });
  return { startedAt, pid: client.readManifest().pid, before, bounds };
}

async function firstDrawnTerminal(desk, startedAt) {
  if (shell === 'native') {
    const drawn = desk.shells.map((s) => client.request('state', { of: `terminal:${s.runtimeId}`, includes: MARK, timeout_ms: 30_000 }).then(() => performance.now()));
    return Promise.any(drawn).then((at) => at - startedAt);
  }
  const ids = new Set(desk.shells.map((s) => s.sessionId));
  for (;;) {
    const snapshot = await client.request('capture_perf_snapshot', { settleFrames: 0, includeMemory: false });
    if ((snapshot.terminals ?? []).some((t) => ids.has(t.sessionId) && t.renderCount > 0)) return performance.now() - startedAt;
    if (performance.now() - startedAt > 30_000) throw new Error('no terminal drew within 30 s of launch');
    await sleep(10);
  }
}

async function frameTimes() {
  if (shell !== 'native') return null;
  await client.quitApp();
  const snapshot = JSON.parse(fs.readFileSync(perfJson, 'utf8'));
  const { frames } = snapshot;
  return { total: frames.total, recent: frames.recent_count, workMs: frames.work_ms, uiAllocatedBytes: frames.ui_allocated_bytes, droppedSamples: snapshot.dropped_samples };
}

async function runWorkload(name, desks, index) {
  const workload = WORKLOADS[name];
  const desk = desks[workload.desk];
  const all = [...desks.one.shells, ...desks.four.shells];
  for (const s of all) type(s, '\u0003');
  await sleep(300);
  for (const s of all) type(s, `${MARK_COMMAND}\r`);
  const row = { type: 'run', shell, workload: name, run: index, load: loadAverage() };
  const launched = await launch(desk);
  row.bounds = launched.bounds;
  const roles = () => processRoles(probe, launched, daemonPid());
  if (workload.launch) {
    row.firstTerminalMs = round(await firstDrawnTerminal(desk, launched.startedAt));
    return row;
  }
  await sleep(extras.settle * 1000);
  if (workload.window) {
    row.window = await measureWindow(probe, roles(), extras.seconds);
  } else if (workload.paint) {
    const painters = workload.paint === 'three' ? desk.shells.slice(0, 3) : desk.shells;
    for (const s of painters) type(s, `${workload.paint === 'flood' ? FLOOD_COMMAND : PAINT_COMMAND}\r`);
    await sleep(3000);
    row.window = await measureWindow(probe, roles(), extras.seconds);
    if (extras.profile) row.profile = await sampleApp(launched.pid, 8, path.join(runner.runDir, `${name}-${shell}-${index}.sample.txt`));
    row.frames = await frameTimes();
    for (const s of painters) type(s, '\u0003');
  } else if (workload.switches) {
    const set = roles();
    row.before = await measureWindow(probe, set, 10);
    for (let n = 0; n < workload.switches; n += 1) {
      await observer.setCurrentDesktop(desks.one.id);
      await sleep(2000);
      await observer.setCurrentDesktop(desks.four.id);
      await sleep(3000);
    }
    await sleep(15_000);
    row.after = await measureWindow(probe, set, 10);
    row.growthMiB = round(row.after.app.footprintMiB - row.before.app.footprintMiB);
  } else if (workload.sample) {
    const file = await sampleApp(launched.pid, workload.sample, path.join(runner.runDir, `rest-${shell}-${index}.sample.txt`));
    row.rest = { file, ...readRestSample(file) };
  }
  return row;
}

function sampleApp(pid, seconds, file) {
  return new Promise((resolve, reject) => spawn('sample', [String(pid), String(seconds), '-file', file], { stdio: 'ignore' })
    .on('exit', (code) => (code === 0 ? resolve(file) : reject(new Error(`sample exited ${code}`)))));
}

function summarize(name, rows) {
  const pick = (fn) => {
    const values = rows.map(fn).filter((v) => v != null);
    return values.length ? { median: median(values), min: Math.min(...values), max: Math.max(...values), n: values.length } : null;
  };
  return {
    type: 'summary', shell, workload: name, runs: rows.length,
    cpuPct: pick((r) => r.window?.app.cpuPct),
    mInstructionsPerS: pick((r) => r.window?.app.mInstructionsPerS),
    footprintMiB: pick((r) => r.window?.app.footprintMiB),
    growthMiB: pick((r) => r.growthMiB),
    firstTerminalMs: pick((r) => r.firstTerminalMs),
    frameWorkMs: rows.some((r) => r.frames) ? {
      p50: pick((r) => r.frames?.workMs?.p50), p99: pick((r) => r.frames?.workMs?.p99), max: pick((r) => r.frames?.workMs?.max),
    } : null,
    rest: rows.some((r) => r.rest) ? Object.fromEntries(Object.keys(REST_SIGNS).map((k) => [k, pick((r) => r.rest?.[k])])) : null,
  };
}

try {
  await client.launchFreshApp();
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
  await observer.connect();
  const startedOn = observer.currentDesktopId();
  if (startedOn) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(startedOn));
  const desks = await runner.step('make_desktops', makeDesks);
  const summaries = [];
  const out = fs.createWriteStream(path.join(runner.runDir, 'runs.jsonl'));
  for (const name of extras.workloads) {
    const rows = [];
    for (let index = 0; index < extras.runs; index += 1) {
      const row = await runner.step(`${name}_${index}`, () => runWorkload(name, desks, index));
      console.log(JSON.stringify(row));
      out.write(`${JSON.stringify(row)}\n`);
      rows.push(row);
    }
    const summary = summarize(name, rows);
    console.log(JSON.stringify(summary));
    out.write(`${JSON.stringify(summary)}\n`);
    summaries.push(summary);
  }
  out.end();
  await runner.finishSuccess({ shell, summaries });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
