#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  submitPrompt,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { transcriptMessages, writeMockAgentFixture } from './mockAgent.mjs';
import { delay } from './platform.mjs';
import { ensureCodexPromptReadyViaPty } from './scenarioAgents.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
} from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const GENERIC_DOORBELL = '📬 You have unread items in your attn inbox. Run attn agent inbox to read them.';
const GARDEN_ITEM_COUNT = 9;
const BURST_PEER_BODY = 'BURST_PEER_BODY only the inbox may reveal';
const LATER_PEER_BODY = 'LATER_PEER_BODY proves the missing hook did not latch the lane';
const UNREAD_PEER_BODY = 'UNREAD_PEER_BODY survives until the cooldown reminder';
const RELEASE_FILE = 'release-first-read';
const READ_MARKER = 'AGENT_INBOX_READ';
const UNREAD_READ_MARKER = 'UNREAD_AGENT_INBOX_READ';

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
  const mark = `peermsgmark${++marks}x`;
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

async function waitForRecipient(client, pane, expected, timeoutMs = 60_000) {
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    text = await paneText(client, pane);
    if (flat(text).includes(flat(expected))) return text;
    await delay(250);
  }
  throw new Error(`recipient never reached ${JSON.stringify(expected)}:\n${text}`);
}

async function waitForRecipientReads(client, pane, count, timeoutMs = 60_000) {
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    text = await paneText(client, pane);
    if (occurrences(flat(text), READ_MARKER) >= count) return text;
    await delay(250);
  }
  throw new Error(`recipient completed fewer than ${count} inbox reads:\n${text}`);
}

function timestampedDoorbells(transcript) {
  const doorbells = [];
  for (const line of String(transcript).split('\n')) {
    if (!line.trim()) continue;
    let entry;
    try {
      entry = JSON.parse(line);
    } catch {
      continue;
    }
    const payload = entry?.type === 'response_item' ? entry.payload : entry?.message;
    if (payload?.role !== 'user' || !Array.isArray(payload.content)) continue;
    const text = payload.content.map((block) => block?.text || '').join('');
    if (text === GENERIC_DOORBELL) doorbells.push({ text, timestamp: entry.timestamp });
  }
  return doorbells;
}

async function waitForDoorbells(cwd, count, timeoutMs = 70_000) {
  const deadline = Date.now() + timeoutMs;
  let transcript = '';
  while (Date.now() < deadline) {
    transcript = readRecipientTranscript(cwd);
    const doorbells = timestampedDoorbells(transcript);
    if (doorbells.length >= count) return { transcript, doorbells };
    await delay(250);
  }
  throw new Error(`recipient received fewer than ${count} doorbells:\n${transcript}`);
}

function peerMessageID(output) {
  return output.match(/\(id([0-9a-f-]{36})\)/)?.[1] ?? null;
}

function writeRecipientFixture(cwd) {
  writeMockAgentFixture(cwd, {
    name: 'peer-message-read-receipts',
    minimumWorkingMs: 0,
    turns: [
      {
        includes: 'Wait for peer messages',
        actions: [{ type: 'reply', text: 'PEER_RECIPIENT_READY', state: 'idle' }],
      },
      {
        includes: GENERIC_DOORBELL,
        submitHook: false,
        actions: [
          { type: 'wait_for_file', path: RELEASE_FILE },
          { type: 'attn', args: ['agent', 'inbox', '--limit', '10', '--json'] },
          { type: 'attn', args: ['agent', 'inbox', '--limit', '10', '--json'] },
          { type: 'reply', text: READ_MARKER, state: 'idle' },
        ],
      },
    ],
  });
}

function writeUnreadRecipientFixture(cwd) {
  writeMockAgentFixture(cwd, {
    name: 'peer-message-unread-reminder',
    minimumWorkingMs: 0,
    turns: [
      {
        includes: 'Wait for a peer message without reading it',
        actions: [{ type: 'reply', text: 'UNREAD_RECIPIENT_READY', state: 'idle' }],
      },
      {
        includes: GENERIC_DOORBELL,
        submitHook: false,
        actions: [{ type: 'reply', text: 'DOORBELL_LEFT_UNREAD', state: 'idle' }],
      },
      {
        includes: 'Read the waiting inbox item',
        actions: [
          { type: 'attn', args: ['agent', 'inbox', '--limit', '10', '--json'] },
          { type: 'reply', text: UNREAD_READ_MARKER, state: 'idle' },
        ],
      },
    ],
  });
}

