#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { createWindowDriver } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { waitForFirstDesktopPane, waitForPaneInputFocus, waitForPaneText } from './scenarioAssertions.mjs';
import { ensureClaudeInitialPanePromptReady } from './scenarioAgents.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Agent reload verification requires a named instance');
const resources = resolveHarnessResources(instance);
const runner = createScenarioRunner(options, {
  scenarioId: 'AGENT-RELOAD', tier: 'local', prefix: 'agent-reload',
  metadata: { focus: 'CLI reload preserves the agent tile and native keyboard input.' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const created = [];
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, created));

try {
  await launchFreshAppAndConnect(client, observer);
  fs.mkdirSync(runner.sessionDir, { recursive: true });
  writeMockAgentFixture(runner.sessionDir, {
    resumable: true, minimumWorkingMs: 0,
    turns: ['before', 'after'].map(when => ({
      includes: `Check input ${when} reload`,
      actions: [{ type: 'reply', text: `INPUT_${when.toUpperCase()}_RELOAD_OK`, state: 'idle' }],
    })),
  });
  const session = await createSessionAndWaitForInitialPane({
    client, observer, cwd: runner.sessionDir, label: 'Reload probe', agent: 'claude',
    promptReadyFn: ensureClaudeInitialPanePromptReady,
  });
  created.push(session);
  const pane = await waitForFirstDesktopPane(client, session, 'agent tile');
  const desktop = observer.desktopOf(session).id;
  const terminal = observer.terminalOf(session);
  await client.request('select_session', { sessionId: session });
  await client.request('dismiss_whats_new');

  for (const when of ['before', 'after']) {
    if (when === 'after') {
      await runner.step('reload_through_the_cli', async () => {
        const respawned = observer.waitForMessage(
          message => message.event === 'runtime_respawned' && message.id === terminal,
          'CLI reload respawn',
        );
        const { stdout } = await promisify(execFile)(resources.appDaemon,
          ['agent', 'reload', session, '--source-session', session, '--json'],
          { env: instanceCliEnv(instance), encoding: 'utf8', timeout: 30_000 });
        const result = JSON.parse(stdout);
        runner.writeJson('reload-result.json', result);
        runner.assert(result.outcomes.length === 1 && result.outcomes[0].success
          && result.outcomes[0].session_id === session, 'CLI reports the selected agent reloaded', result);
        await respawned;
        await ensureClaudeInitialPanePromptReady(client, session);
        const restored = await waitForFirstDesktopPane(client, session, 'reloaded agent tile');
        runner.assert(restored.paneId === pane.paneId && observer.desktopOf(session).id === desktop,
          'reload keeps the same tile and desktop', { pane, restored, desktop });
      });
    }
    await runner.step(`native_input_${when}_reload`, async () => {
      await waitForPaneInputFocus(client, session, pane.paneId);
      await driver.typeText(`Check input ${when} reload`);
      await driver.pressEnter();
      await waitForPaneText(client, session, pane.paneId,
        text => text.includes(`INPUT_${when.toUpperCase()}_RELOAD_OK`),
        `agent reply to native input ${when} reload`);
      await driver.screenshot(path.join(runner.runDir, `${when}-reload.png`), { windowId: await driver.mainWindowId() });
    });
  }
  await runner.finishSuccess({ session, pane: pane.paneId, desktop });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
