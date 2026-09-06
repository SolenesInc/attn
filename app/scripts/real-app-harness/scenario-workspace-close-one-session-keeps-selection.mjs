#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  waitForPaneAttached,
  waitForPaneShellReady,
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

function paneIds(workspace) {
  return new Set((workspace?.panes || []).map((pane) => pane.paneId));
}

async function waitForPaneCount(client, sessionId, count, description, timeoutMs = 30_000) {
  return waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => (workspace?.panes || []).length === count && (workspace?.panes || []).every((pane) => pane.runtimeId),
    description,
    timeoutMs,
  );
}

async function waitForShellPaneReady(client, sessionId, paneId, description) {
  await waitForPaneVisible(client, sessionId, paneId, 20_000);
  await waitForPaneAttached(client, sessionId, paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, paneId, {
    timeoutMs: 20_000,
    description,
  });
}

async function createShellWorkspace(client, observer, cwd, label) {
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'shell',
    waitForInitialPaneVisible: false,
    sessionWaitMs: 30_000,
  });
  const workspace = await waitForPaneCount(client, sessionId, 1, `initial pane for ${label}`);
  const pane = workspace.panes[0];
  await client.request('select_session', { sessionId });
  await waitForShellPaneReady(client, sessionId, pane.paneId, `shell prompt ready for ${label}`);
  return { sessionId, pane };
}

async function splitWithShortcut(client, sessionId, shortcutId, expectedCount) {
  const before = await client.request('get_workspace', { sessionId });
  const beforeIds = paneIds(before);
  await client.request('dispatch_shortcut', { shortcutId });
  const after = await waitForPaneCount(client, sessionId, expectedCount, `${shortcutId} created pane`, 30_000);
  const created = (after.panes || []).find((pane) => !beforeIds.has(pane.paneId));
  if (!created) {
    throw new Error(`No new pane appeared after ${shortcutId}. Before=${JSON.stringify(before)} After=${JSON.stringify(after)}`);
  }
  await waitForPaneVisible(client, sessionId, created.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, created.paneId, 20_000);
  return { workspace: after, pane: created };
}

