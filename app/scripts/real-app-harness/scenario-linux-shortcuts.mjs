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
import { createWindowDriver, delay } from './platform.mjs';
import {
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return {
    options: parseCommonArgs(args),
    help: args.includes('--help') || args.includes('-h'),
  };
}

async function waitForActivePane(client, sessionId, paneId, description, timeoutMs = 10_000) {
  return waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => workspace?.activePaneId === paneId,
    description,
    timeoutMs,
  );
}

async function waitForPicker(client, title, timeoutMs = 10_000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await client.request('location_picker_get_state');
    if (last?.open && last.title === title) return last;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${title}. Last picker state:\n${JSON.stringify(last, null, 2)}`);
}

async function waitForSelector(client, selector, timeoutMs = 10_000) {
  const deadline = Date.now() + timeoutMs;
  let lastError = null;
  while (Date.now() < deadline) {
    try {
      return await client.request('capture_screenshot_data', { selector }, { timeoutMs: 20_000 });
    } catch (error) {
      lastError = error;
      await delay(150);
    }
  }
  throw new Error(`Timed out waiting for ${selector}: ${lastError?.message || lastError}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-linux-shortcuts.mjs');
    return;
  }
  if (process.platform !== 'linux') {
    throw new Error('The Linux shortcut scenario must run on Linux.');
  }

  process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
  const runner = createScenarioRunner(options, {
    scenarioId: 'LINUX-SHORTCUTS',
    tier: 'tier1-local-shell',
    prefix: 'linux-shortcuts',
    metadata: {
      agent: 'shell',
      focus: 'xdotool drives Ctrl+Shift app actions while plain Ctrl+W reaches bash readline',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath });
  let sessionId = null;
  let primaryPane = null;
  let utilityPane = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session_panes', async () => {
    if (!sessionId) return;
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    for (const pane of workspace?.panes || []) {
      await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    }
  });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_shell', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `linux-shortcuts-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
      });
      await client.request('select_session', { sessionId });
      const workspace = await client.request('get_workspace', { sessionId });
      primaryPane = workspace?.panes?.[0] ?? null;
      runner.assert(Boolean(primaryPane?.paneId), `No initial shell pane: ${JSON.stringify(workspace)}`);
      await waitForPaneVisible(client, sessionId, primaryPane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, primaryPane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, primaryPane.paneId, {
        timeoutMs: 20_000,
        description: 'Linux shortcut shell prompt',
      });
    });

    await runner.step('plain_ctrl_w_reaches_bash', async () => {
      await client.request('focus_pane', { sessionId, paneId: primaryPane.paneId });
      await driver.typeText('echo CTRL_W_SHOULD_DISAPPEAR');
      await driver.pressKey('w', { control: true });
      await driver.typeText('CTRL_W_REACHED_BASH');
      await driver.pressEnter();
      await waitForPaneText(
        client,
        sessionId,
        primaryPane.paneId,
        (text) => text.includes('CTRL_W_REACHED_BASH'),
        'bash readline to delete the prior word on Ctrl+W',
        15_000,
      );
    });

    await runner.step('ctrl_shift_n_opens_new_session', async () => {
      await client.request('focus_pane', { sessionId, paneId: primaryPane.paneId });
      await driver.pressKey('n', { control: true, shift: true });
      await waitForPicker(client, 'New Session Location');
      await driver.pressKeyCode(53);
    });

    await runner.step('ctrl_shift_k_opens_action_menu', async () => {
      await client.request('focus_pane', { sessionId, paneId: primaryPane.paneId });
      await driver.pressKey('k', { control: true, shift: true });
      await waitForSelector(client, '.action-menu');
      await driver.pressKeyCode(53);
    });

    await runner.step('ctrl_shift_d_splits_and_arrows_move_focus', async () => {
      await client.request('focus_pane', { sessionId, paneId: primaryPane.paneId });
      await driver.pressKey('d', { control: true, shift: true });
      const workspace = await waitForSessionWorkspace(
        client,
        sessionId,
        (entry) => (entry?.panes || []).length === 2,
        'Ctrl+Shift+D utility split',
        20_000,
      );
      utilityPane = workspace.panes.find((pane) => pane.paneId !== primaryPane.paneId);
      runner.assert(Boolean(utilityPane?.paneId), `No utility pane after split: ${JSON.stringify(workspace)}`);
      await waitForPaneVisible(client, sessionId, utilityPane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, utilityPane.paneId, 20_000);
      await waitForActivePane(client, sessionId, utilityPane.paneId, 'new utility pane to hold focus');

      await driver.pressKey('Up', { control: true, shift: true });
      await waitForActivePane(client, sessionId, primaryPane.paneId, 'Ctrl+Shift+Up to focus the upper pane');
      await driver.pressKey('Down', { control: true, shift: true });
      await waitForActivePane(client, sessionId, utilityPane.paneId, 'Ctrl+Shift+Down to focus the lower pane');
    });

    await runner.step('ctrl_shift_w_closes_focused_pane', async () => {
      await driver.pressKey('w', { control: true, shift: true });
      await waitForSessionWorkspace(
        client,
        sessionId,
        (workspace) => (workspace?.panes || []).length === 1
          && workspace.panes[0]?.paneId === primaryPane.paneId,
        'Ctrl+Shift+W to close the focused utility pane',
        20_000,
      );
    });

    let screenshotPath;
    await runner.step('ctrl_shift_slash_opens_linux_cheatsheet', async () => {
      await client.request('focus_pane', { sessionId, paneId: primaryPane.paneId });
      await driver.pressKey('/', { control: true, shift: true });
      await waitForSelector(client, '.shortcuts-modal');
      screenshotPath = path.join(runner.runDir, 'linux-shortcuts-cheatsheet.png');
      await driver.screenshot(screenshotPath);
      runner.assert(fs.existsSync(screenshotPath), `Missing cheat sheet screenshot: ${screenshotPath}`);
    });

    const summary = await runner.finishSuccess({
      sessionId,
      primaryPaneId: primaryPane.paneId,
      closedPaneId: utilityPane.paneId,
      screenshotPath,
    });
    console.log('[RealAppHarness] Linux shortcuts passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
