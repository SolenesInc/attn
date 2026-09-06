#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  queryDaemonDb,
  relaunchAppAndConnect,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  captureSessionArtifacts,
  sleep,
  waitForFirstWorkspacePane,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import {
  ensureClaudeInitialPanePromptReady,
  ensureCodexInitialPanePromptReady,
} from './scenarioAgents.mjs';
import { agentHomeRoots, writeMockAgentFixture } from './mockAgent.mjs';
import { currentHarnessProfile, dataDirForProfile, profileCliEnv } from './harnessProfile.mjs';
import { appDaemonInTree } from './platform.mjs';

const execFileAsync = promisify(execFile);

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function readPersistedResumeId(dataDir, sessionId) {
  const dbPath = path.join(dataDir, 'attn.db');
  return queryDaemonDb(
    dbPath,
    `select coalesce(resume_session_id, '') from sessions where id = '${sessionId}';`,
  );
}

function readPersistedTranscriptPath(dataDir, sessionId) {
  const dbPath = path.join(dataDir, 'attn.db');
  return queryDaemonDb(
    dbPath,
    `select coalesce(transcript_path, '') from sessions where id = '${sessionId}';`,
  );
}

function normalizeExistingPath(value) {
  const resolved = path.resolve(String(value || ''));
  try {
    return fs.realpathSync.native(resolved);
  } catch {
    return resolved;
  }
}

// A rollout's first line carries the session_meta naming its id, which is how
// the daemon's own codex driver locates it.
function findCodexRollout(resumeId) {
  const root = agentHomeRoots().codexSessions;
  const stack = [root];
  while (stack.length > 0) {
    const dir = stack.pop();
    let entries = [];
    try {
      entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const entry of entries) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        stack.push(full);
        continue;
      }
      // internal/transcript/discovery.go narrows on the file name first; without
      // it this reads every rollout the machine has ever written.
      if (!entry.name.endsWith(`${resumeId}.jsonl`)) continue;
      let head = '';
      try {
        head = fs.readFileSync(full, 'utf8').split('\n', 1)[0] || '';
      } catch {
        continue;
      }
      if (head.includes(`"id":"${resumeId}"`)) return full;
    }
  }
  return null;
}

async function waitFor(description, predicate, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await predicate();
    if (last) return last;
    await sleep(500);
  }
  throw new Error(`Timed out waiting for ${description}`);
}

// SIGKILL only PIDs this profile's registry recorded: never match by name or
// path, or the kill lands on another session.
function killProfileRuntimeLikeACrash(dataDir, log) {
  const killed = { workers: [], daemon: null };
  const workersRoot = path.join(dataDir, 'workers');
  const instances = fs.existsSync(workersRoot) ? fs.readdirSync(workersRoot) : [];
  for (const instance of instances) {
    const registryDir = path.join(workersRoot, instance, 'registry');
    if (!fs.existsSync(registryDir)) continue;
    for (const entry of fs.readdirSync(registryDir)) {
      const raw = fs.readFileSync(path.join(registryDir, entry), 'utf8');
      const record = JSON.parse(raw);
      const pid = Number(record.pid ?? record.Pid ?? record.worker_pid);
      if (!Number.isInteger(pid) || pid <= 1) continue;
      try {
        process.kill(pid, 'SIGKILL');
        killed.workers.push({ session: entry, pid });
      } catch (error) {
        log(`worker ${entry} pid ${pid} already gone: ${error.message}`);
      }
    }
  }
  const pidFile = path.join(dataDir, 'attn.pid');
  if (fs.existsSync(pidFile)) {
    const pid = Number(fs.readFileSync(pidFile, 'utf8').trim());
    if (Number.isInteger(pid) && pid > 1) {
      try {
        process.kill(pid, 'SIGKILL');
        killed.daemon = pid;
      } catch (error) {
        log(`daemon pid ${pid} already gone: ${error.message}`);
      }
    }
  }
  return killed;
}

function daemonLogTail(dataDir, lines = 200) {
  const logPath = path.join(dataDir, 'daemon.log');
  if (!fs.existsSync(logPath)) return '';
  return fs.readFileSync(logPath, 'utf8').split('\n').slice(-lines).join('\n');
}

// The whole file, not a tail: a reboot writes enough lines to push the previous
// run's summary out of any window, and the count is what says a new pass ran.
function reconciliationSummaries(dataDir) {
  const logPath = path.join(dataDir, 'daemon.log');
  if (!fs.existsSync(logPath)) return [];
  return fs.readFileSync(logPath, 'utf8')
    .split('\n')
    .filter((line) => line.includes('worker session reconciliation summary'));
}

