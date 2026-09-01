#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureScreenshot, ensureDir } from './common.mjs';
import {
  assertDefaultProfileHarnessIsolation,
  defaultProfileHarnessEnv,
  DEFAULT_PROFILE_HARNESS_PACKAGING_PROFILE,
} from './defaultProfileHarness.mjs';
import { resolveHarnessResources } from './harnessProfile.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HARNESS_DIR, '../../..');
const DEFAULT_APP_PATH = path.join(
  REPO_ROOT,
  'app/src-tauri/target/release/bundle/macos/attn-legacy-recovery.app',
);
const RECOVERY_VERSION = 2;

function parseArgs(argv) {
  const options = {
    appPath: process.env.ATTN_REAL_APP_PATH || DEFAULT_APP_PATH,
    artifactsDir: process.env.ATTN_REAL_APP_ARTIFACTS_DIR || path.join(os.tmpdir(), 'attn-real-app-harness'),
    sessionRootDir: process.env.ATTN_REAL_APP_SESSION_ROOT || path.join(os.tmpdir(), 'attn-real-app-sessions'),
  };
  const args = argv[0] === '--' ? argv.slice(1) : [...argv];
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }
  return options;
}

function printHelp() {
  console.log(`Usage: pnpm run real-app:scenario-legacy-ticket-recovery [-- options]

Build first with: make build-default-profile-harness

Options:
  --app-path <path>          Default: ${DEFAULT_APP_PATH}
  --artifacts-dir <path>     Screenshot and summary directory
  --session-root-dir <path>  Isolated fixture roots
`);
}

