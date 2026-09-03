import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  appLocalDataDirForProfile,
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  bundleIdentifierForProfile,
  currentHarnessProfile,
  daemonPidFilePathForProfile,
  dataDirForProfile,
  defaultAppPathForProfile,
  defaultDaemonPortForProfile,
  defaultWSURLForProfile,
  deepLinkSchemeForProfile,
  hasRunAgainstProdFlag,
  isProductionHarnessTarget,
  manifestPathForProfile,
  profileCliEnv,
  profileForAppPath,
  resolveHarnessResources,
} from './harnessProfile.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { getFrontWindowBounds } from './nativeWindowCapture.mjs';

const TEST_DIR = path.dirname(fileURLToPath(import.meta.url));

function xdgDataHome() {
  return (process.env.XDG_DATA_HOME ?? '').trim() || path.join(os.homedir(), '.local', 'share');
}

function installedAppPath(appName) {
  return process.platform === 'darwin'
    ? path.join(os.homedir(), 'Applications', `${appName}.app`)
    : path.join(xdgDataHome(), appName);
}

function attnBinary() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(TEST_DIR, '../../../attn')]
    .filter(Boolean);
  return candidates.find((candidate) => fs.existsSync(candidate)) ?? null;
}
const ATTN_BIN = attnBinary();
const describeWithBinary = ATTN_BIN ? describe : describe.skip;

const originalHarnessProfile = process.env.ATTN_HARNESS_PROFILE;
const originalProfile = process.env.ATTN_PROFILE;
const originalArgv = process.argv;

beforeEach(() => {
  delete process.env.ATTN_HARNESS_PROFILE;
  delete process.env.ATTN_PROFILE;
});

afterEach(() => {
  process.argv = originalArgv;
  for (const [name, value] of [
    ['ATTN_HARNESS_PROFILE', originalHarnessProfile],
    ['ATTN_PROFILE', originalProfile],
  ]) {
    if (value === undefined) delete process.env[name];
    else process.env[name] = value;
  }
});

describe('currentHarnessProfile (one-knob precedence)', () => {
  it('defaults to the safe dev sibling when neither knob is set', () => {
    expect(currentHarnessProfile()).toBe('dev');
    expect(defaultAppPathForProfile()).toBe(installedAppPath('attn-dev'));
    expect(defaultDaemonPortForProfile()).toBe(29849);
  });

  it('follows ATTN_PROFILE when no harness override is set', () => {
    process.env.ATTN_PROFILE = 'agent7';
    expect(currentHarnessProfile()).toBe('agent7');
  });

  it('never targets production by omission (empty/default ATTN_PROFILE ⇒ dev)', () => {
    process.env.ATTN_PROFILE = '';
    expect(currentHarnessProfile()).toBe('dev');
    process.env.ATTN_PROFILE = 'default';
    expect(currentHarnessProfile()).toBe('dev');
  });

  it('lets ATTN_HARNESS_PROFILE override ATTN_PROFILE', () => {
    process.env.ATTN_PROFILE = 'agent7';
    process.env.ATTN_HARNESS_PROFILE = 'agent9';
    expect(currentHarnessProfile()).toBe('agent9');
  });

  it('treats an explicit empty/default ATTN_HARNESS_PROFILE as the prod escape hatch', () => {
    process.env.ATTN_PROFILE = 'agent7';
    process.env.ATTN_HARNESS_PROFILE = '';
    expect(currentHarnessProfile()).toBe('');
    process.env.ATTN_HARNESS_PROFILE = 'default';
    expect(currentHarnessProfile()).toBe('');
  });

  it('normalizes case and whitespace', () => {
    process.env.ATTN_HARNESS_PROFILE = '  DEV  ';
    expect(currentHarnessProfile()).toBe('dev');
  });
});

