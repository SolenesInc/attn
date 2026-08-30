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
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
import { preTrustClaudeFolder, ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

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

function resolveAttnBin() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(HARNESS_DIR, '../../../attn')].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error('attn binary not found (build ./attn or set ATTN_HARNESS_BIN)');
}

function makeAttnRunner(attnBin, profile) {
  return function runAttn(args) {
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      env: { ...process.env, ATTN_PROFILE: profile },
    });
    const brace = stdout.indexOf('{');
    return { stdout, json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null };
  };
}

// Claude treats a fast multi-line write as a paste, so the submit has to be a
// lone carriage return a beat later.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('write_pane', { sessionId, paneId, text, submit: false });
  await delay(600);
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
}

async function pressCancelCountdown(driver) {
  await driver.activateApp();
  await driver.pressKey('.', { command: true });
}

async function splitIntoShellPane(client, sessionId) {
  const before = await client.request('get_workspace', { sessionId });
  const beforeIds = new Set((before.panes || []).map((pane) => pane.paneId));
  await client.request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
  const after = await waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => (workspace?.panes || []).length === beforeIds.size + 1
      && (workspace?.panes || []).every((pane) => pane.runtimeId),
    'the split pane to register a session',
    30_000,
  );
  const created = (after.panes || []).find((pane) => !beforeIds.has(pane.paneId));
  if (!created) {
    throw new Error(`No new pane appeared after the split. Before=${JSON.stringify(before)} After=${JSON.stringify(after)}`);
  }
  await waitForPaneVisible(client, sessionId, created.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, created.paneId, 20_000);
  return created;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-countdown-cancel.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the countdown-cancel scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'COUNTDOWN-CANCEL',
    tier: 'tier3-local-agent',
    prefix: 'countdown-cancel',
    metadata: {
      agent: 'claude',
      focus: 'a real Cmd+. cancels the auto-settle and ticket-nudge countdowns that are on screen',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath });
  const note = (message, extra) => runner.log(message, extra);

  let agentId = null;   // the booted claude agent — auto-settle target, then nudge target
  let agentPaneId = null;
  let authorId = null;  // the split shell — authors ticket activity, and holds selection

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, profile });

  // Cleanups run in reverse registration order, so the observer and app are
  // registered first to close last.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('restore_auto_settle', () =>
    client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' }).catch(() => {}));
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
        90_000,
      );
      note('agent booted and owes a turn', { agentId, owed });
    });

    await runner.step('cancel_auto_settle_with_a_real_keystroke', async () => {
      await client.request('set_setting', { key: 'auto_settle_arm_seconds', value: '5' });
      await client.request('set_setting', { key: 'auto_settle_countdown_seconds', value: '60' });
      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'true' });

      // Measured on the pinned haiku: 77 numbers a second (239 -> 1004 across
      // ten seconds), so 2000 buys ~26s; 200 ran dry in five.
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
      const armed = await pollFor(
        () => observer.getSession(agentId)?.auto_settle_fires_at || null,
        'the auto-settle countdown to arm on the visible agent',
        90_000,
      );
      const remainingMs = Date.parse(armed) - Date.now();
      note('auto-settle armed', { firesAt: armed, remainingMs });
      runner.assert(
        remainingMs > 20_000,
        `the countdown has far more than the keystroke needs left on it (${remainingMs}ms), so expiry cannot explain a cancel`,
      );

      await pressCancelCountdown(driver);

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

      await pressCancelCountdown(driver);

      await pollFor(
        () => (observer.getSession(agentId)?.auto_settle_dismiss_armed ? null : true),
        'the real Cmd+. to undo the standing dismissal',
        15_000,
      );
      const rearmed = await pollFor(
        () => observer.getSession(agentId)?.auto_settle_fires_at || null,
        'the settle to re-arm after the dismissal was undone',
        60_000,
      );
      note('dismissal undone, settle re-armed', { firesAt: rearmed });

      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' });
    });

    const ticketId = await runner.step('arm_a_nudge_on_the_visible_unselected_pane', async () => {
      const created = runAttn([
        'ticket', 'new',
        '--title', `Countdown cancel fixture ${runner.runId.slice(-6)}`,
        '--session', agentId,
        '--json',
      ]);
      const id = created.json?.ticket_id;
      runner.assert(typeof id === 'string' && id.length > 0, `ticket new returned an id (got ${JSON.stringify(created.json)})`, created.json);

      const splitPane = await splitIntoShellPane(client, agentId);
      authorId = splitPane.runtimeId;
      await client.request('focus_pane', { sessionId: agentId, paneId: splitPane.paneId });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === authorId ? state : null;
      }, 'the split pane to take the selection off the agent', 20_000);
      note('split pane holds the selection; the agent tile is still on screen', { authorId });

      runAttn(['ticket', 'comment', id, '-m', 'Please take a look when you can.', '--session', authorId]);
      const armed = await pollFor(
        () => {
          const session = observer.getSession(agentId);
          return session?.nudge_fires_at ? session : null;
        },
        'the ticket nudge to arm a countdown on the unselected agent',
        30_000,
      );
      note('nudge armed on the visible agent', { firesAt: armed.nudge_fires_at, unread: armed.ticket_unread });
      return id;
    });

    await runner.step('cancel_nudge_with_a_real_keystroke', async () => {
      await pressCancelCountdown(driver);

      await pollFor(
        () => (observer.getSession(agentId)?.nudge_fires_at ? null : true),
        'the real Cmd+. to cancel the armed nudge on the pane the user can see but is not in',
        15_000,
      );
      const after = observer.getSession(agentId);
      note('nudge cancelled by keystroke', { unread: after?.ticket_unread });
      runner.assert(
        after?.ticket_unread === true,
        `the ticket is still unread after the cancel (got ticket_unread=${JSON.stringify(after?.ticket_unread)})`,
        after,
      );
    });

    await runner.step('the_cancel_survives_looking_at_the_session', async () => {
      await client.request('select_session', { sessionId: agentId });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === agentId ? state : null;
      }, 'the agent to take the selection', 20_000);
      await client.request('select_session', { sessionId: authorId });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === authorId ? state : null;
      }, 'the selection to go back to the split pane', 20_000);

      await delay(4_000);
      const after = observer.getSession(agentId);
      runner.assert(
        !after?.nudge_fires_at,
        `the cancelled nudge stayed cancelled across a selection round trip (got nudge_fires_at=${JSON.stringify(after?.nudge_fires_at)})`,
        after,
      );
      runner.assert(
        after?.ticket_unread === true,
        `and the ticket is still there to come back to (got ticket_unread=${JSON.stringify(after?.ticket_unread)})`,
        after,
      );
      note('cancel survived a selection round trip');
    });

    await runner.step('new_ticket_activity_asks_again', async () => {
      runAttn(['ticket', 'comment', ticketId, '-m', 'One more thing, after the cancel.', '--session', authorId]);
      const rearmed = await pollFor(
        () => {
          const session = observer.getSession(agentId);
          return session?.nudge_fires_at ? session : null;
        },
        'genuinely new ticket activity to arm the nudge again',
        30_000,
      );
      note('new activity re-armed the nudge', { firesAt: rearmed.nudge_fires_at });
    });

    const summary = await runner.finishSuccess({ agentId, authorId, ticketId });
    console.log('[countdown-cancel] PASS — a real Cmd+. cancelled both visible countdowns, and only what was pending.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { agentId, authorId });
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
