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
  readTripwireLedger,
  TRIPWIRE_BINARIES,
  TRIPWIRE_EXIT_CODE,
  TRIPWIRE_MARKER_VAR,
  tripwireDir,
  tripwireMarker,
  writeTripwireShims,
} from './agentTripwire.mjs';

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
    expect(env.ATTN_CLAUDE_EXECUTABLE).toBe(path.join(dir, 'claude'));
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
      ATTN_CLAUDE_EXECUTABLE: path.join(tripwire.dir, 'claude'),
      ATTN_CODEX_EXECUTABLE: path.join(tripwire.dir, 'codex'),
      ATTN_COPILOT_EXECUTABLE: path.join(tripwire.dir, 'copilot'),
      ATTN_PI_EXECUTABLE: path.join(tripwire.dir, 'pi'),
    });
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
