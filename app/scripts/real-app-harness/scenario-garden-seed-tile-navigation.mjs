#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';
import { createWindowDriver, delay } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const PLOT = {
  title: 'Navigate a long plan',
  body: [
    '# The plan',
    '',
    'The plot remains available before this document, even when the plan is long.',
    '',
    ...Array.from({ length: 24 }, (_, index) => `## Decision ${index + 1}\n\nKeep the working context visible and annotatable.`),
  ].join('\n'),
  children: [
    { title: 'Shape the interaction', body: 'Make the hierarchy legible.' },
    { title: 'Polish the surface', body: 'Keep the terminal-adjacent UI restrained.' },
    { title: 'Verify the route', body: 'Walk in, back, and out to the Garden.' },
  ],
};

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

function squash(text) {
  return text.replace(/\s+/g, '');
}

let marks = 0;
let nativeInputUnavailable = false;

async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const mark = `mark${++marks}x`;
  await client.request('write_pane', { ...pane, text: `${command}; echo ${mark}` });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = (await client.request('read_pane_text', pane)).text || '';
    const compact = text.replace(/\n/g, '');
    const firstMark = compact.indexOf(mark);
    const lastMark = compact.lastIndexOf(mark);
    if (firstMark !== -1 && lastMark > firstMark) {
      const output = compact.slice(firstMark + mark.length, lastMark);
      if (squash(output).includes(squash(expected))) return output;
      throw new Error(`${JSON.stringify(command)} did not answer with ${JSON.stringify(expected)}:\n${output}`);
    }
  }
  throw new Error(`pane never finished ${JSON.stringify(command)}:\n${text}`);
}

async function openPane(client, observer, runner) {
  const cwd = path.join(runner.sessionDir, 'gardener');
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client, observer, cwd, label: 'gardener', agent: 'shell',
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, 'garden tile pane', 20_000);
  return { sessionId, paneId: pane.paneId };
}

function seedIDs(text) {
  return [...text.replace(/\n/g, '').matchAll(/(s-[a-z0-9]{6})/g)].map((match) => match[1]);
}

async function awaitSeedTile(client, seedID, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let state = { present: false };
  while (Date.now() < deadline) {
    state = await client.request('seed_document_get_state', { seedId: seedID });
    if (state.present) return state;
    await delay(200);
  }
  throw new Error(`the seed tile for ${seedID} never appeared: ${JSON.stringify(state)}`);
}

