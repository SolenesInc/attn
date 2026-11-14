#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const SESSIONS_FILTERS_SETTING = 'sessions.filters';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
assertFreshWorldTargetSafe({ instance, appPath: options.appPath });
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SESSIONS-SURFACE', tier: 'tier2-local-mock-agent', prefix: 'native-sessions-surface',
  metadata: { agent: 'claude', instance, focus: 'The native Sessions list shows live and closed sessions, narrows them by repository, profile, scope and dates, updates a row in place when its session closes, reopens on the filters it was left with, and keeps a refused reopen in the ledger, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const cli = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const git = (cwd, ...args) => execFileSync('git', args, { cwd, encoding: 'utf8', env: instanceCliEnv() }).trim();
const sessions = {};
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('unregister_sessions', async () => {
  for (const id of Object.values(sessions)) if (observer.getSession(id)) await observer.unregisterSession(id);
});

function buildRepositories(root) {
  const made = {};
  for (const name of ['ledger-repo', 'other-repo']) {
    const dir = path.join(root, name);
    fs.mkdirSync(dir, { recursive: true });
    git(dir, 'init', '-q', '-b', 'main');
    git(dir, 'config', 'user.email', 'harness@attn.local');
    git(dir, 'config', 'user.name', 'attn harness');
    fs.writeFileSync(path.join(dir, 'README.md'), `# ${name}\n`);
    git(dir, 'add', 'README.md');
    git(dir, 'commit', '-q', '-m', 'fixture');
    writeMockAgentFixture(dir, { name: 'sessions surface mock', turns: [] });
    made[name] = dir;
  }
  return { repo: made['ledger-repo'], other: made['other-repo'] };
}

function nodesWhere(node, test, found = []) {
  if (!node) return found;
  if (test(node)) found.push(node);
  for (const child of node.children ?? []) nodesWhere(child, test, found);
  return found;
}
const ROW = /^ledger\.row\.([^.]+)$/;
const listed = async () => nodesWhere(await driver.tree(), (n) => ROW.test(n.key ?? '') && n.role === 'list-box-option').map((n) => ROW.exec(n.key)[1]);
const valueOf = async (key) => nodesWhere(await driver.tree(), (n) => n.key === key)[0]?.value;
const nameOf = async (key) => (await driver.waitFor({ key })).name ?? '';
const row = (id) => ({ key: `ledger.row.${id}` });
const verb = (id) => `ledger.row.${id}.verb`;

async function query(text) {
  await driver.click({ key: 'ledger.query.field' });
  await driver.typeInto(null, text);
}
async function clearQuery() {
  await driver.click({ key: 'ledger.query.field' });
  await driver.pressKey('Escape');
}
async function scope(name) {
  await driver.click({ key: `ledger.scope.${name}` });
  runner.assert(await valueOf(`ledger.scope.${name}`) === 'pressed', `the ${name} scope is chosen`);
}
async function openTheList() {
  await driver.run('sessions.open');
  await driver.waitFor({ key: 'ledger.panel' });
}
async function closeTheList() {
  await driver.click({ key: 'ledger.close' });
  await driver.waitFor({ key: 'ledger.panel' }, { gone: true });
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  const { repo, other } = await runner.step('build_repositories', () => buildRepositories(runner.sessionDir));

  await runner.step('launch_app', async () => {
    await client.quitApp();
    cli('daemon', 'stop');
    await launchFreshNativeAppAndConnect(client, observer);
    queueDaemonSettingRestore(observer, SESSIONS_FILTERS_SETTING);
    observer.send({ cmd: 'set_setting', key: SESSIONS_FILTERS_SETTING, value: '' });
    await observer.waitFor(() => (observer.getSetting(SESSIONS_FILTERS_SETTING) ?? '') === '', 'the remembered filters cleared');
  });

  await runner.step('create_sessions_in_two_repositories', async () => {
    for (const [name, cwd] of [['one', repo], ['two', repo], ['elsewhere', other]]) {
      const id = crypto.randomUUID();
      const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `${name} spawned`, 60_000);
      observer.send({ cmd: 'spawn_session', id, agent: 'claude', cwd, profile_id: observer.profileId, placement: {}, label: `${name}-${runner.runId}`.slice(0, 40), cols: 100, rows: 30 });
      runner.assert((await spawned).success, `spawn_session starts ${name}`);
      sessions[name] = id;
    }
    const registered = await observer.waitFor(() => {
      const session = observer.getSession(sessions.one);
      return session?.repository ? session : null;
    }, 'the daemon records the repository the session runs in', 20_000);
    runner.assert(registered.repository === repo, 'a session in a plain checkout names that checkout as its repository', { repository: registered.repository, repo });
  });

  await runner.step('the_shortcut_opens_every_session', async () => {
    await openTheList();
    for (const id of Object.values(sessions)) await driver.waitFor(row(id));
    runner.assert(await valueOf('ledger.scope.all') === 'pressed', 'the list opens on live and closed together');
    await driver.screenshot(path.join(runner.runDir, 'native-sessions-opened.png'));
  });

  await runner.step('the_repository_filter_narrows_the_list', async () => {
    await query(`repo-path:${encodeURIComponent(repo)}`);
    await driver.waitFor(row(sessions.elsewhere), { gone: true });
    await driver.waitFor(row(sessions.one));
    await driver.waitFor(row(sessions.two));
    const shown = await listed();
    runner.assert(shown.includes(sessions.one) && shown.includes(sessions.two), 'both sessions of the repository stay', { shown });
    await clearQuery();
    await driver.waitFor(row(sessions.elsewhere));
  });

  await runner.step('the_profile_filter_names_the_profile_the_sessions_run_in', async () => {
    await query('profile:default');
    await driver.waitFor({ key: 'ledger.chip.0', name: 'profile:default' });
    runner.assert(await valueOf('ledger.chip.0') !== 'unresolved', 'the Default profile resolves');
    for (const id of Object.values(sessions)) await driver.waitFor(row(id));
    await clearQuery();
    await query('profile:no-such-profile');
    await driver.waitFor({ key: 'ledger.chip.0', name: 'profile:no-such-profile' });
    runner.assert(await valueOf('ledger.chip.0') === 'unresolved', 'an unknown profile is marked unresolved');
    for (const id of Object.values(sessions)) await driver.waitFor(row(id));
    await clearQuery();
  });

  await runner.step('a_close_updates_the_row_in_place', async () => {
    await driver.waitFor({ key: verb(sessions.two), name: 'Focus' });
    const before = (await listed()).length;
    const desktop = observer.desktopOf(sessions.two);
    const pane = desktop.panes.find((p) => p.session_id === sessions.two);
    observer.send({ cmd: 'desktop_close_tile', request_id: crypto.randomUUID(), desktop_id: desktop.id, tile_id: pane.pane_id });
    await driver.waitFor({ key: verb(sessions.two), name: 'Reopen' }, { timeoutMs: 20_000 });
    const meta = await nameOf(`ledger.row.${sessions.two}.meta`);
    runner.assert(meta.includes('closed by you'), 'the row names who closed it', { meta });
    runner.assert((await listed()).length === before, 'the close replaces the row rather than adding one', { before });
  });

  await runner.step('an_agent_close_names_the_session_that_closed_it', async () => {
    cli('agent', 'close', sessions.elsewhere, '-m', 'the run finished', '--source-session', sessions.elsewhere);
    await driver.waitFor({ key: verb(sessions.elsewhere), name: 'Reopen' }, { timeoutMs: 20_000 });
    const meta = await nameOf(`ledger.row.${sessions.elsewhere}.meta`);
    runner.assert(meta.includes(`closed by ${`elsewhere-${runner.runId}`.slice(0, 40)}`), 'the row names the session that closed it', { meta });
    runner.assert(!meta.includes(`closed by ${sessions.elsewhere}`), 'the row does not fall back to the raw session id', { meta });
    runner.assert(meta.includes('the run finished'), 'the row carries the reason', { meta });
    runner.assert(await nameOf(verb(sessions.one)) === 'Focus', 'a live row never offers Reopen');
  });

  await runner.step('the_live_scope_drops_the_closed_row', async () => {
    await scope('live');
    await driver.waitFor(row(sessions.two), { gone: true });
    await scope('closed');
    await driver.waitFor(row(sessions.two));
    await driver.waitFor(row(sessions.one), { gone: true });
  });

  await runner.step('a_date_preset_and_a_custom_range_both_filter', async () => {
    await scope('all');
    await query('today');
    for (const id of Object.values(sessions)) await driver.waitFor(row(id));
    await clearQuery();
    const wayBack = new Date(Date.now() - 60 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
    const alsoBack = new Date(Date.now() - 30 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
    await query(`from:${wayBack} to:${alsoBack}`);
    const empty = await driver.waitFor({ key: 'ledger.empty' });
    runner.assert((await listed()).length === 0 && Boolean(empty.name), 'an empty list says why it is empty', empty);
    await clearQuery();
    for (const id of Object.values(sessions)) await driver.waitFor(row(id));
  });

  await runner.step('the_list_reopens_where_it_was_left', async () => {
    await scope('closed');
    await query(`7d repo-path:${encodeURIComponent(repo)}`);
    await driver.waitFor(row(sessions.elsewhere), { gone: true });
    await driver.waitFor(row(sessions.two));
    const remembered = JSON.stringify({ scope: 'closed', range: '7d', customFrom: '', customTo: '', profileId: '', repository: repo });
    await observer.waitFor(() => observer.getSetting(SESSIONS_FILTERS_SETTING) === remembered, 'the daemon keeps the filters');
    await closeTheList();
    const assertRemembered = async (where) => {
      await driver.waitFor(row(sessions.two));
      runner.assert(await valueOf('ledger.scope.closed') === 'pressed', `${where} holds the Closed scope`);
      const field = await nameOf('ledger.query.field');
      const shown = await listed();
      runner.assert(!shown.includes(sessions.one) && !shown.includes(sessions.elsewhere), `${where} is already narrowed, with no live row and no other repository`, { shown, field });
    };
    await openTheList();
    await assertRemembered('a reopened list');
    await closeTheList();
    await client.quitApp();
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await openTheList();
    await assertRemembered('the list after an app restart');
    await clearQuery();
    await scope('all');
  });

  await runner.step('the_cli_and_the_list_agree', async () => {
    const shown = cli('session', 'list', '--all', '--repository', repo);
    runner.assert(shown.includes(sessions.one) && shown.includes(sessions.two), 'the CLI repository filter answers the same rows', { shown });
    runner.assert(!shown.includes(sessions.elsewhere), 'the CLI repository filter leaves out the other repository', { shown });
  });

  await runner.step('a_missing_directory_refuses_reopen_in_the_ledger', async () => {
    cli('agent', 'close', sessions.one, '-m', 'moving the checkout', '--source-session', sessions.one);
    await driver.waitFor({ key: verb(sessions.one), name: 'Reopen' }, { timeoutMs: 20_000 });
    fs.renameSync(repo, `${repo}-moved`);
    await driver.click({ key: verb(sessions.two) });
    const note = await driver.waitFor({ key: `ledger.row.${sessions.two}.note` }, { timeoutMs: 20_000 });
    runner.assert(!observer.getSession(sessions.two), 'the session stays closed', note);
    const inspector = nodesWhere(await driver.tree(), (n) => n.key === 'ledger.inspector')[0];
    const said = inspector ? nodesWhere(inspector, (n) => typeof n.name === 'string').map((n) => n.name) : [];
    runner.assert(said.includes('directory is gone'), 'the inspector says the directory is why', { said });
    const verbs = nodesWhere(await driver.tree(), (n) => (n.key ?? '').startsWith(`ledger.row.${sessions.two}.`) && n.role === 'button').map((n) => n.name);
    runner.assert(!verbs.some((name) => name?.startsWith('Start fresh')), 'the session offers no fresh conversation', { verbs });
    const window = await driver.state('window');
    runner.assert(window.layer === 'Ledger', 'the refusal stays in the ledger', window);
    await driver.screenshot(path.join(runner.runDir, 'native-sessions-refused.png'));
    await closeTheList();
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ sessions, repo, other, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error, { sessions });
  process.exitCode = 1;
}
