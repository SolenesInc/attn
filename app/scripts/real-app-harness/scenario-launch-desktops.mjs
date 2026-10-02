#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs, printCommonHelp, shownAgentId } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver, delay } from './platform.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';

const options = parseCommonArgs(process.argv.slice(2));
if (options.help) { printCommonHelp('scripts/real-app-harness/scenario-launch-desktops.mjs'); process.exit(0); }
const instance = currentHarnessInstance();
if (!instance) throw new Error('Launch desktop verification requires a named instance');
const realCopy = process.env.ATTN_LAUNCH_REAL_COPY === '1';
const runner = createScenarioRunner(options, { scenarioId: 'LAUNCH-DESKTOPS', tier: 'local', prefix: 'launch-desktops', allowRealAgents: false, metadata: { realCopy } });
const resources = resolveHarnessResources(instance);
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
const binary = appDaemonInTree(options.appPath);
const cliEnv = { ...instanceCliEnv(instance), ATTN_SESSION_ID: '' };
const cli = (...args) => execFileSync(binary, args, { env: cliEnv, encoding: 'utf8' });
const crew = () => { const output = cli('crew', 'list', '--json'); return JSON.parse(output.slice(output.indexOf('['))); };
const db = path.join(resources.dataDir, 'attn.db');
const backup = path.join(runner.runDir, 'before.db');
const ownedHomes = [];
let members = [];
let saved = false;
const wait = (selector, conditions = {}) => client.request('dom_wait', { selector, timeoutMs: 30000, ...conditions });
const click = (selector) => client.request('dom_click', { selector });
const capture = async (name) => {
  await captureFrontWindowScreenshot(path.join(runner.runDir, name), { client, driver });
  if (process.env.ATTN_HARNESS_RECORD === '1') await delay(1200);
};
const request = (cmd, fields, event) => {
  const requestId = `${cmd}-${crypto.randomUUID()}`;
  const result = observer.waitForMessage((message) => message.event === event && message.request_id === requestId && message, cmd);
  observer.send({ cmd, request_id: requestId, ...fields });
  return result;
};
runner.registerCleanup('restore_fixture', async () => {
  await client.quitApp().catch(() => {});
  cli('daemon', 'stop');
  if (!saved) return;
  for (const suffix of ['', '-wal', '-shm']) fs.rmSync(`${db}${suffix}`, { force: true });
  fs.copyFileSync(backup, db);
  for (const home of ownedHomes) fs.rmSync(home, { recursive: true, force: true });
});
runner.registerCleanup('observer', () => observer.close());

