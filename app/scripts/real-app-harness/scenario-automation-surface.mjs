#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { parseCommonArgs, printCommonHelp, launchFreshAppAndConnect } from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, dataDirForProfile, resolveHarnessResources, profileCliEnv as profileEnv } from './harnessProfile.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import { appDaemonInTree } from './platform.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import { registeredAgentPid } from './workerRegistry.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const PANEL_APPEAR_TIMEOUT_MS = 30_000;
const RUN_DELIVERED_TIMEOUT_MS = 45_000;
const TOGGLE_TIMEOUT_MS = 20_000;
const RUN_ERROR_TIMEOUT_MS = 20_000;
const RESTART_READY_TIMEOUT_MS = 60_000;

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

function sessionsForDefinition(binary, definitionID, env) {
  const runs = runJSON(binary, ['automation', 'runs', definitionID], env) || [];
  return [...new Set(runs.map((automationRun) => automationRun.session_id).filter(Boolean))];
}

function seedForSession(binary, sessionID, env) {
  const garden = runJSON(binary, ['seed', 'ls', '--json'], env) || {};
  return (garden.seeds || []).find((seed) => seed.tender_session === sessionID) || null;
}

function seedByID(binary, seedID, env) {
  const garden = runJSON(binary, ['seed', 'ls', '--json'], env) || {};
  return (garden.seeds || []).find((seed) => seed.id === seedID) || null;
}

async function closeProbeSession(client, observer, dataDir, sessionID, cwd) {
  await cleanupSessionViaAppClose(client, observer, sessionID, RESTART_READY_TIMEOUT_MS);
  await poll(
    () => (registeredAgentPid(dataDir, sessionID, cwd) === null ? true : null),
    `registered agent for session ${sessionID} to exit`,
    RESTART_READY_TIMEOUT_MS,
  );
}

async function poll(fn, description, timeoutMs = 30_000) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(250);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

async function waitForDaemonReady(binary, daemonEnv) {
  await poll(() => {
    try {
      runJSON(binary, ['automation', 'list'], daemonEnv);
      return { ready: true };
    } catch {
      return null;
    }
  }, 'profile daemon', RESTART_READY_TIMEOUT_MS);
}

