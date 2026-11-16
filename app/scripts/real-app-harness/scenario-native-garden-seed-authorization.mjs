#!/usr/bin/env node

import crypto from 'node:crypto';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { MOCK_AGENT_MODEL, writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-GARDEN-SEED-AUTHORIZATION', tier: 'tier2-local-mock-agent', prefix: 'native-garden-seed-authorization',
  metadata: { focus: 'The user sets and clears a tended seed’s authorization from the keyboard in its native seed tile, and the tending agent is typed each change' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));

const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const EMPTY = 'No authorization from the user for this task.';

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

async function typedInto(agent, words) {
  const desktops = await driver.state('desktops');
  const pane = desktops.flatMap((d) => d.panes ?? []).find((p) => p.session_id === agent);
  await driver.state(`terminal:${pane.runtime_id}`, { includes: words, timeoutMs: 30_000 });
}

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  let desktop;
  await runner.step('make_a_desktop', async () => {
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
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const cwd = path.join(runner.sessionDir, 'authorization');
  let dispatcher;
  let agent;
  let seedId;
  let tile;
  await runner.step('delegate_a_tended_seed_and_open_it_beside_the_agent', async () => {
    writeMockAgentFixture(cwd, { name: 'Search endpoint', turns: [] });
    seedId = JSON.parse(attn('seed', 'plant', `Search endpoint ${runner.runId}`, '-m', 'Build the search endpoint.', '--profile', observer.profileId, '--json')).id;
    runner.registerCleanup('wither_seed', () => attn('seed', 'wither', seedId, '-m', 'Scenario complete', '--profile', observer.profileId, '--force'));
    dispatcher = crypto.randomUUID();
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === dispatcher && m, 'dispatcher spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: dispatcher, agent: 'codex', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'Dispatcher', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session starts the dispatcher');
    runner.registerCleanup('unregister_dispatcher', () => observer.unregisterSession(dispatcher));
    const reply = await observer.requestResult({
      cmd: 'delegate', cwd, source_session_id: dispatcher, agent: 'codex', model: MOCK_AGENT_MODEL, label: 'Search endpoint',
      assignment: { kind: 'seed', seed_id: seedId },
    }, 'delegate_result', 60_000);
    agent = reply.result?.session_id;
    runner.assert(Boolean(agent), 'the delegate tends the seed', reply);
    runner.registerCleanup('unregister_agent', () => observer.unregisterSession(agent));
    attn('open', seedId, '--session', agent);
    const shown = await observer.waitForDesktopOf(agent, (d) => seedTiles(d).some((t) => t.tile_params === seedId) && d, 'the seed opens as a tile beside the agent');
    [tile] = seedTiles(shown).filter((t) => t.tile_params === seedId);
    runner.registerCleanup('remove_tile', () => observer.profileCommand('desktop_remove_leaf', { desktop_id: shown.id, leaf_id: tile.tile_id }).catch(() => {}));
    if (await driver.find({ key: `shelf.chip.${tile.tile_id}` })) await driver.click({ key: `shelf.chip.${tile.tile_id}` });
    await driver.waitFor({ key: `seed-tile.${tile.tile_id}.authorization.empty`, name: EMPTY });
  });

  const part = (name) => ({ key: `seed-tile.${tile.tile_id}.authorization.${name}` });
  await runner.step('set_the_words_from_the_keyboard', async () => {
    await driver.click(part('edit'));
    await driver.waitFor(part('field'));
    await driver.typeInto(part('field'), 'You may land on main.');
    await driver.pressKey('shift+Enter');
    await driver.typeInto(part('field'), 'Ask me before releasing.');
    await driver.pressKey('Enter');
    await driver.waitFor(part('field'), { gone: true });
    await driver.waitFor({ ...part('edit'), name: 'Change' });
    const stored = JSON.parse(attn('seed', 'show', seedId, '--json')).seed.authorization;
    runner.assert(stored?.text === 'You may land on main.\nAsk me before releasing.', 'the daemon keeps both lines', stored);
    await typedInto(agent, `The user changed the authorization for seed ${seedId}`);
    await driver.screenshot(path.join(runner.runDir, 'native-authorization-set.png'));
    runner.writeText('seed-show-after-set.txt', attn('seed', 'show', seedId));
  });

  await runner.step('escape_in_the_field_sends_nothing', async () => {
    await driver.click(part('edit'));
    await driver.waitFor(part('field'));
    await driver.pressKey('Escape');
    await driver.waitFor(part('field'), { gone: true });
    const stored = JSON.parse(attn('seed', 'show', seedId, '--json')).seed.authorization;
    runner.assert(stored?.text === 'You may land on main.\nAsk me before releasing.', 'Escape kept the words', stored);
  });

  await runner.step('clear_the_words', async () => {
    await driver.click(part('clear'));
    await driver.waitFor({ ...part('empty'), name: EMPTY });
    await driver.waitFor({ ...part('edit'), name: 'Set' });
    await typedInto(agent, 'The user removed the authorization');
    runner.writeText('seed-notes.txt', attn('seed', 'notes', seedId, '--kind', 'authorization'));
  });

  await runner.finishSuccess({ desktopId: desktop.id, agent, seedId });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
