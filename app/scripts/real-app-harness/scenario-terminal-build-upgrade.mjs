#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import {
  readPaneText,
  sleep,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, dataDirForProfile, profileCliEnv } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HARNESS_DIR, '../../..');

const STAGED_SNAPSHOT_FORMAT = 'ffffffffffff';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 60_000, intervalMs = 250) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await sleep(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

function readRegistryEntry(dataDir, sessionId) {
  const workersDir = path.join(dataDir, 'workers');
  if (!fs.existsSync(workersDir)) return null;
  for (const instance of fs.readdirSync(workersDir)) {
    const file = path.join(workersDir, instance, 'registry', `${sessionId}.json`);
    if (!fs.existsSync(file)) continue;
    try {
      return JSON.parse(fs.readFileSync(file, 'utf8'));
    } catch {
      return null;
    }
  }
  return null;
}

function restartDaemon(appPath, profile) {
  const binary = path.join(appPath, 'Contents', 'MacOS', 'attn');
  // profileCliEnv, not a hand-built env: a harness driven from inside attn
  // inherits ATTN_DATA_DIR and would land on production ~/.attn.
  const env = profileCliEnv(profile);
  execFileSync(binary, ['daemon', 'stop'], { env, stdio: 'inherit' });
  execFileSync(binary, ['daemon', 'ensure'], { env, stdio: 'inherit' });
}

function daemonLogTail(dataDir, bytes = 200_000) {
  const file = path.join(dataDir, 'daemon.log');
  if (!fs.existsSync(file)) return '';
  const raw = fs.readFileSync(file);
  return raw.subarray(Math.max(0, raw.length - bytes)).toString('utf8');
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-build-upgrade.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  const dataDir = dataDirForProfile(profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-BUILD-UPGRADE',
    tier: 'tier1-local-shell',
    prefix: 'terminal-build-upgrade',
    metadata: {
      agent: 'shell',
      focus: 'a terminal-engine update swaps the pty-worker in place, keeping pid, PTY and child',
      profile,
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  let observer = new DaemonObserver({ wsUrl: options.wsUrl });

  runner.log(`[RealAppHarness] profile=${profile} dataDir=${dataDir} wsUrl=${options.wsUrl}`);

  let staged = false;
  let cleanedUp = false;
  const cleanUp = async () => {
    if (cleanedUp) return;
    cleanedUp = true;
    // Put the real tag back: a staged format left installed would upgrade every
    // session of this profile on the next daemon start.
    if (staged) {
      execFileSync('make', ['install-daemon', `PROFILE=${profile}`], {
        cwd: REPO_ROOT,
        stdio: 'inherit',
        env: profileCliEnv(profile),
      });
      restartDaemon(options.appPath, profile);
    }
    await client.quitApp().catch(() => {});
    observer.close();
  };
  runner.registerCleanup('scenario_cleanup', cleanUp);

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    const marker = `VT_BEFORE_${runner.runId.replace(/[^A-Za-z0-9]/g, '')}`;
    const session = await runner.step('shell_session_with_output', async () => {
      const cwd = path.join(runner.sessionDir, 'shell');
      fs.mkdirSync(cwd, { recursive: true });
      const sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `vt-upgrade-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
      });
      const workspace = await client.request('get_workspace', { sessionId });
      const pane = workspace.panes[0];
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, { timeoutMs: 20_000 });
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: `printf '${marker}\\n'`, submit: true });
      await pollFor(
        async () => (await readPaneText(client, sessionId, pane.paneId)).includes(marker),
        `pre-upgrade marker ${marker} on the pane`,
        20_000,
      );
      return { sessionId, paneId: pane.paneId };
    });

    const before = await runner.step('read_worker_pids', async () => {
      const entry = readRegistryEntry(dataDir, session.sessionId);
      runner.assert(entry?.worker_pid > 0 && entry?.child_pid > 0, 'worker registry has no pids', entry);
      runner.log(`[RealAppHarness] before: worker_pid=${entry.worker_pid} child_pid=${entry.child_pid}`);
      return entry;
    });

    await runner.step('stage_terminal_engine_update', async () => {
      staged = true;
      execFileSync(
        'make',
        ['install-daemon', `PROFILE=${profile}`, `SNAPSHOT_FORMAT=${STAGED_SNAPSHOT_FORMAT}`],
        { cwd: REPO_ROOT, stdio: 'inherit', env: profileCliEnv(profile) },
      );
      restartDaemon(options.appPath, profile);
      await pollFor(
        async () => daemonLogTail(dataDir).includes(`terminal upgrade: session=${session.sessionId} worker swapped in place`),
        'the daemon to report the worker swapped in place',
        90_000,
        500,
      );
    });

    await runner.step('same_process_same_child', async () => {
      const entry = await pollFor(
        async () => {
          const current = readRegistryEntry(dataDir, session.sessionId);
          return current?.worker_pid > 0 ? current : null;
        },
        'the worker registry after the upgrade',
        30_000,
      );
      runner.log(`[RealAppHarness] after: worker_pid=${entry.worker_pid} child_pid=${entry.child_pid}`);
      runner.assert(
        entry.worker_pid === before.worker_pid,
        `worker pid changed across the upgrade (${before.worker_pid} -> ${entry.worker_pid}); execve must keep the process`,
      );
      runner.assert(
        entry.child_pid === before.child_pid,
        `the agent child was restarted (${before.child_pid} -> ${entry.child_pid}); the whole point is that it is not`,
      );
    });

    await runner.step('pane_survived_and_still_answers', async () => {
      await pollFor(
        async () => {
          const current = await readPaneText(client, session.sessionId, session.paneId).catch(() => '');
          return current.includes(marker) ? current : null;
        },
        `the pre-upgrade marker ${marker} still on the pane`,
        60_000,
      );

      const after = `VT_AFTER_${runner.runId.replace(/[^A-Za-z0-9]/g, '')}`;
      await pollFor(
        async () => {
          await client
            .request('write_pane', { sessionId: session.sessionId, paneId: session.paneId, text: `printf '${after}\\n'`, submit: true })
            .catch(() => {});
          const current = await readPaneText(client, session.sessionId, session.paneId).catch(() => '');
          return current.includes(after) ? current : null;
        },
        `the shell to answer after the upgrade (${after})`,
        60_000,
        2_000,
      );
    });

    await runner.step('no_stale_build_notice', async () => {
      observer.close();
      observer = new DaemonObserver({ wsUrl: options.wsUrl });
      await observer.connect();
      const state = await pollFor(
        async () => {
          const found = observer.getSession(session.sessionId);
          return found ? found : null;
        },
        'the session in the restarted daemon state',
        30_000,
      );
      runner.assert(
        state.terminal_build_stale !== true,
        'the session is still flagged terminal_build_stale after a successful swap; the user would see a reload notice for nothing',
        state,
      );
    });

    // Before finishSuccess, which releases the single-tenant scenario lock: the
    // restore reinstalls the daemon, and the next run must not overlap it.
    await cleanUp();
    await runner.finishSuccess();
  } catch (error) {
    await runner.finishFailure(error);
    throw error;
  } finally {
    await cleanUp();
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
