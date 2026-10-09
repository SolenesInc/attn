#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import { sleep, waitForFirstDesktopPane, waitForPaneInputFocus, waitForPaneText } from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  const options = parseCommonArgs(args);
  if (args.includes('--help') || args.includes('-h')) {
    printCommonHelp('scripts/real-app-harness/scenario-clear-opens-session.mjs');
    return;
  }
  const runner = createScenarioRunner(options, {
    scenarioId: 'CLEAR-OPENS-SESSION', tier: 'tier2-local-mock-agent', prefix: 'clear-opens-session',
    metadata: { agent: 'claude', focus: '/clear opens a new session in the focused pane and closes the old one into the ledger' },
  });
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const cli = (...cliArgs) => execFileSync(appDaemonInTree(options.appPath), cliArgs, {
    encoding: 'utf8', env: instanceCliEnv(currentHarnessInstance()),
  });
  const label = `checkout-${runner.runId}`;
  const run = { first: null, next: null, paneId: null, terminal: null };
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_pane', async () => {
    const live = [run.next, run.first].find((id) => id && observer.getSession(id));
    if (live && run.paneId) await client.request('close_pane', { sessionId: live, paneId: run.paneId });
  });
  const row = async (sessionId) => (await client.request('get_session_ui_state', { sessionId })).sidebarItem;
  const placeInDesktop = async (sessionId) => {
    const item = await row(sessionId);
    const desktop = observer.desktopOf(sessionId);
    if (!item || !desktop) return null;
    const group = await client.request('dom_bounds', { selector: `[data-testid="sidebar-desktop-${desktop.id}"]` });
    return { ...item, offset: item.bounds.y - group.bounds.y };
  };
  const poll = async (read, description, timeoutMs = 10_000) => {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const found = await read();
      if (found) return found;
      if (Date.now() >= deadline) throw new Error(`timed out after ${timeoutMs}ms waiting for ${description}`);
      await sleep(100);
    }
  };

  try {
    await runner.step('launch_and_take_a_turn', async () => {
      const cwd = path.join(runner.sessionDir, 'shop');
      fs.mkdirSync(cwd, { recursive: true });
      writeMockAgentFixture(cwd, {
        name: 'clear mock',
        turns: [
          { includes: 'PRICE_THE_CART', actions: [{ type: 'reply', text: 'Priced the cart', state: 'waiting_input' }] },
          { includes: 'ADD_SHIPPING', actions: [{ type: 'reply', text: 'Shipping added', state: 'waiting_input' }] },
        ],
      });
      await launchFreshAppAndConnect(client, observer);
      run.first = await createSessionAndWaitForInitialPane({
        client, observer, cwd, label, agent: 'claude', promptReadyFn: ensureClaudePromptReadyViaPty,
      });
      run.paneId = (await waitForFirstDesktopPane(client, run.first, 'agent pane')).paneId;
      run.terminal = observer.terminalOf(run.first);
      await client.request('select_session', { sessionId: run.first });
      await waitForPaneInputFocus(client, run.first, run.paneId);
      await driver.typeText('PRICE_THE_CART');
      await driver.pressEnter();
      await waitForPaneText(client, run.first, run.paneId, (text) => text.includes('Priced the cart'), 'first reply');
      await observer.waitFor(() => observer.getSession(run.first)?.state === 'waiting_input', 'first turn ended');
    });

    await runner.step('clear_replaces_the_row_in_place_and_keeps_focus', async () => {
      const before = await placeInDesktop(run.first);
      runner.assert(before?.text.includes(label), 'the sidebar lists the session before /clear', { before });
      await driver.typeText('/clear');
      await driver.pressEnter();
      const successor = await observer.waitFor(
        () => [...observer.sessionsById.values()].find((session) => session.succeeds === run.first),
        'the session /clear opened',
        15_000,
      );
      run.next = successor.id;
      await observer.waitFor(() => !observer.getSession(run.first), 'the cleared session leaves the live sessions');
      runner.assert(observer.terminalOf(run.next) === run.terminal, 'the new session runs in the same terminal', {
        terminal: run.terminal, now: observer.terminalOf(run.next),
      });
      runner.assert(successor.label !== label, 'the new session gets a new name', { label: successor.label });
      await poll(async () => !(await row(run.first)), 'the cleared session to leave the sidebar');
      const after = await poll(() => placeInDesktop(run.next), 'the new session in the sidebar');
      const ui = await client.request('get_session_ui_state', { sessionId: run.next });
      runner.assert(ui.selected, 'the new session stays selected', { ui });
      runner.assert(!after.text.includes(label) && Math.abs(after.offset - before.offset) <= 1,
        'the new session takes the cleared one\'s sidebar row', { before, after });
      await waitForPaneInputFocus(client, run.next, run.paneId);
    });

    await runner.step('typing_continues_into_the_new_session', async () => {
      await driver.typeText('ADD_SHIPPING');
      await driver.pressEnter();
      await waitForPaneText(client, run.next, run.paneId, (text) => text.includes('Shipping added'), 'reply after /clear');
      await observer.waitFor(() => observer.getSession(run.next)?.state === 'waiting_input', 'the new session\'s turn ended');
    });

    await runner.step('ledger_lists_the_cleared_session', async () => {
      const closed = cli('session', 'list', '--closed');
      runner.assert(closed.includes(run.first), 'session list --closed lists the cleared session', { closed });
      const shown = cli('session', 'show', run.first);
      runner.assert(/^state\s+closed$/m.test(shown), 'session show reports the cleared session closed', { shown });
      runner.writeText('session-show.txt', shown);
    });

    console.log(JSON.stringify(await runner.finishSuccess(run), null, 2));
  } catch (error) {
    await captureScreenshotData(path.join(runner.runDir, 'failure.png'), { client }).catch(() => {});
    const result = await runner.finishFailure(error, run);
    console.error(result.error);
    process.exitCode = 1;
  }
}

main().catch((error) => { console.error(error.stack || error); process.exitCode = 1; });
