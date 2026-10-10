#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { appDaemonInTree, createWindowDriver, delay } from './platform.mjs';
import { currentHarnessInstance, dataDirForInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';

async function poll(read, description) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    const result = await read();
    if (result) return result;
    await delay(100);
  }
  throw new Error(`Timed out waiting for ${description}`);
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  const instance = currentHarnessInstance();
  if (!instance) throw new Error('Crew header verification requires a named instance');
  const runner = createScenarioRunner(options, {
    scenarioId: 'CrewSeedHeader', tier: 'local', prefix: 'crew-seed-header', allowRealAgents: false,
  });
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const driver = createWindowDriver({ ...options, client });
  const sessions = [];
  const unsettledSeeds = new Set();
  let lastHandoffAt = 0;
  let memberRegistered = false;
  const member = `fern-${Date.now().toString(36)}`;
  const home = path.join(dataDirForInstance(instance), 'crew', member);
  const run = (args) => execFileSync(appDaemonInTree(options.appPath), args, {
    encoding: 'utf8', env: instanceCliEnv(instance, { ATTN_SESSION_ID: '' }),
  });
  const json = (args) => {
    const output = run(args);
    return JSON.parse(output.slice(output.search(/[\[{]/)));
  };
  const settleSeeds = () => {
    for (const id of unsettledSeeds) {
      run(['seed', 'wither', id, '--force', '-m', 'Harness fixture cleanup']);
      unsettledSeeds.delete(id);
    }
  };
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('delete_member', () => {
    if (memberRegistered) run(['doc', 'delete', 'core/crew', 'members', member]);
  });
  runner.registerCleanup('remove_member_home', () => {
    if (memberRegistered) fs.rmSync(home, { recursive: true });
  });
  runner.registerCleanup('settle_seeds', settleSeeds);
  runner.registerCleanup('close_crew_session', async () => {
    const current = sessions.at(-1);
    if (current) {
      // Handoff filenames have minute precision, so the successor's closing
      // letter must wait for the previous letter's minute to end.
      const nextMinute = Math.ceil((lastHandoffAt + 1) / 60_000) * 60_000;
      await delay(Math.max(0, nextMinute - Date.now()));
      run(['handoff', '--session', current, '--sleep', '-m', 'Crew header verification finished.']);
      await observer.waitFor(() => !observer.getSession(current), 'the crew session to close', 10_000);
    }
  });
  const wake = async () => {
    const id = json(['crew', 'wake', member, '--json']).session_id;
    sessions.push(id);
    await observer.waitForSession({ id, timeoutMs: 30_000 });
    await client.request('select_session', { sessionId: id });
    return id;
  };
  const header = (sessionId, title) => poll(async () => {
    const state = await client.request('session_seed_chip_get_state', { sessionId });
    return title === null ? !state.present : state.title === title && state;
  }, `header ${title ?? 'to clear'}`);
  const roster = () => json(['crew','list','--json']).find((entry) => entry.key === member);
  const capture = async (name, sessionId) => {
    const shot = await client.request('capture_screenshot_data', {
      selector: `[data-pane-session-id="${sessionId}"] .desktop-pane-header`,
    });
    fs.writeFileSync(path.join(runner.runDir, `${name}.png`), Buffer.from(shot.pngBase64, 'base64'));
    if (process.env.ATTN_HARNESS_RECORD === '1') await delay(1800);
  };

  try {
    fs.mkdirSync(home, { recursive: true });
    fs.writeFileSync(path.join(home, 'CHARTER.md'), '# Fern\n\nWait for seed header checks.\n');
    writeMockAgentFixture(runner.sessionDir, {
      name: 'Crew header verification', minimumWorkingMs: 0, turns: [],
      defaultActions: [{ type: 'reply', text: 'Ready to verify the seed header.', state: 'idle' }],
    });
    await client.quitApp();
    run(['daemon', 'stop']);
    await launchFreshAppAndConnect(client, observer);
    memberRegistered = true;
    runner.writeText('preflight.txt', run(['preflight', '--agent', 'claude', '--model', 'claude-haiku-4-5']));
    run(['crew', 'set', member, '--cwd', runner.sessionDir, '--agent', 'claude', '--model', 'claude-haiku-4-5']);
    const sessionId = await wake();
    await client.request('dom_click', { selector: '.warning-dismiss' }).catch((error) => {
      if (!String(error).includes('dom_click selector not found in DOM')) throw error;
    });
    const first = json(['seed', 'plant', 'Review release notes', '--json']).id;
    unsettledSeeds.add(first);
    const second = json(['seed', 'plant', 'Verify upload completion', '--json']).id;
    unsettledSeeds.add(second);
    await runner.step('show_member_claims_in_the_existing_header_and_popover', async () => {
      await header(sessionId, null);
      run(['seed', 'tend', first, '--for', member]);
      const single = await header(sessionId, 'Review release notes');
      runner.assert(single.id === first && single.status === 'growing', 'member claim uses the seed-state header', single);
      await capture('one-member-claim', sessionId);
      run(['seed', 'tend', second, '--for', member]);
      await header(sessionId, 'tending 2');
      await client.request('dom_click', { selector: `[data-testid="seed-chip-${sessionId}"]` });
      const popover = await client.request('dom_text', { selector: '.tended-seeds-popover' });
      runner.assert(popover.text.includes('Review release notes') && popover.text.includes('Verify upload completion'),
        'both member claims appear in the existing popover', popover);
      if (process.env.ATTN_HARNESS_RECORD === '1') await delay(1800);
      await client.request('dom_key', { selector: '.tended-seeds-popover', key: 'Escape' });
      run(['seed', 'park', second, '--force']);
      await header(sessionId, 'Review release notes');
    });
    await runner.step('retain_the_member_claim_across_sessions_and_open_it', async () => {
      lastHandoffAt = Date.now();
      run(['handoff', '--session', sessionId, '--sleep', '-m', 'Continue checking the member claim.']);
      await client.request('open_dock_panel', {panelId:'garden'});
      await client.request('garden_expand_seed', {seedId:first,reopen:true});
      await client.request('garden_resume_seed', {seedId:first});
      const next = await poll(async () => {
        const binding = roster().binding_session;
        return binding && binding !== sessionId && binding;
      }, 'Garden Resume to wake the member');
      sessions.push(next);
      const focused = await poll(async () => {
        const state = await client.request('get_state');
        return state.activeLeaf?.sessionId === next && state;
      }, 'Resume to focus the member session');
      runner.assert(focused.activeLeaf.sessionId === next, 'Garden Resume focuses the member', focused.activeLeaf);
      runner.writeJson('member-resume-state.json', focused);
      await captureFrontWindowScreenshot(path.join(runner.runDir,'member-resume-native.png'),{client,driver});
      const unchanged = json(['seed','show',first,'--json']).seed;
      runner.assert(unchanged.claimed && unchanged.tender?.ref === `member:${roster().key}`, 'Resume keeps the member claim', unchanged);
      runner.assert(next !== sessionId, 'crew starts a new session', { sessionId, next });
      await header(next, 'Review release notes');
      await capture('member-claim-after-wake', next);
      await poll(async () => {
        const state = await client.request('session_seed_chip_get_state', { sessionId: next });
        return state.animationsRunning === 0;
      }, 'the seed animation to stop');
      const appPid = client.readManifest().pid;
      const metrics = () => execFileSync('ps', ['-p', String(appPid), '-o', '%cpu=,rss='], { encoding: 'utf8' }).trim();
      const before = metrics();
      await delay(3000);
      runner.writeText('idle-app.txt', `CPU% RSS(KiB)\nbefore ${before}\nafter ${metrics()}\n`);
      run(['seed', 'harvest', first, '--force', '-m', 'Header behavior verified']);
      unsettledSeeds.delete(first);
      await header(next, null);
      run(['seed', 'tend', second, '--for', member]);
      await header(next, 'Verify upload completion');
      await client.request('dom_focus', { selector: `[data-testid="seed-chip-${next}"]` });
      await driver.pressEnter();
      await poll(async () => (await client.request('seed_document_get_state', { seedId: second })).present, 'the seed document');
    });
    await runner.step('settle_synthetic_seeds', async () => {
      settleSeeds();
      const seeds = json(['seed', 'ls', '--flat', '--json']).seeds;
      const firstSeed = seeds.find(seed => seed.id === first);
      const secondSeed = seeds.find(seed => seed.id === second);
      runner.assert(
        firstSeed?.status === 'harvested' && secondSeed?.status === 'withered',
        'synthetic seeds are settled',
        { firstSeed, secondSeed },
      );
    });
    await runner.finishSuccess();
  } catch (error) {
    await runner.finishFailure(error);
    throw error;
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