function createFixture(root) {
  const dir = fs.mkdtempSync(path.join(root, 'automations-surface-'));
  return fs.realpathSync(dir);
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

// A YAML carrying `enabled:` is rejected outright (errEnabledManagedOutsideSpec
// in internal/automation/automation.go), so no template below emits it.
function manualDefinitionYAML({ id, locationPath, executable }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 6 packaged automations-panel proof (manual)
trigger:
  type: manual
prompt: |
  Slice 6 packaged automations-panel proof. Do nothing; this executable is a test double.
launch:
  driver: codex
  executable: ${JSON.stringify(executable)}
  model: slice6-manual-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

function scheduledDefinitionYAML({ id, locationPath, executable }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 6 packaged automations-panel proof (non-manual)
trigger:
  type: scheduled
  schedule:
    cron: "0 0 1 1 *"
    time_zone: UTC
  continuity: fresh
  catch_up: latest
prompt: |
  Slice 6 non-manual trigger fixture. Never fires during the scenario window
  (once-a-year cron); exists only to prove the panel hides run-now for a
  non-manual trigger.
launch:
  driver: codex
  executable: ${JSON.stringify(executable)}
  model: slice6-scheduled-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

function findDefinitionRow(state, definitionId) {
  return (state?.definitions || []).find((row) => row.id === definitionId) || null;
}

function currentRuns(state) {
  return state?.runs || [];
}

async function closeAndReopenPanel(client) {
  // No close-panel bridge verb: automations_open_panel on an open panel is a
  // no-op and forces no refetch, so the close button is clicked through the DOM.
  await client.request('dom_click', { selector: '.automations-panel__close' });
  return client.request('automations_open_panel');
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
    printCommonHelp('scripts/real-app-harness/scenario-automation-surface.mjs');
    return;
  }
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('automation surface scenario requires a named non-production profile');
  const resources = resolveHarnessResources(profile);
  const binary = appDaemonInTree(resources.appPath);
  const runner = createScenarioRunner(options, {
    scenarioId: 'AUTOMATION-SURFACE',
    allowRealAgents: false,
    tier: 'tier2-local-fake-agent',
    prefix: 'automation-surface',
    metadata: { profile, panel: 'profile-level Automations dock panel' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const suffix = Date.now().toString(36);
  const manualID = `automations-surface-manual-${suffix}`;
  const scheduledID = `automations-surface-scheduled-${suffix}`;
  const manualDefinitionFile = path.join(runner.sessionDir, 'manual.yml');
  const scheduledDefinitionFile = path.join(runner.sessionDir, 'scheduled.yml');

  let daemonEnv = null;
  let fixturePath = null;
  let probe = null;
  let manualApplied = false;
  let scheduledApplied = false;
  let firstRunId = '';
  let manualSessionID = '';
  let manualSeedID = '';
  let manualSeedSettled = false;

  try {
    daemonEnv = profileEnv(profile);
    fixturePath = createFixture(runner.sessionDir);
    probe = createCodexProbe(runner.sessionDir);

    await runner.step('restart_isolated_daemon', async () => {
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
    });

    await runner.step('launch_packaged_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('leg1_apply_manual_and_panel_shows_it', async () => {
      fs.writeFileSync(
        manualDefinitionFile,
        manualDefinitionYAML({ id: manualID, locationPath: fixturePath, executable: probe.executable }),
      );
      runJSON(binary, ['automation', 'apply', '--file', manualDefinitionFile], daemonEnv);
      manualApplied = true;

      await client.request('automations_open_panel');
      const state = await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, manualID) ? current : null;
      }, `manual definition ${manualID} to appear in the panel`, PANEL_APPEAR_TIMEOUT_MS);
      const row = findDefinitionRow(state, manualID);
      runner.assert(row.trigger === 'Manual', 'manual definition renders trigger label "Manual"', row);
      runner.assert(row.enabled === true, 'manual definition renders enabled', row);
      runner.assert(row.canRunNow === true, 'manual definition renders the run-now affordance', row);
    });

    await runner.step('leg2_run_now_and_navigable', async () => {
      await client.request('automations_select_definition', { definitionId: manualID });
      await client.request('automations_run_now', { definitionId: manualID });
      const state = await poll(async () => {
        const current = await client.request('automations_get_state');
        const delivered = currentRuns(current).filter((r) => r.state === 'delivered');
        return delivered.length >= 1 ? current : null;
      }, 'run-now run to reach delivered', RUN_DELIVERED_TIMEOUT_MS);
      const runs = currentRuns(state);
      runner.assert(runs.length === 1, 'exactly one run exists after a single run-now click', runs);
      runner.assert(runs[0].state === 'delivered', 'the run reached delivered', runs[0]);
      runner.assert(runs[0].navigable === true, 'the delivered run is navigable through its session', runs[0]);
      firstRunId = runs[0].id;
      const daemonRuns = runJSON(binary, ['automation', 'runs', manualID], daemonEnv) || [];
      manualSessionID = daemonRuns.find((row) => row.id === firstRunId)?.session_id || '';
      runner.assert(Boolean(manualSessionID), 'the delivered run names its launched session', daemonRuns);

      const reopened = await closeAndReopenPanel(client);
      const reopenedRuns = currentRuns(reopened);
      runner.assert(reopenedRuns.length === 1, 'reopening the panel does not duplicate the run row', reopenedRuns);
      runner.assert(reopenedRuns[0].id === firstRunId, 'reopening the panel shows the same run id', reopenedRuns);
    });

    await runner.step('leg3_failure_shown_not_hidden', async () => {
      fs.writeFileSync(
        scheduledDefinitionFile,
        scheduledDefinitionYAML({ id: scheduledID, locationPath: fixturePath, executable: probe.executable }),
      );
      runJSON(binary, ['automation', 'apply', '--file', scheduledDefinitionFile], daemonEnv);
      scheduledApplied = true;
      const withScheduled = await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, scheduledID) ? current : null;
      }, `scheduled definition ${scheduledID} to appear in the panel`, PANEL_APPEAR_TIMEOUT_MS);
      const scheduledRow = findDefinitionRow(withScheduled, scheduledID);
      runner.assert(scheduledRow.trigger.startsWith('Scheduled'), 'non-manual definition renders a Scheduled trigger label', scheduledRow);
      runner.assert(scheduledRow.canRunNow === false, 'non-manual definition has no run-now affordance', scheduledRow);

      await client.request('automations_toggle_enabled', { definitionId: manualID });
      await poll(async () => {
        const current = await client.request('automations_get_state');
        const row = findDefinitionRow(current, manualID);
        return row && row.enabled === false ? current : null;
      }, 'manual definition to render disabled after broadcast', TOGGLE_TIMEOUT_MS);

      await client.request('automations_run_now', { definitionId: manualID });
      const rejected = await poll(async () => {
        const current = await client.request('automations_get_state');
        const row = findDefinitionRow(current, manualID);
        return row && row.runError ? current : null;
      }, 'daemon rejection of run-now on a disabled definition to render inline', RUN_ERROR_TIMEOUT_MS);
      const rejectedRow = findDefinitionRow(rejected, manualID);
      runner.assert(rejectedRow.canRunNow === true, 'run-now still renders for a disabled manual definition (the rejection is surfaced, not hidden by the button disappearing)', rejectedRow);
      runner.assert(rejectedRow.runError.toLowerCase().includes('disabled'), 'the inline run error names the daemon rejection reason', rejectedRow);
      runner.assert(currentRuns(rejected).length === 1, 'the rejected run-now click did not create a new run row', currentRuns(rejected));

      await client.request('automations_toggle_enabled', { definitionId: manualID });
      await poll(async () => {
        const current = await client.request('automations_get_state');
        const row = findDefinitionRow(current, manualID);
        return row && row.enabled === true ? current : null;
      }, 'manual definition to render enabled again after broadcast', TOGGLE_TIMEOUT_MS);
    });

    await runner.step('leg4_restart_preserves_list_and_navigation', async () => {
      await client.quitApp();
      await observer.close();
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
      await launchFreshAppAndConnect(client, observer);

      await client.request('automations_open_panel');
      const state = await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, manualID) && findDefinitionRow(current, scheduledID) ? current : null;
      }, 'both definitions to render after restart', RESTART_READY_TIMEOUT_MS);
      const manualRow = findDefinitionRow(state, manualID);
      const scheduledRow = findDefinitionRow(state, scheduledID);
      runner.assert(manualRow.enabled === true, 'manual definition is still enabled after restart', manualRow);
      runner.assert(scheduledRow.enabled === true, 'scheduled definition is still enabled after restart', scheduledRow);

      await client.request('automations_select_definition', { definitionId: manualID });
      const withRuns = await poll(async () => {
        const current = await client.request('automations_get_state');
        return currentRuns(current).length >= 1 ? current : null;
      }, 'manual definition run history to render after restart', RESTART_READY_TIMEOUT_MS);
      const runs = currentRuns(withRuns);
      runner.assert(runs.length === 1, 'exactly one run still exists after restart', runs);
      runner.assert(runs[0].id === firstRunId, 'the surviving run is the same run created before restart', runs[0]);
      runner.assert(runs[0].navigable === true, 'the run is still navigable after restart', runs[0]);
    });

    await runner.step('cleanup_probe_session', async () => {
      disableDefinition(binary, manualID, daemonEnv);
      disableDefinition(binary, scheduledID, daemonEnv);
      const manualSeed = seedForSession(binary, manualSessionID, daemonEnv);
      runner.assert(Boolean(manualSeed), 'the delivered manual run has a bound Garden seed', {
        manualSessionID,
        manualSeed,
      });
      manualSeedID = manualSeed.id;
      await closeProbeSession(client, observer, dataDirForProfile(profile), manualSessionID, fixturePath);
      manualSessionID = '';
      run(binary, ['seed', 'wither', manualSeedID, '-m', 'Automation surface harness fixture complete'], daemonEnv);
      manualSeedSettled = true;
      const settledSeed = seedByID(binary, manualSeedID, daemonEnv);
      runner.assert(settledSeed?.status === 'withered', 'the synthetic automation seed is withered after session cleanup', {
        manualSeedID,
        settledSeed,
      });

      run(binary, ['automation', 'delete', manualID], daemonEnv);
      manualApplied = false;
      run(binary, ['automation', 'delete', scheduledID], daemonEnv);
      scheduledApplied = false;
      const definitions = runJSON(binary, ['automation', 'list'], daemonEnv) || [];
      runner.assert(
        !definitions.some((definition) => definition.id === manualID || definition.id === scheduledID),
        'both fixture definitions are absent after cleanup',
        { definitions, manualID, scheduledID },
      );
    });

    await runner.finishSuccess({ profile, manualID, scheduledID, firstRunId, fixturePath });
  } catch (error) {
    await captureFailureEvidence(runner, client).catch(() => {});
    await runner.finishFailure(error, { profile, manualID, scheduledID, firstRunId, fixturePath });
    throw error;
  } finally {
    if (daemonEnv) {
      try {
        run(binary, ['daemon', 'ensure'], daemonEnv);
        await waitForDaemonReady(binary, daemonEnv);
      } catch {}
    }
    // Disable first so a failed session teardown cannot leave a directory
    // definition ticking against the fixture while cleanup continues.
    if (daemonEnv) {
      if (manualApplied) { try { disableDefinition(binary, manualID, daemonEnv); } catch {} }
      if (scheduledApplied) { try { disableDefinition(binary, scheduledID, daemonEnv); } catch {} }
    }
    const teardownSessionIDs = new Set(manualSessionID ? [manualSessionID] : []);
    if (manualApplied && daemonEnv) {
      try {
        for (const sessionID of sessionsForDefinition(binary, manualID, daemonEnv)) teardownSessionIDs.add(sessionID);
      } catch {}
    }
    const teardownSeedIDs = new Set(manualSeedID ? [manualSeedID] : []);
    if (daemonEnv) {
      for (const sessionID of teardownSessionIDs) {
        try {
          const seedID = seedForSession(binary, sessionID, daemonEnv)?.id;
          if (seedID) teardownSeedIDs.add(seedID);
        } catch {}
      }
    }
    for (const sessionID of teardownSessionIDs) {
      await closeProbeSession(
        client,
        observer,
        dataDirForProfile(profile),
        sessionID,
        fixturePath,
      ).catch(() => {});
    }
    if (daemonEnv) {
      for (const seedID of teardownSeedIDs) {
        if (manualSeedSettled && seedID === manualSeedID) continue;
        try { run(binary, ['seed', 'wither', seedID, '-m', 'Automation surface harness fixture complete'], daemonEnv); } catch {}
      }
    }
    if (daemonEnv) {
      if (manualApplied) { try { run(binary, ['automation', 'delete', manualID], daemonEnv); } catch {} }
      if (scheduledApplied) { try { run(binary, ['automation', 'delete', scheduledID], daemonEnv); } catch {} }
    }
    if (fixturePath) {
      try { fs.rmSync(fixturePath, { recursive: true, force: true }); } catch {}
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    try { run(binary, ['daemon', 'ensure'], profileEnv(profile)); } catch {}
    await runner.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
