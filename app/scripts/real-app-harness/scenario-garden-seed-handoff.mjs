#!/usr/bin/env node
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';
import { delay } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { recordingEnabled } from './windowRecording.mjs';
import fs from 'node:fs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

// Short enough to survive any pane width without wrapping.
const HANDOFF = 'PICKUP7 start at freshestHandoff';
const SEED_TITLE = 'carry a seed across two sessions without losing where the handoff belongs';
// Garden columns start at 1160px. The 24px fullscreen inset leaves 96px of room at this width.
const WIDE_GARDEN_WINDOW = 1280;

function occurrences(haystack, needle) {
  let count = 0;
  let at = haystack.indexOf(needle);
  while (at !== -1) {
    count += 1;
    at = haystack.indexOf(needle, at + needle.length);
  }
  return count;
}

// The flow runs in about five seconds; a recorded run holds each answer on
// screen long enough to read.
const PACE_MS = recordingEnabled() ? 1_400 : 0;

async function pace() {
  if (PACE_MS > 0) await delay(PACE_MS);
}

async function paneText(client, pane) {
  const payload = await client.request('read_pane_text', pane);
  return payload.text || '';
}

// Wait for a NEW occurrence: the pane still shows what earlier commands
// printed, so "contains it" would pass before this command ran.
async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const before = occurrences(await paneText(client, pane), expected);
  await client.request('write_pane', { ...pane, text: command });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = await paneText(client, pane);
    if (occurrences(text, expected) > before) {
      await pace();
      return text;
    }
  }
  throw new Error(`pane never answered ${JSON.stringify(command)} with ${JSON.stringify(expected)}:\n${text}`);
}

