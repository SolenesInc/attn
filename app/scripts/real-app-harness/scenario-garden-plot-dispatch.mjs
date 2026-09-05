#!/usr/bin/env node
import path from 'node:path';
import fs from 'node:fs';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { runShellCommandInPane, waitForFirstWorkspacePane, waitForPaneShellReady } from './scenarioAssertions.mjs';
import { delay } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { recordingEnabled } from './windowRecording.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

const PLOT = {
  title: 'Walk a plot end to end',
  body: '# The plan\n\nThe crown body is the plan a delegate is primed with.',
  children: [
    { title: 'First parallel step', body: 'Nothing holds this one.', blocks: ['sequenced-step'] },
    { title: 'Second parallel step', body: 'Nothing holds this one either.' },
    { title: 'The sequenced step' },
  ],
};

const PACE_MS = recordingEnabled() ? 1_400 : 0;

async function pace() {
  if (PACE_MS > 0) await delay(PACE_MS);
}

// A pane wraps at its own width and a break landing on a space swallows it, so
// everything read here is matched with the whitespace taken out of both sides.
function flat(text) {
  return text.replace(/\n/g, '');
}

function squash(text) {
  return text.replace(/\s+/g, '');
}

function saw(haystack, needle) {
  return squash(haystack).includes(squash(needle));
}

async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const output = await runShellCommandInPane(client, pane, command, expected, timeoutMs);
  await pace();
  return output;
}

