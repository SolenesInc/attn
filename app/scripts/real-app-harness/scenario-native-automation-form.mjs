#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { stepClock } from './stepClock.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const FORM_TIMEOUT_MS = 30_000;
const SCHEDULE_TICK = '1m';
const SCHEDULE_TICK_LIMIT = 10;
const FORM = 'automations.form';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('the native automation form scenario needs a named non-production instance');
const binary = appCliInTree(resolveHarnessResources(instance).appPath);
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-AUTOMATION-FORM', allowRealAgents: false, tier: 'tier2-local', prefix: 'native-automation-form',
  metadata: { instance, focus: 'The native automation form validates before the wire, saves new and edited definitions, recovers a stale revision, refuses a save on a deleted definition, leaves the enabled column to the switch, deletes on a second press, and saves a guarded schedule whose checks the drawer shows' },
});

const clockDir = fs.mkdtempSync(path.join(os.platform() === 'darwin' ? '/private/tmp' : os.tmpdir(), 'attn-clock-'));
const clock = await stepClock(path.join(clockDir, 'daemon.sock'), FORM_TIMEOUT_MS);
const daemonEnv = instanceCliEnv(instance, clock.env);

const client = new UiAutomationClient({ appPath: options.appPath, launchEnv: clock.env });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const cli = (...args) => execFileSync(binary, args.map(String), { encoding: 'utf8', env: daemonEnv, stdio: ['ignore', 'pipe', 'pipe'], timeout: 30_000 });
const cliJSON = (...args) => JSON.parse(cli(...args));
const listed = () => cliJSON('automation', 'list') || [];
const listRow = (id) => listed().find((row) => row.id === id) || null;
const byName = (name) => listed().find((row) => row.name === name)?.id;
const showFails = (id) => {
  try { cli('automation', 'show', id); return false; } catch (error) { return String(error.stderr || error.message).includes('automation: daemon error'); }
};
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const suffix = Date.now().toString(36);
const created = new Set();
let fixturePath = null;

async function poll(fn, description, timeoutMs = FORM_TIMEOUT_MS) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(150);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

function nodesWhere(node, test, found = []) {
  if (!node) return found;
  if (test(node)) found.push(node);
  for (const child of node.children ?? []) nodesWhere(child, test, found);
  return found;
}
const namesUnder = async (key) => nodesWhere(nodesWhere(await driver.tree(), (n) => n.key === key)[0], (n) => typeof n.name === 'string').map((n) => n.name);
const nameOf = async (key) => (await driver.find({ key }))?.name ?? null;

const drawer = () => driver.state('automations');
const form = async () => (await drawer()).form;
const waitForm = (test, description) => poll(async () => { const f = await form(); return test(f) ? (f ?? { present: false }) : null; }, description);
const formGone = (description) => waitForm((f) => f === null, description);
const press = (name) => driver.click({ key: `${FORM}.${name}` });

async function fill(name, text) {
  const key = `${FORM}.${name}`;
  await driver.click({ key });
  await driver.pressKey('mod+a');
  await driver.pressKey('Backspace');
  if (text) await driver.typeInto({ key }, text);
}

async function openNew() {
  await driver.click({ key: 'automations.new' });
  return waitForm((f) => f?.status === 'ready' && f.mode === 'create', 'a new form ready');
}

async function openEdit(id) {
  await driver.click({ key: `automations.edit.${id}` });
  return waitForm((f) => f?.status === 'ready' && f.definitionId === id, `the form for definition ${id} ready`);
}

async function createManual(name, prompt) {
  await openNew();
  await fill('name', name);
  await fill('directory', fixturePath);
  await fill('prompt', prompt);
  await press('save');
  await formGone(`${name} saved`);
  const id = byName(name);
  runner.assert(Number.isInteger(id) && id > 0, 'the daemon assigned a numeric ID', { name, id });
  created.add(id);
  return id;
}

async function toggleInDrawer(id, enabled) {
  await driver.click({ key: `automations.toggle.${id}` });
  await poll(async () => (await drawer()).definitions.find((d) => d.id === id)?.enabled === enabled, `definition ${id} ${enabled ? 'on' : 'off'} in the drawer`);
}

async function tickSchedulesUntil(what, done) {
  for (let ticks = 1; ticks <= SCHEDULE_TICK_LIMIT; ticks++) {
    await clock.awaitIdle();
    await clock.advance(SCHEDULE_TICK);
    await clock.awaitIdle();
    if (done()) return ticks;
  }
  throw new Error(`${what}: not after ${SCHEDULE_TICK_LIMIT} schedule ticks of ${SCHEDULE_TICK} (SCHEDULE_TICK_LIMIT)`);
}

