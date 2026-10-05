#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { ensureCodexInitialPanePromptReady } from './scenarioAgents.mjs';
import { waitForPaneInputFocus, waitForPaneText, waitForSessionDesktop } from './scenarioAssertions.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Shared Codex verification requires a named, non-production instance');
const runner = createScenarioRunner(options, { scenarioId: 'CODEX-SHARED', tier: 'tier2-local-mock-agent', prefix: 'scenario-codex-shared' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const daemonBinary = appDaemonInTree(options.appPath);

const SHARED_TOGGLE = '[data-testid="settings-shared-codex-toggle"]';
const CONVERSATION = /Conversation (mock-[0-9a-f-]{36})/g;
const lastConversation = (text) => [...text.matchAll(CONVERSATION)].at(-1)?.[1];
const APPROVAL = 'Allow the command to run?';
const baseline = new Map();

const sessions = [];
const session = (id) => observer.getSession(id);
const isHidden = (id) => session(id)?.hidden === true;
const attn = (...args) => execFileSync(daemonBinary, args, { encoding: 'utf8', env: instanceCliEnv(instance) });

function tileOf(sessionId) {
  for (const desktop of observer.desktops) {
    const pane = desktop.panes.find((entry) => entry.session_id === sessionId);
    if (pane) return { desktop, pane };
  }
  return null;
}

async function paneOf(sessionId) {
  const desktop = await waitForSessionDesktop(client, sessionId, (entry) => entry.panes.some((pane) => pane.sessionId === sessionId && pane.runtimeId), `a tile showing ${sessionId}`);
  return desktop.panes.find((pane) => pane.sessionId === sessionId);
}

async function setSetting(key, value) {
  if (!baseline.has(key)) baseline.set(key, observer.getSetting(key));
  await client.request('set_setting', { key, value });
  await observer.waitFor(() => observer.getSetting(key) === value, `${key}=${value}`);
}

async function submit(sessionId, paneId, text) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text });
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
}

async function queueRow(sessionId) {
  const queue = await client.request('queue_get_state');
  return [...queue.turns, ...queue.settled, ...queue.pinned, ...queue.snoozed.rows].find((row) => row.id === sessionId) ?? null;
}

async function openLedger() {
  await client.request('dispatch_shortcut', { shortcutId: 'sessions.open' });
  await client.request('dom_wait', { selector: '.ledger-row', timeoutMs: observer.connectTimeoutMs });
}

async function closeLedger() {
  await client.request('dispatch_shortcut', { shortcutId: 'sessions.open' });
}

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', async () => {
  for (const [key, value] of baseline) await client.request('set_setting', { key, value }).catch(() => {});
});
runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, sessions.filter((id) => observer.getSession(id))));

