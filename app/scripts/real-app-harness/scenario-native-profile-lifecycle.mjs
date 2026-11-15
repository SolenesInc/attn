#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-PROFILE-LIFECYCLE', tier: 'tier1-local-shell', prefix: 'native-profile-lifecycle',
  metadata: { focus: 'In the native app the profile switcher creates a profile with N and switches to it, an agent started there keeps it, the title bar pill and the palette switch back and forth, R renames it, delete is refused while it holds the agent and goes through once it is empty, and the last profile stays, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const mac = process.platform === 'darwin';
const SWITCH = mac ? 'mod+shift+u' : 'mod+alt+u';
const PALETTE = mac ? 'mod+shift+k' : 'mod+alt+k';
const MENU = { key: 'profile-switcher' };
const tag = runner.runId.replace(/\D/g, '').slice(-6);
const WORK = `harness-work-${tag}`;
const STUDIO = `harness-studio-${tag}`;

const profiles = (includes) => driver.state('profiles', includes ? { includes, timeoutMs: 15_000 } : {});
const named = (state, name) => state.all.find((p) => p.name === name) ?? null;
const selectedIs = async (id) => {
  const state = await profiles(id);
  runner.assert(state.selected === id, 'the window shows the profile', { id, state });
  return state;
};
const openSwitcher = async () => {
  await driver.pressKey(SWITCH);
  await driver.waitFor(MENU);
};
const clearField = async (key) => {
  const field = (await driver.tree());
  const find = (node) => node.key === key ? node : (node.children ?? []).map(find).find(Boolean);
  await driver.pressKey('End');
  for (let i = 0; i < [...(find(field)?.value ?? '')].length; i += 1) await driver.pressKey('Backspace');
};

try {
  const frontmostBefore = mac ? await driver.frontmostBundleId() : null;
  let home;
  await runner.step('launch_app', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    const state = await profiles();
    for (const leftover of state.all.filter((p) => p.name.startsWith('harness-'))) {
      await observer.profileCommand('profile_delete', { profile_id: leftover.id, expected_revision: leftover.revision }).catch(() => {});
    }
    home = (await profiles()).all.find((p) => p.id === state.selected && !p.name.startsWith('harness-')) ?? (await profiles()).all.find((p) => !p.name.startsWith('harness-'));
    runner.assert(home, 'a profile that is not a harness leftover is on screen', state);
  });

  let work;
  await runner.step('n_creates_a_profile_and_switches_to_it', async () => {
    await openSwitcher();
    await driver.pressKey('n');
    await driver.waitFor({ key: 'profile-switcher.name' });
    await driver.typeInto({ key: 'profile-switcher.name' }, WORK);
    await driver.pressKey('Enter');
    await driver.waitFor(MENU, { gone: true });
    work = named(await profiles(WORK), WORK);
    await selectedIs(work.id);
    const pill = await driver.waitFor({ key: 'titlebar.profile' });
    runner.assert(pill.name?.includes(WORK), 'the title bar pill names the new profile', pill);
    runner.registerCleanup('delete_work_profile', async () => {
      const left = (await profiles().catch(() => ({ all: [] }))).all.find((p) => p.id === work.id);
      if (left) await observer.profileCommand('profile_delete', { profile_id: left.id, expected_revision: left.revision }).catch(() => {});
    });
    await driver.screenshot(path.join(runner.runDir, 'native-profile-created.png'));
  });

  const sessionId = crypto.randomUUID();
  await runner.step('an_agent_started_in_the_new_profile_keeps_it', async () => {
    const cwd = path.join(runner.sessionDir, 'work');
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === sessionId && m, 'shell spawned', 30_000);
    observer.send({ cmd: 'spawn_session', id: sessionId, agent: 'shell', cwd, profile_id: work.id, placement: {}, label: `work-${tag}`, cols: 80, rows: 24 });
    runner.assert((await spawned).success, 'the shell starts in the new profile');
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(sessionId));
    await driver.waitFor({ key: `sidebar.session.${sessionId}` });
    const sessions = await driver.state('sessions', { includes: sessionId });
    runner.assert(sessions.find((s) => s.id === sessionId)?.profile_id === work.id, 'the agent belongs to the profile it started in', sessions);
  });

  await runner.step('the_pill_and_the_palette_switch_profiles', async () => {
    await driver.click({ key: 'titlebar.profile' });
    await driver.waitFor(MENU);
    await driver.click({ key: `profile-switcher.choose.${home.id}` });
    await driver.waitFor(MENU, { gone: true });
    await selectedIs(home.id);
    await driver.waitFor({ key: `sidebar.session.${sessionId}` }, { gone: true });
    await driver.pressKey(PALETTE);
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, `Switch to ${WORK}`);
    await driver.waitFor({ key: `palette.row.command:profile-${work.id}` });
    await driver.pressKey('Enter');
    await selectedIs(work.id);
    await driver.waitFor({ key: `sidebar.session.${sessionId}` });
  });

  await runner.step('r_renames_the_profile', async () => {
    await openSwitcher();
    await driver.pressKey('r');
    const field = await driver.waitFor({ key: 'profile-switcher.name' });
    runner.assert(field.name === `Rename ${WORK}`, 'R opens the rename field for the current profile', field);
    await clearField('profile-switcher.name');
    await driver.typeInto(null, STUDIO);
    await driver.pressKey('Enter');
    const renamed = named(await profiles(STUDIO), STUDIO);
    runner.assert(renamed?.id === work.id, 'the rename keeps the profile', renamed);
    const row = await driver.waitFor({ key: `profile-switcher.choose.${work.id}` });
    runner.assert(row.name === STUDIO, 'the switcher lists the new name', row);
    await driver.pressKey('Escape');
    await driver.waitFor(MENU, { gone: true });
  });

  await runner.step('delete_is_refused_while_the_profile_holds_the_agent', async () => {
    await openSwitcher();
    await driver.pressKey('Backspace');
    const ask = await driver.waitFor({ key: 'profile-switcher.delete.ask' });
    runner.assert(ask.name === `Delete ${STUDIO}? Clean up its agents, crew, automations and tiles first.`, 'delete asks to clean up first', ask);
    await driver.pressKey('Enter');
    const refusal = await driver.waitFor({ key: 'profile-switcher.refusal' });
    runner.assert(refusal.name?.startsWith(`profile "${STUDIO}" is not empty:`), 'the daemon refuses a profile that holds work', refusal);
    await driver.screenshot(path.join(runner.runDir, 'native-profile-refused.png'));
    await driver.pressKey('Escape');
    await driver.pressKey('Escape');
    await driver.waitFor(MENU, { gone: true });
  });

  await runner.step('an_empty_profile_deletes_and_the_window_moves_back', async () => {
    observer.unregisterSession(sessionId);
    await driver.waitFor({ key: `sidebar.session.${sessionId}` }, { gone: true });
    await openSwitcher();
    await driver.pressKey('Backspace');
    await driver.waitFor({ key: 'profile-switcher.delete.ask' });
    await driver.pressKey('Enter');
    await selectedIs(home.id);
    const state = await profiles();
    runner.assert(!state.all.some((p) => p.id === work.id), 'the profile is gone', state);
    await driver.pressKey('Escape');
    await driver.waitFor(MENU, { gone: true });
  });

  await runner.step('the_last_profile_says_why_it_stays', async () => {
    const state = await profiles();
    if (state.all.length !== 1) {
      runner.log('skip_last_profile', { reason: 'the instance holds other profiles', profiles: state.all.map((p) => p.name) });
      return;
    }
    await openSwitcher();
    await driver.pressKey('Backspace');
    const refusal = await driver.waitFor({ key: 'profile-switcher.refusal' });
    runner.assert(refusal.name === `${home.name} is the last profile, and attn always keeps one.`, 'the last profile says why it stays', refusal);
    await driver.pressKey('Escape');
    await driver.waitFor(MENU, { gone: true });
  });

  const frontmostAfter = mac ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ work: work.id, home: home.id, frontmostBefore, frontmostAfter });
} catch (error) {
  await driver.screenshot(path.join(runner.runDir, 'failure.png')).catch(() => {});
  await runner.finishFailure(error);
  process.exitCode = 1;
}
