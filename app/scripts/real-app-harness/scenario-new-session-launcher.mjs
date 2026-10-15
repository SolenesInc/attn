#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
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
const visible = (selector) => client.request('dom_wait', { timeoutMs: 10_000, selector });
const picker = () => client.request('location_picker_get_state');
async function open(shortcut = 'session.new') {
  await pressShortcutKeys(client, driver, shortcut);
  await visible(
    '[data-launcher="one-screen"] [data-testid="location-picker-item-0"][aria-selected="true"]',
  );
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
    timeoutMs: 10_000,
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
    git('worktree', 'add', '-b', 'delete-clean', `${repo}--delete-clean`);
    git('worktree', 'add', '-b', 'delete-dirty', `${repo}--delete-dirty`);
    fs.writeFileSync(
      path.join(`${repo}--delete-dirty`, 'uncommitted.txt'),
      'keep until force',
    );
    await launchFreshAppAndConnect(client, observer);
    const previousLauncherEnabled = observer.getSetting('new_session_launcher_enabled') || 'false';
    runner.registerCleanup('launcher_setting', () => client.request('set_setting', {
      key: 'new_session_launcher_enabled', value: previousLauncherEnabled,
    }));
    for (const key of [
      'new_session_agent',
      'projects_directory',
      'keybindings_config',
    ])
      queueDaemonSettingRestore(observer, key);
    await client.request('set_setting', {
      key: 'new_session_launcher_enabled',
      value: 'true',
    });
    await client.request('set_setting', {
      key: 'new_session_agent',
      value: 'codex',
    });
    await client.request('set_setting', {
      key: 'projects_directory',
      value: runner.sessionDir,
    });
    await client.request('set_setting', {
      key: 'keybindings_config',
      value: '',
    });
    const id = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repo,
      label: 'launcher-seed',
      agent: 'shell',
    });
    sessionIds.push(id);
  });
  await runner.step('automation_opens_the_closed_launcher', async () => {
    const state = await client.request('location_picker_open');
    runner.assert(state.open && state.mode === 'path-input', 'automation must open the closed launcher');
    runner.assert(!(await client.request('location_picker_close')).open, 'automation must close the launcher');
    runner.assert((await client.request('location_picker_open')).open, 'automation must reopen the launcher');
    await client.request('location_picker_close');
  });
  await runner.step('cmd_t_is_terminal_for_one_opening', async () => {
    await open('session.newTerminal');
    runner.assert(
      (await picker()).selectedAgent === 'Terminal',
      'CmdT should select Terminal',
    );
    await driver.pressKey('Escape');
    await open();
    runner.assert(
      (await picker()).selectedAgent !== 'Terminal',
      'CmdN should use the preferred harness',
    );
    await driver.pressKey('Escape');
  });
  await runner.step('terminal_tab_browses_repo_and_back_restores_query', async () => {
    await open('session.newTerminal');
    await driver.pressKey('Tab');
    await visible('[data-testid="location-picker-path-input"]');
    const state = await picker();
    runner.assert(state.mode === 'path-input', 'Terminal Tab must stay in the folder list');
    const displayedRepo = repo.startsWith(`${os.homedir()}/`)
      ? `~${repo.slice(os.homedir().length)}`
      : repo;
    runner.assert(
      state.pathInputValue === `${displayedRepo}/`,
      'Terminal Tab must complete the repo path',
    );
    await driver.pressKey('Escape');
    await driver.pressKey('Escape');
    await open();
    await driver.typeText('launcher-project');
    await driver.pressKey('Tab');
    await visible('[role="listbox"][aria-label="Where"]');
    await driver.pressKey('Backspace');
    await visible('[data-testid="location-picker-item-0"]');
    const restored = await picker();
    runner.assert(
      restored.pathInputValue === 'launcher-project',
      'Back must restore the query',
      restored,
    );
    await driver.pressKey('Escape');
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
      timeoutMs: 10_000,
      selector: '[data-testid="location-picker-overlay"]',
      absent: true,
    });
  });
  await runner.step('existing_worktree_and_native_terminal', async () => {
    await open();
    await driver.pressKey('Tab');
    await visible('[role="listbox"][aria-label="Where"]');
    await driver.typeText('existing');
    await visible('[data-kind="existing"][aria-selected="true"]');
    const session = await startAt(tree, true);
    runner.assert(
      session.agent === 'shell',
      'Terminal mode should open the existing worktree',
    );
  });
  await runner.step('main_checkout_updates_destination_memory', async () => {
    await open();
    await driver.pressKey('Tab');
    await visible('[role="listbox"][aria-label="Where"]');
    await driver.pressKey('ArrowDown');
    await driver.pressKey('ArrowDown');
    await visible('[data-kind="main"][aria-selected="true"]');
    await startAt(repo);
    await open();
    await visible(`[data-path="${repo}"] .launcher-destination:not(.launcher-worktree)`);
    await driver.pressKey('Escape');
  });
  await runner.step('worktree_delete_cancel_clean_and_force', async () => {
    for (const branch of ['delete-clean', 'delete-dirty']) {
      await open();
      await driver.pressKey('Tab');
      await visible('[role="listbox"][aria-label="Where"]');
      await driver.typeText(branch);
      await visible('[data-kind="existing"][aria-selected="true"]');
      await driver.pressKey('Backspace', { command: true });
      await visible('[data-kind="existing"][aria-selected="true"] .launcher-badge');
      await driver.pressKey('n');
      await visible('[data-kind="existing"][aria-selected="true"] .launcher-age');
      runner.assert(fs.existsSync(`${repo}--${branch}`), 'n must keep the worktree');
      await driver.pressKey('Backspace', { command: true });
      await driver.pressKey('y');
      if (branch === 'delete-dirty') {
        await visible('[role="alert"]');
        runner.assert(
          fs.existsSync(`${repo}--${branch}`),
          'dirty worktree must wait for force confirmation',
        );
        await driver.pressKey('y');
      }
      await client.request('dom_wait', {
        timeoutMs: 10_000,
        selector: '[data-kind="existing"][aria-selected="true"]',
        absent: true,
      });
      runner.assert(
        !fs.existsSync(`${repo}--${branch}`),
        'confirmed delete must remove the worktree',
      );
      await driver.pressKey('Escape');
      await driver.pressKey('Escape');
    }
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
    await client.request('set_setting', {
      key: 'new_session_launcher_enabled',
      value: 'false',
    });
    await pressShortcutKeys(client, driver, 'session.newTerminal');
    await visible('[data-testid="location-picker"]:not([data-launcher])');
    runner.assert(
      (await picker()).selectedAgent === 'Terminal',
      'CmdT must also select Terminal in the old dialog',
    );
    await driver.pressKey('Escape');
    runner.assert((await client.request('location_picker_open')).open, 'automation must also open the old dialog');
    await client.request('location_picker_close');
  });
  console.log(JSON.stringify(await runner.finishSuccess({ sessionIds }), null, 2));
} catch (error) {
  console.error((await runner.finishFailure(error, { sessionIds })).error);
  process.exitCode = 1;
}
