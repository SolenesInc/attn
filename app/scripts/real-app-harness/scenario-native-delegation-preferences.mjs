#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { MOCK_AGENT_MODEL } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-DELEGATION-PREFERENCES', tier: 'tier2-local-mock-agent', prefix: 'native-delegation-preferences',
  metadata: { focus: "The native app's Delegation settings turn roles on, refuse the workflow install on an instance, save a custom role's model, effort and alternative, delete and undo a role, and hide the table from agents while off" },
});
const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const roles = () => JSON.parse(attn('delegate', 'roles', '--json'));
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));
runner.registerFailureEvidence('native_tree', async () => runner.writeJson('failure-tree.json', await driver.tree()));

const SWITCH = { role: 'switch', name: 'Delegation preferences' };
const PICKER = 'settings.route-picker';
const shot = (file) => driver.screenshot(path.join(runner.runDir, file));
const created = [];

async function preferencesRequest(cmd, preferences) {
  const request_id = crypto.randomUUID();
  const answer = observer.waitForMessage((m) => m.request_id === request_id && m, `${cmd} answer`, 10_000);
  observer.send({ cmd, request_id, ...(preferences ? { preferences } : {}) });
  const value = await answer;
  if (!value.success) throw new Error(`${cmd}: ${value.error}`);
  return value;
}
const preferences = async () => (await preferencesRequest('delegation_preferences_get')).preferences;

async function saved(check, description) {
  const changed = () => observer.waitForMessage((m) => m.event === 'delegation_preferences_changed' && m, description, 15_000);
  for (;;) {
    const next = changed();
    const found = check(await preferences());
    if (found) {
      next.catch(() => {});
      return found;
    }
    await next;
  }
}

function nodes(tree, under) {
  const found = [];
  const walk = (node, inside) => {
    if (!node) return;
    const here = inside || node.key === under;
    if (here) found.push(node);
    for (const child of node.children ?? []) walk(child, here);
  };
  walk(tree, false);
  return found;
}

async function pressIn(list, role, prefix) {
  await driver.waitFor({ key: list === 'models' ? `${PICKER}.model.1` : `${PICKER}.${list}` }, { timeoutMs: 30_000 });
  const hit = nodes(await driver.tree(), `${PICKER}.${list}`).find((n) => n.role === role && n.key && (n.name ?? '').startsWith(prefix));
  runner.assert(Boolean(hit), `the picker's ${list} offer ${prefix}`);
  await driver.click({ key: hit.key });
}

async function openModel(label) {
  await driver.click({ role: 'button', name: `Model for ${label}` });
  await driver.waitFor({ key: PICKER });
}

async function closePicker() {
  if (await driver.find({ key: PICKER })) {
    await driver.pressKey('Escape');
    await driver.waitFor({ key: PICKER }, { gone: true });
  }
}

