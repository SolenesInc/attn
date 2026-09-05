#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  queryDaemonDb,
} from './common.mjs';
import { sleep, waitForFirstWorkspacePane, waitForPaneText } from './scenarioAssertions.mjs';
import { ensureClaudeInitialPanePromptReady } from './scenarioAgents.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, dataDirForProfile, profileCliEnv } from './harnessProfile.mjs';
import { appDaemonInTree } from './platform.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 300) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await sleep(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

// The app's bundled binary — daemon lifecycle must run through the exact build
// under test, not an unrelated ./attn on PATH.
function resolveAppBin(appPath) {
  const bin = appDaemonInTree(appPath);
  if (!fs.existsSync(bin)) throw new Error(`app binary not found at ${bin}`);
  return bin;
}

function makeAttnRunner(attnBin, profile) {
  return function runAttn(args) {
    return execFileSync(attnBin, args, {
      encoding: 'utf8',
      env: profileCliEnv(profile),
    }).trim();
  };
}

// Only a pid this profile's worker registry recorded for this session: matching
// on a name or a path would land the signal on somebody else's worker.
function registeredWorkerPid(dataDir, sessionId) {
  const workersRoot = path.join(dataDir, 'workers');
  const instances = fs.existsSync(workersRoot) ? fs.readdirSync(workersRoot) : [];
  for (const instance of instances) {
    const registryDir = path.join(workersRoot, instance, 'registry');
    if (!fs.existsSync(registryDir)) continue;
    for (const entry of fs.readdirSync(registryDir)) {
      let record = null;
      try {
        record = JSON.parse(fs.readFileSync(path.join(registryDir, entry), 'utf8'));
      } catch {
        continue;
      }
      const pid = Number(record?.worker_pid);
      if (record?.session_id !== sessionId || !Number.isInteger(pid) || pid <= 1) continue;
      return pid;
    }
  }
  return null;
}

function sessionCostJson(dbPath, sessionId) {
  const sql = `SELECT session_cost_json FROM sessions WHERE id = '${sessionId.replaceAll("'", "''")}' LIMIT 1;`;
  const raw = queryDaemonDb(dbPath, sql);
  return raw ? JSON.parse(raw) : null;
}

function costObservationCount(cost) {
  return Object.keys(cost?.observations || {}).length;
}

function costTokenTotal(cost) {
  return Object.values(cost?.ledger || {}).reduce((total, usage) => {
    return total + Object.values(usage || {}).reduce((sum, value) => sum + (typeof value === 'number' ? value : 0), 0);
  }, 0);
}

