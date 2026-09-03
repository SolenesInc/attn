#!/usr/bin/env node
import path from 'node:path';
import fs from 'node:fs';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { waitForFirstWorkspacePane, waitForPaneShellReady } from './scenarioAssertions.mjs';
import { ensureCodexPromptReadyViaPty } from './scenarioAgents.mjs';
import { delay } from './platform.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const BRIEF = 'Reply with exactly GSREOPEN_READY and then wait for the user.';
const HANDOFF = 'Continue this same seed from the resumed conversation.';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

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

// The marker appears twice, once as typed and once as the shell prints it, so
// the command's output is what lies between them.
async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const mark = `mark${++marks}x`;
  await client.request('write_pane', { ...pane, text: `${command}; echo ${mark}` });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    const raw = (await client.request('read_pane_text', pane)).text || '';
    text = flat(raw);
    if (raw.split('\n').some((line) => line.trim() === mark)) {
      const typed = text.lastIndexOf(`echo ${mark}`);
      const first = typed >= 0 ? typed + `echo ${mark}`.length : text.indexOf(mark) + mark.length;
      const out = text.slice(first, text.lastIndexOf(mark));
      if (saw(out, expected)) return out;
      throw new Error(`${JSON.stringify(command)} did not answer with ${JSON.stringify(expected)}:\n${out}`);
    }
  }
  throw new Error(`pane never finished ${JSON.stringify(command)}:\n${text}`);
}


