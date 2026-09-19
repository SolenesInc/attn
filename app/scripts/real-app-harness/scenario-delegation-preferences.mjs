#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, profileCliEnv, socketPathForProfile } from './harnessProfile.mjs';
import { appDaemonInTree, delay } from './platform.mjs';
import { captureWebKitPids, readLiveDaemonPid, readProcessTable, snapshot, readAppFootprint } from './perfMeasure.mjs';
import { MOCK_AGENT_MODEL } from './mockAgent.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const profile = currentHarnessProfile();
if (!profile) throw new Error('Delegation preferences verification requires a named profile');
const runner = createScenarioRunner(options, { scenarioId: 'DelegationPreferences', tier: 'local', prefix: 'delegation-preferences', allowRealAgents: false });
process.env.ATTN_HARNESS_SKILL_SYNC = '1';
process.env.ATTN_TOOL_HOME = path.join(runner.runDir, 'tool-home');
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const root = '[data-testid="delegation-settings"]';
const toggle = '.settings-content-head [role="switch"][aria-label="Delegation preferences"]';
const popover = 'dialog[aria-label="Choose a model"]';
const runAttn = args => execFileSync(appDaemonInTree(options.appPath), args, { encoding: 'utf8', env: profileCliEnv(profile, { ATTN_SOCKET_PATH: socketPathForProfile(profile) }) });
const roles = () => JSON.parse(runAttn(['delegate', 'roles', '--json']));
const click = selector => client.request('dom_click', { selector });
const type = (selector, text) => client.request('dom_type', { selector, text });
const text = async (selector = root) => (await client.request('dom_text', { selector })).text;
const exists = selector => client.request('dom_text', { selector }).then(() => true, () => false);
const hold = () => process.env.ATTN_HARNESS_RECORD === '1' ? delay(1200) : Promise.resolve();
async function until(check, description) {
  for (let i = 0; i < 100; i++) { const found = await check(); if (found) return found; await delay(100); }
  throw new Error(`Timed out: ${description}`);
}
const preferences = async () => (await preferencesRequest('delegation_preferences_get')).preferences;
const savedRole = (name, check = () => true) => until(async () => (await preferences()).roles.find(role => role.name === name && check(role)), `${name} saved`);
// Text fields commit on blur: focus the field, set its value, then move focus to the open row's details button.
async function fill(selector, value) {
  await client.request('dom_focus', { selector });
  await type(selector, value);
  await client.request('dom_focus', { selector: `${root} .delegation-row.open .delegation-more` });
}
async function openModel(label) {
  await click(`${root} [aria-label="Model for ${label}"]`);
  await until(() => exists(popover), `model popover for ${label}`);
}
const closePopover = () => click(`${root} .delegation-lead`);
// DOM pixels through the bridge, so nothing activates the window. A fixed popover only paints
// from body; a whole-body capture is safe here because no terminal pane exists yet.
async function screenshot(name, selector = '[data-testid="settings-modal"]') {
  if (process.env.ATTN_HARNESS_SCREENSHOTS === '0') return;
  const shot = await client.request('capture_screenshot_data', { selector });
  fs.writeFileSync(path.join(runner.runDir, name), Buffer.from(shot.pngBase64, 'base64'));
}

