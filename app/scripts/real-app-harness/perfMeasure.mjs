import fs from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { daemonPidFilePathForProfile, dataDirForProfile } from './harnessProfile.mjs';

const execFileAsync = promisify(execFile);
export const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export async function readProcessTable() {
  const { stdout } = await execFileAsync('ps', ['-axo', 'pid=,ppid=,%cpu=,rss=,comm=,command=']);
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
  if (command.includes('attn daemon')) return 'daemon';
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
    if (!line || /^[=\s]+$/.test(line) || /^REGION TYPE/.test(line)) continue;
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

export function readLiveDaemonPid(profile) {
  let pid = null;
  try {
    pid = Number(fs.readFileSync(daemonPidFilePathForProfile(profile), 'utf8').trim());
  } catch {
    return null;
  }
  if (!Number.isInteger(pid) || pid <= 0) return null;
  try { process.kill(pid, 0); } catch { return null; }
  return pid;
}

export async function stopDaemon(profile) {
  const pid = readLiveDaemonPid(profile);
  if (pid == null) return null;
  try { process.kill(pid, 'SIGTERM'); } catch { return null; }
  for (let i = 0; i < 50; i += 1) {
    try { process.kill(pid, 0); } catch { return pid; }
    await delay(200);
  }
  try { process.kill(pid, 'SIGKILL'); } catch {}
  return pid;
}

export async function teardownProfileState({ client, profile, wipe = true }) {
  if (!profile || profile === 'default') {
    throw new Error(`teardownProfileState refuses an empty/prod profile (got ${JSON.stringify(profile)})`);
  }
  const dataDir = dataDirForProfile(profile);
  if (dataDir === dataDirForProfile('')) {
    throw new Error(`teardownProfileState refuses to wipe the prod data dir ${dataDir}`);
  }
  await client.quitApp();
  await stopDaemon(profile);
  if (wipe) {
    try { fs.rmSync(dataDir, { recursive: true, force: true }); } catch {}
  }
}

export async function paneIdForSession(client, sessionId) {
  const ws = await client.request('get_workspace', { sessionId }, { timeoutMs: 10_000 });
  return ws.activePaneId || ws.panes?.[0]?.paneId || null;
}

// The observer's WS `unregister` is rejected without the workspace_sessions
// capability, so close_session is the supported cleanup path.
export async function closeSessions(client, ids) {
  for (const sessionId of ids) {
    await client.request('close_session', { sessionId }, { timeoutMs: 15_000 }).catch(() => {});
  }
}

// Sequential with a per-pane settle: filling every pane at once overruns the
// websocket's 256-message buffer (see AGENTS.md).
export async function fillAllPanes(client, sessionIds, cmd, perPaneSettleMs) {
  let filled = 0;
  for (const sessionId of sessionIds) {
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
