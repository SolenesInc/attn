import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const DEV_INSTANCE = 'dev';
const PROD_BUNDLE_ID = 'com.attn.manager';
const PROD_APP_NAME = 'attn.app';
const PROD_APP_TREE = 'attn';
const PROD_DAEMON_PORT = '9849';

// Instance name grammar — mirrors config.instanceNamePattern on the Go side.
const INSTANCE_NAME = /^[a-z0-9][a-z0-9-]{0,15}$/;

// Fast-path resources for prod ('') and dev. The drift guard in
// harnessInstance.test.mjs pins them to `attn instance resolve`.
const BUILTIN_RESOURCES = {
  '': {
    instance: '',
    bundleId: PROD_BUNDLE_ID,
    appName: 'attn',
    wsPort: 9849,
    socket: path.join(os.homedir(), '.attn', 'attn.sock'),
    dataDir: path.join(os.homedir(), '.attn'),
    deepLinkScheme: 'attn',
    mockGitHubPort: 19850,
  },
  dev: {
    instance: 'dev',
    bundleId: 'com.attn.manager.dev',
    appName: 'attn-dev',
    wsPort: 29849,
    socket: path.join(os.homedir(), '.attn-dev', 'attn.sock'),
    dataDir: path.join(os.homedir(), '.attn-dev'),
    deepLinkScheme: 'attn-dev',
    mockGitHubPort: 32556,
  },
};

function xdgDataHome() {
  return (process.env.XDG_DATA_HOME ?? '').trim() || path.join(os.homedir(), '.local', 'share');
}

function appLocalDataDir(bundleId) {
  if (process.platform === 'darwin') {
    return path.join(os.homedir(), 'Library', 'Application Support', bundleId);
  }
  return path.join(xdgDataHome(), bundleId);
}

// The executable inside an app tree the caller names, which `--app-path` can
// place anywhere; deriving it from an instance would answer for another install.
export function appExecutableInAppTree(appPath, platform = process.platform) {
  return platform === 'darwin'
    ? path.join(appPath, 'Contents', 'MacOS', 'app')
    : path.join(appPath, 'bin', 'attn-app');
}

function installedApp(appName) {
  if (process.platform === 'darwin') {
    const appPath = path.join(os.homedir(), 'Applications', `${appName}.app`);
    return {
      appPath,
      appExecutable: appExecutableInAppTree(appPath),
      appDaemon: path.join(appPath, 'Contents', 'MacOS', 'attn'),
    };
  }
  const appPath = path.join(xdgDataHome(), appName);
  return {
    appPath,
    appExecutable: appExecutableInAppTree(appPath),
    appDaemon: path.join(appPath, 'bin', 'attn'),
  };
}

function normalizeInstance(raw) {
  const value = (raw ?? '').trim().toLowerCase();
  return value === 'default' ? '' : value;
}

export function currentHarnessInstance() {
  const override = process.env.ATTN_HARNESS_INSTANCE;
  if (override !== undefined) {
    return normalizeInstance(override);
  }
  const base = normalizeInstance(process.env.ATTN_INSTANCE);
  return base === '' ? DEV_INSTANCE : base;
}

const resourceCache = new Map();

function resolveAttnBinaryPath() {
  const candidates = [
    process.env.ATTN_HARNESS_BIN,
    path.resolve(HARNESS_DIR, '../../../attn'),
  ].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error(
    `attn binary not found for instance resolution. Tried: ${candidates.join(', ')}. `
    + `Build it with 'make dev' (or 'go build -o ./attn ./cmd/attn'), or set ATTN_HARNESS_BIN.`,
  );
}

