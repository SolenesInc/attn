#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, emitVerdict, parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { instanceForAppPath } from './harnessInstance.mjs';
import { getMachineFingerprint, loadBaseline, recordOrCompareBaseline } from './machineRegistry.mjs';
import { buildBaselineVerdict, evaluateRssBaseline } from './rssBaselineVerdict.mjs';
import { getFrontWindowBounds, setFrontWindowBounds } from './nativeWindowCapture.mjs';
import { delay, captureWebKitPids, snapshot, classRssMb, readLiveDaemonPid, stopDaemonKeepingTerminals, openPerfShell, closeSessions, fillAllPanes, readRegionFootprint, readGraphicsRegions, readAppFootprint } from './perfMeasure.mjs';

function parseArgs(argv) {
  const filtered = argv.filter((arg) => arg !== '--');
  const passthrough = [];
  const extras = {
    sessions: 8,
    settleMs: 4000,
    restartDaemon: true,
    window: null,
    fillCmd: null,
    fillSettleMs: 3000,
    reclaimHoldMs: 0,
    reclaimHoldIntervalMs: 15000,
    closeProbe: false,
    churnRounds: 0,
    rssTolerancePct: 15,
    recordBaseline: false,
  };
  for (let index = 0; index < filtered.length; index += 1) {
    const arg = filtered[index];
    if (arg === '--sessions') extras.sessions = Number(filtered[++index]);
    else if (arg === '--settle-ms') extras.settleMs = Number(filtered[++index]);
    else if (arg === '--no-restart-daemon') extras.restartDaemon = false;
    else if (arg === '--window') extras.window = filtered[++index];
    else if (arg === '--fill-cmd') extras.fillCmd = filtered[++index];
    else if (arg === '--fill-settle-ms') extras.fillSettleMs = Number(filtered[++index]);
    else if (arg === '--reclaim-hold-ms') extras.reclaimHoldMs = Number(filtered[++index]);
    else if (arg === '--reclaim-hold-interval-ms') extras.reclaimHoldIntervalMs = Number(filtered[++index]);
    else if (arg === '--rss-tolerance-pct') extras.rssTolerancePct = Number(filtered[++index]);
    else if (arg === '--close-probe') extras.closeProbe = true;
    else if (arg === '--churn') extras.churnRounds = Number(filtered[++index]);
    else if (arg === '--record-baseline') extras.recordBaseline = true;
    else passthrough.push(arg);
  }
  const options = parseCommonArgs(passthrough);
  return Object.assign(options, extras);
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

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/scenario-perf-baseline.mjs');
    console.log('  --sessions <n>      Shell sessions to create (default: 8)');
    console.log('  --settle-ms <n>     Settle time before the idle snapshot (default: 4000)');
    console.log('  --no-restart-daemon Do not restart the dev daemon for ATTN_PPROF');
    console.log('  --window <WxH>      Resize the app window before measuring (e.g. 1728x1080)');
    console.log('  --fill-cmd <cmd>    Run this command in every pane, one at a time, to simulate');
    console.log('                      idle panes that have rendered real output. e.g. "seq 1 60000"');
    console.log('  --fill-settle-ms <n>  Per-pane settle after the fill command (default: 3000)');
    console.log('  --reclaim-hold-ms <n> Hold the measured state and sample RSS over this window (no');
    console.log('                      pressure) to capture the reclaim decay curve. Pair with --fill-cmd.');
    console.log('  --reclaim-hold-interval-ms <n>  Sample interval during the hold (default: 15000)');
    console.log('  --churn <rounds>    Open --sessions sessions, close them all, repeat <rounds> times,');
    console.log('                      measuring at rest each round. A rising at-rest line is memory');
    console.log('                      no session owns any more.');
    console.log('  --close-probe       Close every session after the sweep and re-measure, to see');
    console.log('                      whether full teardown releases what unmounting did not.');
    console.log('  --rss-tolerance-pct <n>  Allowed growth over the per-machine baseline before the');
    console.log('                      verdict fails (default: 15)');
    console.log('  --record-baseline   Overwrite the per-machine baseline with this run\'s RSS instead');
    console.log('                      of comparing against it');
    console.log('');
    console.log('Set ATTN_PPROF=<port> to also capture /debug/vars + a heap pprof.');
    return;
  }

  const startedAt = Date.now();
  const port = pprofPort();
  const { runId, runDir, sessionDir } = createRunContext(options, 'perf-baseline');
  const client = new UiAutomationClient(options);
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
      const killed = await stopDaemonKeepingTerminals(instance);
      console.log(`[perf] stopped ${instance || 'production'} daemon pid=${killed ?? 'none'} so a fresh one inherits ATTN_PPROF=${port}`);
    }

    const webkitBaseline = await captureWebKitPids();

    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await observer.connect();

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
      await closeSessions(stale.map((session) => session.id), observer);
      await waitForSessionsGone(observer, isPerfBaselineLabel, 20_000);
    }

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

    // The daemon is detached/reparented, so its PTY host is NOT a descendant
    // of the app pid; resolve its pid from the instance pid file.
    if (!daemonPid) {
      daemonPid = readLiveDaemonPid(instanceForAppPath(options.appPath));
      if (daemonPid) {
        console.log(`[perf] resolved daemon pid=${daemonPid} from pid file (daemon + PTY host included)`);
      } else {
        console.warn('[perf] WARNING: no live daemon pid file found; daemon + PTY host RSS are NOT included in this baseline (app-tree numbers remain valid)');
      }
    }
    summary.daemonPid = daemonPid;
    summary.daemonPidSource = daemonPid ? (summary.diagUp ? 'debug-vars' : 'pid-file') : 'none';

    summary.snapshots.empty = await snapshot(appPid, daemonPid, webkitBaseline);
    summary.appFootprint = { empty: await readAppFootprint(summary.snapshots.empty) };
    summary.emptyApp = {
      regions: await readRegionFootprint(appPid),
      surfaces: await readGraphicsRegions(appPid),
    };
    console.log(
      `[perf] empty snapshot: ${summary.snapshots.empty.totalRssMb} MB rss `
      + `(${summary.snapshots.empty.procCount} procs) | APP FOOTPRINT `
      + `${summary.appFootprint.empty.totalMb} MB`,
    );
    console.log(
      `[perf] empty app process: ${JSON.stringify(summary.emptyApp.regions?.slices ?? {})} `
      + `footprint=${summary.emptyApp.regions?.footprintMb ?? 'n/a'}MB | surfaces=`
      + `${summary.emptyApp.surfaces?.largeCount ?? 'n/a'} `
      + JSON.stringify(summary.emptyApp.surfaces?.histogram ?? {}),
    );
    console.log(`[perf] empty app by pid: ${JSON.stringify(summary.appFootprint.empty.byPid)}`);

    for (let i = 0; i < options.sessions; i += 1) {
      const label = `perf-baseline-${runId}-${i}`;
      const sessionId = await openPerfShell({ client, observer, cwd: sessionDir, label });
      sessionIds.push(sessionId);
      console.log(`[perf] created session ${i + 1}/${options.sessions} (${sessionId})`);
    }

    await delay(options.settleMs);

    if (options.fillCmd) {
      await fillAllPanes(sessionIds, options.fillCmd, options.fillSettleMs, observer);
      await delay(options.settleMs);
    }

    if (options.churnRounds > 0) {
      summary.churn = [];
      for (let round = 0; round < options.churnRounds; round += 1) {
        const roundIds = [];
        for (let i = 0; i < options.sessions; i += 1) {
          roundIds.push(await openPerfShell({ client, observer, cwd: sessionDir, label: `perf-churn-${runId}-${round}-${i}` }));
        }
        if (options.fillCmd) await fillAllPanes(roundIds, options.fillCmd, options.fillSettleMs, observer);
        await delay(options.settleMs);
        const peak = await snapshot(appPid, daemonPid, webkitBaseline);
        const peakRegions = await readRegionFootprint(appPid);
        await closeSessions(roundIds, observer);
        await delay(options.settleMs);
        const rest = await snapshot(appPid, daemonPid, webkitBaseline);
        const restRegions = await readRegionFootprint(appPid);
        const restSurfaces = await readGraphicsRegions(appPid);
        summary.churn.push({
          round,
          peak: { totalRssMb: peak.totalRssMb, appDirtyMb: peakRegions?.slices ?? null },
          rest: {
            totalRssMb: rest.totalRssMb,
            appDirtyMb: restRegions?.slices ?? null,
            graphicsSurfaces: restSurfaces ?? null,
          },
        });
        console.log(
          `[perf] churn round ${round + 1}/${options.churnRounds}: `
          + `at rest (0 sessions) graphics=${restRegions?.slices.graphics ?? 'n/a'}MB `
          + `malloc=${restRegions?.slices.malloc ?? 'n/a'}MB `
          + `total=${rest.totalRssMb}MB | peak malloc=${peakRegions?.slices.malloc ?? 'n/a'}MB`,
        );
      }
    }

    if (options.closeProbe) {
      const beforeRegions = await readRegionFootprint(appPid);
      const beforeSurfaces = await readGraphicsRegions(appPid);
      await closeSessions(sessionIds, observer);
      sessionIds.length = 0;
      await delay(options.settleMs);
      const afterRegions = await readRegionFootprint(appPid);
      const afterSurfaces = await readGraphicsRegions(appPid);
      summary.closeProbe = {
        before: {
          appDirtyMb: beforeRegions?.slices ?? null,
          graphicsSurfaces: beforeSurfaces ?? null,
        },
        after: {
          appDirtyMb: afterRegions?.slices ?? null,
          graphicsSurfaces: afterSurfaces ?? null,
        },
      };
      console.log(
        `[perf] close-all: graphics ${beforeRegions?.slices.graphics ?? 'n/a'} -> `
        + `${afterRegions?.slices.graphics ?? 'n/a'}MB | malloc `
        + `${beforeRegions?.slices.malloc ?? 'n/a'} -> ${afterRegions?.slices.malloc ?? 'n/a'}MB | `
        + `paneSizedSurfaces ${beforeSurfaces?.largeCount ?? 'n/a'} -> ${afterSurfaces?.largeCount ?? 'n/a'}`,
      );
    }

    if (options.reclaimHoldMs > 0) {
      summary.reclaimHold = [];
      const holdStart = Date.now();
      let elapsed = 0;
      for (;;) {
        const snap = await snapshot(appPid, daemonPid, webkitBaseline);
        const regions = await readRegionFootprint(appPid);
        const entry = {
          tMs: elapsed,
          totalRssMb: snap.totalRssMb,
          appRssMb: classRssMb(snap, 'app'),
          appDirtyMb: regions?.slices ?? null,
        };
        summary.reclaimHold.push(entry);
        console.log(
          `[perf] HOLD t=${Math.round(elapsed / 1000)}s: app=${entry.appRssMb}MB total=${entry.totalRssMb}MB`
          + (regions ? ` gfxDirty=${regions.slices.graphics}MB mallocDirty=${regions.slices.malloc}MB` : ''),
        );
        if (elapsed >= options.reclaimHoldMs) break;
        await delay(Math.min(options.reclaimHoldIntervalMs, options.reclaimHoldMs - elapsed));
        elapsed = Date.now() - holdStart;
      }
      const first = summary.reclaimHold[0];
      const last = summary.reclaimHold[summary.reclaimHold.length - 1];
      console.log(
        `[perf] HOLD DECAY over ${Math.round(last.tMs / 1000)}s: app ${first.appRssMb}->${last.appRssMb}MB `
        + `(${Number((last.appRssMb - first.appRssMb).toFixed(1))}MB), `
        + `total ${first.totalRssMb}->${last.totalRssMb}MB (${Number((last.totalRssMb - first.totalRssMb).toFixed(1))}MB)`,
      );
    }

    if (summary.diagUp) summary.vars.idle = await httpGetJson(port, '/debug/vars').catch(() => null);
    summary.snapshots.idle = await snapshot(appPid, daemonPid, webkitBaseline);
    const workerCount = summary.vars.idle ? Object.keys(summary.vars.idle.worker_pids || {}).length : null;
    console.log(`[perf] IDLE @ ${options.sessions} sessions: ${summary.snapshots.idle.totalRssMb} MB total; workers=${workerCount ?? 'n/a'}`);

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
      ptyHost: classRssMb(idle, 'pty_host'),
    };
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
    await closeSessions(sessionIds, observer);
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
    }));
  }
}

main().catch((error) => {
  console.error('[perf] Failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
