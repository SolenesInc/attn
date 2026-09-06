#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  submitPrompt,
  queryDaemonDb,
} from './common.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
} from './scenarioAssertions.mjs';
import { ensureCodexPromptReadyViaPty } from './scenarioAgents.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { transcriptMessages, writeMockAgentFixture } from './mockAgent.mjs';
import { delay, appDaemonInTree } from './platform.mjs';
import { currentHarnessProfile, dataDirForProfile, profileCliEnv } from './harnessProfile.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const FIRST_NOTE = 'FIRST_READ_RECEIPT';
const SECOND_NOTE = 'SECOND_READ_RECEIPT';
const READ_MARKER = 'SEED_BELL_READ';
const DISPATCH_PROMPT = 'Delegate the subscription proof';
const DISPATCHED_MARKER = 'SUBSCRIPTION_DELEGATED';
const HOLD_PROMPT = 'Hold the subscription proof turn';
const HOLD_READY = 'watch-proof-held';
const HOLD_RELEASE = 'watch-proof-release';
const HOLD_DONE = 'SUBSCRIPTION_HOLD_RELEASED';
// Every pane read here squashes whitespace out, so a body with a space in it
// could never be found again.
const PEER_BODY = 'PEER_MESSAGE_READ_RECEIPT_BODY';
const GENERIC_DOORBELL = '📬 You have unread items in your attn inbox. Run attn agent inbox to read them.';

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

function writeWatcherFixture(cwd, delegateCwd) {
  writeMockAgentFixture(cwd, {
    name: 'seed-read-receipt',
    resumable: true,
    minimumWorkingMs: 0,
    turns: [
      {
        includes: DISPATCH_PROMPT,
        actions: [
          { type: 'attn', args: ['delegate', '--agent', 'codex', '--model', 'gpt-5.6-sol', '--yolo', '--no-worktree',
            '--cwd', delegateCwd, '--plot', '{{seed}}', '--name', 'subscription-delegate',
            '--brief', 'Wait for the subscription proof.'] },
          { type: 'reply', text: DISPATCHED_MARKER, state: 'idle' },
        ],
      },
      {
        includes: HOLD_PROMPT,
        actions: [
          { type: 'touch', path: HOLD_READY },
          { type: 'wait_for_file', path: HOLD_RELEASE },
          { type: 'reply', text: HOLD_DONE, state: 'idle' },
        ],
      },
      {
        includes: 'Watch seed',
        actions: [
          { type: 'capture', from: 'prompt', pattern: '(s-[a-z0-9]{6})', name: 'seed' },
          { type: 'attn', args: ['seed', 'watch', '{{seed}}'] },
          { type: 'reply', text: 'SEED_WATCH_READY', state: 'idle' },
        ],
      },
      {
        includes: GENERIC_DOORBELL,
        submitHook: false,
        actions: [
          { type: 'attn', args: ['agent', 'inbox', '--json'] },
          { type: 'attn', args: ['seed', 'show', '{{seed}}'] },
          { type: 'reply', text: READ_MARKER, state: 'idle' },
        ],
      },
    ],
  });
}

