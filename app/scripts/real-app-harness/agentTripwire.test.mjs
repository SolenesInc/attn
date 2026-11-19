import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, it, mock } from 'node:test';
import {
  agentTripwireLaunchEnv,
  applyTripwireEnv,
  armAgentTripwire,
  armedBinaries,
  ensureDaemonCarriesTripwire,
  HEADLESS_TASKS_VAR,
  readDaemonHeadlessSwitch,
  readDaemonTripwireReceipt,
  readTripwireLedger,
  TRIPWIRE_BINARIES,
  TRIPWIRE_EXIT_CODE,
  TRIPWIRE_MARKER_VAR,
  tripwireDir,
  tripwireMarker,
} from './agentTripwire.mjs';
import { MOCK_AGENT_EXECUTABLE } from './mockAgent.mjs';

let tmpDir;
let runDir;
let savedArtifactsDir;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'agent-tripwire-test-'));
  runDir = path.join(tmpDir, 'run');
  fs.mkdirSync(runDir, { recursive: true });
  savedArtifactsDir = process.env.ATTN_REAL_APP_ARTIFACTS_DIR;
  process.env.ATTN_REAL_APP_ARTIFACTS_DIR = tmpDir;
});

afterEach(() => {
  if (savedArtifactsDir === undefined) delete process.env.ATTN_REAL_APP_ARTIFACTS_DIR;
  else process.env.ATTN_REAL_APP_ARTIFACTS_DIR = savedArtifactsDir;
  fs.rmSync(tmpDir, { recursive: true, force: true });
});

function freshEnv() {
  return { PATH: '/usr/bin:/bin', ATTN_REAL_APP_ARTIFACTS_DIR: tmpDir };
}

describe('the shim a real agent exec lands in', () => {
  it('appends the scenario and argv to the armed ledger and refuses to run', () => {
    const env = freshEnv();
    const tripwire = armAgentTripwire({ scenarioId: 'NUDGE-TRIGGER', runDir, env, log: () => {} });

    const result = spawnSync(path.join(tripwire.dir, 'claude'), ['--print', 'hello world'], { encoding: 'utf8' });

    assert.equal(result.status, TRIPWIRE_EXIT_CODE);
    assert.ok(result.stderr.includes('NUDGE-TRIGGER'));
    assert.ok(result.stderr.includes(tripwire.ledgerPath));
    assert.deepEqual(tripwire.read(), ['NUDGE-TRIGGER\tclaude --print hello world']);
  });

  it('records an exec that lands between scenarios in the unattributed ledger', () => {
    const env = freshEnv();
    const tripwire = armAgentTripwire({ scenarioId: 'TR-201', runDir, env, log: () => {} });
    fs.rmSync(path.join(tripwire.dir, 'current-run'));

    const result = spawnSync(path.join(tripwire.dir, 'codex'), ['--version'], { encoding: 'utf8' });

    assert.equal(result.status, TRIPWIRE_EXIT_CODE);
    assert.deepEqual(tripwire.read(), []);
    assert.deepEqual(readTripwireLedger(path.join(tripwire.dir, 'unattributed.ledger')), [
      'unattributed\tcodex --version',
    ]);
  });
});

describe('what a scenario arms', () => {
  it('arms every agent binary by default', () => {
    assert.deepEqual(armedBinaries(undefined), TRIPWIRE_BINARIES);
  });

  it('arms the binaries a scenario did not name', () => {
    assert.deepEqual(armedBinaries(['codex']), ['claude']);
  });

  it('refuses a binary name it does not shim', () => {
    assert.throws(() => armedBinaries(['gemini']), /unknown agent binary "gemini"/);
  });
});

describe('how the tripwire reaches the app, the daemon and the CLI', () => {
  it('puts the shim dir first on PATH and pins each armed executable', () => {
    const env = freshEnv();
    const dir = tripwireDir();

    applyTripwireEnv(env, { dir, binaries: ['claude', 'codex'] });

    assert.equal(env.PATH.split(path.delimiter)[0], dir);
    // claude and codex run the mock instead of the shim, so an armed scenario
    // gets a deterministic agent; the shim still catches a name-resolved exec.
    assert.equal(env.ATTN_CLAUDE_EXECUTABLE, MOCK_AGENT_EXECUTABLE);
    assert.equal(env.ATTN_CODEX_EXECUTABLE, MOCK_AGENT_EXECUTABLE);
    assert.equal(env[TRIPWIRE_MARKER_VAR], tripwireMarker({ dir, binaries: ['claude', 'codex'] }));
  });

  it('hands the app launch the PATH and pins the daemon must inherit', () => {
    const env = freshEnv();
    const tripwire = armAgentTripwire({ scenarioId: 'TR-401', runDir, env, log: () => {} });

    assert.deepEqual(agentTripwireLaunchEnv(env), {
      PATH: env.PATH,
      [TRIPWIRE_MARKER_VAR]: tripwire.marker,
      [HEADLESS_TASKS_VAR]: 'off',
      ATTN_CLAUDE_EXECUTABLE: MOCK_AGENT_EXECUTABLE,
      ATTN_CODEX_EXECUTABLE: MOCK_AGENT_EXECUTABLE,
    });
  });
});

