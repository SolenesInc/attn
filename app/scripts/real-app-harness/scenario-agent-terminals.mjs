#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { createWindowDriver } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { waitForFirstDesktopPane, waitForPaneInputFocus } from './scenarioAssertions.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'AGENT-TERMINALS', tier: 'local', prefix: 'agent-terminals',
  metadata: { focus: 'Agent-opened shell splits pulse and stack while native input stays with the agent.' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const created = [];
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, created.toReversed()));

try {
  await launchFreshAppAndConnect(client, observer);
  await driver.activateApp();
  await client.request('dismiss_whats_new');
  fs.mkdirSync(runner.sessionDir, { recursive: true });
  writeMockAgentFixture(runner.sessionDir, {
    name: 'Agent terminal splits', minimumWorkingMs: 0,
    turns: ['first', 'second'].map(name => ({ includes: `Open the ${name} terminal`, actions: [
      { type: 'attn', args: ['terminal', 'open', '--', `printf '${name} terminal ready\\n'`] },
      { type: 'reply', text: `${name} terminal opened; focus stays here.`, state: 'idle' },
    ] })),
  });
  const caller = await createSessionAndWaitForInitialPane({ client, observer, cwd: runner.sessionDir, label: 'Terminal operator', agent: 'claude' });
  created.push(caller);
  const callerPane = await waitForFirstDesktopPane(client, caller, 'calling agent tile');
  await client.request('select_session', { sessionId: caller });
  await waitForPaneInputFocus(client, caller, callerPane.paneId);
  const initial = observer.desktopOf(caller);
  const shellPanes = [];
  for (const name of ['first', 'second']) {
    await runner.step(`agent_opens_${name}_terminal_without_taking_focus`, async () => {
      const previous = new Set(observer.sessionsById.keys());
      const pulsing = client.request('dom_wait', {
        selector: `[data-desktop-id="${initial.id}"] .desktop-pane.leaf-arrival:not([data-pane-id="${callerPane.paneId}"])`,
        timeoutMs: observer.connectTimeoutMs,
      });
      await driver.typeText(`Open the ${name} terminal`);
      await driver.pressEnter();
      await pulsing;
      const windowId = await driver.mainWindowId();
      await driver.screenshot(path.join(runner.runDir, `${name}-arrival.png`), { windowId });
      let shell;
      await observer.waitFor(() => {
        shell = [...observer.sessionsById.values()].find(session => !previous.has(session.id) && session.agent === 'shell');
        return Boolean(shell);
      }, `${name} shell session`, observer.connectTimeoutMs);
      created.push(shell.id);
      const pane = await waitForFirstDesktopPane(client, shell.id, `${name} shell tile`);
      shellPanes.push(pane.paneId);
      const desktop = observer.desktopOf(caller);
      runner.assert(desktop.id === initial.id && desktop.active_pane_id === callerPane.paneId,
        'the active tile stays on the calling agent', desktop);
      await waitForPaneInputFocus(client, caller, callerPane.paneId);
      const callerBounds = await client.request('dom_bounds', { selector: `[data-pane-id="${callerPane.paneId}"]` });
      const shellBounds = await client.request('dom_bounds', { selector: `[data-pane-id="${pane.paneId}"]` });
      runner.writeText(`${name}-bounds.json`, JSON.stringify({ caller: callerBounds, shell: shellBounds }, null, 2));
      await client.request('dom_wait', { selector: `[data-pane-id="${pane.paneId}"].leaf-arrival`, absent: true, timeoutMs: observer.connectTimeoutMs });
    });
  }
  const desktop = observer.desktopOf(caller);
  const tree = JSON.parse(desktop.tree_json);
  const contains = (node, id) => node.pane_id === id || (node.children ?? []).some(child => contains(child, id));
  const column = tree.children?.find(child => shellPanes.every(id => contains(child, id)));
  runner.assert(tree.direction === 'vertical' && contains(tree.children[0], callerPane.paneId)
    && column?.direction === 'horizontal' && contains(column.children[0], shellPanes[0]) && contains(column.children[1], shellPanes[1]),
  'the first shell is right of the caller and the second is below it', tree);
  runner.writeText('arrangement.json', JSON.stringify(desktop, null, 2));
  await runner.finishSuccess({ caller, shellPanes });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
