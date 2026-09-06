#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import {
  runShellCommandInPane,
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { appDaemonInTree, delay } from './platform.mjs';
import { currentHarnessProfile, profileCliEnv } from './harnessProfile.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { recordingEnabled } from './windowRecording.mjs';

const SETTING = 'garden_needs_human_enabled';
const TITLE = 'Choose the Garden queue contract';
const QUESTION = 'Should queued decisions use the narrow inline answer?';
const ANSWER = 'Use the inline answer and keep the seed tended.';
const WITHDRAWN_QUESTION = 'Do we need another answer path?';
const PACE_MS = recordingEnabled() ? 1_400 : 0;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

function compact(text) {
  return String(text || '').replace(/\s+/g, '');
}

function saw(haystack, needle) {
  return compact(haystack).includes(compact(needle));
}

function seedIDs(text) {
  return [...String(text).matchAll(/(s-[a-z0-9]{6})/g)].map((match) => match[1]);
}

async function pace() {
  if (PACE_MS > 0) await delay(PACE_MS);
}

async function poll(read, description, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await read();
    if (last) return last;
    await delay(200);
  }
  throw new Error(`Timed out waiting for ${description}. Last value: ${JSON.stringify(last)}`);
}

async function waitForText(client, selector, needle, description) {
  return poll(async () => {
    const result = await client.request('dom_text', { selector }).catch(() => null);
    return result && saw(result.text, needle) ? result : null;
  }, description);
}

async function waitForGone(client, selector, description) {
  return poll(async () => {
    const result = await client.request('dom_text', { selector }).catch(() => null);
    return result === null ? true : null;
  }, description);
}

async function ensurePresent(client, selector, description) {
  return poll(
    () => client.request('dom_text', { selector }).catch(() => null),
    description,
  );
}

async function ensureQuestionQueueExpanded(client, seedId) {
  const row = `[data-testid="garden-question-open-${seedId}"]`;
  const present = await client.request('dom_text', { selector: row }).catch(() => null);
  if (!present) {
    await client.request('dom_click', { selector: '[data-testid="garden-needs-human-toggle"]' });
  }
  await ensurePresent(client, row, 'the expanded question queue');
}

async function screenshot(client, runner, filename, selector) {
  const shot = await client.request('capture_screenshot_data', { selector });
  runner.assert(Boolean(shot?.pngBase64), `${filename} has visible pixels`, { selector });
  fs.writeFileSync(path.join(runner.runDir, filename), Buffer.from(shot.pngBase64, 'base64'));
  await pace();
}

async function openPane(client, observer, runner) {
  const cwd = path.join(runner.sessionDir, 'raiser');
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client, observer, cwd, label: 'decision raiser', agent: 'shell',
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, 'the raiser shell pane', 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId);
  return { sessionId, paneId: pane.paneId };
}

async function runInRevealedPane(client, pane, command, expected) {
  await client.request('click_pane', pane);
  await waitForPaneVisible(client, pane.sessionId, pane.paneId);
  await waitForPaneAttached(client, pane.sessionId, pane.paneId);
  return runShellCommandInPane(client, pane, command, expected);
}

