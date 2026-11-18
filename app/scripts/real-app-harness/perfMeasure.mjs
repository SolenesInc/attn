import crypto from 'node:crypto';
import fs from 'node:fs';
import { execFile, spawn } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';
import {
  daemonPidFilePathForInstance,
  defaultAppPathForInstance,
} from './harnessInstance.mjs';
import { createSessionAndWaitForInitialPane } from './common.mjs';
import { resetInstanceData } from './freshWorld.mjs';
import { appShellInTree } from './platform.mjs';

const execFileAsync = promisify(execFile);
export const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export async function readProcessTable() {
  const { stdout } = await execFileAsync('ps', ['-axo', 'pid=,ppid=,%cpu=,rss=,comm=,command='], { maxBuffer: Infinity });
  return stdout
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const match = line.match(/^(\d+)\s+(\d+)\s+([\d.]+)\s+(\d+)\s+(\S+)\s+(.*)$/);
      if (!match) return null;
      return {
        pid: Number(match[1]),
        ppid: Number(match[2]),
        cpuPct: Number(match[3]),
        rssKb: Number(match[4]),
        comm: match[5],
        command: match[6],
      };
    })
    .filter(Boolean);
}

export function collectDescendantPids(processes, rootPid) {
  const childrenByParent = new Map();
  for (const proc of processes) {
    const siblings = childrenByParent.get(proc.ppid) || [];
    siblings.push(proc.pid);
    childrenByParent.set(proc.ppid, siblings);
  }
  const visited = new Set([rootPid]);
  const queue = [rootPid];
  while (queue.length > 0) {
    const pid = queue.shift();
    for (const childPid of childrenByParent.get(pid) || []) {
      if (visited.has(childPid)) continue;
      visited.add(childPid);
      queue.push(childPid);
    }
  }
  return visited;
}

// WebKit content/GPU/networking processes are reparented to launchd, so they
// are NOT descendants of the app pid and must be matched by command.
export function isRelevantWebKitProcess(proc) {
  return proc.command.includes('com.apple.WebKit.WebContent')
    || proc.command.includes('com.apple.WebKit.Networking')
    || proc.command.includes('com.apple.WebKit.GPU');
}

export async function captureWebKitPids() {
  const table = await readProcessTable();
  return new Set(table.filter(isRelevantWebKitProcess).map((proc) => proc.pid));
}

export function classify(proc) {
  const command = proc.command;
  if (command.includes('pty-worker')) return 'pty_worker';
  if (/attnd? daemon/.test(command)) return 'daemon';
  if (command.includes('/Contents/MacOS/app')) return 'app';
  if (command.includes('com.apple.WebKit.WebContent')) return 'webkit_webcontent';
  if (command.includes('com.apple.WebKit.Networking')) return 'webkit_networking';
  if (command.includes('com.apple.WebKit.GPU')) return 'webkit_gpu';
  if (/\/(fish|zsh|bash|dash|tcsh|ksh)( |$)|\/sh( |$)/.test(command)) return 'shell';
  return proc.comm;
}

