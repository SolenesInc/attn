#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, dataDirForProfile } from './harnessProfile.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { ensureCodexInitialPanePromptReady } from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane, waitForPaneVisible } from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const CLOSE_BUDGET_MS = 1_000;

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
  const dataDir = dataDirForProfile(profile);
  const runner = createScenarioRunner(options, {
    scenarioId: 'CLOSE-PANE-NONBLOCKING',
    tier: 'tier1-local-shell',
    prefix: 'close-pane-nonblocking',
    metadata: {
      agent: 'codex',
      focus: 'a pane disappears before its SIGTERM-ignoring mock agent finishes teardown',
      profile,
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
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

    const summary = await runner.finishSuccess({ sessionId, paneId: pane.paneId, closeElapsedMs, escalationLog });
    console.log('[RealAppHarness] Non-blocking pane close passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
