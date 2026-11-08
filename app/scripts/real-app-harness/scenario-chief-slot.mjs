#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, queueDaemonSettingRestore } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { MOCK_AGENT_MODEL } from './mockAgent.mjs';
import { createWindowDriver } from './platform.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Chief slot verification requires a named instance');
const resources = resolveHarnessResources(instance);
const env = { ...instanceCliEnv(instance), ATTN_WRAPPER_PATH: resources.appCli };
const cli = args => execFileSync(resources.appCli, args, { env, encoding: 'utf8', timeout: 30_000 });
const runner = createScenarioRunner(options, { scenarioId: 'CHIEF-SLOT', tier: 'local', prefix: 'chief-slot' });
const client = new UiAutomationClient({ ...options, launchEnv: { ATTN_WRAPPER_PATH: resources.appCli } });
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const slot = '[data-testid="chief-slot"]';
const setup = '[data-testid="chief-setup"]';
const wake = `${setup} .chief-pop-primary[aria-disabled="false"]`;
const folder = `${setup} [data-testid="chief-notebook-folder"]`;
let profile;
let previousProfile;
let chief;

const waitDom = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: 15_000, ...extra });
const screenshot = async name => {
  await client.request('get_state');
  return driver.screenshot(path.join(runner.runDir, `${name}.png`), { windowId: await driver.mainWindowId() });
};
const roster = () => JSON.parse(cli(['crew', 'list', '--profile', profile.id, '--json']));
const chiefOfRoster = () => roster().find(member => member.chief);
const setting = key => cli(['settings', 'get', key, '--profile', profile.id]).trim();
const assertFits = async (label, file) => {
  const [pop, body, window] = await Promise.all([
    client.request('dom_bounds', { selector: '.chief-pop' }),
    client.request('dom_bounds', { selector: '.chief-pop-body' }),
    client.request('get_terminal_context_menu_state'),
  ]);
  runner.writeJson(file, { pop, body, window });
  const scrolls = metrics => metrics.scroll.scrollHeight > metrics.scroll.clientHeight || metrics.scroll.scrollWidth > metrics.scroll.clientWidth;
  runner.assert(!scrolls(pop) && !scrolls(body), `${label} has no scroll bars`, { pop: pop.scroll, body: body.scroll });
  const { x, y, width, height } = pop.bounds;
  runner.assert(x >= 0 && y >= 0 && x + width <= window.innerWidth && y + height <= window.innerHeight, `${label} fits inside the window`, { bounds: pop.bounds, window });
};

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_profile', async () => {
  const binding = chief && chiefOfRoster()?.binding_session;
  if (binding) {
    await client.request('select_session', { sessionId: binding });
    await waitDom('[data-session-visible="1"] [aria-label="Terminal input"]', { focused: true });
    await driver.pressKey('d', { control: true });
    await observer.waitFor(() => (observer.sessionsById.has(binding) ? null : true), `the Chief's session ${binding} to end`, 30_000);
  }
  if (previousProfile) await observer.profileCommand('profile_select', { profile_id: previousProfile });
  if (profile) {
    const current = (await client.request('get_state')).arrangement.profiles.find(candidate => candidate.id === profile.id);
    if (current) await observer.profileCommand('profile_delete', { profile_id: current.id, expected_revision: current.revision });
  }
});

const assertNothingMarksASessionChief = async (where) => {
  const { text } = await client.request('dom_text', { selector: 'body' });
  runner.assert(!/chief of staff/i.test(text), `no Chief of Staff control on the ${where}`, { text: text.slice(0, 2000) });
};

