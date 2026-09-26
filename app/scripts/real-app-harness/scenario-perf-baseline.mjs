#!/usr/bin/env node


import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, createSessionAndWaitForInitialPane, emitVerdict, parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { instanceForAppPath } from './harnessInstance.mjs';
import { getMachineFingerprint, loadBaseline, recordOrCompareBaseline } from './machineRegistry.mjs';
import { buildBaselineVerdict, evaluateRssBaseline } from './rssBaselineVerdict.mjs';
import { captureFrontWindowScreenshot, getFrontWindowBounds, setFrontWindowBounds } from './nativeWindowCapture.mjs';
import { delay, captureWebKitPids, snapshot, classRssMb, sampleWindow, readLiveDaemonPid, assertDaemonRestartDoesNotHostSession, stopDaemon, paneIdForSession, closeSessions, fillAllPanes, readRegionFootprint, readGraphicsRegions, readAppFootprint } from './perfMeasure.mjs';

const execFileAsync = promisify(execFile);

// The app's own WebContent is the largest attributed pid; the second one is an
// auxiliary web view (the in-app browser) and never holds the terminal panes.
function webContentPid(snap) {
  const pids = snap?.byClass?.webkit_webcontent?.pids ?? [];
  if (pids.length === 0) return null;
  return pids.reduce((best, entry) => (entry.rssKb > best.rssKb ? entry : best)).pid;
}

function parseArgs(argv) {
  const filtered = argv.filter((arg) => arg !== '--');
  const passthrough = [];
  const extras = {
    sessions: 8,
    stream: 2,
    chunkBytes: 64 * 1024,
    chunkCount: 256,
    settleMs: 4000,
    restartDaemon: true,
    cpuSeconds: 20,
    realCmd: null,
    realWindowMs: 25000,
    window: null,
    fillCmd: null,
    fillSettleMs: 3000,
    reclaimHoldMs: 0,
    reclaimHoldIntervalMs: 15000,
    pressure: false,
    closeProbe: false,
    churnRounds: 0,
    dockProbe: null,
    switchProbe: false,
    rssTolerancePct: 15,
    recordBaseline: false,
  };
  for (let index = 0; index < filtered.length; index += 1) {
    const arg = filtered[index];
    if (arg === '--sessions') extras.sessions = Number(filtered[++index]);
    else if (arg === '--stream') extras.stream = Number(filtered[++index]);
    else if (arg === '--chunk-bytes') extras.chunkBytes = Number(filtered[++index]);
    else if (arg === '--chunk-count') extras.chunkCount = Number(filtered[++index]);
    else if (arg === '--settle-ms') extras.settleMs = Number(filtered[++index]);
    else if (arg === '--cpu-seconds') extras.cpuSeconds = Number(filtered[++index]);
    else if (arg === '--real-cmd') extras.realCmd = filtered[++index];
    else if (arg === '--real-window-ms') extras.realWindowMs = Number(filtered[++index]);
    else if (arg === '--no-restart-daemon') extras.restartDaemon = false;
    else if (arg === '--window') extras.window = filtered[++index];
    else if (arg === '--fill-cmd') extras.fillCmd = filtered[++index];
    else if (arg === '--fill-settle-ms') extras.fillSettleMs = Number(filtered[++index]);
    else if (arg === '--reclaim-hold-ms') extras.reclaimHoldMs = Number(filtered[++index]);
    else if (arg === '--reclaim-hold-interval-ms') extras.reclaimHoldIntervalMs = Number(filtered[++index]);
    else if (arg === '--rss-tolerance-pct') extras.rssTolerancePct = Number(filtered[++index]);
    else if (arg === '--pressure') extras.pressure = true;
    else if (arg === '--close-probe') extras.closeProbe = true;
    else if (arg === '--churn') extras.churnRounds = Number(filtered[++index]);
    else if (arg === '--dock-probe') extras.dockProbe = filtered[++index];
    else if (arg === '--switch-probe') extras.switchProbe = true;
    else if (arg === '--record-baseline') extras.recordBaseline = true;
    else passthrough.push(arg);
  }
  const options = parseCommonArgs(passthrough);
  return Object.assign(options, extras);
}

