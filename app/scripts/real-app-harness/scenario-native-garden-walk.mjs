#!/usr/bin/env node

import crypto from 'node:crypto';
import path from 'node:path';
import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-GARDEN-WALK', tier: 'tier1-local-shell', prefix: 'native-garden-walk',
  metadata: { focus: 'The native garden finds a plot, walks it in the dock and in columns, keeps the walk through Escape, walks it again in a seed tile, and shows only the selected profile’s seeds' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));

const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const tag = runner.runId;

function plant(profile, title, parent) {
  const planted = JSON.parse(attn('seed', 'plant', title, '-m', `Planted by ${tag}.`, '--profile', profile, '--json', ...(parent ? ['--part-of', parent] : [])));
  runner.registerCleanup(`wither_${planted.id}`, () => attn('seed', 'wither', planted.id, '-m', 'Scenario complete', '--profile', profile));
  return planted.id;
}

function seedTiles(desktop) {
  const tiles = [];
  const walk = (node) => {
    if (!node) return;
    if (node.tile_kind === 'seed') tiles.push(node);
    for (const child of node.children ?? []) walk(child);
  };
  walk(JSON.parse(desktop?.tree_json || 'null'));
  return tiles;
}

const GARDEN = { role: 'region', name: 'The garden' };
const FULL = { role: 'dialog', name: 'The garden' };
const DOCKED = { key: 'garden.frame-button', name: 'Expand the garden' };
const heading = (title) => ({ key: 'garden.head.title', name: title });

