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
  writeTripwireShims,
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

  it('keeps one line per exec when the argv carries newlines and tabs', () => {
    const env = freshEnv();
    const tripwire = armAgentTripwire({ scenarioId: 'AGENT-QUEUE', runDir, env, log: () => {} });

    spawnSync(path.join(tripwire.dir, 'codex'), ['exec', 'first\nsecond\tthird'], { encoding: 'utf8' });

    expect(tripwire.read()).toEqual(['AGENT-QUEUE\tcodex exec first second third']);
  });

  it('names a huge argv without archiving it', () => {
    const env = freshEnv();
    const tripwire = armAgentTripwire({ scenarioId: 'TR-401', runDir, env, log: () => {} });

    spawnSync(path.join(tripwire.dir, 'claude'), ['--append-system-prompt', 'x'.repeat(9000)], { encoding: 'utf8' });

    const [line] = tripwire.read();
    expect(line.startsWith('TR-401\tclaude --append-system-prompt xxx')).toBe(true);
    expect(line).toMatch(/\.\.\. \(\+\d+ chars\)$/);
    expect(line.length).toBeLessThan(600);
  });

  it('records an exec that lands between scenarios in the unattributed ledger', () => {
    const env = freshEnv();
    const tripwire = armAgentTripwire({ scenarioId: 'TR-201', runDir, env, log: () => {} });
    fs.rmSync(path.join(tripwire.dir, 'current-run'));

    const result = spawnSync(path.join(tripwire.dir, 'copilot'), ['--version'], { encoding: 'utf8' });

    expect(result.status).toBe(TRIPWIRE_EXIT_CODE);
    expect(tripwire.read()).toEqual([]);
    expect(readTripwireLedger(path.join(tripwire.dir, 'unattributed.ledger'))).toEqual([
      'unattributed\tcopilot --version',
    ]);
  });
});

describe('what a scenario arms', () => {
  it('arms every agent binary by default', () => {
    expect(armedBinaries(undefined)).toEqual(TRIPWIRE_BINARIES);
  });

  it('arms nothing when a scenario allows real agents outright', () => {
    expect(armedBinaries(true)).toEqual([]);
  });

  it('arms the binaries a scenario did not name', () => {
    expect(armedBinaries(['pi'])).toEqual(['claude', 'codex', 'copilot']);
  });

  it('refuses a binary name it does not shim', () => {
    expect(() => armedBinaries(['gemini'])).toThrow('unknown agent binary "gemini"');
  });

  it('removes the shim of a binary a scenario allows', () => {
    const dir = tripwireDir();
    writeTripwireShims({ dir, binaries: TRIPWIRE_BINARIES });
    expect(fs.existsSync(path.join(dir, 'pi'))).toBe(true);

    writeTripwireShims({ dir, binaries: ['claude', 'codex', 'copilot'] });

    expect(fs.existsSync(path.join(dir, 'pi'))).toBe(false);
    expect(fs.existsSync(path.join(dir, 'claude'))).toBe(true);
  });
});

