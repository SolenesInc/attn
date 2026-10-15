#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  pressShortcutKeys,
  queueDaemonSettingRestore,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NEW-SESSION-LAUNCHER',
  tier: 'tier1-local-shell',
  prefix: 'new-session-launcher',
  metadata: {
    focus: 'One-screen launcher and terminal shortcuts through native shortcut dispatch',
  },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const repo = path.join(runner.sessionDir, 'launcher-project');
const tree = `${repo}--existing`;
const sessionIds = [];
runner.registerCleanup('observer', () => observer.close());
runner.registerCleanup('app', () => client.quitApp());
runner.registerCleanup('sessions', async () => {
  for (const sessionId of sessionIds.reverse())
    await client.request('close_session', { sessionId });
});
const visible = (selector) => client.request('dom_wait', { selector });
const picker = () => client.request('location_picker_get_state');
async function open(shortcut = 'session.new') {
  await pressShortcutKeys(client, driver, shortcut);
  await visible('[data-launcher="one-screen"] [data-testid="location-picker-path-input"]');
}
async function startAt(directory, terminal = false) {
  const registered = observer.waitForMessage((message) => {
    const session = message.session;
    return session && session.directory === directory && !sessionIds.includes(session.id)
      ? session
      : null;
  }, `session in ${directory}`);
  await driver.pressKey('Enter', terminal ? { command: true } : {});
  const session = await registered;
  sessionIds.push(session.id);
  await client.request('dom_wait', {
    selector: '[data-testid="location-picker-overlay"]',
    absent: true,
  });
  return session;
}
try {
  await runner.step('prepare_repo_and_instance', async () => {
    fs.mkdirSync(repo, { recursive: true });
    const git = (...args) => execFileSync('git', args, { cwd: repo });
    git('init', '-b', 'main');
    git('config', 'user.email', 'harness@example.com');
    git('config', 'user.name', 'Harness');
    git('commit', '--allow-empty', '-m', 'initial');
    git('update-ref', 'refs/remotes/origin/main', 'HEAD');
    git('checkout', '-b', 'topic');
    git('commit', '--allow-empty', '-m', 'topic work');
    git('worktree', 'add', '-b', 'existing', tree);
    await launchFreshAppAndConnect(client, observer);
    for (const key of [
      'new_session_launcher_enabled',
      'new_session_agent',
      'projects_directory',
      'keybindings_config',
    ])
      queueDaemonSettingRestore(observer, key);
    await client.request('set_setting', { key: 'new_session_launcher_enabled', value: 'true' });
    await client.request('set_setting', { key: 'new_session_agent', value: 'codex' });
    await client.request('set_setting', { key: 'projects_directory', value: runner.sessionDir });
    await client.request('set_setting', { key: 'keybindings_config', value: '' });
    const id = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repo,
      label: 'launcher-seed',
      agent: 'shell',
    });
    sessionIds.push(id);
  });
  await runner.step('cmd_t_is_terminal_for_one_opening', async () => {
    await open('session.newTerminal');
    runner.assert((await picker()).selectedAgent === 'Terminal', 'CmdT should select Terminal');
    await driver.pressKey('Escape');
    await open();
    runner.assert(
      (await picker()).selectedAgent !== 'Terminal',
      'CmdN should use the preferred harness',
    );
    await driver.pressKey('Escape');
  });
  await runner.step('project_search_and_cmd_enter_terminal', async () => {
    await open();
    await driver.typeText('launcher-project');
    await visible('[data-testid="location-picker-item-0"]');
    const session = await startAt(repo, true);
    runner.assert(session.agent === 'shell', 'CmdEnter on a project must launch Terminal');
  });
  await runner.step('named_worktree_from_default', async () => {
    await open();
    await driver.pressKey('Tab');
    await visible('[role="listbox"][aria-label="Where"]');
    await driver.typeText('launcher-named');
    await visible('[data-testid="launcher-where-item-0"][aria-selected="true"]');
    const shot = await client.request('capture_screenshot_data', {
      selector: '[data-launcher="one-screen"]',
    });
    fs.writeFileSync(
      path.join(runner.runDir, 'where-list.png'),
      Buffer.from(shot.pngBase64, 'base64'),
    );
    await startAt(`${repo}--launcher-named`);
  });
  await runner.step('named_worktree_from_checkout_branch', async () => {
    await open();
    await driver.pressKey('Tab');
    await visible('[role="listbox"][aria-label="Where"]');
    await driver.typeText('from-topic');
    await driver.pressKey('ArrowDown');
    await visible('[data-testid="launcher-where-item-1"][aria-selected="true"]');
    await startAt(`${repo}--from-topic`);
    const topicHead = execFileSync('git', ['rev-parse', 'topic'], {
      cwd: repo,
      encoding: 'utf8',
    }).trim();
    const worktreeHead = execFileSync('git', ['rev-parse', 'HEAD'], {
      cwd: `${repo}--from-topic`,
      encoding: 'utf8',
    }).trim();
    runner.assert(
      topicHead === worktreeHead,
      'the current-branch row must start from the main checkout branch',
    );
  });
  await runner.step('remembered_project_bare_enter_creates_worktree', async () => {
    await open();
    const registered = observer.waitForMessage(
      (message) =>
        message.session?.directory?.startsWith(`${repo}--`) &&
        !sessionIds.includes(message.session.id)
          ? message.session
          : null,
      'new worktree session',
    );
    await driver.pressKey('Enter');
    const session = await registered;
    sessionIds.push(session.id);
    runner.assert(
      session.directory !== `${repo}--launcher-named`,
      'bare Enter should create a new worktree',
    );
    await client.request('dom_wait', {
      selector: '[data-testid="location-picker-overlay"]',
      absent: true,
    });
  });
  await runner.step('existing_worktree_and_native_terminal', async () => {
    await open('session.newTerminal');
    await driver.pressKey('Tab');
    await visible('[role="listbox"][aria-label="Where"]');
    await driver.typeText('existing');
    await visible('[data-kind="existing"][aria-selected="true"]');
    const session = await startAt(tree);
    runner.assert(session.agent === 'shell', 'Terminal mode should open the existing worktree');
  });
  await runner.step('cmd_enter_creates_terminal_worktree', async () => {
    await open();
    await driver.pressKey('Tab');
    await visible('[role="listbox"][aria-label="Where"]');
    await driver.typeText('terminal-new');
    const session = await startAt(`${repo}--terminal-new`, true);
    runner.assert(
      session.agent === 'shell',
      'CmdEnter on a new row should create a terminal worktree',
    );
  });
  await runner.step('old_dialog_cmd_t', async () => {
    await client.request('set_setting', { key: 'new_session_launcher_enabled', value: 'false' });
    await pressShortcutKeys(client, driver, 'session.newTerminal');
    await visible('[data-testid="location-picker"]:not([data-launcher])');
    runner.assert(
      (await picker()).selectedAgent === 'Terminal',
      'CmdT must also select Terminal in the old dialog',
    );
    await driver.pressKey('Escape');
  });
  console.log(JSON.stringify(await runner.finishSuccess({ sessionIds }), null, 2));
} catch (error) {
  console.error((await runner.finishFailure(error, { sessionIds })).error);
  process.exitCode = 1;
}
