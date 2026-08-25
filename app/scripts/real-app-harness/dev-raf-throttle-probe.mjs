#!/usr/bin/env node

// This probe cycles the window through non-key and occluded states.
process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { DaemonObserver } from './daemonObserver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
} from './common.mjs';
import {
  bundleIdentifierForProfile,
  defaultAppPathForProfile,
  defaultWSURLForProfile,
} from './harnessProfile.mjs';
import { sleep, waitForFirstWorkspacePane } from './scenarioAssertions.mjs';

const execFileAsync = promisify(execFile);
const ATTN_BUNDLE_ID = bundleIdentifierForProfile();

async function frontmostBundle() {
  try {
    const { stdout } = await execFileAsync('osascript', [
      '-e',
      'tell application "System Events" to bundle identifier of first application process whose frontmost is true',
    ], { timeout: 5_000 });
    return stdout.trim();
  } catch {
    return '';
  }
}

async function activateBundle(bundleId) {
  await execFileAsync('osascript', [
    '-e',
    `tell application id "${bundleId}" to activate`,
  ], { timeout: 5_000 }).catch(() => {});
}

// The bridge exposes no JS eval, so rAF rate is read through `renderCount`,
// which only advances inside onRender and so only advances on a rAF tick.
async function sampleRenderRate(client, sessionId, paneId, windowMs = 2000) {
  const startSample = await client.request('get_pane_state', { sessionId, paneId });
  const startRender = startSample?.renderHealth?.terminal?.renderCount ?? 0;
  const startWriteParsed = startSample?.renderHealth?.terminal?.writeParsedCount ?? 0;
  const t0 = Date.now();

  await sleep(windowMs);

  const endSample = await client.request('get_pane_state', { sessionId, paneId });
  const endRender = endSample?.renderHealth?.terminal?.renderCount ?? 0;
  const endWriteParsed = endSample?.renderHealth?.terminal?.writeParsedCount ?? 0;
  const elapsedMs = Date.now() - t0;

  return {
    renderDelta: endRender - startRender,
    writeParsedDelta: endWriteParsed - startWriteParsed,
    elapsedMs,
    renderFps: ((endRender - startRender) / elapsedMs) * 1000,
    writeFps: ((endWriteParsed - startWriteParsed) / elapsedMs) * 1000,
  };
}

async function main() {
  const runId = `raf-${Date.now()}`;
  const runDir = path.join(os.tmpdir(), 'raf-throttle-probe', runId);
  fs.mkdirSync(runDir, { recursive: true });
  console.log(`[raf-probe] runDir=${runDir}`);

  const callerBundleId = await frontmostBundle();
  console.log(`[raf-probe] caller frontmost=${callerBundleId}`);

  const appPath = defaultAppPathForProfile();
  const observer = new DaemonObserver({ wsUrl: defaultWSURLForProfile() });
  const client = new UiAutomationClient({ appPath });
  const sessionDir = fs.mkdtempSync(path.join(os.tmpdir(), 'raf-probe-'));

  const results = {
    callerBundleId,
    phases: {},
  };

  try {
    await launchFreshAppAndConnect(client, observer);
    const sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: sessionDir,
      label: `raf-${runId}`,
      agent: 'claude',
      sessionWaitMs: 60_000,
    });
    const ws0 = await client.request('get_workspace', { sessionId });
    const before = new Set((ws0.panes || []).map((p) => p.paneId));
    const initialPane = await waitForFirstWorkspacePane(client, sessionId, 'initial pane for raf probe split', 20_000);
    await client.request('split_pane', { sessionId, targetPaneId: initialPane.paneId, direction: 'vertical' });
    await sleep(1_000);
    const wsAfter = await client.request('get_workspace', { sessionId });
    const newPane = (wsAfter.panes || []).find((p) => !before.has(p.paneId));
    if (!newPane) throw new Error('no new pane after split');
    const paneId = newPane.paneId;
    console.log(`[raf-probe] paneId=${paneId}`);

    await client.request('write_pane', {
      sessionId,
      paneId,
      text: 'yes 2>/dev/null | head -c 200000 > /dev/null 2>&1; while :; do echo tick; sleep 0.1; done',
      submit: false,
    });
    await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
    await sleep(1_500);

    // launchApp hands key back to the caller, so attn is activated explicitly.
    await activateBundle(ATTN_BUNDLE_ID);
    await sleep(800);
    results.phases.key_frontmost = {
      frontmost: await frontmostBundle(),
      sample: await sampleRenderRate(client, sessionId, paneId),
    };
    console.log('[raf-probe] key_frontmost:', results.phases.key_frontmost);

    if (callerBundleId) {
      await activateBundle(callerBundleId);
    }
    await sleep(800);
    results.phases.visible_nonkey = {
      frontmost: await frontmostBundle(),
      sample: await sampleRenderRate(client, sessionId, paneId),
    };
    console.log('[raf-probe] visible_nonkey:', results.phases.visible_nonkey);

    await activateBundle(ATTN_BUNDLE_ID);
    await sleep(800);
    results.phases.key_frontmost_again = {
      frontmost: await frontmostBundle(),
      sample: await sampleRenderRate(client, sessionId, paneId),
    };
    console.log('[raf-probe] key_frontmost_again:', results.phases.key_frontmost_again);

    const fpsKey = results.phases.key_frontmost.sample.renderFps;
    const fpsNonKey = results.phases.visible_nonkey.sample.renderFps;
    const ratio = fpsKey > 0 ? fpsNonKey / fpsKey : 0;
    results.summary = {
      fpsKey: Number(fpsKey.toFixed(2)),
      fpsNonKey: Number(fpsNonKey.toFixed(2)),
      ratio: Number(ratio.toFixed(3)),
      verdict: ratio >= 0.8
        ? 'NOT_THROTTLED (parking viable for tests)'
        : ratio >= 0.2
          ? 'PARTIALLY_THROTTLED (tests with tight timeouts may fail)'
          : 'HEAVILY_THROTTLED (parking not viable)',
    };
    console.log('[raf-probe] summary:', results.summary);
  } catch (error) {
    results.error = error instanceof Error ? (error.stack || error.message) : String(error);
    console.error('[raf-probe] failed:', results.error);
  } finally {
    fs.writeFileSync(path.join(runDir, 'results.json'), JSON.stringify(results, null, 2));
    await client.quitApp().catch(() => {});
    await observer.close();
  }

  if (callerBundleId) {
    await activateBundle(callerBundleId);
  }
  process.exit(results.summary?.ratio >= 0.8 ? 0 : 1);
}

main().catch((err) => {
  console.error('[raf-probe] fatal:', err instanceof Error ? (err.stack || err.message) : err);
  process.exit(1);
});
