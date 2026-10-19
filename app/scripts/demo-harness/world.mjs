import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { assertPackagedAppBuildMatchesCurrentSource } from '../real-app-harness/buildPreflight.mjs';
import { DaemonObserver } from '../real-app-harness/daemonObserver.mjs';
import { assertFreshWorldTargetSafe, ensureFreshWorld } from '../real-app-harness/freshWorld.mjs';
import {
  appLocalDataDirForInstance,
  dataDirForInstance,
  defaultAppPathForInstance,
} from '../real-app-harness/harnessInstance.mjs';
import { ensureMockGitHubServer, stopMockGitHubServer } from '../real-app-harness/mockGitHub.mjs';
import { setFrontWindowBounds } from '../real-app-harness/nativeWindowCapture.mjs';
import { appBuildIdentityInTree, appDaemonInTree, createWindowDriver } from '../real-app-harness/platform.mjs';
import { startStubModel } from '../real-app-harness/stubModel.mjs';
import { UiAutomationClient } from '../real-app-harness/uiAutomationClient.mjs';
import { writeAgentHome } from './agentHome.mjs';
import { createTurnRouter } from './turns.mjs';

const DEMO_DIR = path.dirname(fileURLToPath(import.meta.url));
export const DEMO_INSTANCE = 'demo';
export const REPOS_ROOT = path.join(os.homedir(), 'projects', 'attn-demo');
export const NOTEBOOK_ROOT = path.join(REPOS_ROOT, 'notebook');
export const WINDOW = { x: 80, y: 60, width: 1200, height: 800 };
const STREAM_PACE = { chars: 12, ms: 25 };
const THINK_MS = 1500;
const GITHUB_SELF_THROTTLE_MS = 60_000;

function git(cwd, ...args) {
  return execFileSync('git', ['-c', 'user.name=Demo', '-c', 'user.email=demo@example.com', ...args], { cwd, encoding: 'utf8' });
}

export function preflight(instance) {
  const appPath = defaultAppPathForInstance(instance);
  assertFreshWorldTargetSafe({ instance, appPath });
  const identityPath = appBuildIdentityInTree(appPath);
  const identity = fs.existsSync(identityPath) ? JSON.parse(fs.readFileSync(identityPath, 'utf8')) : null;
  if (identity?.demo !== true) {
    throw new Error(`${appPath} is not a demo build; run make install INSTANCE=${instance} DEMO=1`);
  }
  assertPackagedAppBuildMatchesCurrentSource({ appPath });
  if (fs.existsSync(REPOS_ROOT)) {
    throw new Error(`${REPOS_ROOT} already exists; the demo creates and removes it, so move it away first`);
  }
  return appPath;
}

function scrubInstanceData(instance) {
  for (const dir of [dataDirForInstance(instance), appLocalDataDirForInstance(instance)]) {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function worktreePath(repo, branch) {
  return path.join(fs.realpathSync(REPOS_ROOT), `${repo}--${branch}`);
}

function createRepos(repos) {
  const folders = [];
  for (const [name, { worktrees = [] }] of Object.entries(repos)) {
    const repo = path.join(REPOS_ROOT, name);
    fs.cpSync(path.join(DEMO_DIR, 'fixtures', 'repos', name), repo, { recursive: true });
    git(repo, 'init', '-q', '-b', 'main');
    git(repo, 'add', '.');
    git(repo, 'commit', '-qm', `Start ${name}`);
    folders.push(fs.realpathSync(repo), ...worktrees.map((branch) => worktreePath(name, branch)));
  }
  return folders;
}

async function createWorktrees(daemon, repos) {
  for (const [name, { worktrees = [] }] of Object.entries(repos)) {
    for (const branch of worktrees) {
      const answered = daemon.waitForMessage(
        (message) => (message.event === 'create_worktree_result' ? message : null),
        `the worktree ${branch} of ${name}`,
        30_000,
      );
      daemon.send({ cmd: 'create_worktree', main_repo: fs.realpathSync(path.join(REPOS_ROOT, name)), branch, path: worktreePath(name, branch) });
      const result = await answered;
      if (!result.success) throw new Error(`creating the worktree ${branch} of ${name} failed: ${result.error}`);
    }
  }
}

export async function startWorld({ repos, log }) {
  const instance = DEMO_INSTANCE;
  process.env.ATTN_HARNESS_INSTANCE = instance;
  const appPath = preflight(instance);
  const cleanups = [];
  let stopping = null;
  const world = {
    instance,
    appPath,
    repoPath: (repo, worktree) => fs.realpathSync(path.join(REPOS_ROOT, worktree ? `${repo}--${worktree}` : repo)),
    stop() {
      stopping ??= (async () => {
        for (const cleanup of cleanups.reverse()) {
          try {
            await cleanup();
          } catch (error) {
            log(`cleanup failed: ${error?.message ?? error}`);
          }
        }
      })();
      return stopping;
    },
  };
  for (const [signal, code] of [['SIGINT', 130], ['SIGTERM', 143], ['SIGHUP', 129]]) {
    process.once(signal, () => {
      log(`${signal}: cleaning up`);
      world.stop().finally(() => process.exit(code));
    });
  }
  try {
    await ensureFreshWorld({ instance, appPath, log });
    scrubInstanceData(instance);

    cleanups.push(() => fs.rmSync(REPOS_ROOT, { recursive: true, force: true }));
    const trustedFolders = createRepos(repos);
    fs.mkdirSync(NOTEBOOK_ROOT);
    trustedFolders.push(fs.realpathSync(NOTEBOOK_ROOT));

    world.router = createTurnRouter({ thinkMs: THINK_MS });
    const stub = await startStubModel(world.router.route, {
      pace: STREAM_PACE,
      onError: (error) => log(`stub model: ${error?.message ?? error}`),
    });
    cleanups.push(() => stub.close());

    const toolHome = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-demo-home-'));
    cleanups.push(() => fs.rmSync(toolHome, { recursive: true, force: true }));
    const launchEnv = {
      ...writeAgentHome({ toolHome, stubOrigin: stub.origin, trustedFolders }),
      ATTN_WRAPPER_PATH: appDaemonInTree(appPath),
    };
    ensureMockGitHubServer({ instance, appPath, env: launchEnv, log });
    cleanups.push(() => stopMockGitHubServer({ instance, appPath, log }));

    cleanups.push(() => ensureFreshWorld({ instance, appPath, log }));
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '0';
    const client = new UiAutomationClient({ appPath, launchEnv });
    await client.launchFreshApp();
    await client.waitForManifest(30_000);
    await client.waitForReady(30_000);
    await client.waitForFrontendResponsive(30_000);
    await client.request('dismiss_whats_new');
    await client.request('set_setting', { key: 'notebook.root', value: `~/${path.relative(os.homedir(), NOTEBOOK_ROOT)}` });
    await client.request('dom_wait', { selector: '.dock-collapse-btn[aria-expanded="true"]', timeoutMs: 10_000 });
    await client.request('dom_click', { selector: '.dock-collapse-btn' });
    world.placeWindow = () => setFrontWindowBounds(WINDOW, { client });
    await world.placeWindow();
    const daemon = new DaemonObserver();
    await daemon.connect();
    world.githubThrottleEndsAt = Date.now() + GITHUB_SELF_THROTTLE_MS;
    cleanups.push(() => daemon.close());
    await createWorktrees(daemon, repos);
    world.client = client;
    world.driver = createWindowDriver({ appPath, client });
    return world;
  } catch (error) {
    await world.stop();
    throw error;
  }
}
