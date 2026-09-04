#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, dataDirForProfile } from './harnessProfile.mjs';
import { delay } from './platform.mjs';
import { waitForPaneAttached, waitForPaneShellReady, waitForPaneText } from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const KEY = 'pty_shared_host_enabled';
const TOGGLE = '[data-testid="settings-shared-pty-host-toggle"]';
const STATUS = '[data-testid="settings-shared-pty-host-status"]';

async function main() {
  const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('PTY host Settings verification requires a named, non-production profile');
  const dataDir = dataDirForProfile(profile);
  const runner = createScenarioRunner(options, {
    scenarioId: 'PTY-HOST-SETTING', tier: 'tier1-local-shell', prefix: 'pty-host-setting',
    allowRealAgents: false,
  });
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const shells = [];
  let originalSetting = null;

  const cleanups = [
    ['close_shells', async () => {
      for (const shell of shells) {
        await client.request('close_pane', { sessionId: shell.sessionId, paneId: shell.paneId });
      }
    }],
    ['restore_setting', async () => {
      if (originalSetting === null) return;
      await client.request('set_setting', { key: KEY, value: originalSetting });
      await observer.waitFor(() => observer.getSetting(KEY) === originalSetting, 'original PTY setting');
    }],
    ['quit_app', () => client.quitApp()],
    ['close_observer', () => observer.close()],
  ];
  for (const [name, cleanup] of [...cleanups].reverse()) runner.registerCleanup(name, cleanup);

  function identity(runtimeId, shared) {
    const root = path.join(dataDir, shared ? 'pty-hosts' : 'workers');
    for (const instance of fs.readdirSync(root)) {
      const file = path.join(root, instance, 'registry', `${runtimeId}.json`);
      if (!fs.existsSync(file)) continue;
      const entry = JSON.parse(fs.readFileSync(file, 'utf8'));
      return { worker: entry.worker_pid, child: entry.child_pid };
    }
    throw new Error(`No ${shared ? 'shared' : 'dedicated'} registry for ${runtimeId}`);
  }

  async function openSettings() {
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await client.request('settings_select_section', { sectionId: 'agents' });
    await client.request('dom_scroll_into_view', { selector: TOGGLE });
  }

  async function checkSetting(enabled, label) {
    await observer.waitFor(() => observer.getSetting(KEY) === String(enabled)
      && observer.getSetting('pty_shared_host_active') === String(enabled), label);
    const expected = enabled ? 'New sessions use the shared Rust host.' : 'New sessions use dedicated Go workers.';
    const deadline = Date.now() + 10_000;
    let text = '';
    while (Date.now() < deadline) {
      text = (await client.request('dom_text', { selector: STATUS })).text;
      if (text === expected) break;
      await delay(100);
    }
    runner.assert(text === expected, `${label}: Settings shows the effective backend`, { text });
    const shot = await client.request('capture_screenshot_data', {
      selector: `.settings-block:has(${TOGGLE})`,
    });
    fs.writeFileSync(path.join(runner.runDir, `${label}.png`), Buffer.from(shot.pngBase64, 'base64'));
    if (process.env.ATTN_HARNESS_RECORD === '1') await delay(1500);
  }

  async function createShell(label, shared) {
    const sessionId = await createSessionAndWaitForInitialPane({
      client, observer, cwd: runner.sessionDir, label, agent: 'shell', waitForInitialPaneVisible: false,
    });
    await client.request('select_session', { sessionId });
    const workspace = await client.request('get_workspace', { sessionId });
    const pane = workspace?.panes?.[0];
    runner.assert(Boolean(pane?.paneId && pane?.runtimeId), 'created shell has a live pane');
    const shell = { ...pane, sessionId, shared, label };
    shells.push(shell);
    await waitForPaneAttached(client, sessionId, pane.paneId);
    await waitForPaneShellReady(client, sessionId, pane.paneId);
    shell.identity = identity(pane.runtimeId, shared);
    runner.assert(shell.identity.worker > 0 && shell.identity.child > 0, 'registry records live worker and child PIDs');
    runner.writeJson(`${label}-identity.json`, shell.identity);
  }

  try {
    await runner.step('launch_and_default_off', async () => {
      await launchFreshAppAndConnect(client, observer);
      await client.request('dismiss_whats_new');
      originalSetting = observer.getSetting(KEY);
      await openSettings();
      if (originalSetting === 'true') await client.request('dom_click', { selector: TOGGLE });
      await checkSetting(false, 'default-off');
      await client.request('dom_click', { selector: '[data-testid="settings-close"]' });
      await createShell('dedicated-before', false);
    });
    await runner.step('enable_and_launch_shared', async () => {
      await openSettings();
      await client.request('dom_click', { selector: TOGGLE });
      await checkSetting(true, 'enabled');
      await client.request('dom_click', { selector: '[data-testid="settings-close"]' });
      await createShell('shared-during', true);
    });
    await runner.step('disable_and_launch_dedicated', async () => {
      await openSettings();
      await client.request('dom_click', { selector: TOGGLE });
      await checkSetting(false, 'disabled');
      await client.request('dom_click', { selector: '[data-testid="settings-close"]' });
      await createShell('dedicated-after', false);
    });
    await runner.step('all_existing_terminals_still_work', async () => {
      for (const shell of shells) {
        const after = identity(shell.runtimeId, shell.shared);
        runner.assert(after.worker === shell.identity.worker && after.child === shell.identity.child,
          `${shell.label} retained its original worker and child`);
        await client.request('select_session', { sessionId: shell.sessionId });
        const marker = `PTY_ALIVE_${shell.label}`;
        await client.request('write_pane', { sessionId: shell.sessionId, paneId: shell.paneId, text: `printf '%s\\n' '${marker}'` });
        const result = await waitForPaneText(client, shell.sessionId, shell.paneId,
          (value) => value.split('\n').some((line) => line.trim() === marker), `${shell.label} fresh output`);
        runner.writeText(`${shell.label}-output.txt`, result.text);
      }
    });
    await runner.finishSuccess({ shells: shells.map(({ label, shared, identity }) => ({ label, shared, identity })) });
  } catch (error) {
    const summary = await runner.finishFailure(error);
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const [name, cleanup] of cleanups) {
      try {
        await cleanup();
        runner.log('cleanup:ok', { name });
      } catch (error) {
        runner.log('cleanup:error', { name, error: String(error) });
        process.exitCode = 1;
      }
    }
  }
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