// The harness parks the window nearly off-screen and `screencapture -R` grabs a
// screen region, so put the window fully on screen and raise the app first.
async function bringWindowForward(client) {
  const parked = await getFrontWindowBounds(client.bundleId, { client }).catch(() => null);
  if (parked) {
    await setFrontWindowBounds({ x: 0, y: 25, width: parked.width, height: parked.height }, { client })
      .catch((error) => console.warn(`[perf] unpark failed: ${error.message}`));
    await delay(1500);
  }
  await execFileAsync('open', ['-b', client.bundleId]).catch(() => {});
  await delay(1200);
}

function pprofPort() {
  const raw = (process.env.ATTN_PPROF || '').trim().toLowerCase();
  if (!raw || ['0', 'off', 'false', 'no'].includes(raw)) return null;
  if (['1', 'on', 'true', 'yes'].includes(raw)) return 6060;
  const match = raw.match(/(\d+)\s*$/);
  if (match) {
    const port = Number(match[1]);
    if (port > 0 && port <= 65535) return port;
  }
  return null;
}

function httpGetJson(port, urlPath, timeoutMs = 3000) {
  return new Promise((resolve, reject) => {
    const req = http.get({ host: '127.0.0.1', port, path: urlPath, timeout: timeoutMs }, (res) => {
      let data = '';
      res.on('data', (chunk) => { data += chunk; });
      res.on('end', () => {
        if (res.statusCode !== 200) { reject(new Error(`status ${res.statusCode}`)); return; }
        try { resolve(JSON.parse(data)); } catch (error) { reject(error); }
      });
    });
    req.on('error', reject);
    req.on('timeout', () => req.destroy(new Error('timeout')));
  });
}

function httpGetToFile(port, urlPath, outPath, timeoutMs = 60_000) {
  return new Promise((resolve, reject) => {
    const file = fs.createWriteStream(outPath);
    const req = http.get({ host: '127.0.0.1', port, path: urlPath, timeout: timeoutMs }, (res) => {
      if (res.statusCode !== 200) { reject(new Error(`status ${res.statusCode}`)); return; }
      res.pipe(file);
      file.on('finish', () => file.close(() => resolve(outPath)));
    });
    req.on('error', reject);
    req.on('timeout', () => req.destroy(new Error('timeout')));
  });
}

async function waitForSessionsGone(observer, predicate, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (![...observer.sessionsById.values()].some(predicate)) return true;
    await delay(300);
  }
  return false;
}

