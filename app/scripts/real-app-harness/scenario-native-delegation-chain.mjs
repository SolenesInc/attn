#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { MOCK_AGENT_MODEL, writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-DELEGATION-CHAIN', tier: 'tier2-local-mock-agent', prefix: 'native-delegation-chain',
  metadata: { focus: 'In the native app delegates carry their roles into the header and the delegation chain, which opens from a row, the header and the palette and walks by arrows; a delegate to a missing numbered desktop leaves the caller where it was' },
});
const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));

const created = [];
const seedOf = new Map();
const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));

function preferences(cmd, prefs) {
  const request_id = crypto.randomUUID();
  return new Promise((resolve, reject) => {
    const receive = (raw) => {
      const result = JSON.parse(String(raw));
      if (result.request_id !== request_id) return;
      observer.ws.off('message', receive);
      if (result.success) resolve(result); else reject(new Error(result.error));
    };
    observer.ws.on('message', receive);
    observer.ws.send(JSON.stringify({ cmd, request_id, ...(prefs ? { preferences: prefs } : {}) }));
  });
}

function nodesWhere(node, test, found = []) {
  if (node && test(node)) found.push(node);
  for (const child of node?.children ?? []) nodesWhere(child, test, found);
  return found;
}

async function chainRows(surface) {
  return nodesWhere(await driver.tree(), (n) => n.key?.startsWith(`${surface}.agent.`)).map((n) => ({
    key: n.key, name: n.name ?? '', picked: JSON.stringify(n.states ?? '').includes('selected'),
  }));
}

async function picked(surface, name) {
  const rows = await chainRows(surface);
  const row = rows.find((r) => r.picked);
  runner.assert(row?.name.startsWith(name), `the chain picks ${name}`, rows);
}