async function openPane(client, observer, runner, label) {
  const cwd = path.join(runner.sessionDir, label);
  fs.mkdirSync(cwd, { recursive: true });
  writeMockAgentFixture(cwd, {
    // Resume needs the rollout where the codex driver looks, not under the cwd.
    resumable: true,
    name: 'reopen mock',
    turns: [{
      includes: 'GSREOPEN_READY',
      actions: [{ type: 'reply', text: 'GSREOPEN_READY', state: 'waiting_input' }],
    }],
  });
  const sessionId = await createSessionAndWaitForInitialPane({
    client, observer, cwd, label, agent: 'shell',
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${label}`, 20_000);
  return { sessionId, paneId: pane.paneId };
}

function seedIDs(text) {
  return [...flat(text).matchAll(/(s-[a-z0-9]{6})/g)].map((match) => match[1]);
}

async function pollFor(fn, description, timeoutMs = 20_000, intervalMs = 250) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

async function waitForRenderedReply(client, sessionId, expected, timeoutMs = 120_000) {
  const pane = await waitForFirstWorkspacePane(client, sessionId, `reply pane for ${sessionId}`, 20_000);
  const deadline = Date.now() + timeoutMs;
  let last = '';
  while (Date.now() < deadline) {
    last = (await client.request('read_pane_text', { sessionId, paneId: pane.paneId })).text || '';
    const answered = last.split('\n').some((line) => line.trim().replace(/^• /, '') === expected);
    if (answered && !last.includes('Working (')) return;
    await delay(250);
  }
  last = flat(last);
  throw new Error(`Timed out waiting for ${JSON.stringify(expected)} from ${sessionId}:\n${last}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-seed-reopen');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenSeedReopen',
    tier: 'local',
    prefix: 'garden-seed-reopen',
  });

  let pane = null;
  let delegated = null;
  let seed = null;
  let reopened = null;
  let handedOver = null;
  try {
    await launchFreshAppAndConnect(client, observer);
    pane = await runner.step('open_session', () => openPane(client, observer, runner, 'dispatcher'));

    delegated = await runner.step('dispatch_a_delegation', async () => {
      const known = new Set(observer.sessionsById.keys());
      const delegateName = `gsr-${pane.sessionId.slice(0, 8)}`;
      await client.request('write_pane', {
        ...pane,
        text: `attn delegate --agent codex --model gpt-5.4-mini --effort low --yolo --new-workspace --no-worktree ` +
          `--source-session ${pane.sessionId} --name ${delegateName} --brief "${BRIEF}"`,
      });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the delegated session exists', 60_000);
      await client.request('select_session', { sessionId: pane.sessionId });
      await waitForPaneShellReady(client, pane.sessionId, pane.paneId, {
        description: 'dispatcher shell after delegation',
        timeoutMs: 30_000,
      });
      await ensureCodexPromptReadyViaPty(client, spawned, 60_000);
      await client.request('select_session', { sessionId: spawned });
      await waitForRenderedReply(client, spawned, 'GSREOPEN_READY');
      return spawned;
    });

    seed = await runner.step('the_delegate_pane_names_its_seed', async () => {
      const listed = await runInPane(client, pane, 'attn seed ls', delegated);
      const planted = seedIDs(listed)[0];
      runner.assert(Boolean(planted), 'the delegation planted a seed', { listed });

      await client.request('select_session', { sessionId: delegated });
      const chip = await pollFor(
        async () => {
          const state = await client.request('session_seed_chip_get_state', { sessionId: delegated });
          return state.present ? state : null;
        },
        'the delegated pane to carry its seed chip',
      );
      runner.assert(chip.id === planted,
        'the chip points to the reporting seed', { chip, planted });
      runner.writeText('seed-chip.json', JSON.stringify(chip, null, 2) + '\n');
      return planted;
    });

    await runner.step('the_chip_opens_the_seed_as_a_tile', async () => {
      await client.request('dom_click', { selector: `[data-testid="seed-chip-${delegated}"]` });
      const tile = await pollFor(
        async () => {
          const state = await client.request('seed_document_get_state', { seedId: seed });
          return state.present ? state : null;
        },
        'the seed tile the chip opens',
      );
      runner.assert(saw(tile.body, BRIEF), 'the tile reads the brief as the seed body', { tile });
    });

    reopened = await runner.step('a_closed_tender_is_reopened_from_the_drill', async () => {
      await client.request('close_session', { sessionId: delegated });
      await observer.waitFor(() => !observer.sessionsById.has(delegated),
        'the delegated session to be unregistered', 20_000);

      await client.request('open_dock_panel', { panelId: 'garden' });
      await pollFor(
        async () => {
          const state = await client.request('garden_expand_seed', { seedId: seed, reopen: true });
          return state.resumeAvailable ? state : null;
        },
        'the panel drill to verify exact Resume availability',
      );

      const known = new Set(observer.sessionsById.keys());
      await client.request('garden_resume_seed', { seedId: seed });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the tender to be reopened as a session', 60_000);
      await ensureCodexPromptReadyViaPty(client, spawned, 60_000);
      return spawned;
    });

    await runner.step('the_reopened_session_reports_to_the_same_seed', async () => {
      const chip = await pollFor(
        async () => {
          const state = await client.request('session_seed_chip_get_state', { sessionId: reopened });
          return state.present ? state : null;
        },
        'the reopened pane to carry the same seed chip',
      );
      runner.assert(chip.id === seed,
        'the reopened session reports to the seed it was reopened from', { chip, seed });
    });

    handedOver = await runner.step('the_resumed_seed_is_handed_to_a_new_agent', async () => {
      await client.request('open_dock_panel', { panelId: 'garden' });
      await pollFor(
        async () => {
          const state = await client.request('garden_expand_seed', { seedId: seed, reopen: true });
          return state.handoverAvailable ? state : null;
        },
        'the resumed seed to be open for Handover',
      );

      const known = new Set(observer.sessionsById.keys());
      await client.request('garden_handover_seed', { seedId: seed, handoff: HANDOFF });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the Handover agent to start', 120_000);
      runner.assert(observer.sessionsById.has(reopened),
        'the old conversation remains open after Handover', { reopened, spawned });
      await ensureCodexPromptReadyViaPty(client, spawned, 60_000);
      return spawned;
    });

    await runner.step('the_handover_agent_reports_to_the_same_seed', async () => {
      const chip = await pollFor(
        async () => {
          const state = await client.request('session_seed_chip_get_state', { sessionId: handedOver });
          return state.present ? state : null;
        },
        'the Handover pane to carry the same seed chip',
      );
      runner.assert(chip.id === seed,
        'the Handover session reports to the same seed', { chip, seed });
      const shown = await runInPane(client, pane, `attn seed show ${seed}`, HANDOFF);
      runner.assert(saw(shown, HANDOFF), 'the confirmed handoff landed on the seed log', { shown });
    });

    const summary = await runner.finishSuccess({ seed, delegated, reopened, handedOver });
    console.log('[RealAppHarness] Garden seed continuation passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { seed, delegated, reopened, handedOver });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [handedOver, reopened, delegated, pane?.sessionId]) {
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
