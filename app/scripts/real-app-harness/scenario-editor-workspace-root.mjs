#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  pressShortcutKeys,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
import { createWindowDriver } from './platform.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

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

// Inactive workspaces stay mounted but hidden, so an unscoped selector matches
// stale tiles from a previous run's workspace.
const ACTIVE_TILE = '.terminal-wrapper.active .workspace-dock-tile';
const FINDER_SELECTOR = `${ACTIVE_TILE} .notebook-finder`;
const EDITOR_SELECTOR = `${ACTIVE_TILE} .cm-content`;
const RAIL_SELECTOR = `${ACTIVE_TILE} .notebook-browser-rail`;

// Mirrors internal/notebook/layout.go DefaultRoot, and is the real root only
// while the daemon's `notebook.root` setting is unset.
function defaultNotebookRootForProfile(profile) {
  const normalized = (profile || '').trim().toLowerCase();
  const base = path.join(os.homedir(), 'attn-notebook');
  return normalized === '' || normalized === 'default' ? base : `${base}-${normalized}`;
}

async function domSelectorPresent(client, selector) {
  try {
    await client.request('capture_screenshot_data', { selector });
    return true;
  } catch (error) {
    if (String(error).includes('Screenshot selector not found in DOM')) {
      return false;
    }
    // html-to-image throws on some subtrees (CodeMirror's .cm-content); only the
    // "not found" error means the element is absent.
    return true;
  }
}

async function waitForDomSelector(client, selector, present, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    if ((await domSelectorPresent(client, selector)) === present) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${selector} to be ${present ? 'present' : 'absent'}: ${description}`);
}

// Confirms the negative twice, a beat apart: a single premature check passes
// against a fetch that is merely slow.
async function assertNeverAppears(client, selector, description, settleMs = 1_500) {
  await waitForDomSelector(client, selector, false, `${description} (initial)`, 3_000);
  await new Promise((resolve) => setTimeout(resolve, settleMs));
  await waitForDomSelector(client, selector, false, `${description} (after settle)`, 3_000);
}

async function waitForWorkspaceUi(client, workspaceId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_workspace_ui_state', { workspaceId }).catch((error) => ({ error: String(error) }));
    if (predicate(last)) {
      return last;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last workspace UI state:\n${JSON.stringify(last, null, 2)}`);
}

async function dockEditorTileNative(client, driver, workspaceId) {
  await driver.activateApp();
  await pressShortcutKeys(client, driver, 'notebook.openTile');
  try {
    return await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1
        && Array.isArray(state?.tileTitles) && state.tileTitles.includes('Editor'),
      'native Cmd+Opt+N to dock a fresh editor tile (titled "Editor")',
      15_000,
    );
  } catch (dockError) {
    const frontmost = await driver.frontmostBundleId().catch(() => '(unknown)');
    throw new Error(
      `${dockError.message}\n\nThe native Cmd+Opt+N did not reach the app. This scenario `
      + `needs native keyboard input: grant Accessibility permission to the process running it `
      + `and keep attn frontmost. Frontmost app was "${frontmost}" (expected "${driver.bundleId}").`,
    );
  }
}

