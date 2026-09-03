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
} from './common.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';
import { ensureCodexPromptReadyViaPty } from './scenarioAgents.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, socketPathForProfile } from './harnessProfile.mjs';

const DOORBELL_SUBSTRING = 'Activity on a ticket that predates the garden';
// Mirrors ticketNudgePrompt minus the leading emoji, which the grid can split.
const DOORBELL_CORE = 'Activity on a ticket that predates the garden — run `attn ticket inbox` to read and acknowledge it.';

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

// The mock agent submits on any CR outside a bracketed paste, and nothing between
// write_pane and its stdin adds paste markers, so no gap belongs before the CR.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('write_pane', { sessionId, paneId, text, submit: true });
}

async function readPaneText(client, sessionId) {
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${sessionId}`, 20_000);
  const res = await client
    .request('read_pane_text', { sessionId, paneId: pane.paneId }, { timeoutMs: 20_000 })
    .catch(() => null);
  return { paneId: pane.paneId, text: res?.text || '' };
}

// A booted agent reaches `idle` only after a completed turn.
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
      focus: 'ticket-nudge deliver-now button, idle and busy delivery paths',
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
            includes: 'ticket that predates the garden',
            actions: [
              { type: 'attn', args: ['ticket', 'inbox'] },
            ],
          },
          {
            includes: 'initial turn ready',
            actions: [
              { type: 'reply', text: 'initial turn ready' },
            ],
          },
          {
            includes: 'foreground turn finished',
            actions: [
              { type: 'delay', ms: 8_000 },
              { type: 'reply', text: 'foreground turn finished' },
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
        !beforeClick.text.includes(DOORBELL_SUBSTRING),
        `no doorbell injected before the click (the countdown gate held); pane unexpectedly contains "${DOORBELL_SUBSTRING}"`,
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
        30_000,
        100,
      ).catch(() => null);

      let afterText = (await readPaneText(client, targetId)).text;
      runner.writeText('pane-after-click.txt', afterText);
      runner.assert(
        started === true,
        'the delivered doorbell started a Codex turn instead of remaining in the composer (see pane-after-click.txt)',
      );
      note(`doorbell submitted: Codex entered working state`);

      const settledState = await pollFor(
        () => {
          const state = observer.getSession(targetId)?.state;
          return IDLE_STATES.has(state) ? state : null;
        },
        'Codex to finish the nudge turn',
        90_000,
        250,
      );
      await pollFor(
        () => (observer.getSession(targetId)?.ticket_unread === true ? null : true),
        'the submitted nudge turn to consume the ticket inbox',
        30_000,
        250,
      );

      afterText = (await readPaneText(client, targetId)).text;
      runner.writeText('pane-after-settle.txt', afterText);
      const wantedCore = squashWs(DOORBELL_CORE);
      runner.assert(
        squashWs(afterText).includes(wantedCore),
        `the submitted transcript contains the complete doorbell message (see pane-after-settle.txt)`,
      );
      note(`nudge turn settled with inbox consumed and no stranded composer text`, { state: settledState });
    });

    await runner.step('deliver_busy_nudge', async () => {
      const pane = await waitForFirstWorkspacePane(client, targetId, `pane for ${targetId}`, 20_000);
      const busyPrompt = 'Run `sleep 8`, then reply with the exact words: foreground turn finished';
      await submitPrompt(client, targetId, pane.paneId, busyPrompt);
      await pollFor(
        () => (observer.getSession(targetId)?.state === 'working' ? true : null),
        'Codex to start the foreground busy turn',
        30_000,
        100,
      );

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
        'Codex to settle after the foreground and queued nudge turns',
        120_000,
        250,
      );
      const afterBusy = (await readPaneText(client, targetId)).text;
      runner.writeText('pane-after-busy-nudge.txt', afterBusy);
      runner.assert(
        squashWs(afterBusy).includes(squashWs('Busy-state follow-up.')),
        'the queued nudge turn read the busy-state ticket event (see pane-after-busy-nudge.txt)',
      );
      note(`busy-state nudge processed through Codex queue semantics`, { state: busySettledState });
    });

    const summary = await runner.finishSuccess({ targetId, authorId, ticketId });
    console.log('[nudge-trigger] Nudge trigger scenario passed: idle and busy Codex nudges submitted and consumed ticket activity.');
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