function run(binary, args, env, timeout = 60_000) {
  return execFileSync(binary, args, {
    encoding: 'utf8',
    env,
    timeout,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

function sqlite(dbPath, sql) {
  return execFileSync('sqlite3', ['-batch', '-cmd', '.timeout 5000', dbPath, sql], {
    encoding: 'utf8',
    timeout: 30_000,
  }).trim();
}

function sqliteRows(dbPath, sql) {
  const output = execFileSync('sqlite3', ['-json', '-batch', '-cmd', '.timeout 5000', dbPath, sql], {
    encoding: 'utf8',
    timeout: 30_000,
  }).trim();
  return output ? JSON.parse(output) : [];
}

function sqlString(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

function sha256(filePath) {
  return crypto.createHash('sha256').update(fs.readFileSync(filePath)).digest('hex');
}

async function poll(fn, description, timeoutMs = 60_000, intervalMs = 200) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    try {
      last = await fn();
      if (last) return last;
    } catch (error) {
      last = error instanceof Error ? error.message : String(error);
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

function createWorld(resources, name, includeWarnings) {
  const shortName = name === 'complete' ? 'c' : 'p';
  const root = fs.mkdtempSync(path.join('/tmp', `attn-ltr-${shortName}-`));
  fs.chmodSync(root, 0o700);
  const dataDir = path.join(root, 'data');
  const toolHome = path.join(dataDir, 'tool-home');
  const codexHome = path.join(toolHome, '.codex');
  const copilotHome = path.join(toolHome, '.copilot', 'session-state');
  const notebookRoot = path.join(dataDir, 'notebook');
  for (const dir of [dataDir, toolHome, codexHome, copilotHome, notebookRoot]) {
    fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    fs.chmodSync(dir, 0o700);
  }
  const wsUrl = `ws://127.0.0.1:${resources.wsPort}/ws`;
  const resolved = assertDefaultProfileHarnessIsolation({
    dataDir,
    toolHome,
    codexHome,
    copilotHome,
    notebookRoot,
    appPath: resources.appPath,
    bundleId: resources.bundleId,
    wsUrl,
  });
  return {
    name,
    includeWarnings,
    root,
    dataDir,
    toolHome,
    codexHome,
    copilotHome,
    notebookRoot,
    dbPath: path.join(dataDir, 'attn.db'),
    wsUrl,
    resources,
    resolved,
    env: defaultProfileHarnessEnv({
      dataDir,
      toolHome,
      codexHome,
      notebookRoot,
      wsPort: resources.wsPort,
    }),
  };
}

async function waitForRecovery(world) {
  return poll(() => {
    const state = sqlite(world.dbPath, `SELECT state FROM legacy_ticket_recovery_runs WHERE version=${RECOVERY_VERSION};`);
    return state === 'succeeded' || state === 'warned' ? state : null;
  }, `${world.name} recovery terminal state`, 90_000);
}

function stopDaemon(world, binary) {
  try {
    run(binary, ['daemon', 'stop'], world.env, 30_000);
  } catch (error) {
    const pidPath = path.join(world.dataDir, 'attn.pid');
    if (fs.existsSync(pidPath)) throw error;
  }
}

function writeCodexFixture(world) {
  const native = `${world.name}-codex-native`;
  const contextPath = path.join(world.dataDir, 'workspace-contexts', `${world.name}-context`, 'context.md');
  ensureDir(path.dirname(contextPath));
  fs.writeFileSync(contextPath, `isolated ${world.name} context\n`, { mode: 0o600 });
  const transcriptPath = path.join(world.codexHome, 'sessions', '2026', '08', '29', `${native}.jsonl`);
  ensureDir(path.dirname(transcriptPath));
  const records = [
    { timestamp: '2026-01-04T10:00:00Z', type: 'session_meta', payload: { id: native, cwd: world.root } },
    {
      timestamp: '2026-01-04T10:00:01Z', type: 'response_item', payload: {
        type: 'message', role: 'developer', content: [{
          type: 'input_text',
          text: `attn checked out this workspace's shared context for this session at "${contextPath}".`,
        }],
      },
    },
    { timestamp: '2026-01-04T10:00:02Z', type: 'event_msg', payload: { type: 'user_message', message: 'Please preserve this transcript-only task' } },
    { timestamp: '2026-01-04T10:00:03Z', type: 'event_msg', payload: { type: 'agent_message', message: 'Recovered work is complete.' } },
    {
      timestamp: '2026-01-04T10:00:04Z', type: 'response_item', payload: {
        type: 'custom_tool_call', name: 'functions.exec', call_id: 'close-ticket',
        input: 'const r = await tools.exec_command({cmd: "attn ticket status done"});',
      },
    },
    {
      timestamp: '2026-01-04T10:00:05Z', type: 'response_item', payload: {
        type: 'custom_tool_call_output', call_id: 'close-ticket', output: 'ticket transcript-done → done',
      },
    },
  ];
  fs.writeFileSync(transcriptPath, `${records.map((record) => JSON.stringify(record)).join('\n')}\n`, { mode: 0o600 });
  return { native, contextPath, transcriptPath };
}

function writeCopilotFixture(world) {
  const native = `${world.name}-copilot-native`;
  const transcriptPath = path.join(world.copilotHome, native, 'events.jsonl');
  ensureDir(path.dirname(transcriptPath));
  const records = [
    {
      id: 'copilot-start', parentId: null, timestamp: '2026-01-05T10:00:00Z', type: 'session.start', data: {
        sessionId: native,
        version: 1,
        producer: 'copilot-agent',
        copilotVersion: '1.0.80',
        startTime: '2026-01-05T10:00:00Z',
        context: { cwd: world.root, gitRoot: world.root, repository: 'fixture/attn', hostType: 'github' },
      },
    },
    {
      id: 'copilot-user', parentId: 'copilot-start', timestamp: '2026-01-05T10:00:01Z', type: 'user.message', data: {
        content: 'Please preserve this Copilot task', interactionId: 'copilot-interaction', delivery: 'idle',
      },
    },
    {
      id: 'copilot-assistant', parentId: 'copilot-user', timestamp: '2026-01-05T10:00:02Z', type: 'assistant.message', data: {
        content: 'Copilot failure details survived.', interactionId: 'copilot-interaction', toolRequests: [],
      },
    },
    {
      id: 'copilot-call', parentId: 'copilot-assistant', timestamp: '2026-01-05T10:00:03Z', type: 'tool.execution_start', data: {
        toolCallId: 'copilot-close-ticket',
        toolName: 'bash',
        arguments: { command: 'attn ticket status failed', description: 'Close the ticket' },
      },
    },
    {
      id: 'copilot-result', parentId: 'copilot-call', timestamp: '2026-01-05T10:00:04Z', type: 'tool.execution_complete', data: {
        toolCallId: 'copilot-close-ticket',
        success: true,
        result: { content: 'ticket copilot-failed → failed', detailedContent: 'ticket copilot-failed → failed' },
      },
    },
  ];
  fs.writeFileSync(transcriptPath, `${records.map((record) => JSON.stringify(record)).join('\n')}\n`, { mode: 0o600 });
  return { native, transcriptPath };
}

function seedDatabaseFixture(world) {
  const notebookTicketDir = path.join(world.notebookRoot, 'tickets', 'backup-failed');
  fs.mkdirSync(notebookTicketDir, { recursive: true, mode: 0o700 });
  const notebookPath = path.join(notebookTicketDir, 'recovery-note.md');
  fs.writeFileSync(notebookPath, '# Recovered notebook evidence\nThe useful details survived.\n', { mode: 0o600 });
  if (world.includeWarnings) {
    const unbound = path.join(world.notebookRoot, 'tickets', 'unbound-notebook');
    fs.mkdirSync(unbound, { recursive: true, mode: 0o700 });
    fs.writeFileSync(path.join(unbound, 'partial.md'), 'orphaned notebook evidence\n', { mode: 0o600 });
  }

  const old = '2026-01-01T00:00:00Z';
  const closed = '2026-01-02T00:00:00Z';
  const ticket = (id, title, description, status, automationRun = 'NULL') => `
    INSERT INTO tickets
      (id,title,description,status,assignee,cwd,last_agent_id,project_id,automation_run_id,created_at,updated_at,closed_at,archived_at)
    VALUES
      (${sqlString(id)},${sqlString(title)},${sqlString(description)},${sqlString(status)},'',${sqlString(world.root)},'codex','',${automationRun},${sqlString(old)},${sqlString(closed)},${sqlString(closed)},${sqlString(closed)});`;
  let script = `
    INSERT INTO settings(key,value) VALUES ('notebook.root',${sqlString(world.notebookRoot)})
      ON CONFLICT(key) DO UPDATE SET value=excluded.value;
    ${ticket('live-done', 'Current live ticket', 'backup copy must lose', 'done')}
    ${ticket('backup-failed', 'Recovered backup failure', 'recover this exact backup description', 'failed')}
    ${ticket('auto-done', 'Deleted Automation ticket', 'must stay excluded', 'done', sqlString('automation-run-old'))}
    ${ticket('auto-live', 'Expiring Automation ticket', 'the only ticket allowed to age out', 'done', sqlString('automation-run-live'))}
    INSERT INTO ticket_activity(ticket_id,kind,author,from_status,to_status,comment,created_at)
      VALUES ('backup-failed','status_change','attn','working','failed','failure details from backup',${sqlString(closed)});
  `;
  if (world.includeWarnings) {
    script += `
      INSERT INTO delegation_operations
        (request_id,operation_id,request_json,state,progress,session_id,workspace_id,ticket_id,worktree_path,worktree_owned,result_json,error,created_at,updated_at)
      VALUES
        ('orphan-request','orphan-operation','{"brief":"keep exact request"}','completed','done','','','orphan-delegation','',0,'{"summary":"keep exact result"}','',${sqlString(old)},${sqlString(closed)});`;
  }
  sqlite(world.dbPath, script);

  const backupDir = path.join(world.dataDir, 'backups');
  fs.mkdirSync(backupDir, { recursive: true });
  const backups = [];
  for (let day = 1; day <= 13; day += 1) {
    const target = path.join(backupDir, `attn-202601${String(day).padStart(2, '0')}-000000.db`);
    sqlite(world.dbPath, `VACUUM INTO ${sqlString(target)};`);
    backups.push(target);
  }

  sqlite(world.dbPath, `
    DELETE FROM ticket_activity WHERE ticket_id IN ('backup-failed','auto-done');
    DELETE FROM tickets WHERE id IN ('backup-failed','auto-done');
    DELETE FROM delegation_operations WHERE request_id='orphan-request';
    UPDATE tickets SET description='current version wins', updated_at='2026-01-03T00:00:00Z' WHERE id='live-done';
    DELETE FROM legacy_ticket_recovery_items;
    DELETE FROM legacy_ticket_recovery_sources;
    DELETE FROM legacy_ticket_recovery_runs;
    DELETE FROM jobs WHERE kind='recover_legacy_closed_work';
    DELETE FROM notifications WHERE kind='legacy_ticket_recovery_warned';
  `);
  return { backups, notebookPath };
}

async function initializeWorld(world, binary) {
  run(binary, ['daemon', 'ensure'], world.env, 60_000);
  await waitForRecovery(world);
  stopDaemon(world, binary);
  const transcripts = {
    codex: writeCodexFixture(world),
    copilot: writeCopilotFixture(world),
  };
  const fixture = seedDatabaseFixture(world);
  return { ...fixture, transcripts };
}

function recoveryReceipt(world) {
  const runRow = sqliteRows(world.dbPath, `
    SELECT state,counts_json,warning_notification_id,terminal_error
    FROM legacy_ticket_recovery_runs WHERE version=${RECOVERY_VERSION};
  `)[0];
  const links = sqliteRows(world.dbPath, `
    SELECT ticket_id,seed_id,original_terminal_state FROM legacy_ticket_seed_links ORDER BY ticket_id;
  `);
  const tickets = sqliteRows(world.dbPath, `
    SELECT id,status,description,archived_at FROM tickets
    WHERE id IN ('live-done','backup-failed','transcript-done','copilot-failed','auto-done','auto-live') ORDER BY id;
  `);
  const notifications = sqliteRows(world.dbPath, `
    SELECT id,kind,severity,title,body,detail FROM notifications
    WHERE kind='legacy_ticket_recovery_warned' ORDER BY created_at;
  `);
  const attachments = sqliteRows(world.dbPath, `
    SELECT ticket_id,filename,path,note FROM ticket_attachments ORDER BY ticket_id,filename;
  `);
  return { runRow, links, tickets, notifications, attachments };
}

function assertRecovery(runner, world, fixture, receipt) {
  const expectedState = world.includeWarnings ? 'warned' : 'succeeded';
  runner.assert(receipt.runRow?.state === expectedState, `${world.name} reached ${expectedState}`, receipt.runRow);
  const byTicket = new Map(receipt.tickets.map((ticket) => [ticket.id, ticket]));
  runner.assert(byTicket.get('backup-failed')?.status === 'failed', 'backup-only failed ticket was restored');
  runner.assert(byTicket.get('transcript-done')?.status === 'done', 'transcript-only done ticket was restored');
  runner.assert(byTicket.get('copilot-failed')?.status === 'failed', 'Copilot transcript-only failed ticket was restored');
  runner.assert(byTicket.get('live-done')?.description === 'current version wins', 'the live ticket won without overwrite');
  runner.assert(!byTicket.has('auto-done'), 'historically proven Automation ticket was excluded');
  runner.assert(!byTicket.has('auto-live'), 'only the proven Automation ticket aged out');
  runner.assert(receipt.links.length === 4, 'every user terminal ticket has exactly one closed seed', receipt.links);
  runner.assert(receipt.links.find((link) => link.ticket_id === 'live-done')?.original_terminal_state === 'done', 'done maps to harvested');
  runner.assert(receipt.links.find((link) => link.ticket_id === 'backup-failed')?.original_terminal_state === 'failed', 'failed maps to withered');
  runner.assert(receipt.attachments.some((item) => item.ticket_id === 'backup-failed' && item.path === fixture.notebookPath), 'Notebook metadata was attached additively');
  runner.assert(receipt.attachments.some((item) => item.ticket_id === 'transcript-done' && item.filename.endsWith('.md')), 'normalized conversation was attached');
  runner.assert(receipt.attachments.some((item) => item.ticket_id === 'copilot-failed' && item.filename.endsWith('.md')), 'normalized Copilot conversation was attached');

  const conversations = {
    codex: path.join(world.dataDir, 'legacy-ticket-recovery', 'conversations', 'codex', `${fixture.transcripts.codex.native}.md`),
    copilot: path.join(world.dataDir, 'legacy-ticket-recovery', 'conversations', 'copilot', `${fixture.transcripts.copilot.native}.md`),
  };
  const codexConversation = fs.readFileSync(conversations.codex, 'utf8');
  const copilotConversation = fs.readFileSync(conversations.copilot, 'utf8');
  runner.assert(Object.values(conversations).every((file) => (fs.statSync(file).mode & 0o777) === 0o600), 'conversations are owner-only');
  runner.assert(codexConversation.includes('Please preserve this transcript-only task'), 'Codex conversation keeps the human prompt');
  runner.assert(codexConversation.includes('Recovered work is complete.'), 'Codex conversation keeps the assistant message');
  runner.assert(!codexConversation.includes('ticket transcript-done') && !codexConversation.includes('checked out this workspace'), 'Codex conversation strips receipts and wrappers');
  runner.assert(copilotConversation.includes('Please preserve this Copilot task'), 'Copilot conversation keeps the human prompt');
  runner.assert(copilotConversation.includes('Copilot failure details survived.'), 'Copilot conversation keeps the assistant message');
  runner.assert(!copilotConversation.includes('ticket copilot-failed') && !copilotConversation.includes('copilot-close-ticket'), 'Copilot conversation strips receipts and wrappers');

  const rotating = fs.readdirSync(path.join(world.dataDir, 'backups')).filter((name) => /^attn-\d{8}-\d{6}\.db$/.test(name));
  runner.assert(rotating.length <= 12, 'routine backups prune only after the recovery fence', rotating);
  runner.assert(!fs.existsSync(fixture.backups[0]), 'an inventoried old backup was read before it became pruneable');

  if (world.includeWarnings) {
    runner.assert(receipt.notifications.length === 1, 'partial recovery emits exactly one warning', receipt.notifications);
    const delegationPath = path.join(world.dataDir, 'legacy-ticket-delegations.json');
    const fragmentsPath = path.join(world.dataDir, 'legacy-ticket-recovery', 'fragments.json');
    const delegation = JSON.parse(fs.readFileSync(delegationPath, 'utf8'));
    runner.assert(delegation.operations[0].row.request_json === '{"brief":"keep exact request"}', 'delegation request JSON stayed exact');
    runner.assert(delegation.operations[0].row.result_json === '{"summary":"keep exact result"}', 'delegation result JSON stayed exact');
    runner.assert((fs.statSync(delegationPath).mode & 0o777) === 0o600, 'delegation dump is owner-only');
    runner.assert((fs.statSync(fragmentsPath).mode & 0o777) === 0o600, 'fragment dump is owner-only');
  } else {
    runner.assert(receipt.notifications.length === 0, 'complete recovery is silent');
    runner.assert(!fs.existsSync(path.join(world.dataDir, 'legacy-ticket-delegations.json')), 'no empty delegation artifact was created');
    runner.assert(!fs.existsSync(path.join(world.dataDir, 'legacy-ticket-recovery', 'fragments.json')), 'no empty fragment artifact was created');
  }
  return Object.values(conversations);
}

function idempotenceReceipt(world, conversations) {
  const artifactPaths = [...conversations];
  for (const candidate of [
    path.join(world.dataDir, 'legacy-ticket-delegations.json'),
    path.join(world.dataDir, 'legacy-ticket-recovery', 'fragments.json'),
  ]) {
    if (fs.existsSync(candidate)) artifactPaths.push(candidate);
  }
  return {
    runCount: Number(sqlite(world.dbPath, `SELECT COUNT(*) FROM legacy_ticket_recovery_runs WHERE version=${RECOVERY_VERSION};`)),
    itemCount: Number(sqlite(world.dbPath, 'SELECT COUNT(*) FROM legacy_ticket_recovery_items;')),
    linkCount: Number(sqlite(world.dbPath, 'SELECT COUNT(*) FROM legacy_ticket_seed_links;')),
    notificationCount: Number(sqlite(world.dbPath, "SELECT COUNT(*) FROM notifications WHERE kind='legacy_ticket_recovery_warned';")),
    ticketCount: Number(sqlite(world.dbPath, "SELECT COUNT(*) FROM tickets WHERE id IN ('live-done','backup-failed','transcript-done','copilot-failed');")),
    artifacts: Object.fromEntries(artifactPaths.map((file) => [file, sha256(file)])),
  };
}

async function launchAndInspect(runner, world, binary, receipt) {
  const client = new UiAutomationClient({
    appPath: world.resources.appPath,
    bundleId: world.resources.bundleId,
    launchEnv: world.env,
  });
  const observer = new DaemonObserver({ wsUrl: world.wsUrl });
  const driver = createWindowDriver({ appPath: world.resources.appPath });
  process.env.ATTN_CLIENT_TOKEN = fs.readFileSync(path.join(world.dataDir, 'client-token'), 'utf8').trim();
  try {
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await client.request('dismiss_whats_new', {}).catch(() => {});
    await observer.connect();

    await client.request('open_dock_panel', { panelId: 'garden' });
    await poll(async () => {
      const state = await client.request('garden_get_state');
      return state.present && state.search?.closedToggle ? state : null;
    }, `${world.name} Garden closed lens`, 20_000);
    await client.request('dom_click', { selector: '.garden-chrome__scope' });
    const garden = await poll(async () => {
      const state = await client.request('garden_get_state');
      return state.seeds?.length >= 4 ? state : null;
    }, `${world.name} closed seeds in Garden`, 20_000);
    const byTitle = new Map(garden.seeds.map((seed) => [seed.title, seed]));
    runner.assert(byTitle.get('Current live ticket')?.status === 'harvested', 'Garden renders done as harvested');
    runner.assert(byTitle.get('Recovered backup failure')?.status === 'withered', 'Garden renders failed as withered');
    runner.assert(byTitle.get('Recovered ticket transcript-done')?.status === 'harvested', 'Garden renders transcript-only work already closed');
    runner.assert(byTitle.get('Recovered ticket copilot-failed')?.status === 'withered', 'Garden renders Copilot transcript-only work already closed');
    await captureScreenshot(driver, path.join(runner.runDir, `${world.name}-garden.png`));

    await client.request('dom_click', { selector: '[aria-label="Show Notifications"]' });
    const notifications = await poll(async () => {
      const state = await client.request('notifications_get_state');
      if (!state.present) return null;
      if (world.includeWarnings) return state.rows.length > 0 ? state : null;
      return state.empty === 'No notifications.' ? state : null;
    }, `${world.name} notifications`, 20_000);
    if (world.includeWarnings) {
      runner.assert(notifications.rows.length === 1, 'the app shows one recovery warning', notifications);
      runner.assert(notifications.rows[0].title === 'Closed ticket recovery needs attention', 'the warning is recognizable in attn');
    } else {
      runner.assert(notifications.rows.length === 0, 'the app stays silent after complete recovery', notifications);
    }
    await captureScreenshot(driver, path.join(runner.runDir, `${world.name}-notifications.png`));

    const ticketShows = {};
    const seedShows = {};
    for (const ticketID of ['live-done', 'backup-failed', 'transcript-done', 'copilot-failed']) {
      ticketShows[ticketID] = JSON.parse(run(binary, ['ticket', 'show', ticketID, '--json'], world.env));
      const link = receipt.links.find((candidate) => candidate.ticket_id === ticketID);
      seedShows[ticketID] = run(binary, ['seed', 'show', link.seed_id], world.env);
    }
    runner.assert(ticketShows['backup-failed'].status === 'failed', 'attn ticket show reads the restored ticket');
    runner.assert(seedShows['backup-failed'].includes('status    withered'), 'attn seed show reads the equivalent closed state');
    runner.writeJson(`${world.name}-cli.json`, { ticketShows, seedShows, garden, notifications });
  } finally {
    await observer.close().catch(() => {});
    await client.quitApp().catch(() => {});
  }
}

async function exerciseWorld(runner, world, binary) {
  const fixture = await runner.step(`${world.name}:initialize_fixture`, () => initializeWorld(world, binary));
  run(binary, ['daemon', 'ensure'], world.env, 60_000);
  const state = await waitForRecovery(world);
  await poll(() => sqlite(world.dbPath, "SELECT COUNT(*) FROM tickets WHERE id='auto-live';") === '0', `${world.name} Automation retention`, 20_000);
  await poll(() => {
    const dir = path.join(world.dataDir, 'backups');
    return fs.readdirSync(dir).filter((name) => /^attn-\d{8}-\d{6}\.db$/.test(name)).length <= 12;
  }, `${world.name} post-fence backup pruning`, 20_000);

  const preflight = JSON.parse(run(binary, ['preflight', '--app-path', world.resources.appPath, '--json'], world.env, 60_000));
  runner.writeJson(`${world.name}-preflight.json`, preflight);
  const receipt = recoveryReceipt(world);
  const conversations = assertRecovery(runner, world, fixture, receipt);
  runner.writeJson(`${world.name}-recovery.json`, { state, receipt, isolation: world.resolved });

  const beforeRestart = idempotenceReceipt(world, conversations);
  stopDaemon(world, binary);
  run(binary, ['daemon', 'ensure'], world.env, 60_000);
  await waitForRecovery(world);
  const afterRestart = idempotenceReceipt(world, conversations);
  runner.assert(JSON.stringify(afterRestart) === JSON.stringify(beforeRestart), 'restart is create-only and idempotent', { beforeRestart, afterRestart });
  await launchAndInspect(runner, world, binary, receipt);
  stopDaemon(world, binary);
  return { state, preflight, receipt, beforeRestart };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printHelp();
    return;
  }
  process.env.ATTN_HARNESS_PROFILE = DEFAULT_PROFILE_HARNESS_PACKAGING_PROFILE;
  process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
  const profileResources = resolveHarnessResources(DEFAULT_PROFILE_HARNESS_PACKAGING_PROFILE);
  const resources = { ...profileResources, appPath: path.resolve(options.appPath) };
  options.appPath = resources.appPath;
  options.wsUrl = `ws://127.0.0.1:${resources.wsPort}/ws`;
  const binary = appDaemonInTree(resources.appPath);
  if (!fs.existsSync(binary)) {
    throw new Error(`default-profile harness bundle is missing; run make build-default-profile-harness (${binary})`);
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'LEGACY-TICKET-RECOVERY',
    allowRealAgents: false,
    tier: 'tier2-local-packaged-default-profile',
    prefix: 'legacy-ticket-recovery',
    metadata: {
      logicalProfile: 'default',
      packagingProfile: DEFAULT_PROFILE_HARNESS_PACKAGING_PROFILE,
      dataPolicy: 'fresh owner-only root; production paths refused twice',
    },
  });
  const worlds = [
    createWorld(resources, 'complete', false),
    createWorld(resources, 'partial', true),
  ];
  runner.registerCleanup('stop_fixture_daemons', () => {
    for (const world of worlds) stopDaemon(world, binary);
  });

  try {
    const results = {};
    for (const world of worlds) {
      results[world.name] = await exerciseWorld(runner, world, binary);
    }
    const summary = await runner.finishSuccess({ results });
    console.log('[RealAppHarness] Legacy ticket recovery passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error);
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const world of worlds) stopDaemon(world, binary);
    delete process.env.ATTN_CLIENT_TOKEN;
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