try {
  await runner.step('launch', () => launchFreshAppAndConnect(client, observer));

  await runner.step('enable_shared_codex_in_settings', async () => {
    runner.assert(observer.getSetting('codex_shared_enabled') !== 'true', 'Shared Codex starts off');
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await client.request('settings_select_section', { sectionId: 'experimental' });
    await client.request('dom_scroll_into_view', { selector: SHARED_TOGGLE });
    baseline.set('codex_shared_enabled', observer.getSetting('codex_shared_enabled'));
    await client.request('dom_click', { selector: SHARED_TOGGLE });
    await observer.waitFor(() => observer.getSetting('codex_shared_enabled') === 'true', 'Shared Codex enabled from Settings');
    await client.request('dom_click', { selector: '[data-testid="settings-close"]' });
    await setSetting('queue_mode_enabled', 'true');
    runner.assert(observer.getSetting('queue_show_hidden_sessions') !== 'false', 'hidden sessions show in the queue by default');
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

  let checkout;
  let discount;
  let terminal;
  let checkoutConversation;

  await runner.step('a_codex_terminal_runs_against_the_shared_server', async () => {
    checkout = await createSessionAndWaitForInitialPane({
      client, observer, cwd, label: 'checkout', agent: 'codex', promptReadyFn: ensureCodexInitialPanePromptReady,
    });
    sessions.push(checkout);
    const pane = await paneOf(checkout);
    terminal = pane.runtimeId;
    const shown = await waitForPaneText(client, checkout, pane.paneId, (text) => Boolean(lastConversation(text)), 'the terminal shows its conversation');
    checkoutConversation = lastConversation(shown.text);
    await submit(checkout, pane.paneId, 'plan the change');
    await observer.waitFor(() => session(checkout)?.state === 'waiting_input', 'checkout waits on the user');
  });

  await runner.step('new_leaves_the_old_session_hidden_in_the_queue_and_ledger', async () => {
    const pane = await paneOf(checkout);
    await submit(checkout, pane.paneId, '/new');
    await waitForPaneText(client, checkout, pane.paneId, (text) => Boolean(lastConversation(text)) && lastConversation(text) !== checkoutConversation, 'the terminal shows a new conversation');
    await submit(checkout, pane.paneId, 'look at the tax table');
    await observer.waitFor(() => isHidden(checkout), 'checkout is hidden once its terminal moved on');
    discount = observer.desktops.flatMap((desktop) => desktop.panes).find((entry) => entry.runtime_id === terminal)?.session_id;
    runner.assert(discount && discount !== checkout, 'the terminal now shows another session', { discount });
    sessions.push(discount);
    runner.assert(session(checkout).state === 'waiting_input', 'the hidden session keeps its queue state', session(checkout));
    await client.request('dom_wait', { selector: '[data-testid="sidebar-queue"]', timeoutMs: observer.connectTimeoutMs });
    const row = await queueRow(checkout);
    runner.assert(row && row.desktopId === '', 'the hidden session queues without a desktop chip', row);
    await openLedger();
    await client.request('dom_wait', { selector: `.ledger-row[data-row-key="${checkout}"][data-hidden="true"]`, timeoutMs: observer.connectTimeoutMs });
    await closeLedger();
  });

  await runner.step('switching_conversations_keeps_keyboard_focus_in_the_tile', async () => {
    const pane = await paneOf(discount);
    await client.request('click_pane', { sessionId: discount, paneId: pane.paneId });
    await waitForPaneInputFocus(client, discount, pane.paneId, 15_000);
    await submit(discount, pane.paneId, `/agents ${checkoutConversation}`);
    await waitForPaneText(client, discount, pane.paneId, (text) => lastConversation(text) === checkoutConversation, 'the terminal switches back to checkout');
    await submit(discount, pane.paneId, 'lock it');
    await observer.waitFor(() => tileOf(checkout) && !isHidden(checkout) && isHidden(discount), 'checkout is shown again and the other session is hidden');
    const back = await paneOf(checkout);
    runner.assert(back.paneId === pane.paneId, 'the switch reused the same tile', { before: pane.paneId, after: back.paneId });
    await waitForPaneInputFocus(client, checkout, back.paneId, 15_000, { stableMs: 500 });
    await client.request('type_pane_via_ui', { sessionId: checkout, paneId: back.paneId, text: 'still typing' });
    const typed = await waitForPaneText(client, checkout, back.paneId, (text) => text.includes('still typing'), 'a draft typed after the switch reaches the terminal');
    runner.writeText('after-switch-pane.txt', typed.text);
  });

  await runner.step('a_hidden_approval_is_answered_through_the_queue', async () => {
    const sent = await observer.requestResult({ cmd: 'session_annotations_submit', session_id: discount, text: 'needs approval to run the migration' }, 'session_annotations_submit_result');
    runner.assert(sent.success && sent.status === 'delivered', 'feedback reaches the hidden session', sent);
    await observer.waitFor(() => session(discount)?.state === 'pending_approval' && isHidden(discount), 'the hidden session waits on an approval');
    const selector = `[data-testid="queue-turn-${discount}"] .queue-row-select`;
    await client.request('dom_wait', { selector, timeoutMs: observer.connectTimeoutMs });
    const row = await queueRow(discount);
    runner.assert(row && row.desktopId === '' && row.state === 'pending_approval', 'the approval is a queue row with no desktop chip', row);
    await client.request('dom_click', { selector });
    await observer.waitFor(() => tileOf(discount)?.desktop.id === observer.currentDesktopId() && !isHidden(discount), 'the tile opens on the current desktop');
    const pane = await paneOf(discount);
    await waitForPaneText(client, discount, pane.paneId, (text) => text.includes(APPROVAL), 'the replayed approval shows in the new tile');
    await waitForPaneInputFocus(client, discount, pane.paneId, 15_000);
    await driver.screenshot(path.join(runner.runDir, 'hidden-approval-in-tile.png'), { windowId: await driver.mainWindowId() });
    await driver.pressEnter();
    await observer.waitFor(() => session(discount)?.state === 'waiting_input', 'Enter answers the approval and the turn finishes');
    await waitForPaneText(client, discount, pane.paneId, (text) => text.includes('migrated'), 'the reply shows in the tile');
    await setSetting('queue_mode_enabled', 'false');
  });

  await runner.step('closing_the_last_tile_leaves_the_session_closed_in_the_ledger', async () => {
    const pane = await paneOf(discount);
    await client.request('focus_pane', { sessionId: discount, paneId: pane.paneId });
    await client.request('dispatch_shortcut', { shortcutId: 'session.close' });
    await observer.waitFor(() => !observer.sessionsById.has(discount), 'the session closes with its last tile');
    const shown = attn('session', 'show', discount);
    runner.writeText('session-show-closed.txt', shown);
    runner.assert(/^state\s+closed$/m.test(shown), 'the ledger reads the session as closed', { shown });
    runner.assert(/^usage\s+[1-9]/m.test(shown), 'the closed session keeps its usage', { shown });
    runner.assert(session(checkout) && tileOf(checkout), 'the other session and its tile are untouched', session(checkout));
    runner.assert(attn('session', 'list', '--closed').includes(discount), 'the ledger lists the closed session', { discount });
  });

  console.log(JSON.stringify(await runner.finishSuccess({ sessions, checkoutConversation }), null, 2));
} catch (error) {
  console.error((await runner.finishFailure(error, { sessions })).error);
  process.exitCode = 1;
}
