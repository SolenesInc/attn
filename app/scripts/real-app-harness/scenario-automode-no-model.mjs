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

const MODEL = 'opencode-go/glm-5.3';

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
    printCommonHelp('scripts/real-app-harness/scenario-automode-no-model.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the automode-no-model scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const runAttn = makeAttnRunner(path.join(options.appPath, 'Contents', 'MacOS', 'attn'), profile);

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'AutoModeNoModel',
    tier: 'local',
    prefix: 'automode-no-model',
    metadata: { focus: 'auto mode stays off until a model is named, and turns on when one is' },
  });
  const note = (message, extra) => runner.log(message, extra);

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    await launchFreshAppAndConnect(client, observer);

    const pillText = async () =>
      (await client.request('dom_text', { selector: '[data-testid="automode-new-sessions"]' })).text;

    const promote = async (proposalId) => {
      await client.request('dom_click', { selector: `[data-testid="automode-promote-${proposalId}"]` });
    };

    const openSettings = async () => {
      await client.request('dismiss_whats_new');
      await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
      await client.request('settings_select_section', { sectionId: 'autoMode' });
      return pollFor(
        async () => {
          const state = await client.request('automode_get_state');
          return state.present ? state : null;
        },
        'the auto mode pane',
      );
    };

    await runner.step('a_model_is_what_makes_auto_mode_run', async () => {
      let pane = await openSettings();
      if (pane.models.length === 0) {
        const proposed = runAttn(['automode', 'model', MODEL, '--json']).json;
        await promote(proposed.proposal.id);
        pane = await pollFor(
          async () => {
            const state = await client.request('automode_get_state');
            return state.models.length > 0 ? state : null;
          },
          'the promoted model to reach the pane',
        );
      }
      runner.assert(pane.enabledDefault === true, 'new sessions are configured to start with auto mode', { pane });
      runner.assert(await pillText() === 'Auto mode on', 'a named model reads as on', { models: pane.models });
      note(`auto mode runs on ${pane.models.join(', ')}`);
      await hold();
    });

    await runner.step('naming_no_model_turns_it_off', async () => {
      const proposed = runAttn(['automode', 'model', '--none', '--json']).json;
      runner.assert(proposed.proposal.value === '', 'the CLI proposed no model at all', { proposed });
      runner.assert(await pillText() === 'Auto mode on', 'a proposal changes nothing on its own', {});
      note('the CLI proposed dropping every model, and nothing changed yet');
      await hold();

      await promote(proposed.proposal.id);
      const cleared = await pollFor(
        async () => {
          const state = await client.request('automode_get_state');
          return state.models.length === 0 ? state : null;
        },
        'the cleared model list to reach the pane',
      );
      runner.assert(cleared.enabledDefault === true,
        'the configured default is untouched, so the pill is answering the missing model', { cleared });
      runner.assert(await pillText() === 'Auto mode off',
        'no model means auto mode is off, whatever the default says', { cleared });
      const models = await client.request('dom_text', { selector: '[data-testid="automode-models"]' });
      runner.assert(models.text.includes('No model, so auto mode stays off'), 'the pane says why', { models });
      const chip = await client.request('dom_text', { selector: '[data-testid="settings-nav-autoMode"]' })
        .catch(() => ({ text: '' }));
      note(`the section chip reads ${JSON.stringify(chip.text)}`);
      note('promoting it left auto mode off, with the reason on screen');
      await hold();
    });

    await runner.step('naming_one_again_turns_it_back_on', async () => {
      const proposed = runAttn(['automode', 'model', MODEL, '--json']).json;
      await promote(proposed.proposal.id);
      const back = await pollFor(
        async () => {
          const state = await client.request('automode_get_state');
          return state.models.length > 0 ? state : null;
        },
        'the model to come back',
      );
      runner.assert(back.models[0] === MODEL, 'the model the CLI named is in force', { back });
      runner.assert(await pillText() === 'Auto mode on', 'the way out has a way back', { back });
      note('naming a model again turned auto mode back on');
      await hold();
    });

    const summary = runner.finishSuccess({ model: MODEL });
    console.log('[RealAppHarness] Auto mode no-model passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { model: MODEL });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
