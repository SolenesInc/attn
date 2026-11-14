#!/usr/bin/env node

import crypto from 'node:crypto';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MOCK_AGENT_MODEL, writeMockAgentFixture } from './mockAgent.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-MOVE-WITH-DELEGATES', tier: 'tier2-local-mock-agent', prefix: 'native-move-with-delegates',
  metadata: { focus: 'The native palette moves a dispatcher, two delegates and a nested delegate to another desktop together, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const created = [];

function rowsIn(node, found = []) {
  if (node?.key?.startsWith('palette.row.')) found.push({ key: node.key, selected: JSON.stringify(node.states ?? '').includes('selected') });
  for (const child of node?.children ?? []) rowsIn(child, found);
  return found;
}

function closeDesktopLater(desktop) {
  runner.registerCleanup(`close_desktop_${desktop.id}`, () => {
    const left = observer.desktop(desktop.id);
    return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
  });
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  let source;
  let target;
  await runner.step('make_a_source_and_a_target_desktop', async () => {
    const before = observer.currentDesktopId();
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    source = await observer.createDesktop();
    closeDesktopLater(source);
    target = (await observer.profileCommand('desktop_create', { profile_id: observer.profileId, name: `Delegate destination ${runner.runId}` })).desktops[0];
    closeDesktopLater(target);
    await driver.click({ key: `sidebar.desktop.${source.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const cwd = path.join(runner.sessionDir, 'delegates');
  let root;
  let first;
  let second;
  let nested;
  await runner.step('place_a_dispatcher_and_its_delegates', async () => {
    writeMockAgentFixture(cwd, { name: 'Desktop delegates', turns: [] });
    root = crypto.randomUUID();
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === root && message, 'dispatcher spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: root, agent: 'codex', cwd, profile_id: observer.profileId, placement: { desktop_id: source.id }, label: 'Dispatcher', cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, 'spawn_session starts the dispatcher', result);
    created.push(root);
    const delegate = async (dispatcher, label) => {
      const reply = await observer.requestResult({
        cmd: 'delegate', cwd, source_session_id: dispatcher,
        agent: 'codex', model: MOCK_AGENT_MODEL, label,
        assignment: { kind: 'new', brief: `Wait for direction as ${label}.` },
      }, 'delegate_result', 60_000);
      const id = reply.result?.session_id;
      runner.assert(Boolean(id), `${label} starts`, reply);
      created.push(id);
      return id;
    };
    first = await delegate(root, 'First delegate');
    second = await delegate(root, 'Second delegate');
    nested = await delegate(first, 'Nested delegate');
    const placed = await observer.waitForDesktopOf(root, (desktop) => created.every((id) => desktop.panes.some((pane) => pane.session_id === id)), 'all four agents on the source desktop');
    runner.assert(placed.id === source.id, 'the group starts on the source desktop', placed);
    await driver.click({ key: `tile.${result.pane_id}.header` });
    const window = await driver.state('window', { includes: root, timeoutMs: 15_000 });
    runner.assert(window.on_screen === root, 'the dispatcher is the agent on screen', window);
    await driver.screenshot(path.join(runner.runDir, 'before-move.png'));
  });

  await runner.step('move_the_group_through_the_native_palette', async () => {
    await driver.run('ui.commandPalette');
    const dialog = await driver.waitFor({ key: 'palette.dialog' });
    runner.assert(dialog.name === 'Commands', 'the palette opens on commands', dialog);
    await driver.typeInto(null, `Move with delegates to ${target.name}`);
    const row = `palette.row.command:move-with-delegates-${target.id}`;
    await driver.waitFor({ key: row });
    const shown = rowsIn(await driver.tree());
    runner.assert(shown.find((entry) => entry.selected)?.key === row, 'the move to the target is highlighted', shown);
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'palette' }, { gone: true });
    const moved = await observer.waitForDesktopOf(root, (desktop) => desktop.id === target.id
      && created.every((id) => desktop.panes.some((pane) => pane.session_id === id)), 'all four agents land on the target desktop');
    runner.writeText('arrangement.json', JSON.stringify(moved, null, 2));
    await driver.waitFor({ key: 'launcher.path.field' });
    await driver.screenshot(path.join(runner.runDir, 'after-palette-move.png'));
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ root, first, second, nested, source: source.id, target: target.id, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
