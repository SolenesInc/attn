#!/usr/bin/env node

// Prereqs: a non-production profile install with the automation layer, and a
// built `./attn` (or ATTN_HARNESS_BIN) for the two restart steps.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';

import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  pressShortcutKeys,
  printCommonHelp,
  relaunchAppAndConnect,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, dataDirForProfile, profileCliEnv } from './harnessProfile.mjs';
import { ensureClaudePromptReadyViaPty, writeQueueAgentFixture } from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane, waitForPaneInputFocus } from './scenarioAssertions.mjs';
import { registeredAgentPid } from './workerRegistry.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const TURN_OPENING_STATES = new Set(['waiting_input', 'pending_approval', 'unknown']);
const STOPPED_STATES = new Set([...TURN_OPENING_STATES, 'idle']);

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 60_000, intervalMs = 500) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

function resolveAttnBin() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(HARNESS_DIR, '../../../attn')].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error('attn binary not found (build ./attn or set ATTN_HARNESS_BIN)');
}

async function queueState(client) {
  return client.request('queue_get_state');
}

function turnIds(queue) {
  return (queue.turns || []).map((row) => row.id);
}

function settledIds(queue) {
  return (queue.settled || []).map((row) => row.id);
}

function snoozedIds(queue) {
  return (queue.snoozed?.rows || []).map((row) => row.id);
}

async function paneIdFor(client, workspaceSessionId, sessionId) {
  const workspace = await client.request('get_workspace', { sessionId: workspaceSessionId });
  const pane = (workspace.panes || []).find((entry) => entry.sessionId === sessionId);
  if (!pane) {
    throw new Error(
      `no pane for session ${sessionId} in ${workspaceSessionId}: ${JSON.stringify((workspace.panes || []).map((entry) => entry.sessionId))}`,
    );
  }
  return pane.paneId;
}

// Auto-settle arms only behind user input, never behind an automation write.
async function typePromptAsUser(client, sessionId, paneId, text) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text });
  await delay(600);
  await client.request('type_pane_via_ui', { sessionId, paneId, text: '\r' });
}

// A fast write lands as a paste, so the submit has to be a lone carriage
// return a beat later.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('write_pane', { sessionId, paneId, text, submit: false });
  await delay(600);
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
}