async function openPane(client, observer, runner, label) {
  const cwd = path.join(runner.sessionDir, label);
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client, observer, cwd, label, agent: 'shell',
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${label}`, 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId);
  return { sessionId, paneId: pane.paneId };
}

function seedIDs(text) {
  return [...flat(text).matchAll(/(s-[a-z0-9]{6})/g)].map((match) => match[1]);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-plot-dispatch');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenPlotDispatch',
    tier: 'local',
    prefix: 'garden-plot-dispatch',
  });

  let pane = null;
  let delegated = null;
  let second = null;
  let crown = null;
  let children = [];
  let strayID = null;
  try {
    await launchFreshAppAndConnect(client, observer);
    pane = await runner.step('open_session', () => openPane(client, observer, runner, 'gardener'));

    await runner.step('plant_a_plot_in_one_command', async () => {
      const payload = path.join(runner.sessionDir, 'plot.json');
      fs.writeFileSync(payload, JSON.stringify(PLOT));
      const planted = await runInPane(client, pane,
        `attn seed plot -f ${payload} --session ${pane.sessionId}`, 'sequenced-step');
      const ids = seedIDs(planted);
      runner.assert(ids.length >= 4, 'the plot answered with a crown and three children', { planted });
      crown = ids[0];
      children = ids.slice(1, 4);
      runner.writeText('plot.txt', planted + '\n');
    });

    await runner.step('the_crown_wears_its_plot', async () => {
      const listed = await runInPane(client, pane, 'attn seed ls', 'done ·');
      runner.assert(saw(listed, '[0/3 done · 0 growing · 2 ready · 1 blocked]'),
        'the crown row carries its plot progress', { listed });
      runner.writeText('ls-tree.txt', listed + '\n');
    });

    delegated = await runner.step('dispatch_a_delegate_at_the_plot', async () => {
      const known = new Set(observer.sessionsById.keys());
      await client.request('write_pane', {
        ...pane,
        text: `attn delegate --agent shell --model none --no-worktree --source-session ${pane.sessionId} ` +
          `--plot ${crown} --name plotdel --brief "Tend the plot you were dispatched at."`,
      });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the delegated session exists', 60_000);
      await client.request('select_session', { sessionId: pane.sessionId });
      await waitForPaneShellReady(client, pane.sessionId, pane.paneId);
      await runInPane(client, pane, 'true', '');
      return spawned;
    });

    await runner.step('the_delegates_ready_is_its_plot', async () => {
      const scoped = await runInPane(client, pane,
        `attn seed ready --session ${delegated}`, 'ready in the plot under');
      runner.assert(saw(scoped, `in the plot under ${crown}`),
        'a flag-free ready inside the delegated session answers with its plot', { scoped });
      runner.assert(!saw(scoped, 'ready in the garden'),
        'the plot answer did not fall back to the garden', { scoped });
      runner.writeText('ready-plot.txt', scoped + '\n');
    });

    await runner.step('dispatch_is_scope_and_not_a_fence', async () => {
      const outside = await runInPane(client, pane,
        `attn seed plant "Work outside the plot" --session ${pane.sessionId}`, 's-');
      strayID = seedIDs(outside).pop();
      const all = await runInPane(client, pane,
        `attn seed ready --all --session ${delegated}`, 'ready in the garden');
      runner.assert(saw(all, strayID),
        '--all from the delegated session reaches a seed outside its plot', { all, strayID });
      runner.writeText('ready-all.txt', all + '\n');
    });

    await runner.step('the_panel_walks_the_garden', async () => {
      await client.request('open_dock_panel', { panelId: 'garden' });
      await delay(500);
      const whole = await client.request('garden_get_state', {});
      runner.assert(whole.present, 'the garden panel is on screen', { whole });
      runner.assert(whole.trail.length === 1 && whole.trail[0].here,
        'the panel opens on the whole garden', { trail: whole.trail });
      const crownRow = whole.seeds.find((seed) => seed.id === crown);
      runner.assert(Boolean(crownRow?.plot), 'the crown row carries its plot counts', { crownRow });
      await pace();

      const inside = await client.request('garden_open_plot', { seedId: crown });
      runner.assert(inside.trail.length === 2 && inside.trail[1].here,
        'opening a crown walks into its plot', { trail: inside.trail });
      const listed = inside.seeds.map((seed) => seed.id).sort();
      runner.assert(JSON.stringify(listed) === JSON.stringify([...children].sort()),
        'the plot shows its children and nothing else', { listed, children });
      fs.writeFileSync(path.join(runner.runDir, 'garden-plot.png'),
        Buffer.from((await client.request('capture_screenshot_data', { selector: '.garden-panel' })).pngBase64, 'base64'));
      await pace();

      const back = await client.request('garden_climb_to', { depth: 0 });
      runner.assert(
        back.trail.length === 1
          && back.trail[0].here
          && back.seeds.some((seed) => seed.id === crown)
          && back.seeds.some((seed) => seed.id === strayID),
        'the trail climbs back out to the whole garden',
        { trail: back.trail, seeds: back.seeds, crown, strayID },
      );
      await pace();
    });

    await runner.step('the_plot_drains_live', async () => {
      const [first, , sequenced] = children;
      const inside = await client.request('garden_open_plot', { seedId: crown });
      runner.assert(inside.crown.includes('0/3 done'), 'the plot head starts with nothing done', { head: inside.crown });
      await runInPane(client, pane, `attn seed tend ${first} --session ${delegated}`, 'is growing');
      const growing = await client.request('garden_get_state', {});
      runner.assert(growing.crown.includes('1 growing'),
        'the panel shows the plot moving without anybody refreshing it', { head: growing.crown });
      await pace();

      await runInPane(client, pane,
        `attn seed harvest ${first} -m "the first parallel step is done" --session ${delegated}`, 'is harvested');
      const drained = await client.request('garden_get_state', {});
      runner.assert(drained.crown.includes('1/3 done'),
        'harvesting a child drains the plot on screen', { head: drained.crown });
      const freed = await runInPane(client, pane, `attn seed ready --session ${delegated}`, 'ready in the plot under');
      runner.assert(saw(freed, sequenced),
        'harvesting the blocker freed the sequenced step', { freed, sequenced });
      runner.assert(drained.crown.includes('2 ready') && !drained.crown.includes('blocked'),
        'the freed step is what the plot counts as ready, with nothing blocked left', { head: drained.crown });
      const back = await client.request('garden_climb_to', { depth: 0 });
      const drainedRow = back.seeds.find((seed) => seed.id === crown);
      runner.assert(Boolean(drainedRow?.plot.startsWith('1/3')),
        'the list row counts the harvest too', { row: drainedRow });
      runner.writeText('ready-after-harvest.txt', freed + '\n');
      await pace();
    });

    await runner.step('two_delegates_share_one_plot', async () => {
      const [, parallel, sequenced] = children;
      const known = new Set(observer.sessionsById.keys());
      const refusedCrown = await runInPane(client, pane,
        `attn delegate --agent shell --model none --no-worktree --source-session ${pane.sessionId} ` +
          `--plot ${crown} --name plotdel2 --brief "Tend the plot you were dispatched at."`, 'one tender at a time');
      runner.assert(saw(refusedCrown, `${crown} is being tended by ${delegated}`),
        'dispatching at a tended crown is refused and names its tender', { refusedCrown });
      runner.assert(observer.sessionsById.size === known.size,
        'the refused dispatch started no session', { sessions: [...observer.sessionsById.keys()] });

      await client.request('write_pane', {
        ...pane,
        text: `attn delegate --agent shell --model none --no-worktree --source-session ${pane.sessionId} ` +
          `--plot ${parallel} --name plotdel2 --brief "Tend the seed you were dispatched at."`,
      });
      await observer.waitFor(() => {
        second = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(second);
      }, 'the second delegated session exists', 60_000);
      await client.request('select_session', { sessionId: pane.sessionId });
      await waitForPaneShellReady(client, pane.sessionId, pane.paneId);
      await runInPane(client, pane, 'true', '');

      const offered = await runInPane(client, pane, `attn seed ready --all --session ${second}`, 'ready in the garden');
      runner.assert(saw(offered, sequenced) && !saw(offered, parallel),
        'the second delegate already holds its seed and sees the rest of the plot', { offered });
      await runInPane(client, pane, `attn seed tend ${sequenced} --session ${delegated}`, 'is growing');

      const refused = await runInPane(client, pane,
        `attn seed tend ${parallel} --session ${delegated}`, 'takes it from them');
      runner.assert(saw(refused, `${parallel} is being tended by ${second}`),
        'a second claim on one seed is refused and names who holds it', { refused });

      const drained = await runInPane(client, pane, `attn seed ready --session ${delegated}`, 'in the plot under');
      runner.assert(saw(drained, `nothing is ready in the plot under ${crown}`),
        'the two claims emptied the plot\u2019s ready list between them', { drained });
      runner.writeText('two-delegates.txt', refusedCrown + '\n' + drained + '\n');
    });

    await runner.step('a_fresh_session_reorients_from_ready_alone', async () => {
      const fresh = await openPane(client, observer, runner, 'newcomer');
      const seen = await runInPane(client, fresh, 'attn seed ready', 'ready in the garden');
      runner.assert(saw(seen, 'ready in the garden'),
        'a fresh session answers for the whole garden', { seen });
      const tree = await runInPane(client, fresh, `attn seed show ${crown}`, 'plot');
      runner.assert(saw(tree, '1 of 3 done'),
        'the crown tells a newcomer where its plot stands', { tree });
      runner.writeText('fresh-session.txt', seen + '\n' + tree + '\n');
      await client.request('close_session', { sessionId: fresh.sessionId }).catch(() => {});
    });

    const summary = await runner.finishSuccess({ crown, children, delegated, second });
    console.log('[RealAppHarness] Garden plot dispatch passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { crown, children, delegated, second });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [delegated, second, pane?.sessionId]) {
      if (id) await client.request('close_session', { sessionId: id }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
