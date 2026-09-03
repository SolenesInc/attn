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

const FIRST_BODY = 'FIRST_PEER_BODY only the inbox may reveal';
const SECOND_BODY = 'SECOND_PEER_BODY follows the first read';
const RELEASE_FILE = 'release-first-read';
const READ_MARKER = 'PEER_MESSAGE_READ';

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

function messageID(output) {
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
        includes: '📨 session',
        submitHook: false,
        actions: [
          { type: 'capture', from: 'prompt', pattern: 'message ([0-9a-f-]{36})', name: 'message' },
          { type: 'wait_for_file', path: RELEASE_FILE },
          { type: 'attn', args: ['agent', 'inbox', '{{message}}'] },
          { type: 'reply', text: READ_MARKER, state: 'idle' },
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
    metadata: { agent: 'mock-codex', receipt: 'agent inbox without prompt-submit hooks' },
  });

  let author = null;
  let recipient = null;
  let recipientCwd = null;
  let firstID = null;
  let secondID = null;
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

    await runner.step('only_the_first_doorbell_is_notified', async () => {
      const first = await runInShell(
        client, author,
        `attn agent msg ${recipient.sessionId} "${FIRST_BODY}" --source-session ${author.sessionId}`,
        'notified:',
      );
      firstID = messageID(first);
      runner.assert(Boolean(firstID), 'the first send returned its message id', { first });

      const second = await runInShell(
        client, author,
        `attn agent msg ${recipient.sessionId} "${SECOND_BODY}" --source-session ${author.sessionId}`,
        'queued:',
      );
      secondID = messageID(second);
      runner.assert(Boolean(secondID), 'the second queued send returned its message id', { second });
      await runInShell(client, author,
        `attn agent msg-status ${firstID} --session ${author.sessionId}`, 'notified:');
      await runInShell(client, author,
        `attn agent msg-status ${secondID} --session ${author.sessionId}`, 'queued:');
      runner.writeText('send-results.txt', `${first}\n${second}\n`);
    });

    await runner.step('inbox_reads_rearm_fifo_without_prompt_submit', async () => {
      fs.writeFileSync(path.join(recipientCwd, RELEASE_FILE), 'read\n');
      const text = await waitForRecipientReads(client, recipient, 2);
      runner.assert(flat(text).includes(flat(FIRST_BODY)), 'the first inbox read returned its body', { text });
      runner.assert(flat(text).includes(flat(SECOND_BODY)), 'the second inbox read returned its body', { text });
      await runInShell(client, author,
        `attn agent msg-status ${firstID} --session ${author.sessionId}`, 'read:');
      await runInShell(client, author,
        `attn agent msg-status ${secondID} --session ${author.sessionId}`, 'read:');
      runner.writeText('recipient-pane.txt', `${text}\n`);
    });

    await runner.step('transcript_proves_content_free_doorbells', async () => {
      const transcript = readRecipientTranscript(recipientCwd);
      const doorbells = transcriptMessages(transcript)
        .filter((message) => message.role === 'user' && message.text.includes('📨 session'));
      runner.assert(doorbells.length === 2, 'the recipient got exactly two peer doorbells', { doorbells });
      runner.assert(doorbells[0].text.includes(firstID) && doorbells[1].text.includes(secondID),
        'the doorbells followed FIFO order', { doorbells, firstID, secondID });
      for (const doorbell of doorbells) {
        runner.assert(!doorbell.text.includes(FIRST_BODY) && !doorbell.text.includes(SECOND_BODY),
          'a doorbell contains no peer message body', { doorbell });
      }
      runner.writeText('recipient-transcript.json', `${JSON.stringify(transcriptMessages(transcript), null, 2)}\n`);
    });

    const summary = await runner.finishSuccess({
      authorSessionId: author.sessionId,
      recipientSessionId: recipient.sessionId,
      firstID,
      secondID,
    });
    console.log('[RealAppHarness] Peer message read receipts passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, {
      authorSessionId: author?.sessionId ?? null,
      recipientSessionId: recipient?.sessionId ?? null,
      firstID,
      secondID,
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [recipient?.sessionId, author?.sessionId]) {
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
