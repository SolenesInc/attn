#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { parseCommonArgs, printCommonHelp, queryDaemonDb } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { ensureFreshWorld } from './freshWorld.mjs';
import { dataDirForInstance, instanceCliEnv, instanceForAppPath } from './harnessInstance.mjs';
import { captureScreenshotData, setFrontWindowBounds } from './nativeWindowCapture.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const LEGACY_WORKSPACES = [
  'Daemon lifecycle', 'attn zero', 'Garden & crew', 'Shell config', 'Session ledger', 'Release train',
  'Side project', 'Plugin runtime', 'Research', 'SSH outpost', 'CLI polish', 'Browser control',
];
const PAIRED = new Set([1, 5, 9]);
const LAST_SCHEMA_BEFORE_CONVERSION = 152;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

function sql(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

function agentsOf(index) {
  return PAIRED.has(index) ? [`mig-agent-${index}`, `mig-agent-${index}b`] : [`mig-agent-${index}`];
}

function legacyWorkspacesSql(fixtureDir) {
  const statements = [
    'DELETE FROM desktop_panes;',
    'DELETE FROM desktops;',
    'DELETE FROM profiles;',
    'DELETE FROM profile_migration;',
    "UPDATE sessions SET profile_id = '';",
  ];
  LEGACY_WORKSPACES.forEach((title, offset) => {
    const index = offset + 1;
    const directory = path.join(fixtureDir, String(index));
    const agents = agentsOf(index);
    for (const agent of agents) {
      statements.push(`INSERT INTO sessions (id, label, directory, state_since, state_updated_at, last_seen, workspace_id)
        VALUES (${sql(agent)}, ${sql(`${title} ${agent}`)}, ${sql(directory)}, 'now', 'now', 'now', 'mig-ws-${index}');`);
      statements.push(`INSERT INTO workspace_layout_panes (workspace_id, pane_id, runtime_id, session_id, kind, title, status, error, created_at, updated_at)
        VALUES ('mig-ws-${index}', 'pane-${agent}', ${sql(agent)}, ${sql(agent)}, 'agent', ${sql(title)}, 'ready', '', 'now', 'now');`);
    }
    statements.push(`INSERT INTO workspaces (id, title, directory, created_at, rank)
      VALUES ('mig-ws-${index}', ${sql(title)}, ${sql(directory)}, 'now', 'r${String(index).padStart(3, '0')}');`);
    const layout = agents.length === 2
      ? { type: 'split', split_id: `mig-split-${index}`, direction: 'vertical', ratio: 0.6, children: agents.map((agent) => ({ type: 'pane', pane_id: `pane-${agent}` })) }
      : { type: 'pane', pane_id: `pane-${agents[0]}` };
    statements.push(`INSERT INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at)
      VALUES ('mig-ws-${index}', 'pane-${agents[0]}', ${sql(JSON.stringify(layout))}, 'now');`);
  });
  statements.push(`DELETE FROM schema_migrations WHERE version > ${LAST_SCHEMA_BEFORE_CONVERSION};`);
  return statements.join('\n');
}

function sqlite(dbPath, script) {
  execFileSync('sqlite3', [dbPath], { input: script, stdio: ['pipe', 'pipe', 'pipe'] });
}

async function poll(read, predicate, description, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    try {
      last = await read();
      if (predicate(last)) return last;
    } catch (error) {
      last = error instanceof Error ? error.message : String(error);
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${description}. Last: ${JSON.stringify(last)?.slice(0, 2000)}`);
}

function planGroups(node) {
  if (!node) return [];
  return node.group ? [node.group] : node.children.flatMap(planGroups);
}

function draftDesktop(migration, slot) {
  const desktop = migration.desktops.find((entry) => entry.shortcut_slot === slot);
  return desktop?.tree_json ? JSON.parse(desktop.tree_json) : null;
}

function group(migration, id) {
  return migration.groups.find((entry) => entry.group_id === id);
}

function windowRelativePoint(pageX, pageY, windowBounds, innerWidth, innerHeight) {
  const { width, height } = windowBounds.logicalBounds;
  const chromeX = Math.max(0, width - innerWidth);
  const chromeY = Math.max(0, height - innerHeight);
  return { relativeX: (chromeX / 2 + pageX) / width, relativeY: (chromeY + pageY) / height };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-desktop-migration.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'DESKTOP-MIGRATION',
    tier: 'tier1-local-shell',
    prefix: 'desktop-migration',
    metadata: {
      agent: 'none',
      focus: 'converted legacy workspaces open the mandatory picker before any shell; keyboard, native pointer drop, a second client, daemon restart and app reopen all edit one shared draft; finish commits and the normal shell mounts only after Continue',
    },
  });

  const instance = instanceForAppPath(options.appPath);
  const dataDir = dataDirForInstance(instance);
  const dbPath = path.join(dataDir, 'attn.db');
  const daemonBinary = appDaemonInTree(options.appPath);
  const snapshotPath = path.join(runner.runDir, 'attn-before-migration.db');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const daemon = (...args) => execFileSync(daemonBinary, args, { env: instanceCliEnv(instance), encoding: 'utf8', timeout: 60_000 });
  const migrationState = () => client.request('migration_get_state');
  const waitForDraft = (predicate, description, timeoutMs) =>
    poll(migrationState, (state) => state?.migration && predicate(state.migration, state), description, timeoutMs);
  const waitForText = (selector, text) => client.request('dom_wait', { selector, textIncludes: text, timeoutMs: 15_000 });
  const screenshot = (name) => captureScreenshotData(path.join(runner.runDir, name), { client }).catch((error) =>
    runner.log('screenshot failed', { name, error: String(error) }));
  const observerMigration = async (cmd, fields) => {
    const requestId = `harness-${cmd}-${Date.now()}`;
    const answer = observer.waitForMessage(
      (message) => message.event === 'migration_result' && message.request_id === requestId && message,
      `${cmd} answer`,
    );
    observer.send({ cmd, request_id: requestId, ...fields });
    return answer;
  };

  let seeded = false;
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('restore_database', async () => {
    await client.quitApp().catch(() => {});
    if (!seeded) return;
    await ensureFreshWorld({ instance, appPath: options.appPath });
    for (const suffix of ['', '-wal', '-shm']) fs.rmSync(`${dbPath}${suffix}`, { force: true });
    fs.copyFileSync(snapshotPath, dbPath);
  });

  try {
    await runner.step('seed_legacy_workspaces', async () => {
      await ensureFreshWorld({ instance, appPath: options.appPath });
      fs.mkdirSync(runner.runDir, { recursive: true });
      execFileSync('sqlite3', [dbPath, `.backup ${snapshotPath}`]);
      seeded = true;
      sqlite(dbPath, legacyWorkspacesSql(path.join(runner.sessionDir, 'legacy')));
    });

    await runner.step('startup_converts_and_the_app_opens_on_the_intro', async () => {
      daemon('daemon', 'ensure');
      const phase = queryDaemonDb(dbPath, 'SELECT phase FROM profile_migration;');
      runner.assert(phase === 'placement_required', `Conversion left phase ${JSON.stringify(phase)}`);
      await client.launchFreshApp();
      await client.waitForReady(30_000);
      await observer.connect();
      await waitForText('main', 'Your workspaces are already desktops. Confirm where each one goes before you continue.');
      const shell = await client.request('dom_wait', { selector: '.app', absent: true, timeoutMs: 2_000 });
      runner.assert(Boolean(shell), 'The normal shell mounted during the migration');
      const state = await waitForDraft((migration) => migration.groups.length === LEGACY_WORKSPACES.length, 'every imported group');
      runner.assert(
        state.migration.desktops.filter((desktop) => !desktop.shortcut_slot).length === LEGACY_WORKSPACES.length - 9,
        `Expected ${LEGACY_WORKSPACES.length - 9} extra desktops: ${JSON.stringify(state.migration.desktops)}`,
      );
      await screenshot('01-intro.png');
    });

    await runner.step('keyboard_keeps_and_merges', async () => {
      await driver.pressKey('Enter');
      await waitForText('main', 'Confirm your desktops');
      await driver.pressKey('k');
      await waitForDraft((migration) => group(migration, 'mig-ws-1')?.confirmed, 'Daemon lifecycle kept with K');
      await driver.pressKey('1');
      await client.request('dom_wait', { selector: 'dialog.mp-dialog', textIncludes: 'Merge attn zero', timeoutMs: 10_000 });
      await driver.pressKey('ArrowDown');
      await driver.pressKey('Enter');
      const merged = await waitForDraft(
        (migration) => planGroups(draftDesktop(migration, 1)).join(',') === 'mig-ws-1,mig-ws-2',
        'attn zero merged below Daemon lifecycle on Desktop 1',
      );
      const tree = draftDesktop(merged.migration, 1);
      runner.assert(tree.direction === 'horizontal', `The merge did not split below: ${JSON.stringify(tree)}`);
      runner.assert(draftDesktop(merged.migration, 2) === null, 'Desktop 2 did not stay as an empty slot');
      await screenshot('02-keyboard-merge.png');
    });

    await runner.step('a_second_client_edits_the_same_draft', async () => {
      const { migration } = await migrationState();
      const kept = await observerMigration('migration_keep', { group_ids: ['mig-ws-3'], expected_revision: migration.revision });
      runner.assert(kept.success, `The second client could not keep a group: ${JSON.stringify(kept)}`);
      await waitForDraft((next) => group(next, 'mig-ws-3')?.confirmed, 'the app to render the other client’s keep');
      await client.request('dom_wait', { selector: '.mp-source.confirmed[data-drag-group="mig-ws-3"]', timeoutMs: 10_000 });
      const stale = await observerMigration('migration_keep', { group_ids: ['mig-ws-4'], expected_revision: migration.revision });
      runner.assert(!stale.success && stale.error_code === 'stale_revision', `A stale edit was not refused: ${JSON.stringify(stale)}`);
    });

    await runner.step('native_pointer_drop_merges_beside_a_group', async () => {
      const source = '.mp-source[data-drag-group="mig-ws-11"] .mp-grip';
      const anchor = '[data-migration-desktop] [data-migration-group="mig-ws-4"]';
      const [from, to, windowBounds, viewport] = await Promise.all([
        client.request('dom_bounds', { selector: source }),
        client.request('dom_bounds', { selector: anchor }),
        client.request('get_window_bounds', {}),
        client.request('dom_bounds', { selector: 'body' }),
      ]);
      const start = windowRelativePoint(from.bounds.x + from.bounds.width / 2, from.bounds.y + from.bounds.height / 2,
        windowBounds, viewport.bounds.width, viewport.bounds.height);
      const end = windowRelativePoint(to.bounds.x + to.bounds.width * 0.9, to.bounds.y + to.bounds.height / 2,
        windowBounds, viewport.bounds.width, viewport.bounds.height);
      await client.request('arm_native_pointer_witness', { selector: '.mp-shell' });
      await driver.dragWindow(start.relativeX, start.relativeY, end.relativeX, end.relativeY, { steps: 24 });
      const receipt = await client.request('wait_native_pointer_witness', {});
      const inside = (box, x, y) => x >= box.x && x <= box.x + box.width && y >= box.y && y <= box.y + box.height;
      runner.assert(inside(to.bounds, receipt.clientX, receipt.clientY),
        `The trusted native mouseup landed outside the anchor group: ${JSON.stringify({ receipt, anchor: to.bounds, start, end })}`);
      const dropped = await waitForDraft(
        (migration) => planGroups(draftDesktop(migration, 4)).join(',') === 'mig-ws-4,mig-ws-11',
        'CLI polish merged right of Shell config on Desktop 4',
      );
      const tree = draftDesktop(dropped.migration, 4);
      runner.assert(tree.direction === 'vertical', `The drop did not split beside: ${JSON.stringify(tree)}`);
      const extraGroups = new Set(dropped.migration.desktops
        .filter((desktop) => !desktop.shortcut_slot)
        .flatMap((desktop) => planGroups(desktop.tree_json ? JSON.parse(desktop.tree_json) : null)));
      runner.assert(!extraGroups.has('mig-ws-11'), 'The emptied extra desktop stayed in the draft');
      await screenshot('03-pointer-drop.png');
    });

    await runner.step('undo_reverts_the_drop', async () => {
      await driver.pressKey('z', { command: true });
      await waitForDraft(
        (migration) => planGroups(draftDesktop(migration, 4)).join(',') === 'mig-ws-4' && !group(migration, 'mig-ws-11')?.confirmed,
        'the drop undone',
      );
    });

    let confirmedCount;
    await runner.step('the_draft_survives_a_daemon_restart', async () => {
      const { migration } = await migrationState();
      const confirmed = migration.groups.filter((entry) => entry.confirmed).map((entry) => entry.group_id).sort();
      await observer.close();
      daemon('daemon', 'stop');
      daemon('daemon', 'ensure');
      await observer.connect();
      const draft = JSON.parse(queryDaemonDb(dbPath, 'SELECT draft FROM profile_migration;'));
      runner.assert(draft.confirmed.slice().sort().join() === confirmed.join(), `The restart lost confirmations: ${JSON.stringify(draft.confirmed)}`);
      const reread = await poll(
        () => observerMigration('migration_get', {}),
        (answer) => answer?.success,
        'the restarted daemon to answer',
      );
      const kept = await observerMigration('migration_keep', { group_ids: ['mig-ws-5'], expected_revision: reread.state.revision });
      runner.assert(kept.success, `Keep after the restart failed: ${JSON.stringify(kept)}`);
      await client.request('dom_wait', { selector: '.mp-source.confirmed[data-drag-group="mig-ws-5"]', timeoutMs: 30_000 });
      confirmedCount = confirmed.length + 1;
      await waitForText('.mp-status', `${confirmedCount} of ${LEGACY_WORKSPACES.length}`);
    });

    await runner.step('reopening_the_app_resumes_on_the_board', async () => {
      await client.quitApp();
      await client.launchFreshApp();
      await client.waitForReady(30_000);
      await waitForText('main', 'Confirm your desktops');
      await waitForText('.mp-status', `${confirmedCount} of ${LEGACY_WORKSPACES.length}`);
    });

    await runner.step('narrowest_window_keeps_the_board_inside', async () => {
      const original = await client.request('get_window_bounds', {});
      await setFrontWindowBounds({ ...original.logicalBounds, width: 800 }, { client });
      const [body, board] = await poll(
        async () => Promise.all([
          client.request('dom_bounds', { selector: 'body' }),
          client.request('dom_bounds', { selector: '.mp-board' }),
        ]),
        ([page]) => page.bounds.width <= 800,
        'the window to narrow',
      );
      runner.assert(board.bounds.x + board.bounds.width <= body.bounds.width,
        `The picker overflows the narrowest window: ${JSON.stringify({ body: body.bounds, board: board.bounds })}`);
      await screenshot('04-narrow.png');
      await setFrontWindowBounds(original.logicalBounds, { client });
    });

    await runner.step('finish_commits_then_continue_mounts_the_shell', async () => {
      await client.request('dom_click', { selector: '.mp-bottom-right .mp-button.quiet' });
      await waitForDraft((migration) => migration.groups.every((entry) => entry.confirmed), 'every group confirmed');
      await client.request('dom_wait', { selector: '.mp-bottom-right .mp-button.primary:not([disabled])', timeoutMs: 10_000 });
      await client.request('dom_click', { selector: '.mp-bottom-right .mp-button.primary' });
      await waitForText('main', 'Your Default profile is ready.');
      runner.assert(queryDaemonDb(dbPath, 'SELECT phase FROM profile_migration;') === 'complete', 'Finish did not commit');
      const shell = await client.request('dom_wait', { selector: '.app', absent: true, timeoutMs: 2_000 });
      runner.assert(Boolean(shell), 'The normal shell mounted before Continue');
      await screenshot('05-done.png');
      await driver.pressKey('Enter');
      await client.waitForFrontendResponsive(30_000);
      const rows = queryDaemonDb(dbPath, `SELECT d.shortcut_slot || ':' || p.session_id FROM desktop_panes p JOIN desktops d ON d.id = p.desktop_id WHERE p.session_id LIKE 'mig-agent-%' ORDER BY 1;`);
      for (const agent of [...agentsOf(1), ...agentsOf(2)]) {
        runner.assert(rows.includes(`1:${agent}`), `${agent} is not on Desktop 1 after finish:\n${rows}`);
      }
    });

    await runner.finishSuccess({ legacyWorkspaces: LEGACY_WORKSPACES.length });
    console.log('[RealAppHarness] Desktop migration passed.');
  } catch (error) {
    const result = await runner.finishFailure(error, {});
    console.error(result.error);
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
