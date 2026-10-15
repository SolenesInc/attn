#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, printCommonHelp } from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { appDaemonInTree, appPlatform, createWindowDriver } from './platform.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';

const options = parseCommonArgs(process.argv.slice(2));
if (options.help) { printCommonHelp('scripts/real-app-harness/scenario-shell-titles.mjs'); process.exit(0); }
const runner = createScenarioRunner(options, { scenarioId: 'SHELL-TITLES', tier: 'tier1-local-shell', prefix: 'shell-titles' });
const instance = currentHarnessInstance();
const binary = appDaemonInTree(options.appPath);
const client = new UiAutomationClient(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
let observer;
const sessions = [];
runner.registerCleanup('restore_daemon_environment', async () => {
  await observer?.close();
  await client.quitApp().catch(() => {});
  execFileSync(binary, ['daemon', 'stop'], { env: instanceCliEnv(instance) });
});
runner.registerCleanup('close_shells', async () => {
  for (const sessionId of sessions) await client.request('close_session', { sessionId }).catch(() => {});
});

function titleAfter(sessionId, predicate) {
  const current = observer.sessionsById.get(sessionId);
  if (current && predicate(current.title)) return Promise.resolve(current.title);
  return observer.waitForMessage((event) => event.event === 'session_state_changed'
    && event.session.id === sessionId && predicate(event.session.title) && event.session.title, 'shell title');
}

async function display(sessionId, title) {
  for (const selector of [
    `[data-testid="sidebar-session-${sessionId}"] .session-label`,
    `[data-pane-session-id="${sessionId}"] .desktop-pane-title`,
  ]) {
    await client.request('dom_wait', { selector, textIncludes: title, timeoutMs: 20_000 });
    const { text } = await client.request('dom_text', { selector });
    runner.assert(text === title, `Expected ${JSON.stringify(title)} in ${selector}, received ${JSON.stringify(text)}`);
  }
}

try {
  for (const shell of ['zsh', 'fish']) {
    await runner.step(`${shell}_launch`, async () => {
      await observer?.close();
      await client.quitApp().catch(() => {});
      const env = instanceCliEnv(instance);
      execFileSync(binary, ['daemon', 'stop'], { env });
      const shellPath = execFileSync('/usr/bin/which', [shell], { encoding: 'utf8' }).trim();
      const config = path.join(runner.sessionDir, `${shell}-config`);
      const bin = path.join(config, 'bin');
      fs.mkdirSync(bin, { recursive: true });
      fs.mkdirSync(path.join(config, 'fish'), { recursive: true });
      fs.writeFileSync(path.join(config, '.zshrc'), "PS1='%~ %# '\n");
      fs.writeFileSync(path.join(config, 'fish/config.fish'), 'function fish_title; end\n');
      if (appPlatform.os === 'darwin') {
        fs.writeFileSync(path.join(bin, 'dscl'), `#!/bin/sh\nprintf 'UserShell: %s\\n' '${shellPath}'\n`, { mode: 0o755 });
      }
      execFileSync(binary, ['daemon', 'ensure'], { env: instanceCliEnv(instance, {
        PATH: `${bin}:${process.env.PATH}`, SHELL: shellPath, ZDOTDIR: config, XDG_CONFIG_HOME: config,
        XDG_DATA_HOME: path.join(config, 'data'), XDG_CACHE_HOME: path.join(config, 'cache'),
      }) });
      observer = new DaemonObserver({ wsUrl: options.wsUrl });
      await launchFreshAppAndConnect(client, observer);
    });
    let sessionId;
    await runner.step(`${shell}_folder`, async () => {
      const cwd = path.join(runner.sessionDir, `${shell}-shop`);
      fs.mkdirSync(cwd, { recursive: true });
      sessionId = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: '', agent: 'shell' });
      sessions.push(sessionId);
      await display(sessionId, path.basename(cwd));
      const desktop = await client.request('get_desktop', { sessionId });
      await client.request('click_pane', { sessionId, paneId: desktop.panes[0].paneId });
      const destination = path.join(runner.sessionDir, `${shell}-warehouse`);
      fs.mkdirSync(destination, { recursive: true });
      const changed = titleAfter(sessionId, (title) => title === path.basename(destination));
      await driver.typeText(`cd '${destination}'`);
      await driver.pressEnter();
      await display(sessionId, await changed);
      await client.request('capture_native_window_screenshot', { path: path.join(runner.runDir, `${shell}-prompt.png`) });
    });
    await runner.step(`${shell}_nvim`, async () => {
      const title = titleAfter(sessionId, (value) => value?.includes('title-proof.md') && value.includes('Nvim'));
      await driver.typeText("nvim -u NONE -i NONE --cmd 'set title' +'redraw' title-proof.md");
      await driver.pressEnter();
      await display(sessionId, await title);
      await client.request('capture_native_window_screenshot', { path: path.join(runner.runDir, `${shell}-nvim.png`) });
      const prompt = titleAfter(sessionId, (value) => value === `${shell}-warehouse`);
      await driver.typeText(':qa!');
      await driver.pressEnter();
      await display(sessionId, await prompt);
    });
    await client.request('close_session', { sessionId });
  }
  await runner.finishSuccess({ sessionIds: sessions });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
