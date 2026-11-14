#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('The native settings scenario changes settings, so it needs a named instance');
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SETTINGS', tier: 'tier1-local-shell', prefix: 'native-settings',
  metadata: { focus: 'The native Settings dialog opens from its shortcut, the palette and the home gear, moves between sections, searches, saves a click and a field, shows a refusal beside its field and a change made from the shell, and closes by Escape, its Close button and its backdrop, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const env = instanceCliEnv(instance);
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env, stdio: ['ignore', 'pipe', 'pipe'] });
const mac = process.platform === 'darwin';
const OPEN_SETTINGS = mac ? 'mod+,' : 'mod+shift+,';
const PALETTE = mac ? 'mod+shift+k' : 'mod+alt+k';
const DIALOG = { key: 'settings-dialog' };
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const baseline = new Map();
const remember = (key) => { if (!baseline.has(key)) baseline.set(key, observer.getSetting(key)); };
const savedAs = (key, value) => observer.waitForMessage((m) => m.event === 'settings_updated' && m.settings?.[key] === value && m, `${key} saved as ${JSON.stringify(value)}`, 15_000);
const nodes = async () => {
  const all = [];
  const walk = (node) => { all.push(node); (node.children ?? []).forEach(walk); };
  walk(await driver.tree());
  return all;
};
const node = async (key) => (await nodes()).find((n) => n.key === key) ?? null;
const section = async (id) => {
  await driver.click({ key: `settings.nav.${id}` });
  await driver.state('settings', { includes: id });
  const { activeSection } = await driver.state('settings');
  runner.assert(activeSection === id, `the nav selects ${id}`, { activeSection });
};
const replaceText = async (key, text) => {
  await driver.click({ key });
  await driver.pressKey('End');
  const typed = (await node(key))?.value ?? '';
  for (let i = 0; i < [...typed].length; i += 1) await driver.pressKey('Backspace');
  if (text) await driver.typeInto({ key }, text);
};
const closed = () => driver.waitFor(DIALOG, { gone: true });

try {
  const frontmostBefore = mac ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    for (const key of ['theme', 'worktree_sweep_enabled', 'projects_directory']) remember(key);
    runner.registerCleanup('restore_settings', () => Promise.all([...baseline].map(([key, value]) => observer.requestResult({ cmd: 'set_setting', key, value }, 'settings_updated').catch(() => {}))));
    await driver.waitFor({ key: 'home' });
  });

  await runner.step('the_shortcut_opens_the_dialog_on_github_hosts_and_closes_it', async () => {
    await driver.pressKey(OPEN_SETTINGS);
    await driver.waitFor(DIALOG);
    runner.assert((await driver.state('settings')).activeSection === 'connectivity', 'the first open shows GitHub hosts');
    const heading = await driver.find({ key: 'settings.heading' });
    runner.assert(heading?.name === 'GitHub hosts', 'the page is headed by its section', heading);
    await driver.screenshot(path.join(runner.runDir, 'native-settings-open.png'));
    await driver.pressKey(OPEN_SETTINGS);
    await closed();
  });

  await runner.step('the_palette_opens_the_dialog', async () => {
    await driver.pressKey(PALETTE);
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, 'Settings');
    await driver.waitFor({ key: 'palette.row.command:settings' });
    await driver.pressKey('Enter');
    await driver.waitFor(DIALOG);
    await driver.pressKey('Escape');
    await closed();
  });

  await runner.step('the_home_gear_opens_the_dialog', async () => {
    await driver.click({ key: 'home.settings' });
    await driver.waitFor(DIALOG);
  });

  await runner.step('the_nav_moves_between_sections', async () => {
    for (const id of ['general', 'desktop', 'hygiene', 'agents', 'experimental', 'connectivity']) await section(id);
  });

  await runner.step('a_theme_click_saves_the_theme', async () => {
    await section('general');
    const next = observer.getSetting('theme') === 'light' ? ['Dark', 'dark'] : ['Light', 'light'];
    const saved = savedAs('theme', next[1]);
    await driver.click({ role: 'radio', name: next[0] });
    await saved;
    runner.assert(attn('settings', 'get', 'theme').trim() === next[1], 'the shell reads the theme the dialog saved');
  });

  await runner.step('search_leaves_the_sections_whose_words_match', async () => {
    await driver.typeInto({ key: 'settings.search.field' }, 'sweep');
    await driver.state('settings', { includes: 'sweep' });
    const nav = (await nodes()).filter((n) => n.key?.startsWith('settings.nav.')).map((n) => n.name);
    runner.assert(nav.length === 1 && nav[0] === 'Files and locations', 'only Files and locations holds the sweep', nav);
    await replaceText('settings.search.field', '');
    await driver.waitFor({ key: 'settings.nav.general' });
  });

  await runner.step('a_change_from_the_shell_shows_in_the_open_dialog', async () => {
    await section('desktop');
    const toggle = 'settings.switch.worktree_sweep_enabled';
    await driver.waitFor({ key: toggle });
    const flipped = (await node(toggle)).states?.includes('checked') ? 'false' : 'true';
    const saved = savedAs('worktree_sweep_enabled', flipped);
    attn('settings', 'set', 'worktree_sweep_enabled', flipped);
    await saved;
    await driver.state('settings');
    const shown = (await node(toggle)).states ?? [];
    runner.assert(shown.includes('checked') === (flipped === 'true'), 'the switch follows the shell', { flipped, shown });
    const back = flipped === 'true' ? 'false' : 'true';
    const clicked = savedAs('worktree_sweep_enabled', back);
    await driver.click({ key: toggle });
    await clicked;
  });

  await runner.step('a_refused_field_says_why_and_a_good_value_saves', async () => {
    const field = 'settings.input.projects_directory.field';
    const before = attn('settings', 'get', 'projects_directory').trim();
    await replaceText(field, 'relative/dir');
    await driver.pressKey('Enter');
    const refusal = await driver.waitFor({ key: 'settings.refusal.projects_directory' });
    runner.assert(/absolute/.test(refusal.name ?? ''), 'the refusal names what is wrong', refusal);
    await driver.waitFor({ key: 'settings.retry.projects_directory' });
    runner.assert(attn('settings', 'get', 'projects_directory').trim() === before, 'a refused value leaves the old one');
    await driver.screenshot(path.join(runner.runDir, 'native-settings-refusal.png'));
    const good = path.join(runner.sessionDir, 'projects');
    const saved = savedAs('projects_directory', good);
    await replaceText(field, good);
    await driver.pressKey('Enter');
    await saved;
    await driver.waitFor({ key: 'settings.refusal.projects_directory' }, { gone: true });
    await driver.waitFor({ key: 'settings.saved.projects_directory' });
  });

  await runner.step('close_escape_and_backdrop_each_close_the_dialog', async () => {
    await driver.click({ role: 'button', name: 'Close' });
    await closed();
    await driver.pressKey(OPEN_SETTINGS);
    await driver.waitFor(DIALOG);
    await driver.click({ key: 'settings-layer' }, { at: [0.005, 0.5] });
    await closed();
    await driver.pressKey(OPEN_SETTINGS);
    await driver.waitFor(DIALOG);
    await driver.pressKey('Escape');
    await closed();
  });

  const frontmostAfter = mac ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
