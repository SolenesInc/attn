#!/usr/bin/env node

import crypto from 'node:crypto';
import os from 'node:os';
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { closeEmptiedDesktop, createSlottedDesktop, showNativeDesktop } from './perfMeasure.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createWindowDriver } from './platform.mjs';
import { daemonPidFilePathForInstance } from './harnessInstance.mjs';

const MEASURES = ['connect', 'idle', 'reconnect', 'echo', 'open', 'upload'];
const FLOOD_COMMAND = "yes 'the quick brown fox jumps over the lazy dog 0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ abcdefgh'";
const MARK = 'RHMMARK';
const UPLOAD_CHUNK = 524_288;
// An idle bridge carries about 400 B/s (measured on oma: 24-26 KB/min with and without the app).
const QUIET_BYTES = 3000;
const PROBE_MS = 250;
const CTRL_C = String.fromCharCode(3);

function parseArgs(argv) {
  const args = argv.filter((arg) => arg !== '--');
  const extras = { freshLink: false, measures: MEASURES, runs: 5, idleSeconds: 60, keys: 30, uploadMiB: 4, wireLog: null, wirePids: null, cwd: null };
  const passthrough = [];
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    const value = () => { if (args[i + 1] === undefined) throw new Error(arg + ' needs a value'); i += 1; return args[i]; };
    if (arg === '--measures') extras.measures = value().split(',');
    else if (arg === '--runs') extras.runs = Number(value());
    else if (arg === '--idle-seconds') extras.idleSeconds = Number(value());
    else if (arg === '--keys') extras.keys = Number(value());
    else if (arg === '--upload-mib') extras.uploadMiB = Number(value());
    else if (arg === '--wire-log') extras.wireLog = value();
    else if (arg === '--wire-pids') extras.wirePids = value();
    else if (arg === '--cwd') extras.cwd = value();
    else if (arg === '--fresh-link') extras.freshLink = true;
    else passthrough.push(arg);
  }
  for (const m of extras.measures) if (!MEASURES.includes(m)) throw new Error('--measures takes ' + MEASURES.join(',') + '; got ' + m);
  return { options: parseCommonArgs(passthrough), extras, help: args.includes('--help') || args.includes('-h') };
}

function printHelp() {
  printCommonHelp('scripts/real-app-harness/remote-home-traffic.mjs');
  console.log(`
Measures the app on a machine whose daemon dials a home on another machine, over
the real link. Run it against an instance whose daemon reaches its home through
an ssh whose ProxyCommand is wirecount (go run ./scripts/wirecount prints how),
which logs the bytes on the wire. Start the harness with wirecount's environment:
the harness restarts the daemon, and the daemon inherits it.

  --wire-log F     wirecount's JSON lines (cumulative up/down per connection)
  --wire-pids D    wirecount's pid files; a reconnect run signals them to cut the link
  --cwd P          a directory on the home for the shells (the Cmd+P root)
  --fresh-link     cut the link once before measuring, so the ssh master restarts with
                   the options of the daemon the harness started (such as compression)
  --measures a,b   connect,idle,reconnect,echo,open,upload (default all)
      connect    a fresh app launch: ms to a ready app, ms to the first home terminal
                 drawn, and the bytes on the wire until it goes quiet for 3 s
      idle       bytes on the wire over --idle-seconds with the app open and idle, and
                 with no app at all
      reconnect  the link cut under an open app: ms until a key typed in a home
                 terminal echoes again, probed every 250 ms, and the bytes it cost
      echo       key-to-paint in a home terminal and in a terminal on this machine,
                 while a third terminal, on the home, is quiet and then floods with yes
      open       Cmd+P on a home terminal: ms until a file of --cwd is listed
      upload     an attachment of --upload-mib MiB in the app's 512 KiB chunks: ms and bytes
  --runs N         runs of each measure (default 5)
  --idle-seconds S (default 60)  --keys N (default 30)  --upload-mib M (default 4)

Prints one {"type":"run"} JSON line per run and one {"type":"summary"} line per
number with its median, min, max and run count.`);
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const round = (v, d = 1) => (v == null ? null : Number(v.toFixed(d)));
const median = (vs) => { const s = vs.filter((v) => v != null).sort((a, b) => a - b); return s.length ? s[Math.floor(s.length / 2)] : null; };
const pct = (vs, p) => { const s = [...vs].sort((a, b) => a - b); return s.length ? s[Math.min(s.length - 1, Math.floor(s.length * p))] : null; };

function wire(log) {
  const last = new Map();
  for (const line of fs.readFileSync(log, 'utf8').split('\n')) {
    if (!line) continue;
    const r = JSON.parse(line);
    last.set(r.pid, r);
  }
  let up = 0;
  let down = 0;
  for (const r of last.values()) { up += r.up; down += r.down; }
  return { up, down, total: up + down };
}
async function wireNow(log) { await sleep(300); return wire(log); }
const delta = (a, b) => ({ up: b.up - a.up, down: b.down - a.down, total: b.total - a.total });

async function waitQuiet(log, quietMs, maxMs) {
  const start = performance.now();
  const samples = [{ at: start, total: wire(log).total }];
  while (performance.now() - start < maxMs) {
    await sleep(250);
    const now = { at: performance.now(), total: wire(log).total };
    samples.push(now);
    const from = samples.find((x) => now.at - x.at <= quietMs);
    if (now.at - samples[0].at >= quietMs && now.total - from.total <= QUIET_BYTES) return { quietAt: from.at - start };
  }
  return { quietAt: null };
}

const { options, extras, help } = parseArgs(process.argv.slice(2));
if (help) { printHelp(); process.exit(0); }
if (!extras.wireLog || !extras.cwd) throw new Error('--wire-log and --cwd are required');

const runner = createScenarioRunner(options, {
  scenarioId: 'REMOTE-HOME-TRAFFIC', tier: 'perf', prefix: 'remote-home-traffic', allowRealAgents: false,
  metadata: { focus: 'The app against a home on another machine, over the real link', extras },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl, trustedApp: true });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
const driver = await createWindowDriver({ appPath: options.appPath, client });
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));
runner.registerFailureEvidence('native_tree', async () => fs.writeFileSync(path.join(runner.runDir, 'failure-tree.json'), JSON.stringify(await driver.tree?.() ?? null)));
const type = (shell, data) => observer.send({ cmd: 'pty_input', id: shell.runtimeId, data, source: 'automation' });
const terminal = (shell) => 'terminal:' + shell.runtimeId;

