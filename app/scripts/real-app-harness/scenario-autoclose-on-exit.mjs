#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
  waitForSessionDesktop,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { createWindowDriver } from './platform.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const options = parseCommonArgs(args);
  return {
    options,
    help: args.includes('--help') || args.includes('-h'),
  };
}

async function waitForPaneCount(client, sessionId, count, description, timeoutMs = 30_000) {
  return waitForSessionDesktop(
    client,
    sessionId,
    (desktop) => (desktop?.panes || []).length === count && (desktop?.panes || []).every((pane) => pane.runtimeId),
    description,
    timeoutMs,
  );
}

async function waitForShellDesktop(client, observer, cwd, label) {
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'shell',
    waitForInitialPaneVisible: false,
    sessionWaitMs: 30_000,
  });
  const desktop = await waitForPaneCount(client, sessionId, 1, `initial pane for ${label}`);
  const pane = desktop.panes[0];
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId, {
    timeoutMs: 20_000,
    description: `shell prompt ready for ${label}`,
  });
  return { sessionId, pane };
}

async function waitForSessionAbsentFromDaemon(observer, sessionId, description, timeoutMs = 20_000) {
  await observer.waitFor(
    () => (observer.getSession(sessionId) == null ? true : null),
    description,
    timeoutMs,
  );
}

