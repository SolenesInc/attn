import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'agent-tripwire-test-'));
  runDir = path.join(tmpDir, 'run');
  fs.mkdirSync(runDir, { recursive: true });
  vi.stubEnv('ATTN_REAL_APP_ARTIFACTS_DIR', tmpDir);
});

afterEach(() => {
  vi.unstubAllEnvs();
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

    expect(result.status).toBe(TRIPWIRE_EXIT_CODE);
    expect(result.stderr).toContain('NUDGE-TRIGGER');
    expect(result.stderr).toContain(tripwire.ledgerPath);
    expect(tripwire.read()).toEqual(['NUDGE-TRIGGER\tclaude --print hello world']);
  });

  it('records an exec that lands between scenarios in the unattributed ledger', () => {
    const env = freshEnv();
    const tripwire = armAgentTripwire({ scenarioId: 'TR-201', runDir, env, log: () => {} });
    fs.rmSync(path.join(tripwire.dir, 'current-run'));

    const result = spawnSync(path.join(tripwire.dir, 'codex'), ['--version'], { encoding: 'utf8' });

    expect(result.status).toBe(TRIPWIRE_EXIT_CODE);
    expect(tripwire.read()).toEqual([]);
    expect(readTripwireLedger(path.join(tripwire.dir, 'unattributed.ledger'))).toEqual([
      'unattributed\tcodex --version',
    ]);
  });
});

describe('what a scenario arms', () => {
  it('arms every agent binary by default', () => {
    expect(armedBinaries(undefined)).toEqual(TRIPWIRE_BINARIES);
  });

  it('arms the binaries a scenario did not name', () => {
    expect(armedBinaries(['codex'])).toEqual(['claude']);
  });

  it('refuses a binary name it does not shim', () => {
    expect(() => armedBinaries(['gemini'])).toThrow('unknown agent binary "gemini"');
  });
});

describe('how the tripwire reaches the app, the daemon and the CLI', () => {
  it('puts the shim dir first on PATH and pins each armed executable', () => {
    const env = freshEnv();
    const dir = tripwireDir();

    applyTripwireEnv(env, { dir, binaries: ['claude', 'codex'] });

    expect(env.PATH.split(path.delimiter)[0]).toBe(dir);
    // claude and codex run the mock instead of the shim, so an armed scenario
    // gets a deterministic agent; the shim still catches a name-resolved exec.
    expect(env.ATTN_CLAUDE_EXECUTABLE).toBe(MOCK_AGENT_EXECUTABLE);
    expect(env.ATTN_CODEX_EXECUTABLE).toBe(MOCK_AGENT_EXECUTABLE);
    expect(env[TRIPWIRE_MARKER_VAR]).toBe(tripwireMarker({ dir, binaries: ['claude', 'codex'] }));
  });

  it('hands the app launch the PATH and pins the daemon must inherit', () => {
    const env = freshEnv();
    const tripwire = armAgentTripwire({ scenarioId: 'TR-401', runDir, env, log: () => {} });

    expect(agentTripwireLaunchEnv(env)).toEqual({
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

    expect(tripwire.readReceipt()).toEqual({ headlessTasks: 'off', carriesMarker: true, mockGitHub: 'missing' });
  });

  it('says the daemon carries another run\'s shims when the marker differs', () => {
    expect(readDaemonTripwireReceipt({
      marker: 'shims|claude',
      pidPath: pidPath(),
      readEnvironment: () => `${TRIPWIRE_MARKER_VAR}=shims|codex ${HEADLESS_TASKS_VAR}=off`,
    })).toEqual({ headlessTasks: 'off', carriesMarker: false, mockGitHub: 'missing' });
  });

  it('says on when the daemon never got the switch', () => {
    expect(readDaemonHeadlessSwitch({
      pidPath: pidPath(),
      readEnvironment: () => 'PATH=/usr/bin',
    })).toBe('on');
  });

  it('says so rather than guessing when no daemon is running', () => {
    expect(readDaemonHeadlessSwitch({
      pidPath: path.join(tmpDir, 'missing.pid'),
      readEnvironment: () => 'unused',
    })).toBe('no daemon');
  });
});

describe('the daemon a scenario inherits', () => {
  function pidFileFor(pid) {
    const pidPath = path.join(tmpDir, 'attn.pid');
    fs.writeFileSync(pidPath, `${pid}\n`, 'utf8');
    return pidPath;
  }

  const target = { instance: 'dev', appPath: '/tmp/attn-dev.app' };

  it('leaves a daemon that already carries this scenario marker alone', () => {
    const run = vi.fn();
    const result = ensureDaemonCarriesTripwire({
      ...target,
      marker: 'shims|claude',
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => `PATH=/usr/bin ${TRIPWIRE_MARKER_VAR}=shims|claude`,
      run,
      log: () => {},
    });

    expect(result).toEqual({ restarted: false, reason: 'daemon already armed' });
    expect(run).not.toHaveBeenCalled();
  });

  it('stops a daemon carrying this marker but not the headless switch', () => {
    const run = vi.fn();
    const result = ensureDaemonCarriesTripwire({
      ...target,
      marker: 'shims|claude',
      armed: true,
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => `PATH=/usr/bin ${TRIPWIRE_MARKER_VAR}=shims|claude`,
      run,
      log: () => {},
    });

    expect(result).toEqual({ restarted: true, reason: 'daemon stopped' });
    expect(run).toHaveBeenCalled();
  });

  it('stops a daemon that predates the tripwire so the app brings up an armed one', () => {
    const run = vi.fn();
    const result = ensureDaemonCarriesTripwire({
      ...target,
      marker: 'shims|claude',
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => 'PATH=/usr/bin',
      run,
      log: () => {},
    });

    expect(result).toEqual({ restarted: true, reason: 'daemon stopped' });
    expect(run).toHaveBeenCalledTimes(1);
    expect(run.mock.calls[0][1]).toEqual(['daemon', 'stop']);
  });

  it('never stops a production daemon', () => {
    const run = vi.fn();
    const result = ensureDaemonCarriesTripwire({
      instance: '',
      appPath: '/Applications/attn.app',
      marker: 'shims|claude',
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => 'PATH=/usr/bin',
      run,
      log: () => {},
    });

    expect(result).toEqual({ restarted: false, reason: 'target refuses a daemon restart' });
    expect(run).not.toHaveBeenCalled();
  });

  it('refuses an armed scenario when the daemon environment cannot be read', () => {
    const run = vi.fn();

    expect(() => ensureDaemonCarriesTripwire({
      ...target,
      scenarioId: 'NUDGE-TRIGGER',
      marker: 'shims|claude',
      armed: true,
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => { throw new Error('ps: permission denied'); },
      run,
      log: () => {},
    })).toThrow(/NUDGE-TRIGGER[\s\S]*pid \d+[\s\S]*unreadable \(ps: permission denied\)[\s\S]*ATTN_HEADLESS_TASKS=off/);
    expect(run).not.toHaveBeenCalled();
  });

  it('refuses an armed scenario rather than restarting a production daemon', () => {
    const run = vi.fn();

    expect(() => ensureDaemonCarriesTripwire({
      instance: '',
      appPath: '/Applications/attn.app',
      scenarioId: 'NUDGE-TRIGGER',
      marker: 'shims|claude',
      armed: true,
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => 'PATH=/usr/bin',
      run,
      log: () => {},
    })).toThrow(/a production daemon is never restarted/);
    expect(run).not.toHaveBeenCalled();
  });
});
