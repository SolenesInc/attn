#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  assertCommonTargetAllowed,
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { getFrontWindowBounds, setFrontWindowBounds } from './nativeWindowCapture.mjs';
import {
  captureSessionArtifacts,
  sleep,
  waitForFirstDesktopPane,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const OVERFLOW_TOLERANCE_PX = 2;
const WINDOW_ENLARGE_HEIGHT_PX = 250;
const REVEAL_CONVERGENCE_DEADLINE_MS = 600;
const REVEAL_CONVERGENCE_POLL_MS = 100;
const ENLARGE_REFIT_DEADLINE_MS = 5_000;
const ENLARGE_REFIT_MIN_GROWTH_PX = WINDOW_ENLARGE_HEIGHT_PX / 2;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const options = { ...parseCommonArgs([]) };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--ws-url') options.wsUrl = args[++index];
    else if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir' || arg === '--artifacts') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--run-against-prod') options.runAgainstProd = true;
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }
  if (!options.help) {
    assertCommonTargetAllowed(options, args);
  }
  return {
    options,
    help: Boolean(options.help),
  };
}

async function createShellDesktop(client, observer, cwd, label) {
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'shell',
    waitForInitialPaneVisible: false,
    sessionWaitMs: 30_000,
  });
  const pane = await waitForFirstDesktopPane(client, sessionId, `initial pane for ${label}`);
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId, {
    timeoutMs: 20_000,
    description: `initial shell pane ready for ${label}`,
  });
  return { sessionId, paneId: pane.paneId, label };
}

function boundsOf(dom, key) {
  const bounds = dom?.[key]?.bounds;
  if (!bounds) {
    return null;
  }
  return bounds;
}

async function measurePane(client, sessionId, paneId) {
  const state = await client.request('get_pane_state', { sessionId, paneId });
  const dom = state?.pane?.dom;
  const canvas = boundsOf(dom, 'canvas');
  const container = boundsOf(dom, 'terminalContainer');
  if (!canvas || !container) {
    return { clean: false, reason: 'missing dom bounds', canvas, container };
  }
  const overflowBottom = canvas.y + canvas.height - (container.y + container.height);
  const overflowRight = canvas.x + canvas.width - (container.x + container.width);
  const clean = overflowBottom <= OVERFLOW_TOLERANCE_PX && overflowRight <= OVERFLOW_TOLERANCE_PX;
  return { clean, overflowBottom, overflowRight, canvas, container };
}

async function waitForRevealConvergence(client, sessionId, paneId) {
  const deadline = Date.now() + REVEAL_CONVERGENCE_DEADLINE_MS;
  const measurements = [];
  for (;;) {
    const measurement = await measurePane(client, sessionId, paneId);
    measurements.push({ atMs: REVEAL_CONVERGENCE_DEADLINE_MS - (deadline - Date.now()), ...measurement });
    if (measurement.clean) {
      return { converged: true, measurements };
    }
    if (Date.now() >= deadline) {
      return { converged: false, measurements };
    }
    await sleep(REVEAL_CONVERGENCE_POLL_MS);
  }
}

async function waitForContainerHeightAtLeast(client, sessionId, paneId, minHeight, description, timeoutMs = ENLARGE_REFIT_DEADLINE_MS) {
  const deadline = Date.now() + timeoutMs;
  let lastMeasurement = null;
  for (;;) {
    lastMeasurement = await measurePane(client, sessionId, paneId);
    if (lastMeasurement.container && lastMeasurement.container.height >= minHeight) {
      return lastMeasurement;
    }
    if (Date.now() >= deadline) {
      throw new Error(
        `Timed out waiting for ${description} (wanted container height >= ${minHeight}). Last measurement: ${JSON.stringify(lastMeasurement)}`,
      );
    }
    await sleep(REVEAL_CONVERGENCE_POLL_MS);
  }
}

// A window-height transition that turns out to be a no-op would let this
// scenario pass vacuously. Fail loudly instead.
function assertHeightChanged(beforeHeight, afterHeight, label) {
  if (beforeHeight === afterHeight) {
    throw new Error(`${label}: window height did not change (stayed ${beforeHeight}) — scenario would pass vacuously`);
  }
}

