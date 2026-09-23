import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  appLocalDataDirForInstance,
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  bundleIdentifierForInstance,
  currentHarnessInstance,
  daemonPidFilePathForInstance,
  dataDirForInstance,
  defaultAppPathForInstance,
  defaultDaemonPortForInstance,
  defaultWSURLForInstance,
  deepLinkSchemeForInstance,
  hasRunAgainstProdFlag,
  isProductionHarnessTarget,
  manifestPathForInstance,
  mockGitHubPortForInstance,
  instanceCliEnv,
  instanceForAppPath,
  resolveHarnessResources,
} from './harnessInstance.mjs';
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

const originalHarnessInstance = process.env.ATTN_HARNESS_INSTANCE;
const originalInstance = process.env.ATTN_INSTANCE;
const originalArgv = process.argv;

beforeEach(() => {
  delete process.env.ATTN_HARNESS_INSTANCE;
  delete process.env.ATTN_INSTANCE;
});

afterEach(() => {
  process.argv = originalArgv;
  for (const [name, value] of [
    ['ATTN_HARNESS_INSTANCE', originalHarnessInstance],
    ['ATTN_INSTANCE', originalInstance],
  ]) {
    if (value === undefined) delete process.env[name];
    else process.env[name] = value;
  }
});

describe('currentHarnessInstance (one-knob precedence)', () => {
  it('defaults to the safe dev sibling when neither knob is set', () => {
    expect(currentHarnessInstance()).toBe('dev');
    expect(defaultAppPathForInstance()).toBe(installedAppPath('attn-dev'));
    expect(defaultDaemonPortForInstance()).toBe(29849);
  });

  it('follows ATTN_INSTANCE when no harness override is set', () => {
    process.env.ATTN_INSTANCE = 'agent7';
    expect(currentHarnessInstance()).toBe('agent7');
  });

  it('never targets production by omission (empty/default ATTN_INSTANCE ⇒ dev)', () => {
    process.env.ATTN_INSTANCE = '';
    expect(currentHarnessInstance()).toBe('dev');
    process.env.ATTN_INSTANCE = 'default';
    expect(currentHarnessInstance()).toBe('dev');
  });

  it('lets ATTN_HARNESS_INSTANCE override ATTN_INSTANCE', () => {
    process.env.ATTN_INSTANCE = 'agent7';
    process.env.ATTN_HARNESS_INSTANCE = 'agent9';
    expect(currentHarnessInstance()).toBe('agent9');
  });

  it('treats an explicit empty/default ATTN_HARNESS_INSTANCE as the prod escape hatch', () => {
    process.env.ATTN_INSTANCE = 'agent7';
    process.env.ATTN_HARNESS_INSTANCE = '';
    expect(currentHarnessInstance()).toBe('');
    process.env.ATTN_HARNESS_INSTANCE = 'default';
    expect(currentHarnessInstance()).toBe('');
  });

  it('normalizes case and whitespace', () => {
    process.env.ATTN_HARNESS_INSTANCE = '  DEV  ';
    expect(currentHarnessInstance()).toBe('dev');
  });
});