async function openSettings(client) {
  await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
  await client.request('settings_select_section', { sectionId: 'agents' });
  await client.request('dom_scroll_into_view', {
    selector: '[data-testid="settings-garden-needs-human-toggle"]',
  });
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-needs-human');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) throw new Error('Garden needs-human verification requires a named, non-production profile.');
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenNeedsHuman',
    tier: 'local',
    prefix: 'garden-needs-human',
    metadata: {
      focus: 'default-off refusal, a real ask/answer/withdraw lifecycle, and the pending-decision marks across the Garden',
    },
  });
  let pane = null;
  let seedId = null;
  let originalSetting = '';
  const runAttn = (args, sessionId = pane?.sessionId) => execFileSync(appDaemonInTree(options.appPath), args, {
    encoding: 'utf8',
    env: profileCliEnv(profile, { ATTN_SESSION_ID: sessionId || '' }),
  });

  try {
    await launchFreshAppAndConnect(client, observer);
    await client.request('dismiss_whats_new');
    originalSetting = observer.getSetting(SETTING);
    pane = await runner.step('plant_and_tend_while_questions_are_off', async () => {
      const shell = await openPane(client, observer, runner);
      const planted = await runShellCommandInPane(
        client,
        shell,
        `attn seed plant "${TITLE}" -m "Exercise the pending-decision queue end to end." --session ${shell.sessionId}`,
        's-',
      );
      seedId = seedIDs(planted).at(-1) || null;
      runner.assert(Boolean(seedId), 'plant returned the seed id', { planted });
      await runShellCommandInPane(
        client,
        shell,
        `attn seed tend ${seedId} --session ${shell.sessionId}`,
        'is growing',
      );
      runner.assert(originalSetting === 'false', 'pending decisions start off in daemon Settings', {
        setting: originalSetting,
      });
      const refused = await runShellCommandInPane(
        client,
        shell,
        `attn seed ask ${seedId} -m "${QUESTION}" --session ${shell.sessionId}`,
        SETTING,
      );
      runner.assert(saw(refused, `${SETTING} is off`), 'ask refuses and names the setting that enables it', { refused });
      return shell;
    });

    await runner.step('enable_pending_decisions_in_settings', async () => {
      await openSettings(client);
      const before = await client.request('dom_text', {
        selector: '[data-testid="settings-garden-needs-human-toggle"]',
      });
      runner.assert(before.text.trim() === 'Enable', 'Settings shows the feature is off', { before });
      await screenshot(
        client,
        runner,
        'setting-off.png',
        '.settings-block:has([data-testid="settings-garden-needs-human-toggle"])',
      );
      await client.request('dom_click', {
        selector: '[data-testid="settings-garden-needs-human-toggle"]',
      });
      await observer.waitFor(() => observer.getSetting(SETTING) === 'true', 'the enabled needs-human setting');
      const after = await waitForText(
        client,
        '[data-testid="settings-garden-needs-human-toggle"]',
        'Disable',
        'Settings to show the enabled feature',
      );
      runner.assert(after.text.trim() === 'Disable', 'the Settings control exposes the way back out', { after });
      await screenshot(
        client,
        runner,
        'setting-enabled.png',
        '.settings-block:has([data-testid="settings-garden-needs-human-toggle"])',
      );
      await client.request('dom_click', { selector: '[data-testid="settings-close"]' });
    });

    await runner.step('ask_and_walk_every_visible_mark', async () => {
      const asked = await runShellCommandInPane(
        client,
        pane,
        `attn seed ask ${seedId} -m "${QUESTION}" --session ${pane.sessionId}`,
        'stays growing',
      );
      runner.assert(saw(asked, `asked on ${seedId}`), 'the real CLI raises the question without parking the seed', { asked });

      await client.request('open_dock_panel', { panelId: 'garden' });
      await waitForText(client, '[data-testid="garden-needs-human"]', TITLE, 'the pending-decision band');
      const row = await waitForText(
        client,
        `[data-seed-row="${seedId}"]`,
        'waiting on you',
        'the Garden list mark',
      );
      runner.assert(saw(row.text, QUESTION), 'the list row carries the question beside its waiting mark', { row });
      await screenshot(client, runner, 'garden-band-and-list.png', '.garden-panel');

      await client.request('garden_expand_seed', { seedId, reopen: true });
      const page = await waitForText(
        client,
        '.garden-page .garden-question[aria-label="Question waiting on you"]',
        QUESTION,
        'the seed page question above its body',
      );
      runner.assert(saw(page.text, 'waiting on you'), 'the seed page uses the shared waiting vocabulary', { page });
      await screenshot(client, runner, 'garden-seed-page.png', '.garden-page');
      await client.request('garden_climb_to', { depth: 0 });

      await client.request('garden_toggle_frame');
      await client.request('dom_click', { selector: '.garden-view-switch button:last-child' });
      await poll(async () => {
        const state = await client.request('garden_board_get_state', {});
        return state.present ? state : null;
      }, 'the Garden board');
      const card = await waitForText(
        client,
        `.garden-card[data-seed="${seedId}"]`,
        'waiting on you',
        'the board-card waiting mark',
      );
      runner.assert(saw(card.text, QUESTION), 'the board card carries the open question', { card });
      await screenshot(client, runner, 'garden-board-card.png', `.garden-card[data-seed="${seedId}"]`);
      await client.request('dom_click', { selector: '.garden-view-switch button:first-child' });
      await client.request('garden_toggle_frame');

      await client.request('click_pane', pane);
      await waitForPaneVisible(client, pane.sessionId, pane.paneId);
      await client.request('write_pane', {
        ...pane,
        text: `attn open ${seedId} --session ${pane.sessionId}`,
      });
      await poll(async () => {
        const state = await client.request('seed_document_get_state', { seedId });
        return state.present ? state : null;
      }, 'the seed tile');
      const tile = await waitForText(
        client,
        `.seed-document[data-seed-id="${seedId}"] .garden-question[aria-label="Question waiting on you"]`,
        QUESTION,
        'the seed tile waiting mark',
      );
      runner.assert(saw(tile.text, 'waiting on you'), 'the tile uses the same pending-decision mark', { tile });
      await screenshot(
        client,
        runner,
        'garden-seed-tile.png',
        `.workspace-dock-tile:has(.seed-document[data-seed-id="${seedId}"])`,
      );
    });

    await runner.step('answer_inline_and_read_the_typed_log_note', async () => {
      await client.request('open_dock_panel', { panelId: 'garden' });
      await ensureQuestionQueueExpanded(client, seedId);
      const answerInput = `[data-testid="garden-question-answer-input-${seedId}"]`;
      const inputPresent = await client.request('dom_text', { selector: answerInput }).catch(() => null);
      if (!inputPresent) {
        await client.request('dom_click', { selector: `[data-testid="garden-question-open-${seedId}"]` });
      }
      await ensurePresent(client, answerInput, 'the inline answer field');
      await client.request('dom_type', {
        selector: answerInput,
        text: ANSWER,
      });
      await screenshot(client, runner, 'garden-inline-answer.png', '[data-testid="garden-needs-human"]');
      await client.request('dom_click', { selector: `[data-testid="garden-question-answer-${seedId}"]` });
      await waitForGone(
        client,
        `[data-testid="garden-question-open-${seedId}"]`,
        'the answered question to leave the queue',
      );
      const shown = JSON.parse(runAttn(['seed', 'show', seedId, '--json']));
      runner.assert(!shown.seed.question, 'answering clears the typed question state', { seed: shown.seed });
      const notes = await runInRevealedPane(
        client,
        pane,
        `attn seed notes ${seedId} --session ${pane.sessionId}`,
        ANSWER,
      );
      runner.assert(saw(notes, QUESTION) && saw(notes, ANSWER) && saw(notes, 'answer'),
        'the tender reads a typed answer note containing both sides of the decision', { notes });
      runner.writeText('answer-note.txt', `${notes}\n`);
    });

    await runner.step('withdraw_and_clear_the_second_question', async () => {
      await runShellCommandInPane(
        client,
        pane,
        `attn seed ask ${seedId} -m "${WITHDRAWN_QUESTION}" --session ${pane.sessionId}`,
        'stays growing',
      );
      await client.request('open_dock_panel', { panelId: 'garden' });
      await waitForText(client, '[data-testid="garden-needs-human"]', WITHDRAWN_QUESTION, 'the second question');
      await ensureQuestionQueueExpanded(client, seedId);
      const withdrawn = await runInRevealedPane(
        client,
        pane,
        `attn seed withdraw ${seedId} --session ${pane.sessionId}`,
        'withdrew the question',
      );
      runner.assert(saw(withdrawn, seedId), 'the raiser withdrew its own question', { withdrawn });
      const tombstone = await waitForText(
        client,
        `.garden-question-queue__row[data-seed-id="${seedId}"]`,
        'withdrew this question',
        'the withdrawn tombstone',
      );
      runner.assert(saw(tombstone.text, 'withdrawn'), 'the queue explains why the question vanished', { tombstone });
      await screenshot(client, runner, 'garden-withdrawn-tombstone.png', '[data-testid="garden-needs-human"]');
      await client.request('dom_click', { selector: `[data-testid="garden-question-clear-${seedId}"]` });
      await waitForGone(
        client,
        `.garden-question-queue__row[data-seed-id="${seedId}"]`,
        'Clear to remove the withdrawn tombstone',
      );
      const shown = JSON.parse(runAttn(['seed', 'show', seedId, '--json']));
      runner.assert(!shown.seed.question, 'Clear removes the withdrawn question state', { seed: shown.seed });
    });

    const summary = await runner.finishSuccess({ seedId, setting: SETTING });
    console.log('[RealAppHarness] Garden needs-human passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { seedId, setting: SETTING });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (seedId && pane?.sessionId) {
      try {
        runAttn(['seed', 'withdraw', seedId], pane.sessionId);
        await client.request('open_dock_panel', { panelId: 'garden' });
        await client.request('dom_click', {
          selector: `[data-testid="garden-question-clear-${seedId}"]`,
        });
      } catch {
        // The happy path has already answered or cleared the question.
      }
    }
    if (observer.connected && originalSetting) {
      await client.request('set_setting', { key: SETTING, value: originalSetting }).catch(() => {});
      await observer.waitFor(
        () => observer.getSetting(SETTING) === originalSetting,
        'the original needs-human setting',
      ).catch(() => {});
    }
    if (pane?.sessionId) await client.request('close_session', { sessionId: pane.sessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
