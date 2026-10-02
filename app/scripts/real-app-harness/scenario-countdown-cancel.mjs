#!/usr/bin/env node


import fs from 'node:fs';
import path from 'node:path';

import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  pressShortcutKeys,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { getFrontWindowBounds } from './nativeWindowCapture.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance } from './harnessInstance.mjs';
import {
  ensureClaudePromptReadyViaPty,
  writeQueueAgentFixture,
} from './scenarioAgents.mjs';
import {
  waitForFirstWorkspacePane,
} from './scenarioAssertions.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// The daemon's quiet window, mirrored rather than imported: a change to one
// should make this run fail, not follow it.
const QUIET_WINDOW_MS = 5_000;
const COUNTDOWN_SECONDS = 60;

const LONG_RUN_PROMPT = 'Count from 1 to 2000, one number per line, nothing else. Do not use any tools.';
// Measured over green mock runs: 26.8s for the keystroke legs plus 26.7s for the
// pointer leg; this outlasts the 53.5s they need together 4x.
const LONG_RUN_MS = 210_000;
const LONG_RUN_TURN = {
  includes: LONG_RUN_PROMPT,
  actions: [{ type: 'delay', ms: LONG_RUN_MS }, { type: 'reply', text: '1 ... 2000', state: 'idle' }],
};

function windowRelativePoint(pageX, pageY, windowBounds, innerWidth, innerHeight) {
  const { width, height } = windowBounds.logicalBounds;
  const chromeX = Math.max(0, width - innerWidth);
  const chromeY = Math.max(0, height - innerHeight);
  return {
    relativeX: (chromeX / 2 + pageX) / width,
    relativeY: (chromeY + pageY) / height,
  };
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 60_000, intervalMs = 300) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

// Auto-settle arms only behind user input, and a fast write lands as a paste.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text });
  await delay(600);
  await client.request('type_pane_via_ui', { sessionId, paneId, text: '\r' });
}

