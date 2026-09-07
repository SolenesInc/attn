#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, profileCliEnv, resolveHarnessResources } from './harnessProfile.mjs';
import { MOCK_AGENT_EXECUTABLE, writeMockAgentFixture } from './mockAgent.mjs';
import { crewManagementFixture } from './crewManagementFixture.mjs';
import { appDaemonInTree, createWindowDriver, delay } from './platform.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import {
  captureWebKitPids,
  readAppFootprint,
  readLiveDaemonPid,
  readProcessTable,
  snapshot,
  stopDaemon,
} from './perfMeasure.mjs';

process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';
const options = parseCommonArgs(process.argv.slice(2));
const profile = currentHarnessProfile();
if (!profile) throw new Error('Crew management verification requires a named profile');
const runner = createScenarioRunner(options, {
  scenarioId: 'CrewManagement',
  tier: 'local',
  prefix: 'crew-management',
  allowRealAgents: false,
});
const resources = resolveHarnessResources(profile);
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath });
const memberSuffix = runner.runId.replace(/[^a-z0-9]/gi, '').toLowerCase().slice(-6);
const awake = `alder-${memberSuffix}`;
const asleep = `keel-${memberSuffix}`;
const history = `trellis-${memberSuffix}`;
const awakeHome = path.join(resources.dataDir, 'crew', awake);
const asleepHome = path.join(resources.dataDir, 'crew', asleep);
const historyHome = path.join(resources.dataDir, 'crew', history);
const wakeReceipt = path.join(awakeHome, 'wake-received');
let firstSession = '';
let successor = '';
let linkedSeed = '';

const runAttn = (args) => execFileSync(appDaemonInTree(options.appPath), args, {
  encoding: 'utf8',
  env: profileCliEnv(profile),
  timeout: 30_000,
});
const json = (args) => {
  const output = runAttn(args);
  return JSON.parse(output.slice(output.indexOf(args[0] === 'crew' && args[1] === 'list' ? '[' : '{')));
};
const crewMember = (id) => json(['crew', 'list', '--json']).find((member) => member.id === id);
const click = (selector) => client.request('dom_click', { selector });
const select = (selector, value) => client.request('dom_select', { selector, value });
const type = (selector, text) => client.request('dom_type', { selector, text });
const panelText = async () => (await client.request('dom_text', { selector: '[data-testid="crew-panel"]' })).text;
const hold = () => process.env.ATTN_HARNESS_RECORD === '1' ? delay(1200) : Promise.resolve();
const waitForDom = (selector, condition = {}, timeoutMs = 30_000) => client.request(
  'dom_wait',
  { selector, timeoutMs, ...condition },
  { timeoutMs: timeoutMs + 1_000 },
);
const waitForCrew = (member, predicate, description, timeoutMs = 30_000) => observer.waitForMessage(
  (message) => {
    if (message.event !== 'crew_updated') return null;
    const current = (message.members || []).find((entry) => entry.id === member);
    return current && predicate(current) ? current : null;
  },
  description,
  timeoutMs,
);

function waitForFileSignal(file, description, timeoutMs = 30_000) {
  if (fs.existsSync(file)) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const watcher = fs.watch(path.dirname(file), (_event, name) => {
      if (name !== path.basename(file) || !fs.existsSync(file)) return;
      clearTimeout(timer);
      watcher.close();
      resolve();
    });
    const timer = setTimeout(() => {
      watcher.close();
      reject(new Error(`timed out waiting for ${description}: ${file}`));
    }, timeoutMs);
  });
}

async function screenshot(name) {
  await driver.activateApp();
  await captureFrontWindowScreenshot(path.join(runner.runDir, name), { client, driver });
  await hold();
}

