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
  scenarioId: 'NATIVE-GARDEN-SEED-HEADER', tier: 'tier2-local-mock-agent', prefix: 'native-garden-seed-header',
  metadata: { focus: 'A delegate’s header names the seed it reports to through all five states, its card shows the latest note and the outcome, and the chip opens the seed beside the agent' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));

const BRIEF = `Give the garden a little life ${runner.runId}`;
const FIRST_NOTE = 'The silhouettes work at 24px.';
const LATER_NOTE = 'A new note arrived while the card stayed open.';
const OUTCOME = 'All five garden states are clear in the agent header.';

const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });

function textsUnder(node, key, inside = false, found = []) {
  if (!node) return found;
  const here = inside || node.key === key;
  if (here && node.name) found.push(node.name);
  for (const child of node.children ?? []) textsUnder(child, key, here, found);
  return found;
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

async function cardSays(card, words) {
  await driver.waitFor({ role: 'text', name: words });
  const texts = textsUnder(await driver.tree(), card);
  runner.assert(texts.includes(words), `the card shows ${words}`, texts);
  return texts;
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

  const cwd = path.join(runner.sessionDir, 'garden-header');
  let dispatcher;
  let agent;
  let seedId;
  let pane;
  await runner.step('delegate_an_agent_that_reports_to_a_seed', async () => {
    writeMockAgentFixture(cwd, { name: 'Garden designer', turns: [] });
    dispatcher = crypto.randomUUID();
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === dispatcher && m, 'dispatcher spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: dispatcher, agent: 'codex', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'Dispatcher', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session starts the dispatcher');
    runner.registerCleanup('unregister_dispatcher', () => observer.unregisterSession(dispatcher));
    const reply = await observer.requestResult({
      cmd: 'delegate', cwd, source_session_id: dispatcher, agent: 'codex', model: MOCK_AGENT_MODEL, label: 'Garden designer',
      assignment: { kind: 'new', brief: BRIEF },
    }, 'delegate_result', 60_000);
    agent = reply.result?.session_id;
    seedId = reply.result?.seed_id;
    runner.assert(Boolean(agent && seedId), 'the delegate starts on a seed of its own', reply);
    runner.registerCleanup('wither_seed', () => {
      if (JSON.parse(attn('seed', 'show', seedId, '--json')).seed?.status !== 'withered') attn('seed', 'wither', seedId, '-m', 'Scenario complete', '--profile', observer.profileId, '--force');
    });
    runner.registerCleanup('unregister_agent', () => observer.unregisterSession(agent));
    const placed = await observer.waitForDesktopOf(agent, (d) => d.panes.some((p) => p.session_id === agent) && d, 'the delegate is placed');
    pane = placed.panes.find((p) => p.session_id === agent).pane_id;
    attn('seed', 'note', seedId, '-m', FIRST_NOTE, '--session', agent);
  });

  const chip = () => `tile.${pane}.seed`;
  const card = () => `${chip()}.popover`;
  const title = JSON.parse(attn('seed', 'show', seedId, '--json')).seed?.title ?? BRIEF;
  const named = (state) => ({ key: chip(), name: `${title}, ${state}. Open seed` });

  await runner.step('the_header_names_the_growing_seed_and_its_card_reads_the_latest_note', async () => {
    await driver.waitFor(named('Growing'));
    await driver.hover({ key: chip() });
    const texts = await cardSays(card(), FIRST_NOTE);
    runner.assert(texts.some((t) => t.includes('This agent reports to this seed.')), 'the card says how the agent relates to the seed', texts);
    attn('seed', 'note', seedId, '-m', LATER_NOTE, '--session', agent);
    await cardSays(card(), LATER_NOTE);
    await driver.screenshot(path.join(runner.runDir, 'growing-card.png'));
    await driver.hover({ key: 'sidebar' }, { allowMiss: true });
    await driver.waitFor({ key: card() }, { gone: true });
  });

  for (const [state, move] of [
    ['Parked', ['park', seedId]],
    ['Growing', ['tend', seedId]],
    ['Harvested', ['harvest', seedId, '-m', OUTCOME]],
    ['Planted', ['replant', seedId]],
    ['Withered', ['wither', seedId, '-m', 'This design exploration has ended.']],
  ]) {
    await runner.step(`the_header_follows_${move[0]}`, async () => {
      attn('seed', ...move, '--session', agent);
      await driver.waitFor(named(state));
      if (state !== 'Harvested') return;
      await driver.hover({ key: chip() });
      await cardSays(card(), OUTCOME);
      await driver.screenshot(path.join(runner.runDir, 'harvested-card.png'));
      await driver.hover({ key: 'sidebar' }, { allowMiss: true });
      await driver.waitFor({ key: card() }, { gone: true });
    });
  }

  await runner.step('the_chip_opens_the_seed_beside_the_agent', async () => {
    await driver.click({ key: chip() });
    const shown = await observer.waitForDesktopOf(agent, (d) => seedTiles(d).some((t) => t.tile_params === seedId) && d, 'a seed tile opens on the desktop');
    const [tile] = seedTiles(shown).filter((t) => t.tile_params === seedId);
    await driver.waitFor({ key: `seed-tile.${tile.tile_id}.head.title`, name: title });
    await driver.screenshot(path.join(runner.runDir, 'seed-tile.png'));
    await observer.profileCommand('desktop_remove_leaf', { desktop_id: shown.id, leaf_id: tile.tile_id });
    await driver.waitFor({ key: `tile.${tile.tile_id}` }, { gone: true });
  });

  await runner.finishSuccess({ desktopId: desktop.id, agent, seedId });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
