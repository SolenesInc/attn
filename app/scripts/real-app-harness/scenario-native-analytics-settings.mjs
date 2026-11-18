#!/usr/bin/env node

import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const RUN = crypto.randomUUID().slice(0, 8);
const [VENTED, WITHDRAWN_MARK, TOOL_MARK] = [7, 8, 9].map((n) => `ANALYTICS${n}-${RUN}`);
const VENT = `${VENTED} attn seed tend refused a seed held by a session that ended two hours ago. Retried twice, then used --force.`;
const WITHDRAWN = `${WITHDRAWN_MARK} attn ask list printed asks from another profile.`;
const TOOL = `${TOOL_MARK} The ship-pr skill links a skill file that does not exist.`;

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('The native analytics settings scenario changes settings, so it needs a named instance');
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-ANALYTICS-SETTINGS', tier: 'tier2-local-mock-agent', prefix: 'native-analytics-settings',
  metadata: { focus: 'Native Settings → Analytics lists new vents above done ones, keeps a tool vent here, dismisses, undoes and plants a seed by key, shows records by stream with app actions, and clears the local record' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const env = instanceCliEnv(instance);
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const LIST = 'settings.analytics.list';
const flatten = (node, all = []) => {
  all.push(node);
  for (const child of node?.children ?? []) flatten(child, all);
  return all;
};
async function under(key) {
  const all = flatten(await driver.tree());
  const root = all.find((n) => n.key === key);
  return root ? flatten(root) : [];
}
async function rows() {
  let group = '';
  const listed = [];
  for (const node of await under(LIST)) {
    if (node.role === 'group' && node.name) group = node.name;
    if (node.role === 'list-box-option') listed.push({ key: node.key, group, name: node.name ?? '' });
  }
  return listed;
}
const text = async (key) => (await under(key)).map((n) => n.name ?? '').filter(Boolean).join('\n');
async function waitForToast(includes) {
  await driver.waitFor({ key: 'settings.analytics.toast' });
  const [toast] = await under('settings.analytics.toast');
  const words = `${toast?.name ?? ''} ${toast?.description ?? ''}`;
  runner.assert(words.includes(includes), `the page says ${JSON.stringify(includes)}`, toast);
}
async function rowWith(words) {
  const row = (await rows()).find((r) => r.name.includes(words));
  runner.assert(row, `a row holds ${JSON.stringify(words)}`, await rows());
  return row;
}
const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));

async function spawnAgent(label) {
  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, label.replaceAll(' ', '-'));
  fs.mkdirSync(cwd, { recursive: true });
  const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, `${label} spawned`, 60_000);
  observer.send({ cmd: 'spawn_session', id, agent: 'claude', cwd, profile_id: observer.profileId, placement: {}, label, cols: 100, rows: 30 });
  runner.assert((await spawned).success, `spawn_session starts ${label}`);
  runner.registerCleanup(`unregister_${id}`, () => observer.unregisterSession(id));
  return id;
}
function vent(session, words, about = 'attn') {
  return attn('vent', '--about', about, '--session', session, words).match(/vent (v-[a-z0-9]+) recorded/)[1];
}

