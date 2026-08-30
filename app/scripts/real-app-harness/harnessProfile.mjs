import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const DEV_PROFILE = 'dev';
const PROD_BUNDLE_ID = 'com.attn.manager';
const PROD_APP_NAME = 'attn.app';
const PROD_APP_TREE = 'attn';
const PROD_DAEMON_PORT = '9849';

// Profile name grammar — mirrors config.profileNamePattern on the Go side.
const PROFILE_NAME = /^[a-z0-9][a-z0-9-]{0,15}$/;

// Fast-path resources for prod ('') and dev. The drift guard in
// harnessProfile.test.mjs pins them to `attn profile resolve`.
const BUILTIN_RESOURCES = {
  '': {
    profile: '',
    bundleId: PROD_BUNDLE_ID,
    appName: 'attn',
    wsPort: 9849,
    socket: path.join(os.homedir(), '.attn', 'attn.sock'),
    dataDir: path.join(os.homedir(), '.attn'),
    deepLinkScheme: 'attn',
  },
  dev: {
    profile: 'dev',
    bundleId: 'com.attn.manager.dev',
    appName: 'attn-dev',
    wsPort: 29849,
    socket: path.join(os.homedir(), '.attn-dev', 'attn.sock'),
    dataDir: path.join(os.homedir(), '.attn-dev'),
    deepLinkScheme: 'attn-dev',
  },
};

function xdgDataHome() {
  return (process.env.XDG_DATA_HOME ?? '').trim() || path.join(os.homedir(), '.local', 'share');
}

function installedApp(appName) {
  if (process.platform === 'darwin') {
    const appPath = path.join(os.homedir(), 'Applications', `${appName}.app`);
    return {
      appPath,
      appExecutable: path.join(appPath, 'Contents', 'MacOS', 'app'),
      appDaemon: path.join(appPath, 'Contents', 'MacOS', 'attn'),
    };
  }
  const appPath = path.join(xdgDataHome(), appName);
  return {
    appPath,
    appExecutable: path.join(appPath, 'bin', 'attn-app'),
    appDaemon: path.join(appPath, 'bin', 'attn'),
  };
}

function normalizeProfile(raw) {
  const value = (raw ?? '').trim().toLowerCase();
  return value === 'default' ? '' : value;
}

export function currentHarnessProfile() {
  const override = process.env.ATTN_HARNESS_PROFILE;
  if (override !== undefined) {
    return normalizeProfile(override);
  }
  const base = normalizeProfile(process.env.ATTN_PROFILE);
  return base === '' ? DEV_PROFILE : base;
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
    `attn binary not found for profile resolution. Tried: ${candidates.join(', ')}. `
    + `Build it with 'make dev' (or 'go build -o ./attn ./cmd/attn'), or set ATTN_HARNESS_BIN.`,
  );
}

function resolveViaAuthority(profile) {
  const attn = resolveAttnBinaryPath();
  let stdout;
  try {
    stdout = execFileSync(attn, ['profile', 'resolve', '--profile', profile, '--json'], {
      encoding: 'utf8',
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`Failed to resolve profile '${profile}' via '${attn} profile resolve': ${message}`);
  }
  const resolved = JSON.parse(stdout);
  return {
    profile: resolved.profile,
    bundleId: resolved.bundleId,
    appName: resolved.appName,
    appPath: resolved.appPath,
    appExecutable: resolved.appExecutable,
    appDaemon: resolved.appDaemon,
    wsPort: Number(resolved.wsPort),
    socket: resolved.socket,
    dataDir: resolved.dataDir,
    deepLinkScheme: resolved.deepLinkScheme,
  };
}

export function resolveHarnessResources(profile = currentHarnessProfile()) {
  const key = normalizeProfile(profile);
  if (Object.prototype.hasOwnProperty.call(BUILTIN_RESOURCES, key)) {
    const builtin = BUILTIN_RESOURCES[key];
    return { ...builtin, ...installedApp(builtin.appName) };
  }
  if (!PROFILE_NAME.test(key)) {
    throw new Error(`Invalid attn profile name '${profile}' (expected ${PROFILE_NAME}).`);
  }
  if (!resourceCache.has(key)) {
    resourceCache.set(key, resolveViaAuthority(key));
  }
  return resourceCache.get(key);
}

export function bundleIdentifierForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).bundleId;
}