try {
  await runner.step('prepare_isolated_fixture', async () => {
    await client.quitApp().catch(() => {});
    if (!realCopy) cli('daemon', 'ensure');
    cli('daemon', 'stop');
    execFileSync('sqlite3', [db, `.backup ${backup}`]);
    saved = true;
    if (!realCopy) {
      for (const id of ['alder-launch-check', 'keel-launch-check']) {
        const home = path.join(resources.dataDir, 'crew', id);
        if (fs.existsSync(home)) throw new Error(`Fixture already exists: ${home}`);
        fs.mkdirSync(home, { recursive: true }); ownedHomes.push(home);
        fs.writeFileSync(path.join(home, 'CHARTER.md'), `# ${id}\n\nSynthetic launch-desktop verification member.\n`);
      }
      cli('daemon', 'ensure');
      for (const home of ownedHomes) cli('crew', 'set', path.basename(home), '--cwd', home, '--agent', 'claude', '--model', 'claude-haiku-4-5');
      cli('daemon', 'stop');
      execFileSync('sqlite3', [db, "UPDATE profile_migration SET phase='launch_required', launch_review_complete=0, revision=revision+1 WHERE id=1;"]);
    }
  });
  await runner.step('launch_review_is_mandatory', async () => {
    await client.launchFreshApp(); await client.waitForReady(30000); await observer.connect();
    await wait('.mp-welcome-actions, .mp-launch-shell');
    const initial = await client.request('migration_get_state');
    if (initial.migrationPhase === 'placement_required') {
      await click('.mp-welcome-actions .mp-button.primary');
      await wait('.mp-bottom');
      await capture('00-placement-counts.png');
      runner.writeJson('placement-counts.json', await client.request('dom_text', { selector: '.mp-dest-panel' }));
      await click('.mp-bottom-right .mp-button.quiet');
      await wait('.mp-bottom .mp-button.primary:not(:disabled)');
      await click('.mp-bottom .mp-button.primary');
    }
    await wait('.mp-launch-shell');
    const state = await client.request('migration_get_state');
    members = state.migration.launch_items.filter((item) => item.kind === 'crew' && (realCopy || item.item_id.endsWith('-launch-check'))).slice(0, 2);
    runner.assert(members.length === 2, 'The review shows both crew members', state);
    await wait('.app', { absent: true });
    await capture('01-launch-review.png');
    await click('.mp-crew-help button');
    await capture('02-where-to-change.png');
    await click('.mp-crew-help button');
  });
  await runner.step('name_and_explicitly_join_one_pending_desktop', async () => {
    const [owner, joiner] = members;
    await click(`.mp-launch-row[data-launch-item="${owner.item_id}"] .mp-launch-choice`);
    await wait('dialog input');
    await client.request('dom_type', { selector: 'dialog input', text: 'Launch review' });
    await click('dialog .primary');
    await wait(`select[aria-label="New desktops for ${joiner.name}"]`);
    const state = await client.request('migration_get_state');
    const chosen = state.migration.launch_items.find((item) => item.item_id === owner.item_id);
    await client.request('dom_select', { selector: `select[aria-label="New desktops for ${joiner.name}"]`, value: chosen.setting.destination_id });
    await wait(`.mp-launch-row[data-launch-item="${joiner.item_id}"] .mp-launch-result`, { textIncludes: 'Launch review' });
    await wait('.mp-launch-footer .primary:not(:disabled)');
    await capture('03-shared-pending.png');
    await click('.mp-launch-footer .primary');
    await wait('.mp-done');
    await capture('04-finished.png');
    await click('.mp-done .primary');
    await wait('.app');
    await client.request('dismiss_whats_new', {}).catch(() => {});
  });
  await runner.step('settings_show_the_saved_destination', async () => {
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
    for (const member of members) {
      const home = path.join(resources.dataDir, 'crew', member.item_id);
      writeMockAgentFixture(home, { version: 1, defaultActions: [{ type: 'reply', text: 'Launch placement verified.', state: 'idle' }], turns: [] });
      cli('crew', 'set', member.item_id, '--cwd', home, '--agent', 'claude', '--model', 'claude-haiku-4-5');
    }
    await click('[data-testid="manage-crew"]');
    await click(`[data-testid="crew-roster-${members[0].item_id}"]`);
    await click('[data-testid="crew-tab-launch"]');
    await wait('.launch-desktop-field', { textIncludes: 'Launch review' });
    await capture('05-launch-settings.png');
  });
  await runner.step('a_user_wake_goes_there_and_background_wake_is_an_actionable_row', async () => {
    const output = cli('crew', 'wake', members[0].item_id, '--json');
    const userWake = JSON.parse(output.slice(output.indexOf('{')));
    await observer.waitForSession({ id: userWake.session_id });
    await wait('dialog[aria-labelledby="crew-panel-title"][open]', { absent: true });
    const userState = await client.request('get_state');
    runner.assert(shownAgentId(userState) === userWake.session_id, 'A shell wake shows the member in the app', { view: userState.view, activeLeaf: userState.activeLeaf });
    await click('[data-testid="manage-crew"]');
    const arrival = await request('crew_wake', { member: members[1].item_id, source_session_id: userWake.session_id }, 'crew_wake_result');
    runner.assert(arrival.success, 'The agent wake succeeds', arrival);
    await wait('.toast-row button', { textIncludes: members[1].name });
    await wait('.toast:popover-open');
    runner.writeJson('toast-viewport-bounds.json', await client.request('dom_bounds', { selector: '.toast' }));
    await client.request('dom_focus', { selector: '.toast-row button' });
    await capture('06-background-arrival.png');
    await driver.pressEnter();
    await wait('.toast-row', { textIncludes: '✓ Done' });
    const state = await client.request('get_state');
    runner.assert(shownAgentId(state) === arrival.session_id, 'The toast action selects the arriving agent', state);
    runner.writeJson('toast-after-activation.json', { toast: await client.request('dom_text', { selector: '.toast' }), state });
    await capture('07-click-to-go.png');
    runner.writeJson('launch-choices.json', { members: crew().filter((member) => members.some((item) => item.item_id === member.id)), arrival });
  });
  await runner.finishSuccess({ realCopy, members: members.map((item) => item.item_id) });
} catch (error) {
  await capture('failure.png').catch(() => {});
  await runner.finishFailure(error, { realCopy }); process.exitCode = 1;
}