async function spawnShell(desktopId, label, placement = {}, where = {}) {
  const id = crypto.randomUUID();
  const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, label + ' spawned', 30_000);
  observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd: where.cwd ?? extras.cwd, ...(where.host ? { host: where.host } : {}), profile_id: observer.profileId, placement: { desktop_id: desktopId, ...placement }, label, cols: 100, rows: 30 });
  const result = await spawned;
  if (!result.success) throw new Error('spawn ' + label + ' failed: ' + JSON.stringify(result));
  runner.registerCleanup('unregister_' + label, async () => { await observer.connect(); observer.unregisterSession(id); });
  const runtimeId = await observer.waitFor(() => observer.terminalOf(id), label + ' terminal', 30_000);
  return { sessionId: id, paneId: result.pane_id, runtimeId };
}

async function launchApp(desk) {
  await client.launchFreshApp();
  await client.waitForManifest(30_000);
  await client.waitForReady(30_000);
  await observer.connect();
  await showNativeDesktop(client, observer, desk);
  await park();
}

function screenWidth() {
  const bounds = execFileSync('osascript', ['-e', 'tell application "Finder" to get bounds of window of desktop'], { encoding: 'utf8' });
  return Number(bounds.split(',')[2]);
}

async function park() {
  await client.request('set_window_bounds', { logicalBounds: { x: screenWidth() - 20, y: 156, width: 1710, height: 956 } });
}

async function connection() {
  return String(await client.request('state', { of: 'connection' }));
}

async function echoOnce(shell, timeoutMs = 10_000) {
  const token = 'k' + crypto.randomBytes(3).toString('hex');
  const at = performance.now();
  await driver.typeInto(null, token);
  try {
    await client.request('state', { of: terminal(shell), includes: token, timeout_ms: timeoutMs });
  } catch (error) {
    await driver.pressKey('Enter');
    throw error;
  }
  const ms = performance.now() - at;
  await driver.pressKey('Enter');
  const deadline = performance.now() + 10_000;
  for (;;) {
    const rowsNow = await client.request('state', { of: terminal(shell) });
    if (rowsNow.filter((row) => row.includes(token)).length >= 2) break;
    if (performance.now() > deadline) throw new Error('cat never echoed the line ' + token);
    await sleep(20);
  }
  return ms;
}

async function focus(shell) {
  await driver.click({ key: 'terminal.' + shell.runtimeId });
}

const rows = [];
const out = fs.createWriteStream(path.join(runner.runDir, 'runs.jsonl'));
function emit(row) { console.log(JSON.stringify(row)); out.write(JSON.stringify(row) + '\n'); rows.push(row); }

