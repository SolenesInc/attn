#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { createWindowDriver } from './platform.mjs';
import { MOCK_AGENT_MODEL, writeMockAgentFixture } from './mockAgent.mjs';

const options = parseCommonArgs(process.argv.slice(2));
process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '0';
const runner = createScenarioRunner(options, {
  scenarioId: 'MOVE-WITH-DELEGATES', tier: 'local', prefix: 'move-with-delegates',
  metadata: { focus: 'Native palette moves a dispatcher, two delegates and a nested delegate together; overview sends them back beside their dispatchers.' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const created = [];
const dom = async (selector, expectation = {}) => {
  const result = await client.request('dom_wait', { selector, timeoutMs: 10_000, ...expectation });
  runner.assert(result.matched, `screen matches ${selector}`, result);
};
const screenshot = async name => {
  const windowId = await driver.mainWindowId();
  await driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId });
};
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, created.toReversed()));

try {
  await launchFreshAppAndConnect(client, observer);
  const { logicalBounds } = await client.request('get_window_bounds');
  await client.request('set_window_bounds', { logicalBounds: { ...logicalBounds, x: 0, y: 0 } });
  await driver.activateApp();
  await client.request('dismiss_whats_new');
  fs.mkdirSync(runner.sessionDir, { recursive: true });
  writeMockAgentFixture(runner.sessionDir, { name: 'Desktop delegates', turns: [] });
  let root, first, second, nested, source, target;
  await runner.step('place_a_dispatcher_and_its_delegates', async () => {
    root = await createSessionAndWaitForInitialPane({ client, observer, cwd: runner.sessionDir, label: 'Dispatcher', agent: 'codex' });
    created.push(root);
    const delegate = async (dispatcher, label) => {
      const result = await observer.requestResult({
        cmd: 'delegate', cwd: runner.sessionDir, source_session_id: dispatcher,
        agent: 'codex', model: MOCK_AGENT_MODEL, label,
        assignment: { kind: 'new', brief: `Wait for direction as ${label}.` },
      }, 'delegate_result');
      created.push(result.result.session_id);
      return result.result.session_id;
    };
    first = await delegate(root, 'First delegate');
    second = await delegate(root, 'Second delegate');
    nested = await delegate(first, 'Nested delegate');
    await client.request('select_session', { sessionId: root });
    source = observer.desktopOf(root);
    target = (await observer.profileCommand('desktop_create', { profile_id: source.profile_id, name: `Delegate destination ${root}` })).desktops[0];
    await driver.pressKey('Escape');
    await client.request('select_session', { sessionId: root });
    await dom(`[data-desktop-id="${source.id}"][data-session-visible="1"]`);
    const nestedPane = source.panes.find(pane => pane.session_id === nested);
    runner.assert(Boolean(nestedPane), "nested delegate has a placement", source);
    await dom(`[data-pane-id="${nestedPane.pane_id}"]`);
    await screenshot('before-move');
  });
  const assertGroup = async (desktopId) => {
    const state = await client.request('get_state');
    const desktop = state.arrangement.desktops.find(entry => entry.id === desktopId);
    const ids = new Set(desktop.panes.map(pane => pane.sessionId));
    runner.assert(created.every(id => ids.has(id)), 'all four agents landed together', { desktopId, ids: [...ids], created });
    runner.writeText('arrangement.json', JSON.stringify(state.arrangement, null, 2));
  };
  await runner.step('move_the_group_through_the_native_palette', async () => {
    await pressShortcutKeys(client, driver, 'ui.commandPalette');
    await dom('[role="combobox"][aria-label="Commands"]', { focused: true });
    await driver.typeText(`Move with delegates to ${target.name}`);
    await dom('.unified-palette-option[aria-selected="true"]', { textIncludes: `Move with delegates to ${target.name}` });
    const moved = observer.waitForMessage(message => message.event === 'profile_arrangement_changed'
      && message.desktops.some(desktop => desktop.id === target.id && created.every(id => desktop.panes.some(pane => pane.session_id === id))), 'group arrangement');
    await driver.pressKey('Enter');
    await moved;
    await client.request('select_session', { sessionId: root });
    await dom(`[data-desktop-id="${target.id}"][data-session-visible="1"]`);
    await assertGroup(target.id);
    await screenshot('after-palette-move');
  });
  await runner.step('send_the_group_back_from_the_overview', async () => {
    await pressShortcutKeys(client, driver, 'desktop.overview');
    const selector = `[data-desktop-id="${source.id}"] .desktop-overview-actions button:last-child`;
    await dom(selector, { textIncludes: 'Send with delegates' });
    const [{ bounds: overview }, { logicalBounds }, { innerWidth, innerHeight }] = await Promise.all([
      client.request('dom_bounds', { selector: '.desktop-overview' }),
      client.request('get_window_bounds'),
      client.request('get_terminal_context_menu_state'),
    ]);
    const relative = (x, y) => [
      (Math.max(0, logicalBounds.width - innerWidth) / 2 + x) / logicalBounds.width,
      (Math.max(0, logicalBounds.height - innerHeight) + y) / logicalBounds.height,
    ];
    let { bounds } = await client.request('dom_bounds', { selector });
    if (bounds.y < overview.y || bounds.y + bounds.height > overview.y + overview.height) {
      const centerY = overview.y + overview.height / 2;
      await driver.scrollWindow(...relative(overview.x + overview.width / 2, centerY), centerY - bounds.y - bounds.height / 2);
      ({ bounds } = await client.request('dom_bounds', { selector }));
    }
    runner.assert(bounds.y >= overview.y && bounds.y + bounds.height <= overview.y + overview.height,
      'overview send button is inside the visible panel', { bounds, overview });
    await screenshot('overview-send-with-delegates');
    const moved = observer.waitForMessage(message => message.event === 'profile_arrangement_changed'
      && message.desktops.some(desktop => desktop.id === source.id && created.every(id => desktop.panes.some(pane => pane.session_id === id))), 'return group arrangement');
    await driver.clickWindow(...relative(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2));
    await moved;
    await client.request('select_session', { sessionId: root });
    await dom(`[data-desktop-id="${source.id}"][data-session-visible="1"]`);
    await assertGroup(source.id);
    await screenshot('after-overview-return');
  });
  await runner.finishSuccess({ root, first, second, nested, source: source.id, target: target.id });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
