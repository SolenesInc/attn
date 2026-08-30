import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { acquireScenarioLock, createScenarioRunner } from './scenarioRunner.mjs';

let tmpDir;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'scenario-runner-test-'));
  vi.stubEnv('ATTN_REAL_APP_SCENARIO_LOCK_PATH', path.join(tmpDir, 'scenario.lock'));
  vi.stubEnv('ATTN_REAL_APP_ARTIFACTS_DIR', tmpDir);
});

afterEach(() => {
  vi.unstubAllEnvs();
  fs.rmSync(tmpDir, { recursive: true, force: true });
});

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

describe('packaged app scenario lock', () => {
  it('lets a matrix reserve the shared lock while its child uses a private lock', () => {
    const matrixLock = path.join(tmpDir, 'matrix.lock');
    const childLock = path.join(tmpDir, 'child.lock');
    const releaseMatrix = acquireScenarioLock({
      scenarioId: 'SERIAL-MATRIX',
      tier: 'matrix',
      runId: 'matrix-run',
      runDir: tmpDir,
      appPath: '/tmp/test-attn.app',
    }, matrixLock);
    const releaseChild = acquireScenarioLock({
      scenarioId: 'TR-205',
      tier: 'test',
      runId: 'child-run',
      runDir: tmpDir,
      appPath: '/tmp/test-attn.app',
    }, childLock);

    expect(JSON.parse(fs.readFileSync(path.join(matrixLock, 'owner.json'), 'utf8')).scenarioId).toBe('SERIAL-MATRIX');
    expect(JSON.parse(fs.readFileSync(path.join(childLock, 'owner.json'), 'utf8')).scenarioId).toBe('TR-205');

    releaseChild();
    releaseMatrix();
    expect(fs.existsSync(childLock)).toBe(false);
    expect(fs.existsSync(matrixLock)).toBe(false);
  });

  function holdLock(lockDir, { scenarioId = 'TR-HOLDER', runId = 'holder-run' } = {}) {
    fs.mkdirSync(lockDir);
    fs.writeFileSync(path.join(lockDir, 'owner.json'), JSON.stringify({
      pid: process.pid,
      scenarioId,
      runId,
      startedAt: '2026-08-30T00:00:00.000Z',
    }), 'utf8');
  }

  it('waits for a live holder and acquires once it releases', () => {
    const lockDir = path.join(tmpDir, 'wait.lock');
    holdLock(lockDir);
    const logs = [];
    let sleeps = 0;
    const release = acquireScenarioLock({
      scenarioId: 'TR-WAITER',
      tier: 'test',
      runId: 'waiter-run',
      runDir: tmpDir,
      appPath: '/tmp/test-attn.app',
    }, lockDir, {
      waitMs: 10_000,
      pollMs: 100,
      sleep: () => {
        sleeps += 1;
        if (sleeps === 2) {
          fs.rmSync(lockDir, { recursive: true, force: true });
        }
      },
      log: (message) => logs.push(message),
    });

    expect(sleeps).toBe(2);
    expect(logs).toHaveLength(1);
    expect(logs[0]).toContain('TR-WAITER: waiting for TR-HOLDER');
    expect(JSON.parse(fs.readFileSync(path.join(lockDir, 'owner.json'), 'utf8')).scenarioId).toBe('TR-WAITER');
    release();
    expect(fs.existsSync(lockDir)).toBe(false);
  });

  it('names the holder and the limit when the configured wait budget passes', () => {
    const lockDir = path.join(tmpDir, 'deadline.lock');
    holdLock(lockDir, { scenarioId: 'TR-STUCK', runId: 'stuck-run' });
    let clock = Date.now();
    expect(() => acquireScenarioLock({
      scenarioId: 'TR-WAITER',
      tier: 'test',
      runId: 'waiter-run',
      runDir: tmpDir,
      appPath: '/tmp/test-attn.app',
    }, lockDir, {
      waitMs: 5_000,
      pollMs: 2_000,
      now: () => clock,
      sleep: (ms) => { clock += ms; },
      log: () => {},
    })).toThrow(/gave up after 5000ms \(ATTN_REAL_APP_SCENARIO_LOCK_WAIT_MS\) waiting for TR-STUCK \(pid \d+, run stuck-run/);
    expect(JSON.parse(fs.readFileSync(path.join(lockDir, 'owner.json'), 'utf8')).scenarioId).toBe('TR-STUCK');
  });

  it('keeps waiting on a live heartbeating matrix holder far past any single-scenario budget', () => {
    const lockDir = path.join(tmpDir, 'matrix-wait.lock');
    holdLock(lockDir, { scenarioId: 'SERIAL-MATRIX', runId: 'matrix-run' });
    const ownerPath = path.join(lockDir, 'owner.json');
    let clock = Date.now();
    let sleeps = 0;
    const release = acquireScenarioLock({
      scenarioId: 'TR-WAITER',
      tier: 'test',
      runId: 'waiter-run',
      runDir: tmpDir,
      appPath: '/tmp/test-attn.app',
    }, lockDir, {
      pollMs: 60_000,
      now: () => clock,
      sleep: (ms) => {
        sleeps += 1;
        clock += ms;
        if (sleeps <= 30) {
          fs.utimesSync(ownerPath, new Date(clock), new Date(clock));
        } else {
          fs.rmSync(lockDir, { recursive: true, force: true });
        }
      },
      log: () => {},
    });

    expect(sleeps).toBe(31);
    expect(JSON.parse(fs.readFileSync(ownerPath, 'utf8')).scenarioId).toBe('TR-WAITER');
    release();
  });

  it('gives up on a live holder whose heartbeat has gone stale', () => {
    const lockDir = path.join(tmpDir, 'wedged.lock');
    holdLock(lockDir, { scenarioId: 'TR-WEDGED', runId: 'wedged-run' });
    let clock = Date.now();
    expect(() => acquireScenarioLock({
      scenarioId: 'TR-WAITER',
      tier: 'test',
      runId: 'waiter-run',
      runDir: tmpDir,
      appPath: '/tmp/test-attn.app',
    }, lockDir, {
      pollMs: 60_000,
      now: () => clock,
      sleep: (ms) => { clock += ms; },
      log: () => {},
    })).toThrow(/looks wedged: TR-WEDGED \(pid \d+.*has not heartbeat .* \(threshold 300000ms\)/);
    expect(JSON.parse(fs.readFileSync(path.join(lockDir, 'owner.json'), 'utf8')).scenarioId).toBe('TR-WEDGED');
  });

  it('forgives a single stale poll so a wake-from-sleep race does not fail the waiter', () => {
    const lockDir = path.join(tmpDir, 'wake.lock');
    holdLock(lockDir);
    const ownerPath = path.join(lockDir, 'owner.json');
    let clock = Date.now();
    let sleeps = 0;
    const release = acquireScenarioLock({
      scenarioId: 'TR-WAITER',
      tier: 'test',
      runId: 'waiter-run',
      runDir: tmpDir,
      appPath: '/tmp/test-attn.app',
    }, lockDir, {
      pollMs: 2_000,
      now: () => clock,
      sleep: (ms) => {
        sleeps += 1;
        clock += ms;
        if (sleeps === 1) {
          clock += 400_000;
        } else if (sleeps === 2) {
          fs.utimesSync(ownerPath, new Date(clock), new Date(clock));
        } else {
          fs.rmSync(lockDir, { recursive: true, force: true });
        }
      },
      log: () => {},
    });

    expect(sleeps).toBe(3);
    expect(JSON.parse(fs.readFileSync(ownerPath, 'utf8')).scenarioId).toBe('TR-WAITER');
    release();
  });

  it('heartbeats owner.json while held and stops on release', () => {
    vi.useFakeTimers();
    try {
      const lockDir = path.join(tmpDir, 'beat.lock');
      const release = acquireScenarioLock({
        scenarioId: 'TR-HOLDER',
        tier: 'test',
        runId: 'holder-run',
        runDir: tmpDir,
        appPath: '/tmp/test-attn.app',
      }, lockDir);
      const ownerPath = path.join(lockDir, 'owner.json');
      fs.utimesSync(ownerPath, new Date(1000), new Date(1000));
      vi.advanceTimersByTime(15_000);
      expect(fs.statSync(ownerPath).mtimeMs).toBeGreaterThan(1_000_000);
      release();
      expect(vi.getTimerCount()).toBe(0);
      expect(fs.existsSync(lockDir)).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it('fails fast without sleeping when the wait budget is zero', () => {
    const lockDir = path.join(tmpDir, 'failfast.lock');
    holdLock(lockDir);
    const sleep = vi.fn();
    expect(() => acquireScenarioLock({
      scenarioId: 'TR-WAITER',
      tier: 'test',
      runId: 'waiter-run',
      runDir: tmpDir,
      appPath: '/tmp/test-attn.app',
    }, lockDir, { waitMs: 0, sleep, log: () => {} })).toThrow(/single-tenant/);
    expect(sleep).not.toHaveBeenCalled();
  });

  it('still reclaims a dead holder immediately', () => {
    const lockDir = path.join(tmpDir, 'dead.lock');
    fs.mkdirSync(lockDir);
    fs.writeFileSync(path.join(lockDir, 'owner.json'), JSON.stringify({
      pid: 2 ** 30,
      scenarioId: 'TR-DEAD',
      runId: 'dead-run',
    }), 'utf8');
    const sleep = vi.fn();
    const release = acquireScenarioLock({
      scenarioId: 'TR-WAITER',
      tier: 'test',
      runId: 'waiter-run',
      runDir: tmpDir,
      appPath: '/tmp/test-attn.app',
    }, lockDir, { waitMs: 0, sleep, log: () => {} });
    expect(sleep).not.toHaveBeenCalled();
    expect(JSON.parse(fs.readFileSync(path.join(lockDir, 'owner.json'), 'utf8')).scenarioId).toBe('TR-WAITER');
    release();
  });
});

function runnerWithTripwire({
  scenarioId = 'tripwire-contract',
  ledger = [],
  verdicts = [],
  allowRealAgents = false,
  receipt = { headlessTasks: 'off', carriesMarker: true },
  ensureDaemonArmed = vi.fn(),
} = {}) {
  let ledgerPath = null;
  const runner = createScenarioRunner({
    appPath: '/tmp/test-attn.app',
    artifactsDir: path.join(tmpDir, 'artifacts'),
    sessionRootDir: path.join(tmpDir, 'sessions'),
  }, {
    scenarioId,
    tier: 'test',
    allowRealAgents,
  }, {
    assertBuildMatches: vi.fn(),
    armTripwire: vi.fn(({ runDir, allowRealAgents: declared }) => {
      ledgerPath = path.join(runDir, 'agent-tripwire.ledger');
      return {
        marker: 'test-marker',
        ledgerPath,
        armed: declared !== true,
        pidPath: path.join(tmpDir, 'attn.pid'),
        read: () => ledger,
        readReceipt: () => (declared === true ? null : receipt),
      };
    }),
    ensureDaemonArmed,
    emitRunnerVerdict: (verdict) => verdicts.push(verdict),
    isRecordingEnabled: () => false,
  });
  return { runner, verdicts, ledgerPath: () => ledgerPath };
}

describe('createScenarioRunner agent tripwire', () => {
  it('turns a clean ledger into the ordinary green verdict', async () => {
    const { runner, verdicts } = runnerWithTripwire();

    const summary = await runner.finishSuccess();

    expect(summary.ok).toBe(true);
    expect(verdicts[0]).toMatchObject({ ok: true });
  });

  it('refuses to pass a scenario whose ledger caught a real agent exec', async () => {
    const ledger = ['AGENT-QUEUE\tclaude --print hi', 'AGENT-QUEUE\tcodex exec'];
    const { runner, verdicts } = runnerWithTripwire({ scenarioId: 'AGENT-QUEUE', ledger });

    await expect(runner.finishSuccess()).rejects.toThrow('2 real agent exec(s) during AGENT-QUEUE');
    expect(verdicts).toEqual([]);

    const failure = await runner.finishFailure(new Error('agent tripwire tripped'));

    expect(failure.ok).toBe(false);
    expect(fs.existsSync(path.join(runner.runDir, 'summary.json'))).toBe(false);
    expect(fs.readFileSync(runner.tracePath, 'utf8')).toContain('agent_tripwire:tripped');
  });

  it('names the tripwire before the step the killed session failed', async () => {
    const ledger = ['TR-201\tclaude --session-id 93678622'];
    const { runner } = runnerWithTripwire({ scenarioId: 'TR-201', ledger });

    await runner.step('create_session', async () => {
      throw new Error('session not found: 93678622');
    }).catch(() => {});
    const failure = await runner.finishFailure(new Error('session not found: 93678622'));

    const digest = fs.readFileSync(path.join(runner.runDir, 'failure-digest.txt'), 'utf8');
    expect(digest.split('\n')[0]).toContain('1 real agent exec(s) during TR-201');
    expect(digest).toContain('TR-201\tclaude --session-id 93678622');
    expect(digest.indexOf('agent tripwire:')).toBeLessThan(digest.indexOf('failing step: create_session'));
    expect(failure.agentTripwire).toMatchObject({ count: 1, lines: ledger });
  });

  it('records that the daemon it ran against had headless tasks off', async () => {
    const { runner } = runnerWithTripwire();

    const summary = await runner.finishSuccess();

    expect(summary.headlessTasks).toBe('off');
    expect(JSON.parse(fs.readFileSync(path.join(runner.runDir, 'summary.json'), 'utf8')).headlessTasks).toBe('off');
  });

  it('omits the switch for a scenario that may run a real agent', async () => {
    const { runner } = runnerWithTripwire({ allowRealAgents: true });

    const summary = await runner.finishSuccess();

    expect(summary).not.toHaveProperty('headlessTasks');
  });

  it('refuses a green verdict when the daemon it ran against never carried the switch', async () => {
    for (const mode of ['on', 'no daemon', 'unreadable']) {
      const { runner } = runnerWithTripwire({
        scenarioId: 'NUDGE-TRIGGER',
        receipt: { headlessTasks: mode, carriesMarker: true },
      });

      await expect(runner.finishSuccess()).rejects.toThrow(`ATTN_HEADLESS_TASKS reads ${JSON.stringify(mode)}`);
      expect(fs.existsSync(path.join(runner.runDir, 'summary.json'))).toBe(false);
    }
  });

  it('refuses a green verdict when the daemon carried another run\'s shims', async () => {
    const { runner } = runnerWithTripwire({
      scenarioId: 'NUDGE-TRIGGER',
      receipt: { headlessTasks: 'off', carriesMarker: false },
    });

    await expect(runner.finishSuccess()).rejects.toThrow(/ATTN_AGENT_TRIPWIRE is not test-marker/);
  });

  it('names the failed receipt in the digest of the run it fails', async () => {
    const { runner } = runnerWithTripwire({
      scenarioId: 'NUDGE-TRIGGER',
      receipt: { headlessTasks: 'on', carriesMarker: true },
    });

    const error = await runner.finishSuccess().catch((thrown) => thrown);
    const failure = await runner.finishFailure(error);

    expect(failure.headlessTasks).toBe('on');
    const digest = fs.readFileSync(path.join(runner.runDir, 'failure-digest.txt'), 'utf8');
    expect(digest).toContain('ATTN_HEADLESS_TASKS reads "on"');
  });

  it('stops an armed scenario at construction when the daemon cannot be proved armed', () => {
    const unprovable = new Error('agent tripwire: the daemon it would run against cannot be proved armed');
    const ensureDaemonArmed = vi.fn(() => { throw unprovable; });

    expect(() => runnerWithTripwire({ scenarioId: 'NUDGE-TRIGGER', ensureDaemonArmed })).toThrow(unprovable);

    // The refusal has to hand back the single-tenant lock, or the next run is
    // blocked by a scenario that never started.
    const { runner } = runnerWithTripwire({ scenarioId: 'NUDGE-TRIGGER' });
    expect(runner.runDir).toBeTruthy();
  });

  it('lets a scenario that may run real agents continue past an unreadable daemon', () => {
    const ensureDaemonArmed = vi.fn(() => { throw new Error('daemon environment unreadable'); });

    const { runner } = runnerWithTripwire({ allowRealAgents: true, ensureDaemonArmed });

    expect(runner.runDir).toBeTruthy();
  });

  it('refuses to build a runner no catalog entry and no allowance covers', () => {
    expect(() => createScenarioRunner({
      appPath: '/tmp/test-attn.app',
      artifactsDir: path.join(tmpDir, 'artifacts'),
      sessionRootDir: path.join(tmpDir, 'sessions'),
    }, {
      scenarioId: 'UNLISTED-PROBE',
      tier: 'test',
    }, {
      assertBuildMatches: vi.fn(),
      armTripwire: vi.fn(),
      ensureDaemonArmed: vi.fn(),
      emitRunnerVerdict: vi.fn(),
      isRecordingEnabled: () => false,
    })).toThrow(/UNLISTED-PROBE.*no scenarioCatalog\.mjs entry/s);
  });

  it('arms what the catalog says the scenario may still run for real', () => {
    const armTripwire = vi.fn(() => ({ marker: 'm', ledgerPath: '/dev/null', read: () => [], readReceipt: () => null }));
    createScenarioRunner({
      appPath: '/tmp/test-attn.app',
      artifactsDir: path.join(tmpDir, 'artifacts'),
      sessionRootDir: path.join(tmpDir, 'sessions'),
    }, {
      scenarioId: 'PI-AUTOMODE',
      tier: 'test',
    }, {
      assertBuildMatches: vi.fn(),
      armTripwire,
      ensureDaemonArmed: vi.fn(),
      emitRunnerVerdict: vi.fn(),
      isRecordingEnabled: () => false,
    });

    expect(armTripwire.mock.calls[0][0]).toMatchObject({ scenarioId: 'PI-AUTOMODE', allowRealAgents: ['pi'] });
  });
});

describe('createScenarioRunner recording contract', () => {
  it('emits no green verdict and turns recorder finalization failure into the run failure', async () => {
    const stopped = deferred();
    const verdicts = [];
    const recorder = {
      start: vi.fn(),
      stop: vi.fn(() => stopped.promise),
    };
    const runner = createScenarioRunner({
      appPath: '/tmp/test-attn.app',
      artifactsDir: path.join(tmpDir, 'artifacts'),
      sessionRootDir: path.join(tmpDir, 'sessions'),
    }, {
      scenarioId: 'recording-contract',
      tier: 'test',
      allowRealAgents: false,
    }, {
      assertBuildMatches: vi.fn(),
      armTripwire: () => ({ marker: 'test-marker', ledgerPath: path.join(tmpDir, 'ledger'), armed: true, read: () => [], readReceipt: () => ({ headlessTasks: 'off', carriesMarker: true }) }),
      ensureDaemonArmed: vi.fn(),
      createRecorder: () => recorder,
      createRecordingDriver: () => ({ mainWindowId: async () => 1 }),
      emitRunnerVerdict: (verdict) => verdicts.push(verdict),
      isRecordingEnabled: () => true,
    });

    const finishPromise = runner.finishSuccess();
    await Promise.resolve();
    expect(verdicts).toEqual([]);

    const recorderError = new Error('window recorder is stale');
    const rejectedFinish = expect(finishPromise).rejects.toBe(recorderError);
    stopped.reject(recorderError);
    await rejectedFinish;

    const failure = await runner.finishFailure(recorderError);
    expect(failure).toMatchObject({ ok: false, error: expect.stringContaining('window recorder is stale') });
    expect(verdicts).toHaveLength(1);
    expect(verdicts[0]).toMatchObject({ ok: false, firstFailure: 'window recorder is stale' });
    expect(fs.existsSync(path.join(runner.runDir, 'summary.json'))).toBe(false);
    expect(fs.existsSync(path.join(runner.runDir, 'failure.json'))).toBe(true);
  });
});
