#!/usr/bin/env node

import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  relaunchAppAndConnect,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { widenWindowForSplitPanes } from './nativeWindowCapture.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  assertPaneCoverage,
  assertPaneStyleSummaryPreserved,
  assertPaneVisibleContent,
  assertPaneVisibleContentPreserved,
  captureSessionArtifacts,
  compactTerminalText,
  scrollPaneToTop,
  waitForNewShellPane,
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneState,
  waitForPaneStyle,
  waitForPaneText,
  waitForPaneVisible,
  waitForSessionWorkspace,
  tokenAnchorIgnorePatterns,
} from './scenarioAssertions.mjs';
import {
  ensureClaudeInitialPanePromptReady,
  promptAgentForStructuredBlock,
  waitForMockAgentRepaintedToWidth,
  writeStructuredBlockFixture,
} from './scenarioAgents.mjs';

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

const COLOR_ROWS = 120;
const SCROLLBACK_LINES = 2000;
// `SNAPROW_000` matches rows 1-99 only, and the 120-row color block puts them at
// least 123 rows down, so that band is >= 98 rows deep: a 50-row step cannot skip it.
const DEEP_ROW_WHEEL_STEP = 50;

// The pane's login shell may be fish, so the POSIX-shell generators travel
// base64-wrapped and run under bash.
function bashSeedCommand(script) {
  return `echo ${Buffer.from(script).toString('base64')} | base64 -d | bash`;
}

function coloredRowsScript(seedEnd) {
  return [
    'i=1',
    `while [ $i -le ${COLOR_ROWS} ]; do case $((i % 4)) in 1) e='\\033[31m';; 2) e='\\033[32m';; 3) e='\\033[34m';; 0) e='\\033[43;30m';; esac; printf "${'${e}'}COLOR_%03d\\033[0m\\n" $i; i=$((i+1)); done`,
    "printf '\\033[38;2;255;0;255mTC_MAGENTA_BLOCK\\033[0m\\n'",
    `echo ${seedEnd}`,
  ].join('; ');
}

