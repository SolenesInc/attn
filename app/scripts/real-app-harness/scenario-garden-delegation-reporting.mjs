#!/usr/bin/env node
import path from 'node:path';
import fs from 'node:fs';
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

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

const BRIEF = 'BRIEF9 carry the delegation on a seed';
const STEER = 'STEER4 the seed id is the address';

const PACE_MS = recordingEnabled() ? 1_400 : 0;

async function pace() {
  if (PACE_MS > 0) await delay(PACE_MS);
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

// A pane wraps at its own width and a break on a space swallows it, so every
// read here is matched with the whitespace taken out of both sides.
function flat(text) {
  return text.replace(/\n/g, '');
}

function squash(text) {
  return text.replace(/\s+/g, '');
}

function saw(haystack, needle) {
  return squash(haystack).includes(squash(needle));
}

let marks = 0;
// A docked seed tile weighs 1.5 terminals; 1440px keeps the terminal at 576px,
// above the 480px fold whose released surface reads empty.
const WIDE_WINDOW = 1440;

// The marker appears twice — in the line as typed and again as the shell
// prints it — so the output is what lies between them.
async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const mark = `mark${++marks}x`;
  await client.request('write_pane', { ...pane, text: `${command}; echo ${mark}` });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = flat(await paneText(client, pane));
    if (occurrences(text, mark) >= 2) {
      const first = text.indexOf(mark) + mark.length;
      const out = text.slice(first, text.lastIndexOf(mark));
      if (saw(out, expected)) {
        await pace();
        return out;
      }
      throw new Error(`${JSON.stringify(command)} did not answer with ${JSON.stringify(expected)}:\n${out}`);
    }
  }
  throw new Error(`pane never finished ${JSON.stringify(command)}:\n${text}`);
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

function seedIDs(text) {
  return [...flat(text).matchAll(/(s-[a-z0-9]{6})/g)].map((match) => match[1]);
}

// The tile is read by naming the seed: a workspace keeps older seed tiles
// mounted, and one of those answers too.
async function awaitTile(client, seedID, ready, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let state = { present: false, notes: [], artifacts: [] };
  while (Date.now() < deadline) {
    state = await client.request('seed_document_get_state', { seedId: seedID });
    if (state.present && ready(state)) return state;
    await delay(200);
  }
  throw new Error(`the seed tile for ${seedID} never caught up: ${JSON.stringify(state)}`);
}

