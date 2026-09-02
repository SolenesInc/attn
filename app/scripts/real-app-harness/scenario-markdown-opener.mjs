#!/usr/bin/env node


import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  pressShortcutKeys,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver, delay } from './platform.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const OPENER_INPUT = '.markdown-opener-input';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  return {
    options: parseCommonArgs(args),
    help: args.includes('--help') || args.includes('-h'),
  };
}

async function openerState(client) {
  return client.request('markdown_opener_get_state', {});
}

async function waitForOpener(client, predicate, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await openerState(client);
    if (predicate(last)) return last;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last opener state:\n${JSON.stringify(last, null, 2)}`);
}

async function waitForWorkspaceUi(client, workspaceId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_workspace_ui_state', { workspaceId }).catch((error) => ({ error: String(error) }));
    if (predicate(last)) return last;
    await delay(200);
  }
  throw new Error(`Timed out waiting for ${description}. Last workspace UI state:\n${JSON.stringify(last, null, 2)}`);
}

async function waitForSessionUi(client, sessionId, predicate, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_session_ui_state', { sessionId }).catch((error) => ({ error: String(error) }));
    if (predicate(last)) return last;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last session UI state:\n${JSON.stringify(last, null, 2)}`);
}

function markdownTileIds(state) {
  return (state?.tileIds || []).filter((id) => id.startsWith('tile-markdown'));
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) return;
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await delay(200);
  }
}