describe('real-app harness production safety', () => {
  it('detects production from the empty instance, app path, bundle id, or websocket', () => {
    expect(isProductionHarnessTarget({ instance: '' })).toBe(true);
    expect(isProductionHarnessTarget({
      instance: 'dev',
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    })).toBe(true);
    expect(isProductionHarnessTarget({ instance: 'dev', bundleId: 'com.attn.manager' })).toBe(true);
    expect(isProductionHarnessTarget({ instance: 'dev', wsUrl: 'ws://127.0.0.1:9849/ws' })).toBe(true);
    expect(isProductionHarnessTarget({
      instance: 'dev',
      appPath: path.join(os.homedir(), 'Applications', 'attn-dev.app'),
      bundleId: 'com.attn.manager.dev',
    })).toBe(false);
  });

  it('treats a named instance as an isolated world, not production', () => {
    expect(isProductionHarnessTarget({ instance: 'agent7' })).toBe(false);
    expect(() => assertProductionRunAllowed({ instance: 'agent7' }, [])).not.toThrow();
    expect(isProductionHarnessTarget({ instance: 'agent7', bundleId: 'com.attn.manager' })).toBe(true);
    expect(isProductionHarnessTarget({
      instance: 'agent7',
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    })).toBe(true);
  });

  it('detects the prod app path case-insensitively (macOS filesystems)', () => {
    for (const name of ['Attn.app', 'ATTN.APP', 'attn.App']) {
      const appPath = path.join(os.homedir(), 'Applications', name);
      expect(isProductionHarnessTarget({ instance: 'dev', appPath })).toBe(true);
      expect(instanceForAppPath(appPath, 'dev')).toBe('');
    }
  });

  it('derives the matching instance and bundle from explicit packaged app paths', () => {
    const prodAppPath = path.join(os.homedir(), 'Applications', 'attn.app');
    const devAppPath = path.join(os.homedir(), 'Applications', 'attn-dev.app');
    const namedAppPath = path.join(os.homedir(), 'Applications', 'attn-agent7.app');

    expect(instanceForAppPath(prodAppPath)).toBe('');
    expect(bundleIdentifierForAppPath(prodAppPath)).toBe('com.attn.manager');
    expect(instanceForAppPath(devAppPath, '')).toBe('dev');
    expect(bundleIdentifierForAppPath(devAppPath, '')).toBe('com.attn.manager.dev');
    expect(instanceForAppPath(namedAppPath, '')).toBe('agent7');
  });

  it('derives the instance from a Linux install tree, which has no .app suffix', () => {
    const treeRoot = path.join(os.homedir(), '.local', 'share');
    expect(instanceForAppPath(path.join(treeRoot, 'attn'), 'dev')).toBe('');
    expect(instanceForAppPath(path.join(treeRoot, 'attn-lx'), '')).toBe('lx');
    expect(instanceForAppPath(path.join(treeRoot, 'attn-agent7'), '')).toBe('agent7');
    expect(instanceForAppPath(path.join(treeRoot, 'something-else'), 'dev')).toBe('dev');
  });

  it('detects production from a suffixless install tree too', () => {
    const treeRoot = path.join(os.homedir(), '.local', 'share');
    expect(isProductionHarnessTarget({ instance: 'dev', appPath: path.join(treeRoot, 'attn') })).toBe(true);
    expect(isProductionHarnessTarget({
      instance: 'dev',
      appPath: path.join(treeRoot, 'attn-dev'),
      bundleId: 'com.attn.manager.dev',
    })).toBe(false);
  });

  it('requires the explicit production acknowledgement flag', () => {
    expect(() => assertProductionRunAllowed({ instance: '' }, [])).toThrow(
      'Refusing to run the real-app harness against production',
    );
    expect(() => assertProductionRunAllowed({ instance: '' }, ['--run-against-prod'])).not.toThrow();
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
    expect(appLocalDataDirForInstance('dev')).toBe(expectedDir);
    expect(manifestPathForInstance('dev')).toBe(path.join(expectedDir, 'debug', 'ui-automation.json'));
  });
});

describe('daemon pid file resolution', () => {
  it('maps the dev instance to ~/.attn-dev/attn.pid and prod to ~/.attn/attn.pid', () => {
    expect(daemonPidFilePathForInstance('dev')).toBe(path.join(os.homedir(), '.attn-dev', 'attn.pid'));
    // instanceForAppPath() returns '' for the prod app; that resolves to ~/.attn.
    expect(daemonPidFilePathForInstance('')).toBe(path.join(os.homedir(), '.attn', 'attn.pid'));
  });
});

