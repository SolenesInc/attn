#!/usr/bin/env node

import path from 'node:path';
import fs from 'node:fs';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  submitPrompt,
} from './common.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
} from './scenarioAssertions.mjs';
import { ensureCodexPromptReadyViaPty } from './scenarioAgents.mjs';
import { delay } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { recordingEnabled } from './windowRecording.mjs';
import { transcriptMessages, writeMockAgentFixture } from './mockAgent.mjs';

const RINGING_NOTE = 'RING7';
const BRIEF = `Live proof only. Read your assigned seed id from this prompt, run attn seed note SEED_ID -m ${RINGING_NOTE} --ring, then wait. Do not harvest until you are told.`;
const HARVEST_REASON = 'the live doorbell proof is done';
const RING_RELEASE_FILE = 'ring-now';
const HARVEST_HOLD_FILE = 'harvest-hold';
const HARVEST_RELEASE_FILE = 'harvest-now';
const DISPATCH_PROMPT = 'Delegate the seed bell proof';
const DISPATCHED_MARKER = 'SEED_BELL_DISPATCHED';
const READ_MARKER = 'SEED_BELL_INBOX_READ';
const PACE_MS = recordingEnabled() ? 1_400 : 0;
const GENERIC_DOORBELL = '📬 You have unread items in your attn inbox. Run attn agent inbox to read them.';

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

function occurrences(haystack, needle) {
  return squash(haystack).split(squash(needle)).length - 1;
}

let marks = 0;

async function paneText(client, pane) {
  const payload = await client.request('read_pane_text', pane);
  return payload.text || '';
}

async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const mark = `mark${++marks}x`;
  await client.request('write_pane', { ...pane, text: `${command}; echo ${mark}` });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    const raw = await paneText(client, pane);
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

async function waitForPane(client, pane, expected, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    text = flat(await paneText(client, pane));
    if (saw(text, expected)) return text;
    await delay(250);
  }
  throw new Error(`pane never received ${JSON.stringify(expected)}:\n${text}`);
}

async function waitForPaneOccurrences(client, pane, expected, count, timeoutMs = 60_000) {
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    text = flat(await paneText(client, pane));
    if (occurrences(text, expected) >= count) return text;
    await delay(250);
  }
  throw new Error(`pane received fewer than ${count} copies of ${JSON.stringify(expected)}:\n${text}`);
}

// The dispatcher delegates from its own pane, so the delegate needs a checkout of
// its own: one fixture file per agent, one transcript per directory.
function writeDispatcherFixture(cwd, delegateCwd) {
  writeMockAgentFixture(cwd, {
    name: 'seed-bell-dispatcher',
    minimumWorkingMs: 0,
    turns: [
      {
        includes: DISPATCH_PROMPT,
        actions: [
          {
            type: 'attn',
            args: [
              'delegate', '--agent', 'claude', '--model', 'claude-haiku-4-5',
              '--yolo', '--no-worktree', '--cwd', delegateCwd,
              '--name', 'seed-bell', '--brief', BRIEF,
            ],
          },
          { type: 'reply', text: DISPATCHED_MARKER, state: 'idle' },
        ],
      },
      {
        includes: GENERIC_DOORBELL,
        actions: [
          { type: 'attn', args: ['agent', 'inbox'] },
          { type: 'reply', text: READ_MARKER, state: 'idle' },
        ],
      },
    ],
  });
}

function writeDelegateFixture(cwd) {
  writeMockAgentFixture(cwd, {
    name: 'seed-bell',
    turns: [
      {
        includes: 'Read your assigned seed id from this prompt',
        actions: [
          { type: 'capture', from: 'prompt', pattern: '(s-[a-z0-9]{6})', name: 'seed' },
          { type: 'wait_for_file', path: RING_RELEASE_FILE },
          { type: 'attn', args: ['seed', 'note', '{{seed}}', '-m', RINGING_NOTE, '--ring'] },
          { type: 'touch', path: HARVEST_HOLD_FILE },
          { type: 'wait_for_file', path: HARVEST_RELEASE_FILE },
        ],
      },
      {
        includes: GENERIC_DOORBELL,
        actions: [
          { type: 'attn', args: ['agent', 'inbox'] },
          { type: 'attn', args: ['seed', 'harvest', '{{seed}}', '-m', HARVEST_REASON], state: 'idle' },
        ],
      },
    ],
  });
}

function mockTranscript(cwd) {
  const dir = path.join(cwd, '.attn-mock-agent');
  const file = fs.readdirSync(dir).find((entry) => entry.endsWith('.jsonl'));
  if (!file) throw new Error(`no mock transcript in ${dir}`);
  return transcriptMessages(fs.readFileSync(path.join(dir, file), 'utf8'));
}

function doorbellsIn(messages) {
  return messages.filter((message) => message.role === 'user' && message.text.includes(GENERIC_DOORBELL));
}

async function openDispatcher(client, observer, cwd) {
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label: 'seed-bell-dispatcher',
    agent: 'codex',
    promptReadyFn: ensureCodexPromptReadyViaPty,
    promptReadyTimeoutMs: 90_000,
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, 'dispatcher pane', 20_000);
  return { sessionId, paneId: pane.paneId };
}

async function openOperator(client, observer, cwd) {
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client, observer, cwd, label: 'seed-bell-operator', agent: 'shell',
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, 'operator pane', 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId, {
    timeoutMs: 20_000,
    description: 'operator shell ready',
  });
  return { sessionId, paneId: pane.paneId };
}