async function openPane(client, observer, runner, label) {
  const cwd = path.join(runner.sessionDir, label);
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client, observer, cwd, label, agent: 'shell',
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${label}`, 20_000);
  return { sessionId, paneId: pane.paneId };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-seed-handoff');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenSeedHandoff',
    tier: 'local',
    prefix: 'garden-seed-handoff',
  });

  let paneA = null;
  let paneB = null;
  let seedID = null;
  let authorID = null;
  try {
    await launchFreshAppAndConnect(client, observer);
    const initialBounds = await client.request('get_window_bounds');
    await client.request('set_window_bounds', {
      logicalBounds: { ...initialBounds.logicalBounds, width: WIDE_GARDEN_WINDOW },
    });

    paneA = await runner.step('open_session_a', () => openPane(client, observer, runner, 'gardenA'));

    seedID = await runner.step('a_plants_and_tends', async () => {
      // A shell pane carries no session identity of its own, so the tender has
      // to be named. An agent pane has ATTN_SESSION_ID and passes nothing.
      const planted = await runInPane(client, paneA,
        `attn seed plant "${SEED_TITLE}" --session ${paneA.sessionId}`, 's-');
      const ids = [...planted.matchAll(/^\s*(s-[a-z0-9]{5,})(?:\s|$)/gm)].map((match) => match[1]);
      const id = ids[ids.length - 1];
      runner.assert(Boolean(id), 'plant answered with a seed id', { planted });
      const claimed = await runInPane(client, paneA, `attn seed tend ${id} --session ${paneA.sessionId}`, 'is growing');
      runner.assert(claimed.includes('is growing'), 'A claimed the seed', { claimed });
      return id;
    });

    await runner.step('the_panel_shows_the_seed', async () => {
      await client.request('open_dock_panel', { panelId: 'garden' });
      await delay(500);
      const dock = await client.request('garden_get_state');
      const row = dock.seeds.find((candidate) => candidate.id === seedID);
      runner.assert(row?.displayId === seedID,
        'the dock list always shows the seed id', { row, seedID });
      runner.assert(row?.tender === 'tended by gardenA',
        'the dock list names the tender session instead of exposing its id', { row, sessionId: paneA.sessionId });
      const shot = await client.request('capture_screenshot_data', { selector: '.garden-panel' });
      runner.assert(Boolean(shot?.pngBase64), 'the garden panel is on screen', {});
      fs.writeFileSync(path.join(runner.runDir, 'garden-list-dock.png'), Buffer.from(shot.pngBase64, 'base64'));

      let full = await client.request('garden_toggle_frame');
      const layoutStartedAt = Date.now();
      // The frame's geometry can settle before ResizeObserver updates the list.
      // Wait for the rendered columns; keep the last state if they never appear.
      while (full.frame === 'full' && full.layout !== 'columns' && Date.now() - layoutStartedAt < 5_000) {
        await delay(100);
        full = await client.request('garden_get_state');
      }
      runner.assert(full.frame === 'full', 'the same list expands to the full window', { full });
      runner.assert(full.layout === 'columns', 'the wide full window uses the column list', {
        full, layoutWaitMs: Date.now() - layoutStartedAt,
      });
      const fullShot = await client.request('capture_screenshot_data', { selector: '.garden-frame' });
      runner.assert(Boolean(fullShot?.pngBase64), 'the full Garden list is on screen', {});
      fs.writeFileSync(path.join(runner.runDir, 'garden-list-full.png'), Buffer.from(fullShot.pngBase64, 'base64'));
      await pace();
      await client.request('dom_click', { selector: '.garden-chrome__close' });
    });

    await runner.step('a_leaves_a_handoff_and_ends', async () => {
      const left = await runInPane(client, paneA,
        `attn seed note ${seedID} -m "${HANDOFF}" --handoff --session ${paneA.sessionId}`, 'handoff left on');
      runner.assert(left.includes('handoff left on'), 'the note was recorded as a handoff', { left });
      const ended = paneA.sessionId;
      authorID = ended;
      await client.request('close_session', { sessionId: ended });
      await observer.waitFor(
        () => !observer.sessionsById.has(ended),
        'session A is a session the daemon no longer knows',
        30_000,
      );
      paneA = null;
      await pace();
    });

    paneB = await runner.step('open_session_b', () => openPane(client, observer, runner, 'gardenB'));

    await runner.step('b_tends_and_is_primed', async () => {
      const claimed = await runInPane(client, paneB, `attn seed tend ${seedID} --session ${paneB.sessionId}`, HANDOFF);
      runner.assert(claimed.includes('is growing'), 'B claimed the seed A let go of', { claimed });
      runner.assert(
        claimed.lastIndexOf('is growing') < claimed.lastIndexOf(HANDOFF),
        'the claim is confirmed before the handoff', { claimed });
      // The id wraps in a narrow pane; assert the part that cannot straddle it.
      runner.assert(claimed.includes(`handoff — ${authorID.slice(0, 8)}`),
        'the handoff names the session that left it', { claimed, authorID });
      runner.writeText('tend.txt', claimed + '\n');
    });

    await runner.step('show_puts_the_handoff_first', async () => {
      const shown = await runInPane(client, paneB, `attn seed show ${seedID}`, 'handoff — ');
      const handoffAt = shown.lastIndexOf('handoff — ');
      runner.assert(handoffAt < shown.lastIndexOf('status'), 'the handoff is above the seed', { shown });
      runner.writeText('show.txt', shown + '\n');
    });

    await runner.step('b_harvests', async () => {
      const done = await runInPane(client, paneB,
        `attn seed harvest ${seedID} -m "the handoff reached its successor" --session ${paneB.sessionId}`,
        'is harvested');
      runner.assert(done.includes('is harvested'), 'harvest closed the seed', { done });
    });

    const summary = await runner.finishSuccess({ seedID, sessionB: paneB.sessionId });
    console.log('[RealAppHarness] Garden seed handoff passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { seedID });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const pane of [paneA, paneB]) {
      if (pane) await client.request('close_session', { sessionId: pane.sessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
