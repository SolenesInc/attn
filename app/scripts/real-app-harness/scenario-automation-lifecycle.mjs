#!/usr/bin/env node

// Run serially (packaged-app scenarios are single-tenant):
//   ATTN_HARNESS_PROFILE=<name> node scripts/real-app-harness/scenario-automation-lifecycle.mjs

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync, spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import WebSocket from 'ws';
import { parseCommonArgs, printCommonHelp, launchFreshAppAndConnect } from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import {
  currentHarnessProfile,
  dataDirForProfile,
  harnessClientHello,
  resolveHarnessResources,
  profileCliEnv as profileEnv,
} from './harnessProfile.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import { readFrontendProtocolVersion } from './presentDaemon.mjs';
import { appDaemonInTree } from './platform.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HERE, '../../..');
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// The daemon's automation-schedule ticker fires once a real minute; the
// budgets below are that cadence plus margin.
const ANCHOR_POLL_TIMEOUT_MS = 90_000; // the anchor tick lands within one 60s ticker interval of apply, plus margin.
const SCHEDULE_RUN_TIMEOUT_MS = 90_000; // one more live tick after apply/edit; the cursor is not re-anchored by an edit.
const PANEL_TIMEOUT_MS = 30_000;
const RUN_DELIVERED_TIMEOUT_MS = 45_000;
const GH_DELIVERY_TIMEOUT_MS = 60_000;

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

function disableDefinition(binary, id, env) {
  return runJSON(binary, ['automation', 'disable', id], env);
}

async function poll(fn, description, timeoutMs = 30_000) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(150);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

function sqliteRow(dbPath, sql) {
  const out = execFileSync('sqlite3', ['-cmd', '.timeout 5000', dbPath, sql], { encoding: 'utf8' }).trim();
  return out.length === 0 ? null : out.split('|');
}

