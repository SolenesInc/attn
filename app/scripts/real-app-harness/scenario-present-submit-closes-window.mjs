#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import { buildPresentFixtureRepo } from './presentFixtureRepo.mjs';
import { getPresentations, getPresentationRound } from './presentDaemon.mjs';
import { currentHarnessProfile, defaultDaemonPortForProfile, profileCliEnv, socketPathForProfile } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
// The em dash (U+2014) must match the native window title set in
// app/src-tauri/src/lib.rs exactly, or waitForWindowTitled never matches.
const PRESENT_WINDOW_TITLE = 'attn — present';
const MANIFEST_TITLE = 'Present flow smoke';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 250) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
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

function startWaitingPresent(attnBin, profile, { cwd, sessionId }) {
  const child = spawn(attnBin, ['present', '--wait', '--json'], {
    cwd,
    env: profileCliEnv(profile, {
      ATTN_SOCKET_PATH: socketPathForProfile(profile),
      ATTN_SESSION_ID: sessionId,
    }),
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stderr.on('data', (chunk) => { stderr += chunk; });

  const completion = new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('close', (code, signal) => {
      if (code !== 0) {
        reject(new Error(`attn present --wait exited with code ${code} signal ${signal}: ${stderr}`));
        return;
      }
      const brace = stdout.indexOf('{');
      resolve({ stdout, stderr, json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null });
    });
  });
  completion.catch(() => {});

  return { completion, kill: () => child.kill('SIGTERM') };
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
  const port = defaultDaemonPortForProfile(profile);
  const attnBin = resolveAttnBin();

  const runner = createScenarioRunner(options, {
    scenarioId: 'PRESENT-SUBMIT-CLOSES-WINDOW',
    tier: 'tier1-local-shell',
    prefix: 'scenario-present-submit-closes-window',
    metadata: {
      focus: 'waiting CLI -> notice+chip -> real present window -> submit from the window -> the window hides and the CLI returns',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath });

  let sessionId = null;
  let presentationId = null;
  let waitingPresent = null;

  try {
    const { repoDir, baseSha, headSha } = await runner.step('build_present_fixture_repo', async () => {
      const fixture = buildPresentFixtureRepo(runner.sessionDir);
      runner.log('fixture_built', { repoDir: fixture.repoDir, baseSha: fixture.baseSha, headSha: fixture.headSha });
      return fixture;
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

    presentationId = await runner.step('a_waiting_present_opens_a_presentation', async () => {
      const existingIds = new Set((await getPresentations({ port })).map((p) => p.id));
      waitingPresent = startWaitingPresent(attnBin, profile, { cwd: repoDir, sessionId });
      runner.registerCleanup('stop_waiting_present', () => waitingPresent?.kill());
      const opened = await Promise.race([
        pollFor(async () => {
          const presentations = await getPresentations({ port });
          return presentations.find((p) => !existingIds.has(p.id) && p.session_id === sessionId) || null;
        }, 'attn present --wait to open a presentation', 30_000),
        waitingPresent.completion.then(({ stdout, stderr }) => {
          throw new Error(`attn present --wait returned before review: stdout=${stdout} stderr=${stderr}`);
        }),
      ]);
      const presentations = await getPresentations({ port });
      runner.assert(
        presentations.some((p) => p.id === opened.id),
        'daemon get_presentations includes the opened presentation',
        { id: opened.id, presentations },
      );
      return opened.id;
    });
    runner.log('presentation_opened', { presentationId });

    await runner.step('select_session', async () => {
      await client.request('select_session', { sessionId });
    });

    await runner.step('the_app_shows_a_notice_and_a_chip_titled_from_the_manifest', async () => {
      const state = await pollFor(
        async () => {
          const s = await client.request('present_get_state');
          const notice = (s.notices || []).find((n) => n.id === presentationId);
          const chip = (s.chips || []).find((c) => c.presentationId === presentationId);
          return notice && chip ? { notice, chip } : null;
        },
        `present_get_state to include notice+chip for presentation ${presentationId}`,
        30_000,
      );
      runner.assert(
        state.notice.title === MANIFEST_TITLE,
        `notice title matches the manifest (got ${JSON.stringify(state.notice.title)})`,
      );
      runner.assert(
        state.chip.title === MANIFEST_TITLE,
        `chip title matches the manifest (got ${JSON.stringify(state.chip.title)})`,
      );
    });

    const presentWindow = await runner.step('clicking_the_chip_opens_a_real_native_window', async () => {
      const chipClick = await pollFor(
        () => client.request('present_click_chip', { presentationId }, { timeoutMs: 5_000 }).catch(() => null),
        'presentation chip to render in the pane header',
        20_000,
      );
      runner.assert(
        chipClick?.clicked === true && chipClick?.presentationId === presentationId,
        `the presentation chip was found and clicked (got ${JSON.stringify(chipClick)})`,
        chipClick,
      );

      const win = await driver.waitForWindowTitled(PRESENT_WINDOW_TITLE, { timeoutMs: 15_000 });
      runner.assert(Boolean(win), `present window "${PRESENT_WINDOW_TITLE}" opened`);
      runner.log('present_window_bounds', win);

      const visible = await pollFor(
        () =>
          client
            .request('present_window_is_visible', {}, { timeoutMs: 4_000 })
            .then((r) => (r?.visible === true ? r : null))
            .catch(() => null),
        'the present window to report visible',
        30_000,
      );
      runner.assert(visible.visible === true, `present window reported visible (got ${JSON.stringify(visible)})`, visible);

      try {
        await client.request('capture_native_window_screenshot', {
          path: path.join(runner.runDir, 'present-window.png'),
        });
      } catch (error) {
        runner.log('present_window_screenshot_skipped', {
          error: error instanceof Error ? error.message : String(error),
        });
      }
      return win;
    });

    await runner.step('submitting_from_the_window_hides_it', async () => {
      // The window reports visible before its drive bar mounts, and the handler
      // looks the button up by class; only that miss is a not-ready-yet.
      const result = await pollFor(
        () => client.request('present_window_submit', {}, { timeoutMs: 15_000 }).catch((error) => {
          if (String(error?.message || error).includes('submit button not found')) return null;
          throw error;
        }),
        'the present window drive bar to mount its submit button',
        30_000,
      );
      runner.assert(result?.submitted === true, `present_window_submit dispatched the confirm click (got ${JSON.stringify(result)})`, result);

      // Only passes if the present window's getCurrentWindow().hide() succeeds,
      // which requires the core:window:allow-hide capability.
      const hidden = await pollFor(
        () =>
          client
            .request('present_window_is_visible', {}, { timeoutMs: 3_000 })
            .then((r) => (r?.visible === false ? r : null))
            .catch(() => null),
        'the present window to hide after submit',
        15_000,
      );
      runner.assert(hidden.visible === false, `present window reported hidden after submit (got ${JSON.stringify(hidden)})`, hidden);
    });

    const submittedAt = await runner.step('the_window_submit_recorded_the_round', async () => {
      const roundResult = await pollFor(
        async () => {
          const round = await getPresentationRound(presentationId, { port });
          return round.round?.submitted_at ? round : null;
        },
        'the round the window submitted to carry a submitted_at',
        15_000,
      );
      return roundResult.round.submitted_at;
    });

    await runner.step('the_waiting_cli_returns_with_its_feedback', async () => {
      const { json, stderr } = await waitingPresent.completion;
      waitingPresent = null;
      runner.assert(
        stderr.includes('waiting for review of round'),
        'attn present --wait reported that it was synchronously waiting',
        { stderr },
      );
      runner.assert(json && typeof json.markdown === 'string', 'attn present --wait returned a markdown field', json);
    });

    const summary = await runner.finishSuccess({ sessionId, presentationId, window: presentWindow, submittedAt, baseSha, headSha });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionId, presentationId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    waitingPresent?.kill();
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
