import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  currentHarnessProfile,
  defaultAppPathForProfile,
  isProductionHarnessTarget,
  mockGitHubPortForProfile,
} from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HARNESS_DIR, '../../..');

export const MOCK_GITHUB_SERVER = path.join(REPO_ROOT, 'scripts/mock-github.mjs');
export const MOCK_GITHUB_FIXTURE = path.join(HARNESS_DIR, 'fixtures/github-snapshot.json');
export const MOCK_GITHUB_HOST = 'mock.github.local';
export const MOCK_GITHUB_TOKEN = 'test-token';

// internal/daemon/daemon.go: ATTN_MOCK_GH_URL makes refreshGitHubHosts register
// this host and drop every other one, so `gh` discovery never runs.
export const MOCK_GITHUB_URL_VAR = 'ATTN_MOCK_GH_URL';
export const MOCK_GITHUB_VARS = [MOCK_GITHUB_URL_VAR, 'ATTN_MOCK_GH_TOKEN', 'ATTN_MOCK_GH_HOST'];

export function mockGitHubTarget(profile = currentHarnessProfile()) {
  const port = mockGitHubPortForProfile(profile);
  return { port, url: `http://127.0.0.1:${port}`, host: MOCK_GITHUB_HOST, token: MOCK_GITHUB_TOKEN };
}

export function applyMockGitHubEnv(env, target) {
  env[MOCK_GITHUB_URL_VAR] = target.url;
  env.ATTN_MOCK_GH_TOKEN = target.token;
  env.ATTN_MOCK_GH_HOST = target.host;
  return env;
}

// `open` drops env on macOS, so these have to be named for the spawn-style
// launch that carries them into the daemon the app starts.
export function mockGitHubLaunchEnv(env = process.env) {
  if (!env[MOCK_GITHUB_URL_VAR]) {
    return {};
  }
  return Object.fromEntries(MOCK_GITHUB_VARS.map((name) => [name, env[name]]));
}

function serverCommand(args, run) {
  return JSON.parse(run(process.execPath, [MOCK_GITHUB_SERVER, ...args], {
    encoding: 'utf8',
    cwd: REPO_ROOT,
    timeout: 30_000,
  }).trim());
}

// Production keeps its real GitHub: a mock there would empty the user's live PR
// list. Every other profile gets one mock per port, shared by every scenario.
export function ensureMockGitHubServer({
  profile = currentHarnessProfile(),
  appPath = defaultAppPathForProfile(profile),
  env = process.env,
  fixture = MOCK_GITHUB_FIXTURE,
  run = execFileSync,
  log = (message) => console.log(`[mock-github] ${message}`),
} = {}) {
  if (isProductionHarnessTarget({ profile, appPath })) {
    log('skipped: production target keeps the real github.com');
    return null;
  }
  const target = mockGitHubTarget(profile);
  const started = serverCommand(
    ['--ensure', '--port', String(target.port), '--host', target.host, '--fixture', fixture],
    run,
  );
  applyMockGitHubEnv(env, target);
  log(`${started.started ? 'started' : 'reusing'} ${target.url} as ${target.host} (pid ${started.pid})`);
  return { ...target, ...started };
}

export function stopMockGitHubServer({
  profile = currentHarnessProfile(),
  appPath = defaultAppPathForProfile(profile),
  run = execFileSync,
  log = (message) => console.log(`[mock-github] ${message}`),
} = {}) {
  if (isProductionHarnessTarget({ profile, appPath })) {
    return null;
  }
  const target = mockGitHubTarget(profile);
  const result = serverCommand(['--stop', '--port', String(target.port)], run);
  log(result.stopped ? `stopped pid ${result.pid}` : `nothing to stop on ${target.url}`);
  return result;
}

export function readMockGitHubStatus({ profile = currentHarnessProfile() } = {}) {
  const target = mockGitHubTarget(profile);
  return fetch(`${target.url}/__control`).then((response) => response.json());
}