async function measureConnect(desk, quiet, run) {
  type(quiet, MARK + '\r');
  await sleep(500);
  await client.quitApp();
  await sleep(3000);
  const before = await wireNow(extras.wireLog);
  const startedAt = performance.now();
  await client.launchFreshApp();
  await client.waitForManifest(30_000);
  await client.waitForReady(30_000);
  const readyMs = performance.now() - startedAt;
  await client.request('key', { chord: `${process.platform === 'darwin' ? 'mod' : 'mod+shift'}+${desk.slot}` });
  await client.request('state', { of: terminal(quiet), includes: MARK, timeout_ms: 30_000 });
  const firstTerminalMs = performance.now() - startedAt;
  const { quietAt } = await waitQuiet(extras.wireLog, 3000, 60_000);
  const bytes = delta(before, wire(extras.wireLog));
  await park();
  emit({ type: 'run', measure: 'connect', run, readyMs: round(readyMs), firstTerminalMs: round(firstTerminalMs), quietAfterPaintMs: round(quietAt), ...bytes });
}

async function measureIdle(desk, run) {
  await observer.close();
  await sleep(5000);
  const a = await wireNow(extras.wireLog);
  await sleep(extras.idleSeconds * 1000);
  const b = await wireNow(extras.wireLog);
  const withApp = delta(a, b);
  await client.quitApp();
  await sleep(5000);
  const c = await wireNow(extras.wireLog);
  await sleep(extras.idleSeconds * 1000);
  const d = await wireNow(extras.wireLog);
  const noApp = delta(c, d);
  const perMin = (x) => round((x.total * 60) / extras.idleSeconds, 0);
  emit({ type: 'run', measure: 'idle', run, seconds: extras.idleSeconds, appBytesPerMin: perMin(withApp), noAppBytesPerMin: perMin(noApp), appUp: withApp.up, appDown: withApp.down });
  await launchApp(desk);
}

async function cutLink() {
  const pids = fs.readdirSync(extras.wirePids).map(Number).filter(Boolean);
  let cut = 0;
  for (const pid of pids) {
    try { process.kill(pid, 'SIGUSR1'); cut += 1; } catch (error) { if (error.code !== 'ESRCH') throw error; fs.rmSync(path.join(extras.wirePids, String(pid)), { force: true }); }
  }
  if (cut === 0) throw new Error('no live wirecount connection to cut');
}

async function untilUsable(quiet, since) {
  const states = [];
  while (performance.now() - since < 120_000) {
    const state = await connection();
    if (states.at(-1)?.state !== state) states.push({ state, atMs: round(performance.now() - since) });
    try {
      const echoMs = await echoOnce(quiet, PROBE_MS);
      return { usableMs: round(performance.now() - since), lastEchoMs: round(echoMs), states };
    } catch {}
  }
  throw new Error('a home terminal never echoed within 120 s of the cut');
}

async function measureReconnect(quiet, run) {
  await focus(quiet);
  await echoOnce(quiet);
  await observer.close();
  const before = await wireNow(extras.wireLog);
  await cutLink();
  const usable = await untilUsable(quiet, performance.now());
  await waitQuiet(extras.wireLog, 3000, 60_000);
  const bytes = delta(before, wire(extras.wireLog));
  if (run >= 0) emit({ type: 'run', measure: 'reconnect', run, probeMs: PROBE_MS, ...usable, ...bytes });
  await observer.connect();
}

async function measureEcho(shells, flood, run) {
  for (const load of ['quiet', 'flood']) {
    if (load === 'flood') type(flood, FLOOD_COMMAND + '\r');
    await sleep(load === 'flood' ? 3000 : 500);
    for (const [place, shell] of Object.entries(shells)) {
      await focus(shell);
      const times = [];
      const before = wire(extras.wireLog);
      const startedAt = performance.now();
      for (let k = 0; k < extras.keys; k += 1) times.push(await echoOnce(shell, 30_000));
      const wireKBps = round(delta(before, wire(extras.wireLog)).down / 1024 / ((performance.now() - startedAt) / 1000), 0);
      emit({ type: 'run', measure: 'echo', load, place, run, keys: times.length, p50Ms: round(pct(times, 0.5)), p90Ms: round(pct(times, 0.9)), maxMs: round(Math.max(...times)), wireKBps });
    }
    if (load === 'flood') { type(flood, CTRL_C); await sleep(2000); type(flood, 'clear\r'); }
  }
}

async function measureOpen(quiet, run) {
  await focus(quiet);
  const before = await wireNow(extras.wireLog);
  const at = performance.now();
  await driver.run('file.open');
  await driver.waitFor({ key: 'opener.field' });
  const dialogMs = performance.now() - at;
  await driver.typeInto({ key: 'opener.field' }, 'glossary');
  await driver.waitFor({ key: 'opener.row.' + extras.cwd + '/docs/internal/glossary.md' }, { timeoutMs: 30_000 });
  const listedMs = performance.now() - at;
  const bytes = delta(before, await wireNow(extras.wireLog));
  await driver.pressKey('Escape');
  emit({ type: 'run', measure: 'open', run, dialogMs: round(dialogMs), listedMs: round(listedMs), ...bytes });
}