function processAlive(pid) {
  if (!pid) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function liveWorkerPid(dataDir, sessionId) {
  const pid = registeredWorkerPid(dataDir, sessionId);
  return processAlive(pid) ? pid : null;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-recoverable-auto-revive.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('this scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const dataDir = dataDirForProfile(profile);
  const appBin = resolveAppBin(options.appPath);
  const runAttn = makeAttnRunner(appBin, profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'RECOVERABLE-AUTO-REVIVE',
    tier: 'tier2-local-mock-agent',
    prefix: 'recoverable-auto-revive',
    metadata: { agent: 'claude', focus: 'a session whose worker died comes back with its conversation' },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const token = `REVIVED${Date.now()}`;
  const secondToken = `AGAIN${Date.now()}`;
  const dbPath = path.join(dataDir, 'attn.db');
  const repoDir = path.join(runner.sessionDir, 'target-repo');
  let sessionId = null;
  let paneId = null;

  try {
    await runner.step('prepare_repo_and_fixture', async () => {
      fs.mkdirSync(repoDir, { recursive: true });
      execFileSync('git', ['init', '-q'], { cwd: repoDir });
      execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
        cwd: repoDir,
        env: {
          ...process.env,
          GIT_AUTHOR_NAME: 'attn',
          GIT_AUTHOR_EMAIL: 'attn@local',
          GIT_COMMITTER_NAME: 'attn',
          GIT_COMMITTER_EMAIL: 'attn@local',
        },
      });
      writeMockAgentFixture(repoDir, {
        name: 'auto revive mock',
        resumable: true,
        turns: [
          { includes: token, actions: [{ type: 'reply', text: token }] },
          { includes: secondToken, actions: [{ type: 'reply', text: secondToken }] },
        ],
      });
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_claude_session_that_holds_a_conversation', async () => {
      const created = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: repoDir,
        label: `revive-${runner.runId.slice(-6)}`,
        agent: 'claude',
        sessionWaitMs: 30_000,
        promptReadyFn: ensureClaudeInitialPanePromptReady,
        promptReadyTimeoutMs: 90_000,
      });
      await client.request('select_session', { sessionId: created });
      const pane = await waitForFirstWorkspacePane(client, created, 'claude pane', 20_000);
      paneId = pane.paneId;
      await client.request('type_pane_via_ui', { sessionId: created, paneId, text: `Reply with exactly ${token}` });
      await client.request('type_pane_via_ui', { sessionId: created, paneId, text: '\n' });
      await waitForPaneText(
        client,
        created,
        paneId,
        (text) => text.split(token).length > 2,
        'the agent to answer with the token before the crash',
        60_000,
      );
      await pollFor(() => liveWorkerPid(dataDir, created), 'initial pty-worker alive', 20_000);
      return created;
    });

    const killedPid = await runner.step('kill_the_worker_while_the_daemon_is_down', async () => {
      await client.quitApp();
      await observer.close();
      runAttn(['daemon', 'stop']);
      const pid = registeredWorkerPid(dataDir, sessionId);
      if (processAlive(pid)) process.kill(pid, 'SIGKILL');
      await pollFor(() => (liveWorkerPid(dataDir, sessionId) ? null : true), 'pty-worker gone after kill', 15_000);
      runAttn(['daemon', 'ensure']);
      return pid;
    });
    runner.log('daemon restarted with the worker dead', { killedPid });

    await runner.step('assert_the_session_is_recoverable_with_no_worker', async () => {
      await observer.connect();
      await pollFor(
        () => (observer.getSession(sessionId)?.state === 'recoverable' ? true : null),
        'session state recoverable after restart',
        30_000,
      );
      runner.assert(liveWorkerPid(dataDir, sessionId) === null, 'no pty-worker runs after the restart', { sessionId });
    });

    const revivedPid = await runner.step('reopen_the_app_and_wait_for_the_auto_revive', async () => {
      await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
      await client.request('select_session', { sessionId });
      const pid = await pollFor(
        () => liveWorkerPid(dataDir, sessionId),
        'auto-respawned pty-worker after reopen',
        60_000,
      );
      await pollFor(
        () => ((session) => session && session.state !== 'recoverable')(observer.getSession(sessionId)),
        'recoverable state cleared once the revived worker is adopted',
        30_000,
      );
      return pid;
    });
    runner.log('worker auto-respawned and recoverable cleared', { revivedPid });

    const revivedPaneId = await runner.step('assert_the_revived_pane_carries_the_conversation', async () => {
      const pane = await waitForFirstWorkspacePane(client, sessionId, 'pane after revive', 20_000);
      const revived = await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (value) => value.includes(token),
        'the resumed session to repaint what was said before the worker died',
        60_000,
      );
      const text = revived?.text || '';
      runner.writeText('revived-pane.txt', text);
      runner.assert(!/Failed to attach PTY/.test(text), 'the revived pane shows no attach-failure banner');
      return pane.paneId;
    });

    const costAfterRevive = await runner.step('assert_a_turn_after_the_revive_advances_the_cost_ledger', async () => {
      const before = sessionCostJson(dbPath, sessionId);
      await client.request('type_pane_via_ui', { sessionId, paneId: revivedPaneId, text: `Reply with exactly ${secondToken}` });
      await client.request('type_pane_via_ui', { sessionId, paneId: revivedPaneId, text: '\n' });
      await waitForPaneText(
        client,
        sessionId,
        revivedPaneId,
        (text) => text.split(secondToken).length > 2,
        'the resumed session to answer a new prompt',
        60_000,
      );
      const after = await pollFor(
        () => {
          const cost = sessionCostJson(dbPath, sessionId);
          const advanced = costObservationCount(cost) > costObservationCount(before)
            && costTokenTotal(cost) > costTokenTotal(before);
          return advanced ? cost : null;
        },
        'the session cost ledger to advance after the resumed turn',
        30_000,
      );
      runner.writeJson('cost-after-revive.json', { before, after });
      for (const [id, observation] of Object.entries(before?.observations || {})) {
        runner.assert(
          JSON.stringify(after?.observations?.[id]) === JSON.stringify(observation),
          'the resumed turn keeps every cost observation taken before the crash',
          { observationId: id, before: observation, after: after?.observations?.[id] },
        );
      }
      return after;
    });

    const summary = await runner.finishSuccess({
      sessionId,
      paneId,
      token,
      secondToken,
      killedPid,
      revivedPid,
      costObservationsAfterRevive: costObservationCount(costAfterRevive),
      costTokensAfterRevive: costTokenTotal(costAfterRevive),
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionId, paneId, token });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

await main();
