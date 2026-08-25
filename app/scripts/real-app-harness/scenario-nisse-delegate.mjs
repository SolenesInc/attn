#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, dataDirForProfile, socketPathForProfile } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const GUIDANCE_TOKEN = 'quicksilver-badger';

// The state an agent reports and the column the board keeps it in differ.
const TICKET_COLUMN_FOR_READY = 'in_review';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const pane = (sessionId) => `[data-testid="conversation-pane-${sessionId}"]`;
const reloadOf = (sessionId) => `${pane(sessionId)} [data-testid="conversation-reload"]`;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 60_000, intervalMs = 400) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

function resolveAttnBin() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(HARNESS_DIR, '../../../attn')].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error('attn binary not found (build ./attn or set ATTN_HARNESS_BIN)');
}

// A JSON command prints an object or a bare array, sometimes after a line of
// human progress text on the same stream.
function parseAttnJSON(stdout) {
  const candidates = [stdout.indexOf('{'), stdout.indexOf('[')].filter((index) => index >= 0);
  if (candidates.length === 0) return null;
  try {
    return JSON.parse(stdout.slice(Math.min(...candidates)));
  } catch {
    return null;
  }
}

function makeAttnRunner(attnBin, profile) {
  const socketPath = socketPathForProfile(profile);
  return function runAttn(args, { allowFailure = false } = {}) {
    try {
      const stdout = execFileSync(attnBin, args, {
        encoding: 'utf8',
        env: { ...process.env, ATTN_PROFILE: profile, ATTN_SOCKET_PATH: socketPath },
      });
      return { stdout, status: 0, stderr: '', json: parseAttnJSON(stdout) };
    } catch (error) {
      if (!allowFailure) throw error;
      const stdout = typeof error.stdout === 'string' ? error.stdout : '';
      const stderr = typeof error.stderr === 'string' ? error.stderr : '';
      return { status: error.status ?? 1, stdout, stderr, json: parseAttnJSON(stdout) };
    }
  };
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
      return match ? { pid: Number(match[1]), ppid: Number(match[2]), pgid: Number(match[3]), command: match[4] } : null;
    })
    .filter(Boolean);
}

// Matched by path, not the substring `attn-nisse`: a profile named `nisse*` puts
// that in the bundle path of every sibling process too.
const hostProcesses = () => processTable().filter((entry) => entry.command.includes('/bin/attn-nisse'));

function waitForHost(known, description, timeoutMs = 90_000) {
  return pollFor(async () => hostProcesses().find((entry) => !known.includes(entry.pid)) ?? null, description, timeoutMs);
}

// Steps 1 and 2 must be demanded as SEPARATE bash calls: told only "in order", a
// model batches both and composes the comment unread (observed 2026-08-09).
function briefFor(token) {
  return [
    'Do these three things, in order, using your bash tool. Do not ask for',
    'confirmation and do nothing else. Steps 1 and 2 are two separate bash',
    'calls: you cannot know what to write in step 2 until step 1 has answered.',
    '',
    '1. Read AGENTS.md in the current directory and note the codename it records.',
    '2. In a second, separate bash call, run this command, with <codename>',
    '   replaced by what you read in step 1:',
    `     attn ticket status ready_for_review --comment "${token} codename=<codename>"`,
    '3. Reply with the single word: done',
  ].join('\n');
}

function ticketFor(runAttn, workerId) {
  return pollFor(
    async () => {
      const list = runAttn(['ticket', 'list', '--json'], { allowFailure: true });
      const tickets = Array.isArray(list.json) ? list.json : [];
      return tickets.find((ticket) => ticket.assignee === workerId) ?? null;
    },
    `a ticket bound to worker ${workerId}`,
    45_000,
  );
}

