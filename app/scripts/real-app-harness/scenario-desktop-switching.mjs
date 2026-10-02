#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  pressShortcutKeys,
  shownAgentId,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv, socketPathForInstance } from './harnessInstance.mjs';
import { createWindowDriver } from './platform.mjs';
import { waitForPaneAttached, waitForPaneShellReady, waitForPaneText } from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const ATTN_BIN = path.join(path.dirname(fileURLToPath(import.meta.url)), '../../../attn');

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

async function waitForAppState(client, predicate, description, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_state');
    if (predicate(last)) return last;
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${description}. Last app state:\n${JSON.stringify(last?.arrangement ?? last, null, 2)}`);
}

function shownDesktop(state) {
  return state.arrangement.desktops.find((desktop) => desktop.id === state.arrangement.currentDesktopId);
}

function desktopHolding(state, sessionId) {
  return state.arrangement.desktops.find((desktop) => desktop.panes.some((pane) => pane.sessionId === sessionId));
}

async function waitForShown(client, desktopId, sessionId, description) {
  return waitForAppState(
    client,
    (state) => {
      const shown = shownDesktop(state);
      return shown?.id === desktopId && shown.visible && (sessionId === undefined || shownAgentId(state) === sessionId);
    },
    description,
  );
}

async function waitForDesktopUi(client, desktopId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_desktop_ui_state', { desktopId }).catch((error) => ({ error: String(error) }));
    if (predicate(last)) return last;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last desktop UI state:\n${JSON.stringify(last, null, 2)}`);
}

async function focusModeOf(client, sessionId) {
  const snapshot = await client.request('capture_structured_snapshot', { includePaneText: false });
  return snapshot.sessions.find((session) => session.id === sessionId);
}

function mountedDesktopIds(state) {
  return state.arrangement.desktops.filter((desktop) => desktop.mounted).map((desktop) => desktop.id).sort();
}