async function streamBurst(client, sessionId, paneId, options) {
  return client.request('benchmark_pty_transport', {
    sessionId,
    paneId,
    mode: 'json_base64',
    chunkBytes: options.chunkBytes,
    chunkCount: options.chunkCount,
    flushEvery: 1,
  }, { timeoutMs: 120_000 });
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/scenario-perf-baseline.mjs');
    console.log('  --sessions <n>      Shell sessions to create (default: 8)');
    console.log('  --stream <n>        Sessions to stream into for the CPU profile (default: 2)');
    console.log('  --chunk-bytes <n>   Stream payload bytes per chunk (default: 65536)');
    console.log('  --chunk-count <n>   Stream chunks per burst (default: 256)');
    console.log('  --settle-ms <n>     Settle time before the idle snapshot (default: 4000)');
    console.log('  --cpu-seconds <n>   CPU profile duration when ATTN_PPROF set (default: 20)');
    console.log('  --real-cmd <cmd>    Run a heavy shell command in each stream target via the normal');
    console.log('                      pty path (real-output repro), e.g. "seq 1 5000000". Skips the');
    console.log('                      synthetic benchmark/CPU profile and samples peak + post RSS.');
    console.log('  --real-window-ms <n>  Sampling window for --real-cmd (default: 25000)');
    console.log('  --no-restart-daemon Do not restart the dev daemon for ATTN_PPROF');
    console.log('  --window <WxH>      Resize the app window before measuring (e.g. 1728x1080). A');
    console.log('                      pane\'s GPU surface is sized in device pixels, so the default');
    console.log('                      1200x800 launch window understates per-pane graphics memory.');
    console.log('  --fill-cmd <cmd>    Run this command in every pane (one at a');
    console.log('                      time) to grow each Ghostty WASM heap / atlas, simulating idle');
    console.log('                      panes that have rendered real output. e.g. "seq 1 60000"');
    console.log('  --fill-settle-ms <n>  Per-pane settle after the fill command (default: 3000)');
    console.log('  --reclaim-hold-ms <n> Hold the measured state and sample RSS over this window (no');
    console.log('                      pressure) to capture the reclaim decay curve. Pair with --fill-cmd.');
    console.log('  --reclaim-hold-interval-ms <n>  Sample interval during the hold (default: 15000)');
    console.log('  --pressure          Fire WebKit\'s low-memory notification and');
    console.log('                      re-measure. Separates surfaces a live layer owns (survive) from');
    console.log('                      surfaces WebKit is caching for reuse (dropped).');
    console.log('  --churn <rounds>    Open --sessions sessions, close them all, repeat <rounds> times,');
    console.log('                      measuring at rest each round. A rising at-rest line is memory');
    console.log('                      no session owns any more.');
    console.log('  --switch-probe      Walk every session with select_session and screenshot each one');
    console.log('                      after it is revealed. A pane that hands its GPU drawing buffer');
    console.log('                      back while hidden has to paint again on reveal; a blank or');
    console.log('                      stale window in a shot is that repaint missing. Pair with');
    console.log('                      --fill-cmd so each pane holds distinguishable content.');
    console.log('  --close-probe       Close every session after the sweep and re-measure, to see');
    console.log('                      whether full teardown releases what unmounting did not.');
    console.log('  --rss-tolerance-pct <n>  Allowed growth over the per-machine baseline before the');
    console.log('                      verdict fails (default: 15)');
    console.log('  --record-baseline   Overwrite the per-machine baseline with this run\'s RSS instead');
    console.log('                      of comparing against it');
    console.log('');
    console.log('Set ATTN_PPROF=<port> to also capture /debug/vars + heap/CPU pprof.');
    return;
  }

  const startedAt = Date.now();
  const port = pprofPort();
  const { runId, runDir, sessionDir } = createRunContext(options, 'perf-baseline');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const isPerfBaselineLabel = (session) => typeof session.label === 'string' && session.label.startsWith('perf-baseline-');
  const sessionIds = [];
  let rssEvaluation = null;

  const summary = {
    ok: false,
    runId,
    runDir,
    sessions: options.sessions,
    reclaimHold: null,
    requestedStream: options.stream,
    chunkBytes: options.chunkBytes,
    chunkCount: options.chunkCount,
    pprofPort: port,
    appPid: null,
    daemonPid: null,
    diagUp: false,
    snapshots: {},
    vars: {},
    profiles: {},
  };

  try {
    if (port && options.restartDaemon) {
      const instance = instanceForAppPath(options.appPath);
      assertDaemonRestartDoesNotHostSession(instance);
      const killed = await stopDaemon(instance);
      console.log(`[perf] stopped ${instance || 'production'} daemon pid=${killed ?? 'none'} so a fresh one inherits ATTN_PPROF=${port}`);
    }

    // Snapshot WebKit pids before relaunch so the new ones are attributable to
    // the dev app, excluding a possibly-running prod app's WebKit.
    const webkitBaseline = await captureWebKitPids();

    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await observer.connect();

    // A pane's GPU surface is sized in device pixels: the harness launches at
    // Tauri's 1200x800 default, understating the per-pane surface.
    if (options.window) {
      const match = /^(\d+)x(\d+)$/.exec(options.window);
      if (!match) throw new Error(`--window expects WxH (e.g. 1728x1080), got: ${options.window}`);
      const current = await getFrontWindowBounds(client.bundleId, { client });
      await setFrontWindowBounds(
        { x: current?.x ?? 0, y: current?.y ?? 0, width: Number(match[1]), height: Number(match[2]) },
        { client },
      );
      await delay(1500);
    }
    summary.window = await getFrontWindowBounds(client.bundleId, { client }).catch(() => null);
    console.log(`[perf] window bounds: ${summary.window ? `${summary.window.width}x${summary.window.height}` : 'unknown'}`);

    // Sessions persist in the daemon's SQLite store across restarts, so a fresh
    // daemon can still surface stale perf-baseline sessions with dead workers.
    const stale = [...observer.sessionsById.values()].filter(isPerfBaselineLabel);
    if (stale.length > 0) {
      console.log(`[perf] closing ${stale.length} stale perf-baseline session(s) from prior runs`);
      await closeSessions(client, stale.map((session) => session.id));
      await waitForSessionsGone(observer, isPerfBaselineLabel, 20_000);
    }
    await client.waitForFrontendResponsive(20_000);

    const manifest = client.readManifest();
    const appPid = manifest.pid;
    summary.appPid = appPid;

    let daemonPid = null;
    if (port) {
      for (let i = 0; i < 25; i += 1) {
        try {
          const vars = await httpGetJson(port, '/debug/vars');
          summary.diagUp = true;
          summary.vars.before = vars;
          daemonPid = vars.pid;
          break;
        } catch {
          await delay(400);
        }
      }
      if (summary.diagUp) {
        console.log(`[perf] diag endpoint live on :${port} daemonPid=${daemonPid} backend=${summary.vars.before.pty_backend}`);
      } else {
        console.warn(`[perf] diag endpoint not reachable on :${port}; continuing with ps-tree memory only`);
      }
    }

    // The daemon is detached/reparented, so its pty-workers are NOT descendants
    // of the app pid; resolve its pid from the instance pid file.
    if (!daemonPid) {
      daemonPid = readLiveDaemonPid(instanceForAppPath(options.appPath));
      if (daemonPid) {
        console.log(`[perf] resolved daemon pid=${daemonPid} from pid file (daemon + pty-workers included)`);
      } else {
        console.warn('[perf] WARNING: no live daemon pid file found; daemon + pty-worker RSS are NOT included in this baseline (app-tree + WebKit numbers remain valid)');
      }
    }
    summary.daemonPid = daemonPid;
    summary.daemonPidSource = daemonPid ? (summary.diagUp ? 'debug-vars' : 'pid-file') : 'none';

    summary.snapshots.empty = await snapshot(appPid, daemonPid, webkitBaseline);
    summary.appFootprint = { empty: await readAppFootprint(summary.snapshots.empty) };
    const emptyWc = webContentPid(summary.snapshots.empty);
    summary.emptyWebContent = {
      regions: await readRegionFootprint(emptyWc),
      surfaces: await readGraphicsRegions(emptyWc),
    };
    console.log(
      `[perf] empty snapshot: ${summary.snapshots.empty.totalRssMb} MB rss `
      + `(${summary.snapshots.empty.procCount} procs) | APP FOOTPRINT `
      + `${summary.appFootprint.empty.totalMb} MB`,
    );
    console.log(
      `[perf] empty app webContent: ${JSON.stringify(summary.emptyWebContent.regions?.slices ?? {})} `
      + `footprint=${summary.emptyWebContent.regions?.footprintMb ?? 'n/a'}MB | surfaces=`
      + `${summary.emptyWebContent.surfaces?.largeCount ?? 'n/a'} `
      + JSON.stringify(summary.emptyWebContent.surfaces?.histogram ?? {}),
    );
    console.log(`[perf] empty app by pid: ${JSON.stringify(summary.appFootprint.empty.byPid)}`);

    // Wait for each session's initial pane to mount before creating the next:
    // firing them back-to-back hangs the bridge while a terminal is mounting.
    for (let i = 0; i < options.sessions; i += 1) {
      const label = `perf-baseline-${runId}-${i}`;
      const sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: sessionDir,
        label,
        agent: 'shell',
        sessionWaitMs: 30_000,
        waitForInitialPaneVisible: true,
        initialPaneWaitMs: 25_000,
      });
      sessionIds.push(sessionId);
      console.log(`[perf] created session ${i + 1}/${options.sessions} (${sessionId})`);
    }

    await delay(options.settleMs);

    if (options.fillCmd) {
      await fillAllPanes(client, sessionIds, options.fillCmd, options.fillSettleMs);
      await delay(options.settleMs);
    }

    if (options.pressure) {
      const before = await snapshot(appPid, daemonPid, webkitBaseline);
      const pid = webContentPid(before);
      const beforeRegions = await readRegionFootprint(pid);
      const beforeSurfaces = await readGraphicsRegions(pid);
      await execFileAsync('notifyutil', ['-p', 'org.WebKit.lowMemory']);
      await delay(options.settleMs);
      const after = await snapshot(appPid, daemonPid, webkitBaseline);
      const afterRegions = await readRegionFootprint(pid);
      const afterSurfaces = await readGraphicsRegions(pid);
      summary.pressure = {
        before: {
          totalRssMb: before.totalRssMb,
          byClass: before.byClass,
          webContentDirtyMb: beforeRegions?.slices ?? null,
          graphicsSurfaces: beforeSurfaces ?? null,
        },
        after: {
          totalRssMb: after.totalRssMb,
          byClass: after.byClass,
          webContentDirtyMb: afterRegions?.slices ?? null,
          graphicsSurfaces: afterSurfaces ?? null,
        },
      };
      console.log(
        `[perf] pressure: total ${before.totalRssMb} -> ${after.totalRssMb}MB | `
        + `graphics ${beforeRegions?.slices.graphics ?? 'n/a'} -> ${afterRegions?.slices.graphics ?? 'n/a'}MB | `
        + `paneSizedSurfaces ${beforeSurfaces?.largeCount ?? 'n/a'} -> ${afterSurfaces?.largeCount ?? 'n/a'} `
        + `(${beforeSurfaces?.largeDirtyMb ?? 'n/a'} -> ${afterSurfaces?.largeDirtyMb ?? 'n/a'}MB dirty)`,
      );
    }

    if (options.dockProbe) {
      const closedPid = webContentPid(await snapshot(appPid, daemonPid, webkitBaseline));
      const closed = await readGraphicsRegions(closedPid);
      await client.request('open_dock_panel', { panelId: options.dockProbe }, { timeoutMs: 15_000 });
      await delay(options.settleMs);
      const openPid = webContentPid(await snapshot(appPid, daemonPid, webkitBaseline));
      const opened = await readGraphicsRegions(openPid);
      await bringWindowForward(client);
      const shot = path.join(runDir, `dock-${options.dockProbe}-open.png`);
      await captureFrontWindowScreenshot(shot).catch((error) => console.warn(`[perf] dock screenshot failed: ${error.message}`));
      summary.dockProbe = { panelId: options.dockProbe, closed, opened, screenshot: shot };
      console.log(
        `[perf] dock ${options.dockProbe}: closed surfaces=${closed?.largeCount ?? 'n/a'} `
        + JSON.stringify(closed?.histogram ?? {})
        + ` -> open surfaces=${opened?.largeCount ?? 'n/a'} `
        + JSON.stringify(opened?.histogram ?? {}) + ` | shot=${shot}`,
      );
    }

    if (options.switchProbe && sessionIds.length > 0) {
      await bringWindowForward(client);
      summary.switchProbe = [];
      for (let index = 0; index < sessionIds.length; index += 1) {
        const sessionId = sessionIds[index];
        await client.request('select_session', { sessionId }, { timeoutMs: 15_000 });
        await delay(options.settleMs);
        const shot = path.join(runDir, `switch-${index + 1}.png`);
        await captureFrontWindowScreenshot(shot)
          .catch((error) => console.warn(`[perf] switch screenshot failed: ${error.message}`));
        const surfaces = await readGraphicsRegions(webContentPid(await snapshot(appPid, daemonPid, webkitBaseline)));
        summary.switchProbe.push({ sessionId, screenshot: shot, graphicsSurfaces: surfaces ?? null });
        console.log(
          `[perf] switch ${index + 1}/${sessionIds.length}: ${sessionId} | `
          + `paneSizedSurfaces=${surfaces?.largeCount ?? 'n/a'} `
          + JSON.stringify(surfaces?.histogram ?? {}) + ` | shot=${shot}`,
        );
      }
    }

    if (options.churnRounds > 0) {
      summary.churn = [];
      for (let round = 0; round < options.churnRounds; round += 1) {
        const roundIds = [];
        for (let i = 0; i < options.sessions; i += 1) {
          roundIds.push(await createSessionAndWaitForInitialPane({
            client,
            observer,
            cwd: sessionDir,
            label: `perf-churn-${runId}-${round}-${i}`,
            agent: 'shell',
            sessionWaitMs: 30_000,
            waitForInitialPaneVisible: true,
            initialPaneWaitMs: 25_000,
          }));
        }
        if (options.fillCmd) await fillAllPanes(client, roundIds, options.fillCmd, options.fillSettleMs);
        await delay(options.settleMs);
        const peak = await snapshot(appPid, daemonPid, webkitBaseline);
        const peakRegions = await readRegionFootprint(webContentPid(peak));
        await closeSessions(client, roundIds);
        await delay(options.settleMs);
        const rest = await snapshot(appPid, daemonPid, webkitBaseline);
        const pid = webContentPid(rest);
        const restRegions = await readRegionFootprint(pid);
        const restSurfaces = await readGraphicsRegions(pid);
        summary.churn.push({
          round,
          peak: { totalRssMb: peak.totalRssMb, webContentDirtyMb: peakRegions?.slices ?? null },
          rest: {
            totalRssMb: rest.totalRssMb,
            webContentDirtyMb: restRegions?.slices ?? null,
            graphicsSurfaces: restSurfaces ?? null,
          },
        });
        console.log(
          `[perf] churn round ${round + 1}/${options.churnRounds}: `
          + `at rest (0 sessions) graphics=${restRegions?.slices.graphics ?? 'n/a'}MB `
          + `webkitMalloc=${restRegions?.slices.webkitMalloc ?? 'n/a'}MB `
          + `jsHeap=${restRegions?.slices.jsHeap ?? 'n/a'}MB `
          + `total=${rest.totalRssMb}MB | peak webkitMalloc=${peakRegions?.slices.webkitMalloc ?? 'n/a'}MB`,
        );
      }
    }

    if (options.closeProbe) {
      const before = await snapshot(appPid, daemonPid, webkitBaseline);
      const pid = webContentPid(before);
      const beforeRegions = await readRegionFootprint(pid);
      const beforeSurfaces = await readGraphicsRegions(pid);
      await closeSessions(client, sessionIds);
      sessionIds.length = 0;
      await delay(options.settleMs);
      await snapshot(appPid, daemonPid, webkitBaseline);
      const afterRegions = await readRegionFootprint(pid);
      const afterSurfaces = await readGraphicsRegions(pid);
      summary.closeProbe = {
        before: {
          webContentDirtyMb: beforeRegions?.slices ?? null,
          graphicsSurfaces: beforeSurfaces ?? null,
        },
        after: {
          webContentDirtyMb: afterRegions?.slices ?? null,
          graphicsSurfaces: afterSurfaces ?? null,
        },
      };
      console.log(
        `[perf] close-all: graphics ${beforeRegions?.slices.graphics ?? 'n/a'} -> `
        + `${afterRegions?.slices.graphics ?? 'n/a'}MB | webkitMalloc `
        + `${beforeRegions?.slices.webkitMalloc ?? 'n/a'} -> ${afterRegions?.slices.webkitMalloc ?? 'n/a'}MB | `
        + `jsHeap ${beforeRegions?.slices.jsHeap ?? 'n/a'} -> ${afterRegions?.slices.jsHeap ?? 'n/a'}MB | `
        + `paneSizedSurfaces ${beforeSurfaces?.largeCount ?? 'n/a'} -> ${afterSurfaces?.largeCount ?? 'n/a'}`,
      );
    }

    // WebKit's scavenger reclaims freed WASM + heap on a delay LONGER than a
    // settle window (~30-120s), so record the decay curve instead of one shot.
    if (options.reclaimHoldMs > 0) {
      summary.reclaimHold = [];
      const holdStart = Date.now();
      let elapsed = 0;
      for (;;) {
        const snap = await snapshot(appPid, daemonPid, webkitBaseline);
        const regions = await readRegionFootprint(webContentPid(snap));
        const entry = {
          tMs: elapsed,
          totalRssMb: snap.totalRssMb,
          webContentRssMb: classRssMb(snap, 'webkit_webcontent'),
          gpuRssMb: classRssMb(snap, 'webkit_gpu'),
          webContentDirtyMb: regions?.slices ?? null,
        };
        summary.reclaimHold.push(entry);
        console.log(
          `[perf] HOLD t=${Math.round(elapsed / 1000)}s: webContent=${entry.webContentRssMb}MB `
          + `gpu=${entry.gpuRssMb}MB total=${entry.totalRssMb}MB`
          + (regions ? ` gfxDirty=${regions.slices.graphics}MB mallocDirty=${regions.slices.webkitMalloc}MB` : ''),
        );
        if (elapsed >= options.reclaimHoldMs) break;
        await delay(Math.min(options.reclaimHoldIntervalMs, options.reclaimHoldMs - elapsed));
        elapsed = Date.now() - holdStart;
      }
      const first = summary.reclaimHold[0];
      const last = summary.reclaimHold[summary.reclaimHold.length - 1];
      console.log(
        `[perf] HOLD DECAY over ${Math.round(last.tMs / 1000)}s: webContent ${first.webContentRssMb}->${last.webContentRssMb}MB `
        + `(${Number((last.webContentRssMb - first.webContentRssMb).toFixed(1))}MB), `
        + `total ${first.totalRssMb}->${last.totalRssMb}MB (${Number((last.totalRssMb - first.totalRssMb).toFixed(1))}MB)`,
      );
    }

    if (summary.diagUp) summary.vars.idle = await httpGetJson(port, '/debug/vars').catch(() => null);
    summary.snapshots.idle = await snapshot(appPid, daemonPid, webkitBaseline);
    const workerCount = summary.vars.idle ? Object.keys(summary.vars.idle.worker_pids || {}).length : null;
    console.log(`[perf] IDLE @ ${options.sessions} sessions: ${summary.snapshots.idle.totalRssMb} MB total; workers=${workerCount ?? 'n/a'}`);

    const streamN = Math.min(options.stream, sessionIds.length);
    if (options.realCmd && streamN > 0) {
      const targets = [];
      for (let i = 0; i < streamN; i += 1) {
        const sid = sessionIds[i];
        const paneId = await paneIdForSession(client, sid);
        if (!paneId) continue;
        targets.push({ sid, paneId });
        await client.request('write_pane', { sessionId: sid, paneId, text: options.realCmd }, { timeoutMs: 15_000 })
          .catch((error) => console.warn(`[perf] write_pane ${sid} failed: ${error.message}`));
      }
      console.log(`[perf] ran "${options.realCmd}" in ${targets.length} pane(s); sampling ${options.realWindowMs}ms`);
      const win = await sampleWindow(appPid, daemonPid, webkitBaseline, options.realWindowMs);
      summary.snapshots.realPeak = win.peak;
      await delay(3000);
      summary.snapshots.realPost = await snapshot(appPid, daemonPid, webkitBaseline);
      if (summary.diagUp) summary.vars.realPost = await httpGetJson(port, '/debug/vars').catch(() => null);
      console.log(`[perf] REAL-OUTPUT peak=${win.peak.totalRssMb} MB  post-settle=${summary.snapshots.realPost.totalRssMb} MB  (idle was ${summary.snapshots.idle.totalRssMb} MB)`);
    } else if (summary.diagUp && streamN > 0) {
      try {
        const targets = [];
        for (let i = 0; i < streamN; i += 1) {
          const sid = sessionIds[i];
          const paneId = await paneIdForSession(client, sid);
          if (paneId) targets.push({ sid, paneId });
        }
        if (targets.length > 0) {
          console.log(`[perf] capturing ${options.cpuSeconds}s CPU profile under ${targets.length}-session stream`);
          const cpuPromise = httpGetToFile(port, `/debug/pprof/profile?seconds=${options.cpuSeconds}`, path.join(runDir, 'cpu.pb.gz'), (options.cpuSeconds + 15) * 1000);
          const burst = targets.map(({ sid, paneId }) => (async () => {
            const deadline = Date.now() + options.cpuSeconds * 1000;
            while (Date.now() < deadline) {
              await streamBurst(client, sid, paneId, options).catch(() => {});
            }
          })());
          summary.profiles.cpu = await cpuPromise.catch((error) => { console.warn(`[perf] cpu profile failed: ${error.message}`); return null; });
          await Promise.all(burst).catch(() => {});
          summary.snapshots.active = await snapshot(appPid, daemonPid, webkitBaseline);
          console.log(`[perf] ACTIVE (under stream): ${summary.snapshots.active.totalRssMb} MB total`);
        }
      } catch (error) {
        console.warn(`[perf] streaming/CPU phase skipped: ${error.message}`);
      }
    }

    if (summary.diagUp) {
      summary.profiles.heap = await httpGetToFile(port, '/debug/pprof/heap', path.join(runDir, 'heap.pb.gz')).catch((error) => { console.warn(`[perf] heap profile failed: ${error.message}`); return null; });
      summary.vars.post = await httpGetJson(port, '/debug/vars').catch(() => null);
    }
    summary.snapshots.post = await snapshot(appPid, daemonPid, webkitBaseline);

    summary.ok = true;
    const idle = summary.snapshots.idle;
    summary.headline = {
      sessions: options.sessions,
      totalRssMb: idle.totalRssMb,
      app: classRssMb(idle, 'app'),
      webkit: ['webkit_webcontent', 'webkit_gpu', 'webkit_networking'].reduce((sum, k) => sum + classRssMb(idle, k), 0),
      daemon: classRssMb(idle, 'daemon'),
      ptyWorkers: classRssMb(idle, 'pty_worker'),
      ptyWorkerCount: idle.byClass.pty_worker?.count ?? 0,
      perWorkerAvgMb: idle.byClass.pty_worker?.count ? Number((classRssMb(idle, 'pty_worker') / idle.byClass.pty_worker.count).toFixed(1)) : 0,
    };
    if (summary.snapshots.realPeak) {
      const peak = summary.snapshots.realPeak;
      const post = summary.snapshots.realPost;
      summary.headline.realOutput = {
        cmd: options.realCmd,
        idleWebContentMb: classRssMb(idle, 'webkit_webcontent'),
        peakTotalMb: peak.totalRssMb,
        peakWebContentMb: classRssMb(peak, 'webkit_webcontent'),
        postTotalMb: post?.totalRssMb ?? null,
        postWebContentMb: post ? classRssMb(post, 'webkit_webcontent') : null,
        retainedMb: post ? Number((post.totalRssMb - idle.totalRssMb).toFixed(1)) : null,
      };
    }
    const fingerprint = getMachineFingerprint();
    const baseline = loadBaseline(fingerprint.key);
    rssEvaluation = evaluateRssBaseline({
      totalRssMb: summary.headline.totalRssMb,
      fingerprint,
      baseline,
      tolerancePct: options.rssTolerancePct,
      record: options.recordBaseline,
      recordedAt: new Date().toISOString(),
    });
    recordOrCompareBaseline({ evaluation: rssEvaluation, key: fingerprint.key });
    summary.baselineComparison = rssEvaluation.comparison;
  } finally {
    await closeSessions(client, sessionIds);
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    await observer.close();
  }


  console.log(JSON.stringify({ headline: summary.headline, reclaimHold: summary.reclaimHold, idleByClass: summary.snapshots.idle?.byClass, profiles: summary.profiles, runDir }, null, 2));

  // An RSS regression against the machine baseline sets verdict.ok:false but
  // never a non-zero exit code.
  if (rssEvaluation) {
    emitVerdict(buildBaselineVerdict({
      ok: rssEvaluation.ok,
      comparison: rssEvaluation.comparison,
      scenarioId: 'perf-baseline',
      runId,
      artifactsDir: runDir,
      summaryPath: path.join(runDir, 'summary.json'),
      durationMs: Date.now() - startedAt,
      extraMetrics: summary.headline?.realOutput?.retainedMb != null
        ? { retainedMb: summary.headline.realOutput.retainedMb }
        : {},
    }));
  }
}

main().catch((error) => {
  console.error('[perf] Failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
