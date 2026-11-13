#!/usr/bin/env node

import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-MARKDOWN-OPENER', tier: 'tier1-local-shell', prefix: 'native-markdown-opener',
  metadata: { focus: 'The native markdown opener finds tracked and untracked files by a fuzzy query and docks each, then lists them as recents, the later first, and a recent reuses its tile' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

function markdownTiles(desktop) {
  const tiles = [];
  const walk = (node) => {
    if (!node) return;
    if (node.tile_kind === 'markdown') tiles.push(node);
    for (const child of node.children ?? []) walk(child);
  };
  walk(JSON.parse(desktop?.tree_json || 'null'));
  return tiles;
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_three_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1900, height: 1000 }));

  let desktop;
  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => {
      const left = observer.desktop(desktop.id);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const alpha = `alpha-plan-${runner.runId}.md`;
  const beta = `beta-notes-${runner.runId}.md`;
  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, 'opener-ws');
  await runner.step('a_shell_in_a_repository', async () => {
    fs.mkdirSync(path.join(cwd, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(cwd, 'docs', alpha), '# Alpha plan\n');
    fs.writeFileSync(path.join(cwd, 'docs', beta), '# Beta notes\n');
    const git = (...args) => execFileSync('git', args, { cwd, stdio: 'pipe' });
    git('init');
    git('-c', 'user.email=harness@example.com', '-c', 'user.name=Harness', 'add', path.join('docs', alpha));
    git('-c', 'user.email=harness@example.com', '-c', 'user.name=Harness', 'commit', '-m', 'seed');
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'opener', cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
    const desktops = await driver.state('desktops');
    const pane = desktops.flatMap((d) => d.panes ?? []).find((p) => p.session_id === id);
    await driver.click({ key: `terminal.${pane.runtime_id}` });
  });

  const row = (file) => ({ key: `opener.row.${path.join(cwd, 'docs', file)}` });
  const summon = async (query) => {
    await driver.run('file.open');
    await driver.waitFor({ key: 'opener.field' });
    if (query) await driver.typeInto({ key: 'opener.field' }, query);
  };
  const docked = (count, description) => observer.waitForDesktopOf(id, (entry) => markdownTiles(entry).length === count && entry, description);

  let betaTile;
  await runner.step('a_fuzzy_query_opens_an_untracked_file', async () => {
    await summon('');
    await driver.waitFor(row(beta), { gone: true });
    await driver.typeInto({ key: 'opener.field' }, 'betanotes');
    await driver.click(row(beta));
    await driver.waitFor({ key: 'opener' }, { gone: true });
    const entry = await docked(1, 'the pick docks a markdown tile');
    [betaTile] = markdownTiles(entry);
    runner.assert(betaTile.tile_params.endsWith(beta) && betaTile.tile_session_id === id, 'the tile shows the picked file for the shell', betaTile);
  });

  await runner.step('a_fuzzy_query_opens_a_tracked_file', async () => {
    await summon('alphaplan');
    await driver.click(row(alpha));
    await docked(2, 'the second pick docks a second tile');
  });

  await runner.step('recents_list_the_later_open_first_and_reuse_its_tile', async () => {
    await summon('');
    const [alphaRow, betaRow] = [await driver.waitFor(row(alpha)), await driver.waitFor(row(beta))];
    runner.assert(alphaRow.rect[1] < betaRow.rect[1], 'the later open lists first', { alphaRow, betaRow });
    await driver.screenshot(path.join(runner.runDir, 'native-opener-recents.png'));
    await driver.click(row(beta));
    const entry = await observer.waitForDesktopOf(id, (e) => e.active_pane_id === betaTile.tile_id && e, 'the reused tile becomes active');
    runner.assert(markdownTiles(entry).length === 2, 'a recent reuses its tile', entry.tree_json);
  });

  await runner.step('leave_no_tile_behind', async () => {
    for (const tile of markdownTiles(observer.desktop(desktop.id))) await observer.profileCommand('desktop_remove_leaf', { desktop_id: desktop.id, leaf_id: tile.tile_id });
    await driver.waitFor({ key: `tile.${betaTile.tile_id}` }, { gone: true });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, sessionId: id, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
