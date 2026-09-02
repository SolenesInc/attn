#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { delay } from './platform.mjs';
import {
  captureSessionArtifacts,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { recordingEnabled } from './windowRecording.mjs';

const UNKNOWN_SEED_ID = 's-000000';
const PACE_MS = recordingEnabled() ? 1_400 : 0;
const PREVIEW_SETTLE_MS = 360;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return {
    options: parseCommonArgs(args),
    help: args.includes('--help') || args.includes('-h'),
  };
}

function occurrences(haystack, needle) {
  let count = 0;
  let at = haystack.indexOf(needle);
  while (at !== -1) {
    count += 1;
    at = haystack.indexOf(needle, at + needle.length);
  }
  return count;
}

async function paneText(client, pane) {
  const payload = await client.request('read_pane_text', pane);
  return payload.text || '';
}

async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const before = occurrences(await paneText(client, pane), expected);
  await client.request('write_pane', { ...pane, text: command });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = await paneText(client, pane);
    if (occurrences(text, expected) > before) return text;
  }
  throw new Error(`pane never answered ${JSON.stringify(command)} with ${JSON.stringify(expected)}:\n${text}`);
}

async function waitForSelectorShot(client, selector, description, timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs;
  let lastError = null;
  while (Date.now() < deadline) {
    try {
      const shot = await client.request('capture_screenshot_data', { selector });
      if (shot?.bounds?.width > 0 && shot?.bounds?.height > 0) return shot;
      lastError = new Error(`selector has zero-sized bounds: ${JSON.stringify(shot?.bounds)}`);
    } catch (error) {
      lastError = error;
    }
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}: ${lastError}`);
}

async function selectorIsAbsent(client, selector) {
  try {
    await client.request('capture_screenshot_data', { selector });
    return false;
  } catch (error) {
    if (String(error).includes('Screenshot selector not found in DOM')) return true;
    throw error;
  }
}

async function waitForSelectorAbsent(client, selector, description, timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await selectorIsAbsent(client, selector)) return;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}`);
}