// Rooted at these pids rather than matched by command: a prod daemon shares the
// `attn daemon` command string.
export async function snapshot(appPid, daemonPid, webkitBaseline = new Set(), extraPids = []) {
  const table = await readProcessTable();
  const pidSet = new Set();
  for (const pid of collectDescendantPids(table, appPid)) pidSet.add(pid);
  if (daemonPid) for (const pid of collectDescendantPids(table, daemonPid)) pidSet.add(pid);
  for (const proc of table) {
    if (isRelevantWebKitProcess(proc) && !webkitBaseline.has(proc.pid)) pidSet.add(proc.pid);
  }
  for (const pid of extraPids) pidSet.add(pid);

  const procs = table.filter((proc) => pidSet.has(proc.pid));
  const byClass = {};
  let totalRssKb = 0;
  for (const proc of procs) {
    const label = classify(proc);
    const entry = byClass[label] || { count: 0, rssKb: 0, rssMaxKb: 0, pids: [] };
    entry.count += 1;
    entry.rssKb += proc.rssKb;
    entry.rssMaxKb = Math.max(entry.rssMaxKb, proc.rssKb);
    entry.pids.push({ pid: proc.pid, rssKb: proc.rssKb });
    byClass[label] = entry;
    totalRssKb += proc.rssKb;
  }
  return {
    totalRssMb: Number((totalRssKb / 1024).toFixed(1)),
    procCount: procs.length,
    byClass: Object.fromEntries(
      Object.entries(byClass).map(([label, entry]) => [label, {
        count: entry.count,
        rssMb: Number((entry.rssKb / 1024).toFixed(1)),
        rssMaxMb: Number((entry.rssMaxKb / 1024).toFixed(1)),
        pids: entry.pids,
      }]),
    ),
  };
}

export function classRssMb(snap, label) {
  return snap?.byClass?.[label]?.rssMb ?? 0;
}

const REGION_SLICES = {
  graphics: ['owned unmapped (graphics)', 'VM_ALLOCATE (graphics)'],
  webkitMalloc: ['WebKit Malloc', 'WebKit Malloc metadata'],
  jsHeap: ['JS VM Gigacage', 'JS JIT generated code'],
  malloc: ['MALLOC_LARGE', 'MALLOC_SMALL', 'MALLOC_TINY'],
};

function parseVmmapSize(token) {
  const match = /^([\d.]+)([KMGT])?$/.exec(token);
  if (!match) return null;
  const value = Number(match[1]);
  if (!Number.isFinite(value)) return null;
  const scale = { K: 1 / 1024, M: 1, G: 1024, T: 1024 * 1024 };
  return match[2] ? value * scale[match[2]] : value / (1024 * 1024);
}

// Neither edge anchors the 7 size columns: a name can end in a bare number
// ("Memory Tag 241") and a row can carry trailing prose.
function findSizeWindow(columns) {
  for (let start = 1; start + 7 < columns.length + 1; start += 1) {
    if (!/^\d+$/.test(columns[start + 7] ?? '')) continue;
    const sizes = columns.slice(start, start + 7).map(parseVmmapSize);
    if (sizes.length === 7 && sizes.every((size) => size !== null)) return start;
  }
  return -1;
}

export function parseVmmapSummary(text) {
  const byRegion = {};
  // Unrounded: rounding each region before summing accumulates error in a slice.
  const exactDirtyMb = {};
  let totalDirtyMb = 0;
  for (const rawLine of text.split('\n')) {
    const line = rawLine.trimEnd();
    if (/^\s*MALLOC ZONE/.test(line)) break;
    if (!line || /^[=\s]+$/.test(line) || line.startsWith('REGION TYPE')) continue;
    const columns = line.trim().split(/\s+/);
    if (columns.length < 9) continue;
    const start = findSizeWindow(columns);
    if (start < 0) continue;
    const sizes = columns.slice(start, start + 7).map(parseVmmapSize);
    const name = columns.slice(0, start).join(' ');
    if (!name || name.startsWith('TOTAL')) continue;
    const [residentMb, dirtyMb] = [sizes[1], sizes[2]];
    byRegion[name] = {
      residentMb: Number(residentMb.toFixed(1)),
      dirtyMb: Number(dirtyMb.toFixed(1)),
    };
    exactDirtyMb[name] = dirtyMb;
    totalDirtyMb += dirtyMb;
  }

  const slices = {};
  for (const [slice, names] of Object.entries(REGION_SLICES)) {
    slices[slice] = Number(
      names.reduce((sum, name) => sum + (exactDirtyMb[name] ?? 0), 0).toFixed(1),
    );
  }
  return {
    byRegion,
    slices,
    totalDirtyMb: Number(totalDirtyMb.toFixed(1)),
    footprintMb: parseFootprint(text),
  };
}