async function sampleIdle(webkitBaseline) {
  const appPid = client.readManifest().pid;
  const daemonPid = readLiveDaemonPid(profile);
  const before = await snapshot(appPid, daemonPid, webkitBaseline);
  const pids = new Set(Object.values(before.byClass).flatMap((group) => group.pids.map((entry) => entry.pid)));
  const samples = [];
  for (let index = 0; index < 10; index += 1) {
    await delay(1000);
    samples.push((await readProcessTable()).filter((process) => pids.has(process.pid)));
  }
  const after = await snapshot(appPid, daemonPid, webkitBaseline);
  return { before, after, samples, footprint: await readAppFootprint(after) };
}

async function pressEscapeAndWaitFor(testId) {
  await driver.activateApp();
  await driver.pressKeyCode(53);
  return waitForDom(`[data-testid="${testId}"]`, { focused: true });
}

function modelAwareWrapperSource(mockPath) {
  return `#!/usr/bin/env node
import { spawn } from 'node:child_process';
import readline from 'node:readline';
const args = process.argv.slice(2);
if (args[0] === 'app-server') {
  const input = readline.createInterface({ input: process.stdin });
  input.on('line', line => {
    const request = JSON.parse(line);
    if (request.id === 1) console.log(JSON.stringify({ id: 1, result: {} }));
    if (request.method === 'model/list') console.log(JSON.stringify({ id: request.id, result: { data: [{ id: 'crew-codex', model: 'crew-codex', displayName: 'Crew Codex', supportedReasoningEfforts: [{ reasoningEffort: 'low' }, { reasoningEffort: 'high' }] }], nextCursor: null } }));
  });
} else if (args.includes('--print')) {
  process.stdin.once('data', () => {
    console.log(JSON.stringify({ type: 'control_response', response: { subtype: 'success', request_id: 'attn-model-discovery', response: { models: [{ value: 'crew-claude', displayName: 'Crew Claude', supportsEffort: true, supportedEffortLevels: ['low', 'high'] }] } } }));
  });
} else {
  const child = spawn(process.execPath, [${JSON.stringify(mockPath)}, ...args], { stdio: 'inherit', env: process.env });
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill(signal));
  child.on('exit', (code, signal) => signal ? process.kill(process.pid, signal) : process.exit(code ?? 1));
}
`;
}

function transcriptLaunches(home) {
  const dir = path.join(home, '.attn-mock-agent');
  if (!fs.existsSync(dir)) return [];
  return fs.readdirSync(dir).filter((name) => name.endsWith('.jsonl')).flatMap((name) => {
    const file = path.join(dir, name);
    const records = fs.readFileSync(file, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));
    return records.filter((record) => record.type === 'session_meta').map((record) => ({ file, ...record.payload }));
  });
}

const wrapper = path.join(runner.sessionDir, 'model-aware-mock.mjs');
fs.mkdirSync(runner.sessionDir, { recursive: true });
fs.writeFileSync(wrapper, modelAwareWrapperSource(MOCK_AGENT_EXECUTABLE), { encoding: 'utf8', mode: 0o755 });
fs.chmodSync(wrapper, 0o755);
process.env.ATTN_CODEX_EXECUTABLE = wrapper;
process.env.ATTN_CLAUDE_EXECUTABLE = wrapper;
process.env.ATTN_MOCK_AGENT_LAUNCH_RECEIPT = 'wake-received';

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('restore_queue_mode', () => (
  client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' }).catch(() => {})
));

