#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { installMockCodex } from './mockAgent.mjs';
import { appDaemonInTree } from './platform.mjs';
import { ensureCodexInitialPanePromptReady } from './scenarioAgents.mjs';
import { waitForPaneText, waitForSessionDesktop } from './scenarioAssertions.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const instance = currentHarnessInstance();
if (!instance) throw new Error('The Codex update scenario requires a named, non-production instance');
const runner = createScenarioRunner(options, { scenarioId: 'CODEX-UPDATE', tier: 'tier2-local-mock-agent', prefix: 'scenario-codex-update' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const daemonBinary = appDaemonInTree(options.appPath);
const attn = (...args) => execFileSync(daemonBinary, args, { encoding: 'utf8', env: instanceCliEnv(instance) });

const BANNER = '[aria-label="Codex updates"]';
const BANNER_ACTION = `${BANNER} .banner-button`;
const CONFIRM = '.shared-codex-prompt .mp-button.primary';
const baseline = new Map();
const sessions = [];

async function setSetting(key, value) {
  if (!baseline.has(key)) baseline.set(key, observer.getSetting(key));
  await client.request('set_setting', { key, value });
  await observer.waitFor(() => observer.getSetting(key) === value, `${key}=${value}`);
}

async function bannerText() {
  return (await client.request('dom_text', { selector: BANNER }).catch(() => null))?.text ?? '';
}

async function waitForBanner(expected, description, timeoutMs = 30_000) {
  const started = Date.now();
  let last = '';
  while (Date.now() - started < timeoutMs) {
    last = await bannerText();
    if (last.includes(expected)) return last;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`timed out waiting for the Codex banner to show ${description}; it shows ${JSON.stringify(last)}`);
}

async function paneOf(sessionId) {
  const desktop = await waitForSessionDesktop(client, sessionId, (entry) => entry.panes.some((pane) => pane.sessionId === sessionId && pane.runtimeId), `a tile showing ${sessionId}`);
  return desktop.panes.find((pane) => pane.sessionId === sessionId);
}

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_settings', async () => {
  for (const [key, value] of baseline) await client.request('set_setting', { key, value }).catch(() => {});
});
runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, sessions.filter((id) => observer.getSession(id))));

try {
  const codex = installMockCodex(path.join(runner.sessionDir, 'codex-install'), { version: '1.0.0', latest: '1.1.0' });

  await runner.step('launch', () => launchFreshAppAndConnect(client, observer, { agentExecutables: { codex } }));

  await runner.step('shared_codex_offers_the_newer_release', async () => {
    await setSetting('auto_approve_enabled', 'false');
    await setSetting('codex_shared_enabled', 'true');
    const cwd = path.join(runner.sessionDir, 'shop');
    fs.mkdirSync(cwd, { recursive: true });
    const shop = await createSessionAndWaitForInitialPane({
      client, observer, cwd, label: 'shop', agent: 'codex', promptReadyFn: ensureCodexInitialPanePromptReady,
    });
    sessions.push(shop);
    await waitForBanner('Codex 1.1.0 is available.', 'the 1.1.0 offer');
  });

  await runner.step('install_runs_in_a_tile_and_shared_codex_moves_to_it', async () => {
    const before = new Set(observer.sessionsById.keys());
    await client.request('dom_click', { selector: BANNER_ACTION });
    const shell = await observer.waitFor(
      () => [...observer.sessionsById.values()].find((session) => !before.has(session.id) && session.agent === 'shell')?.id,
      'a shell tile running Codex\'s update',
      30_000,
    );
    sessions.push(shell);
    const tile = await paneOf(shell);
    await waitForPaneText(client, shell, tile.paneId, (text) => text.includes('Codex updated to 1.1.0'), 'Codex\'s update output in the new tile', 30_000);
    await waitForBanner('1 tile still runs an older Codex.', 'the restart onto 1.1.0 with the shop tile left on 1.0.0', 60_000);
    runner.assert((await bannerText()).startsWith('Codex 1.1.0 is running.'), 'shared Codex runs 1.1.0 after the install', await bannerText());
    const status = attn('codex', 'status');
    runner.assert(/^server\s+1\.1\.0$/m.test(status), 'attn codex status reports the app-server on 1.1.0', status);
  });

  await runner.step('reload_tiles_moves_the_old_tile_to_the_new_codex', async () => {
    await client.request('dom_click', { selector: BANNER_ACTION });
    await client.request('dom_wait', { selector: CONFIRM, timeoutMs: 10_000 });
    await client.request('dom_click', { selector: CONFIRM });
    await client.request('dom_wait', { selector: BANNER, absent: true, timeoutMs: 30_000 });
    const status = attn('codex', 'status');
    runner.assert(/^server\s+1\.1\.0$/m.test(status) && !/^old tiles/m.test(status), 'no tile runs an older Codex after the reload', status);
  });

  console.log(JSON.stringify(await runner.finishSuccess({ sessions }), null, 2));
} catch (error) {
  console.error((await runner.finishFailure(error, { sessions })).error);
  process.exitCode = 1;
}