function sqlEscape(value) {
  return value.replaceAll("'", "''");
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

// The tick after the anchor tick legitimately fires a run, so asserting "no
// run yet" after a fixed sleep races it. Poll for the anchor cursor instead.
async function waitForScheduleAnchor(dbPath, definitionID) {
  await poll(
    () => sqliteRow(dbPath, `SELECT observed_at FROM automation_provider_cursors WHERE definition_id='${sqlEscape(definitionID)}' AND provider='schedule' AND scope='*';`),
    `schedule cursor anchor for ${definitionID}`,
    ANCHOR_POLL_TIMEOUT_MS,
  );
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


const API_VERSION = 'attn.dev/automations/v1alpha1';

// `enabled` is not a spec field: a YAML carrying it is rejected outright
// (errEnabledManagedOutsideSpec).
function editRebindDefinitionYAML({ id, locationPath, executable, prompt }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 7 packaged edit-rebind proof
trigger:
  type: scheduled
  schedule:
    cron: "* * * * *"
    time_zone: UTC
  continuity: singleton
  catch_up: latest
prompt: |
  ${prompt}
launch:
  driver: codex
  executable: ${JSON.stringify(executable)}
  model: slice7-edit-rebind-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

function deleteResurrectDefinitionYAML({ id, locationPath, executable }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 7 packaged delete-resurrect proof
trigger:
  type: manual
prompt: |
  Slice 7 packaged delete-resurrect proof. Do nothing; this executable is a test double.
launch:
  driver: codex
  executable: ${JSON.stringify(executable)}
  model: slice7-delete-resurrect-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

const CLEANUP_IDENTITY = 'mock.github.local/owner/repo';

function cleanupLifecycleDefinitionYAML({ id, executable, repoPath, prompt }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 7 packaged cleanup-dirty-safe proof
trigger:
  type: github_review_requested
  repositories:
    mode: all_accessible
    include: [${CLEANUP_IDENTITY}]
prompt: |
  ${prompt}
launch:
  driver: codex
  executable: ${JSON.stringify(executable)}
  model: slice7-cleanup-probe
  effort: high
location:
  type: repository_worktree
  repository_sources:
    default: {type: managed_cache}
    overrides:
      ${CLEANUP_IDENTITY}:
        type: local_clone
        path: ${JSON.stringify(repoPath)}
`;
}


function createCleanupFixture(root) {
  const repo = path.join(root, 'cleanup-fixture-repo');
  fs.mkdirSync(repo, { recursive: true });
  execFileSync('git', ['init', '-q'], { cwd: repo });
  execFileSync('git', ['remote', 'add', 'origin', `https://${CLEANUP_IDENTITY}.git`], { cwd: repo });
  fs.writeFileSync(path.join(repo, 'README.md'), 'Slice 7 cleanup-dirty-safe fixture.\n');
  execFileSync('git', ['add', 'README.md'], { cwd: repo });
  execFileSync('git', ['commit', '-q', '-m', 'fixture'], {
    cwd: repo,
    env: {
      ...process.env,
      GIT_AUTHOR_NAME: 'attn',
      GIT_AUTHOR_EMAIL: 'attn@local',
      GIT_COMMITTER_NAME: 'attn',
      GIT_COMMITTER_EMAIL: 'attn@local',
    },
  });
  return { repo, sha: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim() };
}

async function startMock(sha) {
  const child = spawn(process.execPath, [path.join(REPO_ROOT, 'scripts/automation-mock-github.mjs')], {
    cwd: REPO_ROOT,
    env: { ...process.env, ATTN_AUTOMATION_MOCK_SHA: sha },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  const started = Date.now();
  while (!stdout.includes('\n') && Date.now() - started < 10_000) {
    if (child.exitCode !== null) throw new Error(`mock GitHub exited: ${stderr}`);
    await delay(25);
  }
  if (!stdout.includes('\n')) throw new Error(`mock GitHub did not start: ${stderr}`);
  return { child, ...JSON.parse(stdout.split('\n', 1)[0]) };
}

async function setRequested(mockURL, active) {
  const response = await fetch(`${mockURL}/__control/requested`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ active }),
  });
  if (!response.ok) throw new Error(`mock control returned ${response.status}`);
}

async function wsRequest(wsURL, message, event, timeoutMs = 30_000) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsURL);
    const timer = setTimeout(() => { ws.close(); reject(new Error(`timed out waiting for ${event}`)); }, timeoutMs);
    let ready = false;
    ws.once('open', () => ws.send(JSON.stringify({
      ...harnessClientHello('harness-automation-lifecycle', {
        version: `protocol-${readFrontendProtocolVersion()}`,
      }),
    })));
    ws.on('message', (raw) => {
      const value = JSON.parse(raw.toString());
      if (!ready && value.event === 'initial_state') {
        ready = true;
        ws.send(JSON.stringify(message));
        return;
      }
      if (event ? value.event !== event : value.ok !== true) return;
      clearTimeout(timer);
      ws.close();
      if (value.success === false) reject(new Error(value.error || `${event} failed`));
      else resolve(value);
    });
    ws.once('error', (error) => { clearTimeout(timer); reject(error); });
  });
}

function worktreeListShows(repo, absolutePath) {
  const out = execFileSync('git', ['worktree', 'list', '--porcelain'], { cwd: repo, encoding: 'utf8' });
  return out.includes(absolutePath);
}

function resolveWorktree(observer, profile, sessionID) {
  return observer.getSession(sessionID)?.directory
    || path.join(dataDirForProfile(profile), 'automation', 'worktrees', sessionID, 'repo');
}

async function waitSessionGone(observer, sessionID, description) {
  await observer.waitFor(() => (!observer.getSession(sessionID) ? true : null), description);
}


function findDefinitionRow(state, definitionId) {
  return (state?.definitions || []).find((row) => row.id === definitionId) || null;
}

