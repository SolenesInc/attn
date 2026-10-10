#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

// Reads follow daemon facts, so the keeper's completion is the signal, never a sleep.
function awaitRows(observer, predicate, description) {
  const request_id = crypto.randomUUID();
  const read = () => observer.send({ cmd: 'kept_conversation_list', request_id, include_deleted: true });
  const ready = observer.waitForMessage((event) => {
    if (event.event === 'kept_conversations_changed') read();
    if (event.event === 'kept_conversation_list_result' && event.request_id === request_id && event.success
      && predicate(event.kept_conversation_list_result)) return event.kept_conversation_list_result;
    return null;
  }, description);
  read();
  return ready;
}

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  const options = parseCommonArgs(args);
  if (options.help) return;
  const instance = currentHarnessInstance();
  assertFreshWorldTargetSafe({ instance, appPath: options.appPath });
  const runner = createScenarioRunner(options, {
    scenarioId: 'CONVERSATIONS-SURFACE', tier: 'tier1-local-shell', prefix: 'conversations-surface',
    metadata: { instance, focus: 'kept conversation reasons, keep/unkeep, confirmed Forget and keyboard navigation' },
  });
  process.env.ATTN_TOOL_HOME = path.join(runner.runDir, 'tool-home');
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const driver = createWindowDriver({ client });
  const binary = appDaemonInTree(options.appPath);
  const sessions = [];
  const openSessions = new Set();
  const seeds = [];
  const cli = (...argv) => execFileSync(binary, argv, { env: instanceCliEnv(instance), encoding: 'utf8' });
  const text = async (selector) => (await client.request('dom_text', { selector })).text;
  const key = (selector, value) => client.request('dom_key', { selector, key: value });
  const selector = (id) => `.ledger-row[data-row-key="claude:${id}"]`;
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('close_seeds', () => { for (const seed of seeds) cli('seed', 'wither', seed, '-m', 'scenario complete'); });
  runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, [...openSessions]));
  try {
    await runner.step('launch_isolated_app', () => launchFreshAppAndConnect(client, observer));
    await runner.step('prepare_pinned_seed_kept_and_releasing_copies', async () => {
      const createCopy = async (label) => {
        const cwd = path.join(runner.sessionDir, label.replaceAll(' ', '-'));
        fs.mkdirSync(cwd, { recursive: true });
        writeMockAgentFixture(cwd, { name: label, resumable: true, turns: [{ includes: 'CONVERSATION_READY', actions: [{ type: 'reply', text: 'CONVERSATION_READY', state: 'waiting_input' }] }] });
        const id = await createSessionAndWaitForInitialPane({ client, observer, cwd, label, agent: 'claude' });
        sessions.push(id);
        openSessions.add(id);
        const replied = observer.waitForMessage((event) => event.event === 'session_state_changed' && event.session?.id === id && event.session?.state === 'waiting_input', `${label} mock turn ends`);
        await client.request('write_pane', { sessionId: id, text: 'CONVERSATION_READY' });
        await replied;
        if (label === 'Seed work') {
          const seed = JSON.parse(cli('seed', 'plant', 'Parser work', '-m', 'Synthetic conversation fixture', '--json')).id;
          seeds.push(seed);
          cli('seed', 'tend', seed, '--session', id);
        } else {
          await observer.requestResult({ cmd: 'kept_conversation_keep', session_id: `session:${id}`, keep: true }, 'kept_conversation_keep_result');
        }
        const copied = awaitRows(observer, (result) => result.rows.some((row) => row.session_ids.includes(id) && row.kept && !row.kept.deleted_at), `${label} copy exists`);
        await client.request('close_session', { sessionId: id });
        openSessions.delete(id);
        await copied;
      };
      await createCopy('Pinned parser');
      await createCopy('Seed work');
      await createCopy('Old spike');
      const release = awaitRows(observer, (result) => result.rows.some((row) => row.session_ids.includes(sessions[2]) && row.kept?.delete_after), 'Unkeep starts the grace period');
      await observer.requestResult({ cmd: 'kept_conversation_keep', session_id: `session:${sessions[2]}`, keep: false }, 'kept_conversation_keep_result');
      const result = await release;
      runner.writeJson('daemon-copies.json', result);
    });
    const all = await observer.requestResult({ cmd: 'kept_conversation_list', include_deleted: false }, 'kept_conversation_list_result');
    const copies = all.kept_conversation_list_result.rows;
    const releasing = copies.find((row) => row.session_ids.includes(sessions[2]));
    const pinned = copies.find((row) => row.session_ids.includes(sessions[0]));
    await runner.step('open_third_tab_with_native_shortcuts', async () => {
      await pressShortcutKeys(client, driver, 'sessions.open');
      await driver.pressKey(']');
      await driver.pressKey(']');
      const shown = await text('.ledger-panel');
      runner.assert(shown.includes('Pinned parser') && shown.includes('Seed work') && shown.includes('Old spike'), 'all three copies render', { shown });
      runner.assert(shown.includes('Forever · pinned') && shown.includes('Open: parser-work') && shown.includes('Deletes '), 'each retention reason renders', { shown });
      runner.assert(shown.includes(`${all.kept_conversation_list_result.count} kept`), 'daemon live total renders', { shown });
      await captureScreenshotData(path.join(runner.runDir, 'conversations.png'), { client, selector: '.ledger-panel' });
    });
    await runner.step('keep_and_unkeep_from_the_row', async () => {
      const changed = awaitRows(observer, (result) => result.rows.some((row) => row.resume_id === releasing.resume_id && row.pinned_at), 'row becomes pinned');
      await key(selector(releasing.resume_id), 'Enter');
      await changed;
      runner.assert((await text(selector(releasing.resume_id))).includes('Unkeep'), 'Keep changes the visible action to Unkeep');
      const released = awaitRows(observer, (result) => result.rows.some((row) => row.resume_id === releasing.resume_id && !row.pinned_at && row.kept?.delete_after), 'row releases again');
      await key(selector(releasing.resume_id), 'Enter');
      await released;
    });
    await runner.step('forget_requires_confirmation_and_cancel_preserves_the_copy', async () => {
      await key(selector(pinned.resume_id), '2');
      const shown = await text('.ledger-inspector');
      runner.assert(shown.includes('Delete attn’s copy of Pinned parser') && shown.includes('Claude’s own files are not touched'), 'confirmation names the copy and boundary', { shown });
      await captureScreenshotData(path.join(runner.runDir, 'confirmation.png'), { client, selector: '.ledger-panel' });
      await key(selector(pinned.resume_id), 'Enter');
      const afterCancel = await observer.requestResult({ cmd: 'kept_conversation_list' }, 'kept_conversation_list_result');
      runner.assert(afterCancel.kept_conversation_list_result.rows.some((row) => row.resume_id === pinned.resume_id), 'Cancel keeps the copy');
      await key(selector(pinned.resume_id), '2');
      const deleted = awaitRows(observer, (result) => result.rows.some((row) => row.resume_id === pinned.resume_id && row.kept?.deleted_by?.ref === 'user'), 'confirmed Forget tombstones the copy');
      await client.request('dom_focus', { selector: selector(pinned.resume_id) });
      await driver.pressKey('2');
      await deleted;
      const focus = await client.request('dom_active_element', { selector: '.ledger-row' });
      runner.assert(focus.matches, 'Forget transfers keyboard focus to a surviving row', { focus });
      runner.assert(!(await text('.ledger-list')).includes('Pinned parser'), 'Forgotten copy leaves the default view');
      await client.request('dom_click', { selector: '.ledger-deleted-toggle input' });
      runner.assert((await text('.ledger-list')).includes('You deleted attn’s copy on '), 'Show deleted reveals the tombstone');
      await captureScreenshotData(path.join(runner.runDir, 'deleted.png'), { client, selector: '.ledger-panel' });
    });
    await runner.finishSuccess({ instance, screenshots: ['conversations.png', 'confirmation.png', 'deleted.png'] });
  } catch (error) {
    await runner.finishFailure(error);
    throw error;
  } finally {
    await runner.finishCleanup();
  }
}
main().catch((error) => { console.error(error); process.exitCode = 1; });
