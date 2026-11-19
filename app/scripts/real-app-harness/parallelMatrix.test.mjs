import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, it } from 'node:test';
import { acquireScenarioLock, instanceScenarioLockPath } from './scenarioRunner.mjs';

const RUNNER = path.join(path.dirname(fileURLToPath(import.meta.url)), 'run-parallel-matrix.mjs');

describe('parallel matrix', { skip: process.platform !== 'linux' }, () => {
  let root;
  let artifacts;
  let xvfbCalls;
  let instancePrefix;

  beforeEach(() => {
    root = fs.mkdtempSync(path.join(os.tmpdir(), 'parallel-matrix-test-'));
    artifacts = path.join(root, 'artifacts');
    xvfbCalls = path.join(root, 'xvfb-calls');
    instancePrefix = `pmtest${process.pid}x`;
    const bin = path.join(root, 'bin');
    fs.mkdirSync(bin);
    fs.writeFileSync(path.join(bin, 'Xvfb'), `#!/bin/sh\necho started >> "${xvfbCalls}"\nexit 1\n`, { mode: 0o755 });
  });

  afterEach(() => {
    fs.rmSync(root, { recursive: true, force: true });
  });

  function runMatrix(args, env = {}) {
    return spawnSync(process.execPath, [RUNNER, '--workers', '1', '--instance-prefix', instancePrefix, ...args], {
      encoding: 'utf8',
      env: {
        ...process.env,
        PATH: `${path.join(root, 'bin')}:${process.env.PATH}`,
        ATTN_REAL_APP_ARTIFACTS_DIR: artifacts,
        ...env,
      },
    });
  }

  function runFolders() {
    return fs.readdirSync(artifacts).filter((name) => name.startsWith('parallel-'));
  }

  function xvfbStarts() {
    return fs.existsSync(xvfbCalls) ? fs.readFileSync(xvfbCalls, 'utf8').split('\n').filter(Boolean).length : 0;
  }

  it('keeps an earlier run\'s failure evidence when a --failed-only rerun fails at startup', () => {
    const first = runMatrix(['--scenario', 'native-agent-queue']);
    assert.match(first.stderr, /Xvfb exited before naming its display/);
    const [firstRun] = runFolders();
    const evidenceDir = path.join(artifacts, firstRun, 'w1', 'native-agent-queue-run');
    fs.mkdirSync(evidenceDir);
    fs.writeFileSync(path.join(evidenceDir, 'failure-native.png'), 'the only screenshot of a transient failure');
    fs.writeFileSync(path.join(artifacts, 'last-matrix.json'), JSON.stringify({ results: [{ id: 'native-agent-queue', code: 1 }] }));

    const rerun = runMatrix(['--failed-only']);

    assert.ok(rerun.stdout.includes('--failed-only selected: native-agent-queue'));
    assert.match(rerun.stderr, /Xvfb exited before naming its display/);
    assert.equal(xvfbStarts(), 2);
    assert.equal(runFolders().length, 2);
    assert.equal(fs.readFileSync(path.join(evidenceDir, 'failure-native.png'), 'utf8'), 'the only screenshot of a transient failure');
  });

  it('leaves an instance alone while another run holds it', () => {
    const lockPath = instanceScenarioLockPath(`${instancePrefix}1`);
    const release = acquireScenarioLock({
      scenarioId: 'PARALLEL-MATRIX', tier: 'matrix', runId: 'active-run', runDir: root, appPath: null,
    }, lockPath, { waitMs: 0 });
    try {
      const second = runMatrix(['--scenario', 'native-agent-queue'], { ATTN_REAL_APP_SCENARIO_LOCK_WAIT_MS: '0' });

      assert.equal(second.status, 1);
      assert.match(second.stderr, /waiting for PARALLEL-MATRIX \(pid \d+, run active-run/);
      assert.equal(xvfbStarts(), 0);
      assert.equal((fs.existsSync(artifacts) ? runFolders() : []).length, 0);
      assert.equal(JSON.parse(fs.readFileSync(path.join(lockPath, 'owner.json'), 'utf8')).runId, 'active-run');
    } finally {
      release();
    }
  });
});