async function pressEscape(client, driver, seedID) {
  if (!nativeInputUnavailable) {
    try {
      await driver.pressKeyCode(53);
      return;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (!message.includes('display was off or the screen was locked')) throw error;
      nativeInputUnavailable = true;
      console.warn('[RealAppHarness] Native input unavailable; using the packaged DOM key bridge for Escape.');
    }
  }

  await client.request('dom_terminal_key', {
    selector: `.workspace-dock-tile:has(.seed-document[data-seed-id="${seedID}"]) .workspace-dock-tile-body`,
    key: 'Escape',
    code: 'Escape',
  });
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-seed-tile-navigation');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const driver = createWindowDriver({ appPath: options.appPath });
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenSeedTileNavigation',
    allowRealAgents: false,
    tier: 'local',
    prefix: 'garden-seed-tile-navigation',
  });

  let pane = null;
  let crown = null;
  let children = [];
  let nestedLeaf = null;
  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await launchFreshAppAndConnect(client, observer);
    pane = await runner.step('open_session', () => openPane(client, observer, runner));

    await runner.step('plant_and_open_the_plot', async () => {
      const payload = path.join(runner.sessionDir, 'plot.json');
      fs.writeFileSync(payload, JSON.stringify(PLOT));
      const planted = await runInPane(client, pane,
        `attn seed plot -f ${payload} --session ${pane.sessionId}`, 'verify-the-route');
      const ids = seedIDs(planted);
      runner.assert(ids.length >= 4, 'the fixture planted a crown and three children', { planted });
      [crown, ...children] = ids.slice(0, 4);
      const nestedPlanted = await runInPane(
        client,
        pane,
        `attn seed plant "Nested route" -m "Escape unwinds this recursive trail." --part-of ${children[0]} --session ${pane.sessionId}`,
        's-',
      );
      nestedLeaf = seedIDs(nestedPlanted).at(-1) || null;
      runner.assert(nestedLeaf !== null, 'the fixture planted a seed inside the first child', { nestedPlanted });
      await client.request('write_pane', {
        ...pane,
        text: `attn open ${crown} --session ${pane.sessionId}`,
      });
      await awaitSeedTile(client, crown);
    });

    await runner.step('plot_navigation_precedes_the_long_document', async () => {
      const state = await awaitSeedTile(client, crown);
      runner.assert(state.plotBeforeBody,
        'the plot is above the potentially long annotatable document', { state });
      runner.assert(!state.logOpen, 'the secondary log starts collapsed', { state });
      runner.assert(
        JSON.stringify(state.children.map((child) => child.id).sort()) === JSON.stringify([...children].sort()),
        'the plot offers every child', { state, children },
      );
      const screenshotPath = path.join(runner.runDir, 'seed-tile-plot.png');
      try {
        const shot = await client.request('capture_screenshot_data', {
          selector: `.seed-document[data-seed-id="${crown}"] .seed-document__plot`,
        }, { timeoutMs: 8_000 });
        if (shot.pngBase64) {
          fs.writeFileSync(screenshotPath, Buffer.from(shot.pngBase64, 'base64'));
        }
      } catch (error) {
        console.warn(`[RealAppHarness] Plot screenshot skipped: ${error instanceof Error ? error.message : String(error)}`);
      }
    });

    await runner.step('escape_unwinds_the_recursive_plot_trail', async () => {
      const child = children[0];
      await client.request('dom_click', {
        selector: `.seed-document[data-seed-id="${crown}"] [data-seed-target="${child}"]`,
      });
      const childState = await awaitSeedTile(client, child);
      runner.assert(childState.parent === PLOT.title,
        'the child exposes its canonical parent in the tile header', { childState });
      runner.assert(childState.children.some((seed) => seed.id === nestedLeaf),
        'the child is itself a plot with the nested seed available', { childState, nestedLeaf });

      await client.request('dom_click', {
        selector: `.seed-document[data-seed-id="${child}"] [data-seed-target="${nestedLeaf}"]`,
      });
      const nestedState = await awaitSeedTile(client, nestedLeaf);
      runner.assert(nestedState.parent === PLOT.children[0].title,
        'the nested seed exposes the intermediate plot as its parent', { nestedState });

      await client.request('dom_focus', {
        selector: `.workspace-dock-tile:has(.seed-document[data-seed-id="${nestedLeaf}"]) .workspace-dock-tile-body`,
      });
      await pressEscape(client, driver, nestedLeaf); // Nested seed → child plot.
      await awaitSeedTile(client, child);
      await pressEscape(client, driver, child); // Child plot → crown.
      const crownState = await awaitSeedTile(client, crown);
      runner.assert(crownState.children.length === children.length,
        'successive Escapes unwind one canonical plot edge at a time', { crownState });
    });

    await runner.step('reveal_transfers_the_current_place_to_the_garden', async () => {
      const child = children[0];
      await client.request('dom_click', {
        selector: `.seed-document[data-seed-id="${crown}"] [data-seed-target="${child}"]`,
      });
      await awaitSeedTile(client, child);
      await client.request('dom_click', {
        selector: `.workspace-dock-tile:has(.seed-document[data-seed-id="${child}"]) [aria-label="Reveal in Garden"]`,
      });
      const garden = await client.request('garden_get_state', {});
      runner.assert(garden.present && garden.here === PLOT.children[0].title,
        'Reveal in Garden opens the full Garden at the tile’s current child', { garden });
    });

    const summary = runner.finishSuccess({ crown, children, nestedLeaf });
    console.log('[RealAppHarness] Garden seed tile navigation passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { crown, children, nestedLeaf });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (pane) await client.request('close_session', { sessionId: pane.sessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
