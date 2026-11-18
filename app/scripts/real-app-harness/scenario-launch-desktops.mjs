#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs, printCommonHelp, shownAgentId } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver, delay } from './platform.mjs';
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
const binary = appCliInTree(options.appPath);
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
const openCrew = async () => {
  await client.request('dispatch_shortcut', { shortcutId: 'ui.commandPalette' });
  await client.request('dom_type', { selector: '.unified-palette-input', text: '>Manage crew' });
  await client.request('dom_key', { selector: '.unified-palette-input', key: 'Enter' });
  await wait('[data-testid="crew-panel"]');
};
const capture = async (name) => {
  await captureFrontWindowScreenshot(path.join(runner.runDir, name), { client, driver });
  if (process.env.ATTN_HARNESS_RECORD === '1') await delay(1200);
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
  await runner.step('an_unfinished_launch_review_finishes_at_start', async () => {
    await client.launchFreshApp(); await client.waitForReady(30000); await observer.connect();
    await wait('.app');
    await wait('.mp-welcome-actions, .mp-launch-shell', { absent: true });
    const phase = execFileSync('sqlite3', [db, 'SELECT phase, launch_review_complete FROM profile_migration WHERE id=1;'], { encoding: 'utf8' }).trim();
    runner.assert(phase === 'complete|1', 'The daemon finished the launch review before the app connected', phase);
    members = crew().filter((member) => realCopy || member.id.endsWith('-launch-check')).slice(0, 2).map((member) => ({ item_id: member.id, start: member.launch_desktop }));
    runner.assert(members.length === 2, 'Both crew members are in the roster', crew());
    const [first, second] = members.map((member) => member.start?.desktop_id);
    runner.assert(Boolean(first) && Boolean(second) && first !== second, 'Each member launches on a desktop of its own', members);
    await client.request('dismiss_whats_new', {}).catch(() => {});
    await capture('01-finished-at-start.png');
  });
  await runner.step('settings_show_the_saved_desktop', async () => {
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
    for (const member of members) {
      const home = path.join(resources.dataDir, 'crew', member.item_id);
      writeMockAgentFixture(home, { version: 1, defaultActions: [{ type: 'reply', text: 'Launch placement verified.', state: 'idle' }], turns: [] });
      cli('crew', 'set', member.item_id, '--cwd', home, '--agent', 'claude', '--model', 'claude-haiku-4-5');
    }
    await openCrew();
    await click(`[data-testid="crew-roster-${members[0].item_id}"]`);
    await click('[data-testid="crew-tab-launch"]');
    await wait('.launch-desktop-field', { textIncludes: members[0].start.label.split(' (')[0] });
    await capture('05-launch-settings.png');
  });
  await runner.step('a_user_wake_goes_there_and_background_wake_stays_quiet', async () => {
    const memberHeading = await client.request('dom_text', { selector: '.crew-member-heading h2' });
    const userLaunched = observer.waitForMessage((message) => message.event === 'crew_updated' && message.members?.find((member) => member.id === members[0].item_id && member.binding_session), 'app crew start');
    await click('[data-testid="crew-start"]');
    const launched = await userLaunched;
    const userStart = { session_id: launched.binding_session };
    await observer.waitForSession({ id: userStart.session_id });
    await wait('dialog[aria-labelledby="crew-panel-title"][open]', { absent: true });
    const userState = await client.request('get_state');
    runner.assert(shownAgentId(userState) === userStart.session_id, 'An app start shows the member in the app', { view: userState.view, activeLeaf: userState.activeLeaf });
    await driver.pressKey('w', { command: true, shift: process.platform === 'linux' });
    await wait('.toast', { textIncludes: 'is protected' });
    const error = await client.request('dom_text', { selector: '.toast' });
    runner.assert(error.text.includes(memberHeading.text), 'Closing a crew session still explains its protection', error);
    runner.assert(!error.text.includes('Dismiss') && !error.text.includes('fades'), 'The error has no dismissal button or timer explanation', error);
    await wait('.toast-source', { absent: true });
    await client.request('dom_hover', { selector: '.toast' });
    const hovered = await client.request('dom_text', { selector: '.toast' });
    runner.assert(!hovered.text.includes('paused'), 'Hovering an error does not add pause text', hovered);
    await capture('06-protected-error.png');
    await client.request('dom_hover', { selector: '.toast', leave: true });
    await wait('.toast', { absent: true });
    await openCrew();
    const output = cli('crew', 'start', members[1].item_id, '--json');
    const arrival = JSON.parse(output.slice(output.indexOf('{')));
    runner.assert(Boolean(arrival.session_id), 'The CLI start succeeds', arrival);
    await wait(`[data-testid="crew-roster-${members[1].item_id}"] .is-in-session`);
    await wait('.toast', { absent: true });
    const state = await client.request('get_state');
    runner.assert(shownAgentId(state) === userStart.session_id, 'A background start keeps the currently shown agent', state);
    await wait('[data-testid="crew-panel"]');
    await capture('07-quiet-background-start.png');
    runner.writeJson('launch-choices.json', { members: crew().filter((member) => members.some((item) => item.item_id === member.id)), arrival });
  });
  await runner.finishSuccess({ realCopy, members: members.map((item) => item.item_id) });
} catch (error) {
  await capture('failure.png').catch(() => {});
  await runner.finishFailure(error, { realCopy }); process.exitCode = 1;
}
