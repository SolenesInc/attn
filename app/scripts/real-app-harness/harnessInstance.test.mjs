import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, it } from 'node:test';
import {
  assertProductionRunAllowed,
  bundleIdentifierForInstance,
  currentHarnessInstance,
  defaultAppPathForInstance,
  defaultDaemonPortForInstance,
  defaultWSURLForInstance,
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
    assert.equal(currentHarnessInstance(), 'dev');
    assert.equal(defaultAppPathForInstance(), installedAppPath('attn-dev'));
    assert.equal(defaultDaemonPortForInstance(), 29849);
  });

  it('never targets production by omission (empty/default ATTN_INSTANCE ⇒ dev)', () => {
    process.env.ATTN_INSTANCE = '';
    assert.equal(currentHarnessInstance(), 'dev');
    process.env.ATTN_INSTANCE = 'default';
    assert.equal(currentHarnessInstance(), 'dev');
  });

  it('lets ATTN_HARNESS_INSTANCE override ATTN_INSTANCE', () => {
    process.env.ATTN_INSTANCE = 'agent7';
    process.env.ATTN_HARNESS_INSTANCE = 'agent9';
    assert.equal(currentHarnessInstance(), 'agent9');
  });

  it('treats an explicit empty/default ATTN_HARNESS_INSTANCE as the prod escape hatch', () => {
    process.env.ATTN_INSTANCE = 'agent7';
    process.env.ATTN_HARNESS_INSTANCE = '';
    assert.equal(currentHarnessInstance(), '');
    process.env.ATTN_HARNESS_INSTANCE = 'default';
    assert.equal(currentHarnessInstance(), '');
  });
});

describe('real-app harness production safety', () => {
  it('detects production from the empty instance, app path, bundle id, or websocket', () => {
    assert.equal(isProductionHarnessTarget({ instance: '' }), true);
    assert.equal(isProductionHarnessTarget({
      instance: 'dev',
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    }), true);
    assert.equal(isProductionHarnessTarget({ instance: 'dev', bundleId: 'com.attn.manager' }), true);
    assert.equal(isProductionHarnessTarget({ instance: 'dev', wsUrl: 'ws://127.0.0.1:9849/ws' }), true);
    assert.equal(isProductionHarnessTarget({
      instance: 'dev',
      appPath: path.join(os.homedir(), 'Applications', 'attn-dev.app'),
      bundleId: 'com.attn.manager.dev',
    }), false);
  });

  it('treats a named instance as an isolated world, not production', () => {
    assert.equal(isProductionHarnessTarget({ instance: 'agent7' }), false);
    assert.doesNotThrow(() => assertProductionRunAllowed({ instance: 'agent7' }, []));
    assert.equal(isProductionHarnessTarget({ instance: 'agent7', bundleId: 'com.attn.manager' }), true);
    assert.equal(isProductionHarnessTarget({
      instance: 'agent7',
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    }), true);
  });

  it('detects the prod app path case-insensitively (macOS filesystems)', () => {
    for (const name of ['Attn.app', 'ATTN.APP', 'attn.App']) {
      const appPath = path.join(os.homedir(), 'Applications', name);
      assert.equal(isProductionHarnessTarget({ instance: 'dev', appPath }), true);
      assert.equal(instanceForAppPath(appPath, 'dev'), '');
    }
  });

  it('detects production from a suffixless install tree too', () => {
    const treeRoot = path.join(os.homedir(), '.local', 'share');
    assert.equal(isProductionHarnessTarget({ instance: 'dev', appPath: path.join(treeRoot, 'attn') }), true);
    assert.equal(isProductionHarnessTarget({
      instance: 'dev',
      appPath: path.join(treeRoot, 'attn-dev'),
      bundleId: 'com.attn.manager.dev',
    }), false);
  });

  it('protects low-level macOS lifecycle operations', () => {
    assert.throws(() => new MacOSDriver({
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
      bundleId: 'com.attn.manager',
    }), /Refusing to run the real-app harness against production/);
  });

  it('protects low-level daemon and native-window operations', async () => {
    assert.throws(
      () => new DaemonObserver({ wsUrl: 'ws://127.0.0.1:9849/ws' }),
      /Refusing to run the real-app harness against production/,
    );
    await assert.rejects(
      getFrontWindowBounds('com.attn.manager'),
      /Refusing to run the real-app harness against production/,
    );
  });
});

describe('single authority (attn instance resolve)', { skip: !ATTN_BIN }, () => {
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
      assert.equal(resources.bundleId, r.bundleId);
      assert.equal(resources.appName, r.appName);
      assert.equal(resources.appPath, r.appPath);
      assert.equal(resources.appDaemon, r.appDaemon);
      assert.equal(resources.appCli, r.appCli);
      assert.equal(resources.appLocalDataDir, r.appLocalDataDir);
      assert.equal(resources.wsPort, Number(r.wsPort));
      assert.equal(resources.socket, r.socket);
      assert.equal(resources.dataDir, r.dataDir);
      assert.equal(resources.deepLinkScheme, r.deepLinkScheme);
      assert.equal(resources.mockGitHubPort, Number(r.mockGitHubPort));
      assert.equal(mockGitHubPortForInstance(instance), Number(r.mockGitHubPort));
      assert.equal(bundleIdentifierForInstance(instance), r.bundleId);
      assert.equal(defaultAppPathForInstance(instance), r.appPath);
      assert.equal(defaultDaemonPortForInstance(instance), Number(r.wsPort));
      assert.equal(defaultWSURLForInstance(instance), `ws://127.0.0.1:${r.wsPort}/ws`);
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
    for (const key of Object.keys(routing)) delete process.env[key];
  });

  it('clears every inherited routing override, not just the socket four', () => {
    const env = instanceCliEnv('agent7');
    assert.equal(env.ATTN_INSTANCE, 'agent7');
    for (const key of Object.keys(routing)) assert.equal(env[key], undefined);
  });
});
