#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessInstance, dataDirForInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
assertFreshWorldTargetSafe({ instance, appPath: options.appPath });
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-CONVERSATIONS-SURFACE', tier: 'tier2-local-mock-agent', prefix: 'native-conversations-surface',
  metadata: { instance, focus: 'The native Conversations list shows why attn keeps each copy, keeps and unkeeps from the row, and forgets only after a confirmation that names the crew letters losing their transcript, without the app taking focus' },
});
process.env.ATTN_TOOL_HOME = path.join(runner.runDir, 'tool-home');
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const cli = (...argv) => execFileSync(appCliInTree(options.appPath), argv, { env: instanceCliEnv(instance), encoding: 'utf8' });

const sessions = [];
const openSessions = new Set();
const seeds = [];
const member = `trellis-${Date.now().toString(36)}`;
const memberHome = path.join(dataDirForInstance(instance), 'crew', member);
let memberRegistered = false;
runner.registerCleanup('delete_member', () => {
  if (memberRegistered) cli('doc', 'delete', 'core/crew', 'members', member);
  fs.rmSync(memberHome, { recursive: true, force: true });
});
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('close_seeds', () => { for (const seed of seeds) cli('seed', 'wither', seed, '-m', 'scenario complete'); });
runner.registerCleanup('close_sessions', async () => { for (const id of openSessions) await observer.unregisterSession(id); });