async function writeToken(client, sessionId, paneId, token) {
  await client.request('write_pane', { sessionId, paneId, text: `printf '${token}\\n'` });
  await waitForPaneText(client, sessionId, paneId, (text) => text.includes(token), `pane ${paneId} shows ${token}`, 20_000);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-desktop-switching.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'DESKTOP-SWITCHING',
    tier: 'tier1-local-shell',
    prefix: 'desktop-switching',
    metadata: {
      agent: 'shell',
      focus: 'desktop shortcuts switch, move (following or staying, creating the desktop on an empty digit) and bounce through the daemon; the overview switches; the app follows another client (the harness observer); terminals keep their scrollback across moves; a tile is an ordinary leaf that takes the keyboard and leaves no stale agent focus mode',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const createdSessionIds = [];
  const createdDesktopIds = [];

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('delete_desktops', async () => {
    await observer.unregisterMatchingSessions((session) => createdSessionIds.includes(session.id)).catch(() => {});
    for (const desktopId of createdDesktopIds) {
      await observer
        .waitFor(() => observer.desktop(desktopId)?.panes.length === 0, `desktop ${desktopId} to empty`, 10_000)
        .then(() => observer.deleteDesktop(desktopId))
        .catch((error) => runner.log('desktop cleanup failed', { desktopId, error: String(error) }));
    }
  });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    let desktopA;
    let desktopB;
    const fixtureNotes = path.join(runner.sessionDir, 'desktop-fixture.md');
    fs.writeFileSync(fixtureNotes, '# Desktop switching fixture\n');
    const holdDesktop = async (desktop) => {
      const tileId = `fixture-${desktop.id}`;
      await observer.profileCommand('desktop_dock_tile', {
        desktop_id: desktop.id, expected_revision: observer.desktop(desktop.id).revision,
        tile_id: tileId, tile_kind: 'markdown', tile_params: fixtureNotes, edge: 'right',
      });
      await observer.waitFor(() => observer.desktop(desktop.id)?.active_pane_id === tileId, 'fixture tile docked');
    };
    const releaseDesktop = async (desktop) => {
      await observer.profileCommand('desktop_remove_leaf', {
        desktop_id: desktop.id, expected_revision: observer.desktop(desktop.id).revision,
        leaf_id: `fixture-${desktop.id}`,
      });
    };
    await runner.step('create_two_slotted_desktops', async () => {
      desktopA = await observer.createDesktop(`harness-a-${runner.runId}`);
      createdDesktopIds.push(desktopA.id);
      await holdDesktop(desktopA);
      desktopB = await observer.createDesktop(`harness-b-${runner.runId}`);
      createdDesktopIds.push(desktopB.id);
      await holdDesktop(desktopB);
      runner.assert(
        desktopA.shortcut_slot && desktopB.shortcut_slot,
        `The run needs two free desktop shortcuts; the profile has none left:\n${observer.describeArrangement()}`,
        observer.describeArrangement(),
      );
    });

    await runner.step('switch_with_the_desktop_shortcut', async () => {
      await pressShortcutKeys(client, driver, `desktop.select${desktopA.shortcut_slot}`);
      await waitForShown(client, desktopA.id, undefined, `desktop A shown after its shortcut`);
    });

    let first;
    let split;
    const tokens = { first: `DESK_A_${Date.now()}`, split: `DESK_SPLIT_${Date.now()}` };
    await runner.step('launch_two_shells_on_the_current_desktop', async () => {
      const cwd = path.join(runner.sessionDir, 'shells');
      fs.mkdirSync(cwd, { recursive: true });
      const firstSessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `desk-first-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        ownDesktop: false,
      });
      createdSessionIds.push(firstSessionId);
      const placed = await waitForAppState(
        client,
        (state) => desktopHolding(state, firstSessionId)?.id === desktopA.id,
        'the first shell placed on desktop A',
      );
      first = { sessionId: firstSessionId, paneId: desktopHolding(placed, firstSessionId).panes.find((pane) => pane.sessionId === firstSessionId).paneId };
      await releaseDesktop(desktopA);
      await client.request('select_session', { sessionId: first.sessionId });
      await waitForPaneAttached(client, first.sessionId, first.paneId, 20_000);
      await waitForPaneShellReady(client, first.sessionId, first.paneId, { timeoutMs: 20_000, description: 'first shell ready' });
      await writeToken(client, first.sessionId, first.paneId, tokens.first);

      await client.request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
      const withSplit = await waitForAppState(
        client,
        (state) => state.arrangement.desktops.find((desktop) => desktop.id === desktopA.id)?.panes.length === 2,
        'a split shell on desktop A',
        30_000,
      );
      const splitPane = withSplit.arrangement.desktops
        .find((desktop) => desktop.id === desktopA.id)
        ?.panes.find((pane) => pane.sessionId !== first.sessionId);
      runner.assert(Boolean(splitPane), `No split pane on desktop A: ${JSON.stringify(withSplit.arrangement, null, 2)}`, withSplit.arrangement);
      split = { sessionId: splitPane.sessionId, paneId: splitPane.paneId };
      createdSessionIds.push(split.sessionId);
      await waitForPaneAttached(client, split.sessionId, split.paneId, 20_000);
      await waitForPaneShellReady(client, split.sessionId, split.paneId, { timeoutMs: 20_000, description: 'split shell ready' });
      await writeToken(client, split.sessionId, split.paneId, tokens.split);
    });

    await runner.step('move_the_split_pane_to_desktop_b_and_stay', async () => {
      await client.request('focus_pane', { sessionId: split.sessionId, paneId: split.paneId });
      await waitForAppState(client, (state) => shownAgentId(state) === split.sessionId, 'split pane focused');
      await pressShortcutKeys(client, driver, `desktop.sendStay${desktopB.shortcut_slot}`);
      const moved = await waitForAppState(
        client,
        (state) => desktopHolding(state, split.sessionId)?.id === desktopB.id,
        'the split pane on desktop B',
      );
      await releaseDesktop(desktopB);
      runner.assert(
        moved.arrangement.currentDesktopId === desktopA.id && desktopHolding(moved, first.sessionId)?.id === desktopA.id,
        `Moving a pane with shift moved the user or the other pane:\n${JSON.stringify(moved.arrangement, null, 2)}`,
        moved.arrangement,
      );
    });

    await runner.step('switch_to_b_keeps_a_mounted_as_the_bounce_target', async () => {
      await pressShortcutKeys(client, driver, `desktop.select${desktopB.shortcut_slot}`);
      const state = await waitForShown(client, desktopB.id, split.sessionId, 'desktop B shown with the moved shell selected');
      runner.assert(
        state.arrangement.previousDesktopId === desktopA.id
          && mountedDesktopIds(state).join(',') === [desktopA.id, desktopB.id].sort().join(','),
        `Expected exactly desktops A and B mounted:\n${JSON.stringify(state.arrangement, null, 2)}`,
        state.arrangement,
      );
      await waitForPaneText(client, split.sessionId, split.paneId, (text) => text.includes(tokens.split), 'moved shell kept its scrollback', 15_000);
    });

    await runner.step('the_current_digit_bounces_back', async () => {
      await pressShortcutKeys(client, driver, `desktop.select${desktopB.shortcut_slot}`);
      await waitForShown(client, desktopA.id, first.sessionId, 'desktop A shown after bouncing');
      await waitForPaneText(client, first.sessionId, first.paneId, (text) => text.includes(tokens.first), 'first shell kept its scrollback', 15_000);
    });

    await runner.step('the_overview_switches_desktops', async () => {
      await pressShortcutKeys(client, driver, 'desktop.overview');
      await client.request('dom_wait', { selector: '.desktop-overview', timeoutMs: 5_000 });
      await client.request('dom_click', {
        selector: `.desktop-overview-card[data-desktop-id="${desktopB.id}"] .desktop-overview-open`,
      });
      await waitForShown(client, desktopB.id, split.sessionId, 'desktop B shown from the overview');
      await client.request('dom_wait', { selector: '.desktop-overview', absent: true, timeoutMs: 5_000 });
    });

    await runner.step('the_app_follows_another_client', async () => {
      await observer.setCurrentDesktop(desktopA.id);
      await waitForShown(client, desktopA.id, first.sessionId, 'desktop A shown after another client switched');
      const settled = await client.request('get_state');
      runner.assert(
        observer.currentDesktopId() === desktopA.id && settled.arrangement.currentDesktopId === desktopA.id,
        `The app pushed back against another client's switch:\n${observer.describeArrangement()}`,
        observer.describeArrangement(),
      );
    });

    let tileId;
    await runner.step('a_docked_tile_is_the_active_leaf_and_takes_the_keyboard', async () => {
      await client.request('select_session', { sessionId: first.sessionId });
      await client.request('dom_click', { selector: `[data-testid="focus-pane-${first.paneId}"]` });
      const focused = await focusModeOf(client, first.sessionId);
      runner.assert(
        focused?.desktop?.view?.maximizedPaneId === first.paneId,
        `Focus mode did not maximize ${first.paneId}: ${JSON.stringify(focused?.desktop?.view, null, 2)}`,
        focused?.desktop?.view,
      );
      const notes = path.join(runner.sessionDir, 'shells', 'notes.md');
      fs.writeFileSync(notes, '# Leaf notes\n\nDocked content.\n', 'utf8');
      execFileSync(ATTN_BIN, ['open', notes, '--session', first.sessionId], {
        env: instanceCliEnv(currentHarnessInstance(), { ATTN_SOCKET_PATH: socketPathForInstance() }),
        encoding: 'utf8',
      });
      const state = await waitForDesktopUi(
        client,
        desktopA.id,
        (s) => s?.active === true && s.sessionVisible === true && s.tileBodyFocused === true && s.tileIds?.length === 1,
        'the docked tile shown as the active leaf with its body focused',
      );
      tileId = state.tileIds[0];
      runner.assert(
        state.tileTitles?.includes('Leaf notes'),
        `The tile header did not take its title from the markdown H1: ${JSON.stringify(state, null, 2)}`,
        state,
      );
    });

    await runner.step('the_tile_moves_to_desktop_b_and_the_user_follows', async () => {
      await pressShortcutKeys(client, driver, `desktop.send${desktopB.shortcut_slot}`);
      const state = await waitForDesktopUi(
        client,
        desktopB.id,
        (s) => s?.active === true && s.sessionVisible === true && s.tileBodyFocused === true && s.tileIds?.length === 1,
        'desktop B shown with the moved tile holding the keyboard',
      );
      tileId = state.tileIds[0];
      const app = await client.request('get_state');
      runner.assert(
        app.arrangement.currentDesktopId === desktopB.id,
        `Moving the tile did not follow it to desktop B:\n${JSON.stringify(app.arrangement, null, 2)}`,
        app.arrangement,
      );
    });

    let desktopC;
    await runner.step('the_tile_moves_to_a_digit_with_no_desktop_and_the_user_follows', async () => {
      const held = new Set(observer.desktops.map((desktop) => desktop.shortcut_slot).filter(Boolean));
      const slot = [1, 2, 3, 4, 5, 6, 7, 8, 9].find((candidate) => !held.has(candidate));
      runner.assert(Boolean(slot), `The run needs a third free desktop shortcut:\n${observer.describeArrangement()}`, observer.describeArrangement());
      await pressShortcutKeys(client, driver, `desktop.send${slot}`);
      desktopC = await observer.waitFor(() => observer.desktops.find((desktop) => desktop.shortcut_slot === slot), `a desktop created on slot ${slot}`);
      createdDesktopIds.push(desktopC.id);
      const state = await waitForDesktopUi(
        client,
        desktopC.id,
        (s) => s?.active === true && s.sessionVisible === true && s.tileBodyFocused === true && s.tileIds?.length === 1,
        'the new desktop shown with the moved tile holding the keyboard',
      );
      tileId = state.tileIds[0];
      const app = await client.request('get_state');
      runner.assert(
        app.arrangement.currentDesktopId === desktopC.id && observer.desktop(desktopC.id)?.panes.length === 0,
        `Moving the tile to an empty digit did not land it alone on a new desktop there:\n${observer.describeArrangement()}`,
        observer.describeArrangement(),
      );
    });

    await runner.step('returning_to_the_agent_leaves_no_stale_focus_mode', async () => {
      await client.request('select_session', { sessionId: first.sessionId });
      const returned = await focusModeOf(client, first.sessionId);
      runner.assert(
        returned?.desktop?.view?.maximizedPaneId == null && returned?.sidebarItem?.bounds?.width > 0,
        `Returning from the tile restored stale focus mode: ${JSON.stringify(returned, null, 2)}`,
        returned?.desktop?.view,
      );
      await waitForPaneText(client, first.sessionId, first.paneId, (text) => text.includes(tokens.first), 'first shell kept its scrollback', 15_000);
    });

    const result = await runner.finishSuccess({
      desktops: { a: desktopA.id, b: desktopB.id, c: desktopC.id },
      sessions: { first: first.sessionId, split: split.sessionId },
      tileId,
      tokens,
    });
    console.log('[RealAppHarness] Desktop switching passed.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    const result = await runner.finishFailure(error, { arrangement: observer.describeArrangement() });
    console.error(result.error);
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
