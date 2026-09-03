#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  captureSessionArtifacts,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { ensureCodexInitialPanePromptReady } from './scenarioAgents.mjs';
import { agentHomeRoots, writeMockAgentFixture, MOCK_AGENT_NEW_CONVERSATION } from './mockAgent.mjs';
import { currentHarnessProfile, dataDirForProfile, profileCliEnv } from './harnessProfile.mjs';
import { appDaemonInTree } from './platform.mjs';

const execFileAsync = promisify(execFile);

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: Boolean(options.help) };
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function submitCodexPromptViaUi(client, sessionId, paneId, text) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text });
  await delay(250);
  await client.request('type_pane_via_ui', { sessionId, paneId, text: '\n' });
}

function dbPathForHarnessProfile() {
  return path.join(dataDirForProfile(currentHarnessProfile()), 'attn.db');
}

function sqlString(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

async function queryStoredResumeId(dbPath, sessionId) {
  const sql = `SELECT resume_session_id FROM sessions WHERE id = ${sqlString(sessionId)} LIMIT 1;`;
  try {
    const { stdout } = await execFileAsync('sqlite3', [dbPath, sql], { timeout: 5_000 });
    return stdout.trim();
  } catch {
    return '';
  }
}

async function queryStoredTranscriptPath(dbPath, sessionId) {
  const sql = `SELECT transcript_path FROM sessions WHERE id = ${sqlString(sessionId)} LIMIT 1;`;
  try {
    const { stdout } = await execFileAsync('sqlite3', [dbPath, sql], { timeout: 5_000 });
    return stdout.trim();
  } catch {
    return '';
  }
}

async function queryStoredSessionCost(dbPath, sessionId) {
  const sql = `SELECT session_cost_json FROM sessions WHERE id = ${sqlString(sessionId)} LIMIT 1;`;
  try {
    const { stdout } = await execFileAsync('sqlite3', [dbPath, sql], { timeout: 5_000 });
    const raw = stdout.trim();
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

function sessionCostObservationCount(cost) {
  return Object.keys(cost?.observations || {}).length;
}

function sessionCostTokenTotal(cost) {
  return Object.values(cost?.ledger || {}).reduce((total, usage) => {
    return total + Object.values(usage || {}).reduce((sum, value) => {
      return sum + (typeof value === 'number' ? value : 0);
    }, 0);
  }, 0);
}

async function waitForSessionCostAdvance(dbPath, sessionId, previousCost = null, timeoutMs = 30_000) {
  const previousObservations = sessionCostObservationCount(previousCost);
  const previousTokens = sessionCostTokenTotal(previousCost);
  const startedAt = Date.now();
  let lastCost = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastCost = await queryStoredSessionCost(dbPath, sessionId);
    if (
      sessionCostObservationCount(lastCost) > previousObservations &&
      sessionCostTokenTotal(lastCost) > previousTokens
    ) {
      return lastCost;
    }
    await delay(250);
  }
  throw new Error(
    `Timed out waiting for Codex cost to advance in ${dbPath}; previous=${JSON.stringify(previousCost)} last=${JSON.stringify(lastCost)}`
  );
}

async function waitForStoredResumeId(dbPath, sessionId, timeoutMs = 30_000) {
  const startedAt = Date.now();
  let lastValue = '';
  while (Date.now() - startedAt < timeoutMs) {
    lastValue = await queryStoredResumeId(dbPath, sessionId);
    if (lastValue) {
      return lastValue;
    }
    await delay(250);
  }
  throw new Error(`Timed out waiting for stored Codex resume id in ${dbPath}; last value=${JSON.stringify(lastValue)}`);
}

async function waitForStoredResumeIdChange(dbPath, sessionId, previousId, timeoutMs = 30_000) {
  const startedAt = Date.now();
  let lastValue = '';
  while (Date.now() - startedAt < timeoutMs) {
    lastValue = await queryStoredResumeId(dbPath, sessionId);
    if (lastValue && lastValue !== previousId) {
      return lastValue;
    }
    await delay(250);
  }
  throw new Error(
    `Timed out waiting for Codex resume id to change in ${dbPath}; previous=${JSON.stringify(previousId)} last=${JSON.stringify(lastValue)}`
  );
}

async function waitForStoredTranscriptPath(dbPath, sessionId, expectedPath, timeoutMs = 30_000) {
  const startedAt = Date.now();
  let lastValue = '';
  while (Date.now() - startedAt < timeoutMs) {
    lastValue = await queryStoredTranscriptPath(dbPath, sessionId);
    if (lastValue && normalizeExistingPath(lastValue) === normalizeExistingPath(expectedPath)) {
      return lastValue;
    }
    await delay(250);
  }
  throw new Error(
    `Timed out waiting for stored Codex transcript path in ${dbPath}; expected=${JSON.stringify(expectedPath)} last=${JSON.stringify(lastValue)}`
  );
}

async function waitForSessionNotWorking(observer, sessionId, timeoutMs = 20_000) {
  return observer.waitFor(() => {
    const session = observer.getSession(sessionId);
    if (!session) {
      return null;
    }
    return session.state !== 'working' ? session : null;
  }, `session ${sessionId} to leave working state`, timeoutMs);
}

function codexSessionsRoot() {
  return agentHomeRoots().codexSessions;
}

function walkJsonlFiles(rootDir) {
  const files = [];
  const stack = [rootDir];
  while (stack.length > 0) {
    const current = stack.pop();
    let entries = [];
    try {
      entries = fs.readdirSync(current, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const entry of entries) {
      const fullPath = path.join(current, entry.name);
      if (entry.isDirectory()) {
        stack.push(fullPath);
      } else if (entry.isFile() && entry.name.endsWith('.jsonl')) {
        files.push(fullPath);
      }
    }
  }
  return files;
}

function readCodexSessionMeta(filePath) {
  let fd = null;
  try {
    fd = fs.openSync(filePath, 'r');
    const buffer = Buffer.alloc(64 * 1024);
    const bytes = fs.readSync(fd, buffer, 0, buffer.length, 0);
    const text = buffer.subarray(0, bytes).toString('utf8');
    for (const line of text.split('\n')) {
      if (!line.trim()) {
        continue;
      }
      const event = JSON.parse(line);
      if (event?.type === 'session_meta') {
        return event.payload || null;
      }
    }
  } catch {
    return null;
  } finally {
    if (fd !== null) {
      fs.closeSync(fd);
    }
  }
  return null;
}

function normalizeExistingPath(value) {
  const resolved = path.resolve(String(value || ''));
  try {
    return fs.realpathSync.native(resolved);
  } catch {
    return resolved;
  }
}

function findCodexTranscript({ sessionId, cwd }) {
  const expectedCwd = normalizeExistingPath(cwd);
  // Same narrowing internal/transcript/discovery.go does: the sessions tree holds
  // every rollout this machine ever wrote, and only one file name can match.
  for (const filePath of walkJsonlFiles(codexSessionsRoot())) {
    if (!path.basename(filePath).endsWith(`${sessionId}.jsonl`)) continue;
    const meta = readCodexSessionMeta(filePath);
    if (meta?.id === sessionId && normalizeExistingPath(meta.cwd) === expectedCwd) {
      return { filePath, meta };
    }
  }
  return null;
}

async function waitForCodexTranscript({ sessionId, cwd, timeoutMs = 30_000 }) {
  const startedAt = Date.now();
  let found = null;
  while (Date.now() - startedAt < timeoutMs) {
    found = findCodexTranscript({ sessionId, cwd });
    if (found) {
      return found;
    }
    await delay(500);
  }
  throw new Error(`Timed out waiting for Codex transcript id=${sessionId} cwd=${cwd}`);
}

function codexAssistantTexts(filePath) {
  let content = '';
  try {
    content = fs.readFileSync(filePath, 'utf8');
  } catch {
    return [];
  }

  const texts = [];
  for (const line of content.split('\n')) {
    if (!line.trim()) {
      continue;
    }
    try {
      const event = JSON.parse(line);
      const payload = event?.type === 'response_item' ? event.payload : null;
      if (payload?.type !== 'message' || payload?.role !== 'assistant' || !Array.isArray(payload.content)) {
        continue;
      }
      const text = payload.content
        .filter((item) => item?.type === 'output_text' && typeof item.text === 'string')
        .map((item) => item.text)
        .join('');
      if (text) {
        texts.push(text);
      }
    } catch {
      // The final JSONL line may still be in flight.
    }
  }
  return texts;
}

async function waitForCodexAssistantText(filePath, expected, timeoutMs = 45_000) {
  const startedAt = Date.now();
  let lastTexts = [];
  while (Date.now() - startedAt < timeoutMs) {
    lastTexts = codexAssistantTexts(filePath);
    if (lastTexts.some((text) => text.trim() === expected)) {
      return;
    }
    await delay(250);
  }
  throw new Error(
    `Timed out waiting for Codex assistant text ${JSON.stringify(expected)} in ${filePath}; last=${JSON.stringify(lastTexts.at(-1) || '')}`
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-codex-resume-mapping.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-CODEX-RESUME',
    tier: 'tier2-local-mock-agent',
    prefix: 'scenario-codex-resume-mapping',
    metadata: {
      agent: 'codex',
      focus: 'Codex /new changes the native conversation binding and reload resumes the successor',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const dbPath = dbPathForHarnessProfile();
  const attnBin = appDaemonInTree(options.appPath);
  const daemonEnv = profileCliEnv(currentHarnessProfile());
  const firstReply = 'ATTN_FIRST_TURN_COMPLETE';
  const successorReply = 'new-ok';
  let sessionId = null;
  let initialNativeSessionId = null;
  let nativeSessionId = null;
  let transcript = null;
  let initialCost = null;
  let successorCost = null;
  let initialPaneId = null;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_codex_session', async () => {
      writeMockAgentFixture(runner.sessionDir, {
        name: 'codex resume mock',
        resumable: true,
        turns: [
          { includes: firstReply, actions: [{ type: 'reply', text: firstReply }] },
          { includes: successorReply, actions: [{ type: 'reply', text: successorReply }] },
        ],
      });
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `codex-resume-${runner.runId}`,
        agent: 'codex',
        waitForInitialPaneVisible: false,
      });
    });

    initialNativeSessionId = await runner.step('assert_the_codex_hook_records_the_native_id', async () => {
      const readiness = await ensureCodexInitialPanePromptReady(client, sessionId, 60_000);
      initialPaneId = readiness.paneId;
      await waitForPaneVisible(client, sessionId, initialPaneId, 30_000);
      runner.writeJson('codex-readiness.json', readiness);
      await submitCodexPromptViaUi(client, sessionId, initialPaneId, `Reply with exactly: ${firstReply}`);
      await delay(1000);
      runner.writeJson('codex-after-submit.json', await client.request('read_pane_text', {
        sessionId,
        paneId: initialPaneId,
      }));
      const resumeId = await waitForStoredResumeId(dbPath, sessionId, 45_000);
      runner.assert(resumeId !== sessionId, 'stored resume id is Codex native id, not attn wrapper id', {
        attnSessionId: sessionId,
        resumeId,
      });
      return resumeId;
    });

    nativeSessionId = initialNativeSessionId;

    transcript = await runner.step('assert_the_resume_id_matches_the_rollout_on_disk', async () => {
      const found = await waitForCodexTranscript({
        sessionId: nativeSessionId,
        cwd: runner.sessionDir,
        timeoutMs: 45_000,
      });
      await waitForCodexAssistantText(found.filePath, firstReply, 45_000);
      await waitForStoredTranscriptPath(dbPath, sessionId, found.filePath, 30_000);
      initialCost = await waitForSessionCostAdvance(dbPath, sessionId, null, 30_000);
      runner.writeJson('codex-transcript.json', found);
      runner.writeJson('codex-cost-before-new.json', initialCost);
      const stoppedSession = await waitForSessionNotWorking(observer, sessionId, 20_000);
      runner.assert(stoppedSession.state !== 'working', 'the Codex session is not green after the turn stops', {
        state: stoppedSession.state,
      });
      await captureSessionArtifacts(client, runner.runDir, '01-first-launch', sessionId);
      return found;
    });

    await runner.step('start_successor_with_codex_new', async () => {
      await submitCodexPromptViaUi(client, sessionId, initialPaneId, MOCK_AGENT_NEW_CONVERSATION);
      const freshPrompt = await waitForPaneText(
        client,
        sessionId,
        initialPaneId,
        (text) => text.includes('started a new conversation'),
        'the pane to come back on a new conversation',
        20_000,
      );
      runner.writeJson('codex-after-new.json', freshPrompt);
      await submitCodexPromptViaUi(client, sessionId, initialPaneId, `Reply with exactly: ${successorReply}`);
    });

    const initialTranscript = transcript;
    nativeSessionId = await runner.step('assert_codex_new_changes_native_binding', async () => {
      const successorId = await waitForStoredResumeIdChange(
        dbPath,
        sessionId,
        initialNativeSessionId,
        45_000
      );
      runner.assert(successorId !== sessionId, 'successor binding is a Codex native id', {
        attnSessionId: sessionId,
        initialNativeSessionId,
        successorId,
      });
      return successorId;
    });

    transcript = await runner.step('assert_codex_new_rebinds_transcript_while_old_rollout_exists', async () => {
      const successor = await waitForCodexTranscript({
        sessionId: nativeSessionId,
        cwd: runner.sessionDir,
        timeoutMs: 45_000,
      });
      runner.assert(successor.filePath !== initialTranscript.filePath, 'successor uses a different rollout', {
        initial: initialTranscript.filePath,
        successor: successor.filePath,
      });
      runner.assert(fs.existsSync(initialTranscript.filePath), 'the old rollout still exists during rebinding', {
        initial: initialTranscript.filePath,
      });
      await waitForCodexAssistantText(successor.filePath, successorReply, 45_000);
      await waitForStoredTranscriptPath(dbPath, sessionId, successor.filePath, 30_000);
      successorCost = await waitForSessionCostAdvance(dbPath, sessionId, initialCost, 30_000);
      for (const [observationId, observation] of Object.entries(initialCost?.observations || {})) {
        runner.assert(
          JSON.stringify(successorCost?.observations?.[observationId]) === JSON.stringify(observation),
          'the successor cost ledger preserves each observation from before /new',
          { observationId, before: observation, after: successorCost?.observations?.[observationId] }
        );
      }
      runner.writeJson('codex-cost-after-new.json', successorCost);
      const stoppedSession = await waitForSessionNotWorking(observer, sessionId, 20_000);
      runner.assert(stoppedSession.state !== 'working', 'the successor Codex turn settles normally', {
        state: stoppedSession.state,
      });
      await captureSessionArtifacts(client, runner.runDir, '02-after-new', sessionId);
      return successor;
    });

    await runner.step('assert_pathless_root_hook_cannot_replace_successor_binding', async () => {
      const expectedResumeId = await queryStoredResumeId(dbPath, sessionId);
      const expectedTranscriptPath = await queryStoredTranscriptPath(dbPath, sessionId);
      const hookOutput = execFileSync(attnBin, ['_hook-session-start', sessionId], {
        env: {
          ...daemonEnv,
          ATTN_AGENT_GUIDANCE: '',
          ATTN_CHIEF_GUIDANCE: '',
        },
        input: JSON.stringify({
          session_id: 'ephemeral-pathless-root',
          transcript_path: null,
          cwd: runner.sessionDir,
        }),
        encoding: 'utf8',
      });
      const hookContext = JSON.parse(hookOutput)?.hookSpecificOutput?.additionalContext || '';
      runner.assert(
        hookContext.includes('`attn delegate` creates a visible agent session') &&
          hookContext.includes('ready now'),
        'a pathless root hook still emits agent and Garden guidance',
        {
          hasAgentGuidance: hookContext.includes('`attn delegate` creates a visible agent session'),
          hasGardenPrime: hookContext.includes('ready now'),
        }
      );
      await delay(500);
      const actualResumeId = await queryStoredResumeId(dbPath, sessionId);
      const actualTranscriptPath = await queryStoredTranscriptPath(dbPath, sessionId);
      runner.assert(
        actualResumeId === expectedResumeId && actualTranscriptPath === expectedTranscriptPath,
        'a pathless root hook cannot replace the path-bearing successor binding',
        { expectedResumeId, actualResumeId, expectedTranscriptPath, actualTranscriptPath }
      );
    });

    await runner.step('reload_session_from_ui_automation', async () => {
      await client.request('reload_session', { sessionId }, { timeoutMs: 45_000 });
      await observer.waitForSession({ id: sessionId, timeoutMs: 20_000 });
    });

    await runner.step('assert_reload_preserves_real_codex_resume_id', async () => {
      const resumeIdAfterReload = await waitForStoredResumeId(dbPath, sessionId, 30_000);
      runner.assert(resumeIdAfterReload === nativeSessionId, 'reload keeps the successor Codex native resume id', {
        nativeSessionId,
        resumeIdAfterReload,
      });
      const reloadedSession = await waitForSessionNotWorking(observer, sessionId, 20_000);
      runner.assert(reloadedSession.state !== 'working', 'reloaded stopped Codex session is not green', {
        state: reloadedSession.state,
      });
      const transcriptAfterReload = await waitForCodexTranscript({
        sessionId: nativeSessionId,
        cwd: runner.sessionDir,
        timeoutMs: 30_000,
      });
      runner.assert(transcriptAfterReload.filePath === transcript.filePath, 'reload points at the successor Codex transcript', {
        before: transcript.filePath,
        after: transcriptAfterReload.filePath,
      });
      await waitForStoredTranscriptPath(dbPath, sessionId, transcript.filePath, 30_000);
      await captureSessionArtifacts(client, runner.runDir, '03-after-reload', sessionId);
    });

    await runner.step('assert_daemon_restart_reuses_persisted_transcript_path', async () => {
      await client.quitApp();
      await observer.close().catch(() => {});
      try {
        execFileSync(attnBin, ['daemon', 'stop'], { env: daemonEnv, encoding: 'utf8' });
      } catch {}
      execFileSync(attnBin, ['daemon', 'ensure'], { env: daemonEnv, encoding: 'utf8' });
      await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
      await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
      await waitForStoredTranscriptPath(dbPath, sessionId, transcript.filePath, 30_000);

      const { stdout } = await execFileAsync(attnBin, ['session', 'transcript', sessionId, '--json'], {
        env: daemonEnv,
        timeout: 20_000,
      });
      runner.assert(stdout.includes(successorReply), 'the restarted daemon reads the successor transcript through its public CLI', {
        transcriptPath: transcript.filePath,
        output: stdout,
      });
      await captureSessionArtifacts(client, runner.runDir, '04-after-daemon-restart', sessionId);
    });

    const summary = await runner.finishSuccess({
      sessionId,
      initialPaneId,
      dbPath,
      initialNativeSessionId,
      nativeSessionId,
      transcriptPath: transcript?.filePath || null,
      costObservationsBeforeNew: sessionCostObservationCount(initialCost),
      costObservationsAfterNew: sessionCostObservationCount(successorCost),
      costTokensBeforeNew: sessionCostTokenTotal(initialCost),
      costTokensAfterNew: sessionCostTokenTotal(successorCost),
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, {
      sessionId,
      initialPaneId,
      dbPath,
      initialNativeSessionId,
      nativeSessionId,
      transcriptPath: transcript?.filePath || null,
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    try {
      await cleanupSessionViaAppClose(client, observer, sessionId);
    } catch {}
    try {
      await client.quitApp();
    } catch {}
    try {
      await observer.close();
    } catch {}
  }
}

main()
  .then(() => {
    process.exit(process.exitCode ?? 0);
  })
  .catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