async function write(key, text) {
  await driver.click({ key });
  await driver.pressKey('ctrl+a');
  await driver.typeInto({ key }, text);
  await driver.click({ key: `${key}.label` });
}

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  const baseline = await preferences();
  runner.registerCleanup('restore_preferences', async () => {
    const current = await preferences();
    await preferencesRequest('delegation_preferences_save', { ...baseline, revision: current.revision });
  });
  if (baseline.revision !== 0) {
    await preferencesRequest('delegation_preferences_save', {
      ...baseline, enabled: false, roles: [],
      fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' },
    });
  }

  let builder;
  await runner.step('off_until_turned_on', async () => {
    await driver.run('ui.openSettings');
    await driver.click({ key: 'settings.nav.delegation' });
    await driver.waitFor({ key: 'settings.delegation.empty.heading' });
    const off = await driver.find(SWITCH);
    runner.assert(off && !(off.states ?? []).includes('checked'), 'the head switch is off before opt-in', off);
    runner.assert(roles().roles.length === 0, 'agents see no roles before opt-in');
    await shot('01-off.png');
  });

  await runner.step('an_instance_refuses_the_workflow_install_and_a_custom_role_saves', async () => {
    await driver.click(SWITCH);
    await saved((p) => p.enabled, 'delegation preferences on');
    await driver.click({ key: 'settings.delegation.adopt' });
    await driver.waitFor({ key: 'settings.delegation.error' }, { timeoutMs: 15_000 });
    const error = await driver.find({ key: 'settings.delegation.error' });
    runner.assert((error?.name ?? '').includes('installation is disabled for instance'), 'the instance refuses the workflow install', error);
    runner.assert((await preferences()).roles.length === 0, 'the refused install leaves the table empty');

    await driver.click({ key: 'settings.delegation.add' });
    const role = await saved((p) => p.roles[0], 'a custom role');
    await write(`settings.delegation.field.name.${role.id}`, 'Builder');
    builder = await saved((p) => p.roles.find((r) => r.name === 'Builder'), 'Builder named');
    runner.assert(!builder.builtin, 'Builder is a custom role');
    runner.assert(roles().roles.length === 0, 'a role without a harness is not offered to agents');

    await openModel('Builder');
    await driver.click({ key: `${PICKER}.harness.codex` });
    runner.assert((await preferences()).roles.find((r) => r.id === builder.id).choices[0].selection.harness === '', 'browsing a harness saves nothing');
    await pressIn('models', 'list-box-option', 'Mock agent 1');
    await saved((p) => p.roles.find((r) => r.id === builder.id && r.choices[0].selection.model === MOCK_AGENT_MODEL), 'Builder model');
    await pressIn('effort', 'button', 'medium');
    await saved((p) => p.roles.find((r) => r.id === builder.id && r.choices[0].selection.effort === 'medium'), 'Builder effort');
    await closePicker();
    runner.assert(roles().roles.some((r) => r.id === builder.id), 'a role with a harness and model is offered to agents');
    await shot('02-roles.png');

    await driver.click({ role: 'button', name: 'Anything else' });
    await openModel('anything else');
    await driver.click({ key: `${PICKER}.harness.codex` });
    await driver.click({ key: `${PICKER}.model.0` });
    await saved((p) => p.fallback.selection.harness === 'codex' && p, 'fallback harness');
    await closePicker();
    runner.assert(roles().fallback?.selection.harness === 'codex', 'the fallback is offered to agents');
    await openModel('anything else');
    await driver.click({ key: `${PICKER}.harness.none` });
    await saved((p) => p.fallback.selection.harness === '' && p, 'fallback cleared');
    await closePicker();
    runner.assert(!roles().fallback, 'a cleared fallback is no longer offered');
  });

  await runner.step('an_alternative_keeps_its_own_effort', async () => {
    await driver.click({ role: 'button', name: 'Builder' });
    await driver.click({ role: 'button', name: '+ Alternative model' });
    const alt = (await saved((p) => p.roles.find((r) => r.id === builder.id && r.choices.length === 2), 'an alternative')).choices[1].id;
    await write(`settings.delegation.field.alternative-name.${builder.id}/${alt}`, 'Difficult verification');
    await write(`settings.delegation.field.when.${builder.id}/${alt}`, 'Verification is difficult or the requirements are ambiguous.');
    await saved((p) => p.roles.find((r) => r.id === builder.id && r.choices[1].when.includes('ambiguous')), 'the condition');
    await openModel('Difficult verification');
    await pressIn('effort', 'button', 'high');
    const role = await saved((p) => p.roles.find((r) => r.id === builder.id && r.choices[1].selection.effort === 'high'), 'the alternative effort');
    runner.assert(role.choices[1].name === 'Difficult verification' && role.choices[1].selection.model === MOCK_AGENT_MODEL, 'the alternative starts from the default model', role);
    runner.assert(role.choices[0].selection.effort === 'medium', 'the default choice keeps its effort', role);
    await closePicker();
    await shot('03-alternative.png');
  });

  await runner.step('a_deleted_role_comes_back_with_undo', async () => {
    await driver.click({ role: 'button', name: 'Delete' });
    await saved((p) => !p.roles.some((r) => r.id === builder.id) && p, 'Builder deleted');
    await driver.click({ role: 'button', name: 'Undo' });
    await saved((p) => p.roles.find((r) => r.id === builder.id), 'Builder restored');
    runner.assert(roles().roles.some((r) => r.id === builder.id), 'Undo gives agents the role again');
  });

  const offered = roles().roles.length;
  await runner.step('a_delegation_overrides_the_effort_without_changing_the_role', async () => {
    await driver.pressKey('Escape');
    await driver.waitFor({ key: 'settings-dialog' }, { gone: true });
    fs.mkdirSync(runner.sessionDir, { recursive: true });
    const source = crypto.randomUUID();
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === source && m, 'the source shell spawned', 30_000);
    observer.send({ cmd: 'spawn_session', id: source, agent: 'shell', cwd: runner.sessionDir, profile_id: observer.profileId, label: 'Delegation source', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'the source shell starts');
    created.push(source);
    runner.registerCleanup('close_sessions', () => observer.unregisterMatchingSessions((s) => created.includes(s.id)));
    const output = attn('delegate', '--source-session', source, '--role', builder.id, '--effort', 'high',
      '--brief', 'Delegation settings verification. Wait for direction.', '--cwd', runner.sessionDir, '--name', 'Builder check');
    const result = JSON.parse(output.slice(output.indexOf('{')));
    created.push(result.session_id);
    await driver.waitFor({ key: `sidebar.session.${result.session_id}` });
    runner.assert(roles().roles.find((r) => r.id === builder.id)?.choices[0]?.selection.effort === 'medium', 'the request effort leaves the role default alone');
  });

  await runner.step('off_hides_the_roles_and_on_brings_them_back', async () => {
    await driver.run('ui.openSettings');
    await driver.click({ key: 'settings.nav.delegation' });
    await driver.click(SWITCH);
    await saved((p) => !p.enabled && p, 'delegation preferences off');
    runner.assert(roles().roles.length === 0, 'agents see no roles while off');
    const live = await driver.waitFor({ key: 'settings.delegation.live' });
    runner.assert((live?.name ?? '').includes('Your table is kept'), 'the page says the table is kept', live);
    await driver.click(SWITCH);
    await saved((p) => p.enabled && p, 'delegation preferences on again');
    runner.assert(roles().roles.length === offered, 'the saved roles come back');
    await shot('04-restored.png');
    await driver.pressKey('Escape');
  });

  await runner.finishSuccess({ sessions: created, role: builder?.id });
} catch (error) {
  await runner.finishFailure(error, { sessions: created });
  process.exitCode = 1;
}
