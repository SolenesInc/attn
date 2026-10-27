#!/usr/bin/env node
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import {
  captureScreenshot,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  queueDaemonSettingRestore,
  writeDaemonSettings,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver, delay } from './platform.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const VENT = 'ANALYTICS7 attn seed tend refused a seed held by a session that ended two hours ago. Retried twice, then used --force.';
const WITHDRAWN = 'ANALYTICS8 attn ask list printed asks from another profile.';
const TOOL = 'ANALYTICS9 The ship-pr skill links a skill file that does not exist.';
const IDLE_MS = 5_000;

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Analytics settings verification requires a named instance');
const runner = createScenarioRunner(options, { scenarioId: 'AnalyticsSettings', tier: 'local', prefix: 'analytics-settings', allowRealAgents: false });
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-analytics-'));
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const created = [];
const binary = appDaemonInTree(options.appPath);
const cli = async (...args) => (await promisify(execFile)(binary, args, { env: instanceCliEnv(instance) })).stdout;
const wait = (selector, extra = {}) => client.request('dom_wait', { selector, timeoutMs: observer.connectTimeoutMs, ...extra });
const text = async selector => (await client.request('dom_text', { selector })).text;
const shot = name => captureScreenshot(driver, path.join(runner.runDir, `${name}.png`));
const page = '[data-testid="settings-analytics"]';
const inspector = '[data-testid="analytics-inspector"]';
async function press(key) {
  await client.request('dom_focus', { selector: page });
  await driver.pressKey(key);
}
async function vent(session, words, about = 'attn') {
  const recorded = await cli('vent', '--about', about, '--session', session, words);
  return recorded.match(/vent (v-[a-z0-9]+) recorded/)[1];
}

runner.registerCleanup('remove_scratch', () => fs.rmSync(root, { recursive: true, force: true }));
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, created.toReversed()));
try {
  await launchFreshAppAndConnect(client, observer);
  queueDaemonSettingRestore(observer, 'analytics.enabled');
  queueDaemonSettingRestore(observer, 'analytics.tool_vents');
  await writeDaemonSettings([{ key: 'analytics.enabled', value: 'true' }, { key: 'analytics.tool_vents', value: 'true' }]);
  const [first, second, third] = [path.join(root, 'garden'), path.join(root, 'deploy'), path.join(root, 'release')];
  for (const dir of [first, second, third]) fs.mkdirSync(dir);
  const venting = await createSessionAndWaitForInitialPane({ client, observer, cwd: first, label: 'fix garden bell staleness', agent: 'claude' });
  created.push(venting);
  const withdrawing = await createSessionAndWaitForInitialPane({ client, observer, cwd: second, label: 'songcheck deploy', agent: 'claude' });
  created.push(withdrawing);
  const tooling = await createSessionAndWaitForInitialPane({ client, observer, cwd: third, label: 'write release notes', agent: 'claude' });
  created.push(tooling);
  await vent(tooling, TOOL, 'tools');
  await vent(venting, VENT);
  await cli('vent', 'withdraw', '--session', withdrawing, await vent(withdrawing, WITHDRAWN));

  await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
  await runner.step('vents_listed_new_then_done', async () => {
    await client.request('dom_focus', { selector: '[data-testid="settings-nav-analytics"]' });
    await driver.pressEnter();
    await wait(page, { textIncludes: 'ANALYTICS7' });
    const list = await text('ol[aria-label="Vents"]');
    runner.assert(list.indexOf('New') < list.indexOf('ANALYTICS7') && list.indexOf('ANALYTICS7') < list.indexOf('Done') && list.indexOf('Done') < list.indexOf('ANALYTICS8'), 'new vent above the withdrawn one', list);
    await wait(inspector, { textIncludes: 'Kept on this machine' });
    await shot('00-vents');
  });
  await runner.step('tool_vent_stays_here', async () => {
    await wait('ol[aria-label="Vents"] .analytics-kind', { textIncludes: 'tools' });
    await client.request('dom_click', { selector: 'ol[aria-label="Vents"] li:nth-child(2) button' });
    await wait(inspector, { textIncludes: 'ANALYTICS9' });
    await wait(inspector, { textIncludes: 'tool vents never leave it' });
    await shot('00-tool-vent');
    await client.request('dom_click', { selector: 'ol[aria-label="Vents"] li:first-child button' });
    await wait(inspector, { textIncludes: 'ANALYTICS7' });
  });
  await runner.step('dismiss_undo_with_keys', async () => {
    await press('d');
    await wait('[role="status"]', { textIncludes: 'Dismissed “fix garden bell staleness”' });
    await wait('ol[aria-label="Vents"] .analytics-row.done', { textIncludes: 'ANALYTICS7' });
    await shot('01-dismissed');
    await press('u');
    await wait('[role="status"]', { textIncludes: 'Undone.' });
    await wait('ol[aria-label="Vents"] li:first-child', { textIncludes: 'ANALYTICS7' });
    await client.request('dom_click', { selector: 'ol[aria-label="Vents"] li:first-child button' });
    await wait(inspector, { textIncludes: 'ANALYTICS7' });
  });
  await runner.step('plant_seed_from_vent', async () => {
    await press('p');
    await wait('[role="status"]', { textIncludes: 'Planted' });
    const found = await cli('seed', 'search', 'ANALYTICS7');
    runner.assert(found.includes('ANALYTICS7'), 'the garden holds a seed planted from the vent', found);
    await wait('ol[aria-label="Vents"] .analytics-outcome.planted', { textIncludes: 'analytics7' });
    await shot('02-planted');
    await press('u');
    await wait('[role="status"]', { textIncludes: 'Undone.' });
    await wait('ol[aria-label="Vents"] li:first-child', { textIncludes: 'ANALYTICS7' });
  });
  await runner.step('records_by_stream', async () => {
    await press(']');
    await wait('ol[aria-label="All records"] li');
    await press('j');
    await wait(inspector, { textIncludes: 'Exactly as stored' });
    await shot('03-all-records');
    await press(']');
    await wait('ol[aria-label="bus"] li');
    runner.assert(!(await text('ol[aria-label="bus"]')).includes('vent.'), 'vent facts stay out of the outbox', await text('ol[aria-label="bus"]'));
  });
  await runner.step('clear_the_local_record', async () => {
    await press('m');
    await wait('[data-testid="analytics-machine"]', { textIncludes: 'records,' });
    await wait('[data-testid="analytics-machine"]', { textIncludes: 'Tool vents' });
    await shot('04-machine');
    await client.request('dom_click', { selector: '[data-testid="analytics-machine"] .settings-row-card:last-child button.danger' });
    await client.request('dom_click', { selector: '.analytics-confirm button.danger' });
    await wait('[role="status"]', { textIncludes: 'Cleared' });
    await shot('05-cleared');
    await press('m');
  });
  await runner.step('idle_page_is_still', async () => {
    const [before, { animations }] = await Promise.all([text(page), client.request('dom_bounds', { selector: page })]);
    await delay(IDLE_MS);
    runner.assert(animations.length === 0, 'no animation runs on the open page', animations);
    runner.assert((await text(page)) === before, `the page did not change in ${IDLE_MS}ms idle`, { before });
    await shot('06-idle');
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  await shot('failure').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
