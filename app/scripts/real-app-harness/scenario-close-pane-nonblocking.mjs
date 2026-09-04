#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { readProcessEnvironment } from './agentTripwire.mjs';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  restoreHarnessSettings,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessProfile, dataDirForProfile, profileCliEnv } from './harnessProfile.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appDaemonInTree } from './platform.mjs';
import { ensureCodexInitialPanePromptReady } from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane, waitForPaneVisible } from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const CLOSE_BUDGET_MS = 1_000;
const execFileAsync = promisify(execFile);

function prepareSlowGit(sessionDir) {
  const env = profileCliEnv();
  const realGit = execFileSync('/bin/sh', ['-c', 'command -v git'], { encoding: 'utf8', env }).trim();
  execFileSync(realGit, ['init', '-q'], { cwd: sessionDir, env });
  const binDir = path.join(sessionDir, 'slow-git-bin');
  fs.mkdirSync(binDir);
  const executable = path.join(binDir, 'git');
  const gate = path.join(sessionDir, 'slow-git-enabled');
  // Remove this shim from PATH before calling a Git wrapper that may resolve Git again.
  fs.writeFileSync(executable, '#!/bin/sh\nif [ -f "$ATTN_CLOSE_GIT_GATE" ]; then sleep 3; fi\nexport PATH="$ATTN_CLOSE_GIT_BASE_PATH"\nexec "$ATTN_CLOSE_REAL_GIT" "$@"\n', { mode: 0o755 });
  return {
    executable, gate, binDir,
    env: {
      PATH: `${binDir}${path.delimiter}${process.env.PATH}`,
      ATTN_CLOSE_GIT_BASE_PATH: process.env.PATH,
      ATTN_CLOSE_GIT_GATE: gate,
      ATTN_CLOSE_REAL_GIT: realGit,
    },
  };
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function daemonLog(dataDir) {
  const logPath = path.join(dataDir, 'daemon.log');
  return fs.existsSync(logPath) ? fs.readFileSync(logPath, 'utf8') : '';
}

async function waitForTeardownLog(dataDir, sessionId, timeoutMs = 8_000) {
  const needles = [`session teardown escalated for ${sessionId}:`, `session teardown failed for ${sessionId}:`];
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const line = daemonLog(dataDir).split('\n').find((entry) => needles.some((needle) => entry.includes(needle)));
    if (line) return line;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out after ${timeoutMs}ms waiting for daemon teardown log for ${sessionId}`);
}

async function waitForSessionGone(client, sessionId, timeoutMs) {
  const deadline = performance.now() + timeoutMs;
  let ui;
  while (performance.now() < deadline) {
    ui = await client.request('get_session_ui_state', { sessionId });
    if (ui.exists === false && ui.sidebarItem == null) return ui;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error(`Session ${sessionId} remained visible after ${timeoutMs}ms: ${JSON.stringify(ui, null, 2)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-close-pane-nonblocking.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  assertFreshWorldTargetSafe({ profile, appPath: options.appPath });
  const daemonBinary = appDaemonInTree(options.appPath);
  const dataDir = dataDirForProfile(profile);
  const runner = createScenarioRunner(options, {
    scenarioId: 'CLOSE-PANE-NONBLOCKING',
    tier: 'tier1-local-shell',
    prefix: 'close-pane-nonblocking',
    metadata: {
      agent: 'codex',
      focus: 'a pane disappears despite slow Git and a SIGTERM-ignoring mock agent',
      profile,
    },
  });

  const slowGit = prepareSlowGit(runner.sessionDir);
  const client = new UiAutomationClient({ appPath: options.appPath, launchEnv: slowGit.env });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    writeMockAgentFixture(runner.sessionDir, {
      name: 'stubborn close mock',
      ignoreSigterm: true,
      turns: [],
    });

    await runner.step('launch_app', async () => {
      await client.quitApp();
      await execFileAsync(daemonBinary, ['daemon', 'stop'], { env: profileCliEnv(profile) });
      await launchFreshAppAndConnect(client, observer);
    });

    let pane;
    await runner.step('create_stubborn_agent', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `close-fast-${runner.runId}`,
        agent: 'codex',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      await client.request('select_session', { sessionId });
      await ensureCodexInitialPanePromptReady(client, sessionId, 45_000);
      pane = await waitForFirstWorkspacePane(client, sessionId, 'stubborn mock agent pane', 20_000);
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
    });

    const slowGitProbeMs = await runner.step('arm_slow_git', async () => {
      const daemonPid = Number(fs.readFileSync(path.join(dataDir, 'attn.pid'), 'utf8').trim());
      runner.assert(readProcessEnvironment(daemonPid).includes(`PATH=${slowGit.binDir}${path.delimiter}`),
        'The daemon must resolve Git through the slow wrapper');
      fs.writeFileSync(slowGit.gate, 'enabled\n');
      const start = performance.now();
      await execFileAsync(slowGit.executable, ['rev-parse', '--show-toplevel'], {
        cwd: runner.sessionDir, env: profileCliEnv(profile, slowGit.env),
      });
      const elapsedMs = performance.now() - start;
      runner.assert(elapsedMs >= 3_000, 'Git probe must take at least three seconds', { elapsedMs });
      return elapsedMs;
    });

    let closeElapsedMs;
    await runner.step('close_before_teardown', async () => {
      const startedAt = performance.now();
      await client.request('close_pane', { sessionId, paneId: pane.paneId });
      await waitForSessionGone(client, sessionId, CLOSE_BUDGET_MS);
      closeElapsedMs = performance.now() - startedAt;
      runner.assert(
        closeElapsedMs < CLOSE_BUDGET_MS,
        `Pane removal took ${closeElapsedMs.toFixed(1)}ms; limit=${CLOSE_BUDGET_MS}ms`,
        { closeElapsedMs, closeBudgetMs: CLOSE_BUDGET_MS },
      );
    });

    const escalationLog = await runner.step('teardown_finishes_later', async () => {
      const line = await waitForTeardownLog(dataDir, sessionId);
      runner.writeText('teardown-escalation.log', `${line}\n`);
      return line;
    });

    const summary = await runner.finishSuccess({ sessionId, paneId: pane.paneId, closeElapsedMs, slowGitProbeMs, escalationLog });
    console.log('[RealAppHarness] Non-blocking pane close passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    fs.rmSync(slowGit.gate, { force: true });
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    await restoreHarnessSettings();
    await execFileAsync(daemonBinary, ['daemon', 'stop'], { env: profileCliEnv(profile) }).catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
