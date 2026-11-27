#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-FORK-OFFER', tier: 'tier2-local-mock-agent', prefix: 'native-fork-offer',
  metadata: { focus: "A shared Codex /fork leaves a notice on the fork's tile; Open original beside this from the palette puts the original in the next column with the keyboard, and a dismissed offer stays gone after the app restarts" },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));
runner.registerFailureEvidence('native_tree', async () => runner.writeJson('failure-tree.json', await driver.tree()));

const PALETTE = process.platform === 'linux' ? 'mod+alt+k' : 'mod+shift+k';
const sessions = [];
const session = (id) => observer.getSession(id);
const shot = (file) => driver.screenshot(path.join(runner.runDir, file));
const notice = (fork) => ({ key: `session.${fork}.fork-offer` });
const sessionIn = (runtime) => observer.desktops.flatMap((d) => d.panes).find((p) => p.runtime_id === runtime)?.session_id;

function tileOf(sessionId) {
  for (const desktop of observer.desktops) {
    const pane = desktop.panes.find((entry) => entry.session_id === sessionId);
    if (pane) return { desktop, pane };
  }
  return null;
}

function columns(desktop) {
  const order = [];
  const walk = (node) => {
    if (!node || typeof node !== 'object') return;
    if (node.type === 'pane') order.push(node.pane_id);
    for (const value of Object.values(node)) if (typeof value === 'object') walk(value);
  };
  walk(JSON.parse(desktop.tree_json));
  return order.map((pane) => desktop.panes.find((p) => p.pane_id === pane)?.session_id);
}

async function rowOf(id) {
  const keys = ['queue-turn', 'queue-settled', 'queue-snoozed', 'sidebar.session', 'home.session'].map((band) => `${band}.${id}`);
  const found = [];
  const walk = (n) => { if (!n) return; if (keys.includes(n.key)) found.push(n.key); for (const c of n.children ?? []) walk(c); };
  walk(await driver.tree());
  return keys.find((key) => found.includes(key));
}

async function focusTerminalOf(id) {
  const runtime = await observer.waitFor(() => observer.terminalOf(id), `${id} has a terminal`, 30_000);
  if (!(await driver.find({ key: `terminal.${runtime}` }))) {
    const row = await rowOf(id);
    runner.assert(Boolean(row), `the window has a row for ${id}`);
    await driver.click({ key: row });
  }
  await driver.click({ key: `terminal.${runtime}` });
  return runtime;
}

async function submit(text) {
  await driver.typeInto(null, text);
  await driver.pressKey('Enter');
}

async function forkIn(runtime, from) {
  await submit('/fork');
  const fork = await observer.waitFor(() => { const id = sessionIn(runtime); return id && id !== from ? id : null; }, 'the terminal shows the fork', 30_000);
  sessions.push(fork);
  await observer.waitFor(() => session(fork)?.fork_offer === from, `the fork offers ${from}`, 30_000);
  return fork;
}

try {
  await runner.step('launch', async () => {
    await launchFreshAppAndConnect(client, observer);
    await driver.waitFor({ key: 'chief-slot' });
    if (await driver.find({ key: 'chief-setup' })) {
      await driver.pressKey('Escape');
      await driver.waitFor({ key: 'chief-setup' }, { gone: true });
    }
  });

  let desktop;
  await runner.step('prepare', async () => {
    const before = observer.currentDesktopId();
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => observer.closeDesktop(desktop.id));
    runner.registerCleanup('close_sessions', () => observer.unregisterMatchingSessions((s) => sessions.includes(s.id)));
  });

  const cwd = path.join(runner.sessionDir, 'shop');
  fs.mkdirSync(cwd, { recursive: true });
  writeMockAgentFixture(cwd, {
    agent: 'codex',
    minimumWorkingMs: 600,
    turns: [{ includes: 'plan the change', actions: [{ type: 'reply', text: 'Plan ready.', state: 'waiting_input' }] }],
    defaultActions: [{ type: 'reply', text: 'done', state: 'idle' }],
  });

  const original = crypto.randomUUID();
  let terminal;
  let fork;

  await runner.step('a_codex_session_with_one_turn', async () => {
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === original && m, 'original spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: original, agent: 'codex', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'plan the migration', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session starts the Codex agent');
    sessions.push(original);
    terminal = await focusTerminalOf(original);
    await driver.state(`terminal:${terminal}`, { includes: 'Conversation mock-', timeoutMs: 30_000 });
    await submit('plan the change');
    await observer.waitFor(() => session(original)?.state === 'waiting_input', 'the turn ends', 30_000);
  });

  await runner.step('a_fork_shows_the_notice_and_the_app_rests', async () => {
    fork = await forkIn(terminal, original);
    const shown = await driver.waitFor(notice(fork));
    runner.assert(shown?.name === 'Forked from “plan the migration”.', 'the notice names the original', shown);
    runner.assert(!tileOf(original), 'no tile shows the original');
    await driver.atRest({ timeoutMs: 10_000 });
    await shot('fork-offer.png');
  });

  await runner.step('open_original_from_the_palette_puts_it_focused_in_the_next_column', async () => {
    await driver.pressKey(PALETTE);
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, 'Open original beside this');
    await driver.waitFor({ key: 'palette.row.command:open-fork-original' });
    await driver.pressKey('Enter');
    const placed = await observer.waitFor(() => tileOf(original), 'a tile shows the original', 30_000);
    const order = columns(placed.desktop);
    runner.assert(placed.desktop.id === desktop.id, 'the original opens on the fork\'s desktop', { desktop: placed.desktop.id });
    runner.assert(order.indexOf(original) === order.indexOf(fork) + 1, 'the original is in the column right after the fork', { order });
    runner.assert(placed.desktop.active_pane_id === placed.pane.pane_id, 'the original is the active tile', placed.desktop);
    await driver.state('window', { expect: { on_screen: original }, timeoutMs: 15_000 });
    await observer.waitFor(() => session(fork) && !session(fork).fork_offer, 'Open ends the offer', 30_000);
    await driver.waitFor(notice(fork), { gone: true });
    await driver.atRest({ timeoutMs: 10_000 });
    await shot('fork-opened.png');
  });

  let second;
  await runner.step('dismiss_ends_a_second_fork_offer', async () => {
    const runtime = await focusTerminalOf(original);
    second = await forkIn(runtime, original);
    await driver.waitFor(notice(second));
    await driver.click({ key: `session.${second}.fork-offer.dismiss` });
    await observer.waitFor(() => session(second) && !session(second).fork_offer, 'Dismiss ends the offer', 30_000);
    await driver.waitFor(notice(second), { gone: true });
    await shot('fork-dismissed.png');
  });

  await runner.step('a_dismissed_offer_stays_gone_after_the_app_restarts', async () => {
    await client.quitApp();
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await observer.waitFor(() => tileOf(second), 'the second fork still has its tile', 30_000);
    await focusTerminalOf(second);
    runner.assert(!(await driver.find(notice(second))), 'no notice comes back for the dismissed offer');
    await driver.atRest({ timeoutMs: 10_000 });
    await shot('fork-dismissed-after-restart.png');
  });

  await runner.finishSuccess({ sessions, fork, second });
} catch (error) {
  await runner.finishFailure(error, { sessions });
  process.exitCode = 1;
}
