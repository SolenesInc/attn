#!/usr/bin/env node
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, openHarnessDesktop, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { appLocalDataDirForInstance, currentHarnessInstance } from './harnessInstance.mjs';
import { captureWebKitPids, delay, readProcessTable, collectDescendantPids, classify } from './perfMeasure.mjs';
import { createWindowDriver } from './platform.mjs';
import { waitForSessionDesktop } from './scenarioAssertions.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);
const argv = process.argv.slice(2).filter((arg) => arg !== '--');
const measure = argv.includes('--measure');
const options = parseCommonArgs(argv.filter((arg) => arg !== '--measure'));
const runner = createScenarioRunner(options, {
  scenarioId: 'SHELF-MOTION',
  tier: 'tier1-local-shell',
  prefix: 'shelf-motion',
  metadata: { agent: 'shell', focus: 'per-frame trace of slide, open, close, carry and a new tile on an emptied desktop with streaming terminals; with --measure also idle cost' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const request = (action, payload) => client.request(action, payload);
const press = (shortcutId) => pressShortcutKeys(client, driver, shortcutId);
const sessions = [];
const moves = [];
let firstSession;
let desktop;
let streamFlag;
let otherWebKitPids = new Set();

const SETTLE_WINDOW_MS = 800;
const STREAMS = 4;

const leavesOf = (node) => (node.type === 'split' ? node.children.flatMap(leavesOf) : [node.paneId ?? node.tileId]);
const desktopState = () => request('get_desktop', { sessionId: firstSession });
const readRow = async () => leavesOf((await desktopState()).desktop.layoutTree);
const activeLeaf = async () => (await request('get_state')).activeLeaf.leafId;
const shownLeaves = async () => (await request('get_desktop_ui_state', { desktopId: desktop })).shownLeafIds;
const logPath = () => path.join(appLocalDataDirForInstance(currentHarnessInstance()), 'debug', 'terminal-diagnostics.jsonl');
const waitForActive = (leafId) => request('dom_wait', { selector: `[data-session-terminal-desktop="${desktop}"][data-active-leaf-id="${leafId}"]`, timeoutMs: 15_000 });

function addStream(pane) {
  return request('write_pane', { sessionId: pane.sessionId, paneId: pane.paneId, text: `sh -c 'while [ -e ${streamFlag} ]; do date; sleep 0.05; done'` });
}

async function openBeside() {
  const before = await readRow();
  await press('terminal.splitVertical');
  const state = await waitForSessionDesktop(client, firstSession, (current) => current?.panes?.length === before.length + 1 && current.panes.every((pane) => pane.runtimeId), 'one more shell', 30_000);
  sessions.push(...state.panes.map((pane) => pane.sessionId).filter((id) => !sessions.includes(id)));
  const opened = (await readRow()).find((id) => !before.includes(id));
  return { opened, pane: state.panes.find((pane) => pane.paneId === opened) };
}

async function launchFromEmptyDesktop(emptied, cwd) {
  await request('dom_wait', { selector: '[data-testid="location-picker-path-input"]', focused: true, timeoutMs: 15_000 });
  const registered = observer.waitForMessage((message) => {
    const session = message.session;
    return session && session.directory === cwd && !sessions.includes(session.id) ? session : null;
  }, `session in ${cwd}`);
  await driver.typeText(cwd);
  await driver.pressKey('Enter');
  const session = await registered;
  sessions.push(session.id);
  await request('dom_wait', { selector: `[data-session-terminal-desktop="${emptied}"] .desktop-pane[data-pane-session-id="${session.id}"]`, timeoutMs: 15_000 });
}

async function traceMove(name, act) {
  await request('motion_trace_start', { trace: name });
  const pressedAt = Date.now();
  await act();
  await delay(SETTLE_WINDOW_MS);
  const stopped = await request('motion_trace_stop');
  const rest = await request('motion_at_rest');
  moves.push({ name, pressedAt, stoppedAt: Date.now(), frames: stopped.frames, firstPaints: stopped.firstPaints, firstContentPaints: stopped.firstContentPaints, rest });
  runner.assert(stopped.frames > 10, `${name}: the trace sampled frames`, { frames: stopped.frames });
}

function readDiagnostics() {
  return fs.readFileSync(logPath(), 'utf8').split('\n').filter(Boolean).flatMap((line) => {
    try {
      return [JSON.parse(line)];
    } catch {
      return [];
    }
  });
}

function summarizeMove(move, events) {
  const inMove = events.filter((event) => event.at >= move.pressedAt && event.at <= move.stoppedAt);
  const frames = inMove.filter((event) => event.kind === 'motion_frame' && event.trace === move.name);
  const animated = [...new Set(frames.flatMap((frame) => frame.animated))].sort();
  const sizesAskedFor = {};
  for (const event of inMove) {
    if (event.kind !== 'resize' || event.source !== 'fit' || event.bail || event.toCols == null) continue;
    (sizesAskedFor[event.pane] ??= new Set()).add(`${event.toCols}x${event.toRows}`);
  }
  const resizes = Object.fromEntries(Object.entries(sizesAskedFor).map(([pane, sizes]) => [pane, sizes.size]));
  const mounts = Object.fromEntries(inMove.filter((event) => event.kind === 'pane_mount').map((event) => [event.pane, event.at]));
  const panes = {};
  const last = frames.at(-1);
  for (const final of last?.panes ?? []) {
    if (!final.id) continue;
    const track = frames.map((frame) => ({ at: frame.at, pane: frame.panes.find((pane) => pane.id === final.id) })).filter((sample) => sample.pane);
    const boxes = new Set(track.map((sample) => `${sample.pane.boxLeft}:${sample.pane.boxWidth}`));
    const start = track.find((sample) => sample.pane.x !== final.x);
    let summary = { boxChanges: boxes.size - 1, moved: Boolean(start) };
    if (start) {
      const travel = Math.abs(start.pane.x - final.x);
      const sign = Math.sign(final.x - start.pane.x);
      const overshoot = Math.max(0, ...track.map((sample) => (sample.pane.x - final.x) * sign));
      const moving = track.find((sample, index) => index > 0 && sample.pane.x !== track[index - 1].pane.x)?.at ?? start.at;
      const reached = track.find((sample) => (sample.pane.x - final.x) * sign >= 0 && sample.at >= moving)?.at;
      const peak = track.reduce((best, sample) => ((sample.pane.x - final.x) * sign > (best.pane.x - final.x) * sign ? sample : best), track[0]);
      const lastOff = [...track].reverse().find((sample) => Math.abs(sample.pane.x - final.x) > 0.5);
      summary = {
        ...summary,
        travelPx: Math.round(travel),
        overshootPx: Number(overshoot.toFixed(1)),
        overshootPct: Number(((overshoot / (travel || 1)) * 100).toFixed(2)),
        reachedMs: reached ? reached - moving : null,
        peakMs: peak.at - moving,
        settledMs: lastOff ? lastOff.at - moving : 0,
      };
    }
    panes[final.id] = summary;
  }
  const entering = Object.fromEntries(Object.entries(mounts).map(([pane, mountedAt]) => {
    const elementAt = frames.find((frame) => frame.panes.some((sample) => sample.id === pane))?.at;
    const since = (at) => (at ? at - move.pressedAt : null);
    return [pane, {
      elementFromKey: since(elementAt),
      mountFromKey: since(mountedAt),
      firstPaintFromKey: since(move.firstPaints[pane]),
      firstContentFromKey: since(move.firstContentPaints[pane]),
      blankAfterElement: elementAt && move.firstContentPaints[pane] ? move.firstContentPaints[pane] - elementAt : null,
    }];
  }));
  const gaps = frames.slice(1).map((frame, index) => frame.at - frames[index].at).sort((a, b) => a - b);
  return {
    name: move.name,
    frames: frames.length,
    frameGapP50: gaps[Math.floor(gaps.length / 2)] ?? null,
    frameGapMax: gaps.at(-1) ?? null,
    animated,
    heldPeak: Math.max(0, ...frames.map((frame) => frame.panes.filter((pane) => !pane.id).length)),
    heldInkPeak: Math.max(0, ...frames.flatMap((frame) => frame.panes.filter((pane) => !pane.id).map((pane) => pane.stillInk ?? 0))),
    willChangeFrames: frames.filter((frame) => frame.panes.some((pane) => pane.willChange !== 'auto')).length,
    resizes,
    panes,
    decoration: [...new Set(frames.flatMap((frame) => frame.decoration))].sort(),
    entering,
    rest: move.rest,
  };
}

async function topCpu(label) {
  const table = await readProcessTable();
  const app = table.find((proc) => proc.command.includes(options.appPath) && proc.command.includes('/Contents/MacOS/app'));
  const webkit = [...(await captureWebKitPids())].filter((pid) => !otherWebKitPids.has(pid));
  const pids = [...new Set([...(app ? collectDescendantPids(table, app.pid) : []), ...webkit])].filter((pid) => table.some((proc) => proc.pid === pid && ['app', 'webkit_webcontent', 'webkit_gpu', 'webkit_networking'].includes(classify(proc))));
  const { stdout } = await execFileAsync('top', ['-l', '4', '-s', '5', '-stats', 'pid,command,cpu', ...pids.flatMap((pid) => ['-pid', String(pid)])], { maxBuffer: 8 * 1024 * 1024 });
  const samples = {};
  let block = -1;
  for (const line of stdout.split('\n')) {
    if (line.startsWith('PID')) { block += 1; continue; }
    const match = line.match(/^(\d+)\s+(.+?)\s+([\d.]+)\s*$/);
    if (!match || block < 1) continue;
    const proc = table.find((entry) => entry.pid === Number(match[1]));
    const name = proc ? classify(proc) : match[2];
    (samples[`${name}:${match[1]}`] ??= []).push(Number(match[3]));
  }
  fs.writeFileSync(path.join(runner.runDir, `top-${label}.txt`), stdout);
  return { label, samples };
}

runner.registerCleanup('stop_streams', () => { if (streamFlag) fs.rmSync(streamFlag, { force: true }); });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_sessions', async () => {
  for (const sessionId of [...sessions].reverse()) await request('close_session', { sessionId }).catch(() => {});
});
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ shelf: '[data-testid="desktop-shelf"]' }) });

