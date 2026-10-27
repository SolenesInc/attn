#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import { createWindowDriver } from './platform.mjs';
import { ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import { sleep, waitForFirstDesktopPane, waitForPaneInputFocus, waitForPaneText } from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  const options = parseCommonArgs(args);
  if (args.includes('--help') || args.includes('-h')) {
    printCommonHelp('scripts/real-app-harness/scenario-claude-shared-conversation.mjs');
    return;
  }
  const runner = createScenarioRunner(options, {
    scenarioId: 'CLAUDE-SHARED-CONVERSATION', tier: 'tier2-local-mock-agent', prefix: 'claude-shared-conversation',
    metadata: { agent: 'claude', focus: 'two Claude tiles on one conversation both warn, above their terminals, until one closes' },
  });
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const run = { first: null, second: null, firstPane: null, secondPane: null, conversation: null, rows: {} };
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_panes', async () => {
    for (const [sessionId, paneId] of [[run.second, run.secondPane], [run.first, run.firstPane]]) {
      if (sessionId && paneId && observer.getSession(sessionId)) await client.request('close_pane', { sessionId, paneId });
    }
  });
  const rows = async (sessionId, paneId) => (await client.request('get_pane_state', { sessionId, paneId })).pane?.size?.rows ?? 0;
  const poll = async (read, description, timeoutMs = 10_000) => {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const found = await read();
      if (found) return found;
      if (Date.now() >= deadline) throw new Error(`timed out after ${timeoutMs}ms waiting for ${description}`);
      await sleep(100);
    }
  };
  const sharedWith = (sessionId) => observer.getSession(sessionId)?.conversation_shared_with ?? [];

  try {
    await runner.step('two_claude_tiles', async () => {
      const cwd = path.join(runner.sessionDir, 'shop');
      fs.mkdirSync(cwd, { recursive: true });
      writeMockAgentFixture(cwd, {
        name: 'shared conversation mock',
        resumable: true,
        minimumWorkingMs: 0,
        turns: [{ includes: 'PLAN_THE_RELEASE', actions: [{ type: 'reply', text: 'Release planned', state: 'waiting_input' }] }],
      });
      await launchFreshAppAndConnect(client, observer);
      run.first = await createSessionAndWaitForInitialPane({
        client, observer, cwd, label: `release-${runner.runId}`, agent: 'claude', promptReadyFn: ensureClaudePromptReadyViaPty,
      });
      run.firstPane = (await waitForFirstDesktopPane(client, run.first, 'first agent pane')).paneId;
      run.conversation = observer.terminalOf(run.first);
      await client.request('select_session', { sessionId: run.first });
      await waitForPaneInputFocus(client, run.first, run.firstPane);
      await driver.typeText('PLAN_THE_RELEASE');
      await driver.pressEnter();
      await waitForPaneText(client, run.first, run.firstPane, (text) => text.includes('Release planned'), 'first reply');

      run.second = await createSessionAndWaitForInitialPane({
        client, observer, cwd, label: `review-${runner.runId}`, agent: 'claude', promptReadyFn: ensureClaudePromptReadyViaPty, ownDesktop: false,
      });
      run.secondPane = (await waitForFirstDesktopPane(client, run.second, 'second agent pane')).paneId;
      run.rows = { first: await rows(run.first, run.firstPane), second: await rows(run.second, run.secondPane) };
      runner.assert(sharedWith(run.first).length === 0 && sharedWith(run.second).length === 0,
        'neither session warns while each runs its own conversation', { first: sharedWith(run.first), second: sharedWith(run.second) });
    });

    await runner.step('resume_in_the_second_tile_warns_both_above_their_terminals', async () => {
      await client.request('select_session', { sessionId: run.second });
      await waitForPaneInputFocus(client, run.second, run.secondPane);
      await driver.typeText(`/resume ${run.conversation}`);
      await driver.pressEnter();
      await observer.waitFor(() => sharedWith(run.first).includes(run.second) && sharedWith(run.second).includes(run.first),
        'both sessions marked as sharing one conversation', 15_000);
      const shrunk = await poll(async () => {
        const now = { first: await rows(run.first, run.firstPane), second: await rows(run.second, run.secondPane) };
        return now.first < run.rows.first && now.second < run.rows.second ? now : null;
      }, 'both terminals to give rows to the notice');
      runner.writeText('rows-while-warning.json', JSON.stringify({ before: run.rows, after: shrunk }, null, 2));
      await captureScreenshotData(path.join(runner.runDir, 'both-tiles-warn.png'), { client });
    });

    await runner.step('closing_one_tile_clears_the_other', async () => {
      await client.request('close_pane', { sessionId: run.second, paneId: run.secondPane });
      run.secondPane = null;
      await observer.waitFor(() => sharedWith(run.first).length === 0, 'the remaining session no longer warns', 15_000);
      await poll(async () => (await rows(run.first, run.firstPane)) >= run.rows.first, 'the remaining terminal to take its rows back');
      await captureScreenshotData(path.join(runner.runDir, 'warning-cleared.png'), { client });
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
