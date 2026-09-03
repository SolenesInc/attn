import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  armAgentTripwire,
  ensureDaemonCarriesTripwire,
  formatMockGitHubFailure,
  formatReceiptFailure,
  formatTripwireFailure,
  readDaemonMockGitHubURL,
} from './agentTripwire.mjs';
import { assertPackagedAppBuildMatchesCurrentSource } from './buildPreflight.mjs';
import { createRunContext, emitVerdict, FIRST_FAILURE_MAX_LENGTH, restoreHarnessSettings } from './common.mjs';
import { ensureMockGitHubServer } from './mockGitHub.mjs';
import { createWindowDriver } from './platform.mjs';
import { allowRealAgentsForRunner } from './scenarioCatalog.mjs';
import { createScenarioRecorder, recordingEnabled } from './windowRecording.mjs';

// The verdict's firstFailure must stay one line whatever the error contains.
export function summarizeFirstFailure(error) {
  const message = error instanceof Error ? error.message : String(error);
  const firstLine = message.split(/\r?\n/, 1)[0];
  if (firstLine.length <= FIRST_FAILURE_MAX_LENGTH) {
    return firstLine;
  }
  return firstLine.slice(0, FIRST_FAILURE_MAX_LENGTH);
}

function writeJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

const FAILURE_DIGEST_MAX_ERROR_LINES = 60;

function buildFailureDigest({ scenarioId, runId, steps, error, runDir, tripwire = null }) {
  const failingStep = [...steps].reverse().find((step) => step.status === 'error');
  const errorLines = normalizeError(error).split(/\r?\n/).slice(0, FAILURE_DIGEST_MAX_ERROR_LINES);
  // The tripwire kills the session the failing step was waiting on, so its
  // lines have to come before the step error they caused.
  const tripwireLines = tripwire
    ? [formatTripwireFailure({ scenarioId, ledgerPath: tripwire.ledgerPath, lines: tripwire.lines }), '']
    : [];
  return [
    ...tripwireLines,
    `scenario: ${scenarioId}`,
    `run: ${runId}`,
    `failing step: ${failingStep ? failingStep.name : '(none — failed outside a step)'}`,
    `error:`,
    ...errorLines,
    `artifacts: ${runDir}`,
  ].join('\n');
}

function normalizeError(error) {
  if (error instanceof Error) {
    return error.stack || error.message;
  }
  return String(error);
}

function processExists(pid) {
  if (!Number.isInteger(pid) || pid <= 0) {
    return false;
  }

  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

export function packagedAppScenarioLockPath() {
  return process.env.ATTN_REAL_APP_SCENARIO_LOCK_PATH || path.join(os.tmpdir(), 'attn-real-app-harness-scenario.lock');
}

const SCENARIO_LOCK_POLL_MS = 2_000;
const SCENARIO_LOCK_HEARTBEAT_MS = 15_000;
// 20 missed 15s heartbeats. A SERIAL-MATRIX holder legitimately runs for hours,
// so only a wedged event loop (or a pre-heartbeat build) ever looks this stale.
const SCENARIO_LOCK_STALE_MS = 300_000;

function scenarioLockWaitMs() {
  const raw = process.env.ATTN_REAL_APP_SCENARIO_LOCK_WAIT_MS;
  if (raw === undefined || raw === '') {
    return Infinity;
  }
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`invalid ATTN_REAL_APP_SCENARIO_LOCK_WAIT_MS: ${raw} (want milliseconds; 0 fails fast)`);
  }
  return value;
}

// Blocking the event loop is the point: the runner is sync and nothing else runs.
function sleepSync(ms) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

function describeLockOwner(owner) {
  const scenario = owner?.scenarioId || 'unknown';
  const pid = Number.isInteger(owner?.pid) ? owner.pid : 'unknown';
  const runId = owner?.runId || 'unknown';
  const startedAt = owner?.startedAt || 'unknown';
  return `${scenario} (pid ${pid}, run ${runId}, started ${startedAt})`;
}

