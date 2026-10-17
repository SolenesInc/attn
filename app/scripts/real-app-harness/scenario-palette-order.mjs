#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { waitForFirstDesktopPane, waitForPaneInputFocus } from './scenarioAssertions.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  const options = parseCommonArgs(args);
  if (args.includes('--help') || args.includes('-h')) {
    printCommonHelp('scripts/real-app-harness/scenario-palette-order.mjs');
    return;
  }
  const runner = createScenarioRunner(options, {
    scenarioId: 'PALETTE-ORDER', tier: 'tier2-local-mock-agent', prefix: 'palette-order',
  });
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  let agent;
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_agent', async () => {
    if (agent) await client.request('close_pane', agent);
  });
  const input = '[role="combobox"][aria-label="Commands"]';
  const selected = '.unified-palette-option[aria-selected="true"]';
  const wait = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: observer.connectTimeoutMs, ...extra });
  const open = async () => {
    await pressShortcutKeys(client, driver, 'ui.commandPalette');
    await wait(input, { focused: true });
    await wait('.unified-palette-option');
  };
  const escape = async () => {
    await driver.pressKey('Escape');
    await wait('[role="dialog"][aria-label="Commands"]', { absent: true });
  };
  try {
    await runner.step('launch_agent', async () => {
      await launchFreshAppAndConnect(client, observer);
      const cwd = path.join(runner.sessionDir, 'agent');
      fs.mkdirSync(cwd, { recursive: true });
      const sessionId = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: 'Palette agent', agent: 'claude' });
      const pane = await waitForFirstDesktopPane(client, sessionId, 'palette agent pane');
      agent = { sessionId, paneId: pane.paneId };
      await client.request('select_session', { sessionId });
      await waitForPaneInputFocus(client, sessionId, pane.paneId);
    });
    await runner.step('command_mode_selection_and_learning', async () => {
      await open();
      await driver.typeText('keyboard shortcuts');
      await wait(selected, { textIncludes: 'Keyboard shortcuts' });
      await driver.pressEnter();
      await wait('[role="dialog"][aria-labelledby="shortcuts-modal-title"]');
      await driver.pressKey('Escape');
      await wait('[role="dialog"][aria-labelledby="shortcuts-modal-title"]', { absent: true });
      await open();
      const text = (await client.request('dom_text', { selector: '.unified-palette-list' })).text;
      for (const title of ['Open in editor', 'Keyboard shortcuts', 'New agent']) runner.assert(text.includes(title), `palette contains ${title}`);
      runner.assert(text.indexOf('Open in editor') < text.indexOf('Keyboard shortcuts'), 'active-agent editor remains pinned');
      runner.assert(text.indexOf('Keyboard shortcuts') < text.indexOf('New agent'), 'selected command rises above unused commands');
      await captureScreenshotData(path.join(runner.runDir, 'learned-order.png'), { client });
      await driver.typeText('keyboard shortcuts');
      await wait(selected, { textIncludes: 'Keyboard shortcuts' });
      await escape();
      await waitForPaneInputFocus(client, agent.sessionId, agent.paneId);
    });
    await runner.step('agent_palette_enters_command_mode', async () => {
      await pressShortcutKeys(client, driver, 'ui.actionMenu');
      await wait('[role="combobox"][aria-label="Agents"]', { focused: true });
      await driver.typeText('>keyboard shortcuts');
      await wait(input, { focused: true });
      await wait(selected, { textIncludes: 'Keyboard shortcuts' });
      await driver.pressEnter();
      await wait('[role="dialog"][aria-labelledby="shortcuts-modal-title"]');
      await driver.pressKey('Escape');
      await wait('[role="dialog"][aria-labelledby="shortcuts-modal-title"]', { absent: true });
      await waitForPaneInputFocus(client, agent.sessionId, agent.paneId);
    });
    await runner.step('searchable_priority_command_marks_and_clears_the_sidebar_flag', async () => {
      const marker = `[data-testid="sidebar-session-${agent.sessionId}"] [aria-label="Priority"]`;
      for (const priority of [true, false]) {
        await pressShortcutKeys(client, driver, 'ui.actionMenu');
        await wait('[role="combobox"][aria-label="Agents"]', { focused: true });
        await driver.typeText('>priority');
        await wait(input, { focused: true });
        await wait(selected, { textIncludes: priority ? 'Mark as priority' : 'Clear priority' });
        await driver.pressEnter();
        await wait('[role="dialog"][aria-label="Commands"]', { absent: true });
        await observer.waitFor(() => Boolean(observer.getSession(agent.sessionId)?.priority) === priority,
          `current session priority is ${priority}`, observer.connectTimeoutMs);
        await wait(marker, { absent: !priority });
        await waitForPaneInputFocus(client, agent.sessionId, agent.paneId);
        await captureScreenshotData(path.join(runner.runDir, priority ? 'priority-marked.png' : 'priority-cleared.png'), { client });
      }
    });
    await runner.step('rename_active_session_and_cancel', async () => {
      const renameInput = '.rename-popover-input[aria-label="Rename session"]';
      const header = `[data-pane-id="${agent.paneId}"] .desktop-pane-header`;
      const openRename = async () => {
        await pressShortcutKeys(client, driver, 'ui.actionMenu');
        await wait('[role="combobox"][aria-label="Agents"]', { focused: true });
        await driver.typeText('>rename');
        await wait(selected, { textIncludes: 'Rename this session' });
        await driver.pressEnter();
        await wait(renameInput, { focused: true });
      };
      await openRename();
      await driver.typeText('Renamed palette agent');
      await driver.pressEnter();
      await wait('.rename-popover', { absent: true });
      await wait(header, { textIncludes: 'Renamed palette agent' });
      await wait('[aria-label="Open Renamed palette agent"]');
      await waitForPaneInputFocus(client, agent.sessionId, agent.paneId);
      await captureScreenshotData(path.join(runner.runDir, 'renamed-session.png'), { client });
      await openRename();
      await driver.typeText('Cancelled palette name');
      await driver.pressKey('Escape');
      await wait('.rename-popover', { absent: true });
      await wait(header, { textIncludes: 'Renamed palette agent' });
      await wait('[aria-label="Open Renamed palette agent"]');
      await waitForPaneInputFocus(client, agent.sessionId, agent.paneId);
      await openRename();
      await driver.typeText('x'.repeat(49));
      await driver.pressEnter();
      await wait('.rename-popover-error', { textIncludes: '48-character limit' });
      await driver.pressKey('Escape');
      await wait('.rename-popover', { absent: true });
    });
    console.log(JSON.stringify(await runner.finishSuccess({ agent }), null, 2));
  } catch (error) {
    await captureScreenshotData(path.join(runner.runDir, 'failure.png'), { client }).catch(() => {});
    const result = await runner.finishFailure(error, { agent });
    console.error(result.error);
    process.exitCode = 1;
  }
}
main().catch((error) => { console.error(error.stack || error); process.exitCode = 1; });
