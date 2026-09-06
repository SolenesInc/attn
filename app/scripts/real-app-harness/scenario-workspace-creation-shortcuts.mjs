#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  pressShortcutKeys,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import {
  waitForPaneAttached,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const options = parseCommonArgs(args);
  return {
    options,
    help: args.includes('--help') || args.includes('-h'),
  };
}

async function waitForPicker(client, title, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('location_picker_get_state');
    if (lastState?.open && lastState.title === title) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${title}. Last picker state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function dismissPicker(client, description, timeoutMs = 10_000) {
  let lastState = await client.request('location_picker_close');
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    if (!lastState?.open) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
    lastState = await client.request('location_picker_get_state');
  }
  throw new Error(`Timed out waiting for ${description}. Last picker state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function selectTerminalAgent(driver, client) {
  await driver.activateApp();
  await driver.pressKey('t', { option: true });
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < 5_000) {
    lastState = await client.request('location_picker_get_state');
    if (lastState?.selectedAgent === 'Terminal') {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Alt+T did not select Terminal. Last picker state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForStateSession(client, sessionId, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_state');
    const session = (lastState.sessions || []).find((entry) => entry.id === sessionId);
    if (session?.workspaceId) {
      return session;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${description}. Last state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function summonPicker(client, driver, shortcutId, title) {
  await driver.activateApp();
  await pressShortcutKeys(client, driver, shortcutId);
  const picker = await waitForPicker(client, title);
  await dismissPicker(client, `${shortcutId} picker to close`);
  return picker;
}

async function waitForActiveSession(client, sessionId, description, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_state');
    if (lastState.activeSessionId === sessionId) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

async function waitForNoSessionsUnderDir(client, dir, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastSessions = [];
  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state').catch(() => null);
    lastSessions = (state?.sessions || []).filter((session) => session.cwd?.startsWith(dir));
    if (lastSessions.length === 0) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for harness sessions under ${dir} to close: ${JSON.stringify(lastSessions, null, 2)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-workspace-creation-shortcuts.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'WORKSPACE-CREATION-SHORTCUTS',
    tier: 'tier1-local-shell',
    prefix: 'workspace-creation-shortcuts',
    metadata: {
      agent: 'shell',
      focus: 'Cmd+T, Cmd+N and Cmd+Shift+N survive the real OS keyboard path and summon the right location picker',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({
    appPath: options.appPath,
  });
  const createdSessionIds = [];
  let seedSessionId = null;
  let seedWorkspaceId = null;
  const note = (m, extra) => runner.log(m, extra);

  // Runner cleanups run in REVERSE registration order: observer/app first so
  // they close last, the created sessions last so they close first.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_created_sessions', async () => {
    for (const sessionId of [...createdSessionIds].reverse()) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await waitForNoSessionsUnderDir(client, runner.sessionDir).catch(() => {});
  });

  try {
    await runner.step('launch_app', async () => {
      process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
      process.env.ATTN_HARNESS_ALWAYS_ON_TOP ??= '0';
      await launchFreshAppAndConnect(client, observer);
    });

    // Without a selected local session Cmd+N falls back to the new-workspace
    // picker (App.tsx handleNewSession), so the session chords need one session.
    await runner.step('select_a_workspace_for_the_session_chords', async () => {
      const cwd = path.join(runner.sessionDir, 'selected-workspace');
      fs.mkdirSync(cwd, { recursive: true });
      const sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `ws-create-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      createdSessionIds.push(sessionId);
      seedSessionId = sessionId;
      const workspace = await waitForSessionWorkspace(
        client,
        sessionId,
        (entry) => (entry?.panes || []).length === 1 && (entry?.panes || []).every((pane) => pane.runtimeId),
        'initial pane for the selected workspace',
        30_000,
      );
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, workspace.panes[0].paneId, 20_000);
      await waitForPaneAttached(client, sessionId, workspace.panes[0].paneId, 20_000);
      await waitForActiveSession(client, sessionId, 'the created session to be the selected one');
      const seed = await waitForStateSession(client, sessionId, 'the seed session in app state');
      seedWorkspaceId = seed.workspaceId;
      note(`workspace selected for the session chords`, { sessionId, seedWorkspaceId });
    });

    await runner.step('cmd_t_creates_a_workspace_of_its_own', async () => {
      const cwd = path.join(runner.sessionDir, 'cmd-t-workspace');
      fs.mkdirSync(cwd, { recursive: true });
      await driver.activateApp();
      await pressShortcutKeys(client, driver, 'session.newWorkspace');
      await waitForPicker(client, 'New Workspace Location');
      await selectTerminalAgent(driver, client);
      await client.request('location_picker_set_path', { value: cwd });
      await client.request('location_picker_submit_path');

      const observed = await observer.waitForSession({ directory: cwd, timeoutMs: 30_000 });
      createdSessionIds.push(observed.id);
      const created = await waitForStateSession(client, observed.id, 'the Cmd+T session in app state');
      runner.assert(
        created.workspaceId !== seedWorkspaceId,
        `Cmd+T joined the selected workspace instead of creating its own: ${created.workspaceId} === ${seedWorkspaceId}`,
        { created, seedWorkspaceId },
      );
      runner.assert(
        created.agent === 'shell',
        `Alt+T did not carry Terminal through the submit: agent=${created.agent}`,
        created,
      );
      note(`Cmd+T created a new workspace`, { sessionId: created.id, workspaceId: created.workspaceId });
    });

    await runner.step('cmd_n_summons_the_new_session_picker', async () => {
      await summonPicker(client, driver, 'session.new', 'New Session Location');
      note(`Cmd+N summoned the new-session picker`);
    });

    await runner.step('cmd_shift_n_summons_the_new_session_picker', async () => {
      await summonPicker(client, driver, 'session.newHorizontal', 'New Session Location');
      note(`Cmd+Shift+N summoned the new-session picker`);
    });

    const summary = await runner.finishSuccess({ seedSessionId, seedWorkspaceId, sessionIds: createdSessionIds });
    console.log('[RealAppHarness] Workspace creation shortcuts passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionIds: createdSessionIds });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of [...createdSessionIds].reverse()) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await waitForNoSessionsUnderDir(client, runner.sessionDir).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
