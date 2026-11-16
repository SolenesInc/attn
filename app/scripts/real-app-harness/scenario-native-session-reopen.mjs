#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
assertFreshWorldTargetSafe({ instance, appPath: options.appPath });
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SESSION-REOPEN', tier: 'tier2-local-mock-agent', prefix: 'native-session-reopen',
  metadata: { agent: 'codex', instance, focus: 'The native ledger reopens a closed worktree session under its own id, and once the worktree is gone offers to recreate it and does so only when pressed, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const cli = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const git = (cwd, ...args) => execFileSync('git', args, { cwd, encoding: 'utf8', env: instanceCliEnv() }).trim();
const sessionId = crypto.randomUUID();
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('unregister_session', () => observer.getSession(sessionId) && observer.unregisterSession(sessionId));

function buildWorktree(root, branch) {
  const repo = path.join(root, 'repo');
  fs.mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  git(repo, 'config', 'user.email', 'harness@attn.local');
  git(repo, 'config', 'user.name', 'attn harness');
  fs.writeFileSync(path.join(repo, 'README.md'), '# reopen fixture\n');
  git(repo, 'add', 'README.md');
  git(repo, 'commit', '-q', '-m', 'fixture');
  const worktree = path.join(root, `repo--${branch}`);
  git(repo, 'worktree', 'add', '-q', '-b', branch, worktree);
  return { repo, worktree };
}

const verb = `ledger.row.${sessionId}.verb`;
async function closeFromTheApp() {
  await driver.click({ key: `sidebar.session.${sessionId}` });
  await driver.state('window', { includes: sessionId, timeoutMs: 15_000 });
  await driver.run('session.close');
  await driver.waitFor({ key: `sidebar.session.${sessionId}` }, { gone: true });
  await observer.waitFor(() => !observer.getSession(sessionId), 'the session leaves the live sessions', 15_000);
}
async function openClosedList() {
  await driver.run('sessions.open');
  await driver.click({ key: 'ledger.scope.closed' });
  await driver.waitFor({ key: `ledger.row.${sessionId}` });
}
async function backInTheSidebar(description) {
  await observer.waitFor(() => observer.getSession(sessionId), description, 60_000);
  await driver.waitFor({ key: 'ledger.panel' }, { gone: true });
  await driver.waitFor({ key: `sidebar.session.${sessionId}` }, { timeoutMs: 60_000 });
  const live = cli('session', 'list');
  runner.assert(live.includes(sessionId), `${description}: the session is live under its own id`, { live });
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  const branch = `session-reopen-${runner.runId}`;
  const { repo, worktree } = await runner.step('build_worktree', () => buildWorktree(runner.sessionDir, branch));
  writeMockAgentFixture(worktree, {
    resumable: true,
    name: 'session reopen mock',
    turns: [{ includes: 'start the conversation', actions: [{ type: 'reply', text: 'conversation started', state: 'idle' }] }],
  });

  await runner.step('launch_app', async () => {
    await client.quitApp();
    cli('daemon', 'stop');
    await launchFreshNativeAppAndConnect(client, observer);
  });

  await runner.step('start_a_conversation_in_a_worktree', async () => {
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === sessionId && message, 'agent spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: sessionId, agent: 'codex', cwd: worktree, profile_id: observer.profileId, placement: {}, label: `session-reopen-${runner.runId}`.slice(0, 40), cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session starts the agent in the worktree');
    const registered = await observer.waitFor(() => {
      const session = observer.getSession(sessionId);
      return session?.is_worktree ? session : null;
    }, 'the session is known as a worktree', 20_000);
    runner.assert(registered.main_repo === repo, 'the session points back at its main repository', { mainRepo: registered.main_repo, repo });
    const runtimeId = observer.terminalOf(sessionId);
    await driver.click({ key: `sidebar.session.${sessionId}` });
    await driver.state(`terminal:${runtimeId}`, { includes: '›', timeoutMs: 15_000 });
    await driver.typeInto(null, 'start the conversation');
    await driver.pressKey('Enter');
    await driver.state(`terminal:${runtimeId}`, { includes: 'conversation started', timeoutMs: 30_000 });
    await observer.waitFor(() => {
      const session = observer.getSession(sessionId);
      return session?.conversation_started && session.state === 'idle';
    }, 'the first turn ends with its conversation saved', 30_000);
  });

  await runner.step('close_from_the_app', closeFromTheApp);

  await runner.step('reopen_from_the_ledger', async () => {
    await openClosedList();
    await driver.waitFor({ key: verb, name: 'Reopen' });
    await driver.screenshot(path.join(runner.runDir, 'native-ledger-reopen.png'));
    await driver.click({ key: verb });
    await backInTheSidebar('Reopen brings the session back');
  });

  const beforeRecreate = await runner.step('close_again_and_delete_the_worktree', async () => {
    await closeFromTheApp();
    fs.rmSync(worktree, { recursive: true, force: true });
    runner.assert(!fs.existsSync(worktree), 'the fixture deletes the worktree folder', { worktree });
    return git(repo, 'worktree', 'list', '--porcelain');
  });

  await runner.step('a_refused_reopen_offers_the_recreate_action', async () => {
    await openClosedList();
    await driver.click({ key: verb });
    await driver.waitFor({ key: verb, name: 'Recreate the worktree' });
    await driver.waitFor({ key: `ledger.row.${sessionId}.note` });
    runner.assert(!fs.existsSync(worktree), 'the refused reopen does not put the worktree back', { worktree });
    runner.assert(git(repo, 'worktree', 'list', '--porcelain') === beforeRecreate, 'the refused reopen leaves the worktree registrations alone');
    await driver.screenshot(path.join(runner.runDir, 'native-ledger-refused.png'));
  });

  await runner.step('the_recreate_action_brings_it_back', async () => {
    await driver.click({ key: verb });
    await backInTheSidebar('Recreate the worktree brings the session back');
    runner.assert(fs.existsSync(worktree), 'the recreate action puts the worktree back', { worktree });
    runner.assert(git(worktree, 'rev-parse', '--abbrev-ref', 'HEAD') === branch, 'the recreated worktree is on the session\'s branch', { branch });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ sessionId, worktree, repo, branch, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error, { sessionId });
  process.exitCode = 1;
}
