#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';
import { ensureClaudePromptReadyViaPty, ensureCodexPromptReadyViaPty, preTrustClaudeFolder } from './scenarioAgents.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, profileCliEnv } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function assert(condition, message) {
  if (!condition) throw new Error(`Assertion failed: ${message}`);
}

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 250) {
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

function makeAttnRunner(attnBin, profile) {
  return function runAttn(args) {
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      env: profileCliEnv(profile),
    }).trim();
    // The profile banner goes to stderr, so --json stdout is pure JSON
    // (object or array depending on the command).
    const json = stdout.startsWith('{') || stdout.startsWith('[') ? JSON.parse(stdout) : null;
    return { stdout, json };
  };
}

function ticketStatus(runAttn, ticketId) {
  const { json } = runAttn(['ticket', 'list', '--all', '--json']);
  const tickets = Array.isArray(json) ? json : [];
  const ticket = tickets.find((t) => t.id === ticketId);
  return ticket?.status || null;
}

function reconcileTaskCount(profile, ticketId) {
  const dbPath = path.join(os.homedir(), `.attn-${profile}`, 'attn.db');
  const out = execFileSync('sqlite3', [dbPath, `SELECT COUNT(*) FROM tasks WHERE kind='reconcile' AND subject='${ticketId}';`], { encoding: 'utf8' });
  return Number(out.trim());
}

function workerPid(sessionId) {
  try {
    const out = execFileSync('pgrep', ['-f', `pty-worker.*--session-id ${sessionId}`], { encoding: 'utf8' });
    const pids = out.trim().split('\n').filter(Boolean).map(Number);
    return pids[0] || null;
  } catch {
    return null;
  }
}