runner.registerCleanup('restart_daemon', () => { try { execFileSync(binary, ['daemon', 'ensure'], { env: instanceCliEnv(instance), stdio: 'ignore' }); } catch {} });
runner.registerCleanup('close_clock', async () => { await clock.close(); fs.rmSync(clockDir, { recursive: true, force: true }); });
runner.registerCleanup('stop_stepped_daemon', () => { try { cli('daemon', 'stop'); } catch {} });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('delete_definitions', () => { for (const id of created) { try { cli('automation', 'delete', id); } catch {} } });

try {
  await runner.step('setup_fixtures', () => {
    fixturePath = fs.realpathSync(fs.mkdtempSync(path.join(runner.sessionDir, 'automation-form-')));
  });

  await runner.step('restart_stepped_daemon_and_launch', async () => {
    await client.quitApp();
    try { cli('daemon', 'stop'); } catch {}
    cli('daemon', 'ensure');
    await poll(() => { try { listed(); return true; } catch { return null; } }, 'the stepped daemon');
    await launchFreshNativeAppAndConnect(client, observer);
    await driver.click({ key: 'titlebar.automations' });
    await driver.waitFor({ key: 'automations' });
  });

  await runner.step('leg1_create_mode_defaults', async () => {
    const opened = await openNew();
    runner.assert(opened.mode === 'create' && opened.definitionId === null && opened.revision === 0, 'New opens in create mode with no id at revision 0', opened);
    runner.assert(opened.spec.trigger.type === 'manual', 'the default trigger is manual', opened.spec);
    runner.assert(JSON.stringify(opened.spec.launch) === JSON.stringify({ driver: 'codex' }), 'the default launch is Codex with model and effort left to the harness', opened.spec);
  });

  const primaryName = `Automation form proof ${suffix}`;
  await runner.step('leg2_client_validation_blocks_submit', async () => {
    const before = listed();
    await fill('name', primaryName);
    await fill('directory', 'relative/path');
    await fill('prompt', '');
    await press('save');
    const blocked = await waitForm((f) => Object.keys(f?.errors ?? {}).length > 0, 'validation errors after a blocked save');
    runner.assert('directoryPath' in blocked.errors && 'prompt' in blocked.errors, 'the folder and prompt errors are reported', blocked.errors);
    runner.assert(blocked.saving === false && blocked.saveError === '', 'the save never left the app', blocked);
    runner.assert(await nameOf(`${FORM}.prompt.error`) === 'A prompt is required.', 'the prompt says why under it');
    runner.assert(listed().length === before.length, 'nothing is stored after the blocked save', { before, after: listed() });
  });

  let primaryID = 0;
  let firstRevision = 0;
  const promptV1 = 'Automation form proof: initial create.';
  await runner.step('leg3_create_then_reopen_canonical', async () => {
    await fill('directory', fixturePath);
    await fill('prompt', promptV1);
    await press('save');
    await formGone('the create to close the form');
    primaryID = byName(primaryName);
    runner.assert(Number.isInteger(primaryID) && primaryID > 0, 'the daemon assigned a numeric ID', { primaryID });
    created.add(primaryID);
    await driver.screenshot(path.join(runner.runDir, 'leg3-after-save.png')).catch(() => {});
    const row = listRow(primaryID);
    const yaml = cli('automation', 'show', primaryID);
    runner.assert(row.revision === 1, 'a fresh create lands at revision 1', row);
    runner.assert(yaml.includes(`id: ${primaryID}`) && yaml.includes(`name: ${primaryName}`) && yaml.includes(promptV1), 'the CLI shows the saved spec', yaml);
    runner.assert(!yaml.includes('enabled:'), 'the stored spec carries no enabled key', yaml);
    firstRevision = row.revision;

    const opened = await openEdit(primaryID);
    runner.assert(opened.mode === 'edit' && opened.revision === firstRevision, 'Edit opens at the stored revision', opened);
    runner.assert(opened.spec.prompt.trim() === promptV1 && opened.enabled === true, 'the form reads back the saved prompt and the enabled column', opened);
    await press('cancel');
    await formGone('Cancel to close the form');
  });

  await runner.step('leg6_stale_revision_and_reload', async () => {
    await openEdit(primaryID);
    const outOfBand = 'Mutated out of band via the bundled CLI while the form was open.';
    const file = path.join(runner.sessionDir, 'out-of-band.yml');
    fs.writeFileSync(file, `api_version: attn.dev/automations/v1alpha1\nid: ${primaryID}\nname: ${primaryName}\ntrigger:\n  type: manual\nprompt: ${JSON.stringify(outOfBand)}\nlaunch:\n  driver: codex\nlocation:\n  type: directory\n  path: ${JSON.stringify(fixturePath)}\n`);
    cli('automation', 'apply', '--file', file);
    const outOfBandRevision = listRow(primaryID).revision;
    runner.assert(outOfBandRevision > firstRevision, 'the CLI apply moves the revision past the open form', { outOfBandRevision, firstRevision });

    const local = 'Local form edit before the out-of-band mutation lands.';
    await fill('prompt', local);
    await press('save');
    const stale = await waitForm((f) => f?.stale === true, 'the revision conflict to offer Reload');
    runner.assert(Boolean(stale.saveError), 'the refused save says why', stale);
    await driver.waitFor({ key: `${FORM}.reload` });
    await press('reload');
    const reloaded = await waitForm((f) => f?.revision === outOfBandRevision && f.saveError === '' && !f.stale, 'Reload to read the current revision');
    runner.assert(reloaded.spec.prompt === outOfBand, 'Reload shows what is there now', reloaded.spec);

    await fill('prompt', local);
    await press('save');
    await formGone('the recovered save to close the form');
    runner.assert(listRow(primaryID).revision === outOfBandRevision + 1, 'the recovered save bumps the revision once', listRow(primaryID));
    runner.assert(cli('automation', 'show', primaryID).includes(local), 'the recovered save stores the local edit');
  });

  await runner.step('leg7_unchanged_resave_is_noop_then_edit_bumps', async () => {
    const before = listRow(primaryID);
    await openEdit(primaryID);
    await press('save');
    await formGone('the unchanged save to close the form');
    runner.assert(listRow(primaryID).revision === before.revision, 'saving unchanged values keeps the revision', { before: before.revision, after: listRow(primaryID).revision });
    const renamed = `${primaryName} (renamed)`;
    await openEdit(primaryID);
    await fill('name', renamed);
    await press('save');
    await formGone('the rename to close the form');
    const after = listRow(primaryID);
    runner.assert(after.revision === before.revision + 1 && after.name === renamed, 'a rename bumps the revision once and stores the name', after);
  });

  await runner.step('leg8_github_roundtrip_no_spurious_bump', async () => {
    const name = `Automation form github proof ${suffix}`;
    await openNew();
    await press('trigger-github');
    await fill('name', name);
    await fill('prompt', 'Review PRs that request this automation as a reviewer.');
    await fill('repositories-include', 'github.com/acme/widgets');
    await driver.pressKey('Enter');
    await press('save');
    await formGone('the GitHub create to close the form');
    const id = byName(name);
    runner.assert(Number.isInteger(id) && id > 0, 'the daemon assigned a numeric ID', { id });
    created.add(id);
    runner.assert(cli('automation', 'show', id).includes('github.com/acme/widgets'), 'the stored spec includes the repository');
    runner.assert(listRow(id).revision === 1, 'the GitHub create lands at revision 1', listRow(id));
    const reopened = await openEdit(id);
    runner.assert(reopened.spec.trigger.type === 'github_review_requested', 'the form reads back the GitHub trigger', reopened.spec);
    runner.assert(JSON.stringify(reopened.spec.trigger.repositories?.include) === JSON.stringify(['github.com/acme/widgets']), 'the form reads back the include list', reopened.spec);
    await press('save');
    await formGone('the unchanged GitHub save to close the form');
    runner.assert(listRow(id).revision === 1, 'the daemon canonical spec round-trips through the form without a revision bump', listRow(id));
    cli('automation', 'delete', id);
    created.delete(id);
  });

  await runner.step('leg9_delete_elsewhere_then_save_refused', async () => {
    await openEdit(primaryID);
    cli('automation', 'delete', primaryID);
    created.delete(primaryID);
    runner.assert(!listRow(primaryID), 'the CLI delete removes the definition while the form is open');
    await fill('prompt', 'Edited after the definition was deleted elsewhere — this save must be refused.');
    await press('save');
    const refused = await waitForm((f) => (f?.saveError ?? '').toLowerCase().includes('deleted'), 'the save to be refused as deleted elsewhere');
    runner.assert(refused.saving === false, 'the form stays open with the edit after the refusal', refused);
    runner.assert(showFails(primaryID), 'the definition stays deleted');
    await press('cancel');
    await formGone('Cancel to close the refused form');
  });

  await runner.step('leg10_drawer_switch_and_form_reflects_column', async () => {
    const id = await createManual(`Automation form toggle proof ${suffix}`, 'Definition that the operator disables from the drawer and then edits.');
    const createdRow = listRow(id);
    runner.assert(createdRow.enabled === true, 'a new definition starts enabled', createdRow);
    await toggleInDrawer(id, false);
    const disabled = listRow(id);
    runner.assert(disabled.enabled === false && disabled.revision === createdRow.revision, 'the switch disables without a revision bump', disabled);
    runner.assert(!cli('automation', 'show', id).includes('enabled:'), 'the stored spec carries no enabled key');
    const opened = await openEdit(id);
    runner.assert(opened.enabled === false, 'the form switch reads the enabled column', opened);
    await press('save');
    await formGone('the unrelated save to close the form');
    const saved = listRow(id);
    runner.assert(saved.enabled === false && saved.revision === disabled.revision, 'a save never touches the enabled column', saved);
    await toggleInDrawer(id, true);
    runner.assert(listRow(id).revision === disabled.revision, 'turning it back on keeps the revision', listRow(id));
    cli('automation', 'delete', id);
    created.delete(id);
  });

  await runner.step('leg11_form_two_step_delete', async () => {
    const id = await createManual(`Automation form delete proof ${suffix}`, 'Definition created only to be deleted through the form itself.');
    await openEdit(id);
    await press('delete');
    const armed = await waitForm((f) => f?.deleteArmed === true, 'the first Delete to arm');
    runner.assert(await nameOf(`${FORM}.delete`) === 'Confirm delete', 'the armed button asks to confirm', armed);
    runner.assert(listRow(id) !== null, 'arming deletes nothing');
    await press('delete');
    await formGone('the confirmed delete to close the form');
    runner.assert(showFails(id), 'the definition is gone after the confirmed delete');
    created.delete(id);
  });

  await runner.step('scheduled_guard_in_form_and_drawer', async () => {
    const guard = 'sh guard.sh';
    const name = `Automation guard proof ${suffix}`;
    const guardFile = path.join(fixturePath, 'guard.sh');
    fs.writeFileSync(guardFile, 'exit 0\n');
    await openNew();
    await press('trigger-scheduled');
    await fill('name', name);
    await fill('directory', fixturePath);
    await fill('prompt', 'Work only when the guard prints something new.');
    await fill('cron', '* * * * *');
    await fill('guard', guard);
    await poll(async () => (await namesUnder(`${FORM}.next-runs`)).includes('Next checks'), 'the next runs to read Next checks once a guard is set');
    await driver.screenshot(path.join(runner.runDir, 'guard-form.png')).catch(() => {});
    await press('save');
    await formGone('the guarded schedule to save');
    const id = byName(name);
    runner.assert(id > 0, 'the form creates a scheduled automation with a guard', listed());
    created.add(id);
    const reopened = await openEdit(id);
    runner.assert(reopened.spec.trigger.guard === guard && reopened.spec.trigger.schedule.cron === '* * * * *', 'the saved guard and cron read back into the form', reopened.spec);
    await press('cancel');
    await formGone('Cancel to close the guard form');

    const outcome = () => listRow(id)?.guard_check?.outcome;
    const line = `automations.guard.${id}`;
    const emptyTicks = await tickSchedulesUntil('an empty guard check', () => outcome() === 'empty');
    const empty = await poll(async () => { const said = await nameOf(line); return said?.startsWith('Guard: empty') ? said : null; }, 'the drawer to show the empty check');
    fs.writeFileSync(guardFile, 'echo broken >&2; exit 17\n');
    const failedTicks = await tickSchedulesUntil('a failed guard check', () => outcome() === 'failed');
    const failed = await poll(async () => { const said = await nameOf(line); return said?.startsWith('Guard: failed') ? said : null; }, 'the drawer to show the failed check');
    await driver.waitFor({ key: `automations.failure.${id}` });
    const detail = await nameOf(`automations.guard-error.${id}`);
    runner.assert(detail?.includes('exit 17: broken'), 'a failed guard shows its exit code and stderr in the drawer', { detail });
    runner.writeJson('guard-checks.json', { empty, emptyTicks, failed, failedTicks, detail });
    await driver.screenshot(path.join(runner.runDir, 'guard-failed-drawer.png')).catch(() => {});
    runner.assert((cliJSON('automation', 'runs', id) || []).length === 0, 'empty and failed checks start no run');
    cli('automation', 'disable', id);
    cli('automation', 'delete', id);
    created.delete(id);
  });

  await runner.finishSuccess({ instance, primaryID });
} catch (error) {
  try { runner.writeJson('failure-automations-state.json', await drawer()); } catch {}
  try { await driver.screenshot(path.join(runner.runDir, 'failure.png')); } catch {}
  await runner.finishFailure(error, { instance });
  process.exitCode = 1;
}