function seedIDs(text) {
  return [...flat(text).matchAll(/(s-[a-z0-9]{6})/g)].map((match) => match[1]);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-seed-nudges');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenSeedNudges',
    tier: 'local',
    prefix: 'garden-seed-nudges',
    metadata: { dispatcher: 'mock-codex', delegate: 'mock-claude' },
  });

  let dispatcher = null;
  let dispatcherCwd = null;
  let delegateCwd = null;
  let operator = null;
  let delegated = null;
  let seed = null;
  let harvestMessage = null;
  try {
    await launchFreshAppAndConnect(client, observer);

    dispatcher = await runner.step('open_dispatcher', async () => {
      dispatcherCwd = path.join(runner.sessionDir, 'dispatcher');
      delegateCwd = path.join(runner.sessionDir, 'delegate');
      writeDispatcherFixture(dispatcherCwd, delegateCwd);
      writeDelegateFixture(delegateCwd);
      return openDispatcher(client, observer, dispatcherCwd);
    });

    operator = await runner.step('open_operator', () =>
      openOperator(client, observer, path.join(runner.sessionDir, 'operator')));

    delegated = await runner.step('dispatch_delegate', async () => {
      const known = new Set(observer.sessionsById.keys());
      await submitPrompt(client, dispatcher.sessionId, dispatcher.paneId, DISPATCH_PROMPT);
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the delegated session exists', 60_000);
      await waitForPane(client, dispatcher, DISPATCHED_MARKER, 60_000);
      return spawned;
    });

    seed = await runner.step('resolve_delegated_seed', async () => {
      const listed = await runInPane(client, operator, 'attn seed ls', delegated);
      for (const candidate of new Set(seedIDs(listed))) {
        const shown = await runInPane(client, operator, `attn seed show ${candidate}`, '');
        if (saw(shown, delegated) && saw(shown, BRIEF)) return candidate;
      }
      throw new Error(`no seed belonged to delegate ${delegated}:\n${listed}`);
    });

    await runner.step('ring_note_reaches_dispatcher', async () => {
      fs.writeFileSync(path.join(delegateCwd, RING_RELEASE_FILE), 'ring\n');
      await waitForPaneOccurrences(client, dispatcher, READ_MARKER, 1, 60_000);
      const messages = mockTranscript(dispatcherCwd);
      const doorbells = doorbellsIn(messages);
      runner.assert(doorbells.length === 1, 'the ringing note produced one doorbell', { doorbells });
      runner.assert(doorbells[0].text === GENERIC_DOORBELL, 'the doorbell arrived generic and alone', { doorbells });
      runner.assert(!saw(doorbells[0].text, RINGING_NOTE), 'the doorbell carries no note content', { doorbells });
      runner.assert(!saw(doorbells[0].text, seed), 'the generic doorbell carries no seed id', { doorbells, seed });
      runner.assert(messages.some((message) => saw(message.text, `${seed} moved: note`)),
        'the durable inbox names the seed the doorbell withheld', { messages });
      runner.writeText('note-doorbell.txt', `${await paneText(client, dispatcher)}\n`);
      if (PACE_MS > 0) await delay(PACE_MS);
    });

    await runner.step('read_resets_then_harvest_rings', async () => {
      await runInPane(client, operator, `attn seed show ${seed}`, RINGING_NOTE);
      await observer.waitFor(
        () => fs.existsSync(path.join(delegateCwd, HARVEST_HOLD_FILE)),
        'the delegated agent holding its working turn',
        20_000,
      );
      const queued = await runInPane(client, operator,
        `attn agent msg ${seed} "Now harvest your assigned seed with reason: ${HARVEST_REASON}" ` +
          `--source-session ${operator.sessionId}`,
        'queued: queued (target is not taking input right now');
      harvestMessage = queued.match(/\(id ([0-9a-f-]{36})\)/)?.[1] ?? null;
      runner.assert(Boolean(harvestMessage), 'the queued harvest request returned its message id', { queued });
      fs.writeFileSync(path.join(delegateCwd, HARVEST_RELEASE_FILE), 'harvest\n');
      await waitForPaneOccurrences(client, dispatcher, READ_MARKER, 2, 60_000);
      const messages = mockTranscript(dispatcherCwd);
      const doorbells = doorbellsIn(messages);
      runner.assert(doorbells.length === 2,
        'the note and harvest each produced one generic doorbell', { doorbells });
      runner.assert(doorbells.every((doorbell) => doorbell.text === GENERIC_DOORBELL),
        'the harvest rang as generically as the note', { doorbells });
      runner.assert(messages.some((message) => saw(message.text, `${seed} moved: note`)),
        'the first inbox read stands in the transcript', { messages });
      runner.assert(messages.some((message) => saw(message.text, `${seed} moved: harvested`)),
        'the harvest rings after the read reset', { messages });
      await runInPane(client, operator, `attn seed show ${seed}`, 'status harvested');
      await runInPane(client, operator,
        `attn agent msg-status ${harvestMessage} --session ${operator.sessionId}`,
        `read: message ${harvestMessage}`);
      runner.writeText('both-doorbells.txt', `${await paneText(client, dispatcher)}\n`);
      runner.writeText('dispatcher-transcript.json', `${JSON.stringify(messages, null, 2)}\n`);
      if (PACE_MS > 0) await delay(PACE_MS);
    });

    const summary = await runner.finishSuccess({ seed, delegated, harvestMessage });
    console.log('[RealAppHarness] Garden seed nudges passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { seed, delegated, harvestMessage });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [delegated, operator?.sessionId, dispatcher?.sessionId]) {
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
