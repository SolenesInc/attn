#!/usr/bin/env node


import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';

import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  legacyTicketRequest,
  parseCommonArgs,
  pressShortcutKeys,
  printCommonHelp,
  submitPrompt as submitPromptViaAutomation,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { getFrontWindowBounds } from './nativeWindowCapture.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, socketPathForProfile } from './harnessProfile.mjs';
import { transcriptMessages, writeMockAgentFixture } from './mockAgent.mjs';
import {
  ensureClaudePromptReadyViaPty,
  ensureCodexPromptReadyViaPty,
  writeQueueAgentFixture,
} from './scenarioAgents.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneVisible,
  waitForSessionWorkspace,
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

const IDLE_STATES = new Set(['idle', 'waiting_input']);
const BUSY_RELEASE_FILE = 'busy-turn-release';
const GENERIC_DOORBELL = '📬 You have unread items in your attn inbox. Run attn agent inbox to read them.';
// Mirrors ticketNudgePrompt minus the leading emoji, which the grid can split.
const LEGACY_ITEM_CORE = 'Activity on a ticket that predates the garden — run `attn ticket inbox` to read and acknowledge it.';

const squashWs = (text) => text.replace(/\s+/g, '');

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
  await driver.activateApp();
  await pressShortcutKeys(client, driver, 'session.cancelCountdown');
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

function readTargetTranscript(cwd) {
  const dir = path.join(cwd, '.attn-mock-agent');
  const file = fs.readdirSync(dir).find((entry) => entry.endsWith('.jsonl'));
  if (!file) throw new Error(`no mock transcript in ${dir}`);
  return fs.readFileSync(path.join(dir, file), 'utf8');
}

function inboxBatches(transcript) {
  return transcriptMessages(transcript).flatMap((message) => {
    if (message.role !== 'assistant') return [];
    try {
      const parsed = JSON.parse(message.text);
      return Array.isArray(parsed?.items) ? [parsed] : [];
    } catch {
      return [];
    }
  });
}