describe('real-app harness production safety', () => {
  it('detects production from the empty profile, app path, bundle id, or websocket', () => {
    expect(isProductionHarnessTarget({ profile: '' })).toBe(true);
    expect(isProductionHarnessTarget({
      profile: 'dev',
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    })).toBe(true);
    expect(isProductionHarnessTarget({ profile: 'dev', bundleId: 'com.attn.manager' })).toBe(true);
    expect(isProductionHarnessTarget({ profile: 'dev', wsUrl: 'ws://127.0.0.1:9849/ws' })).toBe(true);
    expect(isProductionHarnessTarget({
      profile: 'dev',
      appPath: path.join(os.homedir(), 'Applications', 'attn-dev.app'),
      bundleId: 'com.attn.manager.dev',
    })).toBe(false);
  });

  it('treats a named profile as an isolated world, not production', () => {
    expect(isProductionHarnessTarget({ profile: 'agent7' })).toBe(false);
    expect(() => assertProductionRunAllowed({ profile: 'agent7' }, [])).not.toThrow();
    expect(isProductionHarnessTarget({ profile: 'agent7', bundleId: 'com.attn.manager' })).toBe(true);
    expect(isProductionHarnessTarget({
      profile: 'agent7',
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    })).toBe(true);
  });

  it('detects the prod app path case-insensitively (macOS filesystems)', () => {
    for (const name of ['Attn.app', 'ATTN.APP', 'attn.App']) {
      const appPath = path.join(os.homedir(), 'Applications', name);
      expect(isProductionHarnessTarget({ profile: 'dev', appPath })).toBe(true);
      expect(profileForAppPath(appPath, 'dev')).toBe('');
    }
  });

  it('derives the matching profile and bundle from explicit packaged app paths', () => {
    const prodAppPath = path.join(os.homedir(), 'Applications', 'attn.app');
    const devAppPath = path.join(os.homedir(), 'Applications', 'attn-dev.app');
    const namedAppPath = path.join(os.homedir(), 'Applications', 'attn-agent7.app');

    expect(profileForAppPath(prodAppPath)).toBe('');
    expect(bundleIdentifierForAppPath(prodAppPath)).toBe('com.attn.manager');
    expect(profileForAppPath(devAppPath, '')).toBe('dev');
    expect(bundleIdentifierForAppPath(devAppPath, '')).toBe('com.attn.manager.dev');
    expect(profileForAppPath(namedAppPath, '')).toBe('agent7');
  });

  it('derives the profile from a Linux install tree, which has no .app suffix', () => {
    const treeRoot = path.join(os.homedir(), '.local', 'share');
    expect(profileForAppPath(path.join(treeRoot, 'attn'), 'dev')).toBe('');
    expect(profileForAppPath(path.join(treeRoot, 'attn-lx'), '')).toBe('lx');
    expect(profileForAppPath(path.join(treeRoot, 'attn-agent7'), '')).toBe('agent7');
    expect(profileForAppPath(path.join(treeRoot, 'something-else'), 'dev')).toBe('dev');
  });

  it('detects production from a suffixless install tree too', () => {
    const treeRoot = path.join(os.homedir(), '.local', 'share');
    expect(isProductionHarnessTarget({ profile: 'dev', appPath: path.join(treeRoot, 'attn') })).toBe(true);
    expect(isProductionHarnessTarget({
      profile: 'dev',
      appPath: path.join(treeRoot, 'attn-dev'),
      bundleId: 'com.attn.manager.dev',
    })).toBe(false);
  });

  it('requires the explicit production acknowledgement flag', () => {
    expect(() => assertProductionRunAllowed({ profile: '' }, [])).toThrow(
      'Refusing to run the real-app harness against production',
    );
    expect(() => assertProductionRunAllowed({ profile: '' }, ['--run-against-prod'])).not.toThrow();
    expect(hasRunAgainstProdFlag(['--run-against-prod'])).toBe(true);
  });

  it('protects low-level macOS lifecycle operations', () => {
    expect(() => new MacOSDriver({
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
      bundleId: 'com.attn.manager',
    })).toThrow('Refusing to run the real-app harness against production');
  });

  it('targets the production bundle for an acknowledged production app path', () => {
    process.argv = [...process.argv, '--run-against-prod'];

    const driver = new MacOSDriver({
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    });

    expect(driver.bundleId).toBe('com.attn.manager');
  });

  it('protects low-level daemon and native-window operations', async () => {
    expect(() => new DaemonObserver({ wsUrl: 'ws://127.0.0.1:9849/ws' })).toThrow(
      'Refusing to run the real-app harness against production',
    );
    await expect(getFrontWindowBounds('com.attn.manager')).rejects.toThrow(
      'Refusing to run the real-app harness against production',
    );
  });
});

describe('ui automation manifest', () => {
  it('follows the platform app_local_data_dir', () => {
    const expectedDir = process.platform === 'darwin'
      ? path.join(os.homedir(), 'Library', 'Application Support', 'com.attn.manager.dev')
      : path.join(xdgDataHome(), 'com.attn.manager.dev');
    expect(appLocalDataDirForProfile('dev')).toBe(expectedDir);
    expect(manifestPathForProfile('dev')).toBe(path.join(expectedDir, 'debug', 'ui-automation.json'));
  });
});

describe('daemon pid file resolution', () => {
  it('maps the dev profile to ~/.attn-dev/attn.pid and prod to ~/.attn/attn.pid', () => {
    expect(daemonPidFilePathForProfile('dev')).toBe(path.join(os.homedir(), '.attn-dev', 'attn.pid'));
    // profileForAppPath() returns '' for the prod app; that resolves to ~/.attn.
    expect(daemonPidFilePathForProfile('')).toBe(path.join(os.homedir(), '.attn', 'attn.pid'));
  });
});