async function closeDesktopPanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const desktop = await client.request('get_desktop', { sessionId }).catch(() => null);
    const pane = desktop?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await sleep(200);
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-reveal-overflow.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'reveal-overflow');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  let desktopA;
  let desktopB;

  try {
    await launchFreshAppAndConnect(client, observer);

    desktopA = await createShellDesktop(client, observer, path.join(sessionDir, 'ws-a'), `revealoverflow-${runId}-a`);
    console.log(`[RealAppHarness] created desktop A: sessionId=${desktopA.sessionId}`);

    const originalBounds = await getFrontWindowBounds(client.bundleId, { client });
    const baselineMeasurement = await measurePane(client, desktopA.sessionId, desktopA.paneId);
    const baselineContainerHeight = baselineMeasurement.container?.height ?? 0;

    const enlargedBounds = {
      x: originalBounds.x,
      y: originalBounds.y,
      width: originalBounds.width,
      height: originalBounds.height + WINDOW_ENLARGE_HEIGHT_PX,
    };
    console.log(`[RealAppHarness] enlarging window from height=${originalBounds.height} to height=${enlargedBounds.height} (desktop A active)`);
    const appliedEnlargedBounds = await setFrontWindowBounds(enlargedBounds, { client });
    assertHeightChanged(originalBounds.height, appliedEnlargedBounds.height, 'enlarge');

    await waitForContainerHeightAtLeast(
      client,
      desktopA.sessionId,
      desktopA.paneId,
      baselineContainerHeight + ENLARGE_REFIT_MIN_GROWTH_PX,
      'desktop A pane to refit to the enlarged window',
    );
    console.log('[RealAppHarness] confirmed desktop A refit to a taller grid at the enlarged window size');

    desktopB = await createShellDesktop(client, observer, path.join(sessionDir, 'ws-b'), `revealoverflow-${runId}-b`);
    console.log(`[RealAppHarness] created desktop B: sessionId=${desktopB.sessionId} (desktop A now hidden)`);

    const shrunkBounds = {
      x: originalBounds.x,
      y: originalBounds.y,
      width: originalBounds.width,
      height: originalBounds.height,
    };
    console.log(`[RealAppHarness] shrinking window back from height=${appliedEnlargedBounds.height} to height=${shrunkBounds.height} (desktop A hidden)`);
    const appliedShrunkBounds = await setFrontWindowBounds(shrunkBounds, { client });
    assertHeightChanged(appliedEnlargedBounds.height, appliedShrunkBounds.height, 'shrink');

    console.log(`[RealAppHarness] revealing desktop A (sessionId=${desktopA.sessionId})`);
    await client.request('select_session', { sessionId: desktopA.sessionId });
    await waitForPaneVisible(client, desktopA.sessionId, desktopA.paneId, 20_000);

    const { converged, measurements } = await waitForRevealConvergence(client, desktopA.sessionId, desktopA.paneId);

    fs.writeFileSync(path.join(runDir, 'measurements.json'), `${JSON.stringify(measurements, null, 2)}\n`, 'utf8');

    if (!converged) {
      console.error(
        `[RealAppHarness] VIOLATION: revealed pane did not converge within ${REVEAL_CONVERGENCE_DEADLINE_MS}ms.`,
      );
      await captureSessionArtifacts(client, runDir, 'violation', desktopA.sessionId).catch(() => {});
      console.error(`[RealAppHarness] Evidence written to ${runDir}`);
      process.exitCode = 1;
      return;
    }

    const summary = {
      ok: true,
      runId,
      convergedAfterMeasurements: measurements.length,
      originalWindowHeightPx: originalBounds.height,
      enlargedWindowHeightPx: appliedEnlargedBounds.height,
      baselineContainerHeightPx: baselineContainerHeight,
      deadlineMs: REVEAL_CONVERGENCE_DEADLINE_MS,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log(`[RealAppHarness] Reveal-overflow scenario passed after ${measurements.length} measurement(s).`);
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    for (const desktop of [desktopB, desktopA]) {
      if (desktop) {
        await closeDesktopPanes(client, desktop.sessionId).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
