#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  if (args.includes('--help')) { printCommonHelp('scenario-notebook-profile-scope'); return; }
  const options = parseCommonArgs(args);
  const instance = currentHarnessInstance();
  if (!instance) throw new Error('Notebook profile verification requires a named instance.');
  const runner = createScenarioRunner(options, {
    scenarioId: 'NotebookProfileScope', tier: 'tier1-local-shell', prefix: 'notebook-profile-scope',
    metadata: { instance, focus: 'Settings and Notebook follow the selected profile' },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const sessions = [];
  const created = [];
  const runAttn = async (argv, session = '') => {
    const { stdout } = await execFileAsync(appDaemonInTree(options.appPath), argv, {
      encoding: 'utf8', maxBuffer: Infinity,
      env: instanceCliEnv(instance, { ATTN_TERMINAL_ID: session ? observer.terminalOf(session) : '' }),
    });
    return stdout;
  };
  const waitDom = (payload) => client.request('dom_wait', { timeoutMs: 5_000, ...payload });
  const state = async () => (await client.request('get_state')).arrangement;
  const openSwitcher = async () => {
    await pressShortcutKeys(client, driver, 'profile.switch');
    await waitDom({ selector: '.profile-switcher' });
  };
  const createProfile = async (name) => {
    await openSwitcher();
    await client.request('dom_key', { selector: '.profile-switcher', key: 'n' });
    await waitDom({ selector: '#profile-switcher-name' });
    await client.request('dom_type', { selector: '#profile-switcher-name', text: name });
    await client.request('dom_click', { selector: '.profile-switcher-form button[type="submit"]' });
    await waitDom({ selector: '.profile-switcher', absent: true });
    await openSwitcher();
    await waitDom({ selector: '.profile-switcher-item:has([aria-label="Current profile"]) .profile-switcher-name', textIncludes: name });
    await client.request('dom_key', { selector: '.profile-switcher', key: 'Escape' });
    const current = await state();
    const profile = current.profiles.find((p) => p.name === name);
    runner.assert(profile?.id === current.selectedProfileId, 'creating a profile selects it', current);
    created.push(profile.id);
    return profile;
  };
  const selectProfile = async (profile) => {
    await openSwitcher();
    const profiles = (await state()).profiles;
    const labels = await Promise.all(profiles.map((_, index) => client.request('dom_text', {
      selector: `.profile-switcher-item:nth-of-type(${index + 1}) .profile-switcher-name`,
    })));
    const index = labels.findIndex((label) => label.text === profile.name);
    runner.assert(index >= 0, 'the profile is listed', labels);
    await client.request('dom_click', { selector: `.profile-switcher-item:nth-of-type(${index + 1}) .profile-switcher-choose` });
    await waitDom({ selector: '.profile-switcher', absent: true });
    await openSwitcher();
    await waitDom({ selector: '.profile-switcher-item:has([aria-label="Current profile"]) .profile-switcher-name', textIncludes: profile.name });
    await client.request('dom_key', { selector: '.profile-switcher', key: 'Escape' });
    runner.assert((await state()).selectedProfileId === profile.id, 'switching selects the profile');
  };
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('remove_synthetic_profiles', async () => {
    await closeScenarioSessions(client, sessions);
    const profiles = (await state()).profiles;
    for (const id of created) {
      const profile = profiles.find((p) => p.id === id);
      if (profile) await observer.profileCommand('profile_delete', { profile_id: id, expected_revision: profile.revision });
    }
  });
  try {
    await launchFreshAppAndConnect(client, observer);
    const tag = runner.runId.replace(/\D/g, '').slice(-6);
    const owned = [];
    for (const label of ['Work', 'Home']) {
      const entry = await runner.step(`prepare_${label}_notebook`, async () => {
        const profile = await createProfile(`Notebook ${label} ${tag}`);
        const root = path.join(runner.sessionDir, `notes-${label.toLowerCase()}`);
        await runAttn(['settings', 'set', 'notebook.root', root, '--profile', profile.id]);
        const cwd = path.join(runner.sessionDir, `agent-${label.toLowerCase()}`);
        fs.mkdirSync(cwd, { recursive: true });
        writeMockAgentFixture(cwd, { resumable: true, name: `${label} Notebook agent`, turns: [] });
        const session = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `${label} Notebook`, agent: 'codex', ownDesktop: false });
        sessions.push(session);
        await runAttn(['journal', 'append', '--date', '2026-10-10', '--entry', `${label} profile journal ${tag}`], session);
        return { profile, root, label };
      });
      owned.push(entry);
    }
    for (const entry of [...owned, owned[0]]) {
      await runner.step(`show_${entry.label}_settings_and_notebook`, async () => {
        await selectProfile(entry.profile);
        await pressShortcutKeys(client, driver, 'ui.openSettings');
        await waitDom({ selector: '[data-testid="settings-modal"]' });
        await client.request('dom_click', { selector: '[data-testid="settings-nav-desktop"]' });
        await waitDom({ selector: '[data-testid="settings-notebook-root-effective"]', textIncludes: entry.root });
        await client.request('dom_scroll_into_view', { selector: '[data-testid="settings-notebook-root-input"]' });
        const description = await client.request('dom_text', { selector: '[data-testid="settings-notebook-root-input"]' });
        runner.writeJson(`${entry.label}-settings.json`, description);
        await captureFrontWindowScreenshot(path.join(runner.runDir, `${entry.label}-settings.png`), { client, driver });
        await client.request('dom_click', { selector: '[data-testid="settings-close"]' });
        await pressShortcutKeys(client, driver, 'notebook.openFullscreen');
        await waitDom({ selector: '.notebook-browser[role="dialog"]' });
        await client.request('dom_click', { selector: '[role="treeitem"][title="journal"]' });
        await waitDom({ selector: '[role="treeitem"][title="journal/2026-10-10.md"]' });
        await client.request('dom_click', { selector: '[role="treeitem"][title="journal/2026-10-10.md"]' });
        await waitDom({ selector: '.notebook-browser-document', textIncludes: `${entry.label} profile journal ${tag}` });
        const document = await client.request('dom_text', { selector: '.notebook-browser-document' });
        const other = entry.label === 'Work' ? 'Home' : 'Work';
        runner.assert(!document.text.includes(`${other} profile journal`), 'only this profile journal is shown', document);
        await captureFrontWindowScreenshot(path.join(runner.runDir, `${entry.label}-notebook.png`), { client, driver });
        await client.request('dom_key', { selector: '.notebook-browser[role="dialog"]', key: 'Escape' });
        await waitDom({ selector: '.notebook-browser[role="dialog"]', absent: true });
      });
    }
    console.log(JSON.stringify(await runner.finishSuccess({ profiles: owned.map((entry) => entry.profile.id) }), null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error);
    console.error(summary.error);
    process.exitCode = 1;
  }
}
main().catch((error) => { console.error(error.stack || String(error)); process.exitCode = 1; });
