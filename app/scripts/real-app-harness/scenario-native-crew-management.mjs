#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { crewManagementFixture, modelAwareWrapperSource, transcriptLaunches } from './crewManagementFixture.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { MOCK_AGENT_EXECUTABLE, writeMockAgentFixture } from './mockAgent.mjs';
import { stopDaemon } from './perfMeasure.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-CREW-MANAGEMENT', tier: 'tier2-local-mock-agent', prefix: 'native-crew-management',
  metadata: { focus: "Manage crew in the native app: each member's seeds open as a reader and Back returns in place, the member menu's details save and clear launch settings, the full charter saves, recovers and meets an outside edit, a letter's seed link opens, a save lost with the daemon goes once it returns, and Close then Start carries the saved settings" },
});
const resources = resolveHarnessResources(instance);
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const suffix = runner.runId.replace(/[^a-z0-9]/gi, '').toLowerCase().slice(-6);
const inSession = `alder-${suffix}`;
const resting = `keel-${suffix}`;
const history = `trellis-${suffix}`;
const home = (member) => path.join(resources.dataDir, 'crew', member);
const wakeReceipt = path.join(home(inSession), 'wake-received');
const claimReadReceipt = path.join(home(resting), 'claim-read');
const planted = new Set();
let firstSession = '';
let successor = '';
let registered = false;

const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance), timeout: 30_000 });
const json = (...args) => {
  const output = attn(...args, '--json');
  return JSON.parse(output.slice(output.search(/[[{]/)));
};
const member = (id) => json('crew', 'list').find((m) => m.id === id);
const plant = (...args) => {
  const seed = json('seed', 'plant', ...args);
  planted.add(seed.id);
  return seed;
};
const shot = (file) => driver.screenshot(path.join(runner.runDir, file));
const named = (key, name, timeoutMs = 30_000) => driver.waitFor({ key, name }, { timeoutMs });

function crewChange(id, predicate, description, timeoutMs = 30_000) {
  const wait = observer.waitForMessage((m) => {
    if (m.event !== 'crew_updated') return null;
    const found = (m.members ?? []).find((entry) => entry.id === id);
    return found && predicate(found) ? found : null;
  }, description, timeoutMs);
  wait.catch(() => {});
  return wait;
}

function fileSignal(file, description, timeoutMs = 30_000) {
  const wait = new Promise((resolve, reject) => {
    const watcher = fs.watch(path.dirname(file), (_event, name) => {
      if (name === path.basename(file) && fs.existsSync(file)) done();
    });
    const timer = setTimeout(() => { watcher.close(); reject(new Error(`timed out waiting for ${description}`)); }, timeoutMs);
    const done = () => { clearTimeout(timer); watcher.close(); resolve(); };
    if (fs.existsSync(file)) done();
  });
  wait.catch(() => {});
  return wait;
}

async function openCrewOn(id, tab) {
  if (!(await driver.find({ key: 'crew.heading' }))) await driver.click({ role: 'button', name: 'Manage crew' });
  await driver.click({ key: `crew.roster.${id}` });
  if (tab) await driver.click({ key: `crew.tab.${tab}` });
}

async function closeCrew() {
  await driver.pressKey('Escape');
  await driver.waitFor({ key: 'crew.heading' }, { gone: true });
}

function seedTileOf(seed) {
  for (const desktop of observer.desktops) {
    const walk = (node) => {
      if (!node) return null;
      if (node.tile_kind === 'seed' && node.tile_params === seed) return node;
      for (const child of node.children ?? []) { const hit = walk(child); if (hit) return hit; }
      return null;
    };
    const tile = walk(JSON.parse(desktop.tree_json || 'null'));
    if (tile) return { desktop, tile };
  }
  return null;
}

async function closeSeedTile(seed) {
  const shown = seedTileOf(seed);
  if (shown) await observer.profileCommand('desktop_remove_leaf', { desktop_id: shown.desktop.id, leaf_id: shown.tile.tile_id });
  await observer.waitFor(() => !seedTileOf(seed), `the ${seed} tile to close`);
}

async function memberDetails(id) {
  const session = member(id)?.binding_session;
  const row = session ? `queue-crew.${session}` : `queue-crew.member.${id}`;
  await driver.hover({ key: row });
  await driver.click({ key: `${row}.actions` });
  await driver.click({ key: session ? 'session.menu.member-details' : 'crew-member-menu.details' });
}

async function valueOf(key) {
  let value;
  const walk = (n) => { if (!n || value !== undefined) return; if (n.key === key) value = n.value ?? ''; for (const c of n.children ?? []) walk(c); };
  walk(await driver.tree());
  return value;
}

async function appendToCharter(text) {
  await driver.click({ key: 'crew.charter.editor' });
  await driver.pressKey('ctrl+End');
  await driver.typeInto(null, text);
}

const wrapper = path.join(runner.sessionDir, 'model-aware-mock.mjs');
fs.mkdirSync(runner.sessionDir, { recursive: true });
fs.writeFileSync(wrapper, modelAwareWrapperSource(MOCK_AGENT_EXECUTABLE), { encoding: 'utf8', mode: 0o755 });

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));
runner.registerFailureEvidence('native_tree', async () => runner.writeJson('failure-tree.json', await driver.tree()));
runner.registerCleanup('remove_member_homes', () => {
  if (fs.existsSync(home(history))) fs.chmodSync(home(history), 0o755);
  for (const id of [inSession, resting, history]) fs.rmSync(home(id), { recursive: true, force: true });
});
runner.registerCleanup('retire_crew', async () => {
  if (!registered) return;
  for (const id of [inSession, resting, history]) {
    const day = member(id)?.binding_session;
    if (day && observer.getSession(day)) {
      attn('handoff', '--session', day, '-m', 'Crew management scenario finished.');
      await observer.waitFor(() => !observer.getSession(day), `${id}'s session to end`, 30_000);
    }
    attn('crew', 'retire', id);
  }
});
runner.registerCleanup('settle_seeds', () => {
  for (const id of [...planted].reverse()) attn('seed', 'wither', id, '--force', '-m', 'Scenario complete');
});