describe('how the tripwire reaches the app, the daemon and the CLI', () => {
  it('puts the shim dir first on PATH and pins each armed executable', () => {
    const env = freshEnv();
    const dir = tripwireDir();

    applyTripwireEnv(env, { dir, binaries: ['claude', 'codex', 'copilot'] });

    expect(env.PATH.split(path.delimiter)[0]).toBe(dir);
    // claude and codex run the mock instead of the shim, so an armed scenario
    // gets a deterministic agent; the shim still catches a name-resolved exec.
    expect(env.ATTN_CLAUDE_EXECUTABLE).toBe(MOCK_AGENT_EXECUTABLE);
    expect(env.ATTN_CODEX_EXECUTABLE).toBe(MOCK_AGENT_EXECUTABLE);
    expect(env.ATTN_COPILOT_EXECUTABLE).toBe(path.join(dir, 'copilot'));
    expect(env.ATTN_PI_EXECUTABLE).toBeUndefined();
    expect(env[TRIPWIRE_MARKER_VAR]).toBe(tripwireMarker({ dir, binaries: ['claude', 'codex', 'copilot'] }));
  });

  it('drops an executable pin left by a scenario that armed more', () => {
    const env = { ...freshEnv(), ATTN_PI_EXECUTABLE: '/gone/pi' };

    applyTripwireEnv(env, { dir: tripwireDir(), binaries: ['claude'] });

    expect(env.ATTN_PI_EXECUTABLE).toBeUndefined();
  });

  it('adds the shim dir once however many scenarios arm in one process', () => {
    const env = freshEnv();
    const dir = tripwireDir();

    applyTripwireEnv(env, { dir, binaries: TRIPWIRE_BINARIES });
    applyTripwireEnv(env, { dir, binaries: TRIPWIRE_BINARIES });

    expect(env.PATH.split(path.delimiter).filter((entry) => entry === dir)).toHaveLength(1);
  });

  it('hands the app launch nothing until a scenario arms', () => {
    expect(agentTripwireLaunchEnv({ PATH: '/usr/bin' })).toEqual({});
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
      ATTN_COPILOT_EXECUTABLE: path.join(tripwire.dir, 'copilot'),
      ATTN_PI_EXECUTABLE: path.join(tripwire.dir, 'pi'),
    });
  });

  it('leaves headless tasks on for a scenario the catalog lets run a real agent', () => {
    const env = freshEnv();

    armAgentTripwire({ scenarioId: 'PI-AUTOMODE', runDir, allowRealAgents: true, env, log: () => {} });

    expect(env[HEADLESS_TASKS_VAR]).toBeUndefined();
    expect(agentTripwireLaunchEnv(env)[HEADLESS_TASKS_VAR]).toBeUndefined();
  });

  it('clears the switch an earlier armed scenario set in the same shell', () => {
    const env = freshEnv();
    armAgentTripwire({ scenarioId: 'TR-401', runDir, env, log: () => {} });

    armAgentTripwire({ scenarioId: 'PI-AUTOMODE', runDir, allowRealAgents: true, env, log: () => {} });

    expect(env[HEADLESS_TASKS_VAR]).toBeUndefined();
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

    expect(tripwire.readReceipt()).toEqual({ headlessTasks: 'off', carriesMarker: true });
  });

  it('says the daemon carries another run\'s shims when the marker differs', () => {
    expect(readDaemonTripwireReceipt({
      marker: 'shims|claude',
      pidPath: pidPath(),
      readEnvironment: () => `${TRIPWIRE_MARKER_VAR}=shims|pi ${HEADLESS_TASKS_VAR}=off`,
    })).toEqual({ headlessTasks: 'off', carriesMarker: false });
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

  it('reports nothing for a scenario that may run a real agent', () => {
    const tripwire = armAgentTripwire({
      scenarioId: 'PI-AUTOMODE',
      runDir,
      allowRealAgents: true,
      env: freshEnv(),
      log: () => {},
    });

    expect(tripwire.readReceipt()).toBeNull();
  });
});

describe('the daemon a scenario inherits', () => {
  function pidFileFor(pid) {
    const pidPath = path.join(tmpDir, 'attn.pid');
    fs.writeFileSync(pidPath, `${pid}\n`, 'utf8');
    return pidPath;
  }

  const target = { profile: 'dev', appPath: '/tmp/attn-dev.app' };

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

  it('leaves an armed daemon alone once it also carries the headless switch', () => {
    const run = vi.fn();
    const result = ensureDaemonCarriesTripwire({
      ...target,
      marker: 'shims|claude',
      armed: true,
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => `PATH=/usr/bin ${TRIPWIRE_MARKER_VAR}=shims|claude ${HEADLESS_TASKS_VAR}=off`,
      run,
      log: () => {},
    });

    expect(result).toEqual({ restarted: false, reason: 'daemon already armed' });
    expect(run).not.toHaveBeenCalled();
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
      profile: '',
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
      profile: '',
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

  it('still only warns for a scenario that may run a real agent', () => {
    const run = vi.fn();
    const logged = [];
    const result = ensureDaemonCarriesTripwire({
      profile: '',
      appPath: '/Applications/attn.app',
      marker: 'shims|',
      pidPath: pidFileFor(process.pid),
      readEnvironment: () => 'PATH=/usr/bin',
      run,
      log: (message) => logged.push(message),
    });

    expect(result).toEqual({ restarted: false, reason: 'target refuses a daemon restart' });
    expect(logged.join('\n')).toContain('WARNING');
    expect(run).not.toHaveBeenCalled();
  });

  it('does nothing when no daemon is running', () => {
    const run = vi.fn();
    const result = ensureDaemonCarriesTripwire({
      ...target,
      marker: 'shims|claude',
      pidPath: path.join(tmpDir, 'missing.pid'),
      run,
      log: () => {},
    });

    expect(result).toEqual({ restarted: false, reason: 'no daemon running' });
    expect(run).not.toHaveBeenCalled();
  });
});