const paneOf = (sessionId) => observer.desktopOf(sessionId)?.panes.find((p) => p.session_id === sessionId)?.pane_id;

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  let root;
  let builder;
  let child;
  let desktop;
  await runner.step('launch_roles_and_a_roleless_delegate', async () => {
    const baseline = (await preferences('delegation_preferences_get')).preferences;
    runner.registerCleanup('restore_preferences', async () => {
      const current = (await preferences('delegation_preferences_get')).preferences;
      await preferences('delegation_preferences_save', { ...baseline, revision: current.revision });
    });
    const role = (id, name, icon) => ({
      id, name, icon, enabled: true, description: '', instructions: 'Wait for direction.', default_choice_id: 'default',
      choices: [{ id: 'default', name: 'Default', when: '', selection: { harness: 'codex', provider: '', model: '', effort: '' } }],
    });
    await preferences('delegation_preferences_save', { ...baseline, enabled: true, roles: [role('coordinate', 'Orchestrator', 'spark'), role('build', 'Builder', 'code')] });
    fs.mkdirSync(runner.sessionDir, { recursive: true });
    writeMockAgentFixture(runner.sessionDir, {
      name: 'Role identity check',
      turns: [
        { includes: 'go on standby', actions: [
          { type: 'capture', name: 'seed', pattern: '(s-[a-z0-9]+)' },
          { type: 'attn', args: ['seed', 'standby', '{{seed}}', '-m', 'Merge once CI is green'] }] },
        { includes: 'role identity', actions: [{ type: 'reply', text: 'Ready to inspect the delegation chain.', state: 'idle' }] },
      ],
    });
    const before = observer.currentDesktopId();
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => {
      const left = observer.desktop(desktop.id);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    runner.registerCleanup('close_sessions', () => observer.unregisterMatchingSessions((s) => created.includes(s.id)));
    const source = crypto.randomUUID();
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === source && m, 'the source shell spawned', 30_000);
    observer.send({ cmd: 'spawn_session', id: source, agent: 'shell', cwd: runner.sessionDir, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'Delegation setup', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'the source shell starts');
    created.push(source);
    const delegate = async (from, name, roleId) => {
      const output = attn('delegate', '--source-session', from, ...(roleId ? ['--role', roleId] : ['--agent', 'codex', '--model', MOCK_AGENT_MODEL]),
        '--brief', 'Inspect role identity. Wait for direction.', '--cwd', runner.sessionDir, '--name', name);
      const result = JSON.parse(output.slice(output.indexOf('{')));
      created.push(result.session_id);
      seedOf.set(result.session_id, result.seed_id);
      await observer.waitFor(() => paneOf(result.session_id), `${name} placed`, 30_000);
      return result.session_id;
    };
    root = await delegate(source, 'Coordinate role identity', 'coordinate');
    builder = await delegate(root, 'Build chain navigator', 'build');
    child = await delegate(builder, 'Check keyboard flow');
    await observer.waitFor(() => observer.getSession(root)?.delegation_role?.name === 'Orchestrator'
      && observer.getSession(builder)?.delegation_role?.name === 'Builder', 'saved roles projected onto sessions');
    runner.assert(!observer.getSession(child)?.delegation_role, 'a direct delegation stays roleless');
  });

  await runner.step('the_row_and_the_header_open_the_chain', async () => {
    await driver.click({ key: `sidebar.session.${builder}` });
    await driver.state('window', { includes: builder, timeoutMs: 15_000 });
    const pane = paneOf(builder);
    await driver.waitFor({ key: `tile.${pane}.role.name`, name: 'Builder' });
    const rowChip = `sidebar.session.${builder}.chain`;
    await driver.hover({ key: rowChip });
    const rowCard = `${rowChip}.popover`;
    await driver.waitFor({ key: rowCard });
    const rows = await chainRows(rowCard);
    const names = rows.map((r) => r.name);
    runner.assert(names.some((n) => n.startsWith('Coordinate role identity · Orchestrator'))
      && names.some((n) => n.startsWith('Build chain navigator · Builder · current'))
      && names.some((n) => n.startsWith('Check keyboard flow')), 'the chain shows the ancestors, roles and the roleless delegate', names);
    await picked(rowCard, 'Build chain navigator');
    await shot('hover-chain');
    await driver.pressKey('Up');
    await picked(rowCard, 'Coordinate role identity');
    await driver.pressKey('Escape');
    await driver.waitFor({ key: rowCard }, { gone: true });
    const header = `tile.${pane}.role`;
    await driver.hover({ key: header });
    const headerCard = `${header}.popover`;
    await driver.waitFor({ key: headerCard });
    await picked(headerCard, 'Build chain navigator');
    await driver.pressKey('Down');
    await picked(headerCard, 'Check keyboard flow');
    await driver.pressKey('Escape');
    await driver.waitFor({ key: headerCard }, { gone: true });
  });

  await runner.step('the_palette_opens_the_chain_and_enter_opens_the_pick', async () => {
    await driver.run('ui.commandPalette');
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, 'delegation chain');
    await driver.waitFor({ key: 'palette.row.command:show-delegation-chain' });
    await driver.pressKey('Enter');
    const card = `tile.${paneOf(builder)}.role.popover`;
    await driver.waitFor({ key: card });
    await picked(card, 'Build chain navigator');
    await shot('keyboard-chain');
    await driver.pressKey('Up');
    await picked(card, 'Coordinate role identity');
    await driver.pressKey('Down');
    await driver.pressKey('Down');
    await picked(card, 'Check keyboard flow');
    await driver.pressKey('Enter');
    await driver.waitFor({ key: card }, { gone: true });
    const window = await driver.state('window', { includes: child, timeoutMs: 15_000 });
    runner.assert(window.on_screen === child, 'Enter opens the picked agent', window);
  });

  await runner.step('a_delegate_on_standby_stays_in_the_chain_and_opens_its_seed', async () => {
    const seed = seedOf.get(child);
    observer.send({ cmd: 'pty_input', id: observer.terminalOf(child), data: `go on standby ${seed}\r`, source: 'automation' });
    await observer.waitFor(() => !observer.getSession(child), 'the delegate closes on standby', 30_000);
    await driver.click({ key: `sidebar.session.${builder}` });
    await driver.state('window', { includes: builder, timeoutMs: 15_000 });
    const card = `sidebar.session.${builder}.chain.popover`;
    await driver.click({ key: `sidebar.session.${builder}.chain` });
    await driver.waitFor({ key: card });
    const dormant = 'Check keyboard flow · on standby · Merge once CI is green';
    const under = (await chainRows(card)).findIndex((r) => r.name.startsWith('Build chain navigator')) + 1;
    await driver.waitFor({ key: `${card}.agent.${under}`, name: dormant }, { timeoutMs: 15_000 });
    await shot('standby-chain');
    await driver.pressKey('Down');
    await picked(card, 'Check keyboard flow · on standby');
    await driver.pressKey('Enter');
    await driver.waitFor({ key: card }, { gone: true });
    await observer.waitFor(() => JSON.stringify(observer.desktopOf(builder) ?? {}).includes(seed),
      'Enter opens the seed beside its dispatcher', 15_000);
    await shot('standby-seed-opened');
  });

  await runner.step('a_deleted_role_keeps_the_agents_launched_with_it', async () => {
    const current = (await preferences('delegation_preferences_get')).preferences;
    await preferences('delegation_preferences_save', { ...current, enabled: false, roles: [] });
    await driver.click({ key: `sidebar.session.${root}` });
    await driver.state('window', { includes: root, timeoutMs: 15_000 });
    await driver.waitFor({ key: `tile.${paneOf(root)}.role.name`, name: 'Orchestrator' });
    runner.assert(observer.getSession(root)?.delegation_role?.name === 'Orchestrator', 'the launch identity outlives its role');
  });

  await runner.step('a_delegate_to_a_missing_numbered_desktop_leaves_the_caller_on_screen', async () => {
    await driver.click({ key: `sidebar.session.${builder}` });
    await driver.state('window', { includes: builder, timeoutMs: 15_000 });
    await observer.closeLeftoverNumberedDesktops();
    const held = new Set(observer.desktops.map((d) => d.shortcut_slot).filter(Boolean));
    const slot = [7, 1, 2, 3, 4, 5, 6, 8, 9].find((candidate) => !held.has(candidate));
    runner.assert(Boolean(slot), 'a numbered desktop is missing', observer.desktops);
    const before = await driver.state('window');
    const target = `${observer.profileId}/desktop_${slot}`;
    const placed = observer.waitForMessage((m) => m.event === 'profile_arrangement_changed'
      && m.desktops?.find((d) => d.id === target && d.panes.length === 1), 'the numbered desktop made again', 30_000);
    const output = attn('delegate', '--source-session', builder, '--agent', 'codex', '--model', MOCK_AGENT_MODEL,
      '--brief', 'Inspect role identity. Wait for direction.', '--cwd', runner.sessionDir, '--name', 'Missing desktop delegate', '--desktop', String(slot));
    const result = JSON.parse(output.slice(output.indexOf('{')));
    created.push(result.session_id);
    const made = await placed;
    runner.assert(result.desktop_id === target && made.panes[0].session_id === result.session_id,
      'the delegate lands on the numbered desktop made for it', { result });
    runner.registerCleanup('close_numbered_desktop', () => {
      const left = observer.desktop(target);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    await driver.waitFor({ key: `sidebar.session.${result.session_id}` });
    const after = await driver.state('window');
    runner.assert(after.on_screen === builder && JSON.stringify(after.keyboard) === JSON.stringify(before.keyboard),
      'the caller stays on screen with the keyboard', { before, after });
    await shot('missing-numbered-desktop-caller');
    await driver.run(`desktop.select${slot}`);
    const opened = await driver.state('window', { includes: result.session_id, timeoutMs: 15_000 });
    runner.assert(opened.on_screen === result.session_id, 'the numbered shortcut shows the delegate', opened);
    await shot('missing-numbered-desktop-delegate');
  });

  await runner.finishSuccess({ sessions: created });
} catch (error) {
  await runner.finishFailure(error, { sessions: created });
  process.exitCode = 1;
}