function processAlive(pid) {
  if (!pid) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

async function submitCodexPrompt(client, sessionId, paneId, text) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text });
  // Codex reads a fast character stream as a paste and makes the next Enter a
  // newline; wait past that window so this is a real submit.
  await sleep(250);
  await client.request('type_pane_via_ui', { sessionId, paneId, text: '\n' });
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-crash-recovery-resumability.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  const dataDir = dataDirForProfile(profile);
  const runner = createScenarioRunner(options, {
    scenarioId: 'CRASH-REC',
    tier: 'tier2-local-mock-agent',
    prefix: 'scenario-crash-recovery-resumability',
    metadata: {
      agents: 'codex+claude+shell',
      focus: 'a crash keeps what it can bring back, with its codex binding and its pane intact',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  // Daemon lifecycle and CLI reads must run through the exact build under test,
  // not an unrelated ./attn on PATH.
  const attnBin = appDaemonInTree(options.appPath);
  const daemonEnv = profileCliEnv(profile);

  const token = `CRASHREC${Date.now()}`;
  const claudeDir = path.join(runner.sessionDir, 'never-prompted');
  let codexSessionId = null;
  let codexResumeId = null;
  let codexTranscriptPath = null;
  let claudeSessionId = null;
  let summariesBeforeCrash = 0;
  let reconciliationAfterReboot = null;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    codexSessionId = await runner.step('create_codex_session_with_a_conversation', async () => {
      writeMockAgentFixture(runner.sessionDir, {
        name: 'crash recovery codex',
        resumable: true,
        turns: [{ includes: token, actions: [{ type: 'reply', text: token }] }],
      });
      const sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `crashrec-codex-${runner.runId}`,
        agent: 'codex',
        promptReadyFn: ensureCodexInitialPanePromptReady,
      });
      const pane = await waitForFirstWorkspacePane(client, sessionId, 'codex pane', 20_000);
      codexResumeId = await waitFor(
        'codex to report its native resume id',
        async () => (readPersistedResumeId(dataDir, sessionId)) || null,
        90_000,
      );
      await submitCodexPrompt(client, sessionId, pane.paneId, `Reply with exactly ${token} and nothing else.`);
      // The pane would show the token from local echo alone, so the claim needs
      // the rollout on disk.
      const rollout = await waitFor(
        'the token to reach the codex rollout on disk',
        () => {
          const file = findCodexRollout(codexResumeId);
          if (!file) return null;
          return fs.readFileSync(file, 'utf8').includes(token) ? file : null;
        },
        120_000,
      );
      // The binding assertions after the crash compare against what the daemon
      // persisted, so wait for that write instead of assuming it landed.
      codexTranscriptPath = await waitFor(
        `the daemon to persist ${rollout} as the codex transcript path`,
        () => {
          const stored = readPersistedTranscriptPath(dataDir, sessionId);
          return stored && normalizeExistingPath(stored) === normalizeExistingPath(rollout) ? stored : null;
        },
        30_000,
      );
      runner.writeJson('codex-conversation.json', {
        resumeId: codexResumeId,
        rollout,
        transcriptPath: codexTranscriptPath,
      });
      if (codexResumeId === sessionId) {
        throw new Error(`the stored resume id is the attn session id (${sessionId}); it must be codex's own native id`);
      }
      return sessionId;
    });

    claudeSessionId = await runner.step('create_claude_session_that_never_took_a_turn', async () => {
      // Resumable, and still nothing on disk: claude writes its transcript on the
      // first turn, and this session never takes one.
      writeMockAgentFixture(claudeDir, { name: 'crash recovery claude', resumable: true, turns: [] });
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: claudeDir,
        label: `crashrec-claude-${runner.runId}`,
        agent: 'claude',
        promptReadyFn: ensureClaudeInitialPanePromptReady,
      });
    });

    await runner.step('record_state_before_the_crash', async () => {
      const claudeResumeId = readPersistedResumeId(dataDir, claudeSessionId);
      const claudeTranscript = agentHomeRoots().claudeProjects;
      const claudeHasTranscript = fs.existsSync(claudeTranscript)
        && fs.readdirSync(claudeTranscript).some((dir) => fs.existsSync(path.join(claudeTranscript, dir, `${claudeResumeId}.jsonl`)));
      if (claudeHasTranscript) {
        throw new Error('the claude session already has a transcript; it cannot stand in for the unresumable case');
      }
      summariesBeforeCrash = reconciliationSummaries(dataDir).length;
      runner.writeJson('before-crash.json', {
        codex: {
          session: observer.getSession(codexSessionId),
          resumeId: codexResumeId,
          transcriptPath: codexTranscriptPath,
        },
        claude: { session: observer.getSession(claudeSessionId), resumeId: claudeResumeId, hasTranscript: false },
        reconciliationSummaries: summariesBeforeCrash,
      });
    });

    const killed = await runner.step('crash_the_machine', async () => {
      const result = killProfileRuntimeLikeACrash(dataDir, (m) => runner.log(m));
      runner.writeJson('killed.json', result);
      if (result.workers.length === 0) {
        throw new Error('no pty workers were killed; the crash was not reproduced');
      }
      const pids = [...result.workers.map((worker) => worker.pid), result.daemon].filter(Boolean);
      await waitFor(
        `the killed pids ${pids.join(',')} to leave the process table`,
        () => (pids.every((pid) => !processAlive(pid)) ? pids : null),
        15_000,
      );
      return result;
    });
    runner.log(`crashed: ${killed.workers.length} workers, daemon pid ${killed.daemon}`);

    await runner.step('reboot_into_the_app', async () => {
      await relaunchAppAndConnect(client, observer);
      // Startup recovery runs before clients cross the barrier, but the observer's
      // first snapshot can beat the deferred pass; the summary line is the receipt.
      reconciliationAfterReboot = await waitFor(
        'the rebooted daemon to log a worker session reconciliation summary',
        () => {
          const summaries = reconciliationSummaries(dataDir);
          return summaries.length > summariesBeforeCrash ? summaries.at(-1) : null;
        },
        60_000,
      );
      runner.log('reconciliation after reboot', { summary: reconciliationAfterReboot });
      runner.writeText('daemon-after-reboot.log', daemonLogTail(dataDir));
    });

    await runner.step('assert_the_resumable_codex_session_survived', async () => {
      const session = await waitFor(
        'the codex session to settle on a recovery verdict',
        async () => {
          const current = observer.getSession(codexSessionId);
          return current && current.state === 'recoverable' ? current : null;
        },
        30_000,
      ).catch(() => observer.getSession(codexSessionId));
      if (!session) {
        throw new Error('the codex session was deleted by startup recovery despite having a rollout to resume');
      }
      if (session.state !== 'recoverable') {
        throw new Error(`codex session state = ${session.state}, want recoverable`);
      }
      const recovery = reconciliationAfterReboot;
      runner.writeJson('after-crash-codex.json', { session, recovery });
      if (!recovery || !/marked_recoverable=[1-9]/.test(recovery)) {
        throw new Error(`reconciliation summary did not mark anything recoverable: ${recovery}`);
      }
    });

    await runner.step('assert_the_unresumable_claude_session_was_reaped', async () => {
      const session = observer.getSession(claudeSessionId);
      if (session) {
        throw new Error(`claude session survived as ${session.state}; it has no transcript to resume`);
      }
      const workspace = observer.getWorkspace(claudeSessionId);
      if (workspace) {
        throw new Error('the reaped claude session left its workspace pane behind');
      }
    });

    await runner.step('revive_the_codex_pane_and_read_the_old_conversation_back', async () => {
      await client.request('select_session', { sessionId: codexSessionId });
      const pane = await waitForFirstWorkspacePane(client, codexSessionId, 'revived codex pane', 30_000);
      await waitForPaneVisible(client, codexSessionId, pane.paneId, 30_000);
      await waitForPaneText(
        client,
        codexSessionId,
        pane.paneId,
        (text) => text.includes(token),
        'the resumed codex conversation still carries what was said before the crash',
        120_000,
      );
      const revived = await client.request('read_pane_text', { sessionId: codexSessionId, paneId: pane.paneId }, { timeoutMs: 20_000 });
      const revivedText = revived?.text || '';
      runner.writeText('revived-pane.txt', revivedText);
      if (/Failed to attach PTY/.test(revivedText)) {
        throw new Error('the revived pane repainted the conversation behind an attach-failure banner');
      }
      await captureSessionArtifacts(client, runner.runDir, 'revived-codex', codexSessionId);
      const session = observer.getSession(codexSessionId);
      if (!session || session.state === 'recoverable') {
        throw new Error(`codex session state after revive = ${session?.state}; want a live state`);
      }
    });

    await runner.step('assert_the_codex_binding_outlived_the_crash', async () => {
      const resumeId = await waitFor(
        `the codex resume id to still be ${codexResumeId} after the crash`,
        () => (readPersistedResumeId(dataDir, codexSessionId) === codexResumeId ? codexResumeId : null),
        30_000,
      );
      const transcriptPath = await waitFor(
        `the codex transcript path to still be ${codexTranscriptPath} after the crash`,
        () => {
          const stored = readPersistedTranscriptPath(dataDir, codexSessionId);
          return stored && normalizeExistingPath(stored) === normalizeExistingPath(codexTranscriptPath)
            ? stored
            : null;
        },
        30_000,
      );
      const { stdout } = await execFileAsync(attnBin, ['session', 'transcript', codexSessionId, '--json'], {
        env: daemonEnv,
        timeout: 20_000,
      });
      runner.writeText('transcript-after-crash.json', stdout);
      if (!stdout.includes(token)) {
        throw new Error(`the rebooted daemon's public CLI did not read the pre-crash reply back from ${transcriptPath}`);
      }
      runner.writeJson('binding-after-crash.json', { resumeId, transcriptPath });
    });

    const summary = await runner.finishSuccess({
      codexSessionId,
      codexResumeId,
      codexTranscriptPath,
      claudeSessionId,
      token,
      artifacts: { runDir: runner.runDir, trace: runner.tracePath },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (codexSessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', codexSessionId).catch(() => {});
    }
    runner.writeText('daemon-on-failure.log', daemonLogTail(dataDir, 400));
    const summary = await runner.finishFailure(error, { codexSessionId, claudeSessionId, token });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of [codexSessionId, claudeSessionId]) {
      if (sessionId) {
        await cleanupSessionViaAppClose(client, observer, sessionId).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