describeWithBinary('single authority (attn instance resolve)', () => {
  function resolve(instance) {
    const stdout = execFileSync(ATTN_BIN, ['instance', 'resolve', '--instance', instance, '--json'], {
      encoding: 'utf8',
    });
    return JSON.parse(stdout);
  }

  it('keeps every dev/prod fast-path literal in sync with the authority', () => {
    for (const instance of ['', 'dev']) {
      const r = resolve(instance);
      const resources = resolveHarnessResources(instance);
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
      expect(resources.mockGitHubPort).toBe(Number(r.mockGitHubPort));
      expect(mockGitHubPortForInstance(instance)).toBe(Number(r.mockGitHubPort));
      expect(bundleIdentifierForInstance(instance)).toBe(r.bundleId);
      expect(defaultAppPathForInstance(instance)).toBe(r.appPath);
      expect(defaultDaemonPortForInstance(instance)).toBe(Number(r.wsPort));
      expect(defaultWSURLForInstance(instance)).toBe(`ws://127.0.0.1:${r.wsPort}/ws`);
    }
  });

  it('round-trips a named instance: appPath ⇒ instance via the authority naming', () => {
    expect(instanceForAppPath(defaultAppPathForInstance('agent7'))).toBe('agent7');
    expect(instanceForAppPath(defaultAppPathForInstance('dev'))).toBe('dev');
    expect(instanceForAppPath(defaultAppPathForInstance(''))).toBe('');
  });

  it('resolves an arbitrary named instance from the authority', () => {
    const r = resolve('agent7');
    expect(bundleIdentifierForInstance('agent7')).toBe('com.attn.manager.agent7');
    expect(defaultAppPathForInstance('agent7')).toBe(installedAppPath('attn-agent7'));
    expect(deepLinkSchemeForInstance('agent7')).toBe('attn-agent7');
    expect(dataDirForInstance('agent7')).toBe(path.join(os.homedir(), '.attn-agent7'));
    expect(defaultDaemonPortForInstance('agent7')).toBe(Number(r.wsPort));
    expect(defaultDaemonPortForInstance('agent7')).not.toBe(9849);
    expect(defaultDaemonPortForInstance('agent7')).not.toBe(29849);
  });
});

describe('instanceCliEnv', () => {
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
    const env = instanceCliEnv('agent7');
    expect(env.ATTN_INSTANCE).toBe('agent7');
    for (const key of Object.keys(routing)) expect(env[key]).toBeUndefined();
  });

  it('keeps ATTN_DATA_DIR and ATTN_WS_PORT out, which an instance name cannot override', () => {
    const env = instanceCliEnv('agent7');
    expect('ATTN_DATA_DIR' in env).toBe(false);
    expect('ATTN_WS_PORT' in env).toBe(false);
  });

  it('lets an explicit extra set a routing value on purpose', () => {
    const env = instanceCliEnv('agent7', { ATTN_SOCKET_PATH: '/tmp/chosen.sock' });
    expect(env.ATTN_SOCKET_PATH).toBe('/tmp/chosen.sock');
    expect(env.ATTN_DATA_DIR).toBeUndefined();
  });

  it('clears them for the unnamed production instance too, which also names a destination', () => {
    const env = instanceCliEnv('');
    expect(env.ATTN_INSTANCE).toBe('');
    for (const key of Object.keys(routing)) expect(key in env).toBe(false);
  });

  it('names the overrides it dropped once per run, not once per child', async () => {
    vi.resetModules();
    const { instanceCliEnv: freshInstanceCliEnv } = await import('./harnessInstance.mjs');
    const log = vi.spyOn(console, 'log').mockImplementation(() => {});

    freshInstanceCliEnv('agent7');
    freshInstanceCliEnv('agent7');

    expect(log).toHaveBeenCalledTimes(1);
    const line = log.mock.calls[0][0];
    expect(line).toContain("instance 'agent7'");
    for (const key of Object.keys(routing)) expect(line).toContain(key);
  });

  it('says nothing when the shell carried no routing to drop', async () => {
    for (const key of Object.keys(routing)) delete process.env[key];
    vi.resetModules();
    const { instanceCliEnv: freshInstanceCliEnv } = await import('./harnessInstance.mjs');
    const log = vi.spyOn(console, 'log').mockImplementation(() => {});

    freshInstanceCliEnv('agent7');

    expect(log).not.toHaveBeenCalled();
  });
});
