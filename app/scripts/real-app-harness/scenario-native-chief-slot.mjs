#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { MOCK_AGENT_MODEL } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-CHIEF-SLOT', tier: 'tier2-local-mock-agent', prefix: 'native-chief-slot',
  metadata: { focus: "A fresh profile's Chief setup in the native app fits the window, takes the Notebook folder and starts the Chief from the keyboard alone, and nothing marks a session chief" },
});
const instance = currentHarnessInstance();
const cliPath = appCliInTree(options.appPath);
const cli = (...args) => execFileSync(cliPath, args, { encoding: 'utf8', env: { ...instanceCliEnv(instance), ATTN_WRAPPER_PATH: cliPath }, timeout: 30_000 });
const client = new UiAutomationClient({ appPath: options.appPath, launchEnv: { ATTN_WRAPPER_PATH: cliPath } });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));

const SLOT = 'chief-slot';
const SETUP = 'chief-setup';
const PRIMARY = 'chief-setup.primary';
let profile;
let home;
let started;

function nodeWhere(node, test) {
  if (!node) return null;
  if (test(node)) return node;
  for (const child of node.children ?? []) {
    const found = nodeWhere(child, test);
    if (found) return found;
  }
  return null;
}
const node = async (key) => nodeWhere(await driver.tree(), (n) => n.key === key);
const focusedKey = async () => nodeWhere(await driver.tree(), (n) => JSON.stringify(n.states ?? '').includes('focused'))?.key;
const names = (tree, found = []) => {
  if (tree?.name) found.push(tree.name);
  for (const child of tree?.children ?? []) names(child, found);
  return found;
};
const screenshot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));
const roster = () => JSON.parse(cli('crew', 'list', '--profile', profile.id, '--json'));
const chiefOfRoster = () => roster().find((member) => member.chief);

async function assertFocus(key) {
  const focused = await focusedKey();
  runner.assert(focused === key, `${key} holds the focus`, { focused });
}

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  await runner.step('fresh_profile_opens_setup', async () => {
    for (const key of ['queue_mode_enabled', 'model_tier_overrides']) queueDaemonSettingRestore(observer, key);
    observer.send({ cmd: 'set_setting', key: 'queue_mode_enabled', value: 'true' });
    observer.send({ cmd: 'set_setting', key: 'model_tier_overrides', value: JSON.stringify([{ harness: 'claude', provider: '', model: MOCK_AGENT_MODEL, tier: 'deep' }]) });
    home = observer.profileId;
    profile = (await observer.profileCommand('profile_create', { name: `Chief slot ${runner.runId}` })).profile;
    runner.registerCleanup('delete_profile', async () => {
      const binding = chiefOfRoster()?.binding_session;
      if (binding) {
        cli('handoff', '--session', binding, '-m', 'Chief slot scenario finished.');
        await observer.waitFor(() => !observer.getSession(binding), "the Chief's session to end", 30_000);
      }
      if (home) await observer.profileCommand('profile_select', { profile_id: home });
      const left = (await driver.state('profiles')).all.find((p) => p.id === profile.id);
      if (left) await observer.profileCommand('profile_delete', { profile_id: left.id, expected_revision: left.revision });
    });
    await driver.click({ key: 'titlebar.profile' });
    await driver.click({ key: `profile-switcher.choose.${profile.id}` });
    await driver.state("profiles", { expect: { selected: profile.id }, timeoutMs: 15_000 });
    await driver.waitFor({ key: SETUP });
    const chief = chiefOfRoster();
    runner.assert(chief && !chief.agent, 'a new profile has a Chief with no harness', { chief });
    runner.assert((await node(SLOT))?.value === 'unconfigured', 'the slot reads unconfigured', await node(SLOT));
    await driver.waitFor({ key: `chief-setup.model./${MOCK_AGENT_MODEL}` }, { timeoutMs: 30_000 });
    runner.assert((await node(PRIMARY))?.value === 'ready', 'Start is ready once the models are read', await node(PRIMARY));
    await assertFocus(PRIMARY);
    const tree = await driver.tree();
    const pop = nodeWhere(tree, (n) => n.key === SETUP);
    runner.writeJson('setup-fit.json', { pop: pop?.bounds, window: tree.bounds });
    const [x, y, w, h] = pop.bounds;
    const [, , width, height] = tree.bounds;
    runner.assert(x >= 0 && y >= 0 && x + w <= width && y + h <= height, 'the first setup fits inside the window', { pop: pop.bounds, window: tree.bounds });
    await screenshot('01-chief-setup');
  });

  await runner.step('keyboard_only_setup_starts_the_chief', async () => {
    const notebookRoot = cli('settings', 'get', 'notebook.root.effective', '--profile', profile.id).trim();
    const shownFolder = (await node('chief-setup.folder'))?.name;
    runner.assert(shownFolder === notebookRoot, "the folder shows the profile's Notebook", { shownFolder, notebookRoot });
    await driver.pressKey('shift+Tab');
    await assertFocus('chief-setup.folder.choose');
    await screenshot('02-chief-folder-shown');
    await driver.pressKey('Tab');
    await assertFocus(PRIMARY);
    const bound = observer.waitForMessage((m) => m.event === 'crew_updated'
      && m.members?.find((member) => member.chief && member.profile_id === profile.id && member.binding_session)?.binding_session, "the Chief's session", 30_000);
    await driver.pressKey('Enter');
    started = await bound;
    await driver.waitFor({ key: `queue-chief.${started}` }, { timeoutMs: 30_000 });
    await driver.waitFor({ key: SETUP }, { gone: true });
    const configured = chiefOfRoster();
    const window = await driver.state('window', { includes: configured?.binding_session ?? '', timeoutMs: 15_000 });
    runner.writeJson('first-start.json', { configured, window });
    runner.assert(configured?.agent === 'claude' && configured?.model === MOCK_AGENT_MODEL, 'setup saved the preselected harness and model', { configured });
    runner.assert(Boolean(configured?.binding_session) && window.on_screen === configured.binding_session, 'the Chief woke and is shown', { configured, window });
    runner.assert(path.dirname(configured.home_dir) === path.join(notebookRoot, 'crew'), "the Chief's home is in the Notebook", { home: configured.home_dir });
    runner.assert(fs.existsSync(path.join(configured.home_dir, 'CHARTER.md')), "the Chief's charter is in the Notebook");
    await screenshot('03-chief-in-session');
  });

  await runner.step('nothing_marks_a_session_chief', async () => {
    await driver.run('session.goToDashboard');
    await driver.waitFor({ key: 'home.new' });
    await driver.waitFor({ key: `queue-chief.${started}` });
    const onHome = names(await driver.tree());
    runner.assert(!onHome.some((name) => /chief of staff/i.test(name)), 'no Chief of Staff control on Home', onHome);
    await screenshot('04-home-without-a-chief-card');
    await driver.run('session.new');
    await driver.waitFor({ key: 'new-session.agent.claude' });
    const inLauncher = names(await driver.tree());
    runner.assert(!inLauncher.some((name) => /chief of staff/i.test(name)), 'no Chief of Staff control in the new session dialog', inLauncher);
    await screenshot('05-new-session-without-a-chief-toggle');
    await driver.pressKey('Escape');
  });

  await runner.finishSuccess({ profile: profile.id });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
