#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { parseCommonArgs, printCommonHelp, queryDaemonDb } from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, dataDirForProfile, resolveHarnessResources, profileCliEnv as profileEnv } from './harnessProfile.mjs';
import { ensureFreshWorld } from './freshWorld.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appDaemonInTree } from './platform.mjs';
import { registeredAgentPid } from './workerRegistry.mjs';
import { DaemonObserver } from './daemonObserver.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const SCHEDULE_TICK_INTERVAL = '1s';
const SCHEDULE_CRON = '@every 2s';
// Local run automation-scheduled-cleanup-2026-09-10T20-37-20-445Z: the 2s
// schedule repeated; three periods leave at least two missed instants.
const DOWNTIME_MS = 6_000;
// That run took 1.0s to anchor, 7.1s to restart, 1.6s for cleanup evidence,
// 10.3s to coalesce and 16.0s for the fresh-continuity restart.
const ANCHOR_POLL_TIMEOUT_MS = 30_000;
const RESTART_RUN_TIMEOUT_MS = 45_000;
const CLEANUP_EVIDENCE_TIMEOUT_MS = 45_000;
const COALESCE_TIMEOUT_MS = 30_000;
const CLEANUP_SUMMARY = 'removed merged-clean, kept dirty-wip';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}


function run(binary, args, env, options = {}) {
  return execFileSync(binary, args, {
    encoding: 'utf8',
    env,
    stdio: options.stdio || ['ignore', 'pipe', 'pipe'],
    timeout: options.timeout || 30_000,
  });
}

function runJSON(binary, args, env) {
  return JSON.parse(run(binary, args, env));
}

// `enable`/`disable` are the only way to move the enabled column.
function disableDefinition(binary, id, env) {
  return runJSON(binary, ['automation', 'disable', id], env);
}

function seedForSession(binary, sessionID, env) {
  const listed = runJSON(binary, ['seed', 'ls', '--json'], env) || {};
  return (listed.seeds || []).find((seed) => seed.tender_session === sessionID) || null;
}

function recordedSessionIDs(binary, definitionIDs, env) {
  const sessionIDs = new Set();
  for (const definitionID of definitionIDs) {
    const runs = runJSON(binary, ['automation', 'runs', definitionID], env) || [];
    for (const row of runs) if (row.session_id) sessionIDs.add(row.session_id);
  }
  return sessionIDs;
}

function boundSeedsForSessions(binary, sessionIDs, env) {
  const listed = runJSON(binary, ['seed', 'ls', '--json'], env) || {};
  return (listed.seeds || []).filter((seed) => sessionIDs.has(seed.tender_session));
}

async function poll(fn, description, timeoutMs = 30_000) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(100);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

function sqliteRow(dbPath, sql) {
  const out = queryDaemonDb(dbPath, sql);
  return out.length === 0 ? null : out.split('|');
}

function sqlEscape(value) {
  return value.replaceAll("'", "''");
}

function gitConfigIdentity(repoDir) {
  execFileSync('git', ['config', 'user.name', 'attn'], { cwd: repoDir });
  execFileSync('git', ['config', 'user.email', 'attn@local'], { cwd: repoDir });
}

function createFixture(root) {
  const repo = path.join(root, 'repo');
  const worktrees = path.join(root, 'worktrees');
  fs.mkdirSync(repo, { recursive: true });
  fs.mkdirSync(worktrees, { recursive: true });

  execFileSync('git', ['init', '-q'], { cwd: repo });
  gitConfigIdentity(repo);
// Deterministic default branch whatever the host's init.defaultBranch says.
  execFileSync('git', ['symbolic-ref', 'HEAD', 'refs/heads/main'], { cwd: repo });
  fs.writeFileSync(path.join(repo, 'README.md'), 'Scheduled cleanup fixture.\n');
  execFileSync('git', ['add', 'README.md'], { cwd: repo });
  execFileSync('git', ['commit', '-q', '-m', 'initial'], { cwd: repo });

  // merged-work: fully merged into main, worktree stays clean -> eligible for removal.
  execFileSync('git', ['checkout', '-q', '-b', 'merged-work'], { cwd: repo });
  fs.writeFileSync(path.join(repo, 'merged-work.txt'), 'merged work\n');
  execFileSync('git', ['add', 'merged-work.txt'], { cwd: repo });
  execFileSync('git', ['commit', '-q', '-m', 'merged work'], { cwd: repo });
  execFileSync('git', ['checkout', '-q', 'main'], { cwd: repo });
  execFileSync('git', ['merge', '-q', '--no-ff', 'merged-work', '-m', 'Merge merged-work'], { cwd: repo });

  const mergedClean = path.join(worktrees, 'merged-clean');
  execFileSync('git', ['worktree', 'add', mergedClean, 'merged-work'], { cwd: repo });

  // wip: dirty worktree -> must be preserved regardless of merge status.
  execFileSync('git', ['branch', 'wip', 'main'], { cwd: repo });
  const dirtyWip = path.join(worktrees, 'dirty-wip');
  execFileSync('git', ['worktree', 'add', dirtyWip, 'wip'], { cwd: repo });
  fs.writeFileSync(path.join(dirtyWip, 'scratch.txt'), 'uncommitted work in progress\n');

  return { repo, worktrees, mergedClean, dirtyWip };
}