async function readPaneText(client, sessionId) {
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${sessionId}`, 20_000);
  const res = await client
    .request('read_pane_text', { sessionId, paneId: pane.paneId }, { timeoutMs: 20_000 })
    .catch(() => null);
  return { paneId: pane.paneId, text: res?.text || '' };
}

// The automation write path on purpose: a real keystroke here would arm the
// nudge keystroke guard and defer the doorbell this scenario waits for.
async function driveAgentToIdle(client, observer, sessionId, note) {
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${sessionId}`, 20_000);
  const reply = 'initial turn ready';
  const prompt = `Reply with the exact words: ${reply}`;
  await submitPromptViaAutomation(client, sessionId, pane.paneId, prompt);
  const stateOf = () => observer.getSession(sessionId)?.state || 'unknown';
  await pollFor(() => (stateOf() === 'working' ? true : null), `${sessionId} to start working after the prompt`, 30_000, 500);
  note(`target started working (prompt accepted)`);
  await pollFor(
    async () => ((await readPaneText(client, sessionId)).text.includes(`\n• ${reply}`) ? true : null),
    `${sessionId} to print its initial reply`,
    90_000,
    500,
  );
  const idle = await pollFor(
    () => (IDLE_STATES.has(stateOf()) ? stateOf() : null),
    `${sessionId} to finish the turn and go idle/waiting`,
    90_000,
    500,
  );
  return idle;
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
  const socketPath = socketPathForProfile(profile);

  // The pointer leg drives a real cursor into the window, so the window has to
  // sit where a pointer can reach it.
  if (process.env.ATTN_HARNESS_PARK_VISIBLE_PX === undefined) {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '800';
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'COUNTDOWN-CANCEL',
    tier: 'tier2-local-mock-agent',
    prefix: 'countdown-cancel',
    metadata: {
      agent: 'claude+mock-codex',
      focus: 'a real Cmd+. and a real pointer reach the countdowns on screen, and the deliver-now button submits a doorbell through a real terminal',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath });
  const note = (message, extra) => runner.log(message, extra);

  let agentId = null;   // the booted claude agent — auto-settle target, then nudge target
  let agentPaneId = null;
  let authorId = null;  // the split shell — authors ticket activity, and holds selection
  let authorPaneId = null;
  let targetId = null;  // the codex agent that receives ticket doorbells
  let targetRepoDir = null;

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, profile });

  // Cleanups run in reverse registration order, so the observer and app are
  // registered first to close last.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('restore_auto_settle', () =>
    client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' }).catch(() => {}));
  try {
    await runner.step('launch_app', async () => {
      process.env.ATTN_HARNESS_ALWAYS_ON_TOP ??= '0';
      await launchFreshAppAndConnect(client, observer);
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

      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' });
    });

    await runner.step('split_a_shell_pane_to_author_ticket_activity', async () => {
      const splitPane = await splitIntoShellPane(client, agentId);
      authorId = splitPane.runtimeId;
      authorPaneId = splitPane.paneId;
      note('split shell ready beside the agent tile', { authorId, authorPaneId });
    });

    await runner.step('boot_a_codex_target_that_receives_doorbells', async () => {
      targetRepoDir = path.join(runner.sessionDir, 'target-repo');
      fs.mkdirSync(targetRepoDir, { recursive: true });
      execFileSync('git', ['init', '-q'], { cwd: targetRepoDir });
      execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
        cwd: targetRepoDir,
        env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
      });
      writeMockAgentFixture(targetRepoDir, {
        name: 'nudge mock',
        turns: [
          {
            includes: GENERIC_DOORBELL,
            submitHook: false,
            actions: [
              { type: 'attn', args: ['agent', 'inbox', '--json'] },
              { type: 'attn', args: ['ticket', 'inbox'] },
              { type: 'wait_for_file', path: BUSY_RELEASE_FILE },
              { type: 'reply', text: 'LEGACY_INBOX_TURN_RELEASED' },
            ],
          },
          {
            includes: 'initial turn ready',
            actions: [
              { type: 'reply', text: 'initial turn ready' },
            ],
          },
        ],
        defaultActions: [
          { type: 'reply', text: 'mock turn finished' },
        ],
      });

      targetId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: targetRepoDir,
        label: `nudge-target-${runner.runId.slice(-6)}`,
        agent: 'codex',
        sessionWaitMs: 30_000,
        promptReadyFn: ensureCodexPromptReadyViaPty,
        promptReadyTimeoutMs: 90_000,
      });
      runner.registerCleanup('close_target_session', () => client.request('close_session', { sessionId: targetId }));
      await client.request('select_session', { sessionId: targetId });
      const idleState = await driveAgentToIdle(client, observer, targetId, note);
      note('target agent idle and selected', { targetId, state: idleState });
    });

    const targetTicketId = await runner.step('overlapping_ticket_activity_reaches_the_selected_target', async () => {
      const created = await legacyTicketRequest(socketPath, {
        cmd: 'ticket_create',
        source_session_id: targetId,
        title: `Nudge trigger fixture ${runner.runId.slice(-6)}`,
      });
      const id = created.ticket_create_result?.ticket_id;
      runner.assert(typeof id === 'string' && id.length > 0, `ticket_create returned an id (got ${JSON.stringify(created)})`, created);
      note('ticket created by target (creator-participant)', { ticketId: id });

      await legacyTicketRequest(socketPath, {
        cmd: 'ticket_comment',
        source_session_id: authorId,
        ticket_id: id,
        comment: 'Please take a look when you can.',
      });
      await legacyTicketRequest(socketPath, {
        cmd: 'ticket_comment',
        source_session_id: authorId,
        ticket_id: id,
        comment: 'One more related note.',
      });
      note('author posted overlapping ticket activity -> should produce one target doorbell');
      return id;
    });

    await runner.step('a_selected_target_arms_no_countdown', async () => {
      const unread = await pollFor(
        () => {
          const s = observer.getSession(targetId);
          return s && s.ticket_unread === true ? s : null;
        },
        `target ${targetId} to show unread ticket activity`,
        30_000,
      );
      note('target shows unread ticket activity', {
        ticket_unread: unread.ticket_unread,
        nudge_fires_at: unread.nudge_fires_at ?? null,
        state: unread.state,
      });
      runner.assert(
        unread.nudge_fires_at === undefined || unread.nudge_fires_at === null,
        `selected target's nudge is paused (no armed countdown); got nudge_fires_at=${JSON.stringify(unread.nudge_fires_at)}`,
        unread,
      );
      runner.assert(IDLE_STATES.has(unread.state), `target is still idle/waiting while paused (got ${unread.state})`, unread);
    });

    await runner.step('the_deliver_now_button_submits_the_doorbell', async () => {
      const beforeClick = await readPaneText(client, targetId);
      runner.writeText('pane-before-click.txt', beforeClick.text);
      runner.assert(
        !beforeClick.text.includes(GENERIC_DOORBELL),
        `no doorbell injected before the click (the countdown gate held); pane unexpectedly contains "${GENERIC_DOORBELL}"`,
      );
      note('gate held: no doorbell in target pane before click');

      try {
        const shot = await client.request('capture_screenshot_data', { selector: '.nudge-header-trigger' });
        if (shot?.pngBase64) {
          fs.writeFileSync(path.join(runner.runDir, 'paused-trigger-button.png'), Buffer.from(shot.pngBase64, 'base64'));
        }
      } catch (error) {
        console.warn(`[countdown-cancel] paused-button screenshot skipped: ${error instanceof Error ? error.message : String(error)}`);
      }

      const clickRes = await client.request('click_nudge_trigger', {});
      runner.assert(clickRes?.clicked === true, `the trigger button was found and clicked (got ${JSON.stringify(clickRes)})`, clickRes);
      note('clicked the deliver-now trigger', { surface: clickRes.surface });

      const started = await pollFor(
        () => (observer.getSession(targetId)?.state === 'working' ? true : null),
        'Codex to start a turn from the delivered doorbell',
        60_000,
        100,
      ).catch(() => null);

      let afterText = (await readPaneText(client, targetId)).text;
      runner.writeText('pane-after-click.txt', afterText);
      runner.assert(
        started === true,
        'the delivered doorbell started a Codex turn instead of remaining in the composer (see pane-after-click.txt)',
      );
      note('doorbell submitted: Codex entered working state');

      await pollFor(
        () => (observer.getSession(targetId)?.ticket_unread === true ? null : true),
        'the missing-hook nudge turn to consume the ticket inbox',
        30_000,
        250,
      );
      runner.assert(observer.getSession(targetId)?.state === 'working',
        'the first missing-hook doorbell turn remains busy after reading its inbox');

      afterText = (await readPaneText(client, targetId)).text;
      runner.writeText('pane-after-inbox-read.txt', afterText);
      const wantedCore = squashWs(LEGACY_ITEM_CORE);
      runner.assert(
        squashWs(afterText).includes(wantedCore),
        `the durable inbox read contains the complete legacy ticket item (see pane-after-inbox-read.txt)`,
      );
      const transcript = readTargetTranscript(targetRepoDir);
      const doorbells = transcriptMessages(transcript)
        .filter((message) => message.role === 'user' && message.text === GENERIC_DOORBELL);
      const batches = inboxBatches(transcript);
      runner.assert(doorbells.length === 1, 'the idle legacy event produced one generic doorbell', { doorbells });
      runner.assert(batches.length === 1 && batches[0].items.length === 1,
        'the idle doorbell exposed one durable legacy maintenance item', { batches });
      runner.assert(batches[0].items[0].kind === 'maintenance_prompt' &&
        squashWs(batches[0].items[0].content).includes(wantedCore),
      'the item body stayed in the inbox rather than the terminal prompt', { batches });
      runner.assert(Boolean(batches[0].items[0].read_at),
        'reading the inbox wrote the legacy item receipt', { batches });
      note('missing-hook doorbell read both inboxes and remains busy for the queued-item check', {
        state: observer.getSession(targetId)?.state,
      });
    });

    await runner.step('a_nudge_delivered_while_the_target_is_busy_wakes_on_idle', async () => {
      runner.assert(observer.getSession(targetId)?.state === 'working',
        'the first missing-hook doorbell still owns the busy turn before the second item');
      await legacyTicketRequest(socketPath, {
        cmd: 'ticket_comment',
        source_session_id: authorId,
        ticket_id: targetTicketId,
        comment: 'Busy-state follow-up.',
      });
      await pollFor(
        () => (observer.getSession(targetId)?.ticket_unread === true ? true : null),
        'busy target to show unread ticket activity',
        30_000,
        100,
      );
      runner.assert(observer.getSession(targetId)?.state === 'working', 'target is still working before busy nudge delivery');

      const busyClick = await client.request('click_nudge_trigger', {});
      runner.assert(busyClick?.clicked === true, `busy-state trigger button was clicked (got ${JSON.stringify(busyClick)})`, busyClick);
      note('delivered a second nudge while Codex was working', { surface: busyClick.surface });

      fs.writeFileSync(path.join(targetRepoDir, BUSY_RELEASE_FILE), 'release\n', 'utf8');

      await pollFor(
        () => (observer.getSession(targetId)?.ticket_unread === true ? null : true),
        'the busy-state queued nudge to consume the ticket inbox',
        120_000,
        250,
      );
      const busySettledState = await pollFor(
        () => {
          const state = observer.getSession(targetId)?.state;
          return IDLE_STATES.has(state) ? state : null;
        },
        'Codex to settle after the missing-hook and queued nudge turns',
        120_000,
        250,
      );
      const afterBusy = (await readPaneText(client, targetId)).text;
      runner.writeText('pane-after-busy-nudge.txt', afterBusy);
      runner.assert(
        squashWs(afterBusy).includes(squashWs('Busy-state follow-up.')),
        'the queued nudge turn read the busy-state ticket event (see pane-after-busy-nudge.txt)',
      );
      const transcript = readTargetTranscript(targetRepoDir);
      const messages = transcriptMessages(transcript);
      const doorbells = messages
        .filter((message) => message.role === 'user' && message.text === GENERIC_DOORBELL);
      runner.assert(doorbells.length === 2,
        'the idle and busy legacy items each produced one generic doorbell', { doorbells });
      runner.assert(doorbells.every((message) => !message.text.includes(LEGACY_ITEM_CORE)),
        'legacy item bodies were never injected as terminal prompts', { doorbells });
      const batches = inboxBatches(transcript);
      runner.assert(batches.length === 2 && batches[1].items.length === 1,
        'the later automatic wake exposed exactly the busy legacy item', { batches });
      runner.assert(Boolean(batches[1].items[0].read_at),
        'the busy legacy item got its own read receipt', { batches });
      runner.writeText('target-transcript.json', `${JSON.stringify(messages, null, 2)}\n`);
      note('busy-state item woke on idle after the earlier missing submit hook', { state: busySettledState });
    });

    const ticketId = await runner.step('arm_a_nudge_on_the_visible_unselected_pane', async () => {
      const created = await legacyTicketRequest(socketPath, {
        cmd: 'ticket_create',
        source_session_id: agentId,
        title: `Countdown cancel fixture ${runner.runId.slice(-6)}`,
      });
      const id = created.ticket_create_result?.ticket_id;
      runner.assert(typeof id === 'string' && id.length > 0, `ticket_create returned an id (got ${JSON.stringify(created)})`, created);

      // Back to the agent's workspace first: the codex target holds the selection,
      // and this leg needs the agent tile on screen but not selected.
      await client.request('select_session', { sessionId: agentId });
      await client.request('focus_pane', { sessionId: agentId, paneId: authorPaneId });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === authorId ? state : null;
      }, 'the split pane to take the selection off the agent', 20_000);
      note('split pane holds the selection; the agent tile is still on screen', { authorId });

      await legacyTicketRequest(socketPath, {
        cmd: 'ticket_comment',
        source_session_id: authorId,
        ticket_id: id,
        comment: 'Please take a look when you can.',
      });
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
      await pressCancelCountdown(client, driver);

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
      await legacyTicketRequest(socketPath, {
        cmd: 'ticket_comment',
        source_session_id: authorId,
        ticket_id: ticketId,
        comment: 'One more thing, after the cancel.',
      });
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

    const summary = await runner.finishSuccess({ agentId, authorId, targetId, ticketId, targetTicketId });
    console.log('[countdown-cancel] PASS — a real Cmd+. and a real pointer reached both visible countdowns, and the deliver-now button submitted its doorbell.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { agentId, authorId, targetId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' }).catch(() => {});
    if (targetId) await client.request('close_session', { sessionId: targetId }).catch(() => {});
    if (agentId) await client.request('close_session', { sessionId: agentId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
