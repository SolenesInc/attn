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
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
import { createWindowDriver } from './platform.mjs';
import { getFrontWindowBounds } from './nativeWindowCapture.mjs';
import { preTrustClaudeFolder, ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// The daemon's quiet window, mirrored rather than imported: a change to one
// should make this run fail, not follow it.
const QUIET_WINDOW_MS = 5_000;
const COUNTDOWN_SECONDS = 60;

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

// The terminal's own input path reaches the daemon untagged; write_pane is
// tagged `automation` and is deliberately a different thing.
async function typeLikeAPerson(client, sessionId, paneId, text) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text });
}

// Keep the prompt on the user's input path: auto-settle deliberately ignores
// automation writes, which this scenario checks again at the end.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text });
  await delay(600);
  await client.request('type_pane_via_ui', { sessionId, paneId, text: '\r' });
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-settle-typing-hold.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the settle-typing-hold scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }

  if (process.env.ATTN_HARNESS_PARK_VISIBLE_PX === undefined) {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '800';
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'SETTLE-TYPING-HOLD',
    tier: 'tier3-local-agent',
    prefix: 'settle-typing-hold',
    metadata: {
      agent: 'claude',
      focus: 'typing to an agent freezes its settling countdown, and going quiet hands back a whole one',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath });
  const note = (message, extra) => runner.log(message, extra);

  let agentId = null;
  let agentPaneId = null;

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, profile });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('boot_agent_owing_a_turn', async () => {
      const cwd = path.join(runner.sessionDir, 'agent-repo');
      fs.mkdirSync(cwd, { recursive: true });
      preTrustClaudeFolder(cwd);
      agentId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `settle-hold-${runner.runId.slice(-6)}`,
        agent: 'claude',
        sessionWaitMs: 60_000,
        promptReadyFn: ensureClaudePromptReadyViaPty,
        promptReadyTimeoutMs: 90_000,
      });
      runner.registerCleanup('close_agent_session', () => client.request('close_session', { sessionId: agentId }));
      const pane = await waitForFirstWorkspacePane(client, agentId, `pane for ${agentId}`, 20_000);
      agentPaneId = pane.paneId;
      await client.request('select_session', { sessionId: agentId });

      await pollFor(
        () => (observer.getSession(agentId)?.turn_owed === true ? true : null),
        'the booted agent to owe a turn',
        90_000,
      );
      note('agent booted and owes a turn', { agentId });
    });

    let frozenDeadline = null;

    await runner.step('typing_freezes_the_running_countdown', async () => {
      await client.request('set_setting', { key: 'auto_settle_arm_seconds', value: '5' });
      await client.request('set_setting', { key: 'auto_settle_countdown_seconds', value: String(COUNTDOWN_SECONDS) });
      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'true' });

      await submitPrompt(
        client,
        agentId,
        agentPaneId,
        'Count from 1 to 2000, one number per line, nothing else. Do not use any tools.',
      );
      await pollFor(
        () => (observer.getSession(agentId)?.state === 'working' ? true : null),
        'the steered agent to start working',
        90_000,
      );
      frozenDeadline = await pollFor(
        () => observer.getSession(agentId)?.auto_settle_fires_at || null,
        'the auto-settle countdown to arm',
        90_000,
      );
      const remainingMs = Date.parse(frozenDeadline) - Date.now();
      note('countdown armed', { firesAt: frozenDeadline, remainingMs });
      runner.assert(
        remainingMs > 30_000,
        `the countdown has far more left than this leg needs (${remainingMs}ms), so expiry cannot explain a freeze`,
      );

      const running = (await client.request('get_session_ui_state', { sessionId: agentId })).settling;
      runner.assert(
        running && running.held === false && running.frozenBar === false && running.text.includes('Settling'),
        `the pane header is drawing a running countdown before anyone types (${JSON.stringify(running)})`,
        running,
      );

      await typeLikeAPerson(client, agentId, agentPaneId, 'and then also');

      const held = await pollFor(
        () => {
          const session = observer.getSession(agentId);
          return session?.auto_settle_held === true ? session : null;
        },
        'the keystrokes to freeze the countdown',
        10_000,
      );
      runner.assert(
        !held.auto_settle_fires_at,
        `a frozen countdown carries no deadline (got auto_settle_fires_at=${JSON.stringify(held.auto_settle_fires_at)})`,
        held,
      );
      runner.assert(
        held.state === 'working' && held.turn_owed === true,
        `the agent is still working and still owes the turn while frozen (state=${JSON.stringify(held.state)}, turn_owed=${JSON.stringify(held.turn_owed)})`,
        held,
      );
      const chip = (await client.request('get_session_ui_state', { sessionId: agentId })).settling;
      runner.assert(
        chip && chip.held === true && chip.frozenBar === true && chip.text.toLowerCase().includes('paused'),
        `the pane header says the countdown is paused and stops animating (${JSON.stringify(chip)})`,
        chip,
      );
      note('countdown frozen by typing', { state: held.state, turn_owed: held.turn_owed, chip });
    });

    await runner.step('the_freeze_outlives_continued_typing', async () => {
      const deadline = Date.now() + QUIET_WINDOW_MS * 2.5;
      let keystrokes = 0;
      while (Date.now() < deadline) {
        await delay(2_000);
        await typeLikeAPerson(client, agentId, agentPaneId, ' more');
        keystrokes += 1;
        const session = observer.getSession(agentId);
        runner.assert(
          session?.auto_settle_held === true && !session?.auto_settle_fires_at,
          `the countdown is still frozen while the user keeps typing (held=${JSON.stringify(session?.auto_settle_held)}, firesAt=${JSON.stringify(session?.auto_settle_fires_at)})`,
          session,
        );
      }
      note('freeze survived continued typing', { keystrokes, forMs: QUIET_WINDOW_MS * 2.5 });
    });

    await runner.step('going_quiet_hands_back_a_whole_countdown', async () => {
      const resumed = await pollFor(
        () => {
          const session = observer.getSession(agentId);
          if (session?.auto_settle_fires_at) return session;
          if (session && session.state !== 'working') {
            throw new Error(`the agent stopped working (state=${session.state}) before the hold could release; its steering task was too short for this run to say anything`);
          }
          return null;
        },
        'the countdown to come back after the user stopped typing',
        QUIET_WINDOW_MS * 4,
      );
      runner.assert(
        !resumed.auto_settle_held,
        `the frozen flag is gone once the countdown is running again (got auto_settle_held=${JSON.stringify(resumed.auto_settle_held)})`,
        resumed,
      );
      const remainingMs = Date.parse(resumed.auto_settle_fires_at) - Date.now();
      runner.assert(
        remainingMs > (COUNTDOWN_SECONDS - 10) * 1_000,
        `the resumed countdown is a whole one, not the remainder of the frozen one (${remainingMs}ms of ${COUNTDOWN_SECONDS}s)`,
        { remainingMs, frozenDeadline, resumedDeadline: resumed.auto_settle_fires_at },
      );
      const resumedChip = (await client.request('get_session_ui_state', { sessionId: agentId })).settling;
      runner.assert(
        resumedChip && resumedChip.held === false && resumedChip.frozenBar === false,
        `the pane header is animating again once the countdown resumed (${JSON.stringify(resumedChip)})`,
        resumedChip,
      );
      note('countdown resumed whole', { remainingMs, chip: resumedChip });
    });

    await runner.step('pointer_movement_freezes_and_extends_the_countdown', async () => {
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
        // Visit both targets so the step never depends on where a previous run left
        // the cursor. Each pair contains real movement.
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
    });

    await runner.step('automation_writes_do_not_freeze_it', async () => {
      const before = observer.getSession(agentId)?.auto_settle_fires_at;
      await client.request('write_pane', { sessionId: agentId, paneId: agentPaneId, text: ' automated', submit: false });
      await delay(2_000);
      const after = observer.getSession(agentId);
      runner.assert(
        after?.auto_settle_held !== true,
        `an automation write did not freeze the countdown (got auto_settle_held=${JSON.stringify(after?.auto_settle_held)})`,
        after,
      );
      runner.assert(
        after?.auto_settle_fires_at === before,
        `the deadline is untouched by an automation write (${JSON.stringify(before)} -> ${JSON.stringify(after?.auto_settle_fires_at)})`,
        after,
      );
      note('automation write left the countdown alone', { firesAt: after?.auto_settle_fires_at });
    });

    const summary = await runner.finishSuccess({ agentId });
    console.log('[settle-typing-hold] PASS — typing and pointer movement froze the countdown, and going quiet handed back a whole one.');
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
