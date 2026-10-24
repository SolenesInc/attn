#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import { resolveAttnBinary, scriptedAgent, startStubWorld, stubAgentModel, waitForPiPreflight } from './piStubProvider.mjs';
import { createWindowDriver } from './platform.mjs';
import { sleep, waitForFirstDesktopPane, waitForPaneInputFocus, waitForPaneText } from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const args = process.argv.slice(2).filter((arg) => arg !== '--');
const options = parseCommonArgs(args);
if (args.includes('--help') || args.includes('-h')) {
  printCommonHelp('scripts/real-app-harness/scenario-pi-shared-conversation.mjs');
  process.exit(0);
}
const instance = currentHarnessInstance();
if (!instance) throw new Error('the pi-shared-conversation scenario needs a named non-production instance');

const world = await startStubWorld({
  scenario: 'pi-shared-conversation', appPath: options.appPath, instance,
  agent: scriptedAgent([{ when: 'PLAN_THE_RELEASE', text: 'RELEASE_PLANNED' }]),
});
const runner = createScenarioRunner(options, {
  scenarioId: 'PI-SHARED-CONVERSATION', tier: 'tier2-local-real-agent', prefix: 'pi-shared-conversation',
  allowRealAgents: ['pi'], preflightLaunchEnv: world.launchEnv,
  metadata: { agent: 'pi', model: 'loopback stub', focus: 'two Pi tiles on one conversation both warn above their terminals until one closes' },
});
const client = new UiAutomationClient({ ...options, launchEnv: world.launchEnv });
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const attn = (argv) => execFileSync(resolveAttnBinary(options.appPath), argv, { env: instanceCliEnv(instance, world.launchEnv), encoding: 'utf8' });
const run = { first: null, second: null, firstPane: null, secondPane: null, rows: {} };
const sharedWith = (sessionId) => observer.getSession(sessionId)?.conversation_shared_with ?? [];
const rows = async (sessionId, paneId) => (await client.request('get_pane_state', { sessionId, paneId })).pane?.size?.rows ?? 0;
const poll = async (read, description, timeoutMs = 15_000) => {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const found = await read();
    if (found) return found;
    if (Date.now() >= deadline) throw new Error(`timed out after ${timeoutMs}ms waiting for ${description}`);
    await sleep(100);
  }
};
const type = async (sessionId, paneId, text) => {
  await client.request('select_session', { sessionId });
  await waitForPaneInputFocus(client, sessionId, paneId);
  await driver.typeText(text);
  await driver.pressEnter();
};
const openPi = async (cwd, label) => {
  const { sessionId } = await client.request('create_session', { cwd, label, agent: 'pi' });
  await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
  const { paneId } = await waitForFirstDesktopPane(client, sessionId, `${label} pane`, 30_000);
  await waitForPaneText(client, sessionId, paneId, (text) => text.replace(/\s+/g, ' ').includes('credential filtering: on'), `${label} ready`, 60_000);
  return { sessionId, paneId };
};

try {
  const cwd = path.join(runner.sessionDir, 'shop');
  fs.mkdirSync(cwd, { recursive: true });
  await world.launch({ client, observer, runner, pinModelFor: 'pi', launchApp: async () => {
    try { attn(['plugin', 'install-bundled', 'attn-pi']); }
    catch (error) { if (!String(error.stderr).includes('is already installed')) throw error; }
    await launchFreshAppAndConnect(client, observer);
  } });
  await waitForPiPreflight({
    run: () => attn(['preflight', '--agent', 'pi', '--model', stubAgentModel, '--json']),
    save: (attempts) => runner.writeJson('pi-preflight-attempts.json', attempts),
  });

  await runner.step('two_pi_tiles', async () => {
    ({ sessionId: run.first, paneId: run.firstPane } = await openPi(cwd, `release-${runner.runId}`));
    await type(run.first, run.firstPane, 'PLAN_THE_RELEASE');
    await waitForPaneText(client, run.first, run.firstPane, (text) => text.includes('RELEASE_PLANNED'), 'first reply', 60_000);
    ({ sessionId: run.second, paneId: run.secondPane } = await openPi(cwd, `review-${runner.runId}`));
    run.rows = { first: await rows(run.first, run.firstPane), second: await rows(run.second, run.secondPane) };
    runner.assert(sharedWith(run.first).length === 0 && sharedWith(run.second).length === 0,
      'neither session warns while each runs its own conversation', { first: sharedWith(run.first), second: sharedWith(run.second) });
  });

  await runner.step('resume_in_the_second_tile_warns_both_above_their_terminals', async () => {
    await type(run.second, run.secondPane, '/resume');
    await waitForPaneText(client, run.second, run.secondPane, (text) => text.includes('PLAN_THE_RELEASE'), 'the resume picker lists the first conversation', 30_000);
    await driver.typeText('PLAN_THE_RELEASE');
    await driver.pressEnter();
    await observer.waitFor(() => sharedWith(run.first).includes(run.second) && sharedWith(run.second).includes(run.first),
      'both sessions marked as sharing one conversation', 30_000);
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
    await observer.waitFor(() => sharedWith(run.first).length === 0, 'the remaining session no longer warns', 30_000);
    await poll(async () => (await rows(run.first, run.firstPane)) >= run.rows.first, 'the remaining terminal to take its rows back');
    await captureScreenshotData(path.join(runner.runDir, 'warning-cleared.png'), { client });
  });

  console.log(JSON.stringify(await runner.finishSuccess(run), null, 2));
} catch (error) {
  await captureScreenshotData(path.join(runner.runDir, 'failure.png'), { client }).catch(() => {});
  const result = await runner.finishFailure(error, run);
  console.error(result.error);
  process.exitCode = 1;
} finally {
  for (const [sessionId, paneId] of [[run.second, run.secondPane], [run.first, run.firstPane]]) {
    if (sessionId && paneId) await client.request('close_pane', { sessionId, paneId }).catch(() => {});
  }
  await client.quitApp().catch(() => {});
  await observer.close().catch(() => {});
  await world.close();
}