async function waitForSessionGoneFromUi(client, sessionId, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_session_ui_state', { sessionId }).catch((error) => ({ error: String(error) }));
    if (lastState?.exists === false && lastState?.sidebarItem == null) {
      return lastState;
    }
    await delay(200);
  }
  throw new Error(`Timed out waiting for ${description}. Last UI state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForPaneTextContains(client, sessionId, paneId, needle, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastText = '';
  while (Date.now() - startedAt < timeoutMs) {
    const result = await client.request('read_pane_text', { sessionId, paneId }).catch((error) => ({ text: '', error: String(error) }));
    lastText = result?.text || '';
    if (lastText.includes(needle)) {
      return lastText;
    }
    await delay(250);
  }
  throw new Error(`Timed out waiting for ${description}. Last pane text tail:\n${lastText.slice(-400)}`);
}

async function closeDesktopPanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const desktop = await client.request('get_desktop', { sessionId }).catch(() => null);
    const pane = desktop?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await delay(200);
  }
}

async function closeExistingSessions(client, sessionRootDir) {
  const initial = await client.request('get_state');
  const harnessSessions = (initial.sessions || []).filter((session) => session.cwd?.startsWith(sessionRootDir));
  for (const session of harnessSessions) {
    await closeDesktopPanes(client, session.id).catch(() => {});
  }
}

async function waitForNoSessionsUnderDir(client, dir, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastSessions = [];
  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state').catch(() => null);
    lastSessions = (state?.sessions || []).filter((session) => session.cwd?.startsWith(dir));
    if (lastSessions.length === 0) {
      return;
    }
    await delay(200);
  }
  throw new Error(`Timed out waiting for harness sessions under ${dir} to close: ${JSON.stringify(lastSessions, null, 2)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-autoclose-on-exit.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'AUTOCLOSE-ON-EXIT',
    tier: 'tier1-local-shell',
    prefix: 'autoclose-on-exit',
    metadata: {
      agent: 'shell',
      focus: 'clean quits close; stopped screens survive relaunch and resume only on request',
    },
  });

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const createdSessionIds = [];

  runner.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  // Runner cleanups run in REVERSE registration order: observer/app are
  // registered first so they close LAST.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('wait_no_sessions_under_dir', () => waitForNoSessionsUnderDir(client, runner.sessionDir).catch(() => {}));
  runner.registerCleanup('close_created_session_panes', async () => {
    for (const sessionId of [...createdSessionIds].reverse()) {
      await closeDesktopPanes(client, sessionId).catch(() => {});
    }
  });

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      await closeExistingSessions(client, options.sessionRootDir);
    });

    const clean = await runner.step('clean_exit_auto_closes', async () => {
      const session = await waitForShellDesktop(client, observer, path.join(runner.sessionDir, 'clean'), `autoclose-clean-${runner.runId}`);
      createdSessionIds.push(session.sessionId);
      await client.request('write_pane', { sessionId: session.sessionId, paneId: session.pane.paneId, text: 'exit', submit: true });
      await waitForSessionAbsentFromDaemon(observer, session.sessionId, 'clean-exit session unregistered from daemon');
      await waitForSessionGoneFromUi(client, session.sessionId, 'clean-exit session gone from UI/sidebar');
      runner.log('[RealAppHarness] Clean exit auto-closed the session.');
      return session;
    });

    const failed = await runner.step('nonzero_exit_stays_open', async () => {
      const session = await waitForShellDesktop(client, observer, path.join(runner.sessionDir, 'failed'), `autoclose-failed-${runner.runId}`);
      createdSessionIds.push(session.sessionId);
      await client.request('write_pane', { sessionId: session.sessionId, paneId: session.pane.paneId, text: 'echo retained-output-42; exit 1', submit: true });
      await waitForPaneTextContains(
        client,
        session.sessionId,
        session.pane.paneId,
        '[Process exited with code 1]',
        'failed-exit pane shows exit banner',
      );
      // Give any (incorrect) auto-close a chance to fire, then assert the session survived.
      await delay(2_000);
      runner.assert(
        observer.getSession(session.sessionId) != null,
        `Non-zero exit session ${session.sessionId} was auto-closed; failed exits must stay open`,
      );
      const failedUi = await client.request('get_session_ui_state', { sessionId: session.sessionId });
      runner.assert(
        failedUi.exists !== false && failedUi.sidebarItem != null,
        `Non-zero exit session ${session.sessionId} missing from UI; failed exits must stay open`,
        failedUi,
      );
      runner.log('[RealAppHarness] Non-zero exit kept the session open.');
      return session;
    });

    const notice = `[data-pane-id="${failed.pane.paneId}"] .desktop-agent-stopped`;
    const driver = createWindowDriver({ appPath: options.appPath, client });
    const clickNoticeButton = async (index) => {
      const selector = `${notice} button:nth-of-type(${index})`;
      const [{ bounds }, { logicalBounds }, { innerWidth, innerHeight }] = await Promise.all([
        client.request('dom_hover', { selector, leave: true }),
        client.request('get_window_bounds'),
        client.request('get_terminal_context_menu_state'),
      ]);
      await driver.clickWindow(
        (Math.max(0, logicalBounds.width - innerWidth) / 2 + bounds.x + bounds.width / 2) / logicalBounds.width,
        (Math.max(0, logicalBounds.height - innerHeight) + bounds.y + bounds.height / 2) / logicalBounds.height,
      );
    };
    await runner.step('stopped_screen_survives_app_relaunch', async () => {
      runner.assert((await client.request('dom_wait', { selector: notice, timeoutMs: 10_000 })).matched, 'stopped notice offers Resume');
      await client.launchFreshApp();
      await client.waitForManifest(20_000);
      await client.waitForReady(20_000);
      await client.waitForFrontendResponsive(20_000);
      await client.request('select_session', { sessionId: failed.sessionId });
      await waitForPaneTextContains(client, failed.sessionId, failed.pane.paneId, 'retained-output-42', 'saved final screen restored');
      const text = (await client.request('read_pane_text', { sessionId: failed.sessionId, paneId: failed.pane.paneId })).text;
      runner.assert(!text.includes('Failed to attach PTY'), 'stopped screen opens without attaching to a missing runtime');
      runner.assert((await client.request('dom_wait', { selector: notice, timeoutMs: 10_000 })).matched, 'agent remains stopped after reopening');
      await captureScreenshotData(path.join(runner.runDir, 'stopped-notice.png'), { client, selector: notice });
    });
    await runner.step('resume_button_restarts_only_on_request', async () => {
      await clickNoticeButton(1);
      runner.assert((await client.request('dom_wait', { selector: notice, absent: true, timeoutMs: 15_000 })).matched, 'Resume removes the stopped notice');
      await waitForPaneShellReady(client, failed.sessionId, failed.pane.paneId, { timeoutMs: 20_000, description: 'resumed shell ready' });
      await client.request('write_pane', { sessionId: failed.sessionId, paneId: failed.pane.paneId, text: 'exit 1', submit: true });
      runner.assert((await client.request('dom_wait', { selector: notice, timeoutMs: 10_000 })).matched, 'later exit is stopped again');
      await clickNoticeButton(2);
      await waitForSessionAbsentFromDaemon(observer, failed.sessionId, 'Close ends the stopped session');
      await waitForSessionGoneFromUi(client, failed.sessionId, 'closed stopped session leaves the UI');
    });

    const summary = await runner.finishSuccess({
      cleanSessionId: clean.sessionId,
      failedSessionId: failed.sessionId,
    });
    console.log('[RealAppHarness] Auto-close-on-exit passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { createdSessionIds });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of createdSessionIds.reverse()) {
      await closeDesktopPanes(client, sessionId).catch(() => {});
    }
    await waitForNoSessionsUnderDir(client, runner.sessionDir).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
