import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  acquireSoakLock,
  formatSoakSummary,
  isRunFailure,
  iterationArtifactPaths,
  parseVerdictFromOutput,
  retainIterationEvidence,
  resetAfterIteration,
  summarizeSoak,
} from './run-soak.mjs';

const tempDirs = [];

afterEach(() => {
  vi.unstubAllEnvs();
  for (const dir of tempDirs.splice(0)) {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

const okVerdict = {
  ok: true,
  scenarioId: 'demo-scenario',
  runId: 'demo-scenario-2026-01-01T00-00-00-000Z',
  failureCount: 0,
  firstFailure: null,
  artifactsDir: '/tmp/attn-real-app-harness/demo-scenario-run',
  summaryPath: '/tmp/attn-real-app-harness/demo-scenario-run/summary.json',
  durationMs: 1234,
};

const failVerdict = {
  ...okVerdict,
  ok: false,
  failureCount: 1,
  firstFailure: 'assertion failed: pane not visible',
};

describe('parseVerdictFromOutput', () => {
  it('returns null when there is no verdict line', () => {
    expect(parseVerdictFromOutput('some\nordinary\nlog output\n')).toBeNull();
  });

  it('returns null for empty or non-string input', () => {
    expect(parseVerdictFromOutput('')).toBeNull();
    expect(parseVerdictFromOutput(undefined)).toBeNull();
    expect(parseVerdictFromOutput(null)).toBeNull();
  });

  it('parses the JSON payload of a single verdict line', () => {
    const output = `some log line\nATTN_VERDICT ${JSON.stringify(okVerdict)}\n`;

    expect(parseVerdictFromOutput(output)).toEqual(okVerdict);
  });

  it('takes the LAST verdict line when there are multiple', () => {
    const output = [
      `ATTN_VERDICT ${JSON.stringify(okVerdict)}`,
      'some trailing trace output',
      `ATTN_VERDICT ${JSON.stringify(failVerdict)}`,
    ].join('\n');

    expect(parseVerdictFromOutput(output)).toEqual(failVerdict);
  });

  it('returns null when the payload after the prefix is not valid JSON', () => {
    const output = 'ATTN_VERDICT {not valid json';

    expect(parseVerdictFromOutput(output)).toBeNull();
  });

  it('falls back to an earlier valid line if the last one is garbage', () => {
    const output = [`ATTN_VERDICT ${JSON.stringify(okVerdict)}`, 'ATTN_VERDICT {broken'].join('\n');

    // The contract is "last line wins" — if the last ATTN_VERDICT line is
    // garbage, the parse result is null, not a stale earlier verdict.
    expect(parseVerdictFromOutput(output)).toBeNull();
  });
});

describe('isRunFailure', () => {
  it('is false for a clean, non-timed-out run with an ok verdict', () => {
    expect(isRunFailure({ exitCode: 0, timedOut: false, verdict: okVerdict })).toBe(false);
  });

  it('is true when the exit code is non-zero', () => {
    expect(isRunFailure({ exitCode: 1, timedOut: false, verdict: okVerdict })).toBe(true);
  });

  it('is true when the run timed out even with exit code 0', () => {
    expect(isRunFailure({ exitCode: 0, timedOut: true, verdict: okVerdict })).toBe(true);
  });

  it('passes a run that exits 0 with no verdict line (pre-verdict-contract scenario)', () => {
    expect(isRunFailure({ exitCode: 0, timedOut: false, verdict: null })).toBe(false);
  });

  it('fails a run that exits non-zero with no verdict line', () => {
    expect(isRunFailure({ exitCode: 1, timedOut: false, verdict: null })).toBe(true);
  });

  it('is true when the parsed verdict says ok: false', () => {
    expect(isRunFailure({ exitCode: 0, timedOut: false, verdict: failVerdict })).toBe(true);
  });
});

describe('summarizeSoak', () => {
  it('reports ok when every run passed', () => {
    const records = [
      { iteration: 1, exitCode: 0, timedOut: false, verdict: okVerdict, durationMs: 10 },
      { iteration: 2, exitCode: 0, timedOut: false, verdict: okVerdict, durationMs: 10 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/attn-real-app-harness/soak-demo-scenario-2026-01-01T00-00-00-000Z',
      summaryPath: '/tmp/attn-real-app-harness/soak-demo-scenario-2026-01-01T00-00-00-000Z/soak-report.json',
      durationMs: 20,
    });

    expect(summary).toEqual({
      ok: true,
      scenarioId: 'soak:demo-scenario',
      runId: 'soak-demo-scenario-2026-01-01T00-00-00-000Z',
      failureCount: 0,
      firstFailure: null,
      artifactsDir: '/tmp/attn-real-app-harness/soak-demo-scenario-2026-01-01T00-00-00-000Z',
      summaryPath: '/tmp/attn-real-app-harness/soak-demo-scenario-2026-01-01T00-00-00-000Z/soak-report.json',
      durationMs: 20,
    });
  });

  it('counts failures and surfaces the first failing run\'s verdict firstFailure', () => {
    const records = [
      { iteration: 1, exitCode: 0, timedOut: false, verdict: okVerdict, durationMs: 10 },
      { iteration: 2, exitCode: 0, timedOut: false, verdict: failVerdict, durationMs: 10 },
      { iteration: 3, exitCode: 1, timedOut: false, verdict: null, durationMs: 5 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/run',
      summaryPath: '/tmp/run/soak-report.json',
      durationMs: 25,
    });

    expect(summary.ok).toBe(false);
    expect(summary.failureCount).toBe(2);
    expect(summary.firstFailure).toBe('assertion failed: pane not visible');
  });

  it('falls back to iteration/exit-code text when the first failure has no verdict', () => {
    const records = [
      { iteration: 1, exitCode: 1, timedOut: false, verdict: null, durationMs: 5 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/run',
      summaryPath: '/tmp/run/soak-report.json',
      durationMs: 5,
    });

    expect(summary.firstFailure).toBe('iteration 1 exit 1');
  });

  it('reports ok for exit-0 runs that never emitted a verdict line', () => {
    const records = [
      { iteration: 1, exitCode: 0, timedOut: false, verdict: null, verdictMissing: true, durationMs: 5 },
      { iteration: 2, exitCode: 0, timedOut: false, verdict: null, verdictMissing: true, durationMs: 5 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/run',
      summaryPath: '/tmp/run/soak-report.json',
      durationMs: 10,
    });

    expect(summary.ok).toBe(true);
    expect(summary.failureCount).toBe(0);
    expect(summary.firstFailure).toBeNull();
  });

  it('treats a timed-out run with exit code 0 as a failure', () => {
    const records = [
      { iteration: 1, exitCode: 0, timedOut: true, verdict: okVerdict, durationMs: 5 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/run',
      summaryPath: '/tmp/run/soak-report.json',
      durationMs: 5,
    });

    expect(summary.ok).toBe(false);
    expect(summary.failureCount).toBe(1);
  });
});

describe('formatSoakSummary', () => {
  it('writes every iteration and the aggregate failure count', () => {
    const markdown = formatSoakSummary([
      { iteration: 1, exitCode: 0, timedOut: false, verdict: okVerdict, durationMs: 1234 },
      { iteration: 2, exitCode: 1, timedOut: false, verdict: failVerdict, durationMs: 5678 },
      { iteration: 3, exitCode: 124, timedOut: true, verdict: null, durationMs: 120000 },
    ], {
      scenarioId: 'terminal-annotations',
      runnerClass: 'github-hosted 4vcpu/16GB',
    });

    expect(markdown).toContain('| 1 | passed | 1234 ms | github-hosted 4vcpu/16GB |');
    expect(markdown).toContain('| 2 | failed | 5678 ms | github-hosted 4vcpu/16GB |');
    expect(markdown).toContain('| 3 | timed out | 120000 ms | github-hosted 4vcpu/16GB |');
    expect(markdown).toContain('**2/3 failed for terminal-annotations.**');
  });
});

describe('retainIterationEvidence', () => {
  function artifactRoot() {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-run-soak-test-'));
    tempDirs.push(dir);
    return dir;
  }

  it('finds only artifacts created by the iteration and keeps tripwire state', () => {
    const root = artifactRoot();
    fs.mkdirSync(path.join(root, 'soak-report'));
    const before = new Set(fs.readdirSync(root));
    fs.mkdirSync(path.join(root, 'scenario-run'));
    fs.mkdirSync(path.join(root, 'agent-tripwire'));

    expect(iterationArtifactPaths(root, before)).toEqual([path.join(root, 'scenario-run')]);
  });

  it('removes passing artifacts and keeps failed or normal local-run evidence', () => {
    const root = artifactRoot();
    const passed = path.join(root, 'passed');
    const failed = path.join(root, 'failed');
    const local = path.join(root, 'local');
    fs.mkdirSync(passed);
    fs.mkdirSync(failed);
    fs.mkdirSync(local);

    expect(retainIterationEvidence([passed], { failed: false, failedEvidenceOnly: true })).toBe(false);
    expect(retainIterationEvidence([failed], { failed: true, failedEvidenceOnly: true })).toBe(true);
    expect(retainIterationEvidence([local], { failed: false, failedEvidenceOnly: false })).toBe(true);
    expect(fs.existsSync(passed)).toBe(false);
    expect(fs.existsSync(failed)).toBe(true);
    expect(fs.existsSync(local)).toBe(true);
  });
});

describe('acquireSoakLock', () => {
  it('reserves the shared lock and gives iterations a private child lock', () => {
    const lockPath = path.join(os.tmpdir(), 'soak-parent.lock');
    const release = () => {};
    const acquire = vi.fn(() => release);
    vi.stubEnv('ATTN_REAL_APP_SCENARIO_LOCK_PATH', 'previous.lock');

    expect(acquireSoakLock({
      scenarioId: 'demo-scenario',
      artifactsRoot: '/tmp/attn-real-app-harness',
      appPath: '/tmp/attn.app',
    }, { acquire, lockPath, childPid: 42 })).toBe(release);
    expect(acquire).toHaveBeenCalledWith({
      scenarioId: 'SOAK-demo-scenario',
      tier: 'soak',
      runId: 'soak-demo-scenario',
      runDir: '/tmp/attn-real-app-harness',
      appPath: '/tmp/attn.app',
    }, lockPath);
    expect(process.env.ATTN_REAL_APP_SCENARIO_LOCK_PATH).toBe(`${lockPath}.children-42`);
  });
});

describe('resetAfterIteration', () => {
  it('resets flagged non-production scenarios', async () => {
    const reset = vi.fn();

    await resetAfterIteration(
      { freshWorldAfter: true },
      { productionTarget: false, profile: 'test', appPath: '/tmp/attn.app' },
      reset,
    );
    expect(reset).toHaveBeenCalledWith({ profile: 'test', appPath: '/tmp/attn.app' });
  });

  it('keeps unflagged and production iterations intact', async () => {
    const reset = vi.fn();

    await resetAfterIteration({}, { productionTarget: false, profile: 'test', appPath: '/tmp/attn.app' }, reset);
    await resetAfterIteration(
      { freshWorldAfter: true },
      { productionTarget: true, profile: 'test', appPath: '/tmp/attn.app' },
      reset,
    );
    expect(reset).not.toHaveBeenCalled();
  });
});