try {
  await runner.step('launch', async () => {
    otherWebKitPids = await captureWebKitPids();
    await launchFreshAppAndConnect(client, observer);
    await request('set_setting', { key: 'narrowest_tile_cols', value: '66' });
    const { logicalBounds } = await request('get_window_bounds');
    await request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1900, height: 900 } });
  });

  await runner.step('build_a_row_of_streaming_terminals', async () => {
    const cwd = path.join(runner.sessionDir, 'row');
    fs.mkdirSync(cwd, { recursive: true });
    streamFlag = path.join(cwd, 'streaming');
    fs.writeFileSync(streamFlag, '');
    firstSession = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: `motion-${runner.runId}`, agent: 'shell', waitForInitialPaneVisible: false });
    sessions.push(firstSession);
    await request('select_session', { sessionId: firstSession });
    desktop = (await desktopState()).desktopId;
    const first = (await desktopState()).desktop.panes[0];
    await addStream(first);
    for (let count = 2; count <= STREAMS; count += 1) {
      const { pane } = await openBeside();
      await addStream(pane);
    }
    await openBeside();
    await openBeside();
    await request('dom_wait', { selector: '[data-testid="desktop-shelf"]', timeoutMs: 15_000 });
    runner.log('row', { row: await readRow(), shown: await shownLeaves() });
  });

  if (measure) {
    await runner.step('idle_before_moves', async () => {
      fs.rmSync(streamFlag, { force: true });
      await delay(3000);
      runner.log('idle_before', await topCpu('idle-before-moves'));
      fs.writeFileSync(streamFlag, '');
      for (const pane of (await desktopState()).desktop.panes.slice(-3)) await addStream(pane).catch(() => {});
    });
  }

  await runner.step('slide_along_the_row', async () => {
    const row = await readRow();
    const start = await activeLeaf();
    let at = row.indexOf(start);
    for (; at > 0; at -= 1) {
      await press('terminal.focusLeft');
      await waitForActive(row[at - 1]);
    }
    await delay(SETTLE_WINDOW_MS);
    const shown = await shownLeaves();
    for (let step = 0; step < shown.length; step += 1) {
      await traceMove(`slide-right-${step}`, async () => {
        await press('terminal.focusRight');
        await waitForActive(row[step + 1]);
      });
    }
    for (let index = shown.length; index > 1; index -= 1) {
      await press('terminal.focusLeft');
      await waitForActive(row[index - 1]);
    }
    await delay(SETTLE_WINDOW_MS);
    await traceMove('slide-left', async () => {
      await press('terminal.focusLeft');
      await waitForActive(row[0]);
    });
  });

  await runner.step('open_beside', async () => {
    await traceMove('open', async () => { await openBeside(); });
  });

  await runner.step('close', async () => {
    const closing = await activeLeaf();
    await traceMove('close', async () => {
      await press('terminal.close');
      await request('dom_wait', { selector: `[data-session-terminal-desktop="${desktop}"] .desktop-pane[data-pane-id="${closing}"]`, absent: true, timeoutMs: 15_000 });
    });
  });

  await runner.step('carry', async () => {
    const active = await activeLeaf();
    const before = await readRow();
    const delta = before.indexOf(active) === 0 ? 1 : -1;
    await traceMove('carry', async () => {
      await press(delta > 0 ? 'shelf.carryRight' : 'shelf.carryLeft');
      await waitForSessionDesktop(client, firstSession, (current) => leavesOf(current.desktop.layoutTree).indexOf(active) === before.indexOf(active) + delta, 'the carried row');
    });
  });

  await runner.step('new_tile_on_an_emptied_desktop', async () => {
    const launcher = '[data-testid="empty-desktop-launcher"]';
    const cwd = path.join(runner.sessionDir, 'emptied');
    fs.mkdirSync(cwd, { recursive: true });
    const { id: emptied, shortcut_slot: ownSlot } = await openHarnessDesktop(client, observer);
    runner.log('emptied desktop', { emptied, ownSlot });
    const only = (await request('create_session', { cwd, label: `emptied-${runner.runId}`, agent: 'shell' })).sessionId;
    sessions.push(only);
    await request('dom_wait', { selector: `[data-session-terminal-desktop="${emptied}"] .desktop-pane[data-pane-session-id="${only}"]`, timeoutMs: 15_000 });
    await delay(SETTLE_WINDOW_MS);
    await press('terminal.close');
    await request('dom_wait', { selector: launcher, timeoutMs: 15_000 });
    await delay(SETTLE_WINDOW_MS);
    await traceMove('emptied-by-close', () => launchFromEmptyDesktop(emptied, cwd));
    await press(`desktop.sendStay${ownSlot === 9 ? 8 : 9}`);
    await request('dom_wait', { selector: launcher, timeoutMs: 15_000 });
    await delay(SETTLE_WINDOW_MS);
    await traceMove('emptied-by-move', () => launchFromEmptyDesktop(emptied, cwd));
    await driver.screenshot(path.join(runner.runDir, 'new-tile-on-emptied-desktop.png'), { windowId: await driver.mainWindowId() });
  });

  if (measure) {
    await runner.step('mid_flight_screenshots', async () => {
      await request('select_session', { sessionId: firstSession });
      const row = await readRow();
      const midFlight = async (name, act) => {
        await delay(SETTLE_WINDOW_MS);
        await request('motion_trace_start', { trace: `pause-${name}`, pauseMotionAfterMs: 60 });
        await act();
        await delay(600);
        await driver.screenshot(path.join(runner.runDir, `mid-flight-${name}.png`), { windowId: await driver.mainWindowId() });
        await request('motion_trace_stop');
        await request('motion_resume');
      };
      const at = row.indexOf(await activeLeaf());
      const shown = await shownLeaves();
      await midFlight('at-rest', async () => {});
      if (shown.includes(row[at + 1])) {
        await press('terminal.focusRight');
        await waitForActive(row[at + 1]);
      }
      const here = row.indexOf(await activeLeaf());
      await midFlight('slide', async () => {
        await press('terminal.focusRight');
        await waitForActive(row[here + 1]);
      });
      await midFlight('open', async () => { await openBeside(); });
      await midFlight('close', async () => { await press('terminal.close'); });
    });

    await runner.step('active_and_idle_cost', async () => {
      const row = await readRow();
      let stop = false;
      const driving = (async () => {
        let index = row.indexOf(await activeLeaf());
        let direction = 1;
        while (!stop) {
          if (index + direction < 0 || index + direction >= row.length) direction = -direction;
          index += direction;
          await press(direction > 0 ? 'terminal.focusRight' : 'terminal.focusLeft');
          await waitForActive(row[index]);
          await delay(600);
        }
      })();
      runner.log('active', await topCpu('active-moves'));
      stop = true;
      await driving;
      fs.rmSync(streamFlag, { force: true });
      await delay(3000);
      runner.log('idle_after', await topCpu('idle-after-moves'));
      fs.writeFileSync(streamFlag, '');
    });
  }

  await runner.step('every_move_used_only_transform_and_opacity', async () => {
    const events = readDiagnostics();
    const summaries = moves.map((move) => summarizeMove(move, events));
    fs.writeFileSync(path.join(runner.runDir, 'motion-summary.json'), `${JSON.stringify(summaries, null, 2)}\n`);
    fs.writeFileSync(path.join(runner.runDir, 'motion-trace.jsonl'), events.filter((event) => event.at >= moves[0].pressedAt && ['motion_frame', 'resize', 'pane_mount', 'pane_unmount'].includes(event.kind)).map((event) => JSON.stringify(event)).join('\n'));
    for (const summary of summaries) {
      runner.log('move', summary);
      runner.assert(summary.animated.every((property) => property === 'transform' || property === 'opacity'), `${summary.name} animates only transform and opacity`, { animated: summary.animated });
      const screenChanged = ['open', 'close', 'carry', 'slide-left'].includes(summary.name) || Object.keys(summary.entering).length > 0;
      if (screenChanged) runner.assert(summary.animated.length > 0, `${summary.name} animates`, { summary });
      runner.assert(Object.values(summary.resizes).every((count) => count <= 1), `${summary.name} resizes each terminal at most once`, { resizes: summary.resizes });
      runner.assert(Object.values(summary.panes).every((pane) => pane.boxChanges <= 1), `${summary.name} changes each measured box at most once`, { panes: summary.panes });
      runner.assert(summary.willChangeFrames === 0, `${summary.name} sets no will-change`, { summary });
      if (summary.name.startsWith('emptied-')) {
        runner.assert(summary.heldPeak === 0, `${summary.name} never shows the tile that left`, { summary });
        runner.assert(summary.animated.includes('opacity') && summary.animated.includes('transform'), `${summary.name} grows and fades the new tile in`, { summary });
      }
      if (summary.heldPeak > 0) runner.assert(summary.heldInkPeak > 1, `${summary.name} keeps the leaving terminal's text on screen`, { summary });
      runner.assert(summary.rest.animations === 0 && summary.rest.heldPanes === 0 && summary.rest.willChange === 0 && summary.rest.layoutTransitions === 0, `${summary.name} leaves nothing at rest`, { rest: summary.rest });
    }
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