describeWithBinary('single authority (attn profile resolve)', () => {
  function resolve(profile) {
    const stdout = execFileSync(ATTN_BIN, ['profile', 'resolve', '--profile', profile, '--json'], {
      encoding: 'utf8',
    });
    return JSON.parse(stdout);
  }

  it('keeps every dev/prod fast-path literal in sync with the authority', () => {
    for (const profile of ['', 'dev']) {
      const r = resolve(profile);
      const resources = resolveHarnessResources(profile);
      expect(resources.bundleId).toBe(r.bundleId);
      expect(resources.appName).toBe(r.appName);
      expect(resources.appPath).toBe(r.appPath);
      expect(resources.appExecutable).toBe(r.appExecutable);
      expect(resources.appDaemon).toBe(r.appDaemon);
      expect(resources.appLocalDataDir).toBe(r.appLocalDataDir);
      expect(resources.wsPort).toBe(Number(r.wsPort));
      expect(resources.socket).toBe(r.socket);
      expect(resources.dataDir).toBe(r.dataDir);
      expect(resources.deepLinkScheme).toBe(r.deepLinkScheme);
      expect(bundleIdentifierForProfile(profile)).toBe(r.bundleId);
      expect(defaultAppPathForProfile(profile)).toBe(r.appPath);
      expect(defaultDaemonPortForProfile(profile)).toBe(Number(r.wsPort));
      expect(defaultWSURLForProfile(profile)).toBe(`ws://127.0.0.1:${r.wsPort}/ws`);
    }
  });

  it('round-trips a named profile: appPath ⇒ profile via the authority naming', () => {
    expect(profileForAppPath(defaultAppPathForProfile('agent7'))).toBe('agent7');
    expect(profileForAppPath(defaultAppPathForProfile('dev'))).toBe('dev');
    expect(profileForAppPath(defaultAppPathForProfile(''))).toBe('');
  });

  it('resolves an arbitrary named profile from the authority', () => {
    const r = resolve('agent7');
    expect(bundleIdentifierForProfile('agent7')).toBe('com.attn.manager.agent7');
    expect(defaultAppPathForProfile('agent7')).toBe(installedAppPath('attn-agent7'));
    expect(deepLinkSchemeForProfile('agent7')).toBe('attn-agent7');
    expect(dataDirForProfile('agent7')).toBe(path.join(os.homedir(), '.attn-agent7'));
    expect(defaultDaemonPortForProfile('agent7')).toBe(Number(r.wsPort));
    expect(defaultDaemonPortForProfile('agent7')).not.toBe(9849);
    expect(defaultDaemonPortForProfile('agent7')).not.toBe(29849);
  });
});

describe('profileCliEnv', () => {
  const routing = {
    ATTN_DATA_DIR: '/Users/nobody/.attn',
    ATTN_WS_PORT: '9849',
    ATTN_SOCKET_PATH: '/Users/nobody/.attn/attn.sock',
    ATTN_DB_PATH: '/Users/nobody/.attn/attn.db',
    ATTN_CONFIG_PATH: '/Users/nobody/.attn/config.json',
    ATTN_PLUGIN_DIR: '/Users/nobody/.attn/plugins',
  };

  beforeEach(() => {
    for (const [key, value] of Object.entries(routing)) process.env[key] = value;
  });

  afterEach(() => {
    vi.restoreAllMocks();
    for (const key of Object.keys(routing)) delete process.env[key];
  });

  it('clears every inherited routing override, not just the socket four', () => {
    const env = profileCliEnv('agent7');
    expect(env.ATTN_PROFILE).toBe('agent7');
    for (const key of Object.keys(routing)) expect(env[key]).toBeUndefined();
  });

  it('keeps ATTN_DATA_DIR and ATTN_WS_PORT out, which a profile name cannot override', () => {
    const env = profileCliEnv('agent7');
    expect('ATTN_DATA_DIR' in env).toBe(false);
    expect('ATTN_WS_PORT' in env).toBe(false);
  });

  it('lets an explicit extra set a routing value on purpose', () => {
    const env = profileCliEnv('agent7', { ATTN_SOCKET_PATH: '/tmp/chosen.sock' });
    expect(env.ATTN_SOCKET_PATH).toBe('/tmp/chosen.sock');
    expect(env.ATTN_DATA_DIR).toBeUndefined();
  });

  it('clears them for the unnamed production profile too, which also names a destination', () => {
    const env = profileCliEnv('');
    expect(env.ATTN_PROFILE).toBe('');
    for (const key of Object.keys(routing)) expect(key in env).toBe(false);
  });

  it('names the overrides it dropped once per run, not once per child', async () => {
    vi.resetModules();
    const { profileCliEnv: freshProfileCliEnv } = await import('./harnessProfile.mjs');
    const log = vi.spyOn(console, 'log').mockImplementation(() => {});

    freshProfileCliEnv('agent7');
    freshProfileCliEnv('agent7');

    expect(log).toHaveBeenCalledTimes(1);
    const line = log.mock.calls[0][0];
    expect(line).toContain("profile 'agent7'");
    for (const key of Object.keys(routing)) expect(line).toContain(key);
  });

  it('says nothing when the shell carried no routing to drop', async () => {
    for (const key of Object.keys(routing)) delete process.env[key];
    vi.resetModules();
    const { profileCliEnv: freshProfileCliEnv } = await import('./harnessProfile.mjs');
    const log = vi.spyOn(console, 'log').mockImplementation(() => {});

    freshProfileCliEnv('agent7');

    expect(log).not.toHaveBeenCalled();
  });
});