try {
  await runner.step('fresh_profile_opens_setup', async () => {
    await launchFreshAppAndConnect(client, observer);
    runner.writeJson('window-bounds.json', await client.request('get_window_bounds'));
    for (const key of ['queue_mode_enabled', 'model_tier_overrides']) queueDaemonSettingRestore(observer, key);
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await client.request('set_setting', { key: 'model_tier_overrides', value: JSON.stringify([{ harness: 'claude', provider: '', model: MOCK_AGENT_MODEL, tier: 'deep' }]) });
    previousProfile = (await client.request('get_state')).arrangement.selectedProfileId;
    profile = (await observer.profileCommand('profile_create', { name: `Chief slot ${runner.runId}` })).profile;
    await pressShortcutKeys(client, driver, 'ui.commandPalette');
    await waitDom('[role="combobox"][aria-label="Commands"]', { focused: true });
    await driver.typeText(`Switch to ${profile.name}`);
    await waitDom('.unified-palette-option[aria-selected="true"]', { textIncludes: `Switch to ${profile.name}` });
    await driver.pressEnter();
    await waitDom(`${slot}[data-chief-phase="unconfigured"]`);
    chief = chiefOfRoster();
    runner.assert(chief && !chief.agent, 'a new profile has a Chief with no harness', { chief });
    await waitDom(wake, { focused: true });
    await assertFits('first-run setup', 'setup-fit.json');
    await screenshot('01-chief-setup');
  });

  await runner.step('keyboard_only_setup_wakes_the_chief', async () => {
    const notebookRoot = setting('notebook.root.effective');
    const { text: shownFolder } = await client.request('dom_text', { selector: folder });
    runner.assert(shownFolder === notebookRoot, "the folder shows the profile's Notebook", { shownFolder, notebookRoot });
    await driver.pressKey('Tab', { shift: true });
    await waitDom(`${setup} button[aria-label="Choose the Notebook folder"]`, { focused: true });
    await screenshot('02-chief-folder-shown');
    await driver.pressKey('Tab');
    await waitDom(wake, { focused: true });
    await driver.pressEnter();
    await waitDom(`${slot}[data-chief-phase="awake"]`, { timeoutMs: 30_000 });
    await waitDom(setup, { absent: true });
    const configured = chiefOfRoster();
    const shown = (await client.request('get_state')).activeLeaf;
    runner.writeJson('first-wake.json', { configured, shown });
    runner.assert(configured?.agent === 'claude' && configured?.model === MOCK_AGENT_MODEL, 'setup saved the preselected harness and model', { configured });
    runner.assert(Boolean(configured?.binding_session) && shown?.sessionId === configured.binding_session, 'the Chief woke and is shown', { configured, shown });
    runner.assert(path.dirname(configured.home_dir) === path.join(notebookRoot, 'crew'), "the Chief's home is in the Notebook", { home: configured.home_dir });
    runner.assert(fs.existsSync(path.join(configured.home_dir, 'CHARTER.md')), "the Chief's charter is in the Notebook");
    await screenshot('03-chief-awake');
  });

  await runner.step('nothing_marks_a_session_chief', async () => {
    await pressShortcutKeys(client, driver, 'session.goToDashboard');
    await waitDom('[data-testid="chief-slot"]');
    await assertNothingMarksASessionChief('home');
    await screenshot('01b-home-without-todays-chief');
    await pressShortcutKeys(client, driver, 'session.new');
    await waitDom('[data-testid="location-picker-overlay"]');
    await assertNothingMarksASessionChief('new session dialog');
    await screenshot('01c-new-session-without-chief-toggle');
    await driver.pressKey('Escape');
  });

  console.log(JSON.stringify(await runner.finishSuccess({ profile: profile.id, chief: chief.id }), null, 2));
} catch (error) {
  try {
    runner.writeJson('chief-slot-failure-state.json', {
      app: await client.request('get_state'),
      focus: await client.request('dom_active_element'),
      setup: await client.request('dom_text', { selector: setup }).catch(cause => ({ error: String(cause) })),
    });
    await screenshot('chief-slot-failure-state');
  } catch (captureError) {
    runner.writeJson('chief-slot-failure-capture-error.json', { error: String(captureError) });
  }
  console.error(error);
  console.error(JSON.stringify(await runner.finishFailure(error, { profile: profile?.id, chief: chief?.id }), null, 2));
  process.exitCode = 1;
}