try {
  await runner.step('launch_app_with_analytics', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    queueDaemonSettingRestore(observer, 'analytics.enabled');
    queueDaemonSettingRestore(observer, 'analytics.tool_vents');
    attn('settings', 'set', 'analytics.enabled', 'true');
    attn('settings', 'set', 'analytics.tool_vents', 'true');
    const tooling = await spawnAgent('write release notes');
    const venting = await spawnAgent('fix garden bell staleness');
    const withdrawing = await spawnAgent('songcheck deploy');
    vent(tooling, TOOL, 'tools');
    vent(venting, VENT);
    attn('vent', 'withdraw', '--session', withdrawing, vent(withdrawing, WITHDRAWN));
    await driver.run('ui.openSettings');
    await driver.waitFor({ key: 'settings-dialog' });
  });

  await runner.step('vents_listed_new_then_done', async () => {
    await driver.click({ key: 'settings.nav.analytics' });
    await driver.state('settings', { includes: 'analytics' });
    await driver.click({ key: 'settings.analytics.views.vents' });
    await driver.waitFor({ key: 'settings.analytics.vent.text', name: VENT });
    const listed = await rows();
    const at = (words) => listed.findIndex((r) => r.name.includes(words));
    runner.assert(listed[at(VENTED)]?.group === 'New' && listed[at(TOOL_MARK)]?.group === 'New', 'the new vents list under New', listed);
    runner.assert(listed[at(WITHDRAWN_MARK)]?.group === 'Done' && listed[at(WITHDRAWN_MARK)].name.includes('withdrawn'), 'the withdrawn vent lists under Done', listed);
    runner.assert(at(VENTED) < at(WITHDRAWN_MARK), 'the new vent lists above the withdrawn one', listed);
    await driver.waitFor({ key: 'settings.analytics.vent.destination', name: 'Kept on this machine' });
    await shot('00-vents');
  });

  await runner.step('tool_vent_stays_here', async () => {
    const tool = await rowWith(TOOL_MARK);
    runner.assert(tool.name.includes('tools'), 'the tool vent is marked tools', tool);
    await driver.click({ key: tool.key });
    await driver.waitFor({ key: 'settings.analytics.vent.text', name: TOOL });
    runner.assert((await text('settings.analytics.vent.where')).includes('tool vents never leave it'), 'the tool vent says it stays here', await text('settings.analytics.vent.where'));
    await shot('01-tool-vent');
    await driver.click({ key: (await rowWith(VENTED)).key });
    await driver.waitFor({ key: 'settings.analytics.vent.text', name: VENT });
  });

  await runner.step('dismiss_undo_with_keys', async () => {
    await driver.pressKey('d');
    await waitForToast('Dismissed “fix garden bell staleness”');
    runner.assert((await rowWith(VENTED)).group === 'Done', 'the dismissed vent moves under Done', await rows());
    await shot('02-dismissed');
    await driver.pressKey('u');
    await waitForToast('Undone.');
    runner.assert((await rowWith(VENTED)).group === 'New', 'undo brings the vent back under New', await rows());
  });

  await runner.step('plant_seed_from_vent', async () => {
    await driver.click({ key: (await rowWith(VENTED)).key });
    await driver.pressKey('p');
    await waitForToast('Planted');
    const found = attn('seed', 'search', VENTED);
    runner.assert(found.includes(VENTED), 'the garden holds a seed planted from the vent', found);
    await shot('03-planted');
    await driver.pressKey('u');
    await waitForToast('Undone.');
    runner.assert((await rowWith(VENTED)).group === 'New', 'undoing the plant leaves the vent new', await rows());
  });

  await runner.step('records_by_stream', async () => {
    await driver.pressKey(']');
    await driver.waitFor({ key: 'settings.analytics.views.all' });
    await driver.waitFor({ role: 'list-box-option' });
    await driver.pressKey('j');
    await driver.waitFor({ key: 'settings.analytics.record.raw' });
    await shot('04-all-records');
    await driver.click({ key: 'settings.analytics.views.app' });
    const app = (await rows()).map((r) => r.name);
    runner.assert(app.some((name) => name.includes('settings_section')), 'opening Analytics recorded its section on the app stream', app);
    await driver.click({ key: 'settings.analytics.views.bus' });
    await driver.waitFor({ role: 'list-box-option' });
    const bus = (await rows()).map((r) => r.name);
    runner.assert(!bus.some((name) => name.includes('vent.')), 'vent facts stay out of the outbox', bus);
  });

  await runner.step('clear_the_local_record', async () => {
    await driver.pressKey('m');
    await driver.waitFor({ key: 'settings.analytics.machine' });
    await driver.waitFor({ key: 'settings.analytics.switch.analytics.tool_vents' });
    await shot('05-machine');
    await driver.click({ key: 'settings.analytics.clear' });
    await driver.click({ key: 'settings.analytics.confirm.yes' });
    await waitForToast('Cleared');
    await shot('06-cleared');
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  await shot('failure').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