async function captureFailureEvidence(runner, client) {
  try {
    const state = await client.request('automations_get_state');
    runner.writeJson('failure-automations-state.json', state);
  } catch (error) {
    runner.log('failure_evidence_state_error', { error: error instanceof Error ? error.message : String(error) });
  }
  try {
    await captureScreenshotData(path.join(runner.runDir, 'failure.png'), { client });
  } catch (error) {
    runner.log('failure_evidence_screenshot_error', { error: error instanceof Error ? error.message : String(error) });
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-automation-lifecycle.mjs');
    return;
  }
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('automation lifecycle scenario requires a named non-production profile');
  const resources = resolveHarnessResources(profile);
  const binary = appDaemonInTree(resources.appPath);
  const dbPath = path.join(dataDirForProfile(profile), 'attn.db');
  const runner = createScenarioRunner(options, {
    scenarioId: 'AUTOMATION-LIFECYCLE',
    tier: 'tier2-local-fake-agent',
    prefix: 'automation-lifecycle',
    metadata: { profile, excludes: 'A3 retention (time-based policy, unit-tested only)' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const suffix = Date.now().toString(36);
  const editID = `automation-lifecycle-edit-${suffix}`;
  const deleteID = `automation-lifecycle-delete-${suffix}`;
  const cleanupID = `automation-lifecycle-cleanup-${suffix}`;
  const editDefinitionFile = path.join(runner.sessionDir, 'edit-rebind.yml');
  const deleteDefinitionFile = path.join(runner.sessionDir, 'delete-resurrect.yml');
  const cleanupDefinitionFile = path.join(runner.sessionDir, 'cleanup-dirty-safe.yml');

  const PROMPT_P1 = 'Edit-rebind proof P1: initial contract.';
  const PROMPT_P2 = 'Edit-rebind proof P2: edited contract.';
  const CLEANUP_PROMPT_V1 = 'Cleanup-dirty-safe proof v1: initial contract.';
  const CLEANUP_PROMPT_V2 = 'Cleanup-dirty-safe proof v2: edited contract.';
  const CLEANUP_PROMPT_V3 = 'Cleanup-dirty-safe proof v3: edited contract again.';

  let daemonEnv = null;
  let mock = null;
  let probe = null;
  let editFixture = null;
  let deleteFixture = null;
  let cleanupFixture = null;
  let editApplied = false;
  let deleteApplied = false;
  let cleanupApplied = false;

  try {
    await runner.step('setup_fixtures', async () => {
      editFixture = fs.realpathSync(fs.mkdtempSync(path.join(runner.sessionDir, 'edit-rebind-')));
      deleteFixture = fs.realpathSync(fs.mkdtempSync(path.join(runner.sessionDir, 'delete-resurrect-')));
      cleanupFixture = createCleanupFixture(runner.sessionDir);
      probe = createCodexProbe(runner.sessionDir);
      mock = await startMock(cleanupFixture.sha);
    });

    await runner.step('restart_isolated_daemon', async () => {
      daemonEnv = profileEnv(profile, {
        ATTN_MOCK_GH_URL: mock.url,
        ATTN_MOCK_GH_HOST: mock.host,
        ATTN_MOCK_GH_TOKEN: 'test-token',
      });
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
    });

    await runner.step('launch_packaged_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    let run1 = null;
    let run2 = null;
    let run3 = null;
    await runner.step('leg1_edit_rebind', async () => {
      fs.writeFileSync(editDefinitionFile, editRebindDefinitionYAML({ id: editID, locationPath: editFixture, executable: probe.executable, prompt: PROMPT_P1 }));
      const created = runJSON(binary, ['automation', 'apply', '--file', editDefinitionFile], daemonEnv);
      editApplied = true;
      runner.assert(created && created.enabled === true, 'sanity: a brand-new definition is inserted enabled by default', created);
      await waitForScheduleAnchor(dbPath, editID);
      const anchoredRows = runJSON(binary, ['automation', 'runs', editID], daemonEnv) || [];
      runner.assert(anchoredRows.length === 0, 'no run fires on the anchor-only tick', { anchoredRows });

      run1 = await poll(() => {
        const rows = (runJSON(binary, ['automation', 'runs', editID], daemonEnv) || []).filter((row) => row.state === 'delivered');
        return rows.length >= 1 ? rows[0] : null;
      }, 'P1 initial delivery', SCHEDULE_RUN_TIMEOUT_MS);
      runner.assert(Boolean(run1.ticket_id) && Boolean(run1.session_id), 'P1 delivery reserves a ticket and session', run1);
      runner.assert(!run1.last_error, 'P1 delivery has no error', run1);

      fs.writeFileSync(editDefinitionFile, editRebindDefinitionYAML({ id: editID, locationPath: editFixture, executable: probe.executable, prompt: PROMPT_P2 }));
      runJSON(binary, ['automation', 'apply', '--file', editDefinitionFile], daemonEnv);

      run2 = await poll(() => {
        const rows = (runJSON(binary, ['automation', 'runs', editID], daemonEnv) || []).filter((row) => row.state === 'delivered' && row.ticket_id !== run1.ticket_id);
        return rows.length >= 1 ? rows[0] : null;
      }, 'P2 edit delivery on a fresh thread', SCHEDULE_RUN_TIMEOUT_MS);
      runner.assert(!run2.last_error, 'P2 edit delivery succeeds; no "contract changed" refusal', run2);
      runner.assert(run2.ticket_id !== run1.ticket_id && run2.session_id !== run1.session_id, 'P2 edit delivery reserves a fresh ticket and session', { run1, run2 });

      const run1AfterEdit = (runJSON(binary, ['automation', 'runs', editID], daemonEnv) || []).find((row) => row.id === run1.id);
      runner.assert(
        run1AfterEdit && run1AfterEdit.state === run1.state && run1AfterEdit.ticket_id === run1.ticket_id && run1AfterEdit.session_id === run1.session_id,
        "P1's original run row is unchanged after the P2 edit",
        { before: run1, after: run1AfterEdit },
      );

      fs.writeFileSync(editDefinitionFile, editRebindDefinitionYAML({ id: editID, locationPath: editFixture, executable: probe.executable, prompt: PROMPT_P1 }));
      runJSON(binary, ['automation', 'apply', '--file', editDefinitionFile], daemonEnv);

      run3 = await poll(() => {
        const rows = (runJSON(binary, ['automation', 'runs', editID], daemonEnv) || []).filter(
          (row) => row.state === 'delivered' && row.ticket_id !== run1.ticket_id && row.ticket_id !== run2.ticket_id,
        );
        return rows.length >= 1 ? rows[0] : null;
      }, 'P1 revert delivery on yet another fresh thread (the A1-fix, live)', SCHEDULE_RUN_TIMEOUT_MS);
      runner.assert(!run3.last_error, 'P1 revert delivery succeeds; the revert edge does not brick delivery', run3);
      runner.assert(
        run3.ticket_id !== run1.ticket_id && run3.ticket_id !== run2.ticket_id,
        'P1 revert delivery reserves a third distinct thread even though its contract matches run1 exactly',
        { run1, run2, run3 },
      );

      disableDefinition(binary, editID, daemonEnv);
    });

    let deleteRunID = '';
    await runner.step('leg2_delete_resurrect', async () => {
      fs.writeFileSync(deleteDefinitionFile, deleteResurrectDefinitionYAML({ id: deleteID, locationPath: deleteFixture, executable: probe.executable }));
      runJSON(binary, ['automation', 'apply', '--file', deleteDefinitionFile], daemonEnv);
      deleteApplied = true;

      await client.request('automations_open_panel');
      await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, deleteID) ? current : null;
      }, `delete-resurrect definition ${deleteID} to appear in the panel`, PANEL_TIMEOUT_MS);

      await client.request('automations_select_definition', { definitionId: deleteID });
      await client.request('automations_run_now', { definitionId: deleteID });
      const delivered = await poll(async () => {
        const current = await client.request('automations_get_state');
        const runs = (current?.runs || []).filter((r) => r.state === 'delivered');
        return runs.length >= 1 ? runs[0] : null;
      }, 'delete-resurrect run-now to reach delivered', RUN_DELIVERED_TIMEOUT_MS);
      deleteRunID = delivered.id;

      run(binary, ['automation', 'delete', deleteID], daemonEnv);

      await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, deleteID) ? null : current;
      }, 'deleted definition to disappear from the panel', PANEL_TIMEOUT_MS);

      const runsAfterDelete = runJSON(binary, ['automation', 'runs', deleteID], daemonEnv) || [];
      runner.assert(
        runsAfterDelete.some((row) => row.id === deleteRunID),
        'runs remain queryable via the CLI after delete',
        runsAfterDelete,
      );
      // sqlite3 prints a lone empty string as an empty line, which sqliteRow cannot
      // tell apart from "no row" — so select id alongside it.
      const deletedRow = sqliteRow(dbPath, `SELECT id, deleted_at FROM automation_definitions WHERE id='${sqlEscape(deleteID)}';`);
      runner.assert(deletedRow && deletedRow[1] !== '' && deletedRow[1] !== undefined, 'the definition row is soft-deleted (deleted_at set) in the DB', { deletedRow });

      fs.writeFileSync(deleteDefinitionFile, deleteResurrectDefinitionYAML({ id: deleteID, locationPath: deleteFixture, executable: probe.executable }));
      runJSON(binary, ['automation', 'apply', '--file', deleteDefinitionFile], daemonEnv);

      await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, deleteID) ? current : null;
      }, 'resurrected definition to reappear in the panel', PANEL_TIMEOUT_MS);

      const runsAfterResurrect = runJSON(binary, ['automation', 'runs', deleteID], daemonEnv) || [];
      runner.assert(
        runsAfterResurrect.some((row) => row.id === deleteRunID),
        'old run history survives resurrection',
        runsAfterResurrect,
      );
      const resurrectedRow = sqliteRow(dbPath, `SELECT id, deleted_at FROM automation_definitions WHERE id='${sqlEscape(deleteID)}';`);
      runner.assert(resurrectedRow && (resurrectedRow[1] ?? '') === '', 'the definition row is live again (deleted_at cleared) in the DB', { resurrectedRow });

      disableDefinition(binary, deleteID, daemonEnv);
    });

    await runner.step('leg3_cleanup_dirty_safe', async () => {
      fs.writeFileSync(cleanupDefinitionFile, cleanupLifecycleDefinitionYAML({ id: cleanupID, executable: probe.executable, repoPath: cleanupFixture.repo, prompt: CLEANUP_PROMPT_V1 }));
      runJSON(binary, ['automation', 'apply', '--file', cleanupDefinitionFile], daemonEnv);
      cleanupApplied = true;

      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');
      const cleanRun = await poll(() => {
        const rows = (runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || []).filter((row) => row.state === 'delivered');
        return rows.length >= 1 ? rows[0] : null;
      }, 'cleanup leg first delivery', GH_DELIVERY_TIMEOUT_MS);
      const cleanWorktree = resolveWorktree(observer, profile, cleanRun.session_id);
      runner.assert(fs.existsSync(cleanWorktree), 'first delivery worktree exists', { cleanWorktree });

      await client.request('close_session', { sessionId: cleanRun.session_id });
      await waitSessionGone(observer, cleanRun.session_id, 'first cleanup-leg session to unregister');

      fs.writeFileSync(cleanupDefinitionFile, cleanupLifecycleDefinitionYAML({ id: cleanupID, executable: probe.executable, repoPath: cleanupFixture.repo, prompt: CLEANUP_PROMPT_V2 }));
      runJSON(binary, ['automation', 'apply', '--file', cleanupDefinitionFile], daemonEnv);

      await setRequested(mock.url, false);
      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');
      await setRequested(mock.url, true);
      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');

      const dirtyRun = await poll(() => {
        const rows = (runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || [])
          .filter((row) => row.state === 'delivered' && row.ticket_id !== cleanRun.ticket_id);
        return rows.length >= 1 ? rows[0] : null;
      }, 'cleanup leg second delivery on a fresh thread', GH_DELIVERY_TIMEOUT_MS);
      const dirtyWorktree = resolveWorktree(observer, profile, dirtyRun.session_id);
      runner.assert(dirtyWorktree !== cleanWorktree, 'the edit-rotated second delivery gets an independent worktree', { cleanWorktree, dirtyWorktree });
      runner.assert(fs.existsSync(dirtyWorktree), 'second delivery worktree exists', { dirtyWorktree });

      await client.request('close_session', { sessionId: dirtyRun.session_id });
      await waitSessionGone(observer, dirtyRun.session_id, 'second cleanup-leg session to unregister');

      fs.writeFileSync(path.join(dirtyWorktree, 'uncommitted-scratch.txt'), 'dirty for leg3 cleanup-dirty-safe\n');

      fs.writeFileSync(cleanupDefinitionFile, cleanupLifecycleDefinitionYAML({ id: cleanupID, executable: probe.executable, repoPath: cleanupFixture.repo, prompt: CLEANUP_PROMPT_V3 }));
      runJSON(binary, ['automation', 'apply', '--file', cleanupDefinitionFile], daemonEnv);

      await setRequested(mock.url, false);
      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');
      await setRequested(mock.url, true);
      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');

      const activeRun = await poll(() => {
        const rows = (runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || [])
          .filter((row) => row.state === 'delivered' && row.ticket_id !== cleanRun.ticket_id && row.ticket_id !== dirtyRun.ticket_id);
        return rows.length >= 1 ? rows[0] : null;
      }, 'cleanup leg third delivery on the current thread', GH_DELIVERY_TIMEOUT_MS);
      const activeWorktree = resolveWorktree(observer, profile, activeRun.session_id);
      runner.assert(activeWorktree !== cleanWorktree && activeWorktree !== dirtyWorktree, 'the third delivery gets an independent worktree', { cleanWorktree, dirtyWorktree, activeWorktree });
      runner.assert(fs.existsSync(activeWorktree), 'third delivery worktree exists', { activeWorktree });

      await client.request('close_session', { sessionId: activeRun.session_id });
      await waitSessionGone(observer, activeRun.session_id, 'third cleanup-leg session to unregister');

      const result = runJSON(binary, ['automation', 'cleanup', cleanupID], daemonEnv);
      runner.assert(
        Array.isArray(result.cleaned) && result.cleaned.includes(cleanRun.id) && !result.cleaned.includes(dirtyRun.id) && !result.cleaned.includes(activeRun.id),
        'cleanup reports only the clean run in cleaned',
        result,
      );
      runner.assert(
        Array.isArray(result.kept_dirty) && result.kept_dirty.includes(dirtyRun.id) && !result.kept_dirty.includes(cleanRun.id) && !result.kept_dirty.includes(activeRun.id),
        'cleanup reports only the dirty run in kept_dirty',
        result,
      );
      runner.assert(
        Array.isArray(result.kept_active) && result.kept_active.includes(activeRun.id) && !result.kept_active.includes(cleanRun.id) && !result.kept_active.includes(dirtyRun.id),
        'cleanup reports the still-bound current thread in kept_active rather than silently skipping it',
        result,
      );
      runner.assert(!fs.existsSync(cleanWorktree), 'the clean worktree is removed from disk', { cleanWorktree });
      runner.assert(!worktreeListShows(cleanupFixture.repo, cleanWorktree), 'the clean worktree is untracked by git worktree list', { cleanWorktree });
      runner.assert(fs.existsSync(dirtyWorktree), 'the dirty worktree directory is preserved', { dirtyWorktree });
      runner.assert(fs.existsSync(path.join(dirtyWorktree, 'uncommitted-scratch.txt')), 'the dirty worktree file is untouched', { dirtyWorktree });
      runner.assert(worktreeListShows(cleanupFixture.repo, dirtyWorktree), 'the dirty worktree is still tracked by git worktree list', { dirtyWorktree });
      runner.assert(fs.existsSync(activeWorktree), 'the still-bound worktree is preserved', { activeWorktree });
      runner.assert(worktreeListShows(cleanupFixture.repo, activeWorktree), 'the still-bound worktree is still tracked by git worktree list', { activeWorktree });

      const rowsAfterCleanup = runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || [];
      runner.assert(rowsAfterCleanup.length === 3, 'all three run rows still exist after cleanup', rowsAfterCleanup);
      runner.assert(
        rowsAfterCleanup.every((row) => row.state === 'delivered'),
        'cleanup never mutates run state',
        rowsAfterCleanup,
      );

      const second = runJSON(binary, ['automation', 'cleanup', cleanupID], daemonEnv);
      runner.assert(
        (second.cleaned || []).length === 0 && (second.kept_dirty || []).includes(dirtyRun.id) && (second.kept_active || []).includes(activeRun.id),
        'a second cleanup invocation is a no-op for the already-cleaned run and still reports the dirty and still-bound runs',
        second,
      );

      disableDefinition(binary, cleanupID, daemonEnv);
    });

    await runner.finishSuccess({ profile, editID, deleteID, cleanupID, run1, run2, run3, deleteRunID });
  } catch (error) {
    await captureFailureEvidence(runner, client).catch(() => {});
    await runner.finishFailure(error, { profile, editID, deleteID, cleanupID });
    throw error;
  } finally {
    // An enabled definition keeps ticking against a torn-down fixture and spams
    // this profile forever.
    if (daemonEnv) {
      if (editApplied) { try { disableDefinition(binary, editID, daemonEnv); } catch {} }
      if (deleteApplied) { try { disableDefinition(binary, deleteID, daemonEnv); } catch {} }
      if (cleanupApplied) { try { disableDefinition(binary, cleanupID, daemonEnv); } catch {} }
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    if (daemonEnv) { try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {} }
    if (mock?.child) mock.child.kill('SIGTERM');
    if (editFixture) { try { fs.rmSync(editFixture, { recursive: true, force: true }); } catch {} }
    if (deleteFixture) { try { fs.rmSync(deleteFixture, { recursive: true, force: true }); } catch {} }
    try { run(binary, ['daemon', 'ensure'], profileEnv(profile)); } catch {}
    await runner.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