function readRecipientTranscript(cwd) {
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

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-peer-message-read-receipts.mjs');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'PeerMessageReadReceipts',
    tier: 'local',
    prefix: 'peer-message-read-receipts',
    metadata: { agent: 'mock-codex', receipt: 'agent inbox without prompt-submit hooks and unread reminder' },
  });

  let author = null;
  let recipient = null;
  let recipientCwd = null;
  let unreadRecipient = null;
  let unreadRecipientCwd = null;
  const seedIDs = [];
  let burstPeerID = null;
  let laterPeerID = null;
  let unreadPeerID = null;
  let reminderIntervalMs = null;
  try {
    await launchFreshAppAndConnect(client, observer);

    author = await runner.step('open_sender_shell', async () => {
      const cwd = path.join(runner.sessionDir, 'author');
      fs.mkdirSync(cwd, { recursive: true });
      const sessionId = await createSessionAndWaitForInitialPane({
        client, observer, cwd, label: 'peer-message-author', agent: 'shell',
      });
      const first = await waitForFirstWorkspacePane(client, sessionId, 'author pane', 20_000);
      const pane = { sessionId, paneId: first.paneId };
      await waitForPaneShellReady(client, sessionId, first.paneId, {
        timeoutMs: 20_000, description: 'peer message author shell ready',
      });
      return pane;
    });

    recipient = await runner.step('open_idle_hook_recipient', async () => {
      recipientCwd = path.join(runner.sessionDir, 'recipient');
      fs.mkdirSync(recipientCwd, { recursive: true });
      writeRecipientFixture(recipientCwd);
      const sessionId = await createSessionAndWaitForInitialPane({
        client, observer, cwd: recipientCwd, label: 'peer-message-recipient', agent: 'codex',
        promptReadyFn: ensureCodexPromptReadyViaPty, promptReadyTimeoutMs: 90_000,
      });
      const first = await waitForFirstWorkspacePane(client, sessionId, 'recipient pane', 20_000);
      const pane = { sessionId, paneId: first.paneId };
      await submitPrompt(client, pane.sessionId, pane.paneId, 'Wait for peer messages');
      await waitForRecipient(client, pane, 'PEER_RECIPIENT_READY');
      return pane;
    });

    await runner.step('plant_and_watch_nine_seeds', async () => {
      for (let index = 0; index < GARDEN_ITEM_COUNT; index += 1) {
        const planted = await runInShell(
          client,
          author,
          `attn seed plant "Inbox burst seed ${index + 1}" -m "Durable inbox burst fixture ${index + 1}." --session ${author.sessionId}`,
          's-',
        );
        const seedID = planted.match(/s-[a-z0-9]{6}/)?.[0] ?? null;
        runner.assert(Boolean(seedID), `plant returned seed ${index + 1}`, { planted });
        seedIDs.push(seedID);
        await runInShell(
          client,
          author,
          `attn seed watch ${seedID} --session ${recipient.sessionId}`,
          `watching ${seedID}`,
        );
      }
    });

    await runner.step('burst_gets_one_generic_doorbell', async () => {
      await runInShell(
        client,
        author,
        `attn seed note ${seedIDs[0]} -m BURST_GARDEN_1 --ring --session ${author.sessionId}`,
        `noted on ${seedIDs[0]}`,
      );
      await waitForRecipient(client, recipient, GENERIC_DOORBELL);

      for (let index = 1; index < seedIDs.length; index += 1) {
        await runInShell(
          client,
          author,
          `attn seed note ${seedIDs[index]} -m BURST_GARDEN_${index + 1} --ring --session ${author.sessionId}`,
          `noted on ${seedIDs[index]}`,
        );
      }
      const peer = await runInShell(
        client,
        author,
        `attn agent msg ${recipient.sessionId} "${BURST_PEER_BODY}" --source-session ${author.sessionId}`,
        'queued:',
      );
      burstPeerID = peerMessageID(peer);
      runner.assert(Boolean(burstPeerID), 'the burst peer send returned its message id', { peer });
      await runInShell(client, author,
        `attn agent msg-status ${burstPeerID} --session ${author.sessionId}`, 'queued:');

      const transcript = readRecipientTranscript(recipientCwd);
      const doorbells = transcriptMessages(transcript)
        .filter((message) => message.role === 'user' && message.text === GENERIC_DOORBELL);
      runner.assert(doorbells.length === 1,
        'nine Garden items and one peer item share one generic doorbell', { doorbells });
      runner.assert(!doorbells[0].text.includes(BURST_PEER_BODY),
        'the generic doorbell contains no durable item body', { doorbells });
      runner.writeText('burst-send-result.txt', `${peer}\n`);
    });

    await runner.step('bounded_batch_reads_fifo_and_writes_each_receipt', async () => {
      fs.writeFileSync(path.join(recipientCwd, RELEASE_FILE), 'read\n');
      const text = await waitForRecipientReads(client, recipient, 1);
      const transcript = readRecipientTranscript(recipientCwd);
      const batches = inboxBatches(transcript);
      runner.assert(batches.length === 2, 'the turn produced one batch and one empty receipt check', { batches });
      const [batch, empty] = batches;
      runner.assert(batch.items.length === GARDEN_ITEM_COUNT + 1 && batch.remaining === 0,
        'the bounded read returned all ten burst items', { batch });
      runner.assert(empty.items.length === 0 && empty.remaining === 0,
        'a second read finds no duplicated durable content', { empty });
      const sources = batch.items.map((item) => item.source_id);
      runner.assert(JSON.stringify(sources) === JSON.stringify([...seedIDs, burstPeerID]),
        'the batch preserved FIFO order across Garden and peer producers', { sources, seedIDs, burstPeerID });
      runner.assert(batch.items.slice(0, GARDEN_ITEM_COUNT).every((item) =>
        item.kind === 'garden_seed' && item.content.includes(`${item.source_id} moved: note`)),
      'each Garden item retained its durable action content', { batch });
      runner.assert(batch.items.at(-1)?.kind === 'peer_message' && batch.items.at(-1)?.content === BURST_PEER_BODY,
        'the peer body appeared once at the end of the FIFO batch', { batch });
      runner.assert(batch.items.every((item) => item.notified_at && item.read_at),
        'each returned item carries its notified and read receipt', { batch });
      await runInShell(client, author,
        `attn agent msg-status ${burstPeerID} --session ${author.sessionId}`, 'read:');
      await observer.waitFor(
        () => ['idle', 'waiting_input'].includes(observer.getSession(recipient.sessionId)?.state),
        'the recipient to return idle after its missing-hook inbox turn',
        30_000,
      );
      runner.writeText('recipient-pane.txt', `${text}\n`);
    });

    await runner.step('later_item_wakes_after_missing_prompt_submit', async () => {
      const peer = await runInShell(
        client,
        author,
        `attn agent msg ${recipient.sessionId} "${LATER_PEER_BODY}" --source-session ${author.sessionId}`,
        '(id',
      );
      laterPeerID = peerMessageID(peer);
      runner.assert(Boolean(laterPeerID), 'the later peer send returned its message id', { peer });
      const text = await waitForRecipientReads(client, recipient, 2);
      const transcript = readRecipientTranscript(recipientCwd);
      const doorbells = transcriptMessages(transcript)
        .filter((message) => message.role === 'user' && message.text === GENERIC_DOORBELL);
      runner.assert(doorbells.length === 2,
        'the burst and later item each produced one generic doorbell', { doorbells });
      for (const doorbell of doorbells) {
        runner.assert(!doorbell.text.includes(BURST_PEER_BODY) && !doorbell.text.includes(LATER_PEER_BODY),
          'a generic doorbell contains no durable item body', { doorbell });
      }
      const batches = inboxBatches(transcript);
      runner.assert(batches.length === 4, 'both wake turns completed their batch and empty reads', { batches });
      const later = batches[2];
      runner.assert(later.items.length === 1 && later.remaining === 0,
        'the later wake consumed exactly its new item', { later });
      runner.assert(later.items[0].source_id === laterPeerID && later.items[0].content === LATER_PEER_BODY,
        'the later batch returned the new peer content without duplication', { later, laterPeerID });
      runner.assert(Boolean(later.items[0].read_at), 'the later item got its read receipt', { later });
      await runInShell(client, author,
        `attn agent msg-status ${laterPeerID} --session ${author.sessionId}`, 'read:');
      runner.writeText('recipient-after-later-wake.txt', `${text}\n`);
      runner.writeText('recipient-transcript.json', `${JSON.stringify(transcriptMessages(transcript), null, 2)}\n`);
    });

    unreadRecipient = await runner.step('open_recipient_that_leaves_the_first_doorbell_unread', async () => {
      unreadRecipientCwd = path.join(runner.sessionDir, 'unread-recipient');
      fs.mkdirSync(unreadRecipientCwd, { recursive: true });
      writeUnreadRecipientFixture(unreadRecipientCwd);
      const sessionId = await createSessionAndWaitForInitialPane({
        client, observer, cwd: unreadRecipientCwd, label: 'peer-message-unread-recipient', agent: 'codex',
        promptReadyFn: ensureCodexPromptReadyViaPty, promptReadyTimeoutMs: 90_000,
      });
      const first = await waitForFirstWorkspacePane(client, sessionId, 'unread recipient pane', 20_000);
      const pane = { sessionId, paneId: first.paneId };
      await submitPrompt(client, pane.sessionId, pane.paneId, 'Wait for a peer message without reading it');
      await waitForRecipient(client, pane, 'UNREAD_RECIPIENT_READY');
      return pane;
    });

    await runner.step('unread_item_gets_one_doorbell_per_cooldown', async () => {
      const peer = await runInShell(
        client,
        author,
        `attn agent msg ${unreadRecipient.sessionId} "${UNREAD_PEER_BODY}" --source-session ${author.sessionId}`,
        '(id',
      );
      unreadPeerID = peerMessageID(peer);
      runner.assert(Boolean(unreadPeerID), 'the unread peer send returned its message id', { peer });

      const observed = await waitForDoorbells(unreadRecipientCwd, 2);
      runner.assert(observed.doorbells.length === 2,
        'the unread item produced one initial doorbell and one reminder', { doorbells: observed.doorbells });
      const firstAt = Date.parse(observed.doorbells[0].timestamp);
      const secondAt = Date.parse(observed.doorbells[1].timestamp);
      reminderIntervalMs = secondAt - firstAt;
      runner.assert(Number.isFinite(reminderIntervalMs) && reminderIntervalMs >= 29_000,
        'the reminder respected the 30 second cooldown', { reminderIntervalMs, doorbells: observed.doorbells });
      runner.assert(observed.doorbells.every((doorbell) => !doorbell.text.includes(UNREAD_PEER_BODY)),
        'the unread reminder contains no durable item body', { doorbells: observed.doorbells });
      await runInShell(client, author,
        `attn agent msg-status ${unreadPeerID} --session ${author.sessionId}`, 'notified:');

      await submitPrompt(
        client,
        unreadRecipient.sessionId,
        unreadRecipient.paneId,
        'Read the waiting inbox item',
      );
      await waitForRecipient(client, unreadRecipient, UNREAD_READ_MARKER);
      const transcript = readRecipientTranscript(unreadRecipientCwd);
      const doorbells = timestampedDoorbells(transcript);
      runner.assert(doorbells.length === 2, 'reading the inbox did not create another doorbell', { doorbells });
      const batches = inboxBatches(transcript);
      runner.assert(batches.length === 1, 'the unread recipient read its inbox exactly once', { batches });
      const [batch] = batches;
      runner.assert(batch.items.length === 1 && batch.remaining === 0,
        'the unread recipient consumed its one durable item', { batch });
      runner.assert(batch.items[0]?.source_id === unreadPeerID && batch.items[0]?.content === UNREAD_PEER_BODY,
        'the cooldown reminder preserved the peer message body', { batch, unreadPeerID });
      runner.assert(Boolean(batch.items[0]?.notified_at) && Boolean(batch.items[0]?.read_at),
        'the reminded item carries notified and read receipts', { batch });
      await runInShell(client, author,
        `attn agent msg-status ${unreadPeerID} --session ${author.sessionId}`, 'read:');
      runner.writeText('unread-reminder-transcript.json', `${JSON.stringify(transcriptMessages(transcript), null, 2)}\n`);
      runner.writeText('unread-reminder-interval.txt', `${reminderIntervalMs}ms\n`);
    });

    const summary = await runner.finishSuccess({
      authorSessionId: author.sessionId,
      recipientSessionId: recipient.sessionId,
      unreadRecipientSessionId: unreadRecipient.sessionId,
      seedIDs,
      burstPeerID,
      laterPeerID,
      unreadPeerID,
      reminderIntervalMs,
    });
    console.log('[RealAppHarness] Peer message read receipts passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, {
      authorSessionId: author?.sessionId ?? null,
      recipientSessionId: recipient?.sessionId ?? null,
      unreadRecipientSessionId: unreadRecipient?.sessionId ?? null,
      seedIDs,
      burstPeerID,
      laterPeerID,
      unreadPeerID,
      reminderIntervalMs,
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [unreadRecipient?.sessionId, recipient?.sessionId, author?.sessionId]) {
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