try {
  let plot;
  let child;
  let held;
  let linked;

  await runner.step('three_members_with_real_charters_and_letters', async () => {
    attn('daemon', 'stop');
    for (const [id, fixture, letter] of [
      [inSession, crewManagementFixture.alder, '2026-09-06T04-45Z-alder.md'],
      [resting, crewManagementFixture.keel, '2026-08-19T00-11Z-keel.md'],
      [history, crewManagementFixture.trellis, '2026-09-01T21-37Z-trellis.md'],
    ]) {
      fs.mkdirSync(path.join(home(id), 'handoffs'), { recursive: true });
      fs.writeFileSync(path.join(home(id), 'CHARTER.md'), fixture.charter);
      fs.writeFileSync(path.join(home(id), 'handoffs', letter), fixture.letter);
    }
    writeMockAgentFixture(home(inSession), {
      version: 1,
      turns: [
        { includes: 'This is a new session', actions: [{ type: 'reply', text: 'CREW_PANEL_READY', state: 'idle' }] },
        { includes: '📬 You have unread items', submitHook: false, actions: [
          { type: 'attn', args: ['agent', 'inbox'] },
          { type: 'wait_for_file', path: 'continue-sleep', timeoutMs: 30_000 },
          { type: 'attn', args: ['handoff', '-m', 'CREW_PANEL_HANDOFF'] },
        ] },
      ],
    });
    writeMockAgentFixture(home(resting), {
      version: 1,
      turns: [
        { includes: 'This is a new session', actions: [{ type: 'reply', text: 'ASLEEP_MEMBER_READY', state: 'idle' }] },
        { includes: '📬 You have unread items', submitHook: false, actions: [
          { type: 'attn', args: ['agent', 'inbox'] },
          { type: 'touch', path: claimReadReceipt },
        ] },
      ],
    });
    process.env.ATTN_MOCK_AGENT_LAUNCH_RECEIPT = 'wake-received';
    await launchFreshNativeAppAndConnect(client, observer);
    for (const [key, value] of [['claude_executable', wrapper], ['codex_executable', wrapper], ['queue_mode_enabled', 'true']]) {
      queueDaemonSettingRestore(observer, key);
      observer.send({ cmd: 'set_setting', key, value });
      await observer.waitFor(() => observer.getSetting(key) === value, `${key}=${value}`);
    }
    attn('crew', 'set', inSession, '--cwd', home(inSession), '--agent', 'claude');
    registered = true;
    attn('crew', 'set', resting, '--cwd', home(resting));
    attn('crew', 'set', history, '--cwd', home(history));
    linked = plant('Crew charter history link receipt', '-m', 'Opened from a full handoff letter.').id;
    fs.writeFileSync(path.join(home(history), 'handoffs', '2026-08-31T18-05Z-trellis.md'),
      `# Linked handoff\n\nContinue from [Crew charter history link receipt](${linked}).\n`);

    const bound = crewChange(inSession, (m) => Boolean(m.binding_session), 'the first session bound');
    firstSession = json('crew', 'start', inSession).session_id;
    runner.assert((await bound).binding_session === firstSession, 'the roster binds the started session');
    await fileSignal(wakeReceipt, 'the first mock launch');
    fs.unlinkSync(wakeReceipt);

    plot = plant(`Crew verification plot ${suffix}`, '-m', 'The planted list opens this plot.', '--session', firstSession).id;
    child = plant(`Crew planted navigation ${suffix}`, '-m', 'A child of the plot.', '--part-of', plot, '--session', firstSession).id;
    json('seed', 'tend', child, '--session', firstSession);
    held = plant(`Crew resting claim ${suffix}`, '-m', 'A member claim stays between sessions.').id;
    const read = fileSignal(claimReadReceipt, 'the resting member reads its claim');
    json('seed', 'tend', held, '--for', resting);
    await read;
    const day = member(resting).binding_session;
    const between = crewChange(resting, (m) => !m.binding_session, 'the resting member between sessions again');
    attn('handoff', '-m', 'Read the Garden claim update', '--session', day);
    await between;
  });

  await runner.step('seed_lists_open_readers_and_back_returns_in_place', async () => {
    await openCrewOn(resting, 'seeds');
    await driver.waitFor({ key: `crew.seed.${held}` });
    await driver.click({ key: `crew.seed.${held}` });
    await driver.waitFor({ key: 'crew.heading' }, { gone: true });
    const shown = await observer.waitFor(() => seedTileOf(held), 'the standalone reader opens', 20_000);
    await driver.waitFor({ key: `seed-tile.${shown.tile.tile_id}.head.title`, name: `Crew resting claim ${suffix}` });
    await shot('01-resting-seed-reader.png');
    await driver.click({ key: `tile.${shown.tile.tile_id}.crew-back` });
    await driver.waitFor({ key: `crew.seed.${held}` });
    runner.assert((await driver.find({ key: 'crew.heading' }))?.name?.toLowerCase().startsWith('keel'), 'Back returns to the resting member');
    await closeCrew();
    await closeSeedTile(held);

    await openCrewOn(inSession, 'seeds');
    await driver.waitFor({ key: `crew.seed.${child}` });
    await driver.click({ key: 'crew.seeds.planted' });
    await driver.waitFor({ key: `crew.seed.${plot}` });
    const live = plant(`Crew live planting update ${suffix}`, '--session', firstSession).id;
    await driver.waitFor({ key: `crew.seed.${live}` });
    await driver.click({ key: 'crew.seeds.find' });
    await driver.typeInto(null, `Crew verification plot ${suffix}`);
    await driver.waitFor({ key: `crew.seed.${live}` }, { gone: true });
    await shot('02-crew-seeds.png');
    await driver.click({ key: `crew.seed.${plot}` });
    const beside = await observer.waitFor(() => seedTileOf(plot), 'the plot opens beside the session', 20_000);
    await driver.waitFor({ key: `seed-tile.${beside.tile.tile_id}.head.title`, name: `Crew verification plot ${suffix}` });
    await closeSeedTile(plot);
  });

  await runner.step('the_member_menu_opens_details_and_saves_then_clears_launch_settings', async () => {
    if (await driver.find({ key: 'crew.heading' })) await closeCrew();
    await memberDetails(resting);
    await named('crew.presence', 'Between sessions');
    await driver.waitFor({ key: 'crew.start' });
    const claude = crewChange(resting, (m) => m.agent === 'claude', 'the resting harness saved');
    await driver.click({ key: `settings.route.crew.${resting}` });
    await driver.click({ key: 'settings.route-picker.harness.claude' });
    await driver.click({ key: 'settings.route-picker.model.0' });
    await claude;
    if (await driver.find({ key: 'settings.route-picker' })) await driver.pressKey('Escape');
    const cleared = crewChange(resting, (m) => !m.agent, 'the crew default restored');
    await driver.click({ key: `settings.route.crew.${resting}` });
    await driver.click({ key: 'settings.route-picker.harness.none' });
    await cleared;
    await named('crew.launch.status', 'Saved');
    await shot('03-resting-defaults.png');
    await closeCrew();
  });

  await runner.step('park_and_tend_again_follow_in_the_seed_list', async () => {
    await openCrewOn(resting, 'seeds');
    json('seed', 'park', held, '--force');
    await driver.waitFor({ key: `crew.seed.${held}` }, { gone: true });
    json('seed', 'tend', held, '--for', resting);
    await driver.waitFor({ key: `crew.seed.${held}` });
    await closeCrew();
  });

  await runner.step('the_full_charter_saves_recovers_and_meets_an_outside_edit', async () => {
    const charter = path.join(home(history), 'CHARTER.md');
    const original = crewManagementFixture.trellis.charter;
    await openCrewOn(history, 'charter');
    await driver.waitFor({ key: 'crew.charter.editor' });
    const shown = await valueOf('crew.charter.editor');
    runner.assert(shown === original, 'the editor holds the whole charter', { length: shown?.length, expected: original.length });
    await shot('04-charter.png');

    fs.chmodSync(home(history), 0o555);
    await appendToCharter('\nretained after a failed write');
    await named('crew.charter.status', 'Not saved');
    runner.assert(fs.readFileSync(charter, 'utf8') === original, 'a failed save leaves the file as it was');
    fs.chmodSync(home(history), 0o755);
    await driver.click({ key: 'crew.charter.retry' });
    await named('crew.charter.status', 'Saved');
    const retried = fs.readFileSync(charter, 'utf8');
    runner.assert(retried.startsWith(original) && retried.includes('retained after a failed write'), 'Retry writes the kept edit');

    fs.writeFileSync(charter, `${retried}\noutside edit`);
    await appendToCharter('\nthe panel keeps this');
    await driver.waitFor({ key: 'crew.charter.keep-mine' }, { timeoutMs: 30_000 });
    runner.assert(fs.readFileSync(charter, 'utf8').endsWith('outside edit'), 'the outside edit is not overwritten');
    await driver.click({ key: 'crew.charter.keep-mine' });
    await named('crew.charter.status', 'Saved');
    runner.assert(fs.readFileSync(charter, 'utf8').endsWith('the panel keeps this'), 'Keep mine writes over the outside edit');

    await appendToCharter('\nflushed on leaving');
    await driver.click({ key: 'crew.tab.handoffs' });
    await driver.waitFor({ key: 'crew.handoff.letter' });
    runner.assert(fs.readFileSync(charter, 'utf8').endsWith('flushed on leaving'), 'leaving the tab saves the edit first');
  });

  await runner.step('the_letters_read_in_full_and_a_seed_link_opens_and_returns', async () => {
    await driver.waitFor({ key: 'crew.handoff.letter' });
    await shot('05-handoffs.png');
    await driver.click({ key: 'crew.handoff.1' });
    await driver.click({ role: 'link', name: 'Crew charter history link receipt' });
    const shown = await observer.waitFor(() => seedTileOf(linked), 'the linked seed opens', 20_000);
    await driver.waitFor({ key: `seed-tile.${shown.tile.tile_id}.head.title`, name: 'Crew charter history link receipt' });
    await shot('06-linked-seed.png');
    await driver.click({ key: `tile.${shown.tile.tile_id}.crew-back` });
    await driver.waitFor({ role: 'link', name: 'Crew charter history link receipt' });
    runner.assert((await driver.find({ key: 'crew.heading' }))?.name?.toLowerCase().startsWith('trellis'), 'Back returns to the same member');
    await closeCrew();
    await closeSeedTile(linked);
  });

  await runner.step('a_member_in_session_saves_its_next_sessions_model_and_effort', async () => {
    await memberDetails(inSession);
    await named('crew.presence', 'In session');
    const running = await driver.waitFor({ key: 'crew.running.route' });
    runner.assert((running?.name ?? '').includes('Claude'), 'running now names the harness the session runs', running);
    const full = crewChange(inSession, (m) => m.agent === 'claude' && m.model === 'crew-claude' && m.effort === 'high', 'the next session settings');
    await driver.click({ key: `settings.route.crew.${inSession}` });
    await driver.click({ key: 'settings.route-picker.harness.claude' });
    await driver.waitFor({ key: 'settings.route-picker.models' });
    const tree = await driver.tree();
    const options = [];
    const walk = (n, inside) => { if (!n) return; const here = inside || n.key === 'settings.route-picker.models'; if (here && n.role === 'list-box-option') options.push(n); for (const c of n.children ?? []) walk(c, here); };
    walk(tree, false);
    const crewClaude = options.find((n) => (n.name ?? '').startsWith('Crew Claude'));
    runner.assert(Boolean(crewClaude), 'the harness offers the discovered model', options.map((n) => n.name));
    await driver.click({ key: crewClaude.key });
    await driver.click({ role: 'button', name: 'high' });
    await full;
    if (await driver.find({ key: 'settings.route-picker' })) await driver.pressKey('Escape');
    await named('crew.launch.status', 'Saved');
    await shot('07-next-session.png');
  });

  await runner.step('a_save_lost_with_the_daemon_goes_once_it_returns', async () => {
    const gone = observer.waitForDisconnect();
    runner.assert(Number.isInteger(await stopDaemon(instance)), 'the scenario stopped its own daemon');
    await gone;
    await driver.click({ key: `settings.route.crew.${inSession}` });
    await driver.click({ role: 'button', name: 'low' });
    if (await driver.find({ key: 'settings.route-picker' })) await driver.pressKey('Escape');
    await named('crew.launch.status', 'Not saved');
    await shot('08-not-saved.png');
    attn('daemon', 'ensure');
    await observer.connect();
    await named('crew.launch.status', 'Saved', 45_000);
    runner.assert(member(inSession)?.effort === 'low', 'the write went once the daemon returned');
  });

  await runner.step('close_files_a_letter_and_start_carries_the_saved_settings', async () => {
    const closed = crewChange(inSession, (m) => !m.binding_session, 'the member closing its session', 45_000);
    await driver.click({ key: 'crew.close' });
    fs.writeFileSync(path.join(home(inSession), 'continue-sleep'), 'continue\n');
    await closed;
    await named('crew.presence', 'Between sessions');
    await driver.click({ key: 'crew.tab.handoffs' });
    await driver.waitFor({ key: 'crew.handoff.letter' });
    const letter = (await driver.tree());
    runner.assert(JSON.stringify(letter).includes('CREW_PANEL_HANDOFF'), 'the new letter leads the history');
    await driver.click({ key: 'crew.tab.launch' });
    const woken = crewChange(inSession, (m) => m.binding_session && m.binding_session !== firstSession, 'the next session', 45_000);
    const launched = fileSignal(wakeReceipt, 'the next mock launch', 45_000);
    await driver.click({ key: 'crew.start' });
    const [next] = await Promise.all([woken, launched]);
    successor = next.binding_session;
    await driver.waitFor({ key: 'crew.heading' }, { gone: true });
    const terminal = await observer.waitFor(() => observer.terminalOf(successor), 'the next session has a terminal', 30_000);
    const launch = transcriptLaunches(home(inSession)).find((entry) => entry.id === terminal);
    const argv = launch?.launch?.argv ?? [];
    runner.assert(argv[argv.indexOf('--model') + 1] === 'crew-claude', 'the saved model reaches the launch', { argv });
    runner.assert(argv[argv.indexOf('--effort') + 1] === 'low', 'the effort saved across the reconnect reaches the launch', { argv });
    runner.writeJson('next-launch.json', { firstSession, successor, argv });
    await shot('09-next-session.png');
  });

  await runner.finishSuccess({ inSession, resting, history, firstSession, successor });
} catch (error) {
  await runner.finishFailure(error, { inSession, resting, history, firstSession, successor });
  process.exitCode = 1;
}
