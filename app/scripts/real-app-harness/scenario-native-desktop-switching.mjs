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
  scenarioId: 'NATIVE-DESKTOP-SWITCHING', tier: 'tier1-local-shell', prefix: 'native-desktop-switching',
  metadata: { focus: 'Desktop keys in the native app switch, move a tile (following or staying, creating the desktop on an empty digit) and bounce; the overview switches; the app follows another client; terminals keep their scrollback across moves; a document tile is an ordinary leaf that takes the keyboard and leaves no stale focus behind' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const keys = process.platform === 'darwin'
  ? { select: (n) => `mod+${n}`, send: (n) => `mod+alt+${n}`, stay: (n) => `mod+alt+shift+${n}`, overview: 'mod+g', split: 'mod+d', focus: 'mod+shift+enter', agents: 'mod+k' }
  : { select: (n) => `mod+shift+${n}`, send: (n) => `mod+alt+${n}`, stay: (n) => `mod+alt+shift+${n}`, overview: 'mod+shift+g', split: 'mod+shift+d', focus: 'mod+alt+enter', agents: 'mod+shift+k' };

function treeHolds(desktop, leafId) {
  return Boolean(desktop?.tree_json?.includes(`"${leafId}"`));
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_two_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  const made = [];
  const before = observer.currentDesktopId();
  if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
  const sessions = [];
  runner.registerCleanup('unregister_sessions', async () => {
    for (const id of sessions) if (observer.getSession(id)) await observer.unregisterSession(id);
  });
  runner.registerCleanup('close_desktops', async () => {
    for (const id of made) {
      const left = observer.desktop(id);
      if (!left) continue;
      await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
      await observer.waitFor(() => !observer.desktop(id), `desktop ${id} closed`);
    }
  });

  let desktopA;
  let desktopB;
  await runner.step('make_two_named_desktops_with_shortcuts', async () => {
    for (const name of ['Switch A', 'Switch B']) {
      const desktop = await observer.createDesktop();
      made.push(desktop.id);
      await observer.profileCommand('desktop_rename', { desktop_id: desktop.id, name });
      runner.assert(desktop.shortcut_slot, `The run needs two free desktop shortcuts; the profile has none left:\n${observer.describeArrangement()}`, observer.describeArrangement());
    }
    [desktopA, desktopB] = made.map((id) => observer.desktop(id));
    await driver.waitFor({ key: `sidebar.desktop.${desktopB.id}` });
  });

  const shows = async (desktopId, pane) => {
    await observer.waitFor(() => observer.currentDesktopId() === desktopId, `desktop ${desktopId} is current`);
    if (pane) await driver.waitFor({ key: `tile.${pane}` });
  };

  await runner.step('switch_with_the_desktop_shortcut', async () => {
    await driver.pressKey(keys.select(desktopA.shortcut_slot));
    await shows(desktopA.id);
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const takesKeyboard = async (pane) => {
    const window = await driver.state('window', { includes: pane });
    runner.assert(window.keyboard?.pane === pane, `tile ${pane} holds the keyboard`, window);
  };
  const keyboardLeft = async (pane) => {
    const window = await driver.state('window');
    runner.assert(window.keyboard?.pane !== pane, `the keyboard left tile ${pane}`, window);
  };
  const runtimeOf = async (pane) => {
    const desktops = await driver.state('desktops');
    return desktops.flatMap((desktop) => desktop.panes ?? []).find((p) => p.pane_id === pane)?.runtime_id;
  };
  const screen = async (shell, includes) => driver.state(`terminal:${shell.runtime}`, { includes, timeoutMs: 15_000 });
  const writeToken = async (shell, name) => {
    await driver.click({ key: `tile.${shell.pane}.header` });
    await takesKeyboard(shell.pane);
    await driver.typeInto(null, `echo ${name}_$((40 + 2))`);
    await driver.pressKey('Enter');
    await screen(shell, `${name}_42`);
    return `${name}_42`;
  };

  let first;
  let split;
  const tokens = {};
  await runner.step('a_shell_and_a_split_beside_it_on_the_current_desktop', async () => {
    const id = crypto.randomUUID();
    const cwd = path.join(runner.sessionDir, 'shells');
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'first shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktopA.id }, label: 'first', cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, 'spawn_session starts the first shell', result);
    sessions.push(id);
    await driver.waitFor({ key: `tile.${result.pane_id}` });
    first = { id, pane: result.pane_id, runtime: await runtimeOf(result.pane_id) };
    tokens.first = await writeToken(first, 'DESK_A');

    await driver.pressKey(keys.split);
    const withSplit = await observer.waitFor(() => {
      const pane = observer.desktop(desktopA.id)?.panes.find((p) => p.session_id && p.session_id !== id);
      return pane;
    }, 'the split shell on desktop A', 30_000);
    sessions.push(withSplit.session_id);
    await driver.waitFor({ key: `tile.${withSplit.pane_id}` });
    split = { id: withSplit.session_id, pane: withSplit.pane_id, runtime: await runtimeOf(withSplit.pane_id) };
    tokens.split = await writeToken(split, 'DESK_SPLIT');
  });

  await runner.step('move_the_split_to_desktop_b_and_stay', async () => {
    await driver.click({ key: `tile.${split.pane}.header` });
    await observer.waitForDesktopOf(split.id, (entry) => entry.active_pane_id === split.pane, 'the split is the active tile');
    await driver.pressKey(keys.stay(desktopB.shortcut_slot));
    await observer.waitForDesktopOf(split.id, (entry) => entry.id === desktopB.id, 'the split on desktop B');
    await driver.waitFor({ key: `tile.${split.pane}` }, { gone: true });
    await driver.waitFor({ key: `tile.${first.pane}` });
    runner.assert(
      observer.currentDesktopId() === desktopA.id && observer.desktopOf(first.id)?.id === desktopA.id,
      'moving with shift leaves the user and the other shell on desktop A',
      observer.describeArrangement(),
    );
  });

  await runner.step('switch_to_b_shows_the_moved_shell_with_its_scrollback', async () => {
    await driver.pressKey(keys.select(desktopB.shortcut_slot));
    await shows(desktopB.id, split.pane);
    await driver.waitFor({ key: `tile.${first.pane}` }, { gone: true });
    await screen(split, tokens.split);
  });

  await runner.step('the_current_digit_bounces_back', async () => {
    await driver.pressKey(keys.select(desktopB.shortcut_slot));
    await shows(desktopA.id, first.pane);
    await screen(first, tokens.first);
  });

  const marker = `SWITCH_${crypto.randomUUID().slice(0, 8)}`;
  await runner.step('typing_right_after_the_switch_reaches_the_shell_shown', async () => {
    await driver.pressKey(keys.select(desktopB.shortcut_slot));
    await driver.typeInto(null, `# ${marker}`);
    await driver.pressKey('Enter');
    await observer.waitFor(() => observer.currentDesktopId() === desktopB.id, 'desktop B is current');
    await screen(split, marker);
  });

  await runner.step('the_overview_switches_desktops', async () => {
    await driver.pressKey(keys.overview);
    await driver.waitFor({ key: 'overview' });
    await driver.click({ key: `overview.card.${desktopA.id}` });
    await driver.waitFor({ key: 'overview' }, { gone: true });
    await shows(desktopA.id, first.pane);
    const rows = await screen(first, tokens.first);
    runner.assert(!rows.join('').includes(marker), 'the shell left behind received no input meant for desktop B', rows);
  });

  await runner.step('the_app_follows_another_client', async () => {
    await observer.setCurrentDesktop(desktopB.id);
    await driver.waitFor({ key: `tile.${split.pane}` });
    await driver.waitFor({ key: `tile.${first.pane}` }, { gone: true });
    runner.assert(observer.currentDesktopId() === desktopB.id, 'the app does not push back against the other client', observer.describeArrangement());
  });

  const doc = `tile-${crypto.randomUUID()}`;
  const docName = `notes-${crypto.randomUUID().slice(0, 8)}.md`;
  await runner.step('going_to_a_document_from_the_palette_makes_it_the_active_tile_and_takes_the_keyboard_off_the_shell', async () => {
    await driver.click({ key: `tile.${split.pane}.header` });
    await driver.pressKey(keys.focus);
    await driver.waitFor({ key: `tile.${split.pane}.return` });
    const notes = path.join(runner.sessionDir, 'shells', docName);
    fs.writeFileSync(notes, '# Leaf notes\n\nDocked content.\n');
    await observer.profileCommand('desktop_dock_tile', { desktop_id: desktopB.id, anchor_id: split.pane, edge: 'right', tile_id: doc, tile_kind: 'markdown', tile_params: notes });
    await driver.state('desktops', { includes: doc });
    runner.assert(!(await driver.find({ key: `tile.${doc}` })), 'a document arriving beside the shell shown alone leaves it alone', await driver.state('window'));
    await driver.waitFor({ key: `tile.${split.pane}.return` });

    await driver.pressKey(keys.agents);
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, docName);
    await driver.waitFor({ key: `palette.row.tile:${desktopB.id}:${doc}` });
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'palette' }, { gone: true });
    const tile = await driver.waitFor({ key: `tile.${doc}` });
    runner.assert(tile.name === docName, 'the tile goes by its file name', tile);
    await keyboardLeft(split.pane);
    await observer.waitFor(() => observer.desktop(desktopB.id)?.active_pane_id === doc, 'the document is the active tile');
  });

  await runner.step('the_document_moves_to_desktop_a_and_the_user_follows', async () => {
    await driver.pressKey(keys.send(desktopA.shortcut_slot));
    await observer.waitFor(() => treeHolds(observer.desktop(desktopA.id), doc) && observer.currentDesktopId() === desktopA.id, 'the document on desktop A, shown');
    await driver.waitFor({ key: `tile.${doc}` });
    await keyboardLeft(split.pane);
  });

  let desktopC;
  await runner.step('the_document_moves_to_a_digit_with_no_desktop_and_the_user_follows', async () => {
    const held = new Set(observer.desktops.map((desktop) => desktop.shortcut_slot).filter(Boolean));
    const slot = [1, 2, 3, 4, 5, 6, 7, 8, 9].find((candidate) => !held.has(candidate));
    runner.assert(Boolean(slot), `The run needs a third free desktop shortcut:\n${observer.describeArrangement()}`, observer.describeArrangement());
    await driver.pressKey(keys.send(slot));
    desktopC = await observer.waitFor(() => observer.desktops.find((desktop) => desktop.shortcut_slot === slot), `a desktop made on slot ${slot}`);
    made.push(desktopC.id);
    await observer.waitFor(() => treeHolds(observer.desktop(desktopC.id), doc) && observer.currentDesktopId() === desktopC.id, 'the document alone on the new desktop, shown');
    await driver.waitFor({ key: `tile.${doc}` });
    await keyboardLeft(split.pane);
    runner.assert(observer.desktop(desktopC.id).panes.length === 0, 'the new desktop holds only the document', observer.describeArrangement());
  });

  await runner.step('returning_to_the_shell_leaves_no_stale_focus', async () => {
    await driver.click({ key: `sidebar.session.${split.id}` });
    await shows(desktopB.id, split.pane);
    await driver.waitFor({ key: `tile.${split.pane}.focus` });
    runner.assert(!(await driver.find({ key: `tile.${split.pane}.return` })), 'the shell is back in its row, not shown alone', await driver.state('window'));
    await screen(split, tokens.split);
  });

  const screenshot = path.join(runner.runDir, 'native-desktop-switching.png');
  await runner.step('capture_window', () => driver.screenshot(screenshot));

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktops: { a: desktopA.id, b: desktopB.id, c: desktopC.id }, first, split, doc, tokens, screenshot });
} catch (error) {
  await runner.finishFailure(error, { arrangement: observer.describeArrangement() });
  process.exitCode = 1;
}
