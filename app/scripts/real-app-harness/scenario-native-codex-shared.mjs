#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-CODEX-SHARED', tier: 'tier2-local-mock-agent', prefix: 'native-codex-shared',
  metadata: { focus: "In the native app a shared Codex terminal moves between conversations in one tile, the session it leaves stays hidden in the queue and the ledger, a hidden approval is answered from the queue and after /resume, and closing the last tile closes the session" },
});
const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));
runner.registerFailureEvidence('native_tree', async () => runner.writeJson('failure-tree.json', await driver.tree()));

const CONVERSATION = /Conversation (mock-[0-9a-f-]{36})/g;
const APPROVAL = 'Allow the command to run?';
const sessions = [];
const session = (id) => observer.getSession(id);
const isHidden = (id) => session(id)?.hidden === true;
const shot = (file) => driver.screenshot(path.join(runner.runDir, file));

function tileOf(sessionId) {
  for (const desktop of observer.desktops) {
    const pane = desktop.panes.find((entry) => entry.session_id === sessionId);
    if (pane) return { desktop, pane };
  }
  return null;
}

const sessionIn = (runtime) => observer.desktops.flatMap((d) => d.panes).find((p) => p.runtime_id === runtime)?.session_id;

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

async function lastConversation(runtime, shown) {
  await observer.waitFor(() => observer.terminalOf(shown) === runtime, `${shown} in the terminal`, 30_000);
  const value = await driver.state(`terminal:${runtime}`, { includes: 'Conversation mock-', timeoutMs: 30_000 });
  const text = typeof value === 'string' ? value : JSON.stringify(value);
  return [...text.matchAll(CONVERSATION)].at(-1)?.[1];
}

async function submit(text) {
  await driver.typeInto(null, text);
  await driver.pressKey('Enter');
}

async function setSetting(key, value) {
  queueDaemonSettingRestore(observer, key);
  observer.send({ cmd: 'set_setting', key, value });
  await observer.waitFor(() => observer.getSetting(key) === value, `${key}=${value}`);
}