// The submit is retried: right after a reload the resumed codex TUI can look
// prompt-ready (stale replayed pane text) while still swallowing input.
async function driveAgentToWorking(client, observer, sessionId, note) {
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${sessionId}`, 20_000);
  const prompt = 'Count from 1 to 40, one number per line, then say done. Do not use tools.';
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    await client.request('write_pane', { sessionId, paneId: pane.paneId, text: prompt, submit: false });
    await delay(1_200);
    await client.request('write_pane', { sessionId, paneId: pane.paneId, text: '\r', submit: false });
    try {
      await pollFor(
        () => (observer.getSession(sessionId)?.state === 'working' ? true : null),
        `${sessionId} to start working (attempt ${attempt})`,
        12_000,
        300,
      );
      note(`agent is mid-turn (working) after attempt ${attempt}`);
      return;
    } catch (error) {
      if (attempt === 3) throw error;
      note(`submit attempt ${attempt} did not reach working; retrying`);
      await delay(3_000);
    }
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-reload-not-crash.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the reload-not-crash scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const { runId, runDir, sessionDir } = createRunContext(options, 'reload-not-crash');

  const repoDir = path.join(sessionDir, 'target-repo');
  fs.mkdirSync(repoDir, { recursive: true });
  execFileSync('git', ['init', '-q'], { cwd: repoDir });
  execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
    cwd: repoDir,
    env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;
  let crashSessionId = null;
  const evidence = { runId, profile, steps: [] };
  const note = (m, extra) => { console.log(`[reload-not-crash] ${m}`); evidence.steps.push({ t: Date.now(), m, ...extra }); };
  const saveEvidence = (verdict) => {
    evidence.verdict = verdict;
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
  };

  console.log(`[reload-not-crash] profile=${profile} runDir=${runDir} repo=${repoDir}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `reload-target-${runId.slice(-6)}`,
      agent: 'codex',
      sessionWaitMs: 30_000,
      promptReadyFn: ensureCodexPromptReadyViaPty,
      promptReadyTimeoutMs: 90_000,
    });
    await client.request('select_session', { sessionId });
    note('target agent ready', { sessionId });

    const created = runAttn(['ticket', 'new', '--title', `Reload fixture ${runId.slice(-6)}`, '--session', sessionId, '--json']);
    const ticketId = created.json?.ticket_id;
    assert(typeof ticketId === 'string' && ticketId.length > 0, `ticket new returned an id (got ${JSON.stringify(created.json)})`);
    runAttn(['ticket', 'take', ticketId, '--session', sessionId, '--confirm']);
    // `ticket take` assigns but does not move the column.
    runAttn(['ticket', 'status', 'in_progress', '--session', sessionId, '--comment', 'harness: starting work']);
    const statusAfterTake = ticketStatus(runAttn, ticketId);
    assert(statusAfterTake === 'working', `ticket bound and working after take (got ${statusAfterTake})`);
    note('ticket bound to agent', { ticketId, status: statusAfterTake });

    await driveAgentToWorking(client, observer, sessionId, note);
    await client.request('reload_session', { sessionId }, { timeoutMs: 45_000 });
    note('reload_session completed');
    await pollFor(() => (workerPid(sessionId) ? true : null), 'respawned pty-worker after reload', 20_000, 300);
    // Give any (buggy) crash/reconcile write a moment to land before asserting.
    await delay(2_000);

    const statusAfterReload = ticketStatus(runAttn, ticketId);
    assert(statusAfterReload === 'working', `ticket unchanged after reload (got ${statusAfterReload})`);
    const tasksAfterReload = reconcileTaskCount(profile, ticketId);
    assert(tasksAfterReload === 0, `no reconcile task after reload (got ${tasksAfterReload})`);
    note('reload left the ticket alone', { statusAfterReload, tasksAfterReload });

    // Claude, not codex: a killed claude fires no Stop hook, so the daemon still
    // sees the session mid-flight when the worker death lands.
    preTrustClaudeFolder(repoDir);
    crashSessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `crash-target-${runId.slice(-6)}`,
      agent: 'claude',
      sessionWaitMs: 30_000,
      promptReadyFn: ensureClaudePromptReadyViaPty,
      promptReadyTimeoutMs: 90_000,
    });
    await client.request('select_session', { sessionId: crashSessionId });
    const crashCreated = runAttn(['ticket', 'new', '--title', `Crash fixture ${runId.slice(-6)}`, '--session', crashSessionId, '--json']);
    const crashTicketId = crashCreated.json?.ticket_id;
    assert(crashTicketId, 'crash-leg ticket created');
    runAttn(['ticket', 'take', crashTicketId, '--session', crashSessionId, '--confirm']);
    runAttn(['ticket', 'status', 'in_progress', '--session', crashSessionId, '--comment', 'harness: starting work']);
    note('crash-leg ticket bound', { crashTicketId });

    await driveAgentToWorking(client, observer, crashSessionId, note);
    const pid = workerPid(crashSessionId);
    assert(pid, `found pty-worker pid for ${crashSessionId}`);
    process.kill(pid, 'SIGKILL');
    note('killed pty-worker', { pid });

    // A SIGKILLed worker produces no immediate PTY exit: the daemon forces it
    // only after "unreachable for 30s", so poll well past that.
    const crashed = await pollFor(
      () => (ticketStatus(runAttn, crashTicketId) === 'crashed' ? true : null),
      `ticket ${crashTicketId} to be stamped crashed after a real worker death`,
      90_000,
      500,
    );
    assert(crashed, 'ticket crashed after real kill');
    const tasksAfterCrash = reconcileTaskCount(profile, crashTicketId);
    assert(tasksAfterCrash === 1, `reconcile task minted for the real crash (got ${tasksAfterCrash})`);
    const reloadTicketFinal = ticketStatus(runAttn, ticketId);
    assert(reloadTicketFinal === 'working', `reload-leg ticket still working at the end (got ${reloadTicketFinal})`);
    note('real crash still detected; reload ticket untouched', { tasksAfterCrash, reloadTicketFinal });

    saveEvidence('pass');
    console.log(`[reload-not-crash] PASS runDir=${runDir}`);
  } catch (error) {
    if (sessionId) {
      try {
        const pane = await waitForFirstWorkspacePane(client, sessionId, 'pane for failure dump', 5_000);
        const text = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
        evidence.failurePaneText = (text?.text || '').slice(-2000);
        console.error(`[reload-not-crash] pane at failure:\n${evidence.failurePaneText}`);
      } catch { /* best effort */ }
    }
    saveEvidence(`fail: ${error?.message || error}`);
    console.error(`[reload-not-crash] FAIL: ${error?.stack || error}`);
    process.exitCode = 1;
  } finally {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
    if (crashSessionId) await client.request('close_session', { sessionId: crashSessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

await main();
