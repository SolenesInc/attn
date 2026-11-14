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
  scenarioId: 'NATIVE-SESSION-CLOSE-LEDGER', tier: 'tier2-local-mock-agent', prefix: 'native-session-close-ledger',
  metadata: { agent: 'claude', instance, focus: 'Closing a worktree session from the native app keeps the worktree, asks nothing, and stays readable in the ledger across a daemon restart, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const cli = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const git = (cwd, ...args) => execFileSync('git', args, { cwd, encoding: 'utf8', env: instanceCliEnv() }).trim();
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

function buildWorktree(root, branch) {
  const repo = path.join(root, 'repo');
  fs.mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  git(repo, 'config', 'user.email', 'harness@attn.local');
  git(repo, 'config', 'user.name', 'attn harness');
  fs.writeFileSync(path.join(repo, 'README.md'), '# close ledger fixture\n');
  git(repo, 'add', 'README.md');
  git(repo, 'commit', '-q', '-m', 'fixture');
  const worktree = path.join(root, `repo--${branch}`);
  git(repo, 'worktree', 'add', '-q', '-b', branch, worktree);
  return { repo, worktree };
}

const sessionId = crypto.randomUUID();
try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  const branch = `close-ledger-${runner.runId}`;
  const { repo, worktree } = await runner.step('build_worktree', () => buildWorktree(runner.sessionDir, branch));
  writeMockAgentFixture(worktree, { name: 'close ledger mock', turns: [] });

  await runner.step('launch_app', async () => {
    await client.quitApp();
    cli('daemon', 'stop');
    await launchFreshNativeAppAndConnect(client, observer);
  });

  let runtimeId;
  await runner.step('show_a_worktree_session', async () => {
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === sessionId && message, 'agent spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: sessionId, agent: 'claude', cwd: worktree, profile_id: observer.profileId, placement: {}, label: `close-ledger-${runner.runId}`.slice(0, 40), cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session starts the agent in the worktree');
    const registered = await observer.waitFor(() => {
      const session = observer.getSession(sessionId);
      return session?.is_worktree ? session : null;
    }, 'the session is known as a worktree', 20_000);
    runner.assert(registered.main_repo === repo, 'the session points back at its main repository', { mainRepo: registered.main_repo, repo });
    runner.writeJson('worktree-session.json', registered);
    runtimeId = observer.terminalOf(sessionId);
    await driver.click({ key: `sidebar.session.${sessionId}` });
    await driver.state(`terminal:${runtimeId}`, { includes: '❯', timeoutMs: 15_000 });
    const window = await driver.state('window', { includes: sessionId, timeoutMs: 15_000 });
    runner.assert(window.on_screen === sessionId, 'the worktree session is on screen', window);
  });

  await runner.step('close_asks_nothing', async () => {
    await driver.run('session.close');
    await driver.waitFor({ key: `sidebar.session.${sessionId}` }, { gone: true });
    await observer.waitFor(() => !observer.getSession(sessionId), 'the session leaves the live sessions');
    const window = await driver.state('window');
    runner.assert(window.layer === null && window.on_screen !== sessionId, 'the close opens no dialog and leaves the session off screen', window);
    runner.writeJson('window-after-close.json', window);
  });

  await runner.step('worktree_survives', async () => {
    runner.assert(fs.existsSync(worktree), 'the worktree folder survives the close', { worktree });
    const listed = git(repo, 'worktree', 'list', '--porcelain');
    runner.assert(listed.includes(worktree), 'Git still knows the worktree', { listed });
    runner.assert(git(repo, 'branch', '--list', branch).length > 0, 'the worktree branch survives the close', { branch });
    runner.writeText('worktree-list.txt', `${listed}\n`);
  });

  await runner.step('ledger_reads_the_close_back', async () => {
    const shown = cli('session', 'show', sessionId);
    runner.assert(/^state\s+closed$/m.test(shown), 'session show reports the session closed', { shown });
    runner.assert(/^closed\s+.* by user$/m.test(shown), 'session show names the user as the closer', { shown });
    runner.assert(/^worktree\s+yes, of /m.test(shown), 'session show keeps the worktree it ran in', { shown });
    const closed = cli('session', 'list', '--closed');
    runner.assert(closed.includes(sessionId), 'session list --closed lists the closed session', { closed });
    const live = cli('session', 'list');
    runner.assert(!live.includes(sessionId), 'session list leaves out a closed session', { live });
    runner.writeText('session-show.txt', shown);
    await driver.run('sessions.open');
    await driver.click({ key: 'ledger.scope.closed' });
    const row = await driver.waitFor({ key: `ledger.row.${sessionId}.verb`, name: 'Reopen' });
    runner.assert(Boolean(row), 'the native ledger lists the session closed with Reopen');
    await driver.screenshot(path.join(runner.runDir, 'native-ledger-closed.png'));
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'ledger.panel' }, { gone: true });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.step('close_survives_a_daemon_restart', async () => {
    await client.quitApp();
    cli('daemon', 'stop');
    cli('daemon', 'ensure');
    const shown = cli('session', 'show', sessionId);
    runner.assert(/^state\s+closed$/m.test(shown), 'a restart neither brings back nor removes the closed session', { shown });
    runner.writeText('session-show-after-restart.txt', shown);
  });

  await runner.finishSuccess({ sessionId, worktree, repo, branch, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error, { sessionId });
  process.exitCode = 1;
}