// macOS filesystems are case-insensitive: `Attn.app` is the prod bundle.
export function profileForAppPath(appPath, fallbackProfile = currentHarnessProfile()) {
  const appName = path.basename(appPath || '').toLowerCase();
  const match = /^attn(?:-([a-z0-9][a-z0-9-]{0,15}))?(?:\.app)?$/.exec(appName);
  if (match) return match[1] ?? '';
  return fallbackProfile;
}

export function bundleIdentifierForAppPath(appPath, fallbackProfile = currentHarnessProfile()) {
  return bundleIdentifierForProfile(profileForAppPath(appPath, fallbackProfile));
}

export function appExecutableForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).appExecutable;
}

export function appExecutableForAppPath(appPath, fallbackProfile = currentHarnessProfile()) {
  return appExecutableForProfile(profileForAppPath(appPath, fallbackProfile));
}

export function defaultAppPathForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).appPath;
}

export function defaultDaemonPortForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).wsPort;
}

export function dataDirForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).dataDir;
}

// The daemon refuses a client_hello without this token.
export function clientTokenForProfile(profile = currentHarnessProfile()) {
  const fromEnv = (process.env.ATTN_CLIENT_TOKEN ?? '').trim();
  if (fromEnv) return fromEnv;
  try {
    return fs.readFileSync(path.join(dataDirForProfile(profile), 'client-token'), 'utf8').trim();
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
    client_token: clientTokenForProfile(),
  };
}

export function socketPathForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).socket;
}

export function daemonPidFilePathForProfile(profile = currentHarnessProfile()) {
  return path.join(resolveHarnessResources(profile).dataDir, 'attn.pid');
}

export function defaultWSURLForProfile(profile = currentHarnessProfile()) {
  return `ws://127.0.0.1:${resolveHarnessResources(profile).wsPort}/ws`;
}

export function manifestPathForProfile(profile = currentHarnessProfile()) {
  const bundleId = resolveHarnessResources(profile).bundleId;
  if (process.platform === 'darwin') {
    return path.join(os.homedir(), 'Library', 'Application Support', bundleId, 'debug', 'ui-automation.json');
  }
  return path.join(xdgDataHome(), bundleId, 'debug', 'ui-automation.json');
}

export function deepLinkSchemeForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).deepLinkScheme;
}

// An attn-hosted shell exports ATTN_DATA_DIR and ATTN_WS_PORT, and `attn
// profile-env` clears neither: miss one and the command lands in prod ~/.attn.
const ROUTING_OVERRIDE_ENV = [
  'ATTN_DATA_DIR',
  'ATTN_WS_PORT',
  'ATTN_SOCKET_PATH',
  'ATTN_DB_PATH',
  'ATTN_CONFIG_PATH',
  'ATTN_PLUGIN_DIR',
];

export function profileCliEnv(profile = currentHarnessProfile(), extra = {}) {
  const env = { ...process.env, ATTN_PROFILE: profile, ...extra };
  for (const key of ROUTING_OVERRIDE_ENV) {
    if (!(key in extra)) delete env[key];
  }
  return env;
}

export function hasRunAgainstProdFlag(argv = process.argv.slice(2)) {
  return argv.includes('--run-against-prod');
}

export function isProductionHarnessTarget({
  appPath,
  bundleId,
  wsUrl,
  profile = currentHarnessProfile(),
} = {}) {
  let wsPort = '';
  try {
    wsPort = new URL(wsUrl).port;
  } catch {
  }
  const appName = path.basename(appPath || '').toLowerCase();
  return (
    profile === ''
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