// Re-reading the drill collapses and reopens it, because the document is
// fetched on the way in.
async function readDrill(client, seedID, ready, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let state = { present: false, notes: [], artifacts: [] };
  while (Date.now() < deadline) {
    state = await client.request('garden_expand_seed', { seedId: seedID, reopen: true, bookkeeping: true });
    if (state.present && ready(state)) return state;
    await delay(200);
  }
  throw new Error(`the panel drill for ${seedID} never caught up: ${JSON.stringify(state)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-delegation-reporting');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenDelegationReporting',
    tier: 'local',
    prefix: 'garden-delegation-reporting',
  });

  let pane = null;
  let delegated = null;
  let delegatePane = null;
  let seed = null;
  let artifactPath = null;
  try {
    await launchFreshAppAndConnect(client, observer);
    const initialBounds = await client.request('get_window_bounds');
    await client.request('set_window_bounds', {
      logicalBounds: { ...initialBounds.logicalBounds, width: WIDE_WINDOW },
    });
    pane = await runner.step('open_session', () => openPane(client, observer, runner, 'dispatcher'));

    delegated = await runner.step('dispatch_a_delegation', async () => {
      const known = new Set(observer.sessionsById.keys());
      await client.request('write_pane', {
        ...pane,
        text: `attn delegate --agent shell --model none --no-worktree --source-session ${pane.sessionId} ` +
          `--name delrep --brief "${BRIEF}"`,
      });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the delegated session exists', 60_000);
      await client.request('select_session', { sessionId: pane.sessionId });
      await runInPane(client, pane, 'true', '');
      return spawned;
    });

    seed = await runner.step('the_brief_is_a_seed_the_delegate_tends', async () => {
      const listed = await runInPane(client, pane, 'attn seed ls', delegated);
      const planted = seedIDs(listed)[0];
      runner.assert(Boolean(planted), 'the delegation planted a seed', { listed });
      const shown = await runInPane(client, pane, `attn seed show ${planted}`, BRIEF);
      runner.assert(saw(shown, BRIEF), 'the brief is the seed’s body', { shown });
      runner.assert(saw(shown, delegated),
        'the delegate session is the seed’s tender', { shown, delegated });
      runner.writeText('seed-show.txt', shown + '\n');
      return planted;
    });

    await runner.step('a_status_report_lands_on_the_log', async () => {
      const reported = await runInPane(client, pane,
        `attn seed note ${seed} -m "digging in" --session ${delegated}`, 'noted on');
      runner.writeText('seed-note.txt', reported + '\n');
      const log = await runInPane(client, pane, `attn seed notes ${seed}`, 'digging in');
      runner.assert(saw(log, 'digging in'),
        'the report’s comment is on the seed’s log', { log });
      runner.writeText('seed-notes.txt', log + '\n');
    });

    await runner.step('artifacts_are_a_projection_over_the_log', async () => {
      artifactPath = path.join(runner.sessionDir, 'evidence.md');
      fs.writeFileSync(artifactPath, '# Evidence\n\nWhat the delegate produced.\n');
      await runInPane(client, pane,
        `attn seed attach ${seed} --path ${artifactPath} --repo harness-fixture -m "the write-up" --session ${delegated}`,
        'attached');

      await client.request('open_dock_panel', { panelId: 'garden' });
      await delay(500);
      const drill = await readDrill(client, seed, (state) => state.notes.some((note) => note.kind === 'attach'));
      runner.assert(drill.present, 'the panel drill shows the seed document', { drill });
      runner.assert(drill.artifacts.some((artifact) => artifact.primary === 'evidence.md'),
        'the drill carries the current artifact', { drill });
      fs.writeFileSync(path.join(runner.runDir, 'drill-attached.png'),
        Buffer.from((await client.request('capture_screenshot_data',
          { selector: '.garden-panel' })).pngBase64, 'base64'));
      await pace();

      await runInPane(client, pane, `attn open ${seed} --session ${pane.sessionId}`, '');
      const tile = await awaitTile(client, seed, (state) => state.notes.some((note) => note.kind === 'attach'));
      runner.assert(tile.present, 'the seed opened as a tile', { tile });
      runner.assert(tile.artifacts.some((label) => label.includes('evidence.md')),
        'the tile carries the same artifact', { tile });
      runner.assert(tile.notes.some((note) => note.kind === 'attach'),
        'the attach is on the log as its own kind', { tile });
      fs.writeFileSync(path.join(runner.runDir, 'tile-attached.png'),
        Buffer.from((await client.request('capture_screenshot_data',
          { selector: '.workspace-dock-tile' })).pngBase64, 'base64'));
      await pace();

      await runInPane(client, pane,
        `attn seed detach ${seed} --path ${artifactPath} --repo harness-fixture -m "superseded" --session ${delegated}`,
        'detached');
      const afterTile = await awaitTile(client, seed, (state) => state.notes.some((note) => note.kind === 'detach'));
      runner.assert(afterTile.artifacts.length === 0,
        'detaching takes the artifact out of the set', { afterTile });
      runner.assert(afterTile.notes.some((note) => note.kind === 'detach'),
        'the detach stayed on the log', { afterTile });
      const afterDrill = await readDrill(client, seed, (state) => state.notes.some((note) => note.kind === 'detach'));
      runner.assert(afterDrill.artifacts.length === 0,
        'the drill agrees with the tile', { afterDrill });
      fs.writeFileSync(path.join(runner.runDir, 'drill-detached.png'),
        Buffer.from((await client.request('capture_screenshot_data',
          { selector: '.garden-panel' })).pngBase64, 'base64'));
      runner.writeText('artifacts.json', JSON.stringify({ drill, tile, afterTile, afterDrill }, null, 2) + '\n');
      await pace();
    });

    await runner.step('a_seed_id_steers_its_tender', async () => {
      delegatePane = await waitForFirstWorkspacePane(client, delegated, 'the delegate’s pane', 20_000);
      const sent = await runInPane(client, pane,
        `attn agent msg ${seed} "${STEER}" --source-session ${pane.sessionId}`, 'notified');
      runner.writeText('agent-msg.txt', sent + '\n');
      const messageID = sent.match(/\(id\s*([0-9a-f-]{36})\)/)?.[1] ?? null;
      runner.assert(Boolean(messageID), 'the steer returned its mailbox id', { sent });
      const read = await runInPane(client, { sessionId: delegated, paneId: delegatePane.paneId },
        `attn agent inbox ${messageID} --session ${delegated}`, STEER);
      const text = flat(await paneText(client, { sessionId: delegated, paneId: delegatePane.paneId }));
      runner.assert(saw(text, STEER),
        'the message addressed to the seed arrived in its tender’s pane', { text });
      runner.writeText('agent-inbox.txt', read + '\n');
      runner.writeText('delegate-pane.txt', text + '\n');
      await pace();
    });

    const summary = await runner.finishSuccess({ seed, delegated });
    console.log('[RealAppHarness] Garden delegation reporting passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { seed, delegated });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [delegated, pane?.sessionId]) {
      if (id) await client.request('close_session', { sessionId: id }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