async function waitForSeedTile(client, seedId, timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs;
  let state = null;
  while (Date.now() < deadline) {
    state = await client.request('seed_document_get_state', { seedId });
    if (state.present) return state;
    await delay(200);
  }
  throw new Error(`Seed tile for ${seedId} did not open: ${JSON.stringify(state)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-seed-preview.mjs');
    return;
  }

  if (process.env.ATTN_HARNESS_PARK_VISIBLE_PX === undefined) {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '800';
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-SEED-PREVIEW',
    tier: 'tier1-local-shell',
    prefix: 'terminal-seed-preview',
    metadata: {
      focus: 'known seed mark, four-way glow preview, terminal-owned click, icon-only tile action',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;
  let pane = null;
  let seedId = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session', async () => {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
  });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_shell_session', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `seed-preview-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      await client.request('select_session', { sessionId });
      const workspace = await client.request('get_workspace', { sessionId });
      pane = workspace?.panes?.[0];
      runner.assert(Boolean(pane), 'workspace has an initial terminal pane', {
        workspaceId: workspace?.workspaceId ?? null,
      });
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, {
        timeoutMs: 20_000,
        description: 'shell pane ready',
      });
    });

    await runner.step('plant_and_render_seed_ids', async () => {
      const planted = await runInPane(
        client,
        { sessionId, paneId: pane.paneId },
        `attn seed plant "Terminal seed preview receipt" -m "Known IDs preview from terminal output." --session ${sessionId}`,
        's-',
      );
      const ids = [...planted.matchAll(/^\s*(s-[0-9a-hjkmnp-tv-z]{6})\b/gm)]
        .map((match) => match[1]);
      seedId = ids[ids.length - 1];
      runner.assert(Boolean(seedId), 'plant answered with a seed id', { ids });
      await runInPane(
        client,
        { sessionId, paneId: pane.paneId },
        `printf 'unknown ${UNKNOWN_SEED_ID}\\n'`,
        `unknown ${UNKNOWN_SEED_ID}`,
      );
    });

    const markSelector = () => `[data-terminal-seed-id="${seedId}"]`;
    const previewSelector = () => `[data-terminal-seed-preview="${seedId}"]`;
    let seedCell;
    let paneSize;
    await runner.step('only_the_known_id_is_marked', async () => {
      const mark = await waitForSelectorShot(client, markSelector(), 'known seed mark');
      runner.assert(Boolean(mark?.pngBase64 && mark?.bounds), 'known seed mark is rendered', { seedId });
      fs.writeFileSync(path.join(runner.runDir, 'seed-mark.png'), Buffer.from(mark.pngBase64, 'base64'));
      runner.assert(
        await selectorIsAbsent(client, `[data-terminal-seed-id="${UNKNOWN_SEED_ID}"]`),
        'unknown seed id remains plain terminal text',
        { unknownSeedId: UNKNOWN_SEED_ID },
      );

      const read = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
      const lines = read.text.split('\n');
      const row = lines.findLastIndex((line) => line.includes(seedId));
      const col = row >= 0 ? lines[row].indexOf(seedId) + Math.floor(seedId.length / 2) : -1;
      runner.assert(row >= 0 && col >= 0, 'seed id is visible in the terminal grid', {
        row,
        col,
        size: read.size,
      });
      seedCell = { row, col };
      paneSize = read.size;
    });

    await runner.step('preview_mirrors_around_terminal_edges', async () => {
      const centerCol = Math.max(1, Math.floor((paneSize.cols - seedId.length) / 2));
      const middleRow = Math.max(2, Math.floor(paneSize.rows / 2));
      const rightCol = Math.max(seedId.length + 2, paneSize.cols - seedId.length - 1);
      const bottomRow = Math.max(middleRow + 2, paneSize.rows - 6);
      const sentinelRow = Math.min(paneSize.rows - 5, bottomRow + 1);
      const screen = Array.from({ length: sentinelRow + 1 }, () => '');
      screen[0] = `${' '.repeat(centerCol)}${seedId}`;
      screen[middleRow] = `${seedId}${' '.repeat(rightCol - seedId.length)}${seedId}`;
      screen[bottomRow] = `${' '.repeat(centerCol)}${seedId}`;
      screen[sentinelRow] = 'EDGE_READY';
      const payload = Buffer.from(screen.join('\n')).toString('base64');
      const drawCommand = `clear; printf '%s' '${payload}' | /usr/bin/base64 --decode`;
      await runInPane(
        client,
        { sessionId, paneId: pane.paneId },
        drawCommand,
        'EDGE_READY',
      );

      const edgeCases = [
        { name: 'top', placement: 'below', row: 0, col: centerCol },
        { name: 'left', placement: 'right', row: middleRow, col: 0 },
        { name: 'right', placement: 'left', row: middleRow, col: rightCol },
        { name: 'bottom', placement: 'above', row: bottomRow, col: centerCol },
      ];
      for (const edge of edgeCases) {
        await client.request('hover_pane_cell', {
          sessionId,
          paneId: pane.paneId,
          cell: { row: edge.row, col: edge.col + Math.floor(seedId.length / 2) },
        });
        await delay(PREVIEW_SETTLE_MS);
        const shot = await waitForSelectorShot(
          client,
          `${previewSelector()}[data-placement="${edge.placement}"]`,
          `${edge.name}-edge ${edge.placement} seed preview`,
        );
        runner.assert(Boolean(shot?.bounds), `${edge.name}-edge preview opens ${edge.placement}`, {
          bounds: shot?.bounds ?? null,
        });
        fs.writeFileSync(
          path.join(runner.runDir, `seed-preview-${edge.name}.png`),
          Buffer.from(shot.pngBase64, 'base64'),
        );
        if (PACE_MS > 0) await delay(PACE_MS);
        await client.request('dom_key', {
          selector: previewSelector(),
          key: 'Escape',
        });
        await waitForSelectorAbsent(client, previewSelector(), `${edge.name}-edge preview to close`);
      }

      seedCell = {
        row: middleRow,
        col: Math.floor(seedId.length / 2),
      };
    });

    await runner.step('hover_opens_preview_and_hands_off_to_card', async () => {
      await client.request('hover_pane_cell', {
        sessionId,
        paneId: pane.paneId,
        cell: seedCell,
      });
      await delay(PREVIEW_SETTLE_MS);
      const preview = await waitForSelectorShot(client, previewSelector(), 'seed hover preview');
      runner.assert(Boolean(preview?.bounds), 'seed hover preview has visible bounds', {
        bounds: preview?.bounds ?? null,
      });
      fs.writeFileSync(path.join(runner.runDir, 'seed-preview.png'), Buffer.from(preview.pngBase64, 'base64'));

      await client.request('hover_pane_cell', {
        sessionId,
        paneId: pane.paneId,
        cell: {
          row: seedCell.row,
          col: Math.min(paneSize.cols - 1, seedCell.col + seedId.length + 4),
        },
      });
      await client.request('dom_hover', { selector: previewSelector() });
      await delay(500);
      const afterHandoff = await waitForSelectorShot(client, previewSelector(), 'preview after pointer handoff');
      runner.assert(Boolean(afterHandoff?.pngBase64), 'preview stays open while the pointer is inside it', {});
    });

    await runner.step('plain_click_remains_terminal_owned', async () => {
      await client.request('click_pane_cell', {
        sessionId,
        paneId: pane.paneId,
        cell: seedCell,
      });
      await delay(400);
      const tile = await client.request('seed_document_get_state', { seedId });
      runner.assert(!tile.present, 'plain click on a seed id does not open a tile', { tile });
    });

    await runner.step('icon_opens_seed_as_tile', async () => {
      await client.request('hover_pane_cell', {
        sessionId,
        paneId: pane.paneId,
        cell: seedCell,
      });
      await delay(PREVIEW_SETTLE_MS);
      await waitForSelectorShot(client, previewSelector(), 'seed preview reopened');
      await waitForSelectorShot(
        client,
        `${previewSelector()} .terminal-seed-preview__open`,
        'open-as-tile icon',
      );
      await client.request('dom_click', {
        selector: `${previewSelector()} .terminal-seed-preview__open`,
      });
      const tile = await waitForSeedTile(client, seedId);
      runner.assert(tile.body.includes('Known IDs preview'), 'tile shows the planted seed', { tile });
      if (PACE_MS > 0) await delay(PACE_MS);
    });

    const result = await runner.finishSuccess({ sessionId, paneId: pane.paneId, seedId });
    console.log('[verify] PASS — terminal seed marks, four-way glow preview, and tile action.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'terminal-seed-preview-failure', sessionId).catch(() => {});
    }
    const result = await runner.finishFailure(error, { sessionId, seedId });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
