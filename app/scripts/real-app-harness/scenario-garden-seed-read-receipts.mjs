#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
} from './scenarioAssertions.mjs';
import { ensureCodexPromptReadyViaPty } from './scenarioAgents.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { delay } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const FIRST_NOTE = 'FIRST_READ_RECEIPT';
const SECOND_NOTE = 'SECOND_READ_RECEIPT';
const READ_MARKER = 'SEED_BELL_READ';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

function flat(text) {
  return String(text).replace(/\s+/g, '');
}

function occurrences(haystack, needle) {
  return haystack.split(needle).length - 1;
}

let marks = 0;

async function paneText(client, pane) {
  const payload = await client.request('read_pane_text', pane);
  return payload.text || '';
}

async function runInShell(client, pane, command, expected, timeoutMs = 30_000) {
  const mark = `seedreadmark${++marks}x`;
  await client.request('write_pane', { ...pane, text: `${command}; echo ${mark}` });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = await paneText(client, pane);
    const flattened = flat(text);
    if (occurrences(flattened, mark) < 2) continue;
    const first = flattened.indexOf(mark) + mark.length;
    const output = flattened.slice(first, flattened.lastIndexOf(mark));
    if (output.includes(flat(expected))) return output;
    throw new Error(`${JSON.stringify(command)} did not answer with ${JSON.stringify(expected)}:\n${text}`);
  }
  throw new Error(`pane never finished ${JSON.stringify(command)}:\n${text}`);
}

async function waitForAgentReads(client, pane, count, note, timeoutMs = 60_000) {
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    text = await paneText(client, pane);
    const flattened = flat(text);
    if (occurrences(flattened, READ_MARKER) >= count && flattened.includes(note)) return text;
    await delay(250);
  }
  throw new Error(`agent did not complete seed read ${count} for ${note}:\n${text}`);
}

function writeWatcherFixture(cwd) {
  writeMockAgentFixture(cwd, {
    name: 'seed-read-receipt',
    minimumWorkingMs: 0,
    turns: [
      {
        includes: 'Watch seed',
        actions: [
          { type: 'capture', from: 'prompt', pattern: '(s-[a-z0-9]{6})', name: 'seed' },
          { type: 'attn', args: ['seed', 'watch', '{{seed}}'] },
          { type: 'reply', text: 'SEED_WATCH_READY', state: 'idle' },
        ],
      },
      {
        includes: '🔔',
        submitHook: false,
        actions: [
          { type: 'attn', args: ['seed', 'show', '{{seed}}'] },
          { type: 'reply', text: READ_MARKER, state: 'idle' },
        ],
      },
    ],
  });
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-garden-seed-read-receipts.mjs');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenSeedReadReceipts',
    tier: 'local',
    prefix: 'garden-seed-read-receipts',
    metadata: { agent: 'mock-codex', receipt: 'seed show without a prompt-submit hook' },
  });

  let author = null;
  let watcher = null;
  let seed = null;
  try {
    await launchFreshAppAndConnect(client, observer);

    author = await runner.step('plant_seed', async () => {
      const cwd = path.join(runner.sessionDir, 'author');
      fs.mkdirSync(cwd, { recursive: true });
      const sessionId = await createSessionAndWaitForInitialPane({
        client, observer, cwd, label: 'seed-read-author', agent: 'shell',
      });
      const first = await waitForFirstWorkspacePane(client, sessionId, 'author pane', 20_000);
      const pane = { sessionId, paneId: first.paneId };
      await waitForPaneShellReady(client, sessionId, first.paneId, {
        timeoutMs: 20_000,
        description: 'seed author shell ready',
      });
      const planted = await runInShell(
        client,
        pane,
        `attn seed plant "Seed bell read receipt" -m "Packaged proof for provider-neutral Garden delivery." --session ${sessionId}`,
        's-',
      );
      seed = planted.match(/s-[a-z0-9]{6}/)?.[0] ?? null;
      runner.assert(Boolean(seed), 'plant returned a seed id', { planted });
      return pane;
    });

    watcher = await runner.step('watch_from_mock_codex', async () => {
      const cwd = path.join(runner.sessionDir, 'watcher');
      writeWatcherFixture(cwd);
      const sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: 'seed-read-watcher',
        agent: 'codex',
        promptReadyFn: ensureCodexPromptReadyViaPty,
        promptReadyTimeoutMs: 90_000,
      });
      const first = await waitForFirstWorkspacePane(client, sessionId, 'watcher pane', 20_000);
      const pane = { sessionId, paneId: first.paneId };
      await client.request('write_pane', { ...pane, text: `Watch seed ${seed}`, submit: false });
      await delay(250);
      await client.request('write_pane', { ...pane, text: '\r', submit: false });
      await waitForAgentReads(client, pane, 0, 'SEED_WATCH_READY');
      return pane;
    });

    await runner.step('first_bell_is_read', async () => {
      await runInShell(
        client,
        author,
        `attn seed note ${seed} -m ${FIRST_NOTE} --ring --session ${author.sessionId}`,
        `noted on ${seed}`,
      );
      await waitForAgentReads(client, watcher, 1, FIRST_NOTE);
    });

    await runner.step('second_bell_arrives_without_a_human_prompt', async () => {
      await runInShell(
        client,
        author,
        `attn seed note ${seed} -m ${SECOND_NOTE} --ring --session ${author.sessionId}`,
        `noted on ${seed}`,
      );
      const text = await waitForAgentReads(client, watcher, 2, SECOND_NOTE);
      const flattened = flat(text);
      runner.assert(occurrences(flattened, `${seed}moved:note`) === 2,
        'each read cycle injected exactly one bell', { text });
      runner.writeText('watcher-pane.txt', `${text}\n`);
    });

    const summary = await runner.finishSuccess({ seed, watcherSessionId: watcher.sessionId });
    console.log('[RealAppHarness] Garden seed read receipts passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { seed, watcherSessionId: watcher?.sessionId ?? null });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [watcher?.sessionId, author?.sessionId]) {
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
