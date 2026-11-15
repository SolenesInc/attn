#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { readProcessEnvironment } from './agentTripwire.mjs';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessInstance, dataDirForInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver, observeProcessExits } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { registeredTerminalProcesses } from './workerRegistry.mjs';

const CLOSE_BUDGET_MS = 1_000;
const execFileAsync = promisify(execFile);

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
assertFreshWorldTargetSafe({ instance, appPath: options.appPath });
const daemonBinary = appCliInTree(options.appPath);
const dataDir = dataDirForInstance(instance);
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-CLOSE-PANE', tier: 'tier2-local-mock-agent', prefix: 'native-close-pane',
  metadata: { focus: 'In the native app, closing a split shell keeps the remaining shell active with the keyboard, closing a tile returns to the most recently used one, closing the last session of a desktop leaves it current and its digit bounces back to the desktop before, and an agent that ignores SIGTERM leaves the screen at once while Git is slow', instance },
});

function prepareSlowGit(sessionDir) {
  const env = instanceCliEnv();
  const realGit = execFileSync('/bin/sh', ['-c', 'command -v git'], { encoding: 'utf8', env }).trim();
  execFileSync(realGit, ['init', '-q'], { cwd: sessionDir, env });
  const binDir = path.join(sessionDir, 'slow-git-bin');
  fs.mkdirSync(binDir);
  const executable = path.join(binDir, 'git');
  const gate = path.join(sessionDir, 'slow-git-enabled');
  fs.writeFileSync(executable, '#!/bin/sh\nif [ -f "$ATTN_CLOSE_GIT_GATE" ]; then sleep 3; fi\nexport PATH="$ATTN_CLOSE_GIT_BASE_PATH"\nexec "$ATTN_CLOSE_REAL_GIT" "$@"\n', { mode: 0o755 });
  return {
    executable, gate, binDir,
    env: {
      PATH: `${binDir}${path.delimiter}${process.env.PATH}`,
      ATTN_CLOSE_GIT_BASE_PATH: process.env.PATH,
      ATTN_CLOSE_GIT_GATE: gate,
      ATTN_CLOSE_REAL_GIT: realGit,
    },
  };
}

const slowGit = prepareSlowGit(runner.sessionDir);
const client = new UiAutomationClient({ appPath: options.appPath, launchEnv: slowGit.env });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('stop_daemon', () => execFileAsync(daemonBinary, ['daemon', 'stop'], { env: instanceCliEnv(instance) }));
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('remove_slow_git_gate', () => fs.rmSync(slowGit.gate, { force: true }));

const mac = process.platform === 'darwin';
const keys = mac
  ? { splitBeside: 'mod+d', close: 'mod+w', desktop: (slot) => `mod+${slot}` }
  : { splitBeside: 'mod+shift+d', close: 'mod+shift+w', desktop: (slot) => `mod+shift+${slot}` };

const inSessionDir = (session) => session.directory?.startsWith(runner.sessionDir);