try {
  const webkitBaseline = await captureWebKitPids();
  await client.quitApp();
  runAttn(['daemon', 'stop']);
  for (const [home, fixture, filename] of [
    [awakeHome, crewManagementFixture.alder, '2026-09-06T04-45Z-alder.md'],
    [asleepHome, crewManagementFixture.keel, '2026-08-19T00-11Z-keel.md'],
    [historyHome, crewManagementFixture.trellis, '2026-09-01T21-37Z-trellis.md'],
  ]) {
    fs.mkdirSync(home, { recursive: true });
    fs.writeFileSync(path.join(home, 'CHARTER.md'), fixture.charter);
    fs.mkdirSync(path.join(home, 'handoffs'), { recursive: true });
    fs.writeFileSync(path.join(home, 'handoffs', filename), fixture.letter);
  }
  writeMockAgentFixture(awakeHome, {
    version: 1,
    turns: [
      { includes: 'You have been woken', actions: [{ type: 'reply', text: 'CREW_PANEL_READY', state: 'idle' }] },
      { includes: '📬 You have unread items', submitHook: false, actions: [
        { type: 'attn', args: ['agent', 'inbox'] },
        { type: 'wait_for_file', path: 'continue-restart', timeoutMs: 30_000 },
        { type: 'attn', args: ['handoff', '--nap', '-m', 'CREW_PANEL_HANDOFF'] },
      ] },
    ],
  });
  writeMockAgentFixture(asleepHome, { version: 1, turns: [] });
  await launchFreshAppAndConnect(client, observer, {
    agentExecutables: { codex: wrapper, claude: wrapper },
  });
  await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
  runAttn(['crew', 'set', awake, '--cwd', awakeHome, '--agent', 'codex']);
  runAttn(['crew', 'set', asleep, '--cwd', asleepHome]);
  runAttn(['crew', 'set', history, '--cwd', historyHome]);
  linkedSeed = json(['seed', 'plant', 'Crew charter history link receipt', '-m', 'Opened from a full handoff letter.', '--json']).id;
  fs.writeFileSync(
    path.join(historyHome, 'handoffs', '2026-08-31T18-05Z-trellis.md'),
    `# Linked handoff\n\nContinue from [Crew charter history link receipt](${linkedSeed}).\n`,
  );
  const awakeBinding = waitForCrew(
    awake,
    (member) => Boolean(member.binding_session),
    'the awake member binding broadcast',
  );
  firstSession = json(['crew', 'wake', awake, '--json']).session_id;
  const boundMember = await awakeBinding;
  runner.assert(boundMember.binding_session === firstSession, 'the roster binds the requested first day', { boundMember, firstSession });
  await observer.waitForSession({ id: firstSession, timeoutMs: 30_000 });
  await waitForFileSignal(wakeReceipt, 'the first crew day mock launch');
  fs.unlinkSync(wakeReceipt);
  await waitForDom('[data-testid="sidebar-queue"]');
  await waitForDom(`[data-testid="queue-crew-${awake}"][data-crew-state="awake"]`);
  await waitForDom(`[data-testid="queue-crew-${asleep}"]`);
  await waitForDom(`[data-testid="queue-crew-${history}"]`);
  await click(`[data-testid="queue-crew-select-${awake}"]`);
  await waitForDom(`[data-testid="queue-crew-${awake}"].selected`);
  const workspaceIdle = await sampleIdle(webkitBaseline);
  await driver.activateApp();

  await runner.step('manage_entry_retains_the_sidebar_and_returns_keyboard_focus', async () => {
    await click('[data-testid="manage-crew"]');
    const text = await panelText();
    runner.assert(text.includes('Manage crew') && text.includes('Awake') && text.includes('Asleep'), 'the roster keeps both member states visible', { text });
    const sidebarGeometry = await client.request('dom_bounds', { selector: '.sidebar' });
    const panelGeometry = await client.request('dom_bounds', { selector: '[data-testid="crew-panel"]' });
    const sidebarBounds = sidebarGeometry.bounds;
    const panelBounds = panelGeometry.bounds;
    runner.assert(
      sidebarBounds.width > 0 && panelBounds.x >= sidebarBounds.x + sidebarBounds.width,
      'the global sidebar remains visible beside Crew',
      { sidebarBounds, panelBounds },
    );
    runner.writeJson('crew-sidebar-geometry.json', { sidebarBounds, panelBounds });
    await screenshot('01-manage-roster.png');
    await pressEscapeAndWaitFor('manage-crew');
  });

  await runner.step('asleep_member_entry_round_trips_defaults_and_focus', async () => {
    await click(`[data-testid="crew-actions-${asleep}"]`);
    await click('[data-testid="crew-member-details-action"]');
    await waitForDom('[data-testid="crew-panel"]', { includes: 'Between days' });
    runner.assert((await panelText()).includes('Wake member'), 'asleep details offer Wake');
    const savedClaude = waitForCrew(asleep, (member) => member.agent === 'claude', 'the explicit asleep harness save');
    await select('[data-testid="crew-harness"]', 'claude');
    await savedClaude;
    const savedDefault = waitForCrew(asleep, (member) => !member.agent, 'the crew-default clear');
    await select('[data-testid="crew-harness"]', '');
    await savedDefault;
    await waitForDom('[data-testid="crew-panel"]', { includes: 'Saved' });
    runner.assert((await panelText()).includes('Saved'), 'the cleared default is acknowledged');
    await screenshot('02-asleep-defaults.png');
    await pressEscapeAndWaitFor(`crew-actions-${asleep}`);
  });

  await runner.step('full_charter_saves_recovers_and_detects_an_external_edit', async () => {
    await click('[data-testid="manage-crew"]');
    await click(`[data-testid="crew-roster-${history}"]`);
    await click('[data-testid="crew-tab-charter"]');
    await waitForDom('[data-testid="crew-charter-editor"]');
    await client.request('dom_focus', { selector: '[data-testid="crew-charter-editor"]' });
    const initialEditor = await client.request('dom_active_element');
    runner.assert(
      initialEditor.valueLength === crewManagementFixture.trellis.charter.length,
      'the editor receives the complete copied Trellis charter',
      { initialEditor, expectedLength: crewManagementFixture.trellis.charter.length },
    );
    await screenshot('03-charter-full.png');

    const charterPath = path.join(historyHome, 'CHARTER.md');
    const failedDraft = `${crewManagementFixture.trellis.charter}\n<!-- retained after write failure -->\n`;
    fs.chmodSync(historyHome, 0o555);
    await type('[data-testid="crew-charter-editor"]', failedDraft);
    await waitForDom('[data-testid="crew-charter-status"]', { includes: 'Not saved' });
    runner.assert(fs.readFileSync(charterPath, 'utf8') === crewManagementFixture.trellis.charter,
      'a failed save keeps the canonical charter unchanged');
    await screenshot('04-charter-save-failed.png');
    fs.chmodSync(historyHome, 0o755);
    await click('[data-testid="crew-charter-save-retry"]');
    await waitForDom('[data-testid="crew-charter-status"]', { includes: 'Saved' });
    runner.assert(fs.readFileSync(charterPath, 'utf8') === failedDraft,
      'retry persists the retained full draft');

    const externalDraft = `${failedDraft}\n<!-- external editor won this revision -->\n`;
    const localDraft = `${failedDraft}\n<!-- panel keeps this local revision -->\n`;
    fs.writeFileSync(charterPath, externalDraft);
    await type('[data-testid="crew-charter-editor"]', localDraft);
    await waitForDom('[data-testid="crew-charter-status"]', { includes: 'Changed elsewhere' });
    runner.assert(fs.readFileSync(charterPath, 'utf8') === externalDraft,
      'the expected content token prevents an external edit from being overwritten');
    await click('[data-testid="crew-charter-keep-mine"]');
    await waitForDom('[data-testid="crew-charter-status"]', { includes: 'Saved' });
    runner.assert(fs.readFileSync(charterPath, 'utf8') === localDraft,
      'Keep my edit retries against the returned authoritative token');

    const navigationDraft = `${localDraft}\n<!-- navigation flush receipt -->\n`;
    await type('[data-testid="crew-charter-editor"]', navigationDraft);
    await click('[data-testid="crew-tab-handoffs"]');
    await waitForDom('[data-testid="crew-handoff-reader"]', { includes: 'day thirty-three' });
    runner.assert(fs.readFileSync(charterPath, 'utf8') === navigationDraft,
      'tab navigation waits for the pending charter write');
  });

  await runner.step('complete_handoff_history_opens_a_native_seed_and_returns_in_place', async () => {
    const reader = await client.request('dom_text', { selector: '[data-testid="crew-handoff-reader"]' });
    runner.assert(
      reader.text.includes('Nothing waits on you except the first-wake design conversation')
        && (await panelText()).includes('2 letters'),
      'the full real 2026-09-01 Trellis letter and its dated history are rendered',
      { reader },
    );
    await screenshot('05-handoff-history-full.png');
    await click('[data-testid="crew-handoff-1"]');
    await waitForDom('[data-testid="crew-handoff-reader"]', { includes: 'Linked handoff' });
    await client.request('dom_focus', { selector: `[data-seed-target="${linkedSeed}"]` });
    await driver.pressEnter();
    await waitForDom(`.seed-document[data-seed-id="${linkedSeed}"]`);
    await waitForDom(`[data-pane-id="tile-seed-${linkedSeed}"] .workspace-dock-tile-body--seed`, { focused: true });
    await waitForDom('[data-testid="crew-seed-back"]');
    const hiddenPanel = await client.request('dom_bounds', { selector: '[data-testid="crew-panel"]' });
    runner.assert(hiddenPanel.bounds.width === 0 && hiddenPanel.bounds.height === 0,
      'the crew panel yields to the existing workspace seed tile', hiddenPanel);
    await screenshot('06-handoff-seed-tile.png');
    await click('[data-testid="crew-seed-back"]');
    await waitForDom('[data-testid="crew-panel"]', { includes: 'Linked handoff' });
    const selected = await waitForDom(`[data-testid="crew-roster-${history}"][aria-current="true"]`);
    const selectedTab = await waitForDom('[data-testid="crew-tab-handoffs"][aria-current="page"]');
    runner.assert(Boolean(selected.text) && selectedTab.text === 'Handoffs',
      'returning preserves the selected member, Handoffs tab, and letter', { selected, selectedTab });
    await pressEscapeAndWaitFor('crew-seed-back');
  });

  await runner.step('awake_member_entry_saves_a_complete_next_wake_selection', async () => {
    await click(`[data-testid="session-actions-${firstSession}"]`);
    await click('[data-testid="crew-member-details-action"]');
    await waitForDom('[data-testid="crew-panel"]', { includes: 'Current day active' });
    const running = await client.request('dom_text', { selector: '[aria-label="Running now"]' });
    runner.assert(running.text.includes('codex') && running.text.includes('ModelNot reported') && running.text.includes('EffortNot reported'), 'running truth stays separate from next-wake pins', running);
    await pressEscapeAndWaitFor(`session-actions-${firstSession}`);
    await click(`[data-testid="session-actions-${firstSession}"]`);
    await click('[data-testid="crew-member-details-action"]');
    await select('[data-testid="crew-harness"]', 'claude');
    await waitForDom('[data-testid="crew-model"]', { includes: 'Crew Claude' });
    const fullSave = waitForCrew(
      awake,
      (member) => member.agent === 'claude' && member.model === 'crew-claude' && member.effort === 'high',
      'the full launch selection to be acknowledged',
    );
    await select('[data-testid="crew-model"]', 'crew-claude');
    await type('[data-testid="crew-effort"]', 'high');
    const saved = await fullSave;
    await waitForDom('[data-testid="crew-panel"]', { includes: 'Saved' });
    runner.writeJson('saved-next-wake.json', saved);
    await screenshot('07-next-wake-saved.png');
  });

  await runner.step('a_disconnected_save_recovers_after_the_socket_reconnects', async () => {
    const disconnected = observer.waitForDisconnect();
    const stopped = await stopDaemon(profile);
    runner.assert(Number.isInteger(stopped), 'the scenario stopped its isolated daemon by captured pid', { stopped });
    await disconnected;
    await type('[data-testid="crew-effort"]', 'low');
    await waitForDom('[data-testid="crew-panel"]', { includes: 'Not saved' });
    runner.assert((await panelText()).includes('WebSocket not connected'), 'the panel explains the transport failure');
    await screenshot('08-save-disconnected.png');
    runAttn(['daemon', 'ensure']);
    await observer.connect();
    await waitForDom('[data-testid="crew-panel"]', { includes: 'Saved' }, 45_000);
    const reconnected = crewMember(awake);
    runner.assert(reconnected?.effort === 'low', 'the reconnect retry persisted the draft', reconnected);
    runner.writeJson('reconnected-next-wake.json', reconnected);
  });

  await runner.step('restart_reports_lifecycle_and_launches_one_changed_successor', async () => {
    await click('[data-testid="crew-restart"]');
    await screenshot('09-restart-confirmation.png');
    await click('[data-testid="crew-confirm-restart"]');
    await waitForDom('[data-testid="crew-panel"]', { includes: 'Handoff requested' });
    await screenshot('10-restart-requested.png');
    const completedEvent = waitForCrew(
      awake,
      (member) => member.restart?.state === 'completed' && member.binding_session !== firstSession,
      'the successor launch',
      45_000,
    );
    const successorReceipt = waitForFileSignal(wakeReceipt, 'the successor mock launch');
    fs.writeFileSync(path.join(awakeHome, 'continue-restart'), 'continue\n');
    const completed = await completedEvent;
    successor = completed.binding_session;
    await observer.waitForSession({ id: successor, timeoutMs: 30_000 });
    await successorReceipt;
    await waitForDom('[data-testid="crew-panel"]', { includes: 'New day started' });
    const bound = [...observer.sessionsById.values()].filter((session) => session.crew_member === awake);
    runner.assert(bound.length === 1 && bound[0].id === successor, 'exactly one live session owns the member binding', { bound, completed });
    const launch = transcriptLaunches(awakeHome).find((entry) => entry.id === successor);
    runner.assert(Boolean(launch), 'the registered successor wrote its launch receipt', { successor });
    const argv = launch.launch?.argv ?? [];
    runner.assert(argv.includes('--model') && argv[argv.indexOf('--model') + 1] === 'crew-claude', 'the saved model reaches the successor argv', { argv });
    runner.assert(argv.includes('--effort') && argv[argv.indexOf('--effort') + 1] === 'low', 'the reconnect-saved effort reaches the successor argv', { argv });
    runner.writeJson('successor-launch.json', { firstSession, successor, argv, completed });
    await screenshot('11-successor-complete.png');
  });

  await runner.step('open_panel_is_idle_after_the_lifecycle_settles', async () => {
    runner.writeJson('idle.json', { workspace: workspaceIdle, crewPanel: await sampleIdle(webkitBaseline) });
  });

  console.log(JSON.stringify(await runner.finishSuccess({ awake, asleep, history, linkedSeed, firstSession, successor }), null, 2));
} catch (error) {
  await screenshot('failure.png').catch(() => {});
  console.error(JSON.stringify(await runner.finishFailure(error, { awake, asleep, history, linkedSeed, firstSession, successor }), null, 2));
  process.exitCode = 1;
} finally {
  try {
    fs.chmodSync(historyHome, 0o755);
    const handoffs = path.join(awakeHome, 'handoffs');
    if (fs.existsSync(handoffs)) {
      for (const name of fs.readdirSync(handoffs)) {
        fs.renameSync(path.join(handoffs, name), path.join(runner.runDir, `restart-${name}`));
      }
    }
    if (successor) {
      await client.request('close_session', { sessionId: successor });
    }
  } catch (error) {
    console.error(`Crew cleanup: ${error instanceof Error ? error.message : String(error)}`);
    process.exitCode = 1;
  }
  await observer.close().catch(() => {});
  await client.quitApp().catch(() => {});
}
