#!/usr/bin/env node

import { execFile } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import { launchFreshNativeAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { writeQueueAgentFixture } from './scenarioAgents.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const SHELLS = 5;
const NARROWEST_COLS = '66';
const execFileAsync = promisify(execFile);
const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SHELF-MOVES', tier: 'tier2-local-mock-agent', prefix: 'native-shelf-moves',
  metadata: { focus: 'On a native row of shells past the screen: a split at capacity keeps its anchor, close keeps focus in the slot, carry and send to the shelf keep the keyboard, documents agents open wait on the shelf or show beside an agent on screen and the jump key brings them without reordering the row, chips and the sidebar go to a tile, and chips say what needs the person' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const instance = currentHarnessInstance();
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const keys = process.platform === 'darwin'
  ? { Left: 'mod+alt+left', Right: 'mod+alt+right', split: 'mod+d', close: 'mod+w', fewer: 'mod+alt+-', more: 'mod+alt+=', carryRight: 'mod+alt+shift+right', send: 'mod+alt+shift+up', jump: 'mod+j', sidebar: 'mod+b', interrupt: 'ctrl+c' }
  : { Left: 'mod+shift+left', Right: 'mod+shift+right', split: 'mod+shift+d', close: 'mod+shift+w', fewer: 'mod+alt+-', more: 'mod+alt+=', carryRight: 'mod+alt+shift+right', send: 'mod+alt+shift+up', jump: 'mod+shift+j', sidebar: 'mod+alt+b', interrupt: 'ctrl+c' };

function leavesOf(desktop) {
  const walk = (node) => (node ? (node.children ? node.children.flatMap(walk) : [node.pane_id ?? node.tile_id]) : []);
  return walk(JSON.parse(desktop.tree_json || 'null'));
}

function keyedNodes(node, found = []) {
  if (node.key) found.push(node);
  for (const child of node.children ?? []) keyedNodes(child, found);
  return found;
}

async function screen() {
  const nodes = keyedNodes(await driver.tree());
  return nodes.filter((node) => /^tile\.[^.]+$/.test(node.key)).sort((a, b) => a.bounds[0] - b.bounds[0]).map((node) => node.key.slice('tile.'.length));
}

async function chip(leaf) {
  await driver.waitFor({ key: `shelf.chip.${leaf}` });
  const found = keyedNodes(await driver.tree()).find((node) => node.key === `shelf.chip.${leaf}`);
  return found?.value ?? '';
}

async function setNarrowest(value) {
  observer.send({ cmd: 'set_setting', key: 'narrowest_tile_cols', value });
  await observer.waitFor(() => observer.getSetting('narrowest_tile_cols') === value, `narrowest_tile_cols is ${value}`);
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    queueDaemonSettingRestore(observer, 'narrowest_tile_cols');
    await setNarrowest(NARROWEST_COLS);
    await driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 });
  });

  let desktop;
  const made = [];
  runner.registerCleanup('close_desktops', async () => {
    for (const id of made) {
      for (const pane of observer.desktop(id)?.panes ?? []) observer.unregisterSession(pane.session_id);
      await observer.waitFor(() => !observer.desktop(id) || observer.desktop(id).panes.length === 0, `no terminals left on ${id}`, 20_000);
      const left = observer.desktop(id);
      if (left) await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    }
  });

  const row = () => leavesOf(observer.desktop(desktop.id));
  const active = () => observer.desktop(desktop.id).active_pane_id;
  const keyboard = async () => (await driver.state('window')).keyboard?.pane ?? null;
  const sessionOf = (leaf) => observer.desktop(desktop.id).panes.find((pane) => pane.pane_id === leaf)?.session_id;
  const runtimeOf = (leaf) => observer.desktop(desktop.id).panes.find((pane) => pane.pane_id === leaf)?.runtime_id;
  const shot = (name) => driver.screenshot(path.join(runner.runDir, `${name}.png`));

  const landOn = async (leaf, why) => {
    await observer.waitFor(() => active() === leaf, why);
    await driver.waitFor({ key: `tile.${leaf}` });
    if (!sessionOf(leaf)) return;
    const holder = await keyboard();
    runner.assert(holder === leaf, `${why}: the keyboard goes with it`, { leaf, holder });
  };

  const walkTo = async (leaf) => {
    while (active() !== leaf) {
      const order = row();
      const step = order.indexOf(active()) < order.indexOf(leaf) ? 1 : -1;
      const next = order[order.indexOf(active()) + step];
      await driver.pressKey(step > 0 ? keys.Right : keys.Left);
      await landOn(next, `the row key shows ${next}`);
    }
  };

  const asked = (leaf) => {
    const find = (node) => (node ? (node.tile_id === leaf ? node : (node.children ?? []).map(find).find(Boolean)) : null);
    return find(JSON.parse(observer.desktop(desktop.id).tree_json || 'null'))?.show_requests ?? 0;
  };
  const tileCount = (count) => ({ role: 'status', name: `${count} tile${count === 1 ? '' : 's'}` });
  const waitForRow = (predicate, why) => observer.waitFor(() => predicate(row()) && row(), why);
  const offScreen = async () => {
    const shown = await screen();
    const leaf = row().find((id) => sessionOf(id) && !shown.includes(id));
    runner.assert(Boolean(leaf), 'a shell sits on the shelf', { shown, row: row() });
    return leaf;
  };

  const agentRunsOpen = async (ownerLeaf, fileName) => {
    const file = path.join(runner.sessionDir, fileName);
    fs.writeFileSync(file, `# ${fileName}\n\nAn agent opened this.\n`);
    const owner = sessionOf(ownerLeaf);
    const env = instanceCliEnv(instance, { ATTN_TERMINAL_ID: observer.terminalOf(owner) });
    await execFileAsync(appCliInTree(options.appPath), ['open', file, '--session', owner], { env });
  };
  const agentOpensDocument = async (ownerLeaf, fileName) => {
    const before = row();
    await agentRunsOpen(ownerLeaf, fileName);
    const after = await waitForRow((current) => current.length === before.length + 1, `${fileName} joins the row`);
    return after.find((id) => !before.includes(id));
  };
  const assertJumpedTo = async (leaf, rowBefore) => {
    const shown = await screen();
    runner.assert(shown.includes(leaf), 'the row slides to show the tile', { shown, leaf });
    runner.assert(row().join() === rowBefore.join(), 'going to the tile leaves the row order alone', { row: row(), rowBefore });
  };
  const closeDocument = async (leaf) => {
    if (!(await screen()).includes(leaf)) await driver.click({ key: `shelf.chip.${leaf}` });
    await driver.click({ key: `tile.${leaf}.header` });
    await observer.waitFor(() => active() === leaf, 'the document is active');
    await driver.pressKey(keys.close);
    await waitForRow((current) => !current.includes(leaf), 'the document leaves the row');
    await driver.waitFor({ key: `tile.${leaf}` }, { gone: true });
  };

  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    desktop = await observer.createDesktop();
    made.push(desktop.id);
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  await runner.step('a_row_of_shells_past_the_screen', async () => {
    let anchor = null;
    for (let index = 0; index < SHELLS; index += 1) {
      const id = crypto.randomUUID();
      const cwd = path.join(runner.sessionDir, `shell-${index}`);
      fs.mkdirSync(cwd, { recursive: true });
      const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, `shell ${index} spawned`);
      const placement = anchor ? { desktop_id: desktop.id, direction: 'vertical', anchor_pane_id: anchor } : { desktop_id: desktop.id };
      observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement, label: `shell ${index}`, cols: 80, rows: 24 });
      const result = await spawned;
      runner.assert(result.success && result.pane_id, `spawn_session starts shell ${index}`, result);
      anchor = result.pane_id;
    }
    await waitForRow((current) => current.length === SHELLS, `${SHELLS} shells in the row`);
    await driver.waitFor({ key: 'shelf' });
    await driver.click({ key: `tile.${active()}.header` });
    await landOn(active(), 'a header press gives the active tile the keyboard');
  });

  await runner.step('a_split_at_capacity_keeps_the_anchor_on_screen', async () => {
    await walkTo((await screen()).at(-1));
    const anchor = active();
    const before = row();
    const shownBefore = await screen();
    await driver.pressKey(keys.split);
    const after = await waitForRow((current) => current.length === before.length + 1, 'the split joins the row');
    const opened = after.find((id) => !before.includes(id));
    await landOn(opened, 'the split shell is active');
    const shown = await screen();
    runner.assert(after[after.indexOf(anchor) + 1] === opened, 'the split opens beside the active column', { after, anchor, opened });
    runner.assert(shown.includes(anchor) && shown.includes(opened), 'the anchor stays on screen beside the new tile', { shown, anchor, opened });
    runner.assert(shown.length === shownBefore.length, 'the screen keeps its tile count', { shown, shownBefore });
    const left = shownBefore.find((id) => !shown.includes(id));
    runner.log('the column that left', { left, value: await chip(left) });
    await shot('split-at-capacity');
  });

  await runner.step('close_keeps_focus_in_the_slot', async () => {
    const closing = row()[1];
    await walkTo(closing);
    const shownBefore = await screen();
    const slot = shownBefore.indexOf(closing);
    await driver.pressKey(keys.close);
    await waitForRow((current) => !current.includes(closing), 'the closed shell leaves the row');
    await driver.waitFor({ key: `tile.${closing}` }, { gone: true });
    await landOn(active(), 'a tile takes over');
    const after = await screen();
    runner.assert(after[slot] === active(), 'close leaves focus in the slot', { shownBefore, after, slot, active: active() });
  });

  await runner.step('carry_and_send_through_the_keys', async () => {
    const moving = active();
    const at = row().indexOf(moving);
    runner.assert(at < row().length - 1, 'there is a column to carry past', { row: row(), moving });
    await driver.pressKey(keys.carryRight);
    await waitForRow((current) => current.indexOf(moving) === at + 1, 'carry moves the column one place right');
    await landOn(moving, 'the carried tile stays active');

    await driver.pressKey(keys.send);
    await driver.waitFor({ key: `tile.${moving}` }, { gone: true });
    await chip(moving);
    await observer.waitFor(() => active() !== moving, 'the next tile takes over');
    const next = active();
    await landOn(next, 'the next tile takes the keyboard');
    await shot('sent-to-shelf');

    await driver.click({ key: `shelf.chip.${moving}` });
    await landOn(moving, 'the chip brings the tile back');
    const brought = await screen();
    runner.assert(brought.includes(next) && brought.includes(moving), 'a chip brings the tile beside the active one, which stays on screen', { brought, next, moving });
  });

  await runner.step('an_agent_opening_a_document_while_the_user_types_keeps_the_keys_in_the_terminal', async () => {
    const typing = active();
    const runtime = runtimeOf(typing);
    const before = row();
    const shownBefore = await screen();
    await driver.typeInto(null, 'echo first');
    const opened = agentRunsOpen(typing, 'plan.md');
    await driver.typeInto(null, 'half');
    await opened;
    await driver.typeInto(null, 'second');
    const after = await waitForRow((current) => current.length === before.length + 1, 'the document joins the row');
    const documentLeaf = after.find((id) => !before.includes(id));
    await driver.waitFor({ key: `tile.${documentLeaf}` });
    await driver.state(`terminal:${runtime}`, { includes: 'echo firsthalfsecond' });
    runner.assert(active() === typing, 'the document takes no focus', { typing, active: active() });
    runner.assert((await keyboard()) === typing, 'the keyboard stays in the terminal', { typing });
    runner.assert((await screen()).includes(documentLeaf), 'the document shows beside its agent', { documentLeaf });
    await shot('document-beside-the-agent');
    await driver.pressKey(keys.interrupt);

    await closeDocument(documentLeaf);
    await driver.click({ key: `tile.${typing}.header` });
    await landOn(typing, 'the agent is active again');
    const shownAfter = await screen();
    runner.assert(shownAfter.join() === shownBefore.join(), 'closing the document restores the screen', { shownBefore, shownAfter });
  });

  await runner.step('the_jump_key_brings_an_unseen_document_and_closing_restores_the_screen', async () => {
    await walkTo((await screen()).at(-1));
    const before = row();
    const shownBefore = await screen();
    const owner = await offScreen();
    const was = active();
    const documentLeaf = await agentOpensDocument(owner, 'waiting.md');
    runner.assert((await chip(documentLeaf)).includes('needs you'), 'the document waits on the shelf as needing the person');
    runner.assert((await screen()).join() === shownBefore.join(), 'the screen stays as it was', { shownBefore });
    runner.assert(active() === was, 'focus stays as it was', { was });
    await shot('document-waiting-on-the-shelf');

    const rowBeforeJump = row();
    await driver.pressKey(keys.jump);
    await landOn(documentLeaf, 'the jump key shows the document');
    await assertJumpedTo(documentLeaf, rowBeforeJump);
    await driver.waitFor({ key: `shelf.chip.${documentLeaf}` }, { gone: true });
    await shot('document-brought');

    await driver.pressKey(keys.close);
    await waitForRow((current) => current.length === before.length, 'the document leaves the row');
    await driver.waitFor({ key: `tile.${documentLeaf}` }, { gone: true });
    await landOn(was, 'the tile active before the jump is active again');
    const shownAfter = await screen();
    runner.assert(shownAfter.join() === shownBefore.join(), 'closing the document restores the screen', { shownBefore, shownAfter });
  });

  await runner.step('a_document_sent_to_the_shelf_waits_when_its_agent_opens_it_again', async () => {
    const owner = await offScreen();
    const documentLeaf = await agentOpensDocument(owner, 'again.md');
    runner.assert((await chip(documentLeaf)).includes('needs you'), 'the document waits on the shelf');
    await driver.pressKey(keys.jump);
    await landOn(documentLeaf, 'the jump key shows the document');
    await driver.pressKey(keys.send);
    await driver.waitFor({ key: `tile.${documentLeaf}` }, { gone: true });
    runner.assert(!(await chip(documentLeaf)).includes('needs you'), 'a document the person saw and sent away waits as seen');
    await walkTo((await screen()).at(-1));
    const was = active();
    const shownBefore = await screen();

    const askedBefore = asked(documentLeaf);
    await agentRunsOpen(owner, 'again.md');
    await observer.waitFor(() => asked(documentLeaf) > askedBefore, 'the agent asks to show the document again');
    runner.assert((await chip(documentLeaf)).includes('needs you'), 'asking again makes it wait as unseen');
    runner.assert((await screen()).join() === shownBefore.join(), 'the screen stays as it was', { shownBefore });
    runner.assert(active() === was, 'focus stays as it was', { was });
    await shot('document-waiting-again');

    const rowBeforeJump = row();
    await driver.pressKey(keys.jump);
    await landOn(documentLeaf, 'the jump key shows it again');
    await assertJumpedTo(documentLeaf, rowBeforeJump);
    await driver.pressKey(keys.close);
    await waitForRow((current) => !current.includes(documentLeaf), 'the document leaves the row');
    await landOn(was, 'the tile active before the jump is active again');
  });

  await runner.step('more_tiles_reveal_an_agent_whose_document_then_shows', async () => {
    await driver.pressKey(keys.sidebar);
    await driver.waitFor({ key: 'sidebar' }, { gone: true });
    await walkTo(row()[0]);
    await walkTo((await screen()).at(-1));
    const was = active();
    const narrow = await screen();
    runner.assert(narrow.at(-1) === was, 'the active tile sits at the end of the screen', { narrow, was });
    await driver.pressKey(keys.more);
    await driver.waitFor(tileCount(narrow.length + 1));
    const wide = await screen();
    runner.assert(wide.length === narrow.length + 1, 'more tiles shows one more', { narrow, wide });
    const revealed = wide.find((id) => !narrow.includes(id));
    runner.assert(wide.indexOf(revealed) > wide.indexOf(was), 'more tiles reveals an agent after the active tile', { wide, was, revealed });
    const documentLeaf = await agentOpensDocument(revealed, 'revealed.md');
    await driver.waitFor({ key: `tile.${documentLeaf}` });
    const shown = await screen();
    runner.assert(shown.includes(was) && shown.includes(revealed) && shown.includes(documentLeaf), 'the document shows beside its newly revealed agent, with the active tile on screen', { shown, was, revealed, documentLeaf });
    runner.assert(active() === was, 'the document takes no focus', { was });
    await shot('document-of-a-revealed-agent');
    await closeDocument(documentLeaf);
    await driver.pressKey(keys.fewer);
    await driver.waitFor(tileCount(narrow.length));
    await driver.pressKey(keys.sidebar);
    await driver.waitFor({ key: 'sidebar' });
  });

  await runner.step('a_document_opened_while_another_desktop_is_current_waits', async () => {
    const owner = await offScreen();
    const was = active();
    const away = await observer.createDesktop();
    made.push(away.id);
    await driver.click({ key: `sidebar.desktop.${away.id}` });
    await observer.waitFor(() => observer.currentDesktopId() === away.id, 'another desktop is current');
    await driver.waitFor({ key: 'launcher.path.field' });
    const documentLeaf = await agentOpensDocument(owner, 'away.md');
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await observer.waitFor(() => observer.currentDesktopId() === desktop.id, 'the row is current again');
    await driver.waitFor({ key: `tile.${was}` });
    runner.assert((await chip(documentLeaf)).includes('needs you'), 'the document waits on the shelf');
    await shot('document-waiting-after-away');
    const rowBeforeJump = row();
    await driver.pressKey(keys.jump);
    await landOn(documentLeaf, 'the jump key shows the document');
    await assertJumpedTo(documentLeaf, rowBeforeJump);
    await driver.pressKey(keys.close);
    await waitForRow((current) => !current.includes(documentLeaf), 'the document leaves the row');
  });

  await runner.step('a_brought_tile_replaces_a_neighbour_and_the_selected_tile_stays', async () => {
    const [first] = row();
    await walkTo(first);
    await shot('bring-selected-on-the-left-before');
    const fromLeft = await offScreen();
    await driver.click({ key: `shelf.chip.${fromLeft}` });
    await landOn(fromLeft, 'the chip brings its tile');
    const afterLeft = await screen();
    runner.assert(afterLeft[0] === first && afterLeft[1] === fromLeft, 'the selected tile on the left stays and the right neighbour is replaced', { afterLeft, first, fromLeft });
    await shot('bring-selected-on-the-left-after');

    const fromRight = await offScreen();
    await driver.click({ key: `shelf.chip.${fromRight}` });
    await landOn(fromRight, 'the chip brings its tile');
    const afterRight = await screen();
    runner.assert(afterRight[0] === fromRight && afterRight[1] === fromLeft, 'the selected tile on the right stays and the left neighbour is replaced', { afterRight, fromLeft, fromRight });
    await shot('bring-selected-on-the-right-after');
  });

  await runner.step('chips_say_what_needs_the_person', async () => {
    const id = crypto.randomUUID();
    const cwd = path.join(runner.sessionDir, 'waiting-agent');
    fs.mkdirSync(cwd, { recursive: true });
    writeQueueAgentFixture(cwd);
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'the agent spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id, agent: 'claude', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id, direction: 'vertical', anchor_pane_id: row().at(-1) }, label: 'Waiting agent', cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, 'spawn_session starts the agent', result);
    const agent = result.pane_id;
    await waitForRow((current) => current.includes(agent), 'the agent joins the row');
    await walkTo(agent);
    await driver.click({ key: `tile.${agent}.header` });
    await landOn(agent, 'a header press gives the agent the keyboard');
    await driver.state(`terminal:${runtimeOf(agent)}`, { includes: '❯', timeoutMs: 60_000 });
    await driver.typeInto(null, 'HAND_IT_BACK');
    await driver.pressKey('enter');
    await observer.waitFor(() => observer.getSession(id)?.state === 'waiting_input', 'the agent waits for the person', 30_000);
    await walkTo(row()[0]);
    const shown = await screen();
    runner.assert(!shown.includes(agent), 'the agent sits on the shelf', { shown, agent });
    const plain = row().find((leaf) => leaf !== agent && sessionOf(leaf) && !shown.includes(leaf));
    const documentLeaf = await agentOpensDocument(plain, 'unseen.md');
    runner.assert((await chip(agent)).includes('needs you'), 'a waiting agent\'s chip needs the person', { value: await chip(agent) });
    runner.assert((await chip(documentLeaf)).includes('needs you'), 'an unseen document\'s chip needs the person');
    runner.assert(!(await chip(plain)).includes('needs you'), 'a plain chip does not');
    await shot('attention-on-the-shelf');
    await closeDocument(documentLeaf);
  });

  await runner.step('going_to_a_tile_from_the_sidebar_or_a_chip_leaves_the_row_alone', async () => {
    const rowBeforeSidebar = row();
    const fromSidebar = await offScreen();
    await driver.click({ key: `sidebar.session.${sessionOf(fromSidebar)}` });
    await landOn(fromSidebar, 'the sidebar row shows its tile');
    await assertJumpedTo(fromSidebar, rowBeforeSidebar);
    await shot('jump-from-sidebar');

    for (let count = (await screen()).length; count > 1; count -= 1) {
      await driver.pressKey(keys.fewer);
      await driver.waitFor(tileCount(count - 1));
    }
    const rowBeforeChip = row();
    const [only] = await screen();
    const fromChip = rowBeforeChip[rowBeforeChip.indexOf(only) + 1] ?? rowBeforeChip[rowBeforeChip.indexOf(only) - 1];
    await driver.click({ key: `shelf.chip.${fromChip}` });
    await landOn(fromChip, 'the chip shows its tile');
    await assertJumpedTo(fromChip, rowBeforeChip);
    runner.assert((await screen()).join() === fromChip, 'a chip with one tile on screen shows only that tile', { fromChip });
    await shot('jump-from-chip-with-one-tile');
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, row: row(), frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
