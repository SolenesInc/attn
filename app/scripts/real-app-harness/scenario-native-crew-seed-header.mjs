#!/usr/bin/env node

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
  scenarioId: 'NATIVE-CREW-SEED-HEADER', tier: 'tier2-local-mock-agent', prefix: 'native-crew-seed-header',
  metadata: { focus: "In the native app a crew member's tile header names the seeds it claims, keeps the claim into its next session, and opens the seed from the keyboard" },
});
const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance, { ATTN_SESSION_ID: '' }) });
const json = (...args) => {
  const output = attn(...args, '--json');
  return JSON.parse(output.slice(output.indexOf('{')));
};
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));

const name = `Fern${runner.runId.replace(/[^a-z0-9]/gi, '').slice(-6)}`;
const sessions = [];
const desktops = [];
const unsettled = new Set();

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

async function wake() {
  const id = json('crew', 'start', name).session_id;
  sessions.push(id);
  const placed = await observer.waitForDesktopOf(id, (d) => d.panes.some((p) => p.session_id === id) && d, `${name}'s session is placed`, 30_000);
  const pane = placed.panes.find((p) => p.session_id === id).pane_id;
  desktops.push(placed.id);
  await driver.click({ key: `sidebar.session.${id}` });
  await driver.waitFor({ key: `tile.${pane}` });
  return { id, desktop: placed.id, pane };
}

const chip = (pane) => `tile.${pane}.seed`;
const header = (pane, label, state, does) => driver.waitFor({ key: chip(pane), name: `${label}, ${state}. ${does}` }, { timeoutMs: 20_000 });
const shot = (file) => driver.screenshot(path.join(runner.runDir, file));

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  let first;
  let second;
  let day;
  await runner.step('a_member_in_session_with_two_seeds_to_claim', async () => {
    writeMockAgentFixture(runner.sessionDir, {
      name: 'Crew header verification', minimumWorkingMs: 0, turns: [],
      defaultActions: [{ type: 'reply', text: 'Ready to verify the seed header.', state: 'idle' }],
    });
    attn('crew', 'create', name, '--profile', observer.profileId);
    runner.registerCleanup('retire_member', () => attn('crew', 'retire', name, '--profile', observer.profileId));
    attn('crew', 'set', name, '--cwd', runner.sessionDir, '--agent', 'claude', '--model', MOCK_AGENT_MODEL, '--profile', observer.profileId);
    runner.registerCleanup('end_sessions', async () => {
      for (const id of sessions.filter((s) => observer.getSession(s))) {
        attn('handoff', '--session', id, '-m', 'Crew header scenario finished.');
        await observer.waitFor(() => !observer.getSession(id), `${name}'s session to end`, 30_000);
      }
      for (const id of new Set(desktops)) {
        const left = observer.desktop(id);
        if (left && observer.desktops.length > 1) await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
      }
    });
    day = await wake();
    first = json('seed', 'plant', 'Review release notes', '--profile', observer.profileId).id;
    second = json('seed', 'plant', 'Verify upload completion', '--profile', observer.profileId).id;
    unsettled.add(first);
    unsettled.add(second);
    runner.registerCleanup('settle_seeds', () => {
      for (const id of unsettled) attn('seed', 'wither', id, '--force', '-m', 'Scenario complete', '--profile', observer.profileId);
    });
    runner.assert(!(await driver.find({ key: chip(day.pane) })), 'the header names no seed before a claim');
  });

  await runner.step('the_header_and_its_card_show_the_member_claims', async () => {
    attn('seed', 'tend', first, '--session', day.id);
    await header(day.pane, 'Review release notes', 'Growing', 'Open seed');
    await shot('one-member-claim.png');
    attn('seed', 'tend', second, '--session', day.id);
    await header(day.pane, 'tending 2', 'Growing', 'Show seeds');
    await driver.click({ key: chip(day.pane) });
    const card = `${chip(day.pane)}.popover`;
    await driver.waitFor({ key: card });
    await driver.waitFor({ key: `${card}.seed.0` });
    const tree = await driver.tree();
    const rows = [];
    const walk = (node) => {
      if (node?.key?.startsWith(`${card}.seed.`)) rows.push(node.name ?? '');
      for (const child of node?.children ?? []) walk(child);
    };
    walk(tree);
    runner.assert(rows.some((r) => r.includes('Review release notes')) && rows.some((r) => r.includes('Verify upload completion')), 'both claims are in the card', rows);
    await shot('two-member-claims.png');
    await driver.pressKey('Escape');
    await driver.waitFor({ key: card }, { gone: true });
    attn('seed', 'park', second, '--session', day.id);
    await header(day.pane, 'Review release notes', 'Growing', 'Open seed');
  });

  await runner.step('the_claim_outlasts_the_session_and_the_chip_opens_the_seed', async () => {
    attn('handoff', '--session', day.id, '-m', 'Continue checking the member claim.');
    await observer.waitFor(() => !observer.getSession(day.id), 'the first session to close', 30_000);
    const next = await wake();
    runner.assert(next.id !== day.id, 'the member starts a new session', { first: day.id, next: next.id });
    await header(next.pane, 'Review release notes', 'Growing', 'Open seed');
    await shot('member-claim-after-wake.png');
    attn('seed', 'harvest', first, '--session', next.id, '-m', 'Header behavior verified');
    unsettled.delete(first);
    await driver.waitFor({ key: chip(next.pane) }, { gone: true, timeoutMs: 20_000 });
    attn('seed', 'tend', second, '--session', next.id);
    await header(next.pane, 'Verify upload completion', 'Growing', 'Open seed');
    await driver.click({ key: chip(next.pane) });
    const shown = await observer.waitForDesktopOf(next.id, (d) => seedTiles(d).some((t) => t.tile_params === second) && d, 'the seed opens in a tile', 20_000);
    const [tile] = seedTiles(shown).filter((t) => t.tile_params === second);
    await driver.waitFor({ key: `seed-tile.${tile.tile_id}.head.title`, name: 'Verify upload completion' });
    await shot('seed-tile.png');
    await observer.profileCommand('desktop_remove_leaf', { desktop_id: shown.id, leaf_id: tile.tile_id });
  });

  await runner.finishSuccess({ member: name, sessions, seeds: [first, second] });
} catch (error) {
  await runner.finishFailure(error, { member: name, sessions });
  process.exitCode = 1;
}