function formattingFixtureScript(token) {
  return [
    `printf '\\033[1;31m${token}-bold-red\\033[0m plain\\n'`,
    `printf '\\033[4;38;2;12;180;220m${token}-underline-rgb\\033[0m plain\\n'`,
    `printf '\\033[7;30;48;5;214m${token}-inverse-palette-bg\\033[0m plain\\n'`,
    `printf '\\033[3;38;5;45;48;2;24;24;96m${token}-italic-rgb-bg\\033[0m plain\\n'`,
  ].join('; ');
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr201-local-relaunch-existing-split.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-201',
    tier: 'tier2-local-mock-agent',
    prefix: 'scenario-tr201-local-relaunch-existing-split',
    metadata: {
      agent: 'claude',
      focus: 'relaunch restores an existing split with its content, SGR styling and deep colored scrollback',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let sessionId = null;
  let initialPaneId = null;
  let utilityPaneId = null;
  let baselineMainVisibleContent = null;
  let baselineStyle = null;
  const utilityToken = `TR201SHELL${Date.now()}`;
  const agentToken = `TR201CLAUDE${Date.now()}`;
  const formatToken = `TR201FMT${Date.now()}`;
  const expectedLastToken = `${formatToken}-italic-rgb-bg`;
  const colorSeedEnd = `COLOR_SEED_END_${Date.now()}`;
  const scrollbackTail = `SNAPTAIL${Date.now()}`;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      await widenWindowForSplitPanes(client);
    });

    sessionId = await runner.step('create_session', async () => {
      writeStructuredBlockFixture(runner.sessionDir, agentToken, 4);
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr201-local-${runner.runId}`,
        agent: 'claude',
        promptReadyFn: ensureClaudeInitialPanePromptReady,
      });
    });

    utilityPaneId = await runner.step('split_the_session_and_capture_the_agent_pane_baseline', async () => {
      const fixture = await promptAgentForStructuredBlock(client, sessionId, agentToken, 4);
      initialPaneId = fixture.paneId;
      runner.writeJson('agent-fixture.json', fixture);

      await waitForPaneText(
        client,
        sessionId,
        initialPaneId,
        (text) => text.includes(agentToken),
        'initial pane text before relaunch',
        45_000,
      );

      const preSplitState = await client.request('get_pane_state', { sessionId, paneId: initialPaneId });
      const preSplitCols = preSplitState?.pane?.visibleContent?.cols;
      if (typeof preSplitCols !== 'number' || preSplitCols <= 0) {
        throw new Error(`initial pane has no column count before split: ${JSON.stringify(preSplitState?.pane?.visibleContent)}`);
      }
      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: initialPaneId,
        direction: 'vertical',
      });
      const utilityPane = await waitForNewShellPane(
        client,
        sessionId,
        existingPaneIds,
        'utility pane before relaunch',
        20_000,
      );

      await waitForMockAgentRepaintedToWidth(client, sessionId, initialPaneId, {
        previousCols: preSplitCols,
        description: 'initial pane resized and repainted after split before baseline capture',
      });

      const baselineMainState = await assertPaneVisibleContent(client, sessionId, initialPaneId, {
        contains: agentToken,
        allowWrappedContains: true,
        minNonEmptyLines: 4,
        minDenseLines: 1,
        minCharCount: 90,
        minMaxLineLength: 16,
        timeoutMs: 30_000,
        description: 'initial pane visible content before relaunch',
      });
      baselineMainVisibleContent = baselineMainState?.pane?.visibleContent || null;
      await assertPaneCoverage(client, sessionId, initialPaneId, {
        minWidthRatio: 0.78,
        minHeightRatio: 0.72,
        timeoutMs: 20_000,
        description: 'initial pane coverage before relaunch',
      });

      await client.request('focus_pane', { sessionId, paneId: utilityPane.paneId });
      await waitForPaneVisible(client, sessionId, utilityPane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, utilityPane.paneId, 20_000);
      return utilityPane.paneId;
    });

    await runner.step('seed_colored_and_deep_scrollback_into_the_utility_pane', async () => {
      await client.request('write_pane', {
        sessionId,
        paneId: utilityPaneId,
        text: bashSeedCommand(coloredRowsScript(colorSeedEnd)),
      });
      await waitForPaneText(
        client,
        sessionId,
        utilityPaneId,
        (text) => text.includes(colorSeedEnd) && text.includes('TC_MAGENTA_BLOCK'),
        'colored scrollback seed output before relaunch',
        30_000,
      );

      await client.request('write_pane', {
        sessionId,
        paneId: utilityPaneId,
        text: `seq -f 'SNAPROW_%05g' 1 ${SCROLLBACK_LINES}; printf '${scrollbackTail}\\n'`,
      });
      await waitForPaneText(
        client,
        sessionId,
        utilityPaneId,
        (text) => text.includes(scrollbackTail),
        'deep numbered scrollback seed output before relaunch',
        60_000,
      );
    });

    await runner.step('seed_styled_rows_and_type_the_utility_token', async () => {
      await client.request('write_pane', {
        sessionId,
        paneId: utilityPaneId,
        text: bashSeedCommand(formattingFixtureScript(formatToken)),
      });
      await waitForPaneText(
        client,
        sessionId,
        utilityPaneId,
        (text) => compactTerminalText(text).includes(compactTerminalText(expectedLastToken)),
        'formatted utility pane text before relaunch',
        20_000,
      );

      await client.request('focus_pane', { sessionId, paneId: utilityPaneId });
      await waitForPaneState(
        client,
        sessionId,
        utilityPaneId,
        (state) => Boolean(state?.renderHealth?.flags?.terminalReady),
        'utility pane terminal ready before relaunch seed',
        20_000,
      );
      await client.request('type_pane_via_ui', {
        sessionId,
        paneId: utilityPaneId,
        text: utilityToken,
      });
      await client.request('write_pane', {
        sessionId,
        paneId: utilityPaneId,
        text: '\r',
        submit: false,
      });
      await waitForPaneText(
        client,
        sessionId,
        utilityPaneId,
        (text) => text.includes(utilityToken),
        'utility pane token before relaunch',
        15_000,
      );

      baselineStyle = await waitForPaneStyle(
        client,
        sessionId,
        utilityPaneId,
        (style) => {
          const summary = style?.summary || {};
          return (
            (summary.styledCellCount || 0) >= 40 &&
            (summary.styledLineCount || 0) >= 4 &&
            (summary.boldCellCount || 0) >= 8 &&
            (summary.italicCellCount || 0) >= 8 &&
            (summary.underlineCellCount || 0) >= 8 &&
            (summary.inverseCellCount || 0) >= 8 &&
            (summary.fgRgbCellCount || 0) >= 8 &&
            (summary.bgRgbCellCount || 0) >= 8 &&
            (summary.uniqueStyleCount || 0) >= 4
          );
        },
        'formatted utility pane style before relaunch',
        20_000,
      );
      runner.writeJson('formatting-baseline-style.json', baselineStyle);

      await assertPaneVisibleContent(client, sessionId, utilityPaneId, {
        contains: utilityToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: utilityToken.length,
        minMaxLineLength: utilityToken.length,
        timeoutMs: 15_000,
        description: 'utility pane visible content before relaunch',
      });
      await assertPaneVisibleContent(client, sessionId, utilityPaneId, {
        contains: expectedLastToken,
        allowWrappedContains: true,
        minNonEmptyLines: 4,
        minDenseLines: 1,
        minCharCount: 80,
        minMaxLineLength: 18,
        timeoutMs: 20_000,
        description: 'formatted utility pane visible content before relaunch',
      });
      await assertPaneCoverage(client, sessionId, utilityPaneId, {
        minWidthRatio: 0.78,
        minHeightRatio: 0.72,
        timeoutMs: 20_000,
        description: 'utility pane coverage before relaunch',
      });

      await captureSessionArtifacts(client, runner.runDir, '01-pre-relaunch', sessionId);
    });

    await runner.step('relaunch_restores_both_panes_with_content_and_coverage', async () => {
      await relaunchAppAndConnect(client, observer);
      await widenWindowForSplitPanes(client);
      await client.request('select_session', { sessionId });

      const restoredWorkspace = await waitForSessionWorkspace(
        client,
        sessionId,
        (workspace) => {
          const paneIds = new Set((workspace.panes || []).map((pane) => pane.paneId));
          return paneIds.has(initialPaneId) && paneIds.has(utilityPaneId);
        },
        `restored split workspace for ${sessionId}`,
        30_000,
      );
      runner.assert((restoredWorkspace.panes || []).length >= 2, 'restored workspace still exposes both split panes', {
        sessionId,
        utilityPaneId,
        paneCount: (restoredWorkspace.panes || []).length,
      });

      if (!initialPaneId) {
        initialPaneId = (await waitForFirstWorkspacePane(client, sessionId, 'restored initial pane', 20_000)).paneId;
      }
      await waitForPaneVisible(client, sessionId, initialPaneId, 20_000);
      await waitForPaneVisible(client, sessionId, utilityPaneId, 20_000);

      await assertPaneVisibleContent(client, sessionId, initialPaneId, {
        contains: agentToken,
        allowWrappedContains: true,
        minNonEmptyLines: 4,
        minDenseLines: 1,
        minCharCount: 90,
        minMaxLineLength: 16,
        timeoutMs: 30_000,
        description: 'initial pane visible content after relaunch',
      });
      await assertPaneVisibleContentPreserved(
        client,
        sessionId,
        initialPaneId,
        baselineMainVisibleContent,
        {
          minNonEmptyLineRatio: 0.75,
          minCharCountRatio: 0.7,
          minAnchorMatches: 2,
          // Anchor only on token lines (claude echo/reflow flake).
          ignoreAnchorPatterns: tokenAnchorIgnorePatterns(agentToken),
          timeoutMs: 20_000,
          description: 'initial pane content preserved after relaunch',
        },
      );
      await assertPaneCoverage(client, sessionId, initialPaneId, {
        minWidthRatio: 0.78,
        minHeightRatio: 0.72,
        timeoutMs: 20_000,
        description: 'initial pane coverage after relaunch',
      });

      await assertPaneVisibleContent(client, sessionId, utilityPaneId, {
        contains: utilityToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: utilityToken.length,
        minMaxLineLength: utilityToken.length,
        timeoutMs: 20_000,
        description: 'utility pane visible content after relaunch',
      });
      await assertPaneCoverage(client, sessionId, utilityPaneId, {
        minWidthRatio: 0.78,
        minHeightRatio: 0.72,
        timeoutMs: 20_000,
        description: 'utility pane coverage after relaunch',
      });
    });

    await runner.step('relaunch_preserves_the_utility_pane_styles', async () => {
      await waitForPaneText(
        client,
        sessionId,
        utilityPaneId,
        (text) => compactTerminalText(text).includes(compactTerminalText(expectedLastToken)),
        'formatted utility pane text after relaunch',
        20_000,
      );
      const restoredStyle = await assertPaneStyleSummaryPreserved(
        client,
        sessionId,
        utilityPaneId,
        baselineStyle?.style || null,
        {
          minStyledCellRatio: 0.85,
          minStyledLineRatio: 0.75,
          minBoldCellRatio: 0.75,
          minUnderlineCellRatio: 0.75,
          minInverseCellRatio: 0.75,
          minFgRgbCellRatio: 0.75,
          minBgRgbCellRatio: 0.75,
          minUniqueStyleRatio: 0.75,
          timeoutMs: 20_000,
          description: 'formatted utility pane style after relaunch',
        },
      );
      await assertPaneVisibleContent(client, sessionId, utilityPaneId, {
        contains: expectedLastToken,
        allowWrappedContains: true,
        minNonEmptyLines: 4,
        minDenseLines: 1,
        minCharCount: 80,
        minMaxLineLength: 18,
        timeoutMs: 20_000,
        description: 'formatted utility pane visible content after relaunch',
      });
      runner.writeJson('formatting-restored-style.json', restoredStyle);
    });

    // Scrolling moves the viewport the content and style steps read, so this step
    // stays last: reordering it above them breaks both without touching the product.
    await runner.step('restored_scrollback_reaches_its_colored_top_and_deep_rows', async () => {
      const payload = await client.request('read_pane_text', { sessionId, paneId: utilityPaneId }, { timeoutMs: 20_000 });
      const body = typeof payload?.text === 'string' ? payload.text : '';
      const rows = (body.match(/SNAPROW_\d{5}/g) || []);
      runner.assert(rows.length > 1000, 'restored buffer carries deep scrollback', {
        restoredRows: rows.length,
        seededRows: SCROLLBACK_LINES,
        firstRow: rows[0] || null,
      });

      await scrollPaneToTop(client, sessionId, utilityPaneId);
      // Read the VISIBLE viewport, not the whole scrollback: COLOR_001 is on screen
      // only at the top of history, and COLOR_100 out of view means a completed buffer.
      await waitForPaneState(
        client,
        sessionId,
        utilityPaneId,
        (state) => {
          const visible = (state?.pane?.visibleContent?.lines || []).join('\n');
          return visible.includes('COLOR_001') && !visible.includes('COLOR_100');
        },
        'top of colored scrollback visible in the viewport after relaunch',
        20_000,
      );

      const deepRowsStartedAt = Date.now();
      let deepRowsVisible = false;
      let lastVisibleTop = '';
      while (Date.now() - deepRowsStartedAt < 30_000) {
        const state = await client.request('get_pane_state', { sessionId, paneId: utilityPaneId });
        const visible = (state?.pane?.visibleContent?.lines || []).join('\n');
        if (visible.includes('SNAPROW_000')) {
          deepRowsVisible = true;
          break;
        }
        lastVisibleTop = (state?.pane?.visibleContent?.lines || [])[0] || '';
        await client.request('wheel_pane', {
          sessionId,
          paneId: utilityPaneId,
          deltaY: DEEP_ROW_WHEEL_STEP,
          deltaMode: 1,
        });
      }
      runner.assert(deepRowsVisible, 'early numbered scrollback rows are reachable by scrolling after relaunch', {
        wheelStepRows: DEEP_ROW_WHEEL_STEP,
        lastVisibleTop,
      });

      await captureSessionArtifacts(client, runner.runDir, '02-post-relaunch', sessionId);
    });

    const summary = await runner.finishSuccess({
      sessionId,
      utilityPaneId,
      tokens: {
        agentToken,
        utilityToken,
        formatToken,
        colorSeedEnd,
        scrollbackTail,
      },
      artifacts: {
        runDir: runner.runDir,
        trace: runner.tracePath,
      },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', sessionId).catch(() => {});
    }
    const summary = await runner.finishFailure(error, {
      sessionId,
      utilityPaneId,
      tokens: {
        agentToken,
        utilityToken,
        formatToken,
        colorSeedEnd,
        scrollbackTail,
      },
    });
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
  process.exit(1);
});
