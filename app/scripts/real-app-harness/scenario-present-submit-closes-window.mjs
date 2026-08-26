#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { buildDiffFixtureRepo } from './diffFixtureRepo.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

function resolveAttnBin() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(HARNESS_DIR, '../../../attn')].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error('attn binary not found (build ./attn or set ATTN_HARNESS_BIN)');
}

function makeAttnRunner(attnBin, profile) {
  return function runAttn(args) {
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      env: { ...process.env, ATTN_PROFILE: profile },
    });
    const brace = stdout.indexOf('{');
    return { stdout, json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null };
  };
}

async function pollFor(fn, description, timeoutMs, intervalMs = 500) {
  const startedAt = Date.now();
  let lastError = null;
  let lastValue = null;
  while (Date.now() - startedAt < timeoutMs) {
    try {
      const result = await fn();
      lastValue = result;
      if (result) return result;
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  throw new Error(
    `Timed out waiting for: ${description}. Last value: ${JSON.stringify(lastValue)}${
      lastError ? ` Last error: ${lastError instanceof Error ? lastError.message : lastError}` : ''
    }`,
  );
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-present-submit-closes-window.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error(
      'the present-submit-closes-window scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile',
    );
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'PRESENT-SUBMIT-CLOSES-WINDOW',
    tier: 'tier1-local-shell',
    prefix: 'scenario-present-submit-closes-window',
    metadata: {
      focus: 'submitting a present review hides the real presentation window',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;

  try {
    const { repoDir } = await runner.step('build_diff_fixture_repo', async () => {
      return buildDiffFixtureRepo(path.join(runner.sessionDir, 'present-fixture'));
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_session', async () => {
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: repoDir,
        label: `present-submit-${runner.runId.slice(-6)}`,
        agent: 'shell',
        sessionWaitMs: 30_000,
      });
    });
    runner.log('session_ready', { sessionId, repoDir });

    const presentationId = await runner.step('open_presentation', async () => {
      const manifestPath = path.join(runner.sessionDir, 'present-submit-scenario.present.yml');
      const manifestYaml = [
        'version: 1',
        'kind: changes',
        'title: "Present submit close scenario"',
        'summary: "Harness fixture exercising the present-window submit -> auto-close path."',
        'frame:',
        `  repo: ${repoDir}`,
        '  base: main',
        '  head: feature',
        'files:',
        '  - path: src/app.ts',
        '    note: "Modified between main and feature (two hunks) — a real changed file for this scenario."',
        '',
      ].join('\n');
      fs.writeFileSync(manifestPath, manifestYaml, 'utf8');

      const opened = runAttn(['present', '--manifest', manifestPath, '--session', sessionId, '--json']);
      const id = opened.json?.presentation_id;
      runner.assert(
        typeof id === 'string' && id.length > 0,
        `attn present returned a presentation_id (got ${JSON.stringify(opened.json)})`,
        opened.json,
      );
      return id;
    });
    runner.log('presentation_opened', { presentationId });

    await runner.step('select_session', async () => {
      await client.request('select_session', { sessionId });
    });

    const chipClick = await runner.step('click_presentation_chip', async () => {
      return pollFor(
        () => client.request('present_click_chip', { presentationId }, { timeoutMs: 5_000 }).catch(() => null),
        'presentation chip to render in the pane header',
        20_000,
        500,
      );
    });
    runner.assert(
      chipClick?.clicked === true && chipClick?.presentationId === presentationId,
      `the presentation chip was found and clicked (got ${JSON.stringify(chipClick)})`,
      chipClick,
    );

    await runner.step('wait_for_present_window_visible', async () => {
      const result = await pollFor(
        () =>
          client
            .request('present_window_is_visible', {}, { timeoutMs: 4_000 })
            .then((r) => (r?.visible === true ? r : null))
            .catch(() => null),
        'the present window to open and report visible',
        30_000,
        500,
      );
      runner.assert(result.visible === true, `present window reported visible (got ${JSON.stringify(result)})`, result);
    });

    await runner.step('submit_review', async () => {
      const result = await client.request('present_window_submit', {}, { timeoutMs: 15_000 });
      runner.assert(result?.submitted === true, `present_window_submit dispatched the confirm click (got ${JSON.stringify(result)})`, result);
    });

    await runner.step('wait_for_present_window_hidden', async () => {
      // Only passes if the present window's getCurrentWindow().hide() succeeds,
      // which requires the core:window:allow-hide capability.
      const result = await pollFor(
        () =>
          client
            .request('present_window_is_visible', {}, { timeoutMs: 3_000 })
            .then((r) => (r?.visible === false ? r : null))
            .catch(() => null),
        'the present window to hide after submit',
        15_000,
        500,
      );
      runner.assert(result.visible === false, `present window reported hidden after submit (got ${JSON.stringify(result)})`, result);
    });

    const summary = await runner.finishSuccess({ sessionId, presentationId });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      await cleanupSessionViaAppClose(client, observer, sessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
