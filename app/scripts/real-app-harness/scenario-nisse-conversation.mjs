#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

// Composer selectors are pane-scoped: a bare [data-testid="conversation-input"]
// resolves to whichever pane is first in the DOM.
const paneOf = (sessionId) => `[data-testid="conversation-pane-${sessionId}"]`;
const INPUT = (sessionId) => `${paneOf(sessionId)} [data-testid="conversation-input"]`;
const SEND = (sessionId) => `${paneOf(sessionId)} [data-testid="conversation-send"]`;

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 60_000, intervalMs = 300) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

function conversationState(client, sessionId) {
  return client.request('conversation_get_state', { sessionId }, { timeoutMs: 20_000 }).catch(() => null);
}

function processTable() {
  const stdout = execFileSync('/bin/ps', ['-eo', 'pid=,ppid=,pgid=,command='], { encoding: 'utf8' });
  return stdout
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const match = /^(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/.exec(line);
      return match
        ? { pid: Number(match[1]), ppid: Number(match[2]), pgid: Number(match[3]), command: match[4] }
        : null;
    })
    .filter(Boolean);
}

function hostProcesses() {
  // By its own path, not the substring `attn-nisse`: a profile named `nisse*`
  // puts that in the bundle path of every sibling process too.
  return processTable().filter((entry) => entry.command.includes('/bin/attn-nisse'));
}

async function sendPrompt(client, sessionId, text) {
  await client.request('dom_type', { selector: INPUT(sessionId), text });
  await client.request('dom_click', { selector: SEND(sessionId) });
}

async function waitForReply(client, sessionId, expected, description) {
  // The run has to close, not just produce text: the composer stays open for
  // the whole run, so the settle signal is the send button leaving Steer.
  return pollFor(
    async () => {
      const state = await conversationState(client, sessionId);
      if (!state || state.sendLabel !== 'Send') return null;
      const reply = state.messages.find(
        (message) => message.role === 'assistant' && message.text.toLowerCase().includes(expected),
      );
      return reply && !reply.streaming ? state : null;
    },
    `${description}: an assistant reply containing "${expected}" with the run settled`,
    120_000,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-nisse-conversation.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the nisse scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'PI-HOST-CONVERSATION',
    tier: 'tier2-local-real-agent',
    prefix: 'nisse-conversation',
    metadata: { agent: 'nisse', focus: 'conversation round trip, second prompt, no orphans on close' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const note = (message, extra) => runner.log(message, extra);
  let sessionId = null;
  let hostPid = null;
  let hostGroup = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { repoDir } = await runner.step('create_repo_fixture', async () => {
      const dir = path.join(runner.sessionDir, 'nisse-repo');
      fs.mkdirSync(dir, { recursive: true });
      execFileSync('git', ['init', '-q'], { cwd: dir });
      execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
        cwd: dir,
        env: {
          ...process.env,
          GIT_AUTHOR_NAME: 'attn',
          GIT_AUTHOR_EMAIL: 'attn@local',
          GIT_COMMITTER_NAME: 'attn',
          GIT_COMMITTER_EMAIL: 'attn@local',
        },
      });
      return { repoDir: dir };
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_conversation_session', async () => {
      const before = hostProcesses().map((entry) => entry.pid);
      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `nisse-${runner.runId.slice(-6)}`,
        agent: 'nisse',
      });
      sessionId = created.sessionId;
      await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
      runner.registerCleanup('close_session', () => client.request('close_session', { sessionId }).catch(() => {}));

      const state = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && current.inputDisabled === false ? current : null;
        },
        'the conversation composer to open (session_ready)',
        90_000,
      );
      const host = hostProcesses().find((entry) => !before.includes(entry.pid));
      if (!host) throw new Error('no attn-nisse process appeared for the session');
      hostPid = host.pid;
      hostGroup = host.pgid;
      note('host is up and the composer is open', { hostPid: host.pid, hostPgid: hostGroup, placeholder: state.placeholder });
    });

    await runner.step('first_prompt_round_trip', async () => {
      await sendPrompt(client, sessionId, 'Reply with exactly one word: alpha');
      const state = await waitForReply(client, sessionId, 'alpha', 'first prompt');
      note('first reply streamed and settled', { messages: state.messages.length });
    });

    await runner.step('second_prompt_after_settle', async () => {
      await sendPrompt(client, sessionId, 'Reply with exactly one word: bravo');
      const state = await waitForReply(client, sessionId, 'bravo', 'second prompt');
      const roles = state.messages.map((message) => message.role);
      if (state.messages.filter((message) => message.role === 'assistant').length < 2) {
        throw new Error(`expected two assistant replies, got roles ${JSON.stringify(roles)}`);
      }
      fs.writeFileSync(
        path.join(runner.runDir, 'conversation.json'),
        `${JSON.stringify(state, null, 2)}\n`,
        'utf8',
      );
      note('second reply streamed and settled', { messages: state.messages.length });
    });

    await runner.step('close_session_leaves_no_orphans', async () => {
      // pi's bash tool blocks for the whole sleep, so a tool subprocess is still
      // live at close, which is what the receipted bug strands.
      await sendPrompt(client, sessionId, 'Run the bash command `sleep 45`, then say done.');
      const toolChildren = await pollFor(
        async () => {
          // pi puts each tool subprocess in its OWN process group, so the
          // host's group never contains them; parentage is what finds them.
          const children = processTable().filter((entry) => entry.ppid === hostPid);
          return children.length > 0 ? children : null;
        },
        `a tool subprocess to appear under host pid ${hostPid}`,
        60_000,
      );

      const groupBefore = processTable().filter((entry) => entry.pgid === hostGroup);
      await client.request('close_session', { sessionId });
      // Matched on pid AND command: a pid the kernel handed to something else
      // in the meantime is not the process we started.
      const stillRunning = () => {
        const live = new Map(processTable().map((entry) => [entry.pid, entry.command]));
        return [
          ...processTable().filter((entry) => entry.pgid === hostGroup),
          ...toolChildren.filter((child) => live.get(child.pid) === child.command),
        ];
      };
      const survivors = await pollFor(
        async () => (stillRunning().length === 0 ? [] : null),
        `the host group ${hostGroup} and its tool subprocesses to exit`,
        30_000,
      ).catch(() => stillRunning());
      fs.writeFileSync(
        path.join(runner.runDir, 'host-process-group.json'),
        `${JSON.stringify({ pgid: hostGroup, hostPid, before: groupBefore, toolChildren, survivors }, null, 2)}\n`,
        'utf8',
      );
      if (survivors.length > 0) {
        throw new Error(`closing the session left ${JSON.stringify(survivors)} behind`);
      }
      note('host and its tool subprocesses are gone after close', {
        pgid: hostGroup,
        hostPid,
        toolChildren: toolChildren.length,
      });
    });

    await runner.finishSuccess({ sessionId, hostPid, hostPgid: hostGroup });
  } catch (error) {
    await runner.finishFailure(error, { sessionId, hostPid, hostPgid: hostGroup });
    throw error;
  } finally {
    // An open socket holds node's event loop open: without this the scenario
    // prints its verdict and never exits.
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