const activityOf = (ticket) => ticket?.activity ?? [];
const textOf = (entry) => entry?.comment ?? '';

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-nisse-delegate.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the nisse delegation scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const dataDir = dataDirForProfile(profile);
  const runAttn = makeAttnRunner(resolveAttnBin(), profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'PI-HOST-DELEGATE',
    tier: 'tier2-local-real-agent',
    prefix: 'nisse-delegate',
    metadata: {
      agent: 'nisse',
      focus: 'delegation to a conversation agent: brief as the first message, ticket report attributed to the delegated session, repository guidance in the worktree, brief replayed after a zero-file crash',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let delegatorId = null;
  let workerId = null;
  let earlyWorkerId = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { repoDir } = await runner.step('seed_repo_fixture', async () => {
      const dir = path.join(runner.sessionDir, 'nisse-delegate-repo');
      fs.mkdirSync(dir, { recursive: true });
      const gitEnv = {
        ...process.env,
        GIT_AUTHOR_NAME: 'attn',
        GIT_AUTHOR_EMAIL: 'attn@local',
        GIT_COMMITTER_NAME: 'attn',
        GIT_COMMITTER_EMAIL: 'attn@local',
      };
      execFileSync('git', ['init', '-q'], { cwd: dir });
      fs.writeFileSync(
        path.join(dir, 'AGENTS.md'),
        `# Repository guide\n\nThis repository's codename is ${GUIDANCE_TOKEN}. Always report it when asked.\n`,
        'utf8',
      );
      execFileSync('git', ['add', '-A'], { cwd: dir, env: gitEnv });
      execFileSync('git', ['commit', '-q', '-m', 'init'], { cwd: dir, env: gitEnv });
      return { repoDir: dir };
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('boot_delegator', async () => {
      delegatorId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: repoDir,
        label: `deleg-${runner.runId.slice(-6)}`,
        agent: 'shell',
        sessionWaitMs: 30_000,
        waitForInitialPaneVisible: false,
      });
    });

    const { ticketId, token } = await runner.step('delegate_a_conversation_agent', async () => {
      const before = hostProcesses().map((entry) => entry.pid);
      const runToken = `PIDELEGATE-${runner.runId.slice(-6).toUpperCase()}`;
      const delegate = runAttn([
        'delegate',
        '--source-session', delegatorId,
        '--agent', 'nisse',
        '--brief', briefFor(runToken),
        '--name', `pid-${runner.runId.slice(-6)}`,
      ]);
      workerId = delegate.json?.session_id;
      runner.assert(typeof workerId === 'string' && workerId.length > 0, `delegate returned a worker session id (got ${JSON.stringify(delegate.json)})`, delegate.json);
      await observer.waitForSession({ id: workerId, timeoutMs: 45_000 });
      const host = await waitForHost(before, 'the host process for the delegated conversation');

      const ticket = await ticketFor(runAttn, workerId);
      runner.assert(
        (ticket.description ?? '').includes(runToken),
        'the delegated ticket carries the brief as its description',
        ticket,
      );
      const worktree = delegate.json?.directory;
      runner.assert(
        delegate.json?.worktree_created === true && worktree !== repoDir,
        `the delegated agent got its own worktree (got ${worktree}, worktree_created=${delegate.json?.worktree_created})`,
        delegate.json,
      );
      runner.log(`[RealAppHarness] worker=${workerId} ticket=${ticket.id} host=${host.pid} cwd=${worktree}`);
      return { ticketId: ticket.id, token: runToken, worktree };
    });

    await runner.step('the_brief_is_the_conversations_first_message', async () => {
      await client.request('select_session', { sessionId: workerId });
      const state = await pollFor(
        async () => {
          const current = await conversationState(client, workerId);
          return current && (current.messages || []).length > 0 ? current : null;
        },
        'the delegated conversation to draw its first message',
        90_000,
      );
      const first = state.messages[0];
      runner.assert(first.role === 'user', `the conversation opens with a user message (got ${first.role})`, state.messages);
      runner.assert(first.text.includes(token), 'the first message is the delegation brief', first);
      runner.writeJson('delegated-conversation.json', state);
      return first;
    });

    await runner.step('the_agent_reports_onto_its_own_ticket', async () => {
      const settled = await pollFor(
        async () => {
          const show = runAttn(['ticket', 'show', ticketId, '--json'], { allowFailure: true });
          return show.json?.status === TICKET_COLUMN_FOR_READY ? show.json : null;
        },
        `the delegated agent to move its ticket to ${TICKET_COLUMN_FOR_READY}`,
        420_000,
        2_000,
      );
      runner.writeJson('delegated-ticket.json', settled);

      const activity = activityOf(settled);
      const report = activity.find((entry) => textOf(entry).includes(token));
      runner.assert(Boolean(report), `the ticket carries the agent's own report (got ${JSON.stringify(activity)})`, activity);
      runner.assert(
        report.author === workerId,
        `the report is attributed to the delegated session (author=${report.author}, worker=${workerId})`,
        report,
      );
      runner.assert(
        textOf(report).includes(GUIDANCE_TOKEN),
        `the agent read the repository guidance in its worktree (comment=${JSON.stringify(textOf(report))})`,
        report,
      );
      return report;
    });

    await runner.step('a_brief_survives_a_crash_before_the_first_word', async () => {
      const before = hostProcesses().map((entry) => entry.pid);
      const earlyToken = `PIREVIVE-${runner.runId.slice(-6).toUpperCase()}`;
      const delegate = runAttn([
        'delegate',
        '--source-session', delegatorId,
        '--agent', 'nisse',
        // One call, deliberately: asking the agent to retry would hide a
        // regression of the read-before-you-write gate (#821).
        '--brief', [
          'Using your bash tool, run exactly this command, once:',
          `attn ticket status ready_for_review --comment "${earlyToken}"`,
          'Then reply with the single word: done',
        ].join('\n'),
        '--name', `pir-${runner.runId.slice(-6)}`,
      ]);
      earlyWorkerId = delegate.json?.session_id;
      runner.assert(typeof earlyWorkerId === 'string' && earlyWorkerId.length > 0, `the second delegation returned a session id (got ${JSON.stringify(delegate.json)})`, delegate.json);
      await observer.waitForSession({ id: earlyWorkerId, timeoutMs: 45_000 });
      const host = await waitForHost(before, 'the host for the second delegation');

      // Kill at once: pi writes nothing to disk until its first assistant
      // message, and this has to land inside that window.
      const sessionsDir = path.join(dataDir, 'hosts', 'state', earlyWorkerId);
      process.kill(host.pid, 'SIGKILL');
      const filesAtKill = fs.existsSync(sessionsDir) ? fs.readdirSync(sessionsDir) : [];
      runner.assert(filesAtKill.length === 0, `the kill landed before any session file existed (found ${JSON.stringify(filesAtKill)})`, filesAtKill);

      await client.request('select_session', { sessionId: earlyWorkerId });
      await pollFor(
        async () => {
          const current = await conversationState(client, earlyWorkerId);
          return current && current.recoverable ? current : null;
        },
        'the crashed delegation to park as recoverable',
        90_000,
      );

      const known = hostProcesses().map((entry) => entry.pid);
      await client.request('dom_click', { selector: reloadOf(earlyWorkerId) });
      await waitForHost(known, 'a replacement host for the crashed delegation');

      const earlyTicket = await ticketFor(runAttn, earlyWorkerId);
      const settled = await pollFor(
        async () => {
          const show = runAttn(['ticket', 'show', earlyTicket.id, '--json'], { allowFailure: true });
          return show.json?.status === TICKET_COLUMN_FOR_READY ? show.json : null;
        },
        'the relaunched delegation to do the work its replayed brief asked for',
        420_000,
        2_000,
      );
      const activity = activityOf(settled);
      runner.assert(
        activity.some((entry) => textOf(entry).includes(earlyToken)),
        `the replayed brief reached the fresh session (got ${JSON.stringify(activity)})`,
        activity,
      );
      runner.writeJson('replayed-brief-ticket.json', settled);
      return { killedPid: host.pid, filesAtKill, ticket: earlyTicket.id };
    });

    const summary = runner.finishSuccess({ profile, delegatorId, workerId, earlyWorkerId, ticketId });
    console.log('[RealAppHarness] nisse delegation scenario passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { delegatorId, workerId, earlyWorkerId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of [workerId, earlyWorkerId, delegatorId]) {
      if (!sessionId) continue;
      await client.request('close_session', { sessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