describe('the receipt that the switch was in force', () => {
  const pidPath = () => {
    const file = path.join(tmpDir, 'attn.pid');
    fs.writeFileSync(file, `${process.pid}\n`, 'utf8');
    return file;
  };

  it('reads the switch and this run\'s shims off the daemon the scenario ran against', () => {
    const tripwire = armAgentTripwire({
      scenarioId: 'NUDGE-TRIGGER',
      runDir,
      env: freshEnv(),
      readReceipt: ({ marker }) => readDaemonTripwireReceipt({
        marker,
        pidPath: pidPath(),
        readEnvironment: () => `PATH=/usr/bin ${TRIPWIRE_MARKER_VAR}=${marker} ${HEADLESS_TASKS_VAR}=off`,
      }),
      log: () => {},
    });

    assert.deepEqual(tripwire.readReceipt(), { headlessTasks: 'off', carriesMarker: true, mockGitHub: 'missing' });
  });

  it('says the daemon carries another run\'s shims when the marker differs', () => {
    assert.deepEqual(readDaemonTripwireReceipt({
      marker: 'shims|claude',
      pidPath: pidPath(),
      readEnvironment: () => `${TRIPWIRE_MARKER_VAR}=shims|codex ${HEADLESS_TASKS_VAR}=off`,
    }), { headlessTasks: 'off', carriesMarker: false, mockGitHub: 'missing' });
  });

  it('says on when the daemon never got the switch', () => {
    assert.equal(readDaemonHeadlessSwitch({
      pidPath: pidPath(),
      readEnvironment: () => 'PATH=/usr/bin',
    }), 'on');
  });

  it('says so rather than guessing when no daemon is running', () => {
    assert.equal(readDaemonHeadlessSwitch({
      pidPath: path.join(tmpDir, 'missing.pid'),
      readEnvironment: () => 'unused',
    }), 'no daemon');
  });
});

describe('the daemon a scenario inherits', () => {
  function pidFileFor(pid) {
    const pidPath = path.join(tmpDir, 'attn.pid');
    fs.writeFileSync(pidPath, `${pid}\n`, 'utf8');
    return pidPath;
  }

  const target = { instance: 'dev', appPath: '/tmp/attn-dev.app' };

  function stopCalls(run) {
    return run.mock.calls.filter(({ arguments: [, args] }) => args[0] === 'daemon' && args[1] === 'stop');
  }

  it('leaves a daemon that already carries this scenario marker alone', () => {
    const run = mock.fn();
    const result = ensureDaemonCarriesTripwire({
      ...target,
      marker: 'shims|claude',
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => `PATH=/usr/bin ${TRIPWIRE_MARKER_VAR}=shims|claude`,
      run,
      log: () => {},
    });

    assert.deepEqual(result, { restarted: false, reason: 'daemon already armed' });
    assert.equal(run.mock.callCount(), 0);
  });

  it('stops a daemon carrying this marker but not the headless switch', () => {
    const run = mock.fn(() => '');
    const result = ensureDaemonCarriesTripwire({
      ...target,
      marker: 'shims|claude',
      armed: true,
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => `PATH=/usr/bin ${TRIPWIRE_MARKER_VAR}=shims|claude`,
      run,
      log: () => {},
    });

    assert.deepEqual(result, { restarted: true, reason: 'daemon stopped' });
    assert.equal(stopCalls(run).length, 1);
  });

  it('stops a daemon that predates the tripwire so the app brings up an armed one', () => {
    const run = mock.fn(() => '');
    const result = ensureDaemonCarriesTripwire({
      ...target,
      marker: 'shims|claude',
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => 'PATH=/usr/bin',
      run,
      log: () => {},
    });

    assert.deepEqual(result, { restarted: true, reason: 'daemon stopped' });
    assert.equal(stopCalls(run).length, 1);
  });

  it('never stops a production daemon', () => {
    const run = mock.fn();
    const result = ensureDaemonCarriesTripwire({
      instance: '',
      appPath: '/Applications/attn.app',
      marker: 'shims|claude',
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => 'PATH=/usr/bin',
      run,
      log: () => {},
    });

    assert.deepEqual(result, { restarted: false, reason: 'target refuses a daemon restart' });
    assert.equal(run.mock.callCount(), 0);
  });

  it('refuses an armed scenario when the daemon environment cannot be read', () => {
    const run = mock.fn();

    assert.throws(() => ensureDaemonCarriesTripwire({
      ...target,
      scenarioId: 'NUDGE-TRIGGER',
      marker: 'shims|claude',
      armed: true,
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => { throw new Error('ps: permission denied'); },
      run,
      log: () => {},
    }), /NUDGE-TRIGGER[\s\S]*pid \d+[\s\S]*unreadable \(ps: permission denied\)[\s\S]*ATTN_HEADLESS_TASKS=off/);
    assert.equal(run.mock.callCount(), 0);
  });

  it('refuses an armed scenario rather than restarting a production daemon', () => {
    const run = mock.fn();

    assert.throws(() => ensureDaemonCarriesTripwire({
      instance: '',
      appPath: '/Applications/attn.app',
      scenarioId: 'NUDGE-TRIGGER',
      marker: 'shims|claude',
      armed: true,
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => 'PATH=/usr/bin',
      run,
      log: () => {},
    }), /target refuses a restart[\s\S]*production/);
    assert.equal(run.mock.callCount(), 0);
  });
});
