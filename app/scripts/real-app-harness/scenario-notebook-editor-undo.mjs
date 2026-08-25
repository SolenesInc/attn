#!/usr/bin/env node

// Playwright delivers ⌘Z as a plain DOM keydown, never through the native menu,
// so only a packaged-app run catches an Edit > Undo item swallowing it.

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
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

// Scope probes to the ACTIVE workspace: hidden workspaces stay mounted, so an
// unscoped selector can match a stale editor from a previous run's workspace.
const FINDER_SELECTOR = '.terminal-wrapper.active .notebook-finder';
const EDITOR_SELECTOR = '.terminal-wrapper.active .cm-content';
const ORIGINAL_CONTENT = '# Undo Probe\n\nThis paragraph exists before the probe types anything.\n';
const PROBE_SUFFIX = ' UNDOPROBE';

async function domSelectorPresent(client, selector) {
  try {
    await client.request('capture_screenshot_data', { selector });
    return true;
  } catch (error) {
    if (String(error).includes('Screenshot selector not found in DOM')) {
      return false;
    }
    // The bridge only emits "not found" when the element is genuinely absent, so
    // any other capture failure still means present.
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

// Autosave is debounced 700ms (NotebookSurface AUTOSAVE_DELAY_MS).
async function waitForFileContent(filePath, predicate, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    try {
      last = fs.readFileSync(filePath, 'utf8');
    } catch (error) {
      last = `<read error: ${error instanceof Error ? error.message : String(error)}>`;
    }
    if (typeof last === 'string' && predicate(last)) {
      return last;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`Timed out waiting for ${description}. Last file content:\n${last}`);
}

// CodeMirror's history groups adjacent fast typing into one undo step, so the
// press count is not guaranteed; retry rather than asserting an exact count.
async function pressUntilFileMatches(driver, filePath, key, modifiers, predicate, description, maxPresses = 5) {
  for (let attempt = 1; attempt <= maxPresses; attempt += 1) {
    await driver.activateApp();
    await driver.pressKey(key, modifiers);
    try {
      return await waitForFileContent(filePath, predicate, `${description} (press ${attempt}/${maxPresses})`, 3_000);
    } catch {
    }
  }
  const finalContent = fs.readFileSync(filePath, 'utf8');
  throw new Error(`${description}: still not satisfied after ${maxPresses} presses of ${key}. Final content:\n${finalContent}`);
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

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-notebook-editor-undo.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'NOTEBOOK-EDITOR-UNDO',
    tier: 'tier1-local-shell',
    prefix: 'notebook-editor-undo',
    metadata: {
      agent: 'shell',
      focus: 'native Cmd+Z / Shift+Cmd+Z inside a notebook tile editor',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;
  let probeFilePath = null;

  runner.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  // Runner cleanups run in REVERSE registration order, so register observer/app
  // first: they must close last.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('remove_probe_file', () => {
    if (probeFilePath) {
      fs.rmSync(probeFilePath, { force: true });
    }
  });

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      await closeExistingSessions(client, options.sessionRootDir);
    });

    const { cwd, probeBasename } = await runner.step('seed_probe_note', async () => {
      const dir = path.join(runner.sessionDir, 'undo-ws');
      fs.mkdirSync(dir, { recursive: true });
      const basename = `undo-probe-${Date.now().toString(36)}`;
      probeFilePath = path.join(dir, `${basename}.md`);
      fs.writeFileSync(probeFilePath, ORIGINAL_CONTENT, 'utf8');
      runner.log(`[RealAppHarness] probeFilePath=${probeFilePath}`);
      return { cwd: dir, probeBasename: basename };
    });

    const { workspaceId } = await runner.step('create_shell_session', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `notebook-undo-${runner.runId}`,
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
      const id = workspace.workspaceId;
      if (!id) {
        throw new Error(`Could not resolve workspace id for session ${sessionId}: ${JSON.stringify(workspace)}`);
      }
      return { workspaceId: id };
    });

    const docked = await runner.step('dock_notebook_tile', async () => {
      await driver.activateApp();
      await driver.pressKey('n', { command: true, option: true });

      let result;
      try {
        result = await waitForWorkspaceUi(
          client,
          workspaceId,
          (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1
            && Array.isArray(state?.tileTitles) && state.tileTitles.includes('Editor'),
          'native Cmd+Opt+N to dock a fresh notebook tile (titled "Editor")',
          15_000,
        );
      } catch (dockError) {
        const frontmost = await driver.frontmostBundleId().catch(() => '(unknown)');
        throw new Error(
          `${dockError.message}\n\nThis scenario needs native keyboard input: grant Accessibility `
          + `permission to the process running it and keep attn frontmost. Frontmost app was `
          + `"${frontmost}" (expected "${driver.bundleId}").`,
        );
      }
      runner.log(`[RealAppHarness] docked notebook tile=${result.tileIds[0]}`);
      return result;
    });

    await runner.step('open_probe_note_via_finder', async () => {
      await waitForDomSelector(client, FINDER_SELECTOR, true, 'fresh notebook tile auto-opens its finder');
      await driver.activateApp();
      await driver.typeText(probeBasename);
      // Pressing Enter before the probe row renders is a no-op (pick(undefined)).
      await waitForDomSelector(
        client,
        '.terminal-wrapper.active .notebook-finder .notebook-finder-option',
        true,
        'finder lists the probe note before Enter',
        15_000,
      );
      await driver.pressEnter();
      await waitForDomSelector(client, FINDER_SELECTOR, false, 'Enter picks the probe note and closes the finder');

      await waitForDomSelector(client, EDITOR_SELECTOR, true, 'note opens into the live markdown editor');
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'editor-open.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] editor-open screenshot failed: ${error}`);
      });
    });

    // Opening a note via the finder does NOT focus the editor, so click into the
    // note body and confirm CodeMirror's `.cm-focused` before typing.
    await runner.step('focus_editor_with_native_click', async () => {
      await driver.activateApp();
      let editorFocused = false;
      for (let attempt = 0; attempt < 2 && !editorFocused; attempt++) {
        await driver.clickWindow(0.85, 0.85);
        try {
          await waitForDomSelector(client, '.terminal-wrapper.active .cm-editor.cm-focused', true, 'native click focuses the CodeMirror editor', 5_000);
          editorFocused = true;
        } catch (error) {
          if (attempt === 1) throw error;
        }
      }
    });

    await runner.step('type_probe_text_and_autosave', async () => {
      await driver.activateApp();
      await driver.typeText(PROBE_SUFFIX);
      await waitForFileContent(
        probeFilePath,
        (content) => content.includes(PROBE_SUFFIX.trim()),
        'autosave to persist the typed probe text',
      );
      runner.log('[RealAppHarness] probe text landed on disk via native typing + autosave.');
    });

    await runner.step('native_cmdz_undo', async () => {
      await pressUntilFileMatches(
        driver,
        probeFilePath,
        'z',
        { command: true },
        (content) => content === ORIGINAL_CONTENT,
        'native Cmd+Z to undo the probe text back to original content',
      );
      runner.log('[RealAppHarness] native Cmd+Z undid the probe text (menu no longer swallows it).');
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'after-undo.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] after-undo screenshot failed: ${error}`);
      });
    });

    await runner.step('native_shift_cmdz_redo', async () => {
      await pressUntilFileMatches(
        driver,
        probeFilePath,
        'z',
        { command: true, shift: true },
        (content) => content.includes(PROBE_SUFFIX.trim()),
        'native Shift+Cmd+Z to redo the probe text',
      );
      runner.log('[RealAppHarness] native Shift+Cmd+Z redid the probe text (menu no longer swallows it).');
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'after-redo.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] after-redo screenshot failed: ${error}`);
      });
    });

    await runner.step('assert_no_pane_zoom', async () => {
      const sessionUiState = await client.request('get_session_ui_state', { sessionId });
      const zoomedPaneId = sessionUiState?.workspace?.view?.zoomedPaneId ?? null;
      runner.assert(
        !zoomedPaneId,
        `Shift+Cmd+Z zoomed pane ${zoomedPaneId} instead of (only) redoing in the editor — `
        + `terminal.toggleZoom fired alongside/instead of CodeMirror redo.`,
        sessionUiState,
      );
    });

    const summary = runner.finishSuccess({
      workspaceId,
      tileId: docked.tileIds[0],
      probeFilePath,
    });
    console.log('[RealAppHarness] Notebook editor native Cmd+Z undo / Shift+Cmd+Z redo passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { sessionId, probeFilePath });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    if (probeFilePath) {
      fs.rmSync(probeFilePath, { force: true });
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