async function openNoteViaFinder(client, driver, basename) {
  await waitForDomSelector(client, FINDER_SELECTOR, true, 'fresh editor tile auto-opens its finder');
  await driver.activateApp();
  await driver.typeText(basename);
  await driver.pressEnter();
  await waitForDomSelector(client, FINDER_SELECTOR, false, 'Enter picks the note and closes the finder');
  await waitForDomSelector(client, EDITOR_SELECTOR, true, 'note opens into the live markdown editor');
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

async function closeExistingSessions(client, sessionRootDir) {
  const initial = await client.request('get_state');
  const harnessSessions = (initial.sessions || []).filter((session) => session.cwd?.startsWith(sessionRootDir));
  for (const session of harnessSessions) {
    await closeWorkspacePanes(client, session.id).catch(() => {});
  }
}

async function openWorkspaceForCwd(client, observer, cwd, label, sessionWaitMs = 30_000) {
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'shell',
    waitForInitialPaneVisible: false,
    sessionWaitMs,
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, 'initial workspace pane');
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId, {
    timeoutMs: 20_000,
    description: 'shell prompt ready',
  });
  const workspace = await client.request('get_workspace', { sessionId });
  const workspaceId = workspace.workspaceId;
  if (!workspaceId) {
    throw new Error(`Could not resolve workspace id for session ${sessionId}: ${JSON.stringify(workspace)}`);
  }
  return { sessionId, workspaceId };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-editor-workspace-root.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'EDITOR-WORKSPACE-ROOT',
    tier: 'tier1-local-shell',
    prefix: 'editor-workspace-root',
    metadata: {
      agent: 'shell',
      focus: 'editor tile over an arbitrary workspace root: off-root gating + on-root positive control',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath });
  let tempSessionId = null;
  let notebookSessionId = null;
  let positiveControlPath = null;

  runner.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  // Runner cleanups run in REVERSE registration order: observer/app are
  // registered first so they close LAST.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('remove_positive_control_note', () => {
    if (positiveControlPath) {
      fs.rmSync(positiveControlPath, { force: true });
    }
  });

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      await closeExistingSessions(client, options.sessionRootDir);
    });

    const { tempRoot, notebookRoot, positiveControlBasename } = await runner.step('seed_fixtures', async () => {
      const root = path.join(runner.sessionDir, 'editor-root');
      fs.mkdirSync(path.join(root, 'dir'), { recursive: true });
      fs.writeFileSync(path.join(root, 'README.md'), '# Editor root fixture\n\nOff-root gating probe.\n', 'utf8');
      fs.writeFileSync(path.join(root, 'dir', 'note.md'), '# Nested note\n\nUnder dir/.\n', 'utf8');
      runner.log(`[RealAppHarness] tempRoot=${root}`);

      const notebookRootDir = defaultNotebookRootForProfile(currentHarnessProfile());
      fs.mkdirSync(notebookRootDir, { recursive: true });
      const basename = `editor-root-positive-control-${runner.runId}`;
      positiveControlPath = path.join(notebookRootDir, `${basename}.md`);
      fs.writeFileSync(positiveControlPath, '# Positive control\n\nOn-root rail probe.\n', 'utf8');
      runner.log(`[RealAppHarness] notebookRoot=${notebookRootDir}`);
      runner.log(`[RealAppHarness] positiveControlPath=${positiveControlPath}`);

      return { tempRoot: root, notebookRoot: notebookRootDir, positiveControlBasename: basename };
    });

    const { off, offRootDocked } = await runner.step('dock_off_root_tile', async () => {
      const result = await openWorkspaceForCwd(client, observer, tempRoot, `editor-root-off-${runner.runId}`);
      tempSessionId = result.sessionId;
      runner.registerCleanup('close_temp_session_panes', () => (tempSessionId ? closeWorkspacePanes(client, tempSessionId) : null));

      const docked = await dockEditorTileNative(client, driver, result.workspaceId);
      runner.log(`[RealAppHarness] docked off-root editor tile=${docked.tileIds[0]}`);
      return { off: result, offRootDocked: docked };
    });

    await runner.step('open_off_root_note_and_assert_title', async () => {
      await openNoteViaFinder(client, driver, 'README');
      await waitForWorkspaceUi(
        client,
        off.workspaceId,
        (state) => Array.isArray(state?.tileTitles) && state.tileTitles.includes('README.md'),
        'tile title becomes the opened file\'s basename',
        10_000,
      );
      await captureScreenshotData(path.join(runner.runDir, 'off-root-open.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] off-root-open screenshot failed: ${error}`);
      });
    });

    await runner.step('assert_off_root_rail_withheld', async () => {
      await assertNeverAppears(client, RAIL_SELECTOR, 'off-root tile withholds the backlinks/outline rail');
      runner.log('[RealAppHarness] off-root tile: rail withheld as expected.');
    });

    const { on, onRootDocked } = await runner.step('dock_on_root_tile', async () => {
      const result = await openWorkspaceForCwd(client, observer, notebookRoot, `editor-root-on-${runner.runId}`);
      notebookSessionId = result.sessionId;
      runner.registerCleanup('close_notebook_session_panes', () => (notebookSessionId ? closeWorkspacePanes(client, notebookSessionId) : null));

      const docked = await dockEditorTileNative(client, driver, result.workspaceId);
      runner.log(`[RealAppHarness] docked on-root (Notebook) editor tile=${docked.tileIds[0]}`);
      return { on: result, onRootDocked: docked };
    });

    await runner.step('open_on_root_note_and_assert_rail', async () => {
      await openNoteViaFinder(client, driver, positiveControlBasename);
      await waitForDomSelector(client, RAIL_SELECTOR, true, 'on-root (Notebook) tile CAN render the backlinks/outline rail', 10_000);
      runner.log('[RealAppHarness] on-root (Notebook) tile: rail rendered (positive control).');
      await captureScreenshotData(path.join(runner.runDir, 'on-root-rail.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] on-root-rail screenshot failed: ${error}`);
      });
    });

    const summary = await runner.finishSuccess({
      tempRoot,
      notebookRoot,
      offRoot: {
        workspaceId: off.workspaceId,
        tileId: offRootDocked.tileIds[0],
        tileTitles: offRootDocked.tileTitles,
      },
      onRoot: {
        workspaceId: on.workspaceId,
        tileId: onRootDocked.tileIds[0],
      },
    });
    console.log('[RealAppHarness] Editor tile over an arbitrary workspace root (off-root gating + positive control) passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { tempSessionId, notebookSessionId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (tempSessionId) {
      await closeWorkspacePanes(client, tempSessionId).catch(() => {});
    }
    if (notebookSessionId) {
      await closeWorkspacePanes(client, notebookSessionId).catch(() => {});
    }
    if (positiveControlPath) {
      fs.rmSync(positiveControlPath, { force: true });
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
