import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  assertProductionRunAllowed,
  bundleIdentifierForInstance,
  currentHarnessInstance,
  defaultAppPathForInstance,
  defaultDaemonPortForInstance,
  defaultWSURLForInstance,
  hasRunAgainstProdFlag,
  isProductionHarnessTarget,
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

beforeEach(() => {
  delete process.env.ATTN_HARNESS_INSTANCE;
  delete process.env.ATTN_INSTANCE;
});

afterEach(() => {
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

  it('protects low-level daemon and native-window operations', async () => {
    expect(() => new DaemonObserver({ wsUrl: 'ws://127.0.0.1:9849/ws' })).toThrow(
      'Refusing to run the real-app harness against production',
    );
    await expect(getFrontWindowBounds('com.attn.manager')).rejects.toThrow(
      'Refusing to run the real-app harness against production',
    );
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

  it('clears them for the unnamed production instance too, which also names a destination', () => {
    const env = instanceCliEnv('');
    expect(env.ATTN_INSTANCE).toBe('');
    for (const key of Object.keys(routing)) expect(key in env).toBe(false);
  });
});