// Physical footprint is the only number counting `owned unmapped (graphics)`:
// those pages are mapped in another process, so `ps` RSS cannot see them.
export function parseFootprint(text) {
  const match = /^Physical footprint:\s+(\S+)/m.exec(text);
  if (!match) return null;
  const mb = parseVmmapSize(match[1]);
  return mb === null ? null : Number(mb.toFixed(1));
}

export const APP_PROCESS_CLASSES = ['app', 'webkit_webcontent', 'webkit_gpu', 'webkit_networking'];

export function appPids(snap) {
  return APP_PROCESS_CLASSES.flatMap((label) => (snap?.byClass?.[label]?.pids ?? []).map((entry) => entry.pid));
}

export async function readAppFootprint(snap) {
  const byPid = {};
  let totalMb = 0;
  let missing = 0;
  for (const label of APP_PROCESS_CLASSES) {
    for (const entry of snap?.byClass?.[label]?.pids ?? []) {
      const regions = await readRegionFootprint(entry.pid);
      const mb = regions?.footprintMb ?? null;
      byPid[entry.pid] = { label, footprintMb: mb };
      if (mb === null) missing += 1;
      else totalMb += mb;
    }
  }
  return { totalMb: Number(totalMb.toFixed(1)), missing, byPid };
}

// Measured at a 1710x1073 window: full-width surfaces run ~22-30 MB, WebKit's
// 512x512@2x compositing tiles ~4 MB, so 10 MB separates them cleanly.
const LARGE_GRAPHICS_SURFACE_MB = 10;

