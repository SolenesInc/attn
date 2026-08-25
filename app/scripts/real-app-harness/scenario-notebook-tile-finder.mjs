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

// Scope probes to the ACTIVE workspace: inactive ones stay mounted but hidden,
// so an unscoped selector can match a previous run's finder.
const FINDER_SELECTOR = '.terminal-wrapper.active .notebook-finder';

async function finderPresent(client) {
  try {
    await client.request('capture_screenshot_data', { selector: FINDER_SELECTOR });
    return true;
  } catch (error) {
    if (String(error).includes('Screenshot selector not found in DOM')) {
      return false;
    }
    // The bridge emits "not found" only when the element is genuinely absent,
    // and html-to-image can throw on some subtrees (CodeMirror's .cm-content).
    return true;
  }
}

async function waitForFinder(client, present, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    if ((await finderPresent(client)) === present) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for finder to be ${present ? 'present' : 'absent'}: ${description}`);
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
    printCommonHelp('scripts/real-app-harness/scenario-notebook-tile-finder.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'NOTEBOOK-TILE-FINDER',
    tier: 'tier1-local-shell',
    prefix: 'notebook-tile-finder',
    metadata: {
      agent: 'shell',
      focus: 'native Cmd+Opt+N dock, notebook finder auto-open, Cmd+P re-summon after Esc',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;

  runner.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  // Runner cleanups run in REVERSE registration order.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      await closeExistingSessions(client, options.sessionRootDir);
    });

    const { workspaceId } = await runner.step('create_shell_session', async () => {
      const cwd = path.join(runner.sessionDir, 'finder-ws');
      fs.mkdirSync(cwd, { recursive: true });
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `notebook-finder-${runner.runId}`,
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
          `${dockError.message}\n\nThe native Cmd+Opt+N did not reach the app. This scenario `
          + `needs native keyboard input: grant Accessibility permission to the process running it `
          + `and keep attn frontmost. Frontmost app was "${frontmost}" (expected "${driver.bundleId}").`,
        );
      }
      runner.log(`[RealAppHarness] docked notebook tile=${result.tileIds[0]}`);
      return result;
    });

    await runner.step('finder_auto_opens', async () => {
      await waitForFinder(client, true, 'fresh notebook tile auto-opens its finder');
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'finder-open.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] finder-open screenshot failed: ${error}`);
      });
    });

    await runner.step('esc_closes_finder', async () => {
      await driver.activateApp();
      await driver.pressKeyCode(53); // Esc — InputDriver's --key map only covers printable keys
      await waitForFinder(client, false, 'Esc dismisses the finder');
    });

    await runner.step('cmdp_resummons_finder', async () => {
      await driver.activateApp();
      await driver.pressKey('p', { command: true });
      await waitForFinder(client, true, 'native Cmd+P re-summons the finder after Esc');
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'finder-resummoned.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] finder-resummoned screenshot failed: ${error}`);
      });
    });

    const summary = runner.finishSuccess({
      workspaceId,
      tileId: docked.tileIds[0],
      tileTitles: docked.tileTitles,
    });
    console.log('[RealAppHarness] Notebook tile finder (native Cmd+Opt+N dock, Cmd+P re-summon) passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { sessionId });
    console.error(summary.error);
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