async function createAgent(client, observer, runner, dirName, label) {
  const cwd = path.join(runner.sessionDir, dirName);
  fs.mkdirSync(cwd, { recursive: true });
  writeQueueAgentFixture(cwd, { minimumWorkingMs: WORKING_WINDOW_MS, turns: QUEUE_TURNS });
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'claude',
    sessionWaitMs: 60_000,
    promptReadyFn: ensureClaudePromptReadyViaPty,
    promptReadyTimeoutMs: 90_000,
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${label}`, 20_000);
  return { sessionId, paneId: pane.paneId, cwd };
}

function questionPrompt(token) {
  return [
    `I am thinking about ${token} but I have not decided anything yet.`,
    'Ask me exactly one short clarifying question about it and then stop and wait for my answer.',
    'Do not use any tools and do not answer it yourself.',
  ].join(' ');
}

// 4 reads of this scenario's 500ms poll gap + the measured 14ms p100 bridge trip.
const WORKING_WINDOW_MS = 2_500;
const SHORT_RUN_PROMPT = 'Reply with the single word: done. Do not ask me anything and do not use any tools.';
const LONG_RUN_PROMPT = 'Count from 1 to 500, one number per line, nothing else. Do not use any tools.';
// Past the measured 8.06s auto-settle chain (5s arm floor + 3s countdown floor).
const LONG_RUN_MS = 12_000;
// 3x the measured 505ms worst case for a daemon broadcast to reach the band.
const BAND_UPDATE_WINDOW_MS = 1_500;
// 10x the measured 504ms read of the closed band; the wake itself fired 69ms late.
const SHORT_SNOOZE_MS = 5_000;
const QUEUE_TURNS = [
  { includes: 'single word: done', actions: [{ type: 'reply', text: 'done', state: 'idle' }] },
  {
    includes: 'Count from 1 to 500',
    actions: [{ type: 'delay', ms: LONG_RUN_MS }, { type: 'reply', text: '1 ... 500', state: 'idle' }],
  },
];

// Measured over a green mock run: the slowest turn opened 3.6s after its prompt.
const OWED_TURN_TIMEOUT_MS = 45_000;
const SHORT_RUN_TIMEOUT_MS = 30_000;
// The long run is LONG_RUN_MS of work plus its stop; its step measured 13.3s.
const LONG_RUN_TIMEOUT_MS = 60_000;

async function driveToOwedTurn(client, observer, agent, token, description) {
  const startedAt = Date.now();
  await submitPrompt(client, agent.sessionId, agent.paneId, questionPrompt(token));
  const state = await pollFor(
    () => {
      const current = observer.getSession(agent.sessionId)?.state;
      return TURN_OPENING_STATES.has(current) ? current : null;
    },
    description,
    OWED_TURN_TIMEOUT_MS,
  );
  return { state, elapsedMs: Date.now() - startedAt };
}

async function driveToStop(client, observer, agent, token, description) {
  await submitPrompt(client, agent.sessionId, agent.paneId, questionPrompt(token));
  await pollFor(
    () => (observer.getSession(agent.sessionId)?.state === 'working' ? true : null),
    `${description} to start working`,
    OWED_TURN_TIMEOUT_MS,
  );
  return pollFor(
    () => {
      const state = observer.getSession(agent.sessionId)?.state;
      return STOPPED_STATES.has(state) ? state : null;
    },
    description,
    OWED_TURN_TIMEOUT_MS,
  );
}

async function waitForTurns(client, expected, description, timeoutMs = 30_000) {
  return pollFor(
    async () => {
      const queue = await queueState(client);
      return JSON.stringify(turnIds(queue)) === JSON.stringify(expected) ? queue : null;
    },
    `${description} (expected turns ${JSON.stringify(expected)})`,
    timeoutMs,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-agent-queue.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'AGENT-QUEUE',
    tier: 'tier2-local-mock-agent',
    prefix: 'agent-queue',
    metadata: {
      focus: 'a turn opens on a state, closes only when the user settles or snoozes it',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath });
  const profile = currentHarnessProfile();
  const attnBin = resolveAttnBin();
  const dataDir = dataDirForProfile(profile);
  const daemonEnv = profileCliEnv(profile);
  const createdSessionIds = [];

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, profile });

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_sessions', async () => {
    for (const sessionId of [...createdSessionIds].reverse()) {
      await client.request('close_session', { sessionId }).catch(() => {});
    }
  });
  runner.registerCleanup('restore_queue_mode', () =>
    client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' }).catch(() => {}));

  const snoozeUntil = (sessionId, ms) => {
    const until = new Date(Date.now() + ms).toISOString();
    observer.send({ cmd: 'snooze_turn', session_id: sessionId, until });
    return until;
  };

  let alpha;
  let beta;

  try {
    await runner.step('launch_app_with_queue_mode', async () => {
      process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
      // macOS makes an always-on-top window non-focusable: native keys land nowhere.
      process.env.ATTN_HARNESS_ALWAYS_ON_TOP ??= '0';
      await launchFreshAppAndConnect(client, observer);
      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    });

    await runner.step('open_first_turn', async () => {
      alpha = await createAgent(client, observer, runner, 'alpha', `queue-alpha-${runner.runId}`);
      createdSessionIds.push(alpha.sessionId);
      await pollFor(async () => {
        const state = await queueState(client);
        return state.present ? state : null;
      }, 'the queue band to render once the arrangement is on', 15_000);
      await waitForTurns(client, [alpha.sessionId], 'alpha queued from the moment it booted');

      runner.log('alpha opened a turn', await driveToOwedTurn(client, observer, alpha, 'QUEUE_ALPHA', 'alpha to want the user'));
      await waitForTurns(client, [alpha.sessionId], 'alpha still in the band');
    });

    await runner.step('open_second_turn', async () => {
      beta = await createAgent(client, observer, runner, 'beta', `queue-beta-${runner.runId}`);
      createdSessionIds.push(beta.sessionId);
      runner.log('beta opened a turn', await driveToOwedTurn(client, observer, beta, 'QUEUE_BETA', 'beta to want the user'));
    });

    await runner.step('band_is_oldest_first_and_each_agent_appears_once', async () => {
      const queue = await waitForTurns(client, [alpha.sessionId, beta.sessionId], 'both turns, oldest first');
      runner.assert(
        queue.turns[0].workspaceId !== queue.turns[1].workspaceId,
        `the two turns come from different workspaces: ${JSON.stringify(queue.turns.map((row) => row.workspaceId))}`,
      );
      for (const sessionId of [alpha.sessionId, beta.sessionId]) {
        runner.assert(
          !queue.treeSessionIds.includes(sessionId),
          `${sessionId} is in the band and nowhere else: ${JSON.stringify(queue.treeSessionIds)}`,
        );
        runner.assert(
          !(queue.settled || []).some((row) => row.id === sessionId),
          `${sessionId} is owed, so it is not also in Settled: ${JSON.stringify((queue.settled || []).map((row) => row.id))}`,
        );
      }
    });

    await runner.step('clicking_a_row_hands_the_agent_over', async () => {
      await client.request('dom_click', { selector: `[data-testid="queue-select-${alpha.sessionId}"]` });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === alpha.sessionId ? state : null;
      }, 'the clicked row to select its agent', 15_000);
      await waitForPaneInputFocus(client, alpha.sessionId, alpha.paneId, 15_000);
    });

    await runner.step('a_row_opens_from_the_keyboard', async () => {
      await client.request('select_session', { sessionId: beta.sessionId });
      await driver.activateApp();

      const focused = await client.request('dom_focus', {
        selector: `[data-testid="queue-select-${alpha.sessionId}"]`,
      });
      runner.assert(focused.tag === 'BUTTON', `the row's open control is a button: ${JSON.stringify(focused)}`);

      const row = (await queueState(client)).turns.find((entry) => entry.id === alpha.sessionId);
      runner.assert(
        row.open?.focused === true && row.open.label.length > 0,
        `the focused control is the row's own, and is named: ${JSON.stringify(row.open)}`,
      );

      await driver.pressEnter();
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === alpha.sessionId ? state : null;
      }, 'Return on the focused row to open its agent', 15_000);
      runner.log('a queue row opened from the keyboard', { row: alpha.sessionId, open: row.open });

      await waitForPaneInputFocus(client, alpha.sessionId, alpha.paneId, 15_000);
    });

    await runner.step('steering_keeps_the_row_in_place', async () => {
      const before = await queueState(client);
      const beforeIndex = turnIds(before).indexOf(alpha.sessionId);
      await submitPrompt(client, alpha.sessionId, alpha.paneId, 'Blue. Reply with the single word: noted.');
      const working = await pollFor(async () => {
        const queue = await queueState(client);
        const row = (queue.turns || []).find((entry) => entry.id === alpha.sessionId);
        return row && row.state === 'working' ? { queue, row } : null;
      }, 'alpha to show as working while still queued', SHORT_RUN_TIMEOUT_MS);
      runner.assert(
        turnIds(working.queue).indexOf(alpha.sessionId) === beforeIndex,
        `alpha kept its position while working: ${JSON.stringify(turnIds(working.queue))}`,
      );
    });

    await runner.step('settling_is_the_only_exit', async () => {
      await pollFor(() => {
        const state = observer.getSession(alpha.sessionId)?.state;
        return state && state !== 'working' ? state : null;
      }, 'alpha to finish the run it was steered into', SHORT_RUN_TIMEOUT_MS);
      const stillThere = await queueState(client);
      runner.assert(
        turnIds(stillThere).includes(alpha.sessionId),
        `alpha is still owed after its run finished: ${JSON.stringify(turnIds(stillThere))}`,
      );

      await client.request('dom_click', { selector: `[data-testid="queue-settle-${alpha.sessionId}"]` });
      const settled = await waitForTurns(client, [beta.sessionId], 'alpha gone from Your turn after settling');
      runner.assert(
        settledIds(settled).includes(alpha.sessionId),
        `settling moves the agent to Settled, it does not remove it: ${JSON.stringify(settledIds(settled))}`,
      );
    });

    await runner.step('a_new_turn_returns_at_the_bottom', async () => {
      runner.log('alpha opened a second turn', await driveToOwedTurn(client, observer, alpha, 'QUEUE_ALPHA_AGAIN', 'alpha to want the user again'));
      await waitForTurns(
        client,
        [beta.sessionId, alpha.sessionId],
        'alpha behind beta, whose turn has been owed longer',
        60_000,
      );
    });

    await runner.step('a_finished_run_returns_the_agent_to_the_queue', async () => {
      await client.request('dom_click', { selector: `[data-testid="queue-settle-${alpha.sessionId}"]` });
      await waitForTurns(client, [beta.sessionId], 'alpha settled again');

      await submitPrompt(
        client,
        alpha.sessionId,
        alpha.paneId,
        SHORT_RUN_PROMPT,
      );
      await pollFor(
        () => (observer.getSession(alpha.sessionId)?.state === 'working' ? 'working' : null),
        'alpha to start the run',
        SHORT_RUN_TIMEOUT_MS,
      );
      const duringRun = await queueState(client);
      runner.assert(
        !turnIds(duringRun).includes(alpha.sessionId),
        `a settled agent stays out of the band while it works: ${JSON.stringify(turnIds(duringRun))}`,
      );

      const finished = await pollFor(
        () => {
          const state = observer.getSession(alpha.sessionId)?.state;
          return state && state !== 'working' ? state : null;
        },
        'alpha to finish the run',
        SHORT_RUN_TIMEOUT_MS,
      );
      runner.log('alpha finished', { state: finished });
      runner.assert(
        finished === 'idle',
        `the run ended without a question, so it resolves to idle: got ${finished}`,
      );
      await waitForTurns(
        client,
        [beta.sessionId, alpha.sessionId],
        'alpha back at the bottom because its run finished',
        60_000,
      );
    });

    await runner.step('classification_suspends_auto_settle', async () => {
      await client.request('set_setting', { key: 'auto_settle_arm_seconds', value: '5' });
      await client.request('set_setting', { key: 'auto_settle_countdown_seconds', value: '60' });
      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'true' });
      try {
        await client.request('select_session', { sessionId: alpha.sessionId });
        const pane = await waitForFirstWorkspacePane(client, alpha.sessionId, `current pane for ${alpha.sessionId}`, 20_000);
        await typePromptAsUser(
          client,
          alpha.sessionId,
          pane.paneId,
          LONG_RUN_PROMPT,
        );
        await pollFor(
          () => (observer.getSession(alpha.sessionId)?.state === 'working' ? true : null),
          'the long run to start',
          SHORT_RUN_TIMEOUT_MS,
        );
        await pollFor(
          () => observer.getSession(alpha.sessionId)?.auto_settle_fires_at || null,
          'the long run to reach the auto-settle countdown',
          SHORT_RUN_TIMEOUT_MS,
        );
        await pollFor(
          () => (observer.getSession(alpha.sessionId)?.state === 'idle' ? true : null),
          'the stop verdict to resolve the long run to idle',
          LONG_RUN_TIMEOUT_MS,
        );

        const session = observer.getSession(alpha.sessionId);
        runner.assert(
          !session?.auto_settle_fires_at,
          `classification removed the countdown (got ${JSON.stringify(session?.auto_settle_fires_at)})`,
        );
        const queue = await queueState(client);
        runner.assert(
          turnIds(queue).includes(alpha.sessionId),
          `the classified result still owes its turn: ${JSON.stringify(turnIds(queue))}`,
        );
        runner.assert(
          !settledIds(queue).includes(alpha.sessionId),
          `classification did not auto-settle the result: ${JSON.stringify(settledIds(queue))}`,
        );
      } finally {
        await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' }).catch(() => {});
      }
    });

    await runner.step('auto_settle_hands_over_the_next_agent', async () => {
      await client.request('set_setting', { key: 'auto_settle_arm_seconds', value: '5' });
      await client.request('set_setting', { key: 'auto_settle_countdown_seconds', value: '3' });
      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'true' });
      try {
        const owed = await pollFor(async () => {
          const ids = turnIds(await queueState(client));
          return ids.length >= 2 ? ids : null;
        }, 'two agents owing turns, so there is somewhere to hand over to', 30_000);
        const watchedId = owed[owed.length - 1];
        const nextId = owed[0];
        const watched = [alpha, beta].find((agent) => agent.sessionId === watchedId);
        runner.assert(Boolean(watched), `the bottom row is one of the two agents: ${watchedId}`);
        runner.log('auto-settle will run on the bottom of the queue', { watched: watchedId, next: nextId });
        await client.request('select_session', { sessionId: watched.sessionId });

        // A pane can be replaced under a session, and writing to an id it no longer
        // has goes nowhere silently.
        const pane = await waitForFirstWorkspacePane(client, watched.sessionId, `current pane for ${watched.sessionId}`, 20_000);

        await typePromptAsUser(
          client,
          watched.sessionId,
          pane.paneId,
          LONG_RUN_PROMPT,
        );
        await pollFor(
          () => (observer.getSession(watched.sessionId)?.state === 'working' ? true : null),
          'the steered agent to go back to work',
          SHORT_RUN_TIMEOUT_MS,
        );
        await pollFor(
          () => (observer.getSession(watched.sessionId)?.auto_settle_fires_at ? true : null),
          'the auto-settle countdown to start on the agent being watched',
          SHORT_RUN_TIMEOUT_MS,
        );

        const handed = await pollFor(async () => {
          const state = await client.request('get_state');
          return state.activeSessionId === nextId ? state : null;
        }, 'the auto-settle to hand over the next agent that owes a turn', 30_000);
        runner.assert(
          handed.activeSessionId === nextId,
          `auto-settle selected the next owed turn: ${handed.activeSessionId}`,
        );
        const after = await waitForTurns(client, [nextId], 'the auto-settled agent out of the band');
        runner.assert(
          settledIds(after).includes(watched.sessionId),
          `auto-settle moved it to Settled like any other settle: ${JSON.stringify(settledIds(after))}`,
        );

        await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' });
        await driveToOwedTurn(
          client,
          observer,
          { ...watched, paneId: pane.paneId },
          'QUEUE_AFTER_AUTO_SETTLE',
          'the auto-settled agent to want the user again',
        );
        await waitForTurns(client, [nextId, watched.sessionId], 'the queue back in the order it was given', 60_000);
      } finally {
        await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' }).catch(() => {});
      }
    });

    await runner.step('a_shell_pane_never_queues', async () => {
      const before = turnIds(await queueState(client));
      const workspace = await client.request('get_workspace', { sessionId: alpha.sessionId });
      const targetPaneId = workspace.activePaneId || workspace.panes?.[0]?.paneId;
      await client.request('split_pane', { sessionId: alpha.sessionId, targetPaneId, direction: 'vertical' });
      // A pane is registered in the spawn-time `working` color and settles a beat
      // later, so asserting on first sight reads the wrong state.
      const shell = await pollFor(async () => {
        const state = await client.request('get_state');
        const session = (state.sessions || []).find((entry) => entry.agent === 'shell');
        return session?.state === 'idle' ? session : null;
      }, 'the shell pane to register and settle into idle', 30_000);
      await delay(BAND_UPDATE_WINDOW_MS);
      const after = await queueState(client);
      runner.assert(
        !turnIds(after).includes(shell.id),
        `the shell pane is not a turn: ${JSON.stringify(turnIds(after))}`,
      );
      runner.assert(
        JSON.stringify(turnIds(after)) === JSON.stringify(before),
        `opening a terminal changed nothing in the band: ${JSON.stringify(before)} -> ${JSON.stringify(turnIds(after))}`,
      );

      runner.assert(
        !settledIds(after).includes(shell.id),
        `a shell beside its agent gets no settled row: ${JSON.stringify(settledIds(after))}`,
      );
      runner.assert(
        !after.treeSessionIds.includes(shell.id),
        `a satellite is not in the tree either: ${JSON.stringify(after.treeSessionIds)}`,
      );

      await client.request('split_pane', {
        sessionId: alpha.sessionId,
        targetPaneId: await paneIdFor(client, alpha.sessionId, shell.id),
        direction: 'horizontal',
      });
      const nested = await pollFor(async () => {
        const state = await client.request('get_state');
        const session = (state.sessions || [])
          .find((entry) => entry.agent === 'shell' && entry.id !== shell.id && entry.state === 'idle');
        return session || null;
      }, 'the shell split out of the shell to register and settle into idle', 30_000);
      await delay(BAND_UPDATE_WINDOW_MS);
      const nestedQueue = await queueState(client);
      runner.assert(
        !turnIds(nestedQueue).includes(nested.id) && !settledIds(nestedQueue).includes(nested.id),
        `a shell split out of a shell is a satellite of the same agent: ${JSON.stringify(settledIds(nestedQueue))}`,
      );

      // Later steps click the middle of the window to focus the agent, and a
      // leftover pane is what that click lands on instead.
      for (const id of [nested.id, shell.id]) {
        await client.request('close_pane', {
          sessionId: alpha.sessionId,
          paneId: await paneIdFor(client, alpha.sessionId, id),
        });
      }
      const cleaned = await pollFor(async () => {
        const workspace = await client.request('get_workspace', { sessionId: alpha.sessionId });
        return (workspace.panes || []).length === 1 ? workspace : null;
      }, 'the shell panes to close, leaving the agent alone in its workspace', 15_000);
      runner.assert(
        cleaned.panes[0].sessionId === alpha.sessionId,
        `the agent is the only pane left: ${JSON.stringify(cleaned.panes.map((pane) => pane.sessionId))}`,
      );
    });

    await runner.step('pinning_from_a_row_takes_that_agent_out_of_the_queue', async () => {
      const before = await queueState(client);
      const alphaWorkspaceId = (before.turns.find((row) => row.id === alpha.sessionId) || {}).workspaceId;
      runner.assert(Boolean(alphaWorkspaceId), 'the row carries the workspace it belongs to');
      const openedAt = observer.getSession(alpha.sessionId)?.turn_opened_at;
      runner.assert(Boolean(openedAt), 'alpha has an open turn to pin over');

      await client.request('dom_click', { selector: `[data-testid="queue-pin-${alpha.sessionId}"]` });
      const pinned = await waitForTurns(client, [beta.sessionId], 'alpha out of the turns band once pinned', 20_000);
      runner.assert(
        !settledIds(pinned).includes(alpha.sessionId),
        `a pinned agent is not in the settled band: ${JSON.stringify(settledIds(pinned))}`,
      );
      runner.assert(
        (pinned.pinned || []).map((row) => row.id).includes(alpha.sessionId),
        `a pinned agent lands in the Pinned band: ${JSON.stringify(pinned.pinned)}`,
      );
      runner.assert(
        !pinned.treeSessionIds.includes(alpha.sessionId),
        `pinning one agent did not pin its workspace: ${JSON.stringify(pinned.treeSessionIds)}`,
      );

      await client.request('dom_click', { selector: `[data-testid="queue-unpin-${alpha.sessionId}"]` });
      await waitForTurns(
        client,
        [beta.sessionId, alpha.sessionId],
        'alpha back in the turns band once unpinned',
        20_000,
      );
      const restoredOpenedAt = observer.getSession(alpha.sessionId)?.turn_opened_at;
      runner.assert(
        restoredOpenedAt === openedAt,
        `the restored turn keeps the instant it opened rather than restarting its clock: ${JSON.stringify({ openedAt, restoredOpenedAt })}`,
      );
    });

    await runner.step('the_chief_never_queues', async () => {
      await client.request('chief_of_staff_open_actions', { sessionId: beta.sessionId });
      await client.request('chief_of_staff_toggle');
      const promoted = await waitForTurns(client, [alpha.sessionId], 'beta out of the band once it is chief', 20_000);
      runner.assert(promoted.chief?.id === beta.sessionId, `beta occupies the chief slot: ${JSON.stringify(promoted.chief)}`);

      const chiefWorkspaceId = promoted.chief.workspaceId;
      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
      await pollFor(async () => {
        const state = await queueState(client);
        return state.present ? null : state;
      }, 'the tree back before pinning the chief workspace', 15_000);
      await client.request('dom_click', { selector: `[data-testid="pin-workspace-${chiefWorkspaceId}"]` });
      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
      const chiefPinned = await pollFor(async () => {
        const state = await queueState(client);
        return state.present && state.chief && state.treeWorkspaceIds.includes(chiefWorkspaceId) ? state : null;
      }, 'the band back with the chief workspace pinned and its group drawn', 15_000);
      runner.assert(
        chiefPinned.chief.id === beta.sessionId,
        `the chief keeps its slot while its workspace is pinned: ${JSON.stringify(chiefPinned.chief)}`,
      );
      runner.assert(
        !chiefPinned.treeSessionIds.includes(beta.sessionId),
        `the pinned group does not draw the chief again: ${JSON.stringify(chiefPinned.treeSessionIds)}`,
      );
      await client.request('dom_click', { selector: `[data-testid="pin-workspace-${chiefWorkspaceId}"]` });

      await client.request('chief_of_staff_open_actions', { sessionId: beta.sessionId });
      await client.request('chief_of_staff_toggle');
      const demoted = await waitForTurns(
        client,
        [beta.sessionId, alpha.sessionId],
        'beta back in the band, with its original turn age',
        20_000,
      );
      runner.assert(demoted.chief === null, 'the chief slot is empty again');
    });

    await runner.step('toggling_the_arrangement_preserves_the_queue', async () => {
      const activeBefore = (await client.request('get_state')).activeSessionId;
      const expected = turnIds(await queueState(client));

      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
      const off = await pollFor(async () => {
        const state = await queueState(client);
        return state.present ? null : state;
      }, 'the band to disappear with the arrangement off', 15_000);
      runner.assert(
        off.treeSessionIds.includes(alpha.sessionId) && off.treeSessionIds.includes(beta.sessionId),
        `turning the arrangement off restores the whole workspace tree: ${JSON.stringify(off.treeSessionIds)}`,
      );

      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
      await waitForTurns(client, expected, 'the same queue after turning the arrangement back on', 15_000);
      const activeAfter = (await client.request('get_state')).activeSessionId;
      runner.assert(
        activeAfter === activeBefore,
        `the selected agent survived the toggle: ${activeBefore} -> ${activeAfter}`,
      );
    });

    await runner.step('a_settle_survives_a_daemon_restart', async () => {
      // The packaged app's native menu can swallow an accelerator before the DOM
      // ever sees it, which no unit or e2e test can catch.
      await client.request('select_session', { sessionId: beta.sessionId });
      await driver.activateApp();
      await driver.clickWindow(0.5, 0.5);
      await pressShortcutKeys(client, driver, 'session.settle');
      await waitForTurns(client, [alpha.sessionId], 'beta settled by shortcut before the restart');

      // The target is read before the settle: reading it afterwards races the
      // daemon broadcast that drops the settled row.
      const jumped = await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === alpha.sessionId ? state : null;
      }, 'settling to hand over the next agent in queue order', 15_000);
      runner.assert(
        jumped.activeSessionId === alpha.sessionId,
        `settling selected the next owed turn: ${jumped.activeSessionId}`,
      );

      const betaTurnBefore = observer.getSession(beta.sessionId)?.turn_opened_at ?? null;

      // The app respawns the daemon, so it has to be down for this to be a restart.
      await client.quitApp();
      await observer.close();
      try { execFileSync(attnBin, ['daemon', 'stop'], { env: daemonEnv, encoding: 'utf8' }); } catch {}
      execFileSync(attnBin, ['daemon', 'ensure'], { env: daemonEnv, encoding: 'utf8' });

      await relaunchAppAndConnect(client, observer);
      const queue = await pollFor(
        async () => {
          const current = await queueState(client);
          return turnIds(current).includes(alpha.sessionId) ? current : null;
        },
        'the queue rebuilt from persisted stamps',
        60_000,
      );
      const betaAfter = observer.getSession(beta.sessionId);
      runner.assert(Boolean(betaAfter), `beta came back as a session: ${JSON.stringify(queue)}`, queue);
      runner.assert(
        !betaAfter.turn_owed || betaAfter.turn_opened_at !== betaTurnBefore,
        `the settled turn did not survive the restart (opened_at before=${JSON.stringify(betaTurnBefore)} after=${JSON.stringify(betaAfter.turn_opened_at)})`,
        betaAfter,
      );
    });

    await runner.step('settling_the_last_turn_lands_on_home', async () => {
      // One press is not enough: the daemon reclassifies every session after the
      // restart, so a settled agent can legitimately open a fresh turn.
      await client.request('select_session', { sessionId: alpha.sessionId });
      await driver.activateApp();
      await driver.clickWindow(0.5, 0.5);
      const emptied = await pollFor(async () => {
        const queue = await queueState(client);
        if (turnIds(queue).length === 0) return queue;
        await pressShortcutKeys(client, driver, 'session.settle');
        await delay(BAND_UPDATE_WINDOW_MS);
        return null;
      }, 'the band emptied one keyboard settle at a time', 45_000, 0);
      runner.assert(emptied.empty, 'the band says so itself once nothing is owed');
      const state = await pollFor(async () => {
        const current = await client.request('get_state');
        return current.activeSessionId === null ? current : null;
      }, 'settling the last turn to land on home', 15_000);
      runner.assert(
        state.activeSessionId === null,
        `no agent is selected once the queue is empty: ${state.activeSessionId}`,
      );

      const home = await pollFor(
        async () => {
          const current = await client.request('home_get_state');
          return current.allSettled ? current : null;
        },
        'home to announce that everything is settled',
        15_000,
      );
      runner.assert(
        home.followNextTurn === true,
        `landing on home from a settle arms the wait: ${JSON.stringify(home)}`,
      );

      await client.request('dom_click', { selector: '[data-testid="follow-next-turn"] input' });
      const off = await pollFor(
        async () => {
          const current = await client.request('home_get_state');
          return current.followNextTurn === false ? current : null;
        },
        'the wait to be called off from the banner',
        10_000,
      );
      runner.assert(off.followNextTurn === false, 'the wait can be called off from the banner');
      await client.request('dom_click', { selector: '[data-testid="follow-next-turn"] input' });
      const backOn = await pollFor(
        async () => {
          const current = await client.request('home_get_state');
          return current.followNextTurn === true ? current : null;
        },
        'the wait to be armed again from the banner',
        10_000,
      );
      runner.assert(backOn.followNextTurn === true, 'and armed again from the same switch');
    });

    await runner.step('waiting_at_home_takes_the_user_to_the_next_turn', async () => {
      await driveToOwedTurn(client, observer, beta, 'what to do about the wait', 'beta to want the user again');
      await waitForTurns(client, [beta.sessionId], 'beta back in the band while home waits');
      const jumped = await pollFor(
        async () => {
          const current = await client.request('get_state');
          return current.activeSessionId === beta.sessionId ? current : null;
        },
        'the wait at home to end on the agent that opened a turn',
        20_000,
      );
      runner.assert(
        jumped.activeSessionId === beta.sessionId,
        `waiting at home handed over the agent that wants the user: ${jumped.activeSessionId}`,
      );
    });

    await runner.step('home_the_user_walked_to_keeps_them', async () => {
      await driver.activateApp();
      await driver.clickWindow(0.5, 0.5);
      await pressShortcutKeys(client, driver, 'session.goToDashboard');
      const home = await pollFor(async () => {
        const current = await client.request('get_state');
        return current.activeSessionId === null ? current : null;
      }, 'Cmd+Shift+H to land on home', 15_000);
      runner.assert(home.activeSessionId === null, 'the user walked home');

      await driveToOwedTurn(client, observer, alpha, 'whether to stay put', 'alpha to want the user too');
      await waitForTurns(
        client,
        [beta.sessionId, alpha.sessionId],
        'both agents owed while the user sits on home',
        30_000,
      );
      await delay(BAND_UPDATE_WINDOW_MS);
      const stayed = await client.request('get_state');
      runner.assert(
        stayed.activeSessionId === null,
        `a home the user chose keeps them, however many agents ask: ${stayed.activeSessionId}`,
      );
    });

    await runner.step('snoozing_from_the_row_menu_parks_the_agent_and_hands_over', async () => {
      await client.request('select_session', { sessionId: alpha.sessionId });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === alpha.sessionId ? state : null;
      }, 'alpha to be the agent on screen', 15_000);

      await client.request('dom_click', { selector: `[data-testid="queue-snooze-${alpha.sessionId}"]` });
      const chosen = await pollFor(
        () => client.request('dom_click', { selector: '[data-testid="snooze-choice-30m"]' }).catch(() => null),
        'the snooze duration menu to open with its 30-minute choice',
        10_000,
      );
      runner.log('chose 30 minutes', chosen);

      const queue = await waitForTurns(client, [beta.sessionId], 'alpha out of the turns band');
      runner.assert(
        !settledIds(queue).includes(alpha.sessionId),
        `a deferred agent is not in Settled either: ${JSON.stringify(settledIds(queue))}`,
      );
      runner.assert(queue.snoozed.present, 'the Snoozed section is drawn once something is deferred');
      runner.assert(
        queue.snoozed.header.includes('(1)'),
        `the section counts what is in it: ${queue.snoozed.header}`,
      );
      runner.assert(
        !queue.snoozed.expanded && queue.snoozed.rows.length === 0,
        'the section ships collapsed — a snooze surfaces itself when it wakes',
      );

      await client.request('dom_click', { selector: '[data-testid="snoozed-section-header"]' });
      const expanded = await pollFor(async () => {
        const current = await queueState(client);
        return current.snoozed.expanded ? current : null;
      }, 'the Snoozed section to expand', 10_000);
      const row = expanded.snoozed.rows.find((entry) => entry.id === alpha.sessionId);
      runner.assert(row, `alpha is the deferred row: ${JSON.stringify(snoozedIds(expanded))}`);
      runner.assert(row.wake, `the row says when it comes back: ${JSON.stringify(row)}`);
      runner.log('deferred row', row);

      const session = await pollFor(
        () => {
          const current = observer.getSession(alpha.sessionId);
          return current && !current.turn_owed && current.turn_snoozed_until ? current : null;
        },
        'the daemon to broadcast turn_owed falsy (it is omitted when false) and a deadline',
        15_000,
      );
      runner.log('broadcast deadline', {
        turnOwed: session.turn_owed ?? false,
        snoozedUntil: session.turn_snoozed_until,
      });
      const minutes = (Date.parse(session.turn_snoozed_until) - Date.now()) / 60_000;
      runner.assert(
        minutes > 28 && minutes < 31,
        `the 30-minute choice deferred it by about 30 minutes: ${minutes.toFixed(1)}`,
      );

      const moved = await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === beta.sessionId ? state : null;
      }, 'snoozing the agent on screen to hand over the next one that wants the user', 15_000);
      runner.assert(moved.activeSessionId === beta.sessionId, 'handover landed on beta');
    });

    await runner.step('the_deferral_holds_while_the_agent_runs_and_stops', async () => {
      const state = await driveToStop(client, observer, alpha, 'SNOOZE_ALPHA_AGAIN', 'the deferred agent to stop again');
      runner.log('the deferred agent stopped', { state });
      await delay(BAND_UPDATE_WINDOW_MS);

      const queue = await queueState(client);
      runner.assert(
        JSON.stringify(turnIds(queue)) === JSON.stringify([beta.sessionId]),
        `stopping opened no turn for the deferred agent: ${JSON.stringify(turnIds(queue))}`,
      );
      runner.assert(
        snoozedIds(queue).includes(alpha.sessionId),
        `it is still parked, with its wake time: ${JSON.stringify(queue.snoozed)}`,
      );
      runner.assert(
        !observer.getSession(alpha.sessionId)?.turn_owed,
        'and the daemon agrees no turn is owed',
      );
    });

    await runner.step('waking_early_returns_it_to_the_tail', async () => {
      await client.request('dom_click', { selector: `[data-testid="queue-wake-${alpha.sessionId}"]` });
      const queue = await waitForTurns(
        client,
        [beta.sessionId, alpha.sessionId],
        'the woken agent back in the band, behind the turn owed longer',
      );
      runner.assert(
        !queue.snoozed.present,
        `the section goes away with the last deferral: ${JSON.stringify(queue.snoozed)}`,
      );
      runner.assert(turnIds(queue)[1] === alpha.sessionId, 'it came back at the tail');
    });

    await runner.step('the_deadline_wakes_it_on_its_own', async () => {
      const until = snoozeUntil(alpha.sessionId, SHORT_SNOOZE_MS);
      runner.log('deferred by the daemon command', { until });
      await waitForTurns(client, [beta.sessionId], 'the short deferral to close the turn');

      const queue = await pollFor(
        async () => {
          const current = await queueState(client);
          return turnIds(current).length === 2 ? current : null;
        },
        'the wake deadline to put it back by itself',
        45_000,
      );
      runner.assert(
        JSON.stringify(turnIds(queue)) === JSON.stringify([beta.sessionId, alpha.sessionId]),
        `the deadline woke it to the tail: ${JSON.stringify(turnIds(queue))}`,
      );
      runner.assert(
        !queue.snoozed.present,
        `and cleared the deferral: ${JSON.stringify(queue.snoozed)}`,
      );
    });

    await runner.step('a_deferral_survives_a_daemon_restart', async () => {
      snoozeUntil(alpha.sessionId, 20 * 60_000);
      await waitForTurns(client, [beta.sessionId], 'alpha deferred again, for long enough to restart under');

      await client.quitApp();
      await observer.close();
      try { execFileSync(attnBin, ['daemon', 'stop'], { env: daemonEnv, encoding: 'utf8' }); } catch {}
      execFileSync(attnBin, ['daemon', 'ensure'], { env: daemonEnv, encoding: 'utf8' });
      await relaunchAppAndConnect(client, observer);

      const queue = await waitForTurns(
        client,
        [beta.sessionId],
        'the deferral rebuilt from the persisted deadline',
        60_000,
      );
      runner.assert(
        snoozedIds(queue).includes(alpha.sessionId) || queue.snoozed.header.includes('(1)'),
        `alpha is still parked after the restart: ${JSON.stringify(queue.snoozed)}`,
      );
      runner.assert(
        observer.getSession(alpha.sessionId)?.turn_snoozed_until,
        'and the daemon still broadcasts its deadline',
      );
    });

    await runner.step('a_dead_agent_breaks_through_its_own_snooze', async () => {
      const pid = registeredAgentPid(dataDir, alpha.sessionId, alpha.cwd);
      runner.assert(pid, `the registry names a live agent process in ${alpha.cwd}: ${pid}`);
      // SIGKILL, not a clean exit: a clean exit is auto-close's business and
      // would take the row away instead of ringing.
      process.kill(pid, 'SIGKILL');

      const queue = await pollFor(
        async () => {
          const current = await queueState(client);
          return turnIds(current).includes(alpha.sessionId) ? current : null;
        },
        'the dead agent to break through its deferral and ring',
        60_000,
      );
      runner.log('after the break-through', {
        turns: turnIds(queue),
        state: observer.getSession(alpha.sessionId)?.state,
        reason: observer.getSession(alpha.sessionId)?.state_reason,
      });
      runner.assert(
        !queue.snoozed.present,
        `the break-through consumed the deferral rather than pausing it: ${JSON.stringify(queue.snoozed)}`,
      );
      runner.assert(
        !observer.getSession(alpha.sessionId)?.turn_snoozed_until,
        'and the daemon dropped the deadline',
      );
    });

    const result = await runner.finishSuccess({
      alphaSessionId: alpha.sessionId,
      betaSessionId: beta.sessionId,
    });
    console.log('[RealAppHarness] Agent queue passed.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    const result = await runner.finishFailure(error, {
      queue: await queueState(client).catch(() => null),
      sessions: (await client.request('get_state').catch(() => null))?.sessions?.map((session) => ({
        id: session.id,
        label: session.label,
        state: session.state,
      })) ?? null,
    });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' }).catch(() => {});
    for (const sessionId of createdSessionIds.reverse()) {
      await client.request('close_session', { sessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