function writeCleanupFixture(root, fixture) {
  writeMockAgentFixture(root, {
    name: 'scheduled cleanup',
    turns: [{
      includes: 'Review git worktrees',
      actions: [
        { type: 'exec', cwd: fixture.repo, cmd: 'git', args: ['worktree', 'remove', fixture.mergedClean], allowFailure: true },
        { type: 'exec', cwd: fixture.repo, cmd: 'git', args: ['branch', '-d', 'merged-work'], allowFailure: true },
        { type: 'capture', from: 'prompt', pattern: 'Your assignment is in seed `(s-[a-z0-9]{6})`', name: 'seed' },
        { type: 'attn', args: ['seed', 'note', '{{seed}}', '-m', CLEANUP_SUMMARY], state: 'idle' },
      ],
    }],
  });
}

function worktreeListShows(repo, absolutePath) {
  const out = execFileSync('git', ['worktree', 'list', '--porcelain'], { cwd: repo, encoding: 'utf8' });
  return out.includes(absolutePath);
}

function createCodexProbe(root) {
  const log = path.join(root, 'codex-invocations.jsonl');
  const executable = path.join(root, 'codex-probe.mjs');
  fs.writeFileSync(
    executable,
    `#!/usr/bin/env node\nimport fs from 'node:fs';\nfs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({argv: process.argv.slice(2), at: new Date().toISOString()}) + '\\n');\nsetInterval(() => {}, 1000);\n`,
    { mode: 0o700 },
  );
  return { executable, log };
}

function invocations(log) {
  if (!fs.existsSync(log)) return [];
  return fs.readFileSync(log, 'utf8').trim().split('\n').filter(Boolean).map((line) => JSON.parse(line));
}

const API_VERSION = 'attn.dev/automations/v1alpha1';