try {
  const checksFocus = mac;
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app_on_a_daemon_that_runs_git_through_the_slow_wrapper', async () => {
    await client.quitApp();
    await execFileAsync(daemonBinary, ['daemon', 'stop'], { env: instanceCliEnv(instance) });
    await launchFreshNativeAppAndConnect(client, observer);
    await driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 });
  });
  runner.registerCleanup('unregister_sessions', () => observer.unregisterMatchingSessions(inSessionDir));
  const startedOn = observer.currentDesktopId();
  if (startedOn) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(startedOn));

  const makeDesktop = async () => {
    const taken = new Set(observer.desktops.map((desktop) => desktop.shortcut_slot).filter(Boolean));
    const slot = [1, 2, 3, 4, 5, 6, 7, 8, 9].find((n) => !taken.has(n));
    runner.assert(slot, 'a desktop slot is free for the scenario', observer.describeArrangement());
    const result = await observer.profileCommand('desktop_create', { profile_id: observer.profileId, shortcut_slot: slot });
    const id = result.desktops?.[0]?.id;
    runner.assert(id, 'desktop_create makes a desktop', result);
    runner.registerCleanup(`close_desktop_${slot}`, () => {
      const left = observer.desktop(id);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    await observer.waitFor(() => observer.desktop(id), `desktop ${id} in the arrangement`);
    return { id, slot };
  };

  const spawn = async (label, desktopId, agent = 'shell', cwd = path.join(runner.sessionDir, label)) => {
    const id = crypto.randomUUID();
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `${label} spawned`, 60_000);
    observer.send({ cmd: 'spawn_session', id, agent, cwd, profile_id: observer.profileId, placement: { desktop_id: desktopId }, label: `${label}-${runner.runId}`, cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, `spawn_session starts ${label}`, result);
    const desktop = await observer.waitForDesktopOf(id, (entry) => entry.panes.some((p) => p.pane_id === result.pane_id && p.runtime_id));
    const pane = desktop.panes.find((p) => p.pane_id === result.pane_id);
    return { session: id, pane: pane.pane_id, runtime: pane.runtime_id };
  };

  const goTo = async (desktop) => {
    if (observer.currentDesktopId() === desktop.id) await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    else await driver.pressKey(keys.desktop(desktop.slot));
    await observer.waitFor(() => observer.currentDesktopId() === desktop.id, `desktop ${desktop.slot} current`);
  };

  const active = (desktop) => observer.desktop(desktop.id)?.active_pane_id;
  const waitActive = (desktop, shell, description) => observer.waitFor(() => active(desktop) === shell.pane, description);

  const split = async (desktop) => {
    const before = new Set(observer.desktop(desktop.id).panes.map((p) => p.pane_id));
    await driver.pressKey(keys.splitBeside);
    const created = await observer.waitFor(
      () => observer.desktop(desktop.id)?.panes.find((p) => !before.has(p.pane_id) && p.runtime_id),
      'a new shell beside the active tile',
      20_000,
    );
    const shell = { session: created.session_id, pane: created.pane_id, runtime: created.runtime_id };
    await waitActive(desktop, shell, 'the new shell is the active tile');
    return shell;
  };

  const pick = async (desktop, shell) => {
    await driver.click({ key: `sidebar.session.${shell.session}` });
    await waitActive(desktop, shell, `${shell.pane} picked from the sidebar`);
  };

  const closeActive = async (shell) => {
    await driver.pressKey(keys.close);
    await driver.waitFor({ key: `tile.${shell.pane}` }, { gone: true });
    await driver.waitFor({ key: `sidebar.session.${shell.session}` }, { gone: true });
    await observer.waitFor(() => !observer.getSession(shell.session), `session ${shell.session} closed`);
  };

  let typed = 0;
  const typingReaches = async (shell) => {
    typed += 1;
    await driver.typeInto(null, `echo typed-${typed}-$((40 + 2))`);
    await driver.pressKey('Enter');
    await driver.state(`terminal:${shell.runtime}`, { includes: `typed-${typed}-42`, timeoutMs: 15_000 });
  };

  let kept;
  let a;
  await runner.step('a_shell_on_a_desktop_of_its_own', async () => {
    kept = await makeDesktop();
    a = await spawn('kept', kept.id);
    await goTo(kept);
    await driver.waitFor({ key: `tile.${a.pane}` });
    await driver.click({ key: `tile.${a.pane}.header` });
    await waitActive(kept, a, 'the kept shell is active');
  });

  await runner.step('closing_a_split_keeps_the_remaining_shell_active_with_the_keyboard', async () => {
    const b = await split(kept);
    await typingReaches(b);
    await closeActive(b);
    await waitActive(kept, a, 'the kept shell is active again');
    runner.assert(observer.currentDesktopId() === kept.id, 'the desktop stays current', observer.describeArrangement());
    runner.assert(observer.desktop(kept.id).panes.length === 1, 'the desktop holds one shell', observer.desktop(kept.id));
    await driver.waitFor({ key: `sidebar.session.${a.session}` });
    await typingReaches(a);
  });

  await runner.step('closing_a_tile_returns_to_the_most_recently_used_tile', async () => {
    const b = await split(kept);
    const c = await split(kept);
    const d = await split(kept);
    for (const shell of [a, b, c]) await pick(kept, shell);
    await closeActive(c);
    await waitActive(kept, b, 'row A B C D, A then B then C, closing C returns to B');
    await typingReaches(b);

    const again = await split(kept);
    for (const shell of [a, b, again, d, again]) await pick(kept, shell);
    await closeActive(again);
    await waitActive(kept, d, 'A, B, C, D, C again: closing C returns to D');
    await typingReaches(d);

    observer.unregisterSession(b.session);
    await driver.waitFor({ key: `sidebar.session.${b.session}` }, { gone: true });
    await observer.waitFor(() => !observer.desktop(kept.id).panes.some((p) => p.pane_id === b.pane), 'B leaves the desktop');
    runner.assert(active(kept) === d.pane, 'closing B in the background keeps D active', observer.desktop(kept.id));
    await typingReaches(d);

    await closeActive(d);
    await waitActive(kept, a, 'closing D skips the closed B and C and returns to A');
    await typingReaches(a);
  });

  let emptied;
  await runner.step('closing_the_last_session_of_a_desktop_leaves_it_current_and_empty', async () => {
    emptied = await makeDesktop();
    const last = await spawn('closing', emptied.id);
    await goTo(emptied);
    await driver.waitFor({ key: `tile.${last.pane}` });
    await driver.click({ key: `tile.${last.pane}.header` });
    await waitActive(emptied, last, 'the last shell is active');
    await closeActive(last);
    await driver.waitFor({ key: 'launcher.path.field' });
    runner.assert(observer.currentDesktopId() === emptied.id && observer.desktop(emptied.id)?.panes.length === 0, 'the emptied desktop stays current', observer.describeArrangement());
  });

  await runner.step('the_emptied_desktops_digit_bounces_back_to_the_desktop_before', async () => {
    await driver.pressKey(keys.desktop(emptied.slot));
    await observer.waitFor(() => observer.currentDesktopId() === kept.id, 'the desktop before is current again');
    await driver.waitFor({ key: `tile.${a.pane}` });
    await driver.waitFor({ key: 'launcher.path.field' }, { gone: true });
    runner.assert(observer.desktop(kept.id).panes.length === 1 && active(kept) === a.pane, 'the desktop before still holds its shell, active', observer.desktop(kept.id));
  });

  let agent;
  await runner.step('an_agent_that_ignores_sigterm', async () => {
    writeMockAgentFixture(runner.sessionDir, { name: 'stubborn close mock', ignoreSigterm: true, turns: [] });
    agent = await spawn('stubborn', kept.id, 'codex', runner.sessionDir);
    await driver.waitFor({ key: `tile.${agent.pane}` });
    await driver.state(`terminal:${agent.runtime}`, { includes: 'OpenAI Codex mock agent', timeoutMs: 30_000 });
  });

  const slowGitProbeMs = await runner.step('git_takes_three_seconds', async () => {
    const daemonPid = Number(fs.readFileSync(path.join(dataDir, 'attn.pid'), 'utf8').trim());
    runner.assert(readProcessEnvironment(daemonPid).includes(`PATH=${slowGit.binDir}${path.delimiter}`), 'the daemon resolves Git through the slow wrapper');
    fs.writeFileSync(slowGit.gate, 'enabled\n');
    const start = performance.now();
    await execFileAsync(slowGit.executable, ['rev-parse', '--show-toplevel'], { cwd: runner.sessionDir, env: instanceCliEnv(instance, slowGit.env) });
    const elapsedMs = performance.now() - start;
    runner.assert(elapsedMs >= 3_000, 'the Git probe takes at least three seconds', { elapsedMs });
    return elapsedMs;
  });

  const processes = registeredTerminalProcesses(dataDir, agent.runtime, runner.sessionDir);
  runner.assert(processes?.workerPid > 1, 'the terminal registry names the worker and child before close', { processes });
  const endsWithTheTerminal = processes.sharedHost ? [processes.childPid] : [processes.workerPid, processes.childPid];
  const exited = await observeProcessExits(endsWithTheTerminal, { env: instanceCliEnv(instance), timeoutMs: 8_000 });

  let closeElapsedMs;
  await runner.step('the_agent_tile_leaves_at_once', async () => {
    await driver.click({ key: `tile.${agent.pane}.header` });
    await waitActive(kept, agent, 'the agent is active');
    const startedAt = performance.now();
    await driver.pressKey(keys.close);
    await driver.waitFor({ key: `tile.${agent.pane}` }, { gone: true, timeoutMs: CLOSE_BUDGET_MS });
    await driver.waitFor({ key: `sidebar.session.${agent.session}` }, { gone: true, timeoutMs: CLOSE_BUDGET_MS });
    closeElapsedMs = performance.now() - startedAt;
    runner.assert(closeElapsedMs < CLOSE_BUDGET_MS, `the tile and its sidebar row left in ${closeElapsedMs.toFixed(1)} ms; limit=${CLOSE_BUDGET_MS} ms`, { closeElapsedMs, closeBudgetMs: CLOSE_BUDGET_MS });
  });

  await runner.step('worker_and_child_exit_after_close', async () => {
    await exited.completed;
    runner.writeJson('closed-processes.json', processes);
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ kept, emptied, agent, closeElapsedMs, slowGitProbeMs, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
