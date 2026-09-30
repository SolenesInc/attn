#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs,
  pressShortcutKeys, printCommonHelp, submitPrompt,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane, waitForPaneInputFocus, waitForPaneText } from './scenarioAssertions.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  const options = parseCommonArgs(args);
  if (args.includes('--help') || args.includes('-h')) {
    printCommonHelp('scripts/real-app-harness/scenario-snooze-keyboard.mjs');
    return;
  }
  const runner = createScenarioRunner(options, {
    scenarioId: 'SNOOZE-KEYBOARD', tier: 'tier2-local-mock-agent', prefix: 'snooze-keyboard',
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const agents = [];
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_sessions', async () => {
    for (const agent of agents) {
      const workspace = await client.request('get_workspace', { sessionId: agent.sessionId });
      for (const pane of [...workspace.panes].reverse()) {
        await client.request('close_pane', { sessionId: agent.sessionId, paneId: pane.paneId });
      }
    }
  });
  // Native palette opening took 420ms locally; this guards a hung UI, not input timing.
  const waitDom = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
  const focusedChoice = (id) => waitDom(`[data-testid="snooze-choice-${id}"]`, { focused: true });
  const palette = async (query) => {
    await pressShortcutKeys(client, driver, 'ui.actionMenu');
    await waitDom('[aria-label="Search actions"]', { focused: true });
    await driver.typeText(query);
    await waitDom('.action-menu-item', { textIncludes: query === 'snooze' ? 'Snooze this agent' : 'Wake this agent' });
    await driver.pressEnter();
  };
  const openPicker = async () => {
    await palette('snooze');
    await focusedChoice('30m');
  };
  const select = async (agent) => {
    await client.request('select_session', { sessionId: agent.sessionId });
    await waitForPaneInputFocus(client, agent.sessionId, agent.paneId);
  };
  const centered = async (agent) => {
    const { bounds: pane } = await client.request('dom_bounds', { selector: `[data-pane-id="${agent.paneId}"]` });
    const { bounds: menu } = await client.request('dom_bounds', { selector: '[data-testid="snooze-menu"]' });
    const dx = Math.abs(menu.x + menu.width / 2 - pane.x - pane.width / 2);
    const dy = Math.abs(menu.y + menu.height / 2 - pane.y - pane.height / 2);
    // dom_bounds rounds each edge to CSS pixels; centers can differ by one pixel.
    runner.assert(dx <= 1 && dy <= 1, `picker centered in agent pane: ${JSON.stringify({ pane, menu, dx, dy })}`);
    runner.log('picker placement', { pane, menu, dx, dy });
  };
  try {
    await runner.step('launch_and_prepare_agents', async () => {
      await launchFreshAppAndConnect(client, observer);
      const originalSettings = ['queue_mode_enabled', 'auto_settle_enabled'].map((key) => [key, observer.getSetting(key)]);
      runner.registerCleanup('restore_attention_settings', async () => {
        for (const [key, value] of originalSettings) await client.request('set_setting', { key, value });
      });
      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' });
      for (const label of ['alpha', 'beta']) {
        const cwd = path.join(runner.sessionDir, label);
        fs.mkdirSync(cwd, { recursive: true });
        writeMockAgentFixture(cwd, {
          name: 'snooze keyboard mock',
          turns: [{ includes: 'READY', actions: [{ type: 'reply', text: 'Ready for keyboard snooze', state: 'waiting_input' }] }],
        });
        const sessionId = await createSessionAndWaitForInitialPane({
          client, observer, cwd, label, agent: 'claude', promptReadyFn: ensureClaudePromptReadyViaPty,
        });
        const pane = await waitForFirstWorkspacePane(client, sessionId, `${label} pane`);
        const agent = { sessionId, paneId: pane.paneId };
        agents.push(agent);
        await submitPrompt(client, sessionId, pane.paneId, 'READY');
        await waitForPaneText(client, sessionId, pane.paneId, (text) => text.includes('Ready for keyboard snooze'), 'ready reply');
        await observer.waitFor(() => observer.getSession(sessionId)?.state === 'waiting_input', `${label} stopped`);
      }
    });
    const [alpha, beta] = agents;
    await runner.step('palette_focus_keys_and_cancel', async () => {
      await select(alpha);
      const before = await client.request('read_pane_text', alpha);
      await openPicker();
      await centered(alpha);
      const menuBefore = await client.request('dom_bounds', { selector: '[data-testid="snooze-menu"]' });
      const scaleBefore = observer.getSetting('uiScale');
      for (const shortcut of ['ui.openSettings', 'ui.showShortcuts', 'sessions.open', 'ui.increaseFontSize', 'ui.decreaseFontSize', 'ui.resetFontSize', 'ui.actionMenu']) {
        await pressShortcutKeys(client, driver, shortcut);
        await focusedChoice('30m');
      }
      const menuAfter = await client.request('dom_bounds', { selector: '[data-testid="snooze-menu"]' });
      runner.assert(JSON.stringify(menuAfter.bounds) === JSON.stringify(menuBefore.bounds), 'always-on shortcuts leave picker geometry unchanged');
      runner.assert(observer.getSetting('uiScale') === scaleBefore, 'font shortcuts do not persist scale changes');
      await driver.pressKey('ArrowUp');
      await focusedChoice('monday');
      await driver.pressKeyCode(115);
      await focusedChoice('30m');
      await driver.pressKey('ArrowDown');
      await focusedChoice('1h');
      await driver.pressKeyCode(48);
      await focusedChoice('1h');
      await driver.pressKeyCode(48, { shift: true });
      await focusedChoice('1h');
      await driver.pressKeyCode(119);
      await focusedChoice('monday');
      await pressShortcutKeys(client, driver, 'session.next');
      runner.assert((await client.request('get_state')).activeSessionId === alpha.sessionId, 'navigation stays with picker target');
      await driver.pressKey('Escape');
      await waitDom('[data-testid="snooze-menu"]', { absent: true });
      await waitForPaneInputFocus(client, alpha.sessionId, alpha.paneId);
      runner.assert(!observer.getSession(alpha.sessionId)?.turn_snoozed_until, 'cancel sends no snooze');
      runner.assert((await client.request('read_pane_text', alpha)).text === before.text, 'picker keys do not change terminal text');
      await driver.typeText('CANCEL_FOCUS');
      await waitForPaneText(client, alpha.sessionId, alpha.paneId, (text) => text.includes('CANCEL_FOCUS'), 'typing after cancel');
    });
    await runner.step('keyboard_row_entry_returns_focus_to_button', async () => {
      const selector = `[data-testid="queue-snooze-${alpha.sessionId}"]`;
      await client.request('dom_focus', { selector });
      await driver.pressEnter();
      await focusedChoice('30m');
      const { bounds: button } = await client.request('dom_bounds', { selector });
      const { bounds: menu } = await client.request('dom_bounds', { selector: '[data-testid="snooze-menu"]' });
      runner.assert(menu.x === button.x, 'row entry remains anchored to its button');
      await driver.pressKey('Escape');
      await waitDom(selector, { focused: true });
    });
    await runner.step('outside_click_preserves_the_selected_destination', async () => {
      await select(alpha);
      await openPicker();
      await client.request('dom_click', { selector: `[data-testid="queue-select-${beta.sessionId}"]` });
      await waitDom('[data-testid="snooze-menu"]', { absent: true });
      await waitForPaneInputFocus(client, beta.sessionId, beta.paneId);
      runner.assert(!observer.getSession(alpha.sessionId)?.turn_snoozed_until, 'outside dismissal sends no snooze');
    });
    await runner.step('center_in_split_pane_and_with_sidebar_hidden', async () => {
      await client.request('split_pane', { sessionId: alpha.sessionId, targetPaneId: alpha.paneId, direction: 'vertical' });
      await select(alpha);
      await pressShortcutKeys(client, driver, 'session.toggleSidebar');
      await openPicker();
      await centered(alpha);
      const workspaceBefore = await client.request('get_workspace', { sessionId: alpha.sessionId });
      for (const shortcut of ['terminal.splitVertical', 'terminal.splitHorizontal', 'terminal.focusRight', 'terminal.find']) {
        await pressShortcutKeys(client, driver, shortcut);
        await focusedChoice('30m');
      }
      const workspaceAfter = await client.request('get_workspace', { sessionId: alpha.sessionId });
      runner.assert(workspaceAfter.panes.length === workspaceBefore.panes.length, 'picker blocks split shortcuts');
      runner.assert(workspaceAfter.activePaneId === workspaceBefore.activePaneId, 'picker blocks pane navigation');
      await captureScreenshotData(path.join(runner.runDir, 'centered-snooze.png'), { client });
      await driver.pressKey('Escape');
      await waitForPaneInputFocus(client, alpha.sessionId, alpha.paneId);
      await pressShortcutKeys(client, driver, 'session.toggleSidebar');
    });
    await runner.step('new_durations_confirm_and_handover_then_keyboard_wake', async () => {
      for (const [choice, hours, moves] of [['2h', 2, 2], ['4h', 4, 3]]) {
        await select(alpha);
        const openedBefore = Date.now();
        await openPicker();
        for (let i = 0; i < moves; i += 1) await driver.pressKey('ArrowDown');
        await focusedChoice(choice);
        await driver.pressEnter();
        const chosenAfter = Date.now();
        await observer.waitFor(() => observer.getSession(alpha.sessionId)?.turn_snoozed_until, `${choice} deadline`);
        const until = Date.parse(observer.getSession(alpha.sessionId).turn_snoozed_until);
        const duration = hours * 60 * 60 * 1000;
        runner.assert(until >= openedBefore + duration && until <= chosenAfter + duration, `${choice} deadline matches the opening instant`);
        await waitForPaneInputFocus(client, beta.sessionId, beta.paneId);
        await driver.typeText(`HANDOVER_${choice}`);
        await waitForPaneText(client, beta.sessionId, beta.paneId, (text) => text.includes(`HANDOVER_${choice}`), 'typing after handover');
        await select(alpha);
        await palette('wake');
        await observer.waitFor(() => !observer.getSession(alpha.sessionId)?.turn_snoozed_until, 'keyboard wake cleared deadline');
        await waitForPaneInputFocus(client, alpha.sessionId, alpha.paneId);
      }
    });
    await runner.step('non_active_row_confirmation_keeps_a_stable_destination', async () => {
      for (const fromHome of [false, true]) {
        await select(alpha);
        if (fromHome) {
          await pressShortcutKeys(client, driver, 'session.goToDashboard');
          await waitDom('[data-testid="sidebar-home"][aria-current="page"]');
        }
        await client.request('dom_focus', { selector: `[data-testid="queue-snooze-${beta.sessionId}"]` });
        await driver.pressEnter();
        await focusedChoice('30m');
        await driver.pressEnter();
        await observer.waitFor(() => observer.getSession(beta.sessionId)?.turn_snoozed_until, 'non-active row snoozed');
        await waitDom(`[data-testid="queue-snooze-${beta.sessionId}"]`, { absent: true });
        if (fromHome) {
          await waitDom('[data-testid="sidebar-home"]', { focused: true });
          await pressShortcutKeys(client, driver, 'ui.actionMenu');
          await waitDom('[aria-label="Search actions"]', { focused: true });
          await driver.pressKey('Escape');
        } else {
          await waitForPaneInputFocus(client, alpha.sessionId, alpha.paneId);
          await driver.typeText('NON_ACTIVE_ROW');
          await waitForPaneText(client, alpha.sessionId, alpha.paneId, (text) => text.includes('NON_ACTIVE_ROW'), 'typing after non-active snooze');
        }
        await select(beta);
        await palette('wake');
        await observer.waitFor(() => !observer.getSession(beta.sessionId)?.turn_snoozed_until, 'non-active row woken');
      }
    });
    await runner.step('changed_selection_cancels_into_current_pane', async () => {
      await select(alpha);
      await openPicker();
      await client.request('select_session', { sessionId: beta.sessionId });
      await focusedChoice('30m');
      await driver.pressKey('Escape');
      await waitForPaneInputFocus(client, beta.sessionId, beta.paneId);
      runner.assert(!observer.getSession(alpha.sessionId)?.turn_snoozed_until, 'selection change then cancel sends no snooze');
    });
    await runner.step('empty_queue_returns_to_home_with_live_keyboard', async () => {
      observer.send({ cmd: 'settle_turn', session_id: beta.sessionId });
      await observer.waitFor(() => !observer.getSession(beta.sessionId)?.turn_owed, 'beta settled');
      await select(alpha);
      await openPicker();
      await driver.pressKeyCode(49);
      await observer.waitFor(() => observer.getSession(alpha.sessionId)?.turn_snoozed_until, 'Space confirms snooze');
      await waitDom('[data-testid="all-settled"]');
      runner.assert((await client.request('get_state')).activeSessionId === null, 'empty queue returns home');
      await pressShortcutKeys(client, driver, 'ui.actionMenu');
      await waitDom('[aria-label="Search actions"]', { focused: true });
      await driver.pressKey('Escape');
    });
    await runner.step('snoozed_section_inspection_and_keyboard_wake', async () => {
      await client.request('dom_focus', { selector: '[data-testid="session-group-snoozed-header"]' });
      await driver.pressEnter();
      const selector = `[data-testid="session-wake-${alpha.sessionId}"]`;
      await waitDom(selector);
      await client.request('dom_focus', { selector });
      await driver.pressEnter();
      await observer.waitFor(() => !observer.getSession(alpha.sessionId)?.turn_snoozed_until, 'section wake cleared deadline');
      await waitDom('[data-testid="session-group-snoozed"]', { absent: true });
      await waitForPaneInputFocus(client, alpha.sessionId, alpha.paneId);
      runner.assert((await client.request('get_state')).activeSessionId === alpha.sessionId, 'home follows the newly woken turn');
    });
    console.log(JSON.stringify(await runner.finishSuccess({ agents }), null, 2));
  } catch (error) {
    await captureScreenshotData(path.join(runner.runDir, 'failure.png'), { client }).catch(() => {});
    const result = await runner.finishFailure(error, { agents });
    console.error(result.error);
    process.exitCode = 1;
  }
}
main().catch((error) => { console.error(error.stack || error); process.exitCode = 1; });