function seedRepo(cwd, alpha, beta) {
  fs.mkdirSync(path.join(cwd, 'docs'), { recursive: true });
  fs.writeFileSync(path.join(cwd, 'docs', alpha), '# Alpha plan\n', 'utf8');
  fs.writeFileSync(path.join(cwd, 'docs', beta), '# Beta notes\n', 'utf8');
  const git = (...args) => execFileSync('git', args, { cwd, stdio: 'pipe' });
  git('init');
  git('config', 'user.email', 'harness@example.com');
  git('config', 'user.name', 'Harness');
  git('add', path.join('docs', alpha));
  git('commit', '-m', 'seed');
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-markdown-opener.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'MARKDOWN-OPENER',
    tier: 'tier1-local-shell',
    prefix: 'markdown-opener',
    metadata: {
      agent: 'shell',
      focus: 'native Cmd+P opener: fuzzy over git-enumerated markdown, then recents reusing a docked tile',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath });
  let sessionId = null;

  runner.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    process.env.ATTN_HARNESS_ALWAYS_ON_TOP ??= '0';
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    // Files are named per run: the recents table persists, so a re-run against
    // the same profile must still prove THIS run's opens landed.
    const alpha = `alpha-plan-${runner.runId}.md`;
    const beta = `beta-notes-${runner.runId}.md`;

    const { workspaceId, cwd } = await runner.step('create_shell_session', async () => {
      const sessionCwd = path.join(runner.sessionDir, 'opener-ws');
      fs.mkdirSync(sessionCwd, { recursive: true });
      seedRepo(sessionCwd, alpha, beta);
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: sessionCwd,
        label: `markdown-opener-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      runner.registerCleanup('close_session_panes', () => (sessionId ? closeWorkspacePanes(client, sessionId) : null));
      const pane = await waitForFirstWorkspacePane(client, sessionId, 'initial workspace pane');
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, {
        timeoutMs: 20_000,
        description: 'shell prompt ready',
      });
      const workspace = await client.request('get_workspace', { sessionId });
      if (!workspace.workspaceId) {
        throw new Error(`Could not resolve workspace id for session ${sessionId}: ${JSON.stringify(workspace)}`);
      }
      return { workspaceId: workspace.workspaceId, cwd: sessionCwd };
    });

    const summon = async (description) => {
      await driver.activateApp();
      await pressShortcutKeys(client, driver, 'file.open');
      try {
        await waitForOpener(client, (state) => state.open, description);
      } catch (error) {
        const frontmost = await driver.frontmostBundleId().catch(() => '(unknown)');
        throw new Error(
          `${error.message}\n\nThe native Cmd+P did not reach the app. This scenario needs native `
          + `keyboard input: grant Accessibility permission to the process running it and keep attn `
          + `frontmost. Frontmost app was "${frontmost}" (expected "${driver.bundleId}").`,
        );
      }
    };

    // Rows carry stable per-index ids and a previous run's recents can outrank
    // this run's file, so never assume the wanted row is the highlighted one.
    const pickRow = async (state, endsWith) => {
      const index = state.rows.findIndex((row) => row.path.endsWith(endsWith));
      if (index < 0) {
        throw new Error(`No opener row for ${endsWith}: ${JSON.stringify(state.rows)}`);
      }
      await client.request('dom_click', { selector: `#markdown-opener-opt-${index}` });
    };

    const typeQuery = async (text) => {
      await client.request('dom_type', { selector: OPENER_INPUT, text });
      await waitForOpener(client, (state) => state.query === text, `the opener query to read ${JSON.stringify(text)}`);
    };

    await runner.step('opener_summons', async () => {
      await summon('native Cmd+P opens the markdown opener');
      const state = await openerState(client);
      runner.assert(
        !state.rows.some((row) => row.path.includes(runner.runId)),
        `Empty query must not list files that have never been opened: ${JSON.stringify(state.rows)}`,
      );
      await captureScreenshotData(path.join(runner.runDir, 'opener-empty.png'), { client }).catch(() => {});
    });

    let betaTileId;
    await runner.step('fuzzy_opens_untracked_file', async () => {
      await typeQuery('betanotes');
      const state = await waitForOpener(
        client,
        (current) => current.rows.some((row) => row.path.endsWith(beta)),
        'fuzzy query matches the untracked markdown file',
      );
      await captureScreenshotData(path.join(runner.runDir, 'opener-fuzzy.png'), { client }).catch(() => {});
      await pickRow(state, beta);
      await waitForOpener(client, (current) => !current.open, 'picking a file closes the opener');
      const ui = await waitForWorkspaceUi(
        client,
        workspaceId,
        (state) => markdownTileIds(state).length === 1,
        'picking a file docks its markdown tile',
      );
      const openedTileId = markdownTileIds(ui)[0];
      betaTileId = openedTileId;
      runner.log(`[RealAppHarness] docked ${openedTileId} for ${beta}`);

      await client.request('dom_click', {
        selector: `[data-pane-id="${openedTileId}"] .workspace-dock-tile-focus-action`,
      });
      const focused = await waitForSessionUi(
        client,
        sessionId,
        (state) => state.workspace?.view?.maximizedPaneId === openedTileId,
        'the new document to enter Focus',
      );
      runner.assert(
        focused.workspace?.view?.maximizedPaneId === openedTileId,
        `The newly opened document must be visible and enter Focus: ${JSON.stringify(focused.workspace?.view)}`,
      );

      await client.request('dom_click', {
        selector: `[data-pane-id="${openedTileId}"] .workspace-dock-tile-review-button--overall`,
      });
      await client.request('dom_focus', { selector: '.md-annotation-popover .md-popover-textarea' });
      await driver.pressKeyCode(53);
      await client.request('dom_click', {
        selector: `[data-pane-id="${openedTileId}"] .workspace-dock-tile-review-button:not(.workspace-dock-tile-review-button--overall)`,
      });
      await client.request('dom_click', { selector: '.md-annotations-sidebar .md-sidebar-title' });
      await driver.pressKeyCode(53);
      const stillFocused = await waitForSessionUi(
        client,
        sessionId,
        (state) => state.workspace?.view?.maximizedPaneId === openedTileId,
        'Escape to close the review inspector without leaving Focus',
      );
      runner.assert(
        stillFocused.workspace?.view?.maximizedPaneId === openedTileId,
        `Floating review layers must unwind before Focus: ${JSON.stringify(stillFocused.workspace?.view)}`,
      );
      await driver.pressKeyCode(53);
      const restored = await waitForSessionUi(
        client,
        sessionId,
        (state) => state.workspace?.view?.maximizedPaneId === null,
        'Escape returns from document Focus',
      );
      runner.assert(
        restored.workspace?.view?.maximizedPaneId === null,
        `Escape must leave document Focus: ${JSON.stringify(restored.workspace?.view)}`,
      );
    });

    await runner.step('fuzzy_opens_tracked_file', async () => {
      await summon('re-summon for the tracked file');
      await typeQuery('alphaplan');
      const state = await waitForOpener(
        client,
        (current) => current.rows.some((row) => row.path.endsWith(alpha)),
        'fuzzy query matches the tracked markdown file',
      );
      await pickRow(state, alpha);
      await waitForWorkspaceUi(
        client,
        workspaceId,
        (state) => markdownTileIds(state).length === 2,
        'the second pick docks a second markdown tile',
      );
    });

    const beforeRecents = await client.request('get_workspace_ui_state', { workspaceId });
    await runner.step('recents_list_opened_files', async () => {
      await summon('re-summon to inspect recents');
      const state = await waitForOpener(
        client,
        (current) => current.rows.some((row) => row.path.endsWith(alpha)),
        'recents appear on an empty query',
      );
      const alphaAt = state.rows.findIndex((row) => row.path.endsWith(alpha));
      const betaAt = state.rows.findIndex((row) => row.path.endsWith(beta));
      runner.assert(
        alphaAt >= 0 && betaAt >= 0 && alphaAt < betaAt,
        `Recents must list both opened files, the later open first: ${JSON.stringify(state.rows)}`,
      );
      await captureScreenshotData(path.join(runner.runDir, 'opener-recents.png'), { client }).catch(() => {});

      // Alpha's tile holds the workspace focus from the previous step, so beta's
      // taking it back is the receipt this pick was acted on.
      await pickRow(state, beta);
      await waitForOpener(client, (current) => !current.open, 'picking a recent closes the opener');
      await waitForSessionUi(
        client,
        sessionId,
        (ui) => ui.workspace?.view?.activeLeafId === betaTileId,
        'the reused tile to take the workspace focus',
      );
      const after = await client.request('get_workspace_ui_state', { workspaceId });
      const before = markdownTileIds(beforeRecents);
      const now = markdownTileIds(after);
      runner.assert(
        now.length === 2 && before.every((id) => now.includes(id)),
        `Picking a recent must reuse its tile. Before: ${JSON.stringify(before)}, after: ${JSON.stringify(now)}`,
      );
    });

    const result = await runner.finishSuccess({ sessionId, workspaceId, cwd });
    console.log('[verify] PASS — markdown opener: fuzzy opens tracked and untracked files, and recents reuse their tile.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    await captureScreenshotData(path.join(runner.runDir, 'failure.png'), { client }).catch(() => {});
    const result = await runner.finishFailure(error, { sessionId });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