async function waitForSessionGoneFromUi(client, sessionId, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_session_ui_state', { sessionId }).catch((error) => ({ error: String(error) }));
    if (lastState?.exists === false && lastState?.sidebarItem == null) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last UI state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForSessionAbsentFromDaemon(observer, sessionId, description, timeoutMs = 20_000) {
  await observer.waitFor(
    () => observer.getSession(sessionId) == null ? true : null,
    description,
    timeoutMs,
  );
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

async function waitForPaneInputFocused(client, sessionId, paneId, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_pane_state', { sessionId, paneId }).catch((error) => ({ error: String(error) }));
    if (lastState?.inputFocused) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last pane state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function assertRemainingWorkspaceSelected(runner, client, remainingSessionId, closedSessionId) {
  const remaining = await client.request('get_session_ui_state', { sessionId: remainingSessionId });
  const closed = await client.request('get_session_ui_state', { sessionId: closedSessionId });
  runner.assert(
    Boolean(remaining.selected),
    `Expected remaining session ${remainingSessionId} to be selected: ${JSON.stringify(remaining, null, 2)}`,
    remaining,
  );
  runner.assert(
    Boolean(remaining.sidebarItem),
    `Expected remaining session ${remainingSessionId} to remain in sidebar: ${JSON.stringify(remaining, null, 2)}`,
    remaining,
  );
  runner.assert(
    Boolean(remaining.workspace?.view?.sessionVisible),
    `Expected remaining workspace to stay visible: ${JSON.stringify(remaining, null, 2)}`,
    remaining,
  );
  runner.assert(
    remaining.workspace?.model?.panes?.length === 1,
    `Expected remaining workspace to have exactly one pane: ${JSON.stringify(remaining.workspace?.model, null, 2)}`,
    remaining,
  );
  runner.assert(
    closed.exists === false && closed.sidebarItem == null,
    `Expected closed session ${closedSessionId} to be absent from sidebar: ${JSON.stringify(closed, null, 2)}`,
    closed,
  );
}

async function assertPreviousWorkspaceVisible(runner, client, visibleSessionId, goneSessionId) {
  const visible = await client.request('get_session_ui_state', { sessionId: visibleSessionId });
  const gone = await client.request('get_session_ui_state', { sessionId: goneSessionId });
  runner.assert(
    Boolean(visible.selected),
    `Expected previous workspace session ${visibleSessionId} to be selected: ${JSON.stringify(visible, null, 2)}`,
    visible,
  );
  runner.assert(
    Boolean(visible.workspace?.view?.sessionVisible),
    `Expected previous workspace ${visibleSessionId} to be visible: ${JSON.stringify(visible, null, 2)}`,
    visible,
  );
  runner.assert(
    visible.workspace?.model?.panes?.length === 1,
    `Expected previous workspace to still have one pane: ${JSON.stringify(visible.workspace?.model, null, 2)}`,
    visible,
  );
  runner.assert(
    gone.exists === false && gone.sidebarItem == null,
    `Expected closed workspace session ${goneSessionId} to be absent from sidebar: ${JSON.stringify(gone, null, 2)}`,
    gone,
  );
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
    printCommonHelp('scripts/real-app-harness/scenario-workspace-close-one-session-keeps-selection.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'WORKSPACE-CLOSE-ONE-SESSION-KEEPS-SELECTION',
    tier: 'tier1-local-shell',
    prefix: 'workspace-close-one-session-keeps-selection',
    metadata: {
      agent: 'shell',
      focus: 'closing a split session keeps the remaining workspace session selected, and closing the last session of a workspace switches back to the previously selected workspace',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const createdSessionIds = [];
  let keptSessionId = null;
  let splitSessionId = null;
  let closingSessionId = null;
  const note = (m, extra) => runner.log(m, extra);

  // Runner cleanups run in REVERSE registration order: observer/app are
  // registered first so they close LAST, the sessions last so they close FIRST.
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
      await launchFreshAppAndConnect(client, observer);
    });

    let kept;
    await runner.step('create_kept_workspace', async () => {
      kept = await createShellWorkspace(client, observer, path.join(runner.sessionDir, 'kept'), `ws-close-one-${runner.runId}`);
      keptSessionId = kept.sessionId;
      createdSessionIds.push(keptSessionId);
      note(`kept workspace ready and selected`, { keptSessionId });
    });

    await runner.step('focusing_a_split_pane_selects_that_split_session', async () => {
      const split = await splitWithShortcut(client, keptSessionId, 'terminal.splitVertical', 2);
      splitSessionId = split.pane.runtimeId;
      createdSessionIds.push(splitSessionId);
      await client.request('focus_pane', { sessionId: keptSessionId, paneId: split.pane.paneId });
      await waitForActiveSession(client, splitSessionId, 'split session selected before close');
      note(`split session created and selected`, { splitSessionId });
    });

    await runner.step('closing_a_split_session_removes_it_everywhere', async () => {
      await client.request('dispatch_shortcut', { shortcutId: 'terminal.close' });
      await waitForSessionAbsentFromDaemon(observer, splitSessionId, 'split session unregistered after close');
      await waitForSessionGoneFromUi(client, splitSessionId, 'split session gone from UI/sidebar after close');
      note(`split session closed`, { splitSessionId });
    });

    await runner.step('closing_a_split_keeps_the_remaining_session_selected', async () => {
      await waitForActiveSession(client, keptSessionId, 'remaining workspace session selected after close');
      await waitForSessionWorkspace(
        client,
        keptSessionId,
        (workspace) => (workspace?.panes || []).length === 1,
        'remaining workspace collapsed to one pane after close',
        15_000,
      );
      await assertRemainingWorkspaceSelected(runner, client, keptSessionId, splitSessionId);
      note(`remaining workspace session selected after close`, { keptSessionId });
    });

    let closing;
    await runner.step('create_second_workspace_and_select_it', async () => {
      closing = await createShellWorkspace(client, observer, path.join(runner.sessionDir, 'closing'), `ws-close-target-${runner.runId}`);
      closingSessionId = closing.sessionId;
      createdSessionIds.push(closingSessionId);
      await client.request('select_session', { sessionId: keptSessionId });
      await waitForActiveSession(client, keptSessionId, 'kept workspace selected before the close target');
      await client.request('select_session', { sessionId: closingSessionId });
      await waitForActiveSession(client, closingSessionId, 'target workspace selected before close shortcut');
      await client.request('focus_pane', { sessionId: closingSessionId, paneId: closing.pane.paneId });
      await waitForPaneInputFocused(client, closingSessionId, closing.pane.paneId, 'target pane focused before close shortcut');
      note(`closing workspace selected and focused`, { closingSessionId });
    });

    await runner.step('closing_the_last_session_of_a_workspace_removes_it_everywhere', async () => {
      await client.request('dispatch_shortcut', { shortcutId: 'session.close' });
      await waitForSessionAbsentFromDaemon(observer, closingSessionId, 'target session unregistered after close shortcut');
      await waitForSessionGoneFromUi(client, closingSessionId, 'target session gone from UI/sidebar after close shortcut');
      note(`closing workspace session closed`, { closingSessionId });
    });

    await runner.step('closing_the_last_session_switches_back_to_the_previous_workspace', async () => {
      await waitForActiveSession(client, keptSessionId, 'previous workspace selected after closing target');
      await assertPreviousWorkspaceVisible(runner, client, keptSessionId, closingSessionId);
      note(`previous workspace selected and visible after switchback`, { keptSessionId });
    });

    const summary = await runner.finishSuccess({
      keptSessionId,
      closedSplitSessionId: splitSessionId,
      closedWorkspaceSessionId: closingSessionId,
    });
    console.log('[RealAppHarness] Workspace close-session selection passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, {
      keptSessionId,
      closedSplitSessionId: splitSessionId,
      closedWorkspaceSessionId: closingSessionId,
    });
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
