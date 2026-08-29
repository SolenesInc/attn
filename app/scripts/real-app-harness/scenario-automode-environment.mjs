#!/usr/bin/env node

import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { delay } from './macosDriver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, socketPathForProfile } from './harnessProfile.mjs';

const SLOT = 'domains';
const TYPED = 'grafana.harness.corp';
const FROM_THE_CLI = 'written-from-the-command-line.corp';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

function parseAttnJSON(stdout) {
  const at = [stdout.indexOf('{'), stdout.indexOf('[')].filter((index) => index >= 0);
  if (at.length === 0) return null;
  try {
    return JSON.parse(stdout.slice(Math.min(...at)));
  } catch {
    return null;
  }
}

function makeAttnRunner(attnBin, profile) {
  const socketPath = socketPathForProfile(profile);
  return function runAttn(args, { input } = {}) {
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      input,
      env: { ...process.env, ATTN_PROFILE: profile, ATTN_SOCKET_PATH: socketPath },
    });
    return { stdout, json: parseAttnJSON(stdout) };
  };
}

function resolveAttnBinary(appPath) {
  return path.join(appPath, 'Contents', 'MacOS', 'attn');
}

const hold = () => (process.env.ATTN_HARNESS_RECORD === '1' ? delay(1500) : Promise.resolve());

async function pollFor(fn, description, timeoutMs = 20_000, intervalMs = 200) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-automode-environment.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the automode-environment scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const runAttn = makeAttnRunner(resolveAttnBinary(options.appPath), profile);

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'AutoModeEnvironment',
    tier: 'local',
    prefix: 'automode-environment',
    metadata: { focus: 'slot writes from the pane and from the CLI, and what an unfilled slot says' },
  });
  const note = (message, extra) => runner.log(message, extra);

  const slotValues = (env, id) => env?.slots?.find((slot) => slot.id === id)?.values ?? [];
  const readEnv = () => runAttn(['automode', 'env', '--json']).json?.environment ?? { slots: [], notes: [] };
  const before = slotValues(readEnv(), SLOT);
  const restore = () =>
    before.length > 0
      ? runAttn(['automode', 'env', 'set', SLOT, ...before])
      : runAttn(['automode', 'env', 'clear', SLOT]);
  runner.registerCleanup('restore_environment', restore);
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    await launchFreshAppAndConnect(client, observer);

    await runner.step('settings_opens_on_auto_mode', async () => {
      await client.request('dismiss_whats_new');
      await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
      await client.request('settings_select_section', { sectionId: 'autoMode' });
      const state = await pollFor(
        async () => {
          const pane = await client.request('automode_get_state');
          return pane.present && pane.environment.total > 0 ? pane : null;
        },
        'the auto mode pane, with the environment schema',
      );
      note(`the pane offers ${state.environment.total} slots, ${state.environment.filled} filled`);
      await client.request('dom_scroll_into_view', {
        selector: '[data-testid="automode-slot-' + SLOT + '"]',
      });
      await hold();
    });

    await runner.step('an_unfilled_slot_says_what_the_rules_assume', async () => {
      const text = await client.request('dom_text', {
        selector: '[data-testid="automode-slot-registry"]',
      });
      runner.assert(text.text.includes('None configured'),
        'the unfilled registry slot says nothing is configured', { text: text.text });
      await hold();
    });

    await runner.step('a_detected_slot_says_it_fills_itself', async () => {
      const text = await client.request('dom_text', {
        selector: '[data-testid="automode-slot-trusted_repo"]',
      });
      runner.assert(text.text.includes('detected per session'),
        'the trusted-repo slot says a session fills it', { text: text.text });
      await hold();
    });

    await runner.step('typing_an_entry_writes_that_slot', async () => {
      await client.request('automode_environment_edit', { slot: SLOT });
      await client.request('dom_type', {
        selector: '[data-testid="automode-slot-input-' + SLOT + '"]',
        text: TYPED,
      });
      await client.request('dom_key', {
        selector: '[data-testid="automode-slot-input-' + SLOT + '"]',
        key: 'Enter',
      });

      const saved = await pollFor(
        async () => {
          const pane = await client.request('automode_get_state');
          return slotValues(pane.environment, SLOT).includes(TYPED) ? pane : null;
        },
        'the pane to show the entry it wrote',
      );
      runner.assert(saved.environment.error === '', 'the write reported no error', { saved });

      const stored = slotValues(readEnv(), SLOT);
      runner.assert(stored.includes(TYPED), 'the daemon has the entry the pane wrote', { stored });
      runner.writeText('saved-environment.json', JSON.stringify(readEnv(), null, 2) + '\n');
      await hold();
    });

    await runner.step('a_cli_write_reaches_the_open_pane', async () => {
      runAttn(['automode', 'env', 'set', SLOT, FROM_THE_CLI]);
      const arrived = await pollFor(
        async () => {
          const pane = await client.request('automode_get_state');
          return slotValues(pane.environment, SLOT).join(',') === FROM_THE_CLI ? pane : null;
        },
        'the CLI write to reach the open pane',
      );
      runner.assert(slotValues(arrived.environment, SLOT).length === 1,
        'the CLI replaced the slot rather than adding to it', { arrived });
      note('a write from the command line reached the pane that was already open');
      await hold();
    });

    await runner.step('clearing_puts_the_slot_back_to_unset', async () => {
      runAttn(['automode', 'env', 'clear', SLOT]);
      const cleared = await pollFor(
        async () => {
          const pane = await client.request('automode_get_state');
          return slotValues(pane.environment, SLOT).length === 0 ? pane : null;
        },
        'the cleared slot to reach the pane',
      );
      const text = await client.request('dom_text', {
        selector: '[data-testid="automode-slot-' + SLOT + '"]',
      });
      runner.assert(text.text.includes('None configured'),
        'the cleared slot reads as unset again', { text: text.text, cleared: cleared.environment.filled });
      await hold();
    });

    const summary = runner.finishSuccess({ slot: SLOT });
    console.log('[RealAppHarness] Auto mode environment passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { slot: SLOT });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    try {
      restore();
    } catch (error) {
      console.error(`could not restore the environment: ${error?.message ?? error}`);
    }
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
