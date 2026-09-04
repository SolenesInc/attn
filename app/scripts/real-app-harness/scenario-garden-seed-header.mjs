#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, printCommonHelp } from './common.mjs';
import { appDaemonInTree, delay } from './platform.mjs';
import { currentHarnessProfile, profileCliEnv } from './harnessProfile.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { captureWebKitPids, snapshot, readAppFootprint, readLiveDaemonPid, readProcessTable, appPids } from './perfMeasure.mjs';

async function poll(read, description) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    const result = await read();
    if (result) return result;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}`);
}

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  if (args.includes('--help')) { printCommonHelp('scenario-garden-seed-header'); return; }
  const options = parseCommonArgs(args);
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('Garden header verification requires a named profile.');
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, { scenarioId: 'GardenSeedHeader', tier: 'local', prefix: 'garden-seed-header', allowRealAgents: false });
  let dispatcher;
  let agent;
  let seedId;
  const runAttn = (args, sessionId = dispatcher) => execFileSync(appDaemonInTree(options.appPath), args, {
    encoding: 'utf8', env: profileCliEnv(profile, { ATTN_SESSION_ID: sessionId ?? '' }),
  });
  try {
    const webkitBaseline = await captureWebKitPids();
    await launchFreshAppAndConnect(client, observer);
    const cwd = path.join(runner.sessionDir, 'garden-header');
    fs.mkdirSync(cwd, { recursive: true });
    writeMockAgentFixture(cwd, {
      name: 'Garden designer',
      turns: [{ includes: 'Give the garden a little life', actions: [{ type: 'reply', text: 'The garden icons are ready to inspect.', state: 'waiting_input' }] }],
    });
    await runner.step('open_an_agent_with_a_reporting_seed', async () => {
      dispatcher = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: 'Garden header setup', agent: 'shell' });
      const before = new Set(observer.sessionsById.keys());
      runAttn(['delegate', '--agent', 'codex', '--model', 'gpt-5.4-mini', '--effort', 'low', '--yolo', '--new-workspace', '--no-worktree', '--cwd', cwd,
        '--name', `garden-${dispatcher.slice(0, 8)}`,
        '--source-session', dispatcher, '--brief', 'Give the garden a little life']);
      await observer.waitFor(() => {
        agent = [...observer.sessionsById.keys()].find((id) => !before.has(id));
        return Boolean(agent);
      }, 'the mock agent to start', 60_000);
      await client.request('select_session', { sessionId: agent });
      const chip = await poll(async () => {
        const state = await client.request('session_seed_chip_get_state', { sessionId: agent });
        return state.id ? state : null;
      }, 'the seed header');
      seedId = chip.id;
      runAttn(['seed', 'note', seedId, '-m', 'The silhouettes work at 24px. The sleeping bud and harvest basket remain distinct.'], agent);
    });
    const selector = () => `[data-testid="seed-chip-${agent}"]`;
    for (const [status, command] of [
      ['growing', null],
      ['dormant', ['park', seedId]],
      ['growing', ['tend', seedId]],
      ['harvested', ['harvest', seedId, '-m', 'All five garden states are clear in the agent header.']],
      ['planted', ['replant', seedId]],
      ['withered', ['wither', seedId, '-m', 'This design exploration has ended.']],
    ]) {
      await runner.step(`show_${status}${command ? `_${command[0]}` : ''}`, async () => {
        if (command) runAttn(['seed', ...command], agent);
        const state = await poll(async () => {
          const chip = await client.request('session_seed_chip_get_state', { sessionId: agent });
          return chip.status === status ? chip : null;
        }, `the ${status} header`);
        runner.assert(state.id === seedId, 'the reporting seed survives release', state);
        let hoverBounds;
        if (!command) {
          await client.request('dom_hover', { selector: selector() });
          await poll(async () => {
            const result = await client.request('dom_text', { selector: '.pane-seed-context' }).catch(() => null);
            return result?.text?.includes('The silhouettes work at 24px.') ? result : null;
          }, 'the hover preview');
          await client.request('dom_terminal_key', {
            selector: '.terminal-wrapper.active .terminal-container', key: 'Escape', code: 'Escape',
          });
          const afterEscape = await client.request('dom_text', { selector: '.pane-seed-context' });
          runner.assert(afterEscape.text.includes('The silhouettes work at 24px.'),
            'an unpinned hover leaves terminal Escape alone', afterEscape);
          hoverBounds = (await client.request('dom_hover', { selector: '.tended-seeds-popover' })).bounds;
        }
        await client.request('dom_key', { selector: selector(), key: 'ArrowDown' });
        if (hoverBounds) {
          const pinnedBounds = (await client.request('dom_hover', { selector: '.tended-seeds-popover' })).bounds;
          runner.assert(hoverBounds.x === pinnedBounds.x && hoverBounds.y === pinnedBounds.y,
            'pinning a hover preview preserves its position', { hoverBounds, pinnedBounds });
        }
        const context = await poll(async () => {
          const result = await client.request('dom_text', { selector: '.pane-seed-context' }).catch(() => null);
          return result?.text?.includes('The silhouettes work at 24px.') ? result : null;
        }, 'the latest note in the preview');
        runner.assert(context.text.includes('This agent reports to this seed.'), 'the relationship stays separate from state', context);
        if (!command) {
          runAttn(['seed', 'note', seedId, '-m', 'The silhouettes work at 24px. A new note arrived while this preview stayed open.'], agent);
          await poll(async () => {
            const result = await client.request('dom_text', { selector: '.pane-seed-context' });
            return result.text.includes('A new note arrived') ? result : null;
          }, 'the open preview to refresh its latest note');
        }
        if (status === 'harvested') runner.assert(context.text.includes('All five garden states are clear'), 'the completed outcome is readable', context);
        runner.writeText(`${status}-context.json`, JSON.stringify({ state, context }, null, 2));
        if (process.env.ATTN_HARNESS_RECORD === '1') await delay(1600);
        await poll(async () => {
          const chip = await client.request('session_seed_chip_get_state', { sessionId: agent });
          return chip.animationsRunning === 0 ? chip : null;
        }, 'the header animation to stop');
        await client.request('dom_key', { selector: '.tended-seeds-popover', key: 'Escape' });
      });
    }
    await runner.step('sample_the_idle_app', async () => {
      const appPid = client.readManifest().pid;
      const samples = [];
      for (let index = 0; index < 2; index += 1) {
        await delay(2000);
        const current = await snapshot(appPid, readLiveDaemonPid(profile), webkitBaseline);
        const pids = new Set(appPids(current));
        const cpu = [];
        for (const { pid, cpuPct } of await readProcessTable()) {
          if (pids.has(pid)) cpu.push({ pid, cpuPct });
        }
        samples.push({ footprint: await readAppFootprint(current), cpu, processes: current });
      }
      runner.writeText('idle-app.json', JSON.stringify(samples, null, 2));
    });
    await runner.step('open_the_seed_from_its_header', async () => {
      await client.request('dom_click', { selector: selector() });
      await poll(async () => {
        const state = await client.request('seed_document_get_state', { seedId });
        return state.present ? state : null;
      }, 'the seed document');
    });
    console.log(JSON.stringify(await runner.finishSuccess({ seedId, agent }), null, 2));
  } catch (error) {
    console.error((await runner.finishFailure(error, { seedId, agent })).error);
    process.exitCode = 1;
  } finally {
    if (agent) await client.request('close_session', { sessionId: agent }).catch(() => {});
    if (dispatcher) await client.request('close_session', { sessionId: dispatcher }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}
main().catch((error) => { console.error(error); process.exitCode = 1; });