try {
  await runner.step('launch', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    await driver.waitFor({ key: 'chief-slot' });
    if (await driver.find({ key: 'chief-setup' })) {
      await driver.pressKey('Escape');
      await driver.waitFor({ key: 'chief-setup' }, { gone: true });
    }
  });

  let desktop;
  await runner.step('prepare', async () => {
    await setSetting('auto_approve_enabled', 'false');
    await setSetting('queue_mode_enabled', 'true');
    runner.assert(observer.getSetting('queue_show_hidden_sessions') !== 'false', 'hidden sessions show in the queue by default');
    const before = observer.currentDesktopId();
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => {
      const left = observer.desktop(desktop.id);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    runner.registerCleanup('close_sessions', () => observer.unregisterMatchingSessions((s) => sessions.includes(s.id)));
  });

  const cwd = path.join(runner.sessionDir, 'shop');
  fs.mkdirSync(cwd, { recursive: true });
  writeMockAgentFixture(cwd, {
    agent: 'codex',
    minimumWorkingMs: 600,
    turns: [
      { includes: 'plan the change', actions: [{ type: 'reply', text: 'Plan ready. Lock it?', state: 'waiting_input' }] },
      { includes: 'needs approval', actions: [{ type: 'reply', text: 'migrated', approval: true }] },
    ],
    defaultActions: [{ type: 'reply', text: 'done', state: 'idle' }],
  });

  const checkout = crypto.randomUUID();
  let discount;
  let terminal;
  let checkoutConversation;
  let discountConversation;

  await runner.step('a_codex_terminal_runs_against_the_shared_server', async () => {
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === checkout && m, 'checkout spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: checkout, agent: 'codex', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'checkout', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session starts the Codex agent');
    sessions.push(checkout);
    terminal = await observer.waitFor(() => observer.terminalOf(checkout), 'checkout has a terminal', 30_000);
    await focusTerminalOf(checkout);
    checkoutConversation = await lastConversation(terminal, checkout);
    runner.assert(Boolean(checkoutConversation), 'the terminal shows its conversation');
    await submit('plan the change');
    await observer.waitFor(() => session(checkout)?.state === 'waiting_input', 'checkout waits on the user', 30_000);
  });

  await runner.step('new_shows_a_new_session_at_once_and_leaves_the_old_one_hidden_in_the_queue_and_ledger', async () => {
    await submit('/new');
    await observer.waitFor(() => isHidden(checkout), 'checkout is hidden as soon as its terminal moved on', 30_000);
    discount = await observer.waitFor(() => { const id = sessionIn(terminal); return id && id !== checkout ? id : null; }, 'the terminal shows another session', 30_000);
    sessions.push(discount);
    discountConversation = await lastConversation(terminal, discount);
    runner.assert(discountConversation && discountConversation !== checkoutConversation, 'the terminal shows a new conversation', { checkoutConversation, discountConversation });
    await submit('look at the tax table');
    runner.assert(session(checkout).state === 'waiting_input', 'the hidden session keeps its queue state', session(checkout));
    await driver.waitFor({ key: `queue-turn.${checkout}` });
    runner.assert(!(await driver.find({ key: `queue-turn.${checkout}.slot` })), 'the hidden session queues without a desktop badge');
    await driver.run('sessions.open');
    const meta = await driver.waitFor({ key: `ledger.row.${checkout}.meta` });
    runner.assert((meta?.name ?? '').includes('hidden'), 'the ledger marks the session hidden', meta);
    await driver.run('sessions.open');
    await driver.waitFor({ key: `ledger.row.${checkout}.meta` }, { gone: true });
  });

  await runner.step('switching_conversations_keeps_the_keyboard_in_the_tile', async () => {
    const pane = tileOf(discount).pane.pane_id;
    await focusTerminalOf(discount);
    await submit(`/agents ${checkoutConversation}`);
    await observer.waitFor(() => tileOf(checkout) && !isHidden(checkout) && isHidden(discount), 'checkout is shown again and discount hidden', 30_000);
    runner.assert(tileOf(checkout).pane.pane_id === pane, 'the switch reused the same tile');
    runner.assert(await lastConversation(terminal, checkout) === checkoutConversation, 'the terminal switches back to checkout');
    await submit('lock it');
    await observer.waitFor(() => session(checkout)?.state === 'idle', 'the lock turn finishes', 30_000);
    await driver.typeInto(null, 'still typing');
    await driver.state(`terminal:${terminal}`, { includes: 'still typing', timeoutMs: 15_000 });
    for (let i = 0; i < 'still typing'.length; i += 1) await driver.pressKey('Backspace');
  });

  await runner.step('a_hidden_approval_is_answered_through_the_queue', async () => {
    const sent = await observer.requestResult({ cmd: 'session_annotations_submit', session_id: discount, text: 'needs approval to run the migration' }, 'session_annotations_submit_result');
    runner.assert(sent.success && sent.status === 'delivered', 'feedback reaches the hidden session', sent);
    await observer.waitFor(() => session(discount)?.state === 'pending_approval' && isHidden(discount), 'the hidden session waits on an approval', 30_000);
    await driver.waitFor({ key: `queue-turn.${discount}` });
    runner.assert(!(await driver.find({ key: `queue-turn.${discount}.slot` })), 'the approval queues with no desktop badge');
    await driver.click({ key: `queue-turn.${discount}` });
    await observer.waitFor(() => tileOf(discount)?.desktop.id === observer.currentDesktopId() && !isHidden(discount), 'the tile opens on the current desktop', 30_000);
    const runtime = await observer.waitFor(() => observer.terminalOf(discount), 'the new tile has a terminal', 30_000);
    await driver.state(`terminal:${runtime}`, { includes: APPROVAL, timeoutMs: 30_000 });
    await shot('hidden-approval-in-tile.png');
    await driver.pressKey('Enter');
    await observer.waitFor(() => session(discount)?.state === 'waiting_input', 'Enter answers the approval and the turn finishes', 30_000);
    await driver.state(`terminal:${runtime}`, { includes: 'migrated', timeoutMs: 30_000 });
    await setSetting('queue_mode_enabled', 'false');
  });

  await runner.step('resuming_a_hidden_approval_answers_it_in_the_tile', async () => {
    const runtime = observer.terminalOf(discount);
    const pane = tileOf(discount).pane.pane_id;
    await focusTerminalOf(discount);
    await submit('/new');
    await observer.waitFor(() => isHidden(discount), 'discount is hidden once its terminal moved on', 30_000);
    const blank = await observer.waitFor(() => { const id = sessionIn(runtime); return id && id !== discount ? id : null; }, 'the tile shows another session', 30_000);
    sessions.push(blank);
    const sent = await observer.requestResult({ cmd: 'session_annotations_submit', session_id: discount, text: 'needs approval to run the migration again' }, 'session_annotations_submit_result');
    runner.assert(sent.success && sent.status === 'delivered', 'feedback reaches the hidden session', sent);
    await observer.waitFor(() => session(discount)?.state === 'pending_approval' && isHidden(discount), 'the hidden session waits on an approval', 30_000);
    await focusTerminalOf(blank);
    await submit(`/resume ${discountConversation}`);
    await observer.waitFor(() => tileOf(discount)?.pane.pane_id === pane && !isHidden(discount), 'discount is shown again in the same tile', 30_000);
    await driver.state(`terminal:${runtime}`, { includes: APPROVAL, timeoutMs: 30_000 });
    await driver.pressKey('Enter');
    await driver.state(`terminal:${runtime}`, { includes: 'migrated', timeoutMs: 30_000 });
    await observer.waitFor(() => session(discount)?.state === 'waiting_input', 'the session leaves the approval once its turn finishes', 30_000);
  });

  await runner.step('closing_the_last_tile_leaves_the_session_closed_in_the_ledger', async () => {
    await focusTerminalOf(discount);
    await driver.run('session.close');
    await observer.waitFor(() => !observer.sessionsById.has(discount), 'the session closes with its last tile', 30_000);
    const shown = attn('session', 'show', discount);
    runner.writeText('session-show-closed.txt', shown);
    runner.assert(/^state\s+closed$/m.test(shown), 'the ledger reads the session as closed', { shown });
    runner.assert(/^usage\s+[1-9]/m.test(shown), 'the closed session keeps its usage', { shown });
    runner.assert(session(checkout) && tileOf(checkout), 'the other session and its tile are untouched', session(checkout));
    runner.assert(attn('session', 'list', '--closed').includes(discount), 'the ledger lists the closed session');
  });

  await runner.finishSuccess({ sessions, checkoutConversation });
} catch (error) {
  await runner.finishFailure(error, { sessions });
  process.exitCode = 1;
}