function resolveViaAuthority(instance) {
  const attn = resolveAttnBinaryPath();
  let stdout;
  try {
    stdout = execFileSync(attn, ['instance', 'resolve', '--instance', instance, '--json'], {
      encoding: 'utf8',
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`Failed to resolve instance '${instance}' via '${attn} instance resolve': ${message}`);
  }
  const resolved = JSON.parse(stdout);
  return {
    instance: resolved.instance,
    bundleId: resolved.bundleId,
    appName: resolved.appName,
    appPath: resolved.appPath,
    appExecutable: resolved.appExecutable,
    appDaemon: resolved.appDaemon,
    appLocalDataDir: resolved.appLocalDataDir,
    wsPort: Number(resolved.wsPort),
    socket: resolved.socket,
    dataDir: resolved.dataDir,
    deepLinkScheme: resolved.deepLinkScheme,
    mockGitHubPort: Number(resolved.mockGitHubPort),
  };
}

export function resolveHarnessResources(instance = currentHarnessInstance()) {
  const key = normalizeInstance(instance);
  if (Object.prototype.hasOwnProperty.call(BUILTIN_RESOURCES, key)) {
    const builtin = BUILTIN_RESOURCES[key];
    return {
      ...builtin,
      ...installedApp(builtin.appName),
      appLocalDataDir: appLocalDataDir(builtin.bundleId),
    };
  }
  if (!INSTANCE_NAME.test(key)) {
    throw new Error(`Invalid attn instance name '${instance}' (expected ${INSTANCE_NAME}).`);
  }
  if (!resourceCache.has(key)) {
    resourceCache.set(key, resolveViaAuthority(key));
  }
  return resourceCache.get(key);
}

export function bundleIdentifierForInstance(instance = currentHarnessInstance()) {
  return resolveHarnessResources(instance).bundleId;
}

// macOS filesystems are case-insensitive: `Attn.app` is the prod bundle.
export function instanceForAppPath(appPath, fallbackInstance = currentHarnessInstance()) {
  const appName = path.basename(appPath || '').toLowerCase();
  const match = /^attn(?:-([a-z0-9][a-z0-9-]{0,15}))?(?:\.app)?$/.exec(appName);
  if (match) return match[1] ?? '';
  return fallbackInstance;
}

export function bundleIdentifierForAppPath(appPath, fallbackInstance = currentHarnessInstance()) {
  return bundleIdentifierForInstance(instanceForAppPath(appPath, fallbackInstance));
}

export function appExecutableForInstance(instance = currentHarnessInstance()) {
  return resolveHarnessResources(instance).appExecutable;
}

export function appExecutableForAppPath(appPath, fallbackInstance = currentHarnessInstance()) {
  return appExecutableForInstance(instanceForAppPath(appPath, fallbackInstance));
}

export function defaultAppPathForInstance(instance = currentHarnessInstance()) {
  return resolveHarnessResources(instance).appPath;
}

export function defaultDaemonPortForInstance(instance = currentHarnessInstance()) {
  return resolveHarnessResources(instance).wsPort;
}

export function dataDirForInstance(instance = currentHarnessInstance()) {
  return resolveHarnessResources(instance).dataDir;
}

export function mockGitHubPortForInstance(instance = currentHarnessInstance()) {
  return resolveHarnessResources(instance).mockGitHubPort;
}

// The daemon refuses a client_hello without this token.
export function clientTokenForInstance(instance = currentHarnessInstance()) {
  const fromEnv = (process.env.ATTN_CLIENT_TOKEN ?? '').trim();
  if (fromEnv) return fromEnv;
  try {
    return fs.readFileSync(path.join(dataDirForInstance(instance), 'client-token'), 'utf8').trim();
  } catch {
    return '';
  }
}

export function harnessClientHello(clientKind, { version = 'real-app-harness', capabilities = ['workspace_sessions'] } = {}) {
  return {
    cmd: 'client_hello',
    client_kind: clientKind,
    version,
    capabilities,
    client_token: clientTokenForInstance(),
  };
}

export function socketPathForInstance(instance = currentHarnessInstance()) {
  return resolveHarnessResources(instance).socket;
}

export function daemonPidFilePathForInstance(instance = currentHarnessInstance()) {
  return path.join(resolveHarnessResources(instance).dataDir, 'attn.pid');
}

export function defaultWSURLForInstance(instance = currentHarnessInstance()) {
  return `ws://127.0.0.1:${resolveHarnessResources(instance).wsPort}/ws`;
}

export function appLocalDataDirForInstance(instance = currentHarnessInstance()) {
  return resolveHarnessResources(instance).appLocalDataDir;
}

export function manifestPathForInstance(instance = currentHarnessInstance()) {
  return path.join(appLocalDataDirForInstance(instance), 'debug', 'ui-automation.json');
}

export function deepLinkSchemeForInstance(instance = currentHarnessInstance()) {
  return resolveHarnessResources(instance).deepLinkScheme;
}

// An attn-hosted shell exports all six, and each one outranks the instance name:
// miss one and the child lands in the hosting session's world, not the instance's.
const ROUTING_OVERRIDE_ENV = [
  'ATTN_DATA_DIR',
  'ATTN_WS_PORT',
  'ATTN_SOCKET_PATH',
  'ATTN_DB_PATH',
  'ATTN_CONFIG_PATH',
  'ATTN_PLUGIN_DIR',
];

let routingDropAnnounced = false;

function announceRoutingDrop(instance, dropped) {
  if (routingDropAnnounced || dropped.length === 0) return;
  routingDropAnnounced = true;
  console.log(
    `[harness-instance] instance '${instance || 'default'}': dropped inherited routing overrides from `
    + `every child environment: ${dropped.join(', ')}`,
  );
}

// Every instance names a destination, the empty one (production) included, so the
// shell's own routing always goes; only an explicit extra survives.
export function instanceCliEnv(instance = currentHarnessInstance(), extra = {}) {
  const env = { ...process.env, ATTN_INSTANCE: instance, ...extra };
  const dropped = [];
  for (const key of ROUTING_OVERRIDE_ENV) {
    if (key in extra || !(key in env)) continue;
    delete env[key];
    dropped.push(key);
  }
  announceRoutingDrop(instance, dropped);
  return env;
}

export function hasRunAgainstProdFlag(argv = process.argv.slice(2)) {
  return argv.includes('--run-against-prod');
}

export function isProductionHarnessTarget({
  appPath,
  bundleId,
  wsUrl,
  instance = currentHarnessInstance(),
} = {}) {
  let wsPort = '';
  try {
    wsPort = new URL(wsUrl).port;
  } catch {
  }
  const appName = path.basename(appPath || '').toLowerCase();
  return (
    instance === ''
    || appName === PROD_APP_NAME
    || appName === PROD_APP_TREE
    || bundleId === PROD_BUNDLE_ID
    || wsPort === PROD_DAEMON_PORT
  );
}

export function assertProductionRunAllowed(target = {}, argv = process.argv.slice(2)) {
  if (!isProductionHarnessTarget(target) || hasRunAgainstProdFlag(argv)) {
    return;
  }
  throw new Error(
    'Refusing to run the real-app harness against production. '
    + 'Use the dev install (default; run `make dev` first), or pass '
    + '`--run-against-prod` explicitly to allow production app or daemon operations.',
  );
}