function awaitRows(predicate, description) {
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

const rowKey = (row) => `ledger.row.claude:${row.resume_id}`;
const nameOf = async (key) => (await driver.waitFor({ key })).name ?? '';
function nodeWhere(node, test) {
  if (!node) return null;
  if (test(node)) return node;
  for (const child of node.children ?? []) {
    const found = nodeWhere(child, test);
    if (found) return found;
  }
  return null;
}
const listRows = (tree, found = []) => { if (tree?.key?.startsWith('ledger.row.') && tree.role === 'list-box-option') found.push({ key: tree.key, value: tree.value }); for (const c of tree?.children ?? []) listRows(c, found); return found; };
const focusedKey = async () => nodeWhere(await driver.tree(), (n) => JSON.stringify(n.states ?? '').includes('focused'))?.key;
const selectedRow = async () => nodeWhere(await driver.tree(), (n) => n.key?.startsWith('ledger.row.') && n.role === 'list-box-option' && n.value === 'selected')?.key;
const choose = async (row) => {
  await driver.click({ key: rowKey(row) });
  runner.assert(await focusedKey() === 'ledger.list', 'a pressed row hands the keyboard to the list', { row: rowKey(row) });
};

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  fs.mkdirSync(memberHome, { recursive: true });
  fs.writeFileSync(path.join(memberHome, 'CHARTER.md'), '# Trellis\n\nKeep the reasons in your letter.\n');
  writeMockAgentFixture(memberHome, { name: 'Crew transcript', resumable: true, minimumWorkingMs: 0, turns: [],
    defaultActions: [{ type: 'reply', text: 'CREW_TRANSCRIPT_READY', state: 'idle' }] });

  await runner.step('launch_isolated_app', async () => {
    await client.quitApp();
    cli('daemon', 'stop');
    await launchFreshNativeAppAndConnect(client, observer);
    memberRegistered = true;
  });

  await runner.step('prepare_pinned_seed_kept_and_releasing_copies', async () => {
    const createCopy = async (label) => {
      const cwd = path.join(runner.sessionDir, label.replaceAll(' ', '-'));
      writeMockAgentFixture(cwd, { name: label, resumable: true, turns: [{ includes: 'CONVERSATION_READY', actions: [{ type: 'reply', text: 'CONVERSATION_READY', state: 'waiting_input' }] }] });
      const id = crypto.randomUUID();
      const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `${label} spawned`, 60_000);
      observer.send({ cmd: 'spawn_session', id, agent: 'claude', cwd, profile_id: observer.profileId, placement: {}, label, cols: 100, rows: 30 });
      runner.assert((await spawned).success, `spawn_session starts ${label}`);
      sessions.push(id);
      openSessions.add(id);
      const runtimeId = observer.terminalOf(id);
      await driver.click({ key: `sidebar.session.${id}` });
      await driver.state(`terminal:${runtimeId}`, { includes: '❯', timeoutMs: 15_000 });
      const replied = observer.waitForMessage((event) => event.event === 'session_state_changed' && event.session?.id === id && event.session?.state === 'waiting_input', `${label} mock turn ends`);
      await driver.typeInto(null, 'CONVERSATION_READY');
      await driver.pressKey('Enter');
      await replied;
      if (label === 'Seed work') {
        const seed = JSON.parse(cli('seed', 'plant', 'Parser work', '-m', 'Synthetic conversation fixture', '--json')).id;
        seeds.push(seed);
        cli('seed', 'tend', seed, '--session', id);
      } else {
        await observer.requestResult({ cmd: 'kept_conversation_keep', session_id: `session:${id}`, keep: true }, 'kept_conversation_keep_result');
      }
      const copied = awaitRows((result) => result.rows.some((row) => row.session_ids.includes(id) && row.kept && !row.kept.deleted_at), `${label} copy exists`);
      cli('agent', 'close', id, '-m', 'conversations scenario', '--source-session', id);
      openSessions.delete(id);
      await copied;
    };
    await createCopy('Pinned parser');
    await createCopy('Seed work');
    await createCopy('Old spike');
    const release = awaitRows((result) => result.rows.some((row) => row.session_ids.includes(sessions[2]) && row.kept?.delete_after), 'Unkeep starts the grace period');
    await observer.requestResult({ cmd: 'kept_conversation_keep', session_id: `session:${sessions[2]}`, keep: false }, 'kept_conversation_keep_result');
    runner.writeJson('daemon-copies.json', await release);
  });

  let crewCopy;
  let letterPath;
  let letterText;
  await runner.step('prepare_a_copy_kept_by_a_crew_letter', async () => {
    cli('crew', 'set', member, '--cwd', memberHome, '--agent', 'claude');
    const replied = observer.waitForMessage((event) => event.event === 'session_state_changed'
      && event.session?.directory === memberHome && event.session?.state === 'idle', 'crew mock turn ends');
    const day = JSON.parse(cli('crew', 'start', member, '--json'));
    openSessions.add(day.session_id);
    await replied;
    const copied = awaitRows((result) => result.rows.some((row) => row.session_ids.includes(day.session_id)
      && row.kept && !row.kept.deleted_at && row.crew_letters?.length), 'crew letter keeps its transcript');
    const handoff = JSON.parse(cli('handoff', '--session', day.session_id, '-m', 'Dear successor, keep the reasons.', '--json'));
    openSessions.delete(day.session_id);
    letterPath = handoff.path;
    letterText = fs.readFileSync(letterPath, 'utf8');
    crewCopy = (await copied).rows.find((row) => row.session_ids.includes(day.session_id));
    runner.writeJson('crew-copy.json', crewCopy);
  });

  const all = (await observer.requestResult({ cmd: 'kept_conversation_list', include_deleted: false }, 'kept_conversation_list_result')).kept_conversation_list_result;
  const byLabel = (id) => all.rows.find((row) => row.session_ids.includes(id));
  const [pinned, seeded, releasing] = sessions.map(byLabel);

  await runner.step('open_the_third_list_with_keys', async () => {
    await driver.run('sessions.open');
    await driver.waitFor({ key: 'ledger.panel' });
    await driver.pressKey(']');
    await driver.pressKey(']');
    await driver.waitFor({ key: rowKey(releasing) });
    runner.assert(nodeWhere(await driver.tree(), (n) => n.key === 'ledger.tab.conversations')?.value === 'pressed', '] twice shows Conversations');
    runner.assert((await nameOf(`${rowKey(pinned)}.meta`)).includes('Forever · pinned'), 'a kept copy says it is pinned');
    runner.assert((await nameOf(`${rowKey(seeded)}.meta`)).includes('Open: parser-work'), 'a seed copy names the open seed');
    runner.assert((await nameOf(`${rowKey(releasing)}.meta`)).includes('Deletes '), 'a released copy says when it goes');
    runner.assert((await nameOf('ledger.counts')).startsWith(`${all.count} kept`), 'the daemon\'s total shows', { count: all.count });
    await driver.screenshot(path.join(runner.runDir, 'conversations.png'));
  });

  await runner.step('keep_and_unkeep_from_the_row', async () => {
    await choose(releasing);
    const kept = awaitRows((result) => result.rows.some((row) => row.resume_id === releasing.resume_id && row.pinned_at), 'the row becomes pinned');
    await driver.pressKey('Enter');
    await kept;
    await driver.waitFor({ key: `${rowKey(releasing)}.verb`, name: 'Unkeep' });
    const released = awaitRows((result) => result.rows.some((row) => row.resume_id === releasing.resume_id && !row.pinned_at && row.kept?.delete_after), 'the row releases again');
    await driver.pressKey('Enter');
    await released;
    await driver.waitFor({ key: `${rowKey(releasing)}.verb`, name: 'Keep forever' });
  });

  await runner.step('forget_asks_first_and_cancel_keeps_the_copy', async () => {
    await choose(pinned);
    await driver.pressKey('2');
    const confirm = await nameOf('ledger.inspector.confirm');
    runner.assert(confirm.includes('Delete attn’s copy of Pinned parser') && confirm.includes('Claude’s own files are not touched'), 'the confirmation names the copy and what stays', { confirm });
    await driver.screenshot(path.join(runner.runDir, 'confirmation.png'));
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'ledger.inspector.confirm' }, { gone: true });
    const afterCancel = (await observer.requestResult({ cmd: 'kept_conversation_list' }, 'kept_conversation_list_result')).kept_conversation_list_result;
    runner.assert(afterCancel.rows.some((row) => row.resume_id === pinned.resume_id && !row.kept?.deleted_at), 'Cancel keeps the copy');
    await driver.pressKey('2');
    const deleted = awaitRows((result) => result.rows.some((row) => row.resume_id === pinned.resume_id && row.kept?.deleted_by === 'user'), 'a confirmed Forget deletes the copy');
    await driver.pressKey('2');
    await deleted;
    await driver.waitFor({ key: rowKey(pinned) }, { gone: true });
    runner.writeJson('tree-after-forget.json', await driver.tree());
    runner.assert(await focusedKey() === 'ledger.list' && Boolean(await selectedRow()), 'after Forget the keyboard stays on the list with a surviving row chosen', { focused: await focusedKey(), selected: await selectedRow(), rows: listRows(await driver.tree()) });
    await driver.click({ key: 'ledger.deleted' });
    await driver.waitFor({ key: rowKey(pinned) });
    runner.assert((await nameOf(`${rowKey(pinned)}.meta`)).includes('You deleted attn’s copy on '), 'Show deleted shows what was forgotten');
    await driver.screenshot(path.join(runner.runDir, 'deleted.png'));
  });

  await runner.step('forget_a_crew_copy_after_the_letter_warning', async () => {
    await choose(crewCopy);
    await driver.pressKey('2');
    const letter = crewCopy.crew_letters[0];
    const date = new Intl.DateTimeFormat('en-GB', { year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' }).format(new Date(letter.occurred_at));
    const label = `${member[0].toUpperCase()}${member.slice(1)} · ${date}`;
    const stay = await nameOf('ledger.inspector.letters-stay');
    runner.assert(stay.includes(`These letters stay, but will no longer point to this transcript: ${label}.`), 'the confirmation names the member and date and says the letter stays', { stay, label });
    await driver.screenshot(path.join(runner.runDir, 'crew-confirmation.png'));
    await driver.pressKey('Enter');
    runner.assert(fs.existsSync(`${letterPath}.json`), 'Cancel keeps the letter\'s session record');
    await driver.pressKey('2');
    const deleted = awaitRows((result) => result.rows.some((row) => row.resume_id === crewCopy.resume_id
      && row.kept?.deleted_by === 'user' && !row.crew_letters?.length), 'a confirmed crew Forget deletes the copy and its reference');
    await driver.pressKey('2');
    await deleted;
    runner.assert(!fs.existsSync(`${letterPath}.json`) && fs.readFileSync(letterPath, 'utf8') === letterText, 'Forget removes only the letter\'s session record');
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ instance, screenshots: ['conversations.png', 'confirmation.png', 'deleted.png', 'crew-confirmation.png'], frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