// Pane reads squash whitespace out, so no space survives between `id` and the uuid.
function peerMessageID(output) {
  return output.match(/\(id([0-9a-f-]{36})\)/)?.[1] ?? null;
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

function readWatcherTranscript(sessionID) {
  const file = queryDaemonDb(path.join(dataDirForProfile(currentHarnessProfile()), 'attn.db'),
    `SELECT transcript_path FROM sessions WHERE id = '${sessionID}'`);
  if (!file) throw new Error(`no mock transcript for ${sessionID}`);
  return fs.readFileSync(file, 'utf8');
}

function mockTranscript(sessionID) {
  return transcriptMessages(readWatcherTranscript(sessionID));
}

function saw(text, expected) {
  return flat(text).includes(flat(expected));
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
    metadata: {
      agent: 'mock-codex',
      receipt: 'agent inbox without a prompt-submit hook',
      focus: 'a read, not a hook, re-arms the next doorbell through a real PTY — '
        + 'on the Garden bell and on the peer mailbox, with the bodies only the durable inbox carries',
    },
  });

  let author = null;
  let watcher = null;
  let watcherCwd = null;
  let seed = null;
  let peerMessage = null;
  let delegated = null;
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('Garden subscription verification requires a named profile.');
  const cli = (args) => execFileSync(appDaemonInTree(options.appPath), args,
    { encoding: 'utf8', env: profileCliEnv(profile) }).trim();
  const unreadSeeds = () => queryDaemonDb(path.join(dataDirForProfile(profile), 'attn.db'),
    `SELECT source_id FROM agent_mailbox_items WHERE recipient_session_id = '${watcher.sessionId}' AND kind = 'garden_seed' AND read_at = '' ORDER BY source_id`,
    { json: true }).map((item) => item.source_id);
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
      watcherCwd = path.join(runner.sessionDir, 'watcher');
      const delegateCwd = path.join(runner.sessionDir, 'delegate');
      writeMockAgentFixture(delegateCwd, { name: 'subscription-delegate', turns: [
        { includes: 'Wait for the subscription proof.', actions: [{ type: 'reply', text: 'DELEGATE_READY', state: 'idle' }] },
      ] });
      writeWatcherFixture(watcherCwd, delegateCwd);
      const sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: watcherCwd,
        label: 'seed-read-watcher',
        agent: 'codex',
        promptReadyFn: ensureCodexPromptReadyViaPty,
        promptReadyTimeoutMs: 90_000,
      });
      const first = await waitForFirstWorkspacePane(client, sessionId, 'watcher pane', 20_000);
      const pane = { sessionId, paneId: first.paneId };
      await submitPrompt(client, pane.sessionId, pane.paneId, `Watch seed ${seed}`);
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
      const transcript = readWatcherTranscript(watcher.sessionId);
      const doorbells = transcriptMessages(transcript)
        .filter((message) => message.role === 'user' && message.text === GENERIC_DOORBELL);
      runner.assert(doorbells.length === 2,
        'each read cycle received exactly one generic doorbell', { doorbells });
      runner.assert(doorbells.every((doorbell) => !doorbell.text.includes(seed)),
        'the durable seed id stayed out of the terminal doorbell', { doorbells, seed });
      const batches = inboxBatches(transcript);
      runner.assert(batches.length === 2, 'each read cycle read the durable inbox once', { batches });
      runner.assert(
        batches.every((batch) => batch.items.length > 0 && batch.items.every((item) =>
          item.kind === 'garden_seed'
          && item.source_id === seed
          && item.content.includes(`${seed} moved: note`)
          && Boolean(item.notified_at)
          && Boolean(item.read_at))),
        'the durable inbox names the seed the doorbell withheld, with its receipts', { batches, seed },
      );
      runner.writeText('watcher-pane.txt', `${text}\n`);
      runner.writeText('watcher-transcript.json', `${JSON.stringify(transcriptMessages(transcript), null, 2)}\n`);
    });

    await runner.step('a_peer_message_rings_the_same_hook_free_lane', async () => {
      const sent = await runInShell(
        client,
        author,
        `attn agent msg ${watcher.sessionId} "${PEER_BODY}" --source-session ${author.sessionId}`,
        '(id',
      );
      peerMessage = peerMessageID(sent);
      runner.assert(Boolean(peerMessage), 'the peer send returned its message id', { sent });
      const text = await waitForAgentReads(client, watcher, 3, PEER_BODY);
      const transcript = readWatcherTranscript(watcher.sessionId);
      const doorbells = transcriptMessages(transcript)
        .filter((message) => message.role === 'user' && message.text === GENERIC_DOORBELL);
      runner.assert(doorbells.length === 3,
        'the peer mailbox rang the same generic doorbell the Garden bell rings', { doorbells });
      runner.assert(doorbells.every((doorbell) => !doorbell.text.includes(PEER_BODY)),
        'the peer body stayed out of the terminal doorbell', { doorbells });
      const batches = inboxBatches(transcript);
      runner.assert(batches.length === 3, 'the peer wake read the durable inbox once', { batches });
      const peerBatch = batches[2];
      runner.assert(peerBatch.items.length === 1 && peerBatch.remaining === 0,
        'the peer wake consumed exactly its new item', { peerBatch });
      const item = peerBatch.items[0];
      runner.assert(item.kind === 'peer_message' && item.source_id === peerMessage && item.content === PEER_BODY,
        'the durable inbox carries the peer body the doorbell withheld', { peerBatch, peerMessage });
      runner.assert(Boolean(item.notified_at) && Boolean(item.read_at),
        'the peer item carries its notified and read receipts', { peerBatch });
      await runInShell(client, author,
        `attn agent msg-status ${peerMessage} --session ${author.sessionId}`, 'read:');
      runner.writeText('peer-send.txt', `${sent}\n`);
      runner.writeText('watcher-after-peer.txt', `${text}\n`);
      runner.writeText('watcher-peer-transcript.json',
        `${JSON.stringify(transcriptMessages(transcript), null, 2)}\n`);
    });

    await runner.step('new_delegation_restores_removed_watch', async () => {
      cli(['seed', 'unwatch', seed, '--session', watcher.sessionId]);
      const known = new Set(observer.sessionsById.keys());
      await submitPrompt(client, watcher.sessionId, watcher.paneId, DISPATCH_PROMPT);
      await observer.waitFor(() => {
        delegated = [...observer.sessionsById.keys()].find(id => !known.has(id)) ?? null;
        return Boolean(delegated);
      }, 'the delegated session exists');
      await waitForAgentReads(client, watcher, 3, DISPATCHED_MARKER);
      const shown = JSON.parse(cli(['seed', 'show', seed, '--session', watcher.sessionId, '--json']));
      runner.assert(shown.watching_via.includes(seed), 'a new delegation restores the ordinary watch', { via: shown.watching_via });
    });

    let keptChild;
    let droppedChild;
    await runner.step('hold_watcher_and_queue_descendant_updates', async () => {
      await submitPrompt(client, watcher.sessionId, watcher.paneId, HOLD_PROMPT);
      await observer.waitFor(() => fs.existsSync(path.join(watcherCwd, HOLD_READY)), 'watcher holding its turn');
      keptChild = JSON.parse(cli(['seed', 'plant', 'Keep child subscription', '--part-of', seed, '--json'])).id;
      droppedChild = JSON.parse(cli(['seed', 'plant', 'Stop inherited subscription', '--part-of', seed, '--json'])).id;
      cli(['seed', 'watch', keptChild, '--session', watcher.sessionId]);
      cli(['seed', 'note', keptChild, '-m', 'Keep this queued child update', '--ring', '--session', author.sessionId]);
      cli(['seed', 'note', droppedChild, '-m', 'Discard this inherited update', '--ring', '--session', author.sessionId]);
      runner.assert(unreadSeeds().length === 2, 'both descendant updates are queued while the watcher works', { unread: unreadSeeds() });
    });

    await runner.step('unwatch_drops_only_uncovered_updates', async () => {
      await client.request('focus_pane', author);
      const output = await runInShell(client, author, `attn seed unwatch ${seed} --session ${watcher.sessionId}`, `removed watch on ${seed}`);
      const unread = unreadSeeds();
      runner.assert(unread.length === 1 && unread[0] === keptChild, 'the separate child watch survives plot unwatch', { unread });
      const inherited = JSON.parse(cli(['seed', 'unwatch', droppedChild, '--session', watcher.sessionId, '--json']));
      runner.assert(!inherited.watching && inherited.watching_via.length === 0, 'the other child has no remaining coverage', { inherited });
      cli(['seed', 'note', droppedChild, '-m', 'This later activity must stay quiet', '--ring', '--session', author.sessionId]);
      runner.assert(unreadSeeds().length === 1, 'later uncovered activity adds no notification', { unread: unreadSeeds() });
      runner.writeText('unwatch-coverage.txt', output);
      fs.writeFileSync(path.join(watcherCwd, HOLD_RELEASE), 'release\n');
      await waitForAgentReads(client, watcher, 3, HOLD_DONE);
      await waitForAgentReads(client, watcher, 4, keptChild);
      const messages = mockTranscript(watcher.sessionId);
      runner.assert(messages.some((message) => saw(message.text, `${keptChild} moved: note`)), 'the surviving child update reaches the actual inbox', { keptChild });
      runner.assert(!messages.some((message) => saw(message.text, `${droppedChild} moved: note`)), 'the removed update never reaches the inbox', { droppedChild });
    });

    await runner.step('rewatch_restores_descendant_delivery', async () => {
      await runInShell(client, author, `attn seed watch ${seed} --session ${watcher.sessionId}`, `watching ${seed} and its descendants`);
      cli(['seed', 'note', droppedChild, '-m', 'Rewatch restores delivery', '--ring', '--session', author.sessionId]);
      await waitForAgentReads(client, watcher, 5, droppedChild);
      runner.assert(mockTranscript(watcher.sessionId).some((message) => saw(message.text, `${droppedChild} moved: note`)), 'rewatch delivers the next descendant update', { droppedChild });
      await runInShell(client, author, `attn seed unwatch ${keptChild} --session ${watcher.sessionId}`, `attn seed unwatch ${seed}`);
      cli(['seed', 'unwatch', seed, '--session', watcher.sessionId]);
      runner.writeText('subscription-transcript.txt', await paneText(client, watcher));
    });

    await runner.step('restart_does_not_restore_dispatch_watches', async () => {
      await client.quitApp();
      await observer.close();
      cli(['daemon', 'stop']);
      await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
      const shown = JSON.parse(cli(['seed', 'show', seed, '--session', watcher.sessionId, '--json']));
      runner.assert(!shown.watching && shown.watching_via.length === 0, 'restart preserves the removed dispatch subscription', { seed, watching: shown.watching, via: shown.watching_via });
      cli(['seed', 'note', droppedChild, '-m', 'Still unwatched after restart', '--ring', '--session', author.sessionId]);
      runner.assert(unreadSeeds().length === 0, 'post-restart activity stays quiet', { unread: unreadSeeds() });
      runner.writeText('watch-after-restart.json', JSON.stringify({ seed, watching: shown.watching, watching_via: shown.watching_via }));
    });

    const summary = await runner.finishSuccess({ seed, watcherSessionId: watcher.sessionId, peerMessage, delegated, keptChild, droppedChild });
    console.log('[RealAppHarness] Garden seed read receipts passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, {
      seed, watcherSessionId: watcher?.sessionId ?? null, peerMessage,
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [delegated, watcher?.sessionId, author?.sessionId]) {
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
