import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createScenarioRunner } from './scenarioRunner.mjs';

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

function runnerWithTripwire({ scenarioId = 'tripwire-contract', ledger = [], verdicts = [], allowRealAgents } = {}) {
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
    armTripwire: vi.fn(({ runDir }) => {
      ledgerPath = path.join(runDir, 'agent-tripwire.ledger');
      return { marker: 'test-marker', ledgerPath, read: () => ledger };
    }),
    ensureDaemonArmed: vi.fn(),
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

  it('arms what the catalog says the scenario may still run for real', () => {
    const armTripwire = vi.fn(() => ({ marker: 'm', ledgerPath: '/dev/null', read: () => [] }));
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
    }, {
      assertBuildMatches: vi.fn(),
      armTripwire: () => ({ marker: 'test-marker', ledgerPath: path.join(tmpDir, 'ledger'), read: () => [] }),
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