async function ask(query) {
  await driver.click({ key: 'garden.search.field' });
  await driver.pressKey('mod+a');
  if (query) await driver.typeInto({ key: 'garden.search.field' }, query);
  else await driver.pressKey('Backspace');
}

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_two_columns', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  let desktop;
  let shell;
  await runner.step('a_desktop_with_a_shell', async () => {
    const before = observer.currentDesktopId();
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', async () => {
      for (;;) {
        const left = observer.desktop(desktop.id);
        if (!left) return;
        const moved = observer.waitForMessage((m) => m.event === 'profile_arrangement_changed' && m, 'the desktop moves on', 10_000);
        try {
          await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
          moved.catch(() => {});
          return;
        } catch (error) {
          if (!/stale_revision/.test(error.message)) throw error;
          await moved;
        }
      }
    });
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    const cwd = path.join(runner.sessionDir, 'walk');
    fs.mkdirSync(cwd, { recursive: true });
    shell = crypto.randomUUID();
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === shell && m, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id: shell, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'walk', cols: 80, rows: 24 });
    runner.assert((await spawned).success, 'spawn_session starts the shell');
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(shell));
  });

  const home = observer.profileId;
  const plot = { title: `Walk drive ${tag}` };
  const inner = { title: `Inner plot ${tag}` };
  const nested = { title: `Nested work ${tag}` };
  const sibling = { title: `Sibling work ${tag}` };
  let elsewhere;
  let work;
  await runner.step('plant_a_plot_here_and_a_seed_in_another_profile', async () => {
    plot.id = plant(home, plot.title);
    inner.id = plant(home, inner.title, plot.id);
    nested.id = plant(home, nested.title, inner.id);
    sibling.id = plant(home, sibling.title, plot.id);
    elsewhere = (await observer.profileCommand('profile_create', { name: `Garden walk ${tag}` })).profile;
    runner.registerCleanup('delete_profile', async () => {
      const left = (await driver.state('profiles')).all.find((p) => p.id === elsewhere.id);
      if (left) await observer.profileCommand('profile_delete', { profile_id: left.id, expected_revision: left.revision });
    });
    work = { title: `Only this profile ${tag}`, id: plant(elsewhere.id, `Only this profile ${tag}`) };
  });

  await runner.step('search_finds_the_plot_in_this_profile_only', async () => {
    await driver.run('board.open');
    await driver.waitFor(GARDEN);
    if (await driver.find(FULL)) await driver.click({ key: 'garden.frame-button' });
    await driver.waitFor(DOCKED);
    await ask(tag);
    await driver.waitFor({ key: `garden.row.${nested.id}` });
    runner.assert(!(await driver.find({ key: `garden.row.${work.id}` })), 'another profile’s seed is not an answer');
    await ask('');
  });

  await runner.step('the_dock_walks_into_the_plot_and_escape_keeps_the_walk', async () => {
    await driver.click({ key: `garden.row.${plot.id}` });
    await driver.waitFor(heading(plot.title));
    await driver.waitFor({ key: `garden.row.${inner.id}` });
    await driver.pressKey('Escape');
    await driver.waitFor(GARDEN, { gone: true });
    await driver.run('board.open');
    await driver.waitFor(heading(plot.title));
  });

  await runner.step('expanded_the_walk_lays_out_in_columns', async () => {
    await driver.click({ key: `garden.row.${inner.id}` });
    await driver.waitFor(heading(inner.title));
    runner.registerCleanup('dock_the_garden', async () => {
      if (!(await driver.find(GARDEN)) && !(await driver.find(FULL))) await driver.run('board.open');
      if (await driver.find(FULL)) await driver.click({ key: 'garden.frame-button' });
      await driver.waitFor(DOCKED);
      await driver.pressKey('Escape');
      await driver.waitFor(GARDEN, { gone: true });
    });
    await driver.click({ key: 'garden.frame-button' });
    await driver.waitFor(FULL);
    await driver.waitFor({ key: `garden.row.${sibling.id}` });
    await driver.waitFor({ key: `garden.row.${nested.id}` });
    await driver.screenshot(path.join(runner.runDir, 'native-garden-columns.png'));
    await driver.click({ key: 'garden.frame-button' });
    await driver.waitFor(DOCKED);
    runner.assert(!(await driver.find({ key: `garden.row.${sibling.id}` })), 'the dock shows one level at a time');
  });

  let tile;
  await runner.step('the_plot_opens_as_a_tile_and_closes_the_garden', async () => {
    await driver.click({ key: 'garden.trail.1' });
    await driver.waitFor(heading(plot.title));
    await driver.click({ key: 'garden.open-tile' });
    await driver.waitFor(GARDEN, { gone: true });
    const shown = await observer.waitForDesktopOf(shell, (d) => seedTiles(d).some((t) => t.tile_params === plot.id) && d, 'the plot opens as a seed tile');
    [tile] = seedTiles(shown).filter((t) => t.tile_params === plot.id);
    runner.registerCleanup('remove_tile', () => observer.profileCommand('desktop_remove_leaf', { desktop_id: desktop.id, leaf_id: tile.tile_id }).catch(() => {}));
    await driver.waitFor({ key: `seed-tile.${tile.tile_id}.head.title`, name: plot.title });
  });

  await runner.step('the_tile_walks_into_a_child_and_escape_climbs_back', async () => {
    await driver.click({ key: `seed-tile.${tile.tile_id}.plot.${inner.id}` });
    await observer.waitForDesktopOf(shell, (d) => seedTiles(d).some((t) => t.tile_id === tile.tile_id && t.tile_params === inner.id) && d, 'the tile walks to the child');
    await driver.waitFor({ key: `seed-tile.${tile.tile_id}.head.title`, name: inner.title });
    await driver.screenshot(path.join(runner.runDir, 'native-garden-seed-tile.png'));
    await driver.pressKey('Escape');
    await observer.waitForDesktopOf(shell, (d) => seedTiles(d).some((t) => t.tile_id === tile.tile_id && t.tile_params === plot.id) && d, 'Escape climbs back to the plot');
    await driver.waitFor({ key: `seed-tile.${tile.tile_id}.head.title`, name: plot.title });
    await observer.profileCommand('desktop_remove_leaf', { desktop_id: desktop.id, leaf_id: tile.tile_id });
    await driver.waitFor({ key: `tile.${tile.tile_id}` }, { gone: true });
  });

  await runner.step('another_profile_shows_its_own_garden', async () => {
    await driver.click({ key: 'titlebar.profile' });
    await driver.click({ key: `profile-switcher.choose.${elsewhere.id}` });
    await driver.run('board.open');
    await driver.waitFor({ key: `garden.row.${work.id}` });
    runner.assert(!(await driver.find({ key: `garden.row.${plot.id}` })), 'the first profile’s plot stays out');
    await driver.screenshot(path.join(runner.runDir, 'native-garden-other-profile.png'));
    await driver.pressKey('Escape');
    await driver.click({ key: 'titlebar.profile' });
    await driver.click({ key: `profile-switcher.choose.${home}` });
  });

  await runner.finishSuccess({ desktopId: desktop.id, plot: plot.id, profile: elsewhere.id });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
