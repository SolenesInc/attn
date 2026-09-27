#!/usr/bin/env node

// In the packaged app the native "Close Pane" item claims ⌘W and dispatches
// session.close, not the DOM terminal.close path browser e2e exercises.

import fs from 'node:fs';
import path from 'node:path';
import {
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import {
  waitForFirstDesktopPane,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

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

// Scoped to the ACTIVE desktop's wrapper: a tile docked in an inactive
// desktop is still mounted, and a bare `.notebook-finder` would match it.
const FINDER_SELECTOR = '.terminal-wrapper.active .notebook-finder';

// Presence via the screenshot bridge: only "not found" proves absence. Any
// other error still proves the element is there.
async function finderPresent(client) {
  try {
    await client.request('capture_screenshot_data', { selector: FINDER_SELECTOR });
    return true;
  } catch (error) {
    return !String(error).includes('Screenshot selector not found in DOM');
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

async function waitForDesktopUi(client, desktopId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_desktop_ui_state', { desktopId }).catch((error) => ({ error: String(error) }));
    if (predicate(last)) {
      return last;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last desktop UI state:\n${JSON.stringify(last, null, 2)}`);
}

async function closeDesktopPanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const desktop = await client.request('get_desktop', { sessionId }).catch(() => null);
    const pane = desktop?.panes?.[0];
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
    await closeDesktopPanes(client, session.id).catch(() => {});
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-notebook-tile-close.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'notebook-tile-close');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  let sessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    const cwd = path.join(sessionDir, 'close-ws');
    fs.mkdirSync(cwd, { recursive: true });
    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd,
      label: `notebook-close-${runId}`,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    const pane = await waitForFirstDesktopPane(client, sessionId, 'initial desktop pane');
    await client.request('select_session', { sessionId });
    await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
    await waitForPaneShellReady(client, sessionId, pane.paneId, {
      timeoutMs: 20_000,
      description: 'shell prompt ready',
    });

    const desktop = await client.request('get_desktop', { sessionId });
    const desktopId = desktop.desktopId;
    if (!desktopId) {
      throw new Error(`Could not resolve desktop id for session ${sessionId}: ${JSON.stringify(desktop)}`);
    }
    const terminalPaneId = pane.paneId;
    await driver.pressKey('n', { command: true, option: true });
    const docked = await waitForDesktopUi(
      client,
      desktopId,
      (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1,
      'native Cmd+Opt+N to dock a fresh notebook tile',
      15_000,
    );
    const tileId = docked.tileIds[0];
    console.log(`[RealAppHarness] docked notebook tile=${tileId}`);

    await waitForFinder(client, true, 'fresh notebook tile auto-opens its finder');
    await driver.pressKeyCode(53);
    await waitForFinder(client, false, 'Esc dismisses the finder, leaving focus in the tile');
    await driver.pressKey('w', { command: true });

    const afterClose = await waitForDesktopUi(
      client,
      desktopId,
      (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 0,
      'native Cmd+W to undock the focused notebook tile',
      15_000,
    );

    const desktopAfter = await client.request('get_desktop', { sessionId });
    const panesAfter = desktopAfter?.panes ?? [];
    const terminalSurvived = panesAfter.some((p) => p.paneId === terminalPaneId);
    if (!terminalSurvived) {
      throw new Error(
        `Cmd+W in the notebook tile closed the terminal pane instead of the tile. `
        + `Expected pane ${terminalPaneId} to survive; panes after = ${JSON.stringify(panesAfter)}`,
      );
    }
    const state = await client.request('get_state');
    const sessionSurvived = (state.sessions || []).some((s) => s.id === sessionId);
    if (!sessionSurvived) {
      throw new Error(`Cmd+W in the notebook tile ended session ${sessionId} instead of closing the tile.`);
    }

    const summary = {
      ok: true,
      runId,
      desktopId,
      tileId,
      terminalPaneId,
      tileIdsAfter: afterClose.tileIds,
      panesAfter: panesAfter.map((p) => p.paneId),
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Notebook tile Cmd+W close (native menu → session.close → undock tile) passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (sessionId) {
      await closeDesktopPanes(client, sessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