export function parseGraphicsRegions(text) {
  const surfaces = [];
  for (const line of text.split('\n')) {
    if (!line.includes('owned unmapped (graphics)')) continue;
    const match = /\[\s*(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s*\]/.exec(line);
    if (!match) continue;
    const virtualMb = parseVmmapSize(match[1]);
    const dirtyMb = parseVmmapSize(match[3]);
    if (virtualMb === null || dirtyMb === null) continue;
    surfaces.push({ virtualMb, dirtyMb });
  }
  const large = surfaces.filter((surface) => surface.virtualMb >= LARGE_GRAPHICS_SURFACE_MB);
  const histogram = {};
  for (const surface of large) {
    const key = surface.virtualMb.toFixed(1);
    histogram[key] = (histogram[key] ?? 0) + 1;
  }
  return {
    regionCount: surfaces.length,
    largeCount: large.length,
    largeDirtyMb: Number(large.reduce((sum, s) => sum + s.dirtyMb, 0).toFixed(1)),
    largeVirtualMb: Number(large.reduce((sum, s) => sum + s.virtualMb, 0).toFixed(1)),
    histogram,
  };
}

export async function readGraphicsRegions(pid) {
  if (!pid) return null;
  try {
    const { stdout } = await execFileAsync('vmmap', [String(pid)], { maxBuffer: 128 * 1024 * 1024 });
    return parseGraphicsRegions(stdout);
  } catch {
    return null;
  }
}

export async function readRegionFootprint(pid) {
  if (!pid) return null;
  try {
    const { stdout } = await execFileAsync('vmmap', ['--summary', String(pid)], {
      maxBuffer: 32 * 1024 * 1024,
    });
    return parseVmmapSummary(stdout);
  } catch {
    return null;
  }
}

export async function sampleWindow(appPid, daemonPid, webkitBaseline, windowMs, intervalMs = 1000) {
  const samples = [];
  const deadline = Date.now() + windowMs;
  while (Date.now() < deadline) {
    samples.push(await snapshot(appPid, daemonPid, webkitBaseline));
    await delay(intervalMs);
  }
  if (samples.length === 0) samples.push(await snapshot(appPid, daemonPid, webkitBaseline));
  const peak = samples.reduce((best, current) => (current.totalRssMb > best.totalRssMb ? current : best), samples[0]);
  return { peak, last: samples[samples.length - 1], count: samples.length };
}

export function readLivePidFile(pidPath) {
  let pid = null;
  try {
    pid = Number(fs.readFileSync(pidPath, 'utf8').trim());
  } catch {
    return null;
  }
  if (!Number.isInteger(pid) || pid <= 0) return null;
  try { process.kill(pid, 0); } catch { return null; }
  return pid;
}

export function readLiveDaemonPid(instance) {
  return readLivePidFile(daemonPidFilePathForInstance(instance));
}

export async function stopDaemon(instance) {
  const pid = readLiveDaemonPid(instance);
  if (pid == null) return null;
  try { process.kill(pid, 'SIGTERM'); } catch { return null; }
  for (let i = 0; i < 50; i += 1) {
    try { process.kill(pid, 0); } catch { return pid; }
    await delay(200);
  }
  try { process.kill(pid, 'SIGKILL'); } catch {}
  return pid;
}

const HOLD_ENSURE_LOCK = 'use Fcntl qw(:flock); $| = 1; open(my $lock, ">>", $ARGV[0]) or die "$!\\n"; flock($lock, LOCK_EX) or die "$!\\n"; print "held\\n"; <STDIN>;';

async function holdEnsureLock(instance) {
  const holder = spawn('perl', ['-e', HOLD_ENSURE_LOCK, `${daemonPidFilePathForInstance(instance)}.ensure`], { stdio: ['pipe', 'pipe', 'inherit'] });
  const exited = once(holder, 'exit');
  const [held] = await Promise.race([once(holder.stdout, 'data'), exited]);
  if (String(held).trim() !== 'held') throw new Error(`could not hold the daemon ensure lock (exit ${held})`);
  return async () => { holder.stdin.end(); await exited; };
}

export async function stopDaemonHeldDown(instance) {
  const release = await holdEnsureLock(instance);
  try {
    return { pid: await stopDaemon(instance), release };
  } catch (error) {
    await release();
    throw error;
  }
}

export async function stopDaemonKeepingTerminals(instance) {
  if (readLiveDaemonPid(instance) == null) return null;
  return stopDaemon(instance);
}

export async function teardownInstanceState({ client, instance, wipe = true }) {
  if (!instance || instance === 'default') {
    throw new Error(`teardownInstanceState refuses an empty/prod instance (got ${JSON.stringify(instance)})`);
  }
  await client.quitApp();
  await stopDaemon(instance);
  if (wipe) {
    resetInstanceData({ instance, appPath: defaultAppPathForInstance(instance) });
  }
}

export async function paneIdForSession(client, sessionId) {
  const ws = await client.request('get_desktop', { sessionId }, { timeoutMs: 10_000 });
  return ws.activePaneId || ws.panes?.[0]?.paneId || null;
}

export async function createSlottedDesktop(observer) {
  const desktop = await observer.createDesktop();
  if (!desktop.shortcut_slot) throw new Error(`desktop ${desktop.id} has no shortcut slot: all nine are taken`);
  return { id: desktop.id, slot: desktop.shortcut_slot };
}

export async function closeEmptiedDesktop(observer, id) {
  if (!observer.desktop(id)) return;
  const left = await observer.waitFor(() => observer.desktop(id)?.panes.length === 0 && observer.desktop(id), `desktop ${id} emptied`);
  await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
}

export async function showNativeDesktop(client, observer, desktop) {
  const { layer } = await client.request('state', { of: 'window' });
  if (layer === 'WhatsNew') {
    await client.request('key', { chord: 'Escape' });
    await client.request('wait', { target: { key: 'whats-new-dialog' }, gone: true });
  }
  const deadline = Date.now() + 10_000;
  while (!(await client.request('state', { of: 'desktops' })).some((known) => known.id === desktop.id)) {
    if (Date.now() > deadline) throw new Error(`the app never listed desktop ${desktop.id}`);
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  const appCurrent = async () => (await client.request('state', { of: 'window' })).current_desktop;
  if (observer.currentDesktopId() === desktop.id) {
    const other = observer.desktops.find((candidate) => candidate.id !== desktop.id);
    await observer.setCurrentDesktop(other.id);
    while ((await appCurrent()) !== other.id) {
      if (Date.now() > deadline) throw new Error(`the app never followed the current desktop to ${other.id}`);
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  await client.request('key', { chord: `${process.platform === 'darwin' ? 'mod' : 'mod+shift'}+${desktop.slot}` });
  await observer.waitFor(() => observer.currentDesktopId() === desktop.id, `desktop ${desktop.id} current`);
}

export async function openPerfShell({ client, observer, cwd, label }) {
  if (appShellInTree(client.appPath) !== 'native') {
    return createSessionAndWaitForInitialPane({
      client, observer, cwd, label, agent: 'shell', sessionWaitMs: 30_000, waitForInitialPaneVisible: true, initialPaneWaitMs: 25_000,
    });
  }
  const desktop = await perfDesktop(client, observer);
  const id = crypto.randomUUID();
  const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, `${label} spawned`, 30_000);
  observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label, cols: 100, rows: 30 });
  const result = await spawned;
  if (!result.success) throw new Error(`spawn ${label} failed: ${JSON.stringify(result)}`);
  const runtimeId = await observer.waitFor(() => { try { return observer.terminalOf(id); } catch { return null; } }, `${label} terminal`, 25_000);
  await client.request('state', { of: `terminal:${runtimeId}`, includes: '', timeout_ms: 25_000 }, { timeoutMs: 30_000 });
  return id;
}

export async function closeSessions(client, ids, observer) {
  const closing = [...new Set(ids)];
  if (appShellInTree(client.appPath) !== 'native') {
    await Promise.allSettled(closing.map((sessionId) => client.request('close_session', { sessionId }, { timeoutMs: 15_000 })));
    return;
  }
  for (const sessionId of closing) observer.unregisterSession(sessionId);
  const desktop = perfDesktops.get(observer);
  if (!desktop) return;
  await observer.waitFor(() => !observer.desktop(desktop.id)?.panes.some((pane) => closing.includes(pane.session_id)), 'closed sessions leave the perf desktop');
  if (observer.desktop(desktop.id)?.panes.length) return;
  perfDesktops.delete(observer);
  await closeEmptiedDesktop(observer, desktop.id);
}

const perfDesktops = new WeakMap();

async function perfDesktop(client, observer) {
  const known = perfDesktops.get(observer);
  if (known && observer.desktop(known.id)) return known;
  const desktop = await createSlottedDesktop(observer);
  await showNativeDesktop(client, observer, desktop);
  perfDesktops.set(observer, desktop);
  return desktop;
}

// Sequential with a per-pane settle: filling every pane at once overruns the
// websocket's 256-message buffer (see AGENTS.md).
export async function fillAllPanes(client, sessionIds, cmd, perPaneSettleMs, observer) {
  let filled = 0;
  for (const sessionId of sessionIds) {
    if (appShellInTree(client.appPath) === 'native') {
      observer.send({ cmd: 'pty_input', id: observer.terminalOf(sessionId), data: `${cmd}\r`, source: 'automation' });
      filled += 1;
      await delay(perPaneSettleMs);
      continue;
    }
    const paneId = await paneIdForSession(client, sessionId);
    if (!paneId) {
      console.warn(`[perf] fill: no pane for session ${sessionId}`);
      continue;
    }
    await client.request('write_pane', { sessionId, paneId, text: cmd }, { timeoutMs: 30_000 })
      .catch((error) => console.warn(`[perf] fill write_pane ${sessionId} failed: ${error.message}`));
    filled += 1;
    await delay(perPaneSettleMs);
  }
  console.log(`[perf] filled ${filled}/${sessionIds.length} panes with \`${cmd}\``);
}