async function measureUpload(run) {
  const size = extras.uploadMiB * 1048576;
  const data = crypto.randomBytes(size);
  const id = crypto.randomUUID();
  const owner = { kind: 'quick_capture', id: crypto.randomUUID() };
  const before = await wireNow(extras.wireLog);
  const at = performance.now();
  for (let offset = 0; offset < size; offset += UPLOAD_CHUNK) {
    const chunk = data.subarray(offset, offset + UPLOAD_CHUNK);
    const requestId = crypto.randomUUID();
    const reply = observer.waitForMessage((m) => m.request_id === requestId && m, 'attachment_put result', 120_000);
    observer.send({ cmd: 'attachment_put', request_id: requestId, profile_id: observer.profileId, owner, attachment_id: id, name: 'measure.bin', offset, data_base64: chunk.toString('base64'), final: offset + UPLOAD_CHUNK >= size });
    const result = await reply;
    if (result.success === false) throw new Error('attachment_put failed: ' + JSON.stringify(result).slice(0, 300));
  }
  const ms = performance.now() - at;
  const bytes = delta(before, await wireNow(extras.wireLog));
  emit({ type: 'run', measure: 'upload', run, mib: extras.uploadMiB, ms: round(ms), mibPerS: round(extras.uploadMiB / (ms / 1000), 2), ...bytes });
}

function summarize() {
  const groups = new Map();
  for (const r of rows.filter((x) => x.type === 'run')) {
    const key = [r.measure, r.place, r.load].filter(Boolean).join(':');
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(r);
  }
  for (const [key, rs] of groups) {
    const fields = Object.keys(rs[0]).filter((f) => typeof rs[0][f] === 'number' && !['run', 'seconds', 'keys', 'mib'].includes(f));
    const summary = { type: 'summary', measure: key, runs: rs.length };
    for (const f of fields) {
      const vs = rs.map((r) => r[f]).filter((v) => v != null);
      summary[f] = { median: median(vs), min: Math.min(...vs), max: Math.max(...vs), n: vs.length };
    }
    console.log(JSON.stringify(summary));
    out.write(JSON.stringify(summary) + '\n');
  }
}

try {
  await client.launchFreshApp();
  await client.waitForManifest(30_000);
  await client.waitForReady(30_000);
  await observer.connect();
  const daemonEnv = execFileSync('ps', ['eww', '-o', 'command=', '-p', fs.readFileSync(daemonPidFilePathForInstance(), 'utf8').trim()], { encoding: 'utf8' });
  if (!daemonEnv.includes('WIRECOUNT_LOG=')) throw new Error('the daemon the app started does not count its wire: start the harness with wirecount\'s environment');
  emit({ type: 'link', sshExtra: (daemonEnv.match(/RHM_SSH_EXTRA=(\S+(?: \S+)?)/) || [])[1] ?? null });
  const desk = await createSlottedDesktop(observer);
  runner.registerCleanup('close_desktop', async () => { await observer.connect(); await closeEmptiedDesktop(observer, desk.id); });
  const quiet = await spawnShell(desk.id, 'rhm-quiet');
  const flood = await spawnShell(desk.id, 'rhm-flood', { anchor_pane_id: quiet.paneId, direction: 'vertical' });
  const daemonId = fs.readFileSync(path.join(path.dirname(daemonPidFilePathForInstance()), 'daemon-id'), 'utf8').trim();
  const thisMachine = observer.outposts.find((row) => row.daemon_id === daemonId);
  if (!thisMachine) throw new Error('the home lists no machine with daemon id ' + daemonId);
  const local = await spawnShell(desk.id, 'rhm-local', { anchor_pane_id: quiet.paneId, direction: 'horizontal' }, { host: thisMachine.id, cwd: os.homedir() });
  type(quiet, 'cat\r');
  type(local, 'cat\r');
  await observer.setCurrentDesktop(desk.id);
  await showNativeDesktop(client, observer, desk);
  await park();
  if (extras.freshLink) await measureReconnect(quiet, -1);
  for (let run = 0; run < extras.runs; run += 1) {
    for (const m of extras.measures) {
      await runner.step(m + '_' + run, async () => {
        if (m === 'connect') await measureConnect(desk, quiet, run);
        else if (m === 'idle') await measureIdle(desk, run);
        else if (m === 'reconnect') await measureReconnect(quiet, run);
        else if (m === 'echo') await measureEcho({ home: quiet, machine: local }, flood, run);
        else if (m === 'open') await measureOpen(quiet, run);
        else if (m === 'upload') await measureUpload(run);
      });
    }
  }
  summarize();
  out.end();
  await runner.finishSuccess({});
} catch (error) {
  summarize();
  await runner.finishFailure(error);
  process.exitCode = 1;
}
