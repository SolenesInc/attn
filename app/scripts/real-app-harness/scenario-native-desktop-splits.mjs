#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-DESKTOP-SPLITS', tier: 'tier1-local-shell', prefix: 'native-desktop-splits',
  metadata: { focus: 'In the native app, switching desktops keeps each desktop\'s split shells and their scrollback to itself, closing one split leaves the other shells and their scrollback, and focus shows one tile alone until it returns to its row or the desktop is left' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const mac = process.platform === 'darwin';
const keys = mac
  ? { splitBeside: 'mod+d', splitBelow: 'mod+shift+d', close: 'mod+w', desktop: (slot) => `mod+${slot}` }
  : { splitBeside: 'mod+shift+d', splitBelow: 'mod+alt+d', close: 'mod+shift+w', desktop: (slot) => `mod+shift+${slot}` };

const inSessionDir = (session) => session.directory?.startsWith(runner.sessionDir);

try {
  const checksFocus = mac;
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_two_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));
  runner.registerCleanup('unregister_shells', () => observer.unregisterMatchingSessions(inSessionDir));

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

  const spawnShell = async (label, desktopId) => {
    const id = crypto.randomUUID();
    const cwd = path.join(runner.sessionDir, label);
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `${label} spawned`);
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktopId }, label, cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, `spawn_session starts ${label}`, result);
    const pane = observer.desktop(desktopId)?.panes.find((p) => p.pane_id === result.pane_id)
      ?? (await observer.waitForDesktopOf(id, (entry) => entry.panes.some((p) => p.pane_id === result.pane_id))).panes.find((p) => p.pane_id === result.pane_id);
    return { session: id, pane: pane.pane_id, runtime: pane.runtime_id };
  };

  const goTo = async (desktop) => {
    if (observer.currentDesktopId() === desktop.id) await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    else await driver.pressKey(keys.desktop(desktop.slot));
    await observer.waitFor(() => observer.currentDesktopId() === desktop.id, `desktop ${desktop.slot} current`);
  };

  const split = async (desktop, from, key) => {
    await driver.click({ key: `tile.${from.pane}.header` });
    await observer.waitFor(() => observer.desktop(desktop.id)?.active_pane_id === from.pane, `${from.pane} active before the split`);
    const before = new Set(observer.desktop(desktop.id).panes.map((p) => p.pane_id));
    await driver.pressKey(key);
    const created = await observer.waitFor(
      () => observer.desktop(desktop.id)?.panes.find((p) => !before.has(p.pane_id) && p.runtime_id),
      `a new shell beside ${from.pane}`,
      20_000,
    );
    await driver.waitFor({ key: `tile.${created.pane_id}` });
    return { session: created.session_id, pane: created.pane_id, runtime: created.runtime_id };
  };

  const screen = (shell, includes) => driver.state(`terminal:${shell.runtime}`, { includes, timeoutMs: 15_000 });

  const writeToken = async (shell, name) => {
    await driver.click({ key: `tile.${shell.pane}.header` });
    await observer.waitFor(() => observer.desktopOf(shell.session)?.active_pane_id === shell.pane, `${shell.pane} active before typing`);
    await driver.typeInto(null, `echo ${name}-$((40 + 2))`);
    await driver.pressKey('Enter');
    await screen(shell, `${name}-42`);
    return `${name}-42`;
  };

  const shows = async (shells, hidden) => {
    for (const shell of shells) {
      await driver.waitFor({ key: `tile.${shell.pane}` });
      await driver.waitFor({ key: `sidebar.session.${shell.session}` });
    }
    for (const shell of hidden) await driver.waitFor({ key: `tile.${shell.pane}` }, { gone: true });
  };

  const holds = async (pairs) => {
    for (const [shell, token] of pairs) await screen(shell, token);
  };

  const startedOn = observer.currentDesktopId();
  if (startedOn) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(startedOn));

  const a = {};
  const b = {};
  const tokens = {};
  await runner.step('desktop_a_holds_three_shells', async () => {
    a.desktop = await makeDesktop();
    a.first = await spawnShell('alpha', a.desktop.id);
    await goTo(a.desktop);
    await driver.waitFor({ key: `tile.${a.first.pane}` });
    a.beside = await split(a.desktop, a.first, keys.splitBeside);
    a.below = await split(a.desktop, a.beside, keys.splitBelow);
    const shown = observer.desktop(a.desktop.id);
    runner.assert(shown.panes.length === 3, 'desktop A holds three shells', shown);
    tokens.a1 = await writeToken(a.first, 'split-a1');
    tokens.a2 = await writeToken(a.beside, 'split-a2');
    tokens.a3 = await writeToken(a.below, 'split-a3');
    await shows([a.first, a.beside, a.below], []);
  });

  await runner.step('desktop_b_holds_two_shells', async () => {
    b.desktop = await makeDesktop();
    b.first = await spawnShell('beta', b.desktop.id);
    await goTo(b.desktop);
    await driver.waitFor({ key: `tile.${b.first.pane}` });
    b.beside = await split(b.desktop, b.first, keys.splitBeside);
    tokens.b1 = await writeToken(b.first, 'split-b1');
    tokens.b2 = await writeToken(b.beside, 'split-b2');
  });

  await runner.step('each_desktop_shows_only_its_own_shells_and_their_scrollback', async () => {
    await goTo(a.desktop);
    await shows([a.first, a.beside, a.below], [b.first, b.beside]);
    await holds([[a.first, tokens.a1], [a.beside, tokens.a2], [a.below, tokens.a3]]);
    await goTo(b.desktop);
    await shows([b.first, b.beside], [a.first, a.beside, a.below]);
    await holds([[b.first, tokens.b1], [b.beside, tokens.b2]]);
  });

  const close = async (desktop, shell) => {
    await driver.click({ key: `tile.${shell.pane}.header` });
    await observer.waitFor(() => observer.desktop(desktop.id)?.active_pane_id === shell.pane, `${shell.pane} active before close`);
    await driver.pressKey(keys.close);
    await driver.waitFor({ key: `tile.${shell.pane}` }, { gone: true });
    await driver.waitFor({ key: `sidebar.session.${shell.session}` }, { gone: true });
    await observer.waitFor(() => !observer.getSession(shell.session), `session ${shell.session} closed`);
  };

  await runner.step('closing_one_split_keeps_the_other_shells_and_their_scrollback', async () => {
    await goTo(a.desktop);
    await close(a.desktop, a.beside);
    const left = observer.desktop(a.desktop.id);
    runner.assert(left.panes.length === 2 && !left.panes.some((p) => p.pane_id === a.beside.pane), 'desktop A keeps its two other shells', left);
    await shows([a.first, a.below], []);
    await holds([[a.first, tokens.a1], [a.below, tokens.a3]]);
  });

  await runner.step('closing_a_split_on_one_desktop_leaves_the_other_untouched', async () => {
    await goTo(b.desktop);
    await close(b.desktop, b.beside);
    await shows([b.first], []);
    await holds([[b.first, tokens.b1]]);
    await goTo(a.desktop);
    await shows([a.first, a.below], [b.first]);
    await holds([[a.first, tokens.a1], [a.below, tokens.a3]]);
  });

  await runner.step('focus_shows_one_shell_alone_and_returns_it_to_the_row', async () => {
    await driver.hover({ key: `tile.${a.first.pane}.header` });
    await driver.click({ key: `tile.${a.first.pane}.focus` });
    await driver.waitFor({ key: `tile.${a.below.pane}` }, { gone: true });
    await driver.waitFor({ key: `tile.${a.first.pane}.return` });
    await driver.waitFor({ key: 'sidebar' }, { gone: true });
    await observer.waitFor(() => observer.desktop(a.desktop.id)?.active_pane_id === a.first.pane, 'the focused shell is the active tile');
    await driver.screenshot(path.join(runner.runDir, 'focus-mode.png'));
    await driver.click({ key: `tile.${a.first.pane}.return` });
    await shows([a.first, a.below], []);
    await driver.waitFor({ key: `tile.${a.first.pane}.return` }, { gone: true });
    await driver.waitFor({ key: 'sidebar' });
  });

  await runner.step('leaving_the_desktop_ends_focus', async () => {
    await driver.hover({ key: `tile.${a.first.pane}.header` });
    await driver.click({ key: `tile.${a.first.pane}.focus` });
    await driver.waitFor({ key: `tile.${a.below.pane}` }, { gone: true });
    await goTo(b.desktop);
    await goTo(a.desktop);
    await shows([a.first, a.below], []);
    await driver.waitFor({ key: `tile.${a.first.pane}.return` }, { gone: true });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ a, b, tokens, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
