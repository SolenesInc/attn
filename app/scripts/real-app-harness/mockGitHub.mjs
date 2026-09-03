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
export const MOCK_GITHUB_SIGNATURE = 'attn-harness-github';

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
  const disposition = started.replaced ? 'replaced a stale server on' : (started.started ? 'started' : 'reusing');
  log(`${disposition} ${target.url} as ${target.host} (pid ${started.pid}, identity ${started.identity})`);
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

export async function readMockGitHubStatus({ profile = currentHarnessProfile(), request = fetch } = {}) {
  const target = mockGitHubTarget(profile);
  const response = await request(`${target.url}/__control`);
  if (!response.ok) {
    throw new Error(`mock GitHub status: ${target.url}/__control returned ${response.status}`);
  }
  const status = await response.json();
  if (status?.mock !== MOCK_GITHUB_SIGNATURE) {
    throw new Error(`mock GitHub status: ${target.url} answered without the ${MOCK_GITHUB_SIGNATURE} signature`);
  }
  return status;
}
