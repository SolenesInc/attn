#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { closeScenarioSessions, createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  if (args.includes('--help')) { printCommonHelp('scenario-garden-profile-scope'); return; }
  const options = parseCommonArgs(args);
  const instance = currentHarnessInstance();
  if (!instance) throw new Error('Garden profile verification requires a named instance.');
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenProfileScope', tier: 'tier1-local-shell', prefix: 'garden-profile-scope',
    metadata: { instance, focus: 'Garden rows, search and empty state belong to the selected profile' },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const sessions = [];
  const created = [];
  const seeds = [];
  let home;
  const runAttn = async (argv, session = '') => {
    const result = await execFileAsync(appDaemonInTree(options.appPath), argv, {
      encoding: 'utf8', maxBuffer: Infinity, env: instanceCliEnv(instance, { ATTN_TERMINAL_ID: session ? observer.terminalOf(session) : '' }),
    });
    return result.stdout;
  };
  // Match the existing profile-lifecycle scenario's DOM deadline.
  const waitDom = (payload) => client.request('dom_wait', { timeoutMs: 5_000, ...payload });
  const state = async () => (await client.request('get_state')).arrangement;
  const openSwitcher = async () => {
    await pressShortcutKeys(client, driver, 'profile.switch');
    await waitDom({ selector: '.profile-switcher' });
  };
  const createProfile = async (name) => {
    await openSwitcher();
    await client.request('dom_key', { selector: '.profile-switcher', key: 'n' });
    await waitDom({ selector: '#profile-switcher-name' });
    await client.request('dom_type', { selector: '#profile-switcher-name', text: name });
    await client.request('dom_click', { selector: '.profile-switcher-form button[type="submit"]' });
    await waitDom({ selector: '.profile-switcher', absent: true });
    await openSwitcher();
    await waitDom({ selector: '.profile-switcher-item:has([aria-label="Current profile"]) .profile-switcher-name', textIncludes: name });
    await client.request('dom_key', { selector: '.profile-switcher', key: 'Escape' });
    const current = await state();
    const profile = current.profiles.find((p) => p.name === name);
    runner.assert(profile?.id === current.selectedProfileId, 'creating a profile selects it', current);
    created.push(profile.id);
    return profile;
  };
  const selectProfile = async (id) => {
    await openSwitcher();
    const current = await state();
    const destination = current.profiles.find((profile) => profile.id === id);
    const labels = await Promise.all(current.profiles.map((_, index) => client.request('dom_text', {
      selector: `.profile-switcher-item:nth-of-type(${index + 1}) .profile-switcher-name`,
    })));
    const index = labels.findIndex((label) => label.text === destination?.name);
    runner.assert(index >= 0, 'the destination profile is in the switcher', current);
    await client.request('dom_click', { selector: `.profile-switcher-item:nth-of-type(${index + 1}) .profile-switcher-choose` });
    await waitDom({ selector: '.profile-switcher', absent: true });
    await openSwitcher();
    await waitDom({ selector: '.profile-switcher-item:has([aria-label="Current profile"]) .profile-switcher-name', textIncludes: destination.name });
    await client.request('dom_key', { selector: '.profile-switcher', key: 'Escape' });
    runner.assert((await state()).selectedProfileId === id, 'switching selects the destination');
  };

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('remove_synthetic_work', async () => {
    await Promise.all(seeds.map((seed) => runAttn(['seed', 'wither', seed.id, '--profile', seed.profile, '-m', 'Scenario complete'])));
    await closeScenarioSessions(client, sessions);
    if (home) {
      const profiles = (await state()).profiles;
      await Promise.all(created.map((id) => {
        const profile = profiles.find((p) => p.id === id);
        return profile && observer.profileCommand('profile_delete', { profile_id: id, expected_revision: profile.revision});
      }));
    }
  });

  try {
    await launchFreshAppAndConnect(client, observer);
    const initialBounds = await client.request('get_window_bounds');
    runner.writeJson('window-before-placement.json', initialBounds);
    await client.request('set_window_bounds', { logicalBounds: { ...initialBounds.logicalBounds, x: 0, y: 0 } });
    home = (await state()).selectedProfileId;
    const tag = runner.runId.replace(/\D/g, '').slice(-6);
    const homeAgent = await runner.step('start_an_agent_in_the_original_profile', async () => {
      const cwd = path.join(runner.sessionDir, 'original-profile');
      fs.mkdirSync(cwd, { recursive: true });
      writeMockAgentFixture(cwd, { resumable: true, name: 'Original profile agent', turns: [] });
      const agent = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `original-profile-${tag}`, agent: 'codex' });
      sessions.push(agent);
      return agent;
    });
    const work = await runner.step('create_a_profile_and_plant_from_its_agent', async () => {
      const profile = await createProfile(`harness-garden-${tag}`);
      const cwd = path.join(runner.sessionDir, 'garden-profile');
      fs.mkdirSync(cwd, { recursive: true });
      writeMockAgentFixture(cwd, { resumable: true, name: 'Garden profile agent', turns: [] });
      const agent = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `garden-profile-${tag}`, agent: 'codex', ownDesktop: false });
      sessions.push(agent);
      const seed = JSON.parse(await runAttn(['seed', 'plant', `Only this profile ${tag}`, '-m', 'Check immutable Garden ownership.', '--json'], agent));
      runner.assert(seed.profile_id === profile.id, 'a seed belongs to its planter profile', seed);
      seeds.push({ id: seed.id, profile: profile.id });
      await pressShortcutKeys(client, driver, 'board.open');
      await waitDom({ selector: `[data-seed-row="${seed.id}"]` });
      const [{ bounds: row }, { bounds: viewport }] = await Promise.all([
        client.request('dom_bounds', { selector: `[data-seed-row="${seed.id}"]` }),
        client.request('dom_bounds', { selector: '.garden-viewport' }),
      ]);
      runner.assert(row.width > 0 && row.height > 0 && row.y < viewport.y + viewport.height && row.y + row.height > viewport.y,
        'the synthetic seed has visible bounds in the Garden', { row, viewport });
      runner.writeJson('populated-garden-bounds.json', { row, viewport });
      if (process.env.ATTN_HARNESS_RECORD === '1') {
        await captureFrontWindowScreenshot(path.join(runner.runDir, 'populated-garden-native.png'), { client, driver });
        const shot = await client.request('capture_screenshot_data', { selector: '.garden-panel' });
        fs.writeFileSync(path.join(runner.runDir, 'populated-garden-dom.png'), Buffer.from(shot.pngBase64, 'base64'));
        await captureFrontWindowScreenshot(path.join(runner.runDir, 'populated-garden-after-dom.png'), { client, driver });
      }
      runner.writeJson('populated-garden.json', await client.request('garden_get_state'));
      await client.request('garden_search', { query: tag });
      return { profile, seed, agent };
    });

    await runner.step('the_original_profile_cannot_see_or_claim_the_new_seed', async () => {
      await selectProfile(home);
      await waitDom({ selector: `[data-seed-row="${work.seed.id}"]`, absent: true });
      const garden = await client.request('garden_get_state');
      runner.assert(garden.present && !garden.seeds.some((seed) => seed.id === work.seed.id), 'the original profile panel excludes new-profile work');
      const rows = await Promise.all(['ls', 'ready', 'search'].map(async (verb) => {
        const extra = verb === 'search' ? [tag] : [];
        const result = await runAttn(['seed', verb, ...extra, '--json'], homeAgent);
        runner.assert(!result.includes(work.seed.id), `the original profile ${verb} excludes the new seed`);
        return { verb, visible: JSON.parse(result) };
      }));
      const [{ visible: listed }] = rows;
      runner.assert(listed.seeds.every((seed) => seed.profile_id === home), 'every listed original seed has its original profile');
      runner.writeJson('original-profile-receipt.json', { profile: home, total: listed.total, returned: listed.seeds.length, foreignSeeds: 0 });
      let refusal = '';
      try { await runAttn(['seed', 'tend', work.seed.id], homeAgent); } catch (error) { refusal = error.stderr || error.message; }
      const original = (await state()).profiles.find((profile) => profile.id === home);
      runner.assert(refusal.includes(work.profile.name) && refusal.includes(original.name), 'the original agent claim names both profiles', refusal);
      await selectProfile(work.profile.id);
      await waitDom({ selector: `[data-seed-row="${work.seed.id}"]` });
    });

    const empty = await runner.step('switch_to_an_empty_garden_and_clear_search', async () => {
      const profile = await createProfile(`harness-empty-${tag}`);
      await waitDom({ selector: `[data-seed-row="${work.seed.id}"]`, absent: true });
      await waitDom({ selector: '.garden-empty' });
      const garden = await client.request('garden_get_state');
      runner.assert(garden.present && garden.seeds.length === 0 && garden.search.query === '', 'the empty profile has no former rows or query', garden);
      runner.writeJson('empty-garden.json', garden);
      await Promise.all(['ls', 'ready', 'search'].map(async (verb) => {
        const extra = verb === 'search' ? [tag] : [];
        const result = await runAttn(['seed', verb, ...extra, '--profile', profile.id, '--json']);
        runner.assert(!result.includes(work.seed.id), `${verb} stays in the selected profile`, result);
      }));
      let refusal = '';
      try { await runAttn(['seed', 'tend', work.seed.id, '--profile', profile.id]); } catch (error) { refusal = error.stderr || error.message; }
      runner.assert(refusal.includes(work.profile.name) && refusal.includes(profile.name), 'a cross-profile claim names both profiles', refusal);
      runner.writeText('cross-profile-refusal.txt', refusal);
      return profile;
    });

    await runner.step('switch_back_then_delete_the_selected_empty_profile', async () => {
      await selectProfile(work.profile.id);
      await waitDom({ selector: `[data-seed-row="${work.seed.id}"]` });
      await selectProfile(empty.id);
      await waitDom({ selector: '.garden-empty' });
      const current = (await state()).profiles.find((p) => p.id === empty.id);
      await observer.profileCommand('profile_delete', { profile_id: empty.id, expected_revision: current.revision});
      await waitDom({ selector: `[data-seed-row="${work.seed.id}"]` });
      runner.assert((await state()).selectedProfileId === work.profile.id, 'deleting the selected profile falls back with its Garden');
      runner.writeJson('fallback-garden.json', await client.request('garden_get_state'));
    });
    console.log(JSON.stringify(await runner.finishSuccess({ seed: work.seed.id, profile: work.profile.id }), null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error);
    console.error(summary.error);
    process.exitCode = 1;
  }
}

main().catch((error) => { console.error(error.stack || String(error)); process.exitCode = 1; });
