#!/usr/bin/env node

import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  captureSessionArtifacts,
  scrollPaneToTop,
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneState,
  waitForPaneText,
} from './scenarioAssertions.mjs';

function withTimeout(promise, timeoutMs) {
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      setTimeout(() => reject(new Error(`Timed out after ${timeoutMs}ms`)), timeoutMs);
    }),
  ]);
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-scrollback-colors.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-SCROLLBACK-COLORS',
    tier: 'tier1-local-shell',
    prefix: 'scenario-terminal-scrollback-colors',
    metadata: {
      focus: 'scrollback rows keep their foreground and background colors when scrolled into view',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const seedEnd = `COLOR_SEED_END_${Date.now()}`;
  let sessionId = null;
  let shellPaneId = null;

  // The pane's login shell may be fish, so the POSIX-shell generator travels
  // base64-wrapped and runs under bash.
  const fillScript = [
    'i=1',
    "while [ $i -le 120 ]; do case $((i % 4)) in 1) e='\\033[31m';; 2) e='\\033[32m';; 3) e='\\033[34m';; 0) e='\\033[43;30m';; esac; printf \"${e}COLOR_%03d\\033[0m\\n\" $i; i=$((i+1)); done",
    "printf '\\033[38;2;255;0;255mTC_MAGENTA_BLOCK\\033[0m\\n'",
    `echo ${seedEnd}`,
  ].join('; ');
  const fill = `echo ${Buffer.from(fillScript).toString('base64')} | base64 -d | bash`;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_session', async () => {
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `scroll-colors-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
      });
    });

    shellPaneId = await runner.step('open_shell_pane', async () => {
      const initialPane = await waitForFirstWorkspacePane(client, sessionId, 'initial shell pane', 20_000);
      shellPaneId = initialPane.paneId;
      await waitForPaneAttached(client, sessionId, shellPaneId, 20_000);
      return shellPaneId;
    });

    await runner.step('fill_scrollback_with_colors', async () => {
      await client.request('write_pane', {
        sessionId,
        paneId: shellPaneId,
        text: fill,
      });
      await waitForPaneText(
        client,
        sessionId,
        shellPaneId,
        (text) => text.includes(seedEnd) && text.includes('TC_MAGENTA_BLOCK'),
        'colored scrollback seed output',
        30_000,
      );
      // The seed marker also matches the typed command's echo, so hold until
      // the pane text is stable or the scroll races the tail of the stream.
      const settleMs = 600;
      let lastText = '';
      const startedAt = Date.now();
      while (Date.now() - startedAt < 10_000) {
        let text = '';
        try {
          const state = await client.request('get_pane_state', { sessionId, paneId: shellPaneId });
          text = ((state?.pane?.visibleContent?.lines) || []).join('\n');
        } catch {}
        if (text.includes(seedEnd) && text === lastText) break;
        lastText = text;
        await new Promise((resolve) => setTimeout(resolve, settleMs));
      }
    });

    await runner.step('scroll_to_top_and_capture', async () => {
      await scrollPaneToTop(client, sessionId, shellPaneId);
      // The assertion must read the VISIBLE viewport, not the whole
      // scrollback: COLOR_001 is only on screen when the top of history is.
      await waitForPaneState(
        client,
        sessionId,
        shellPaneId,
        (state) => {
          const visible = (state?.pane?.visibleContent?.lines || []).join('\n');
          // COLOR_001 alone can be true mid-stream; requiring a deep line OUT
          // of view pins this to the top of a completed buffer.
          return visible.includes('COLOR_001') && !visible.includes('COLOR_100');
        },
        'top of colored scrollback visible in the viewport',
        20_000,
      );
      await new Promise((resolve) => setTimeout(resolve, 1500));
      await captureSessionArtifacts(client, runner.runDir, '01-scrolled-to-top-colored', sessionId);
    });

    const summary = runner.finishSuccess({ sessionId, shellPaneId, seedEnd });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', sessionId).catch(() => {});
    }
    const summary = runner.finishFailure(error, { sessionId, shellPaneId, seedEnd });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    try {
      await withTimeout(cleanupSessionViaAppClose(client, observer, sessionId), 15_000);
    } catch {}
    try {
      await withTimeout(client.quitApp(), 5_000);
    } catch {}
    try {
      await withTimeout(observer.disconnect(), 5_000);
    } catch {}
  }
}

main()
  .then(() => {
    process.exit(process.exitCode ?? 0);
  })
  .catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