function ownerHeartbeatAgeMs(lockDir, nowMs) {
  try {
    return nowMs - fs.statSync(path.join(lockDir, 'owner.json')).mtimeMs;
  } catch {
    return 0;
  }
}

function readLockOwner(lockDir) {
  const ownerPath = path.join(lockDir, 'owner.json');
  return JSON.parse(fs.readFileSync(ownerPath, 'utf8'));
}

function removeDirIfPresent(dirPath) {
  try {
    fs.rmSync(dirPath, { recursive: true, force: true });
  } catch {}
}

export function acquireScenarioLock({ scenarioId, tier, runId, runDir, appPath }, lockDir = packagedAppScenarioLockPath(), {
  waitMs = scenarioLockWaitMs(),
  pollMs = SCENARIO_LOCK_POLL_MS,
  heartbeatMs = SCENARIO_LOCK_HEARTBEAT_MS,
  staleMs = SCENARIO_LOCK_STALE_MS,
  sleep = sleepSync,
  now = Date.now,
  log = (message) => console.error(message),
} = {}) {
  const ownerPath = path.join(lockDir, 'owner.json');
  const owner = {
    pid: process.pid,
    scenarioId,
    tier,
    runId,
    runDir,
    appPath: appPath || null,
    startedAt: new Date().toISOString(),
    command: process.argv.join(' '),
  };

  const waitStart = now();
  let announced = false;
  let staleStrikes = 0;
  while (true) {
    try {
      fs.mkdirSync(lockDir);
      writeJson(ownerPath, owner);
      break;
    } catch (error) {
      if (!error || error.code !== 'EEXIST') {
        throw error;
      }

      let existingOwner = null;
      try {
        existingOwner = readLockOwner(lockDir);
      } catch {
        removeDirIfPresent(lockDir);
        continue;
      }

      if (!processExists(existingOwner?.pid)) {
        removeDirIfPresent(lockDir);
        continue;
      }

      if (now() - waitStart >= waitMs) {
        throw new Error(
          `packaged-app scenarios are single-tenant; gave up after ${waitMs}ms ` +
          `(ATTN_REAL_APP_SCENARIO_LOCK_WAIT_MS) waiting for ` +
          `${describeLockOwner(existingOwner)} to release ${lockDir}`
        );
      }

      const heartbeatAge = ownerHeartbeatAgeMs(lockDir, now());
      // One stale poll is forgiven: after wake-from-sleep the waiter can stat
      // the owner before the holder's overdue heartbeat timer has fired.
      staleStrikes = heartbeatAge >= staleMs ? staleStrikes + 1 : 0;
      if (staleStrikes >= 2) {
        throw new Error(
          `packaged-app scenario lock holder looks wedged: ${describeLockOwner(existingOwner)} ` +
          `is alive but has not heartbeat ${lockDir} for ${Math.round(heartbeatAge)}ms ` +
          `(threshold ${staleMs}ms); stop it, or remove the lock dir if it is gone`
        );
      }

      if (!announced) {
        announced = true;
        log(`[scenario-lock] ${scenarioId}: waiting for ${describeLockOwner(existingOwner)} to release ${lockDir}`);
      }
      sleep(pollMs);
    }
  }

  const heartbeat = setInterval(() => {
    try {
      const stamp = new Date();
      fs.utimesSync(ownerPath, stamp, stamp);
    } catch {}
  }, heartbeatMs);
  heartbeat.unref?.();

  let released = false;
  const release = () => {
    if (released) {
      return;
    }
    released = true;
    clearInterval(heartbeat);
    try {
      const existingOwner = readLockOwner(lockDir);
      if (existingOwner?.pid === process.pid && existingOwner?.runId === runId) {
        removeDirIfPresent(lockDir);
      }
    } catch {
      removeDirIfPresent(lockDir);
    }
  };

  return release;
}