function preferencesRequest(cmd, preferences, installWorkflowSkill = false) {
  const request_id = crypto.randomUUID();
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => { observer.ws?.off('message', receive); reject(new Error(`${cmd} timed out`)); }, 10000);
    const receive = raw => {
      const value = JSON.parse(String(raw));
      if (value.request_id !== request_id) return;
      clearTimeout(timeout); observer.ws.off('message', receive);
      if (value.success) resolve(value); else reject(new Error(value.error));
    };
    observer.ws.on('message', receive);
    observer.ws.send(JSON.stringify({ cmd, request_id, preferences, ...(installWorkflowSkill ? { install_workflow_skill: true } : {}) }));
  });
}
let source, worker, baseline, builderRoleID;
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_sessions', () => closeScenarioSessions(client, [worker, source].filter(Boolean)));
try {
  const webkitBaseline = await captureWebKitPids();
  await launchFreshAppAndConnect(client, observer);
  const initial = await preferencesRequest('delegation_preferences_get');
  baseline = initial.preferences;
  runner.registerCleanup('restore_preferences', async () => {
    const current = await preferencesRequest('delegation_preferences_get');
    await preferencesRequest('delegation_preferences_save', { ...baseline, revision: current.preferences.revision });
  });
  if (baseline.revision !== 0) {
    await preferencesRequest('delegation_preferences_save', { ...baseline, enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } });
  }
  await runner.step('disabled_by_default', async () => {
    await client.request('dismiss_whats_new');
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await client.request('settings_select_section', { sectionId: 'general' });
    const appearanceStatus = await text('.settings-status-pair');
    runner.assert(appearanceStatus.trim() === '', 'appearance header has no decorative status pills');
    await screenshot('00-appearance.png'); await hold();
    await client.request('settings_select_section', { sectionId: 'delegation' });
    await until(async () => (await text()).includes('No roles yet'), 'empty table with role setup available');
    runner.assert((await text(toggle)).includes('Off'), 'the head switch reads Off before opt-in');
    runner.assert(roles().roles.length === 0, 'roles lookup is empty before opt-in');
    await screenshot('01-disabled.png'); await hold();
  });
  await runner.step('profile_rejects_install_then_configure_custom_builder', async () => {
    await click(toggle);
    await until(async () => (await preferences()).enabled, 'delegation preferences enabled');
    await click(`${root} .delegation-empty .settings-action.primary`);
    await until(async () => (await text()).includes('installation is disabled for profile'), 'profile-safe workflow install refusal');
    runner.assert((await preferences()).roles.length === 0, 'failed workflow installation leaves saved roles unchanged');
    await click(`${root} .delegation-empty .settings-action:not(.primary)`);
    await until(() => exists('input[id^="name-role-"]'), 'new custom role opens its editor');
    await fill('input[id^="name-role-"]', 'Builder');
    const builder = await savedRole('Builder');
    runner.assert(builder.id.startsWith('role-') && !builder.builtin, 'custom Builder saves without workflow installation');
    builderRoleID = builder.id;
    runner.assert(roles().roles.length === 0, 'a role without a harness is not offered to agents');
    await openModel('Builder');
    await click(`${popover} [data-harness="codex"]`);
    await savedRole('Builder', role => role.choices[0].selection.harness === 'codex');
    await until(() => exists(`${popover} [data-model="${MOCK_AGENT_MODEL}"]`), 'Codex lists its models through the mock app-server');
    await click(`${popover} [data-model="${MOCK_AGENT_MODEL}"]`);
    await savedRole('Builder', role => role.choices[0].selection.model === MOCK_AGENT_MODEL);
    await click(`${popover} [data-effort="medium"]`);
    await savedRole('Builder', role => role.choices[0].selection.effort === 'medium');
    runner.assert(roles().roles.some(role => role.id === builderRoleID), 'a role with a harness and model is offered to agents');
    const header = await text('.settings-content-head');
    runner.assert(!header.includes('% text') && !header.includes('dark'), 'header has no appearance badges');
    await closePopover();
    await screenshot('02-roles.png'); await hold();
    await openModel('anything else');
    await click(`${popover} [data-harness="codex"]`);
    await until(async () => (await preferences()).fallback.selection.harness === 'codex', 'fallback harness saved');
    runner.assert(roles().fallback?.selection.harness === 'codex', 'fallback configures independently');
    await screenshot('02-fallback.png', 'body'); await hold();
    await click(`${popover} [data-harness=""]`);
    await until(async () => (await preferences()).fallback.selection.harness === '', 'fallback cleared');
    runner.assert(!roles().fallback, 'a cleared fallback is no longer offered to agents');
    await openModel('anything else');
    await click(`${popover} [data-harness="codex"]`);
    await until(async () => (await preferences()).fallback.selection.harness === 'codex', 'fallback harness saved again');
    await closePopover();
  });
  await runner.step('add_builder_effort_alternative', async () => {
    if (!(await exists(`${root} .delegation-row.open[data-role-id="${builderRoleID}"]`))) await click(`${root} [aria-label="Builder"]`);
    await click(`${root} .delegation-alt.add button`);
    await until(() => exists('input[id^="altname-"]'), 'alternative editor opens');
    await fill('input[id^="altname-"]', 'Difficult verification');
    await fill('textarea[id^="when-"]', 'Verification is difficult.\n\nOr the requirements are ambiguous and the agent has to ask.');
    await savedRole('Builder', role => role.choices.length === 2 && role.choices[1].when.includes('ambiguous'));
    await openModel('Difficult verification');
    await click(`${popover} [data-effort="high"]`);
    const builder = await savedRole('Builder', role => role.choices[1].selection.effort === 'high');
    runner.assert(builder.choices[1].name === 'Difficult verification' && builder.choices[1].selection.model === MOCK_AGENT_MODEL, 'the alternative starts from the default model and keeps its own effort');
    runner.assert(builder.choices[0].selection.effort === 'medium' && !builder.builtin, 'the default choice and the custom role are untouched');
    await screenshot('03-model-choices.png', 'body'); await hold();
    await closePopover();
    await screenshot('03-role-editor.png', `${root} .delegation-row.open`); await hold();
  });
  await runner.step('custom_role_can_be_deleted_and_restored', async () => {
    await click(`${root} .delegation-addrow .settings-action:first-child`);
    await until(() => exists(`${root} .delegation-row.open:not([data-role-id="${builderRoleID}"]) input[id^="name-role-"]`), 'second custom role opens its editor');
    await fill(`${root} .delegation-row.open input[id^="name-role-"]`, 'Debug');
    await savedRole('Debug');
    await openModel('Debug');
    await click(`${popover} [data-harness="codex"]`);
    await savedRole('Debug', role => role.choices[0].selection.harness === 'codex' && role.choices[0].selection.model === '');
    await closePopover();
    runner.assert(roles().roles.some(r => r.name === 'Debug' && r.choices[0].selection.harness === 'codex'), 'a harness default is a complete choice');
    await click(`${root} .delegation-row.open .delegation-details.actions .danger`);
    await until(async () => !(await preferences()).roles.some(role => role.name === 'Debug'), 'Debug deleted');
    await click(`${root} .delegation-undo button`);
    await savedRole('Debug');
    runner.assert(roles().roles.some(r => r.name === 'Debug'), 'Undo restores a deleted role');
    await screenshot('04-custom-role.png'); await hold();
  });
  const before = roles();
  await runner.step('request_override_reaches_visible_session', async () => {
    await click('[data-testid="settings-close"]');
    fs.mkdirSync(runner.sessionDir, { recursive: true });
    source = await createSessionAndWaitForInitialPane({ client, observer, cwd: runner.sessionDir, label: 'Delegation source', agent: 'shell', sessionWaitMs: 30000 });
    const output = runAttn(['delegate', '--source-session', source, '--role', builderRoleID, '--effort', 'high', '--brief', 'Delegation settings verification. Wait for direction.', '--cwd', runner.sessionDir, '--name', 'Builder check']);
    const result = JSON.parse(output.slice(output.indexOf('{')));
    worker = result.session_id;
    await observer.waitFor(() => observer.sessionsById.has(worker), 'visible delegated session');
    const builder = roles().roles.find(r => r.id === builderRoleID);
    runner.assert(builder?.choices[0]?.selection.effort === 'medium', 'request effort does not mutate the role default');
    runner.writeText('delegation-result.json', JSON.stringify(result, null, 2));
    await hold();
  });
  await runner.step('disable_hides_roles_and_reenable_restores_them', async () => {
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await client.request('settings_select_section', { sectionId: 'delegation' });
    await click(toggle);
    await until(() => roles().roles.length === 0, 'roles hidden');
    await until(() => exists(`${toggle}[aria-checked="false"]`), 'the switch reads Off');
    runner.assert((await text()).includes('Your table is kept'), 'the table stays visible while off');
    await hold();
    await click(toggle);
    await until(() => roles().roles.length === before.roles.length, 'saved roles restored');
    runner.writeJson('roles.json', roles());
    await screenshot('05-restored.png'); await hold();
  });
  await runner.step('idle_preferences_page', async () => {
    await closeScenarioSessions(client, [worker, source].filter(Boolean));
    worker = undefined;
    source = undefined;
    await delay(3000);
    const appPid = client.readManifest().pid;
    const daemonPid = readLiveDaemonPid(profile);
    const before = await snapshot(appPid, daemonPid, webkitBaseline);
    const pids = new Set(Object.values(before.byClass).flatMap(value => value.pids.map(value => value.pid)));
    const samples = [];
    for (let i = 0; i < 10; i++) {
      await delay(1000);
      samples.push((await readProcessTable()).filter(process => pids.has(process.pid)));
    }
    const after = await snapshot(appPid, daemonPid, webkitBaseline);
    runner.writeJson('idle.json', { before, after, samples, footprint: await readAppFootprint(after) });
  });
  console.log(JSON.stringify(await runner.finishSuccess({ source, worker }), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