// `enabled` is a column, not a spec field: a YAML carrying `enabled:` is
// rejected outright (errEnabledManagedOutsideSpec).
function cleanupDefinitionYAML({ id, locationPath }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 5 packaged scheduled cleanup proof
trigger:
  type: scheduled
  schedule:
    cron: ${JSON.stringify(SCHEDULE_CRON)}
    time_zone: UTC
  continuity: singleton
  catch_up: latest
prompt: |
  Review git worktrees of \`repo/\`; remove with \`git worktree remove\` (never --force) each linked worktree whose branch is fully merged into main AND whose tree is completely clean, then delete that fully-merged branch with \`git branch -d\`. NEVER remove a worktree with staged, unstaged, or untracked changes — list preserved worktrees with reasons. Summarize actions in the ticket.
launch:
  driver: codex
  effort: medium
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

function stormGuardDefinitionYAML({ id, locationPath, executable }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 5 scheduler storm-guard probe
trigger:
  type: scheduled
  schedule:
    cron: ${JSON.stringify(SCHEDULE_CRON)}
    time_zone: UTC
  continuity: fresh
  catch_up: latest
prompt: |
  Scheduler storm-guard probe. Do nothing; this executable is a test double.
launch:
  driver: codex
  executable: ${JSON.stringify(executable)}
  model: slice5-storm-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

// The tick AFTER the anchor tick legitimately fires a run, so a fixed sleep
// races it. Poll for the cursor row the anchor tick writes instead.
async function waitForScheduleAnchor(dbPath, definitionID) {
  await poll(
    () => sqliteRow(dbPath, `SELECT observed_at FROM automation_provider_cursors WHERE definition_id='${sqlEscape(definitionID)}' AND provider='schedule' AND scope='*';`),
    `schedule cursor anchor for ${definitionID}`,
    ANCHOR_POLL_TIMEOUT_MS,
  );
}

async function waitForDaemonReady(binary, daemonEnv) {
  await poll(() => {
    try {
      runJSON(binary, ['automation', 'list'], daemonEnv);
      return { ready: true };
    } catch {
      return null;
    }
  }, 'profile daemon');
}

async function waitForRegisteredAgentExit(dataDir, sessionID, cwd) {
  await poll(
    () => (registeredAgentPid(dataDir, sessionID, cwd) === null ? true : null),
    `registered agent for session ${sessionID} to exit`,
    RESTART_RUN_TIMEOUT_MS,
  );
}

async function closeDeliveredSessions(wsUrl, dataDir, cwd, sessionIDs) {
  const targets = new Set(sessionIDs.filter(Boolean));
  if (targets.size === 0) return [];

  const observer = new DaemonObserver({ wsUrl });
  await observer.connect();
  try {
    const closed = await observer.unregisterMatchingSessions((session) => targets.has(session.id));
    for (const sessionID of targets) await waitForRegisteredAgentExit(dataDir, sessionID, cwd);
    return closed;
  } finally {
    await observer.close();
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-automation-scheduled-cleanup.mjs');
    return;
  }
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('automation scheduled-cleanup scenario requires a named non-production profile');
  const resources = resolveHarnessResources(profile);
  const binary = appDaemonInTree(resources.appPath);
  const dbPath = path.join(dataDirForProfile(profile), 'attn.db');
  const runner = createScenarioRunner(options, {
    scenarioId: 'AUTOMATION-SCHEDULED-CLEANUP',
    allowRealAgents: false,
    tier: 'tier2-local',
    prefix: 'automation-scheduled-cleanup',
    metadata: {
      profile,
      provider: 'local fixture repo',
      legTwo: 'delivery proof: the scheduled brief reaches a launched agent that does the real git work; no model judges what to remove',
      legFour: 'storm-guard re-assertion (fresh continuity); skip-discard-beyond-grace covered by unit tests',
    },
  });

  const suffix = Date.now().toString(36);
  const cleanupID = `scheduled-cleanup-${suffix}`;
  const stormGuardID = `scheduled-storm-guard-${suffix}`;
  const fixtureRoot = fs.realpathSync(fs.mkdtempSync(path.join(runner.sessionDir, 'scheduled-cleanup-')));
  const cleanupDefinitionFile = path.join(runner.sessionDir, 'scheduled-cleanup.yml');
  const stormGuardDefinitionFile = path.join(runner.sessionDir, 'scheduled-storm-guard.yml');

  let daemonEnv = null;
  let fixture = null;
  let probe = null;
  let cleanupTicketID = '';
  let cleanupSeedID = '';
  let stormGuardSeedID = '';
  let cleanupSessionID = '';
  let stormGuardSessionID = '';
  let cleanupApplied = false;
  let stormGuardApplied = false;
  let sessionsClosed = false;
  let cleanupSeedSettled = false;
  let stormGuardSeedSettled = false;

  try {
    daemonEnv = profileEnv(profile);
    fixture = createFixture(fixtureRoot);
    writeCleanupFixture(fixtureRoot, fixture);
    probe = createCodexProbe(runner.sessionDir);

    await runner.step('restart_isolated_daemon', async () => {
      await ensureFreshWorld({ profile, appPath: resources.appPath });
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      daemonEnv.ATTN_AUTOMATION_SCHEDULE_INTERVAL = SCHEDULE_TICK_INTERVAL;
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
    });

    await runner.step('leg1_apply_and_anchor', async () => {
      fs.writeFileSync(cleanupDefinitionFile, cleanupDefinitionYAML({ id: cleanupID, locationPath: fixtureRoot }));
      runJSON(binary, ['automation', 'apply', '--file', cleanupDefinitionFile], daemonEnv);
      cleanupApplied = true;
      await waitForScheduleAnchor(dbPath, cleanupID);
      const rows = runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || [];
      runner.assert(rows.length === 0, 'no run fires on the anchor-only tick', { rows });
    });

    await runner.step('leg1_restart_catchup', async () => {
      run(binary, ['daemon', 'stop'], daemonEnv);
      await delay(DOWNTIME_MS);
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
      const rows = await poll(() => {
        const list = runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || [];
        return list.length >= 1 ? list : null;
      }, 'restart catch-up run', RESTART_RUN_TIMEOUT_MS);
      runner.assert(rows.length === 1, 'exactly one catch-up run fires despite multiple missed instants (latest policy)', { rows });
      const runRow = rows[0];
      cleanupTicketID = runRow.ticket_id;
      cleanupSessionID = runRow.session_id;
      runner.assert(Boolean(cleanupTicketID) && Boolean(cleanupSessionID), 'catch-up run reserves a ticket and session', runRow);

      const occurrence = sqliteRow(
        dbPath,
        `SELECT o.occurrence_key FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE r.id='${sqlEscape(runRow.id)}';`,
      );
      runner.assert(occurrence !== null, 'catch-up run has a resolvable occurrence row', { runID: runRow.id });
      runner.assert(occurrence[0].startsWith('scheduled:'), 'occurrence key carries the scheduled prefix', { occurrenceKey: occurrence[0] });
    });

    await runner.step('leg2_cleanup_evidence', async () => {
      await poll(() => {
        const removedFromDisk = !fs.existsSync(fixture.mergedClean);
        const removedFromGit = !worktreeListShows(fixture.repo, fixture.mergedClean);
        return removedFromDisk && removedFromGit ? true : null;
      }, 'merged-clean worktree removed by the agent', CLEANUP_EVIDENCE_TIMEOUT_MS);
      runner.assert(!fs.existsSync(fixture.mergedClean), 'merged-clean worktree directory is gone from disk');
      runner.assert(!worktreeListShows(fixture.repo, fixture.mergedClean), 'merged-clean worktree is untracked by git worktree list');
      runner.assert(fs.existsSync(fixture.dirtyWip), 'dirty-wip worktree directory is preserved');
      runner.assert(fs.existsSync(path.join(fixture.dirtyWip, 'scratch.txt')), 'dirty-wip uncommitted file is untouched');
      runner.assert(worktreeListShows(fixture.repo, fixture.dirtyWip), 'dirty-wip worktree is still tracked by git worktree list');

      const ticket = sqliteRow(
        dbPath,
        `SELECT status FROM tickets WHERE id='${sqlEscape(cleanupTicketID)}';`,
      );
      runner.assert(ticket !== null, 'cleanup ticket row exists', { cleanupTicketID });
      runner.assert(ticket[0] !== 'failed', 'cleanup ticket did not fail', { ticketStatus: ticket[0] });

      const reported = await poll(() => {
        const listed = runJSON(binary, ['seed', 'ls', '--json'], daemonEnv) || {};
        const seed = (listed.seeds || []).find((row) => row.tender_session === cleanupSessionID);
        if (!seed) return null;
        const shown = runJSON(binary, ['seed', 'show', seed.id, '--json'], daemonEnv) || {};
        const notes = (shown.notes || []).map((note) => note.body);
        return notes.some((body) => body.includes(CLEANUP_SUMMARY)) ? { seed: seed.id, notes } : null;
      }, 'the automation seed carrying the cleanup summary', CLEANUP_EVIDENCE_TIMEOUT_MS);
      runner.assert(
        Boolean(reported),
        'the launched agent reported its cleanup on the seed it tends',
        reported,
      );
      cleanupSeedID = reported.seed;
    });

    await runner.step('leg3_singleton_coalescing', async () => {
      // Poll rather than a fixed sleep so a fast leg 2 still gets one more tick.
      const rows = await poll(() => {
        const list = runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || [];
        return list.length >= 2 ? list : null;
      }, 'a second coalesced occurrence', COALESCE_TIMEOUT_MS);
      runner.assert(rows.length >= 2, 'at least a second occurrence fired while enabled', { count: rows.length });
      const tickets = new Set(rows.map((row) => row.ticket_id));
      const sessions = new Set(rows.map((row) => row.session_id));
      runner.assert(
        tickets.size === 1 && tickets.has(cleanupTicketID),
        'every occurrence for this definition coalesces onto the same singleton ticket',
        { tickets: [...tickets] },
      );
      runner.assert(
        sessions.size === 1 && sessions.has(cleanupSessionID),
        'every occurrence reuses the same live session; no duplicate session is spawned',
        { sessions: [...sessions] },
      );

      runner.assert(fs.existsSync(fixture.dirtyWip), 'dirty-wip worktree directory is still preserved after coalescing');
      runner.assert(fs.existsSync(path.join(fixture.dirtyWip, 'scratch.txt')), 'dirty-wip uncommitted file is still untouched after coalescing');
      runner.assert(worktreeListShows(fixture.repo, fixture.dirtyWip), 'dirty-wip worktree is still tracked by git worktree list after coalescing');

      disableDefinition(binary, cleanupID, daemonEnv);
    });

    // Skip-vs-latest discard past the 5-minute grace would push this past ~15
    // minutes of wall clock; it lives in automations_schedule_test.go instead.
    await runner.step('leg4_storm_guard_restart', async () => {
      fs.writeFileSync(
        stormGuardDefinitionFile,
        stormGuardDefinitionYAML({ id: stormGuardID, locationPath: fixtureRoot, executable: probe.executable }),
      );
      runJSON(binary, ['automation', 'apply', '--file', stormGuardDefinitionFile], daemonEnv);
      stormGuardApplied = true;
      await waitForScheduleAnchor(dbPath, stormGuardID);
      const anchoredRows = runJSON(binary, ['automation', 'runs', stormGuardID], daemonEnv) || [];
      runner.assert(anchoredRows.length === 0, 'storm-guard probe does not fire on its anchor-only tick', { anchoredRows });

      run(binary, ['daemon', 'stop'], daemonEnv);
      await delay(DOWNTIME_MS);
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);

      const claimed = await poll(() => {
        const list = runJSON(binary, ['automation', 'runs', stormGuardID], daemonEnv) || [];
        return list.length >= 1 ? list[0] : null;
      }, 'storm-guard restart catch-up run claimed', RESTART_RUN_TIMEOUT_MS);
      stormGuardSessionID = claimed.session_id;
      disableDefinition(binary, stormGuardID, daemonEnv);

      const delivered = await poll(() => {
        const list = runJSON(binary, ['automation', 'runs', stormGuardID], daemonEnv) || [];
        return list[0]?.state === 'delivered' ? list : null;
      }, 'storm-guard restart catch-up run delivered', RESTART_RUN_TIMEOUT_MS);
      runner.assert(delivered.length === 1, 'storm-guard: exactly one catch-up run under fresh continuity too', { rows: delivered });
      const stormGuardSeed = seedForSession(binary, stormGuardSessionID, daemonEnv);
      runner.assert(Boolean(stormGuardSeed), 'storm-guard run owns a synthetic seed', { stormGuardSessionID, stormGuardSeed });
      stormGuardSeedID = stormGuardSeed.id;

      await poll(() => (invocations(probe.log).length >= 1 ? invocations(probe.log) : null), 'storm-guard probe launch');
      runner.assert(invocations(probe.log).length === 1, 'exactly one process spawn backs the single catch-up run (no replay storm)', {
        invocations: invocations(probe.log),
      });
    });

    await runner.step('cleanup_and_restart_receipt', async () => {
      const sessionIDs = recordedSessionIDs(binary, [cleanupID, stormGuardID], daemonEnv);
      runner.assert(
        sessionIDs.size === 2 && sessionIDs.has(cleanupSessionID) && sessionIDs.has(stormGuardSessionID),
        'the two fixture definitions own the expected delivered sessions',
        { sessionIDs: [...sessionIDs], cleanupSessionID, stormGuardSessionID },
      );
      const boundSeeds = boundSeedsForSessions(binary, sessionIDs, daemonEnv);
      runner.assert(
        boundSeeds.length === 2 && boundSeeds.some((seed) => seed.id === cleanupSeedID) && boundSeeds.some((seed) => seed.id === stormGuardSeedID),
        'the delivered sessions own the expected two synthetic seeds',
        { boundSeeds, cleanupSeedID, stormGuardSeedID },
      );
      const closed = await closeDeliveredSessions(
        options.wsUrl,
        dataDirForProfile(profile),
        fixtureRoot,
        [...sessionIDs],
      );
      runner.assert(closed.length === sessionIDs.size, 'both delivered automation sessions close intentionally', {
        expected: [...sessionIDs],
        closed: closed.map((session) => session.id),
      });
      sessionsClosed = true;
      for (const seed of boundSeeds) {
        run(binary, ['seed', 'wither', seed.id, '-m', 'Scheduled automation harness fixture complete'], daemonEnv);
        if (seed.id === cleanupSeedID) cleanupSeedSettled = true;
        if (seed.id === stormGuardSeedID) stormGuardSeedSettled = true;
      }

      run(binary, ['automation', 'delete', cleanupID], daemonEnv);
      cleanupApplied = false;
      run(binary, ['automation', 'delete', stormGuardID], daemonEnv);
      stormGuardApplied = false;

      run(binary, ['daemon', 'stop'], daemonEnv);
      daemonEnv = profileEnv(profile);
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);

      const definitions = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(
        !definitions.some((definition) => definition.id === cleanupID || definition.id === stormGuardID),
        'the timestamped definitions remain deleted after daemon restart',
        { definitions, cleanupID, stormGuardID },
      );
      const sessions = runJSON(binary, ['agent', 'list', '--json'], daemonEnv) || [];
      runner.assert(
        !sessions.some((session) => sessionIDs.has(session.id)),
        'the delivered sessions remain closed after daemon restart',
        { sessions, sessionIDs: [...sessionIDs] },
      );
      const cleanupSeed = runJSON(binary, ['seed', 'show', cleanupSeedID, '--json'], daemonEnv)?.seed;
      const stormGuardSeed = runJSON(binary, ['seed', 'show', stormGuardSeedID, '--json'], daemonEnv)?.seed;
      runner.assert(
        cleanupSeed?.status === 'withered' && stormGuardSeed?.status === 'withered',
        'the synthetic automation seeds remain withered after daemon restart',
        { cleanupSeedID, cleanupSeed, stormGuardSeedID, stormGuardSeed },
      );
    });

    await runner.finishSuccess({ profile, cleanupID, stormGuardID, cleanupTicketID, cleanupSessionID, fixtureRoot });
  } catch (error) {
    await runner.finishFailure(error, { profile, cleanupID, stormGuardID, cleanupTicketID, cleanupSessionID, fixtureRoot });
    throw error;
  } finally {
    const teardownNeeded = cleanupApplied || stormGuardApplied || !sessionsClosed || !cleanupSeedSettled || !stormGuardSeedSettled;
    const teardownSessionIDs = new Set([cleanupSessionID, stormGuardSessionID].filter(Boolean));
    const teardownSeedIDs = new Set();
    if (daemonEnv && teardownNeeded) {
      try {
        run(binary, ['daemon', 'ensure'], daemonEnv);
        await waitForDaemonReady(binary, daemonEnv);
        const appliedDefinitionIDs = [];
        if (cleanupApplied) appliedDefinitionIDs.push(cleanupID);
        if (stormGuardApplied) appliedDefinitionIDs.push(stormGuardID);
        for (const definitionID of appliedDefinitionIDs) disableDefinition(binary, definitionID, daemonEnv);
        for (const sessionID of recordedSessionIDs(binary, appliedDefinitionIDs, daemonEnv)) teardownSessionIDs.add(sessionID);
        for (const seed of boundSeedsForSessions(binary, teardownSessionIDs, daemonEnv)) teardownSeedIDs.add(seed.id);
      } catch {}
    }
    if (!sessionsClosed) {
      try {
        await closeDeliveredSessions(
          options.wsUrl,
          dataDirForProfile(profile),
          fixtureRoot,
          [...teardownSessionIDs],
        );
      } catch {}
    }
    if (!cleanupSeedSettled && cleanupSeedID) teardownSeedIDs.add(cleanupSeedID);
    if (!stormGuardSeedSettled && stormGuardSeedID) teardownSeedIDs.add(stormGuardSeedID);
    if (daemonEnv) for (const seedID of teardownSeedIDs) {
      try { run(binary, ['seed', 'wither', seedID, '-m', 'Scheduled automation harness fixture complete'], daemonEnv); } catch {}
    }
    if (daemonEnv) {
      if (cleanupApplied) { try { run(binary, ['automation', 'delete', cleanupID], daemonEnv); } catch {} }
      if (stormGuardApplied) { try { run(binary, ['automation', 'delete', stormGuardID], daemonEnv); } catch {} }
    }
    try {
      const transcripts = path.join(fixtureRoot, '.attn-mock-agent');
      for (const name of fs.readdirSync(transcripts)) {
        runner.writeText(`mock-${name}`, fs.readFileSync(path.join(transcripts, name), 'utf8'));
      }
    } catch {}
    try { fs.rmSync(fixtureRoot, { recursive: true, force: true }); } catch {}
    try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
    try { run(binary, ['daemon', 'ensure'], profileEnv(profile)); } catch {}
    await runner.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
