#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  legacyTicketRequest,
  parseCommonArgs,
  printCommonHelp,
  submitPrompt,
} from './common.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';
import { ensureCodexPromptReadyViaPty } from './scenarioAgents.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { transcriptMessages, writeMockAgentFixture } from './mockAgent.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, socketPathForProfile } from './harnessProfile.mjs';

const BUSY_RELEASE_FILE = 'busy-turn-release';

const GENERIC_DOORBELL = '📬 You have unread items in your attn inbox. Run attn agent inbox to read them.';
// Mirrors ticketNudgePrompt minus the leading emoji, which the grid can split.
const LEGACY_ITEM_CORE = 'Activity on a ticket that predates the garden — run `attn ticket inbox` to read and acknowledge it.';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 250) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

const IDLE_STATES = new Set(['idle', 'waiting_input']);

const squashWs = (text) => text.replace(/\s+/g, '');

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

async function driveAgentToIdle(client, observer, sessionId, note) {
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${sessionId}`, 20_000);
  const reply = 'initial turn ready';
  const prompt = `Reply with the exact words: ${reply}`;
  await submitPrompt(client, sessionId, pane.paneId, prompt);
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
    printCommonHelp('scripts/real-app-harness/scenario-nudge-trigger.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the nudge-trigger scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const socketPath = socketPathForProfile(profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'NUDGE-TRIGGER',
    tier: 'tier2-local-fake-agent',
    prefix: 'nudge-trigger',
    metadata: {
      agent: 'mock-codex',
      focus: 'legacy ticket items use the generic inbox across missing-hook and busy delivery paths',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let targetId = null;
  let authorId = null;
  const note = (m, extra) => runner.log(m, extra);

  // Runner cleanups run in REVERSE registration order: observer/app are
  // registered first so they close LAST, sessions last so they close FIRST.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { repoDir } = await runner.step('create_repo_fixture', async () => {
      const dir = path.join(runner.sessionDir, 'target-repo');
      fs.mkdirSync(dir, { recursive: true });
      execFileSync('git', ['init', '-q'], { cwd: dir });
      execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
        cwd: dir,
        env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
      });
      writeMockAgentFixture(dir, {
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
      return { repoDir: dir };
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('boot_target_and_drive_idle', async () => {
      targetId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: repoDir,
        label: `nudge-target-${runner.runId.slice(-6)}`,
        agent: 'codex',
        sessionWaitMs: 30_000,
        promptReadyFn: ensureCodexPromptReadyViaPty,
        promptReadyTimeoutMs: 90_000,
      });
      runner.registerCleanup('close_target_session', () => client.request('close_session', { sessionId: targetId }));
      await client.request('select_session', { sessionId: targetId });
      const idleState = await driveAgentToIdle(client, observer, targetId, note);
      note(`target agent idle and selected`, { targetId, state: idleState });
    });

    const ticketId = await runner.step('create_ticket_fixture', async () => {
      const ticketTitle = `Nudge trigger fixture ${runner.runId.slice(-6)}`;
      const created = await legacyTicketRequest(socketPath, {
        cmd: 'ticket_create',
        source_session_id: targetId,
        title: ticketTitle,
      });
      const id = created.ticket_create_result?.ticket_id;
      runner.assert(typeof id === 'string' && id.length > 0, `ticket_create returned an id (got ${JSON.stringify(created)})`, created);
      note(`ticket created by target (creator-participant)`, { ticketId: id });

      authorId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: repoDir,
        label: `nudge-author-${runner.runId.slice(-6)}`,
        agent: 'shell',
        sessionWaitMs: 30_000,
      });
      runner.registerCleanup('close_author_session', () => client.request('close_session', { sessionId: authorId }));
      // Re-select the target: creating B selects B, which would un-pause A's nudge.
      await client.request('select_session', { sessionId: targetId });
      note(`author shell ready`, { authorId });

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
      note(`author posted overlapping ticket activity -> should produce one target doorbell`);
      return id;
    });

    await runner.step('assert_paused_gate', async () => {
      const unread = await pollFor(
        () => {
          const s = observer.getSession(targetId);
          return s && s.ticket_unread === true ? s : null;
        },
        `target ${targetId} to show unread ticket activity`,
        30_000,
      );
      note(`target shows unread ticket activity`, {
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

    await runner.step('deliver_idle_nudge', async () => {
      const beforeClick = await readPaneText(client, targetId);
      runner.writeText('pane-before-click.txt', beforeClick.text);
      runner.assert(
        !beforeClick.text.includes(GENERIC_DOORBELL),
        `no doorbell injected before the click (the countdown gate held); pane unexpectedly contains "${GENERIC_DOORBELL}"`,
      );
      note(`gate held: no doorbell in target pane before click`);

      try {
        const shot = await client.request('capture_screenshot_data', { selector: '.nudge-header-trigger' });
        if (shot?.pngBase64) {
          fs.writeFileSync(path.join(runner.runDir, 'paused-trigger-button.png'), Buffer.from(shot.pngBase64, 'base64'));
        }
      } catch (error) {
        console.warn(`[nudge-trigger] paused-button screenshot skipped: ${error instanceof Error ? error.message : String(error)}`);
      }

      const clickRes = await client.request('click_nudge_trigger', {});
      runner.assert(clickRes?.clicked === true, `the trigger button was found and clicked (got ${JSON.stringify(clickRes)})`, clickRes);
      note(`clicked the deliver-now trigger`, { surface: clickRes.surface });

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
      note(`doorbell submitted: Codex entered working state`);

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
      const transcript = readTargetTranscript(repoDir);
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
      note(`missing-hook doorbell read both inboxes and remains busy for the queued-item check`, {
        state: observer.getSession(targetId)?.state,
      });
    });

    await runner.step('queue_legacy_item_then_wake_on_idle', async () => {
      runner.assert(observer.getSession(targetId)?.state === 'working',
        'the first missing-hook doorbell still owns the busy turn before the second item');
      await legacyTicketRequest(socketPath, {
        cmd: 'ticket_comment',
        source_session_id: authorId,
        ticket_id: ticketId,
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
      note(`delivered a second nudge while Codex was working`, { surface: busyClick.surface });

      fs.writeFileSync(path.join(repoDir, BUSY_RELEASE_FILE), 'release\n', 'utf8');

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
      const transcript = readTargetTranscript(repoDir);
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
      note(`busy-state item woke on idle after the earlier missing submit hook`, { state: busySettledState });
    });

    const summary = await runner.finishSuccess({ targetId, authorId, ticketId });
    console.log('[nudge-trigger] Nudge trigger scenario passed: generic inbox doorbells consumed idle and busy legacy ticket activity without submit hooks.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { targetId, authorId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (authorId) await client.request('close_session', { sessionId: authorId }).catch(() => {});
    if (targetId) await client.request('close_session', { sessionId: targetId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