async function pressCancelCountdown(client, driver) {
  await pressShortcutKeys(client, driver, 'session.cancelCountdown');
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-countdown-cancel.mjs');
    return;
  }

  const instance = currentHarnessInstance();
  if (!instance) {
    throw new Error('the countdown-cancel scenario does not run against production; set ATTN_INSTANCE / ATTN_HARNESS_INSTANCE to a named instance');
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'COUNTDOWN-CANCEL',
    tier: 'tier2-local-mock-agent',
    prefix: 'countdown-cancel',
    metadata: {
      agent: 'mock-claude',
      focus: 'a real Cmd+. and a real pointer control auto-settle on screen',
    },
  });

  process.env.ATTN_HARNESS_ALWAYS_ON_TOP ??= '0';

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const note = (message, extra) => runner.log(message, extra);

  let agentId = null;
  let agentPaneId = null;

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, instance });

  // Cleanups run in reverse registration order, so the observer and app are
  // registered first to close last.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('restore_auto_settle', () =>
    client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' }).catch(() => {}));
  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      await driver.activateApp();
    });

    await runner.step('boot_agent_owing_a_turn', async () => {
      const cwd = path.join(runner.sessionDir, 'agent-repo');
      fs.mkdirSync(cwd, { recursive: true });
      writeQueueAgentFixture(cwd, { turns: [LONG_RUN_TURN] });
      agentId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `countdown-cancel-${runner.runId.slice(-6)}`,
        agent: 'claude',
        sessionWaitMs: 60_000,
        promptReadyFn: ensureClaudePromptReadyViaPty,
        promptReadyTimeoutMs: 90_000,
      });
      runner.registerCleanup('close_agent_session', () => client.request('close_session', { sessionId: agentId }));
      const pane = await waitForFirstWorkspacePane(client, agentId, `pane for ${agentId}`, 20_000);
      agentPaneId = pane.paneId;
      await client.request('select_session', { sessionId: agentId });

      const owed = await pollFor(
        () => (observer.getSession(agentId)?.turn_owed === true ? true : null),
        'the booted agent to owe a turn',
        45_000,
      );
      note('agent booted and owes a turn', { agentId, owed });
    });

    await runner.step('cancel_auto_settle_with_a_real_keystroke', async () => {
      await client.request('set_setting', { key: 'auto_settle_arm_seconds', value: '5' });
      await client.request('set_setting', { key: 'auto_settle_countdown_seconds', value: String(COUNTDOWN_SECONDS) });
      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'true' });
      await observer.waitFor(
        () => observer.getSetting('auto_settle_arm_seconds') === '5'
          && observer.getSetting('auto_settle_countdown_seconds') === String(COUNTDOWN_SECONDS)
          && observer.getSetting('auto_settle_enabled') === 'true',
        'the daemon to apply the auto-settle settings',
      );

      await submitPrompt(client, agentId, agentPaneId, LONG_RUN_PROMPT);
      await pollFor(
        () => (observer.getSession(agentId)?.state === 'working' ? true : null),
        'the steered agent to start working',
        30_000,
      );
      const armed = await pollFor(
        () => observer.getSession(agentId)?.auto_settle_fires_at || null,
        'the auto-settle countdown to arm on the visible agent',
        30_000,
      );
      const remainingMs = Date.parse(armed) - Date.now();
      note('auto-settle armed', { firesAt: armed, remainingMs });
      runner.assert(
        remainingMs > 20_000,
        `the countdown has far more than the keystroke needs left on it (${remainingMs}ms), so expiry cannot explain a cancel`,
      );

      await pressCancelCountdown(client, driver);

      await pollFor(
        () => (observer.getSession(agentId)?.auto_settle_fires_at ? null : true),
        'the real Cmd+. to cancel the armed auto-settle',
        15_000,
      );
      const after = observer.getSession(agentId);
      note('auto-settle cancelled by keystroke', { state: after?.state, turn_owed: after?.turn_owed });
      runner.assert(
        after?.state === 'working',
        `the agent was still working when the countdown went away, so the keystroke cleared it (got state=${JSON.stringify(after?.state)})`,
        after,
      );
      runner.assert(
        after?.turn_owed === true,
        `the turn the user kept is still owed after the cancel (got turn_owed=${JSON.stringify(after?.turn_owed)})`,
        after,
      );
      runner.assert(
        after?.auto_settle_dismiss_armed === true,
        `the cancel left a standing dismissal on the wire (got auto_settle_dismiss_armed=${JSON.stringify(after?.auto_settle_dismiss_armed)})`,
        after,
      );

      await pressCancelCountdown(client, driver);

      await pollFor(
        () => (observer.getSession(agentId)?.auto_settle_dismiss_armed ? null : true),
        'the real Cmd+. to undo the standing dismissal',
        15_000,
      );
      const rearmed = await pollFor(
        () => observer.getSession(agentId)?.auto_settle_fires_at || null,
        'the settle to re-arm after the dismissal was undone',
        30_000,
      );
      note('dismissal undone, settle re-armed', { firesAt: rearmed });
    });

    await runner.step('pointer_movement_freezes_and_extends_the_countdown', async () => {
      await driver.activateApp();
      const logicalBounds = await getFrontWindowBounds(null, {
        appPath: options.appPath,
        client,
        driver,
      });
      runner.assert(Boolean(logicalBounds), `window bounds available: ${JSON.stringify(logicalBounds)}`);
      const windowBounds = { logicalBounds };
      const cellA = await client.request('get_pane_cell_rect', {
        sessionId: agentId,
        paneId: agentPaneId,
        cell: { row: 2, col: 4 },
      });
      const cellB = await client.request('get_pane_cell_rect', {
        sessionId: agentId,
        paneId: agentPaneId,
        cell: { row: 15, col: 40 },
      });
      const points = [cellA, cellB].map((cell) => windowRelativePoint(
        cell.centerX,
        cell.centerY,
        windowBounds,
        cell.innerWidth,
        cell.innerHeight,
      ));
      note('pointer movement targets', { points });

      const until = Date.now() + QUIET_WINDOW_MS * 2.5;
      let moves = 0;
      while (Date.now() < until) {
        for (const point of points) {
          await driver.movePointerInWindow(point.relativeX, point.relativeY);
          moves += 1;
        }
        await delay(2_000);
        const session = observer.getSession(agentId);
        runner.assert(
          session?.auto_settle_held === true && !session?.auto_settle_fires_at,
          `the countdown stays frozen while the pointer keeps moving (held=${JSON.stringify(session?.auto_settle_held)}, firesAt=${JSON.stringify(session?.auto_settle_fires_at)})`,
          session,
        );
      }
      note('freeze survived continued pointer movement', { moves, forMs: QUIET_WINDOW_MS * 2.5 });

      const resumed = await pollFor(
        () => observer.getSession(agentId)?.auto_settle_fires_at ? observer.getSession(agentId) : null,
        'the countdown to return after pointer movement stopped',
        QUIET_WINDOW_MS * 4,
      );
      const remainingMs = Date.parse(resumed.auto_settle_fires_at) - Date.now();
      runner.assert(
        remainingMs > (COUNTDOWN_SECONDS - 10) * 1_000,
        `pointer quiet returns a whole countdown (${remainingMs}ms of ${COUNTDOWN_SECONDS}s)`,
        resumed,
      );

      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' });
    });

    const summary = await runner.finishSuccess({ agentId });
    console.log('[countdown-cancel] PASS — a real Cmd+. and a real pointer controlled auto-settle on screen.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { agentId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' }).catch(() => {});
    if (agentId) await client.request('close_session', { sessionId: agentId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
