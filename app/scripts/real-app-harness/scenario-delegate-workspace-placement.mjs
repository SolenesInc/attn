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
import { waitForPaneVisible, waitForSessionWorkspace } from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, socketPathForProfile } from './harnessProfile.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function resolveAttnBin() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(HARNESS_DIR, '../../../attn')].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error('attn binary not found (build ./attn or set ATTN_HARNESS_BIN)');
}

function makeAttnRunner(attnBin, profile) {
  const socketPath = socketPathForProfile(profile);
  return function runAttn(args, { allowFailure = false } = {}) {
    try {
      const stdout = execFileSync(attnBin, args, {
        encoding: 'utf8',
        env: { ...process.env, ATTN_PROFILE: profile, ATTN_SOCKET_PATH: socketPath },
      });
      const brace = stdout.indexOf('{');
      return { stdout, status: 0, stderr: '', json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null };
    } catch (error) {
      if (!allowFailure) throw error;
      const stdout = typeof error.stdout === 'string' ? error.stdout : '';
      const stderr = typeof error.stderr === 'string' ? error.stderr : '';
      const brace = stdout.indexOf('{');
      return { status: error.status ?? 1, stdout, stderr, json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null };
    }
  };
}

function initRepo(dir) {
  fs.mkdirSync(dir, { recursive: true });
  execFileSync('git', ['init', '-q'], { cwd: dir });
  execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
    cwd: dir,
    env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
  });
  return fs.realpathSync(dir);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-delegate-workspace-placement.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the delegate-workspace-placement scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'DELEGATE-WORKSPACE-PLACEMENT',
    // The delegated pane must render a real codex session for the placement
    // assertions to mean anything.
    allowRealAgents: ['codex'],
    tier: 'tier2-local-real-agent',
    prefix: 'delegate-workspace-placement',
    metadata: {
      agent: 'codex',
      focus: '--workspace places the delegated pane; --no-worktree keeps the source session checkout, even when the target workspace lives in another repository',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let delegatorId = null;
  let neighborId = null;
  let workerId = null;

  runner.log(`[RealAppHarness] profile=${profile} runDir=${runner.runDir}`);

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { sourceRepo, otherRepo } = await runner.step('seed_two_repositories', async () => ({
      sourceRepo: initRepo(path.join(runner.sessionDir, 'source-repo')),
      otherRepo: initRepo(path.join(runner.sessionDir, 'other-repo')),
    }));

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('boot_delegator_in_source_repo', async () => {
      delegatorId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: sourceRepo,
        label: `src-${runner.runId}`,
        agent: 'shell',
        sessionWaitMs: 30_000,
      });
    });

    const targetWorkspaceId = await runner.step('boot_neighbor_in_other_repo', async () => {
      neighborId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: otherRepo,
        label: `oth-${runner.runId}`,
        agent: 'shell',
        sessionWaitMs: 30_000,
      });
      const list = runAttn(['list', '--json']);
      const neighbor = (list.json?.sessions ?? []).find((session) => session.id === neighborId);
      runner.assert(Boolean(neighbor?.workspace_id), `the neighbor session has a workspace (got ${JSON.stringify(neighbor)})`, neighbor);
      const workspace = (list.json?.workspaces ?? []).find((entry) => entry.id === neighbor.workspace_id);
      runner.assert(
        fs.realpathSync(workspace.directory) === otherRepo,
        `the target workspace is recorded at the other repository (got ${workspace?.directory}, want ${otherRepo})`,
        workspace,
      );
      return neighbor.workspace_id;
    });

    await runner.step('delegate_into_that_workspace_without_a_worktree', async () => {
      const delegate = runAttn([
        'delegate',
        '--source-session', delegatorId,
        '--workspace', targetWorkspaceId,
        '--no-worktree',
        '--allow-worktree-reuse',
        '--agent', 'codex',
        '--model', 'gpt-5.4-mini',
        '--brief', 'Workspace placement QA fixture. Please wait for direction; do not start coding.',
        '--name', `plc-${Date.now().toString(36).slice(-8)}`,
      ]);
      workerId = delegate.json?.session_id;
      runner.assert(typeof workerId === 'string' && workerId.length > 0, `delegate returned a worker session id (got ${JSON.stringify(delegate.json)})`, delegate.json);
      await observer.waitForSession({ id: workerId, timeoutMs: 30_000 });

      runner.assert(
        fs.realpathSync(delegate.json.directory) === sourceRepo,
        `the delegated agent runs in the source checkout (got ${delegate.json.directory}, want ${sourceRepo})`,
        delegate.json,
      );
      runner.assert(
        delegate.json.workspace_id === targetWorkspaceId,
        `the delegated pane still joined the target workspace (got ${delegate.json.workspace_id}, want ${targetWorkspaceId})`,
        delegate.json,
      );
      runner.assert(!delegate.json.worktree_created, 'no worktree was created', delegate.json);
    });

    await runner.step('the_app_shows_the_worker_in_the_source_checkout', async () => {
      const list = runAttn(['list', '--json']);
      const worker = (list.json?.sessions ?? []).find((session) => session.id === workerId);
      runner.assert(
        worker && fs.realpathSync(worker.directory) === sourceRepo && worker.workspace_id === targetWorkspaceId,
        `daemon state agrees: worker in ${sourceRepo} inside workspace ${targetWorkspaceId} (got ${JSON.stringify(worker)})`,
        worker,
      );

      await client.request('select_session', { sessionId: workerId });
      const workspace = await waitForSessionWorkspace(
        client,
        neighborId,
        (entry) => {
          const owners = (entry?.panes || []).map((pane) => pane.sessionId);
          return owners.includes(neighborId) && owners.includes(workerId);
        },
        'the app to render both the neighbor and the delegated pane in one workspace',
        60_000,
      );
      const workerPane = (workspace.panes || []).find((pane) => pane.sessionId === workerId);
      await waitForPaneVisible(client, workerId, workerPane.paneId, 60_000);
      const owners = (workspace.panes || []).map((pane) => pane.sessionId);
      runner.assert(
        owners.includes(neighborId) && owners.includes(workerId) && Boolean(workerPane?.paneId),
        `the app draws the delegated pane beside the ${path.basename(otherRepo)} session, while the agent edits ${path.basename(sourceRepo)}`,
        { panes: (workspace.panes || []).map((pane) => ({ paneId: pane.paneId, sessionId: pane.sessionId, title: pane.title })) },
      );
    });

    await runner.step('conflicting_repository_inputs_are_refused', async () => {
      const conflict = runAttn([
        'delegate',
        '--source-session', delegatorId,
        '--cwd', sourceRepo,
        '--repo', otherRepo,
        '--worktree', 'feat/placement-conflict',
        '--agent', 'codex',
        '--model', 'gpt-5.4-mini',
        '--brief', 'This delegation must be refused before an agent starts.',
      ], { allowFailure: true });
      const message = `${conflict.stdout}${conflict.stderr}`;
      runner.assert(conflict.status !== 0, `the conflicting delegation failed (status ${conflict.status})`, message);
      runner.assert(
        message.includes(sourceRepo) && message.includes(otherRepo) && message.includes('remove --repo'),
        'the refusal names both resolved repositories and how to correct it',
        message,
      );
    });

    const summary = await runner.finishSuccess({ profile, delegatorId, neighborId, workerId, targetWorkspaceId });
    console.log('[RealAppHarness] Delegate workspace-placement scenario passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { delegatorId, neighborId, workerId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of [workerId, neighborId, delegatorId]) {
      if (!sessionId) continue;
      await client.request('close_session', { sessionId }).catch((error) => console.warn('[delegate-workspace-placement] close_session failed: ' + (error instanceof Error ? error.message : String(error))));
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
