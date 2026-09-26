#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  pressShortcutKeys,
  queryDaemonDb,
  queueDaemonSettingRestore,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessInstance, dataDirForInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { sleep } from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

async function waitUntil(read, predicate, description, timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await read();
    if (predicate(last)) return last;
    await sleep(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last state:\n${JSON.stringify(last, null, 2)}`);
}

const arrangement = async (client) => (await client.request('get_state')).arrangement;
const waitForArrangement = (client, predicate, description) => waitUntil(() => arrangement(client), predicate, description);
const waitForSessions = (client, predicate, description, timeoutMs) =>
  waitUntil(() => client.request('sessions_get_state', {}), predicate, description, timeoutMs);
const profileNamed = (state, name) => state.profiles.find((profile) => profile.name === name) ?? null;
const rowFor = (state, sessionId) => state.rows.find((row) => row.id === sessionId) ?? null;

async function waitForBoundConversation(dbPath, sessionId, timeoutMs) {
  return waitUntil(
    () => queryDaemonDb(dbPath, `SELECT resume_session_id FROM sessions WHERE id = '${sessionId}' LIMIT 1;`),
    (resumeId) => !!resumeId,
    `session ${sessionId} to bind a conversation`,
    timeoutMs,
  );
}

const HARNESS_PROFILE_PREFIX = 'harness-';
const SESSIONS_FILTERS_SETTING = 'sessions.filters';

async function sweepHarnessProfiles(client, observer) {
  const state = await arrangement(client);
  const keeper = state.profiles.find((profile) => !profile.name.startsWith(HARNESS_PROFILE_PREFIX));
  if (!keeper) throw new Error(`every profile is a harness leftover: ${JSON.stringify(state.profiles)}`);
  const leftovers = state.profiles.filter((entry) => entry.name.startsWith(HARNESS_PROFILE_PREFIX));
  await Promise.all(leftovers.map((profile) => observer.profileCommand('profile_delete', {
    profile_id: profile.id, expected_revision: profile.revision, destination_profile_id: keeper.id,
  })));
  return waitForArrangement(client,
    (s) => s.profiles.every((profile) => !profile.name.startsWith(HARNESS_PROFILE_PREFIX))
      && s.profiles.some((profile) => profile.id === s.selectedProfileId),
    'the harness profiles of earlier runs to be gone');
}

async function openSwitcher(client, driver) {
  await pressShortcutKeys(client, driver, 'profile.switch');
  await client.request('dom_wait', { selector: '.profile-switcher', timeoutMs: 5_000 });
}

async function nameProfile(client, driver, key, name) {
  await openSwitcher(client, driver);
  await client.request('dom_key', { selector: '.profile-switcher', key });
  await client.request('dom_wait', { selector: '#profile-switcher-name', timeoutMs: 5_000 });
  await client.request('dom_type', { selector: '#profile-switcher-name', text: name });
  await client.request('dom_click', { selector: '.profile-switcher-form button[type="submit"]' });
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-profile-lifecycle.mjs');
    return;
  }

  const instance = currentHarnessInstance();
  assertFreshWorldTargetSafe({ instance, appPath: options.appPath });
  const daemonBinary = appDaemonInTree(options.appPath);
  const dbPath = path.join(dataDirForInstance(instance), 'attn.db');
  const runner = createScenarioRunner(options, {
    scenarioId: 'PROFILE-LIFECYCLE',
    tier: 'tier1-local-shell',
    prefix: 'profile-lifecycle',
    metadata: {
      agent: 'codex',
      focus: 'profiles are created, renamed and deleted from the switcher; the ledger moves a live agent between them and reopens a session whose profile was deleted into a chosen one',
      instance,
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  let sessionId = null;

  runner.registerCleanup('stop_daemon', () => execFileAsync(daemonBinary, ['daemon', 'stop'], { env: instanceCliEnv(instance) }));
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session', () => closeScenarioSessions(client, [sessionId].filter(Boolean)));

  try {
    const directory = path.join(runner.sessionDir, 'profile-lifecycle');
    fs.mkdirSync(directory, { recursive: true });
    writeMockAgentFixture(directory, { resumable: true, name: 'profile lifecycle mock', turns: [] });

    await runner.step('launch_app', async () => {
      await client.quitApp();
      await execFileAsync(daemonBinary, ['daemon', 'stop'], { env: instanceCliEnv(instance) });
      await launchFreshAppAndConnect(client, observer);
      queueDaemonSettingRestore(observer, SESSIONS_FILTERS_SETTING);
      await client.request('set_setting', { key: SESSIONS_FILTERS_SETTING, value: '' });
      await sweepHarnessProfiles(client, observer);
    });

    const tag = runner.runId.replace(/\D/g, '').slice(-6);
    const workName = `${HARNESS_PROFILE_PREFIX}work-${tag}`;
    const studioName = `${HARNESS_PROFILE_PREFIX}studio-${tag}`;

    const home = await runner.step('start_an_agent_in_the_first_profile', async () => {
      const before = await arrangement(client);
      sessionId = await createSessionAndWaitForInitialPane({
        client, observer, cwd: directory, label: `profile-lifecycle-${runner.runId}`, agent: 'codex', sessionWaitMs: 30_000,
      });
      await waitForBoundConversation(dbPath, sessionId, 60_000);
      const profile = before.profiles.find((entry) => entry.id === before.selectedProfileId);
      runner.assert(!!profile, 'the app must know the selected profile', { before });
      runner.assert(observer.getSession(sessionId)?.profile_id === profile.id, 'the agent must start in the selected profile', {
        session: observer.getSession(sessionId),
      });
      return profile;
    });

    const work = await runner.step('n_creates_a_profile_and_switches_to_it', async () => {
      await nameProfile(client, driver, 'n', workName);
      const state = await waitForArrangement(client,
        (s) => !!profileNamed(s, workName) && s.selectedProfileId === profileNamed(s, workName).id,
        'the new Work profile to be selected');
      await client.request('dom_wait', { selector: '.profile-switcher', absent: true, timeoutMs: 5_000 });
      runner.writeJson('created.json', state);
      return profileNamed(state, workName);
    });

    await runner.step('the_ledger_moves_the_agent_into_work', async () => {
      await pressShortcutKeys(client, driver, 'sessions.open');
      await waitForSessions(client, (s) => s.open && !!rowFor(s, sessionId), 'the ledger to list the agent');
      await client.request('sessions_row_action', { sessionId, action: 'Move to…', choice: workName });
      await observer.waitFor(() => observer.getSession(sessionId)?.profile_id === work.id,
        `session ${sessionId} to belong to Work`, 15_000);
      const moved = await waitForSessions(client, (s) => rowFor(s, sessionId)?.profile === work.id,
        'the ledger row to read Work');
      runner.assert(rowFor(moved, sessionId).profileLabel === workName, 'the row names its new profile', { row: rowFor(moved, sessionId) });
      runner.writeJson('moved.json', moved);
      await client.request('dom_key', { selector: '.ledger-panel', key: 'Escape' });
    });

    await runner.step('r_renames_the_profile_everywhere', async () => {
      await nameProfile(client, driver, 'r', studioName);
      const state = await waitForArrangement(client, (s) => profileNamed(s, studioName)?.id === work.id,
        'Work to be renamed Studio under the same id');
      await pressShortcutKeys(client, driver, 'sessions.open');
      const ledger = await waitForSessions(client, (s) => rowFor(s, sessionId)?.profileLabel === studioName,
        'the ledger to name the renamed profile');
      runner.writeJson('renamed.json', { state, ledger });
      await client.request('dom_key', { selector: '.ledger-panel', key: 'Escape' });
    });

    await runner.step('close_the_agent', async () => {
      await client.request('close_session', { sessionId });
      await observer.waitFor(() => !observer.sessionsById.has(sessionId), `session ${sessionId} unregistered`, 15_000);
    });

    await runner.step('delete_asks_for_a_destination_and_the_last_profile_stays', async () => {
      await openSwitcher(client, driver);
      await client.request('dom_key', { selector: '.profile-switcher', key: 'Backspace' });
      const prompt = await client.request('dom_text', { selector: '.profile-switcher' });
      runner.assert(prompt.text.includes(`Delete ${studioName}. Its agents, crew and automations move to:`),
        'delete names what moves and asks where', { prompt });
      await client.request('dom_click', { selector: '.profile-switcher .is-danger' });
      const state = await waitForArrangement(client,
        (s) => !profileNamed(s, studioName) && s.selectedProfileId === home.id,
        'Studio to be deleted and the app back on the first profile');
      await client.request('dom_key', { selector: '.profile-switcher', key: 'Delete' });
      const refusal = await client.request('dom_text', { selector: '.profile-switcher-error' });
      runner.assert(refusal.text === `${home.name} is the last profile, and attn always keeps one.`,
        'the last profile says why it stays', { refusal });
      await client.request('dom_key', { selector: '.profile-switcher', key: 'Escape' });
      runner.writeJson('deleted.json', { state, refusal });
    });

    await runner.step('reopening_into_the_deleted_profile_asks_where', async () => {
      await pressShortcutKeys(client, driver, 'sessions.open');
      await client.request('sessions_set_filter', { scope: 'Closed' });
      const closed = await waitForSessions(client,
        (s) => rowFor(s, sessionId)?.profileLabel === `${studioName} (deleted)` && rowFor(s, sessionId).actions.includes('Reopen'),
        'the closed row to keep its deleted profile and offer Reopen', 30_000);
      runner.writeJson('closed-row.json', closed);
      await client.request('sessions_row_action', { sessionId, action: 'Reopen', choice: home.name });
      await observer.waitFor(() => observer.getSession(sessionId)?.profile_id === home.id,
        `session ${sessionId} back in ${home.name}`, 60_000);
    });

    const summary = await runner.finishSuccess({ sessionId, home: home.id, work: work.id });
    console.log('[RealAppHarness] Profiles were created, renamed and deleted; the ledger moved and reopened the agent between them.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionId });
    console.error(summary.error);
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