export function createScenarioRunner(options, {
  scenarioId,
  tier,
  prefix,
  metadata = {},
  preflightLaunchEnv = null,
  allowRealAgents,
} = {}, {
  assertBuildMatches = assertPackagedAppBuildMatchesCurrentSource,
  armTripwire = armAgentTripwire,
  ensureDaemonArmed = ensureDaemonCarriesTripwire,
  ensureMockGitHub = ensureMockGitHubServer,
  readDaemonMockGitHub = readDaemonMockGitHubURL,
  createRecorder = createScenarioRecorder,
  createRecordingDriver = (appPath) => createWindowDriver({ appPath }),
  emitRunnerVerdict = emitVerdict,
  isRecordingEnabled = recordingEnabled,
} = {}) {
  const runnerCreatedAt = Date.now();
  const declaredAllowRealAgents = allowRealAgents === undefined
    ? allowRealAgentsForRunner(scenarioId)
    : allowRealAgents;
  assertBuildMatches({
    appPath: options?.appPath,
    launchEnv: preflightLaunchEnv,
  });
  const { runId, runDir, sessionDir } = createRunContext(options, prefix || scenarioId.toLowerCase());
  let releaseScenarioLock = null;
  try {
    releaseScenarioLock = acquireScenarioLock({
      scenarioId,
      tier,
      runId,
      runDir,
      appPath: options?.appPath,
    });
  } catch (error) {
    removeDirIfPresent(runDir);
    removeDirIfPresent(sessionDir);
    throw error;
  }
  const tripwire = armTripwire({ scenarioId, runDir, allowRealAgents: declaredAllowRealAgents });
  const mockGitHub = ensureMockGitHub({ appPath: options?.appPath });
  try {
    ensureDaemonArmed({
      scenarioId,
      marker: tripwire.marker,
      armed: tripwire.armed,
      mockGitHubURL: mockGitHub?.url ?? null,
      appPath: options?.appPath,
    });
  } catch (error) {
    if (tripwire.armed) {
      releaseScenarioLock();
      removeDirIfPresent(runDir);
      removeDirIfPresent(sessionDir);
      throw error;
    }
    console.warn(`[agent-tripwire] could not check the running daemon: ${error?.message || error}`);
  }
  const tracePath = path.join(runDir, 'trace.log');
  const steps = [];
  const assertions = [];
  const cleanupHandlers = [];
  let cleanupPromise = null;
  let finalizationPromise = null;

  const appendTrace = (message, details) => {
    const line = `[${new Date().toISOString()}] ${message}${details ? ` ${JSON.stringify(details)}` : ''}\n`;
    fs.appendFileSync(tracePath, line, 'utf8');
    process.stdout.write(line);
  };

  let tripwireTraced = false;
  const collectTripwireLedger = () => {
    const lines = tripwire.read();
    if (lines.length > 0 && !tripwireTraced) {
      tripwireTraced = true;
      appendTrace('agent_tripwire:tripped', { count: lines.length, lines });
    }
    return lines;
  };

  const readDaemonReceipt = () => tripwire.readReceipt?.() || null;
  const headlessSwitchField = (receipt) => (receipt ? { headlessTasks: receipt.headlessTasks } : {});
  let expectedMockGitHubURL = mockGitHub?.url ?? null;
  const observeMockGitHub = () => (expectedMockGitHubURL ? readDaemonMockGitHub({ pidPath: tripwire.pidPath }) : null);
  const mockGitHubField = (observed) => (expectedMockGitHubURL ? { mockGitHub: observed } : {});

  let recorder = null;
  if (isRecordingEnabled()) {
    const recordingDriver = createRecordingDriver(options.appPath);
    recorder = createRecorder({
      runDir,
      resolveWindowId: () => recordingDriver.mainWindowId(),
      log: appendTrace,
    });
    recorder.start();
  }

  const runRegisteredCleanup = async (reason) => {
    if (cleanupPromise) {
      return cleanupPromise;
    }
    cleanupPromise = (async () => {
      // beforeExit does not fire on a signal; draining the queue makes whichever
      // of the two runs second a no-op.
      try {
        const restored = await restoreHarnessSettings();
        if (restored > 0) {
          appendTrace('settings:restored', { count: restored });
        }
      } catch (error) {
        appendTrace('settings:restore_failed', { error: normalizeError(error) });
      }
      if (cleanupHandlers.length === 0) {
        return;
      }
      appendTrace('cleanup:start', { reason, count: cleanupHandlers.length });
      for (const cleanup of [...cleanupHandlers].reverse()) {
        try {
          appendTrace('cleanup:run', { reason, name: cleanup.name });
          await cleanup.fn();
          appendTrace('cleanup:ok', { reason, name: cleanup.name });
        } catch (error) {
          appendTrace('cleanup:error', {
            reason,
            name: cleanup.name,
            error: normalizeError(error),
          });
        }
      }
      appendTrace('cleanup:done', { reason });
    })();
    return cleanupPromise;
  };

  const finalizeRunner = async () => {
    if (finalizationPromise) return finalizationPromise;
    finalizationPromise = (async () => {
      let recorderError = null;
      try {
        await recorder?.stop();
      } catch (error) {
        recorderError = error;
        appendTrace('recording:error', { error: normalizeError(error) });
      } finally {
        releaseScenarioLock?.();
        process.removeListener('exit', exitHandler);
        for (const [signal, handler] of signalHandlers.entries()) {
          process.removeListener(signal, handler);
        }
      }
      return recorderError;
    })();
    return finalizationPromise;
  };

  const signalExitCode = {
    SIGINT: 130,
    SIGTERM: 143,
    SIGHUP: 129,
  };
  let handlingSignal = false;
  const signalHandlers = new Map();
  const exitHandler = () => {
    releaseScenarioLock?.();
  };
  process.once('exit', exitHandler);
  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    const handler = async () => {
      if (handlingSignal) {
        return;
      }
      handlingSignal = true;
      appendTrace('signal', { signal });
      try {
        await runRegisteredCleanup(`signal:${signal}`);
      } finally {
        await finalizeRunner();
        process.exit(signalExitCode[signal] || 1);
      }
    };
    signalHandlers.set(signal, handler);
    process.once(signal, handler);
  };

  const runner = {
    scenarioId,
    tier,
    runId,
    runDir,
    sessionDir,
    metadata,
    tracePath,
    steps,
    assertions,
    log(message, details) {
      appendTrace(message, details);
    },
    writeJson(name, value) {
      writeJson(path.join(runDir, name), value);
    },
    writeText(name, value) {
      fs.writeFileSync(path.join(runDir, name), value, 'utf8');
    },
    registerCleanup(name, fn) {
      if (typeof fn !== 'function') {
        throw new Error(`Cleanup handler ${name} is missing a function`);
      }
      const record = { name, fn };
      cleanupHandlers.push(record);
      return () => {
        const index = cleanupHandlers.indexOf(record);
        if (index >= 0) {
          cleanupHandlers.splice(index, 1);
        }
      };
    },
    async step(name, details, fn) {
      const actualDetails = typeof details === 'function' ? null : (details || null);
      const actualFn = typeof details === 'function' ? details : fn;
      if (typeof actualFn !== 'function') {
        throw new Error(`Scenario step ${name} is missing a function`);
      }
      const startedAt = Date.now();
      appendTrace(`step:start ${name}`, actualDetails || undefined);
      const record = {
        name,
        startedAt: new Date(startedAt).toISOString(),
        endedAt: null,
        durationMs: null,
        status: 'running',
        details: actualDetails,
      };
      steps.push(record);
      try {
        const result = await actualFn();
        const endedAt = Date.now();
        record.endedAt = new Date(endedAt).toISOString();
        record.durationMs = endedAt - startedAt;
        record.status = 'ok';
        appendTrace(`step:ok ${name}`, { durationMs: record.durationMs });
        return result;
      } catch (error) {
        const endedAt = Date.now();
        record.endedAt = new Date(endedAt).toISOString();
        record.durationMs = endedAt - startedAt;
        record.status = 'error';
        record.error = normalizeError(error);
        appendTrace(`step:error ${name}`, {
          durationMs: record.durationMs,
          error: normalizeError(error),
        });
        throw error;
      }
    },
    assert(condition, message, details = null) {
      const assertion = {
        ok: Boolean(condition),
        message,
        details,
        at: new Date().toISOString(),
      };
      assertions.push(assertion);
      appendTrace(condition ? 'assert:ok' : 'assert:fail', { message, details });
      if (!condition) {
        throw new Error(message);
      }
    },
    expectMockGitHub(url) {
      expectedMockGitHubURL = String(url);
      appendTrace('mock_github:expected', { url: expectedMockGitHubURL });
    },
    async finishSuccess(summary = {}) {
      const recorderError = await finalizeRunner();
      if (recorderError) throw recorderError;
      const ledger = collectTripwireLedger();
      if (ledger.length > 0) {
        const digest = formatTripwireFailure({ scenarioId, ledgerPath: tripwire.ledgerPath, lines: ledger });
        process.stdout.write(`${digest}\n`);
        throw new Error(digest);
      }
      const receipt = readDaemonReceipt();
      if (tripwire.armed && !(receipt?.headlessTasks === 'off' && receipt?.carriesMarker)) {
        const digest = formatReceiptFailure({
          scenarioId,
          receipt,
          marker: tripwire.marker,
          pidPath: tripwire.pidPath,
        });
        process.stdout.write(`${digest}\n`);
        throw new Error(digest);
      }
      const observedMockGitHub = observeMockGitHub();
      if (expectedMockGitHubURL && observedMockGitHub !== expectedMockGitHubURL) {
        const digest = formatMockGitHubFailure({
          scenarioId,
          expected: expectedMockGitHubURL,
          observed: observedMockGitHub,
          pidPath: tripwire.pidPath,
        });
        process.stdout.write(`${digest}\n`);
        throw new Error(digest);
      }
      const finalSummary = {
        ok: true,
        scenarioId,
        tier,
        runId,
        runDir,
        sessionDir,
        metadata,
        steps,
        assertions,
        ...headlessSwitchField(receipt),
        ...mockGitHubField(observedMockGitHub),
        ...summary,
      };
      const summaryPath = path.join(runDir, 'summary.json');
      writeJson(summaryPath, finalSummary);
      emitRunnerVerdict({
        ok: true,
        scenarioId,
        runId,
        failureCount: 0,
        firstFailure: null,
        artifactsDir: runDir,
        summaryPath,
        durationMs: Date.now() - runnerCreatedAt,
      });
      return finalSummary;
    },
    async finishFailure(error, summary = {}) {
      const recorderError = await finalizeRunner();
      const ledger = collectTripwireLedger();
      const finalSummary = {
        ok: false,
        scenarioId,
        tier,
        runId,
        runDir,
        sessionDir,
        metadata,
        steps,
        assertions,
        error: normalizeError(error),
        ...(recorderError && recorderError !== error
          ? { recordingError: normalizeError(recorderError) }
          : {}),
        ...headlessSwitchField(readDaemonReceipt()),
        ...mockGitHubField(observeMockGitHub()),
        ...(ledger.length > 0
          ? { agentTripwire: { count: ledger.length, ledgerPath: tripwire.ledgerPath, lines: ledger } }
          : {}),
        ...summary,
      };
      const summaryPath = path.join(runDir, 'failure.json');
      writeJson(summaryPath, finalSummary);
      const digest = buildFailureDigest({
        scenarioId,
        runId,
        steps,
        error,
        runDir,
        tripwire: ledger.length > 0 ? { ledgerPath: tripwire.ledgerPath, lines: ledger } : null,
      });
      fs.writeFileSync(path.join(runDir, 'failure-digest.txt'), `${digest}\n`, 'utf8');
      process.stdout.write(`--- failure digest ---\n${digest}\n--- end digest ---\n`);
      emitRunnerVerdict({
        ok: false,
        scenarioId,
        runId,
        failureCount: 1,
        firstFailure: summarizeFirstFailure(error),
        artifactsDir: runDir,
        summaryPath,
        durationMs: Date.now() - runnerCreatedAt,
      });
      return finalSummary;
    },
    async close() {
      const recorderError = await finalizeRunner();
      if (recorderError) throw recorderError;
    },
  };

  runner.writeJson('scenario.json', {
    scenarioId,
    tier,
    runId,
    runDir,
    sessionDir,
    metadata,
  });

  return runner;
}
