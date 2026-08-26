import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createScenarioRunner } from './scenarioRunner.mjs';

let tmpDir;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'scenario-runner-test-'));
  vi.stubEnv('ATTN_REAL_APP_SCENARIO_LOCK_PATH', path.join(tmpDir, 'scenario.lock'));
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
