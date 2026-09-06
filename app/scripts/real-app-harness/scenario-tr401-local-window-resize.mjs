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
import { getFrontWindowBounds, setFrontWindowBounds } from './nativeWindowCapture.mjs';
import {
  assertPaneCoverage,
  assertPaneNativePaintCoverage,
  assertPaneNativePaintRecovered,
  assertPaneUsesVisibleWidth,
  assertPaneVisibleContent,
  assertPaneVisibleContentPreserved,
  captureSessionArtifacts,
  sleep,
  waitForFirstWorkspacePane,
  waitForNewShellPane,
  waitForPaneAttached,
  waitForPaneState,
  waitForPaneText,
  waitForPaneVisible,
  waitForSessionWorkspace,
  tokenAnchorIgnorePatterns,
} from './scenarioAssertions.mjs';
import {
  ensureCodexInitialPanePromptReady,
  ensureClaudeInitialPanePromptReady,
  promptAgentForStructuredBlock,
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

// The shrunk window still has to clear the workspace's 1248px split floor (see
// widenWindowForSplitPanes), which 1560 at 0.82 does with 31px to spare.
const BASELINE_WINDOW = { width: 1_560, height: 900 };
const SHRINK_RATIO = { width: 0.82, height: 0.82 };
const STRUCTURED_BLOCK_LINES = 6;

function normalizeBaselineWindowBounds(bounds) {
  return {
    x: 80,
    y: 80,
    width: Math.max(bounds.width, BASELINE_WINDOW.width),
    height: Math.max(bounds.height, BASELINE_WINDOW.height),
  };
}

function shrunkWindowBounds(bounds) {
  const width = Math.floor(bounds.width * SHRINK_RATIO.width);
  const height = Math.floor(bounds.height * SHRINK_RATIO.height);
  if (width >= bounds.width || height >= bounds.height) {
    throw new Error(`Computed shrink target does not reduce bounds: ${JSON.stringify(bounds)}`);
  }
  return {
    x: bounds.x,
    y: bounds.y,
    width,
    height,
  };
}

// 704px is the width the retired tr401-codex-initial-pane actually ran green at.
// Only one pane exists at this point, so the 1248px split floor does not apply.
const CODEX_HEADER_NARROW_WIDTH = 704;

function narrowWindowBounds(bounds) {
  if (CODEX_HEADER_NARROW_WIDTH >= bounds.width) {
    throw new Error(`Narrow target ${CODEX_HEADER_NARROW_WIDTH} does not reduce bounds: ${JSON.stringify(bounds)}`);
  }
  return {
    x: bounds.x,
    y: bounds.y,
    width: CODEX_HEADER_NARROW_WIDTH,
    height: bounds.height,
  };
}

function paneShrinkThreshold(bounds) {
  return {
    width: Math.floor(bounds.width * 0.9),
    height: Math.floor(bounds.height * 0.9),
  };
}

function paneRecoveryThreshold(bounds) {
  return {
    width: Math.floor(bounds.width * 0.95),
    height: Math.floor(bounds.height * 0.95),
  };
}

function recoveredWidthThreshold(baselineWidth) {
  if (!Number.isFinite(baselineWidth) || baselineWidth <= 0) {
    return 0;
  }
  return Math.max(240, Math.floor(baselineWidth * 0.9));
}

function shrunkWidthThreshold(baselineWidth) {
  if (!Number.isFinite(baselineWidth) || baselineWidth <= 0) {
    return Number.POSITIVE_INFINITY;
  }
  return Math.ceil(baselineWidth * 0.75);
}

function initialPaneContentOptions(token, description, phase = 'baseline') {
  if (phase === 'shrunk') {
    return {
      contains: token,
      allowWrappedContains: true,
      minNonEmptyLines: 3,
      minDenseLines: 1,
      minCharCount: 90,
      minMaxLineLength: 16,
      timeoutMs: 30_000,
      description,
    };
  }
  return {
    contains: token,
    allowWrappedContains: true,
    minNonEmptyLines: 4,
    minDenseLines: 1,
    minCharCount: 120,
    minMaxLineLength: 18,
    timeoutMs: 45_000,
    description,
  };
}

function utilityContentOptions(token, description, phase = 'baseline') {
  if (phase === 'shrunk') {
    return {
      contains: token,
      allowWrappedContains: true,
      minNonEmptyLines: 3,
      minDenseLines: 1,
      minCharCount: 80,
      minMaxLineLength: 16,
      timeoutMs: 20_000,
      description,
    };
  }
  return {
    contains: token,
    allowWrappedContains: true,
    minNonEmptyLines: 4,
    minDenseLines: 1,
    minCharCount: 110,
    minMaxLineLength: 18,
    timeoutMs: 20_000,
    description,
  };
}

function widthUsageOptions(description, phase = 'baseline') {
  if (phase === 'shrunk') {
    return {
      minMaxOccupiedWidthRatio: 0.55,
      minWideLineCount: 2,
      minMedianOccupiedWidthRatio: 0.4,
      timeoutMs: 20_000,
      description,
    };
  }
  return {
    minMaxOccupiedWidthRatio: 0.62,
    minWideLineCount: 3,
    minMedianOccupiedWidthRatio: 0.46,
    timeoutMs: 20_000,
    description,
  };
}

function singlePaneContentOptions(agent, token, description) {
  if (agent === 'claude') {
    return {
      contains: token,
      minNonEmptyLines: 4,
      minDenseLines: 1,
      minCharCount: 120,
      minMaxLineLength: 18,
      timeoutMs: 45_000,
      description,
    };
  }
  return {
    contains: 'OpenAI Codex',
    allowWrappedContains: true,
    minNonEmptyLines: 2,
    minDenseLines: 0,
    minCharCount: 20,
    minMaxLineLength: 12,
    timeoutMs: 45_000,
    description,
  };
}

function closeRecoveryThresholdsForAgent(agent) {
  if (agent === 'claude') {
    return {
      minNonEmptyLineRatio: 0.75,
      minCharCountRatio: 0.6,
      minAnchorMatches: 3,
      maxBusyColumnRatioRegression: 0.1,
      maxBusyRowRatioRegression: 0.08,
      maxBBoxWidthRatioRegression: 0.1,
      maxBBoxHeightRatioRegression: 0.08,
    };
  }
  return {
    minNonEmptyLineRatio: 0.7,
    minCharCountRatio: 0.55,
    minAnchorMatches: 2,
    maxBusyColumnRatioRegression: null,
    maxBusyRowRatioRegression: null,
    maxBBoxWidthRatioRegression: null,
    maxBBoxHeightRatioRegression: null,
  };
}

function closeNativeCoverageThresholdsForAgent(agent) {
  if (agent === 'claude') {
    return {
      minBusyColumnRatio: 0.35,
      minBusyRowRatio: 0.12,
      minBBoxWidthRatio: 0.35,
      minBBoxHeightRatio: 0.12,
    };
  }
  return {
    minBusyColumnRatio: 0.35,
    minBusyRowRatio: 0.08,
    minBBoxWidthRatio: 0.35,
    minBBoxHeightRatio: 0.12,
  };
}

const splitNativeCoverageThresholds = {
  minBusyColumnRatio: 0.3,
  minBusyRowRatio: 0.08,
  minBBoxWidthRatio: 0.3,
  minBBoxHeightRatio: 0.1,
};

function codexHeaderOptions(description) {
  return {
    contains: 'OpenAI Codex',
    allowWrappedContains: true,
    minNonEmptyLines: 2,
    minDenseLines: 0,
    minCharCount: 20,
    minMaxLineLength: 12,
    timeoutMs: 20_000,
    description,
  };
}

function assertCompleteCodexHeaderFrame(state, description) {
  const lines = state?.pane?.visibleContent?.lines || [];
  const text = lines.join('\n');
  // Codex may print extra bordered boxes, so border counts over the whole pane
  // are not meaningful; anchor on the box containing the header line.
  const headerCount = (text.match(/OpenAI Codex/g) || []).length;
  const headerIndex = lines.findIndex((line) => line.includes('OpenAI Codex'));
  let topBorder = null;
  for (let i = headerIndex; i >= 0; i -= 1) {
    if (lines[i].includes('╭')) { topBorder = lines[i]; break; }
  }
  let bottomBorder = null;
  for (let i = headerIndex; i >= 0 && i < lines.length; i += 1) {
    if (lines[i].includes('╰')) { bottomBorder = lines[i]; break; }
  }
  const completeTopBorder = topBorder != null && topBorder.trimEnd().endsWith('╮');
  const completeBottomBorder = bottomBorder != null && bottomBorder.trimEnd().endsWith('╯');
  if (headerCount !== 1 || headerIndex < 0 || !completeTopBorder || !completeBottomBorder) {
    throw new Error(`${description}: expected one complete Codex header frame, found ${headerCount} headers, topBorderComplete=${completeTopBorder}, bottomBorderComplete=${completeBottomBorder}\n${text}`);
  }
}

async function waitForCompleteCodexHeaderFrame(client, sessionId, paneId, description, timeoutMs = 5_000) {
  const startedAt = Date.now();
  let lastState = null;
  let lastError = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_pane_state', { sessionId, paneId });
    try {
      assertCompleteCodexHeaderFrame(lastState, description);
      return lastState;
    } catch (error) {
      lastError = error;
    }
    await sleep(100);
  }
  throw lastError || new Error(`Timed out waiting for ${description}`);
}

function utilitySeedCommand(token, lineCount = 6) {
  return `node -e "for (let i = 1; i <= ${lineCount}; i += 1) console.log('${token} line ' + String(i).padStart(2, '0') + ' render width coverage payload ' + 'X'.repeat(40))"`;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr401-local-window-resize.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-401',
    tier: 'tier2-local-mock-agent',
    prefix: 'scenario-tr401-local-window-resize',
    metadata: {
      agents: ['codex', 'claude'],
      focus: 'one window: Codex header framing, split-close redraw and split-session window resize render health for both agent vocabularies',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const claudeToken = `TR401CLAUDE${Date.now()}`;
  const legs = { codex: null, claude: null };
  let baselineWindow = null;
  let shrunkWindow = null;
  let restoredWindow = null;
  let codexNarrowWindow = null;

  const openSessionIds = () => Object.values(legs)
    .map((leg) => leg?.sessionId)
    .filter(Boolean);

  const legSummary = () => Object.fromEntries(Object.entries(legs).map(([agent, leg]) => [agent, leg && {
    sessionId: leg.sessionId,
    utilityPaneId: leg.utilityPaneId,
    token: leg.token,
    shellToken: leg.shellToken,
    paneBounds: {
      singleBaseline: leg.singleBaselineState?.pane?.bounds ?? null,
      split: leg.splitMainState?.pane?.bounds ?? null,
      afterClose: leg.closedMainState?.pane?.bounds ?? null,
      baselineMain: leg.baselineMainState?.pane?.bounds ?? null,
      baselineUtility: leg.baselineUtilityState?.pane?.bounds ?? null,
      shrunkMain: leg.shrunkMainState?.pane?.bounds ?? null,
      shrunkUtility: leg.shrunkUtilityState?.pane?.bounds ?? null,
      restoredMain: leg.restoredMainState?.pane?.bounds ?? null,
      restoredUtility: leg.restoredUtilityState?.pane?.bounds ?? null,
    },
  }]));

  async function startAgentLeg(agent) {
    const leg = {
      agent,
      sessionId: null,
      initialPaneId: null,
      utilityPaneId: null,
      token: agent === 'claude' ? claudeToken : 'OpenAI Codex',
      shellToken: `__TR401_${agent.toUpperCase()}_SHELL_${Date.now()}__`,
      headerBaselineWidth: 0,
      singleBaselineState: null,
      singleBaselineNativeMetrics: null,
      splitMainState: null,
      closedMainState: null,
      baselineMainState: null,
      baselineUtilityState: null,
      baselineMainNativeMetrics: null,
      baselineUtilityNativeMetrics: null,
      shrunkMainState: null,
      shrunkUtilityState: null,
      restoredMainState: null,
      restoredUtilityState: null,
    };
    legs[agent] = leg;

    leg.sessionId = await runner.step(`create_local_${agent}_session`, async () => {
      if (agent === 'claude') writeStructuredBlockFixture(runner.sessionDir, claudeToken, STRUCTURED_BLOCK_LINES);
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr401-local-${agent}-${runner.runId}`,
        agent,
        waitForInitialPaneVisible: false,
      });
    });

    await runner.step(`the_${agent}_initial_pane_reaches_its_prompt`, async () => {
      await client.request('select_session', { sessionId: leg.sessionId });
      const readiness = agent === 'claude'
        ? await ensureClaudeInitialPanePromptReady(client, leg.sessionId, 45_000)
        : await ensureCodexInitialPanePromptReady(client, leg.sessionId, 45_000);
      leg.initialPaneId = readiness.paneId
        || (await waitForFirstWorkspacePane(client, leg.sessionId, `${agent} initial pane`, 20_000)).paneId;
      await waitForPaneVisible(client, leg.sessionId, leg.initialPaneId, 45_000);

      if (agent === 'claude') {
        const fixture = await promptAgentForStructuredBlock(client, leg.sessionId, claudeToken, STRUCTURED_BLOCK_LINES);
        leg.initialPaneId = fixture.paneId;
        runner.writeJson('claude-agent-fixture.json', fixture);
        await waitForPaneText(
          client,
          leg.sessionId,
          leg.initialPaneId,
          (text) => text.includes(claudeToken),
          'claude initial pane text to include the structured agent block',
          45_000,
        );
      }
    });

    return leg;
  }

  async function runCodexHeaderFrameLeg(leg) {
    await runner.step('the_fresh_codex_pane_frames_its_header_once', async () => {
      await client.request('select_session', { sessionId: leg.sessionId });
      await assertPaneVisibleContent(
        client,
        leg.sessionId,
        leg.initialPaneId,
        codexHeaderOptions('Codex header visible before initial-pane window resize'),
      );
      const framed = await waitForCompleteCodexHeaderFrame(client, leg.sessionId, leg.initialPaneId, 'Codex baseline header frame');
      leg.headerBaselineWidth = framed?.pane?.bounds?.width ?? 0;
      await captureSessionArtifacts(client, runner.runDir, 'codex-01-header-baseline', leg.sessionId);
    });

    codexNarrowWindow = await runner.step('narrowing_the_window_keeps_one_complete_codex_header_frame', async () => {
      const nextWindow = await setFrontWindowBounds(narrowWindowBounds(baselineWindow), { client });
      await waitForPaneState(
        client,
        leg.sessionId,
        leg.initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const baselineWidth = leg.headerBaselineWidth ?? 0;
          return width > 0 && baselineWidth > 0 && width <= Math.floor(baselineWidth * 0.65);
        },
        'Codex initial pane width to shrink after window resize',
        20_000,
      );
      await captureSessionArtifacts(client, runner.runDir, 'codex-02-narrow-before-assert', leg.sessionId);
      await assertPaneVisibleContent(
        client,
        leg.sessionId,
        leg.initialPaneId,
        codexHeaderOptions('Codex header visible after narrowing main-only window'),
      );
      await waitForCompleteCodexHeaderFrame(client, leg.sessionId, leg.initialPaneId, 'Codex narrowed header frame');
      await captureSessionArtifacts(client, runner.runDir, 'codex-02-narrow-after-assert', leg.sessionId);
      await assertPaneCoverage(client, leg.sessionId, leg.initialPaneId, {
        minWidthRatio: 0.75,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: 'Codex initial pane coverage after narrowing single-pane window',
      });
      return nextWindow;
    });

    await runner.step('restoring_the_window_keeps_one_complete_codex_header_frame', async () => {
      await setFrontWindowBounds(baselineWindow, { client });
      await waitForPaneState(
        client,
        leg.sessionId,
        leg.initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const baselineWidth = leg.headerBaselineWidth ?? 0;
          return baselineWidth > 0 && width >= Math.floor(baselineWidth * 0.95);
        },
        'Codex initial pane width to restore after window resize',
        20_000,
      );
      await captureSessionArtifacts(client, runner.runDir, 'codex-03-restored-before-assert', leg.sessionId);
      await assertPaneVisibleContent(
        client,
        leg.sessionId,
        leg.initialPaneId,
        codexHeaderOptions('Codex header visible after restoring main-only window'),
      );
      await waitForCompleteCodexHeaderFrame(client, leg.sessionId, leg.initialPaneId, 'Codex restored header frame');
      await captureSessionArtifacts(client, runner.runDir, 'codex-03-restored-after-assert', leg.sessionId);
    });
  }

  async function runCloseRedrawLeg(leg) {
    const { agent } = leg;
    const nativeCoverage = closeNativeCoverageThresholdsForAgent(agent);

    await runner.step(`capture_${agent}_single_pane_baseline`, async () => {
      await client.request('select_session', { sessionId: leg.sessionId });
      leg.singleBaselineState = await assertPaneVisibleContent(
        client,
        leg.sessionId,
        leg.initialPaneId,
        singlePaneContentOptions(agent, leg.token, `${agent} initial pane visible content before split close`),
      );
      await assertPaneCoverage(client, leg.sessionId, leg.initialPaneId, {
        minWidthRatio: 0.8,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: `${agent} initial pane coverage before split close`,
      });
      leg.singleBaselineNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        `${agent}-04-single-pane-baseline`,
        leg.sessionId,
        leg.initialPaneId,
        {
          target: 'paneBody',
          ...nativeCoverage,
          description: `${agent} initial pane native paint coverage before split close`,
        },
      );
      await captureSessionArtifacts(client, runner.runDir, `${agent}-04-single-pane-baseline`, leg.sessionId);
    });

    const closingPaneId = await runner.step(`splitting_shrinks_the_${agent}_main_pane`, async () => {
      const workspaceBefore = await client.request('get_workspace', { sessionId: leg.sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId: leg.sessionId,
        targetPaneId: leg.initialPaneId,
        direction: 'vertical',
      });
      const newPane = await waitForNewShellPane(
        client,
        leg.sessionId,
        existingPaneIds,
        `new utility pane after local ${agent} split`,
        30_000,
      );
      leg.splitMainState = await waitForPaneState(
        client,
        leg.sessionId,
        leg.initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const baselineWidth = leg.singleBaselineState?.pane?.bounds?.width ?? 0;
          return width > 0 && width <= shrunkWidthThreshold(baselineWidth);
        },
        `${agent} initial pane width to shrink after split`,
        20_000,
      );
      // Neither welcome banner reflows into a narrow split (claude's header
      // rewraps, codex's box drawing cannot): post-close recovery is the check.
      await captureSessionArtifacts(client, runner.runDir, `${agent}-05-after-split`, leg.sessionId);
      return newPane.paneId;
    });

    await runner.step(`closing_the_split_redraws_the_${agent}_main_pane`, async () => {
      const thresholds = closeRecoveryThresholdsForAgent(agent);
      await client.request('focus_pane', { sessionId: leg.sessionId, paneId: closingPaneId });
      await waitForPaneVisible(client, leg.sessionId, closingPaneId, 20_000);
      await client.request('close_pane', { sessionId: leg.sessionId, paneId: closingPaneId });
      await waitForSessionWorkspace(
        client,
        leg.sessionId,
        (workspace) => {
          const panes = workspace.panes || [];
          return panes.length === 1 && panes[0].paneId === leg.initialPaneId;
        },
        'workspace to collapse back to one pane after closing split',
        20_000,
      );
      leg.closedMainState = await waitForPaneState(
        client,
        leg.sessionId,
        leg.initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          return width >= recoveredWidthThreshold(leg.singleBaselineState?.pane?.bounds?.width ?? 0);
        },
        `${agent} initial pane width to recover after closing split`,
        20_000,
      );
      await assertPaneVisibleContentPreserved(
        client,
        leg.sessionId,
        leg.initialPaneId,
        leg.singleBaselineState?.pane?.visibleContent || null,
        {
          minNonEmptyLineRatio: thresholds.minNonEmptyLineRatio,
          minCharCountRatio: thresholds.minCharCountRatio,
          minAnchorMatches: thresholds.minAnchorMatches,
          // Anchor only on token lines (claude echo/reflow flake).
          ignoreAnchorPatterns: tokenAnchorIgnorePatterns(leg.token),
          timeoutMs: 20_000,
          description: `${agent} initial pane content recovered after closing split`,
        },
      );
      await assertPaneCoverage(client, leg.sessionId, leg.initialPaneId, {
        minWidthRatio: 0.85,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: `${agent} initial pane coverage after closing split`,
      });
      await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        `${agent}-06-after-close-initial-pane`,
        leg.sessionId,
        leg.initialPaneId,
        {
          target: 'paneBody',
          ...nativeCoverage,
          description: `${agent} initial pane native paint coverage after closing split`,
        },
      );
      if (leg.singleBaselineNativeMetrics) {
        await assertPaneNativePaintRecovered(
          client,
          runner.runDir,
          `${agent}-06-after-close-initial-pane-stability`,
          leg.sessionId,
          leg.initialPaneId,
          leg.singleBaselineNativeMetrics,
          {
            target: 'paneBody',
            maxBusyColumnRatioRegression: thresholds.maxBusyColumnRatioRegression,
            maxBusyRowRatioRegression: thresholds.maxBusyRowRatioRegression,
            maxBBoxWidthRatioRegression: thresholds.maxBBoxWidthRatioRegression,
            maxBBoxHeightRatioRegression: thresholds.maxBBoxHeightRatioRegression,
            maxActivePixelRatioRegression: null,
            description: `${agent} initial pane native paint recovery after closing split`,
          },
        );
      }
      await captureSessionArtifacts(client, runner.runDir, `${agent}-06-after-close`, leg.sessionId);
    });
  }

  async function runWindowResizeLeg(leg) {
    const { agent } = leg;

    await runner.step(`prepare_${agent}_split_resize_baseline`, async () => {
      await client.request('select_session', { sessionId: leg.sessionId });
      const workspaceBefore = await client.request('get_workspace', { sessionId: leg.sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId: leg.sessionId,
        targetPaneId: leg.initialPaneId,
        direction: 'vertical',
      });
      const utilityPane = await waitForNewShellPane(
        client,
        leg.sessionId,
        existingPaneIds,
        `new utility pane for ${agent} resize`,
        30_000,
      );
      leg.utilityPaneId = utilityPane.paneId;

      await client.request('focus_pane', { sessionId: leg.sessionId, paneId: leg.utilityPaneId });
      await waitForPaneVisible(client, leg.sessionId, leg.utilityPaneId, 20_000);
      const attachWait = await waitForPaneAttached(client, leg.sessionId, leg.utilityPaneId, 20_000);
      runner.log('pane:runtime_attached', { paneId: leg.utilityPaneId, elapsedMs: attachWait.elapsedMs });
      await client.request('write_pane', {
        sessionId: leg.sessionId,
        paneId: leg.utilityPaneId,
        text: utilitySeedCommand(leg.shellToken),
      });
      await waitForPaneText(
        client,
        leg.sessionId,
        leg.utilityPaneId,
        (text) => text.includes(`${leg.shellToken} line 06`),
        'utility pane text to include generated shell resize token',
        20_000,
      );

      await client.request('select_session', { sessionId: leg.sessionId });
      leg.baselineMainState = await assertPaneVisibleContent(
        client,
        leg.sessionId,
        leg.initialPaneId,
        initialPaneContentOptions(leg.token, `${agent} initial pane visible content before window resize`),
      );
      leg.baselineUtilityState = await assertPaneVisibleContent(
        client,
        leg.sessionId,
        leg.utilityPaneId,
        utilityContentOptions(leg.shellToken, `${agent} utility pane visible content before window resize`),
      );

      await Promise.all([
        assertPaneCoverage(client, leg.sessionId, leg.initialPaneId, {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: `${agent} initial pane coverage before window resize`,
        }),
        assertPaneCoverage(client, leg.sessionId, leg.utilityPaneId, {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: `${agent} utility pane coverage before window resize`,
        }),
        assertPaneUsesVisibleWidth(
          client,
          leg.sessionId,
          leg.initialPaneId,
          widthUsageOptions(`${agent} initial pane width usage before window resize`),
        ),
        assertPaneUsesVisibleWidth(
          client,
          leg.sessionId,
          leg.utilityPaneId,
          widthUsageOptions(`${agent} utility pane width usage before window resize`),
        ),
      ]);

      leg.baselineMainNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        `${agent}-07-baseline-initial-pane`,
        leg.sessionId,
        leg.initialPaneId,
        {
          target: 'paneBody',
          ...splitNativeCoverageThresholds,
          description: `${agent} initial pane native paint coverage before window resize`,
        },
      );
      leg.baselineUtilityNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        `${agent}-07-baseline-utility`,
        leg.sessionId,
        leg.utilityPaneId,
        {
          target: 'paneBody',
          ...splitNativeCoverageThresholds,
          description: `${agent} utility pane native paint coverage before window resize`,
        },
      );

      await captureSessionArtifacts(client, runner.runDir, `${agent}-07-baseline`, leg.sessionId);
    });

    shrunkWindow = await runner.step(`shrinking_the_window_keeps_both_${agent}_panes_painted`, async () => {
      const targetBounds = shrunkWindowBounds(baselineWindow);
      const nextWindow = await setFrontWindowBounds(targetBounds, { client });

      const mainThreshold = paneShrinkThreshold(leg.baselineMainState?.pane?.bounds || {});
      const utilityThreshold = paneShrinkThreshold(leg.baselineUtilityState?.pane?.bounds || {});

      leg.shrunkMainState = await waitForPaneState(
        client,
        leg.sessionId,
        leg.initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width > 0 && height > 0 && width <= mainThreshold.width && height <= mainThreshold.height;
        },
        'initial pane bounds to shrink after window resize',
        20_000,
      );
      leg.shrunkUtilityState = await waitForPaneState(
        client,
        leg.sessionId,
        leg.utilityPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width > 0 && height > 0 && width <= utilityThreshold.width && height <= utilityThreshold.height;
        },
        'utility pane bounds to shrink after window resize',
        20_000,
      );

      await Promise.all([
        waitForPaneVisible(client, leg.sessionId, leg.initialPaneId, 20_000),
        waitForPaneVisible(client, leg.sessionId, leg.utilityPaneId, 20_000),
      ]);

      await Promise.all([
        assertPaneVisibleContent(
          client,
          leg.sessionId,
          leg.initialPaneId,
          initialPaneContentOptions(leg.token, `${agent} initial pane visible content after shrinking window`, 'shrunk'),
        ),
        assertPaneVisibleContent(
          client,
          leg.sessionId,
          leg.utilityPaneId,
          utilityContentOptions(leg.shellToken, `${agent} utility pane visible content after shrinking window`, 'shrunk'),
        ),
        assertPaneCoverage(client, leg.sessionId, leg.initialPaneId, {
          minWidthRatio: 0.75,
          minHeightRatio: 0.7,
          timeoutMs: 20_000,
          description: `${agent} initial pane coverage after shrinking window`,
        }),
        assertPaneCoverage(client, leg.sessionId, leg.utilityPaneId, {
          minWidthRatio: 0.75,
          minHeightRatio: 0.7,
          timeoutMs: 20_000,
          description: `${agent} utility pane coverage after shrinking window`,
        }),
        assertPaneUsesVisibleWidth(
          client,
          leg.sessionId,
          leg.initialPaneId,
          widthUsageOptions(`${agent} initial pane width usage after shrinking window`, 'shrunk'),
        ),
        assertPaneUsesVisibleWidth(
          client,
          leg.sessionId,
          leg.utilityPaneId,
          widthUsageOptions(`${agent} utility pane width usage after shrinking window`, 'shrunk'),
        ),
      ]);

      await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        `${agent}-08-shrunk-initial-pane`,
        leg.sessionId,
        leg.initialPaneId,
        {
          target: 'paneBody',
          ...splitNativeCoverageThresholds,
          description: `${agent} initial pane native paint coverage after shrinking window`,
        },
      );
      await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        `${agent}-08-shrunk-utility`,
        leg.sessionId,
        leg.utilityPaneId,
        {
          target: 'paneBody',
          ...splitNativeCoverageThresholds,
          description: `${agent} utility pane native paint coverage after shrinking window`,
        },
      );

      // The app resizes fit-driven geometry WITHOUT reflow, so shrinking the
      // window truncates scrollback line tails permanently.
      leg.shrunkUtilityState = await client.request('get_pane_state', { sessionId: leg.sessionId, paneId: leg.utilityPaneId });

      await captureSessionArtifacts(client, runner.runDir, `${agent}-08-shrunk`, leg.sessionId);
      return nextWindow;
    });

    restoredWindow = await runner.step(`restoring_the_window_recovers_both_${agent}_panes`, async () => {
      const nextWindow = await setFrontWindowBounds(baselineWindow, { client });

      const mainThreshold = paneRecoveryThreshold(leg.baselineMainState?.pane?.bounds || {});
      const utilityThreshold = paneRecoveryThreshold(leg.baselineUtilityState?.pane?.bounds || {});

      leg.restoredMainState = await waitForPaneState(
        client,
        leg.sessionId,
        leg.initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width >= mainThreshold.width && height >= mainThreshold.height;
        },
        'initial pane bounds to recover after restoring window',
        20_000,
      );
      leg.restoredUtilityState = await waitForPaneState(
        client,
        leg.sessionId,
        leg.utilityPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width >= utilityThreshold.width && height >= utilityThreshold.height;
        },
        'utility pane bounds to recover after restoring window',
        20_000,
      );

      await Promise.all([
        assertPaneVisibleContent(
          client,
          leg.sessionId,
          leg.initialPaneId,
          initialPaneContentOptions(leg.token, `${agent} initial pane visible content after restoring window`),
        ),
        assertPaneVisibleContent(
          client,
          leg.sessionId,
          leg.utilityPaneId,
          utilityContentOptions(leg.shellToken, `${agent} utility pane visible content after restoring window`),
        ),
        assertPaneVisibleContentPreserved(
          client,
          leg.sessionId,
          leg.initialPaneId,
          leg.baselineMainState?.pane?.visibleContent || null,
          {
            minNonEmptyLineRatio: 0.6,
            minCharCountRatio: 0.5,
            minAnchorMatches: 3,
            // Anchor only on token lines (claude echo/reflow flake).
            ignoreAnchorPatterns: tokenAnchorIgnorePatterns(leg.token),
            timeoutMs: 20_000,
            description: `${agent} initial pane content preserved after restoring window`,
          },
        ),
        assertPaneVisibleContentPreserved(
          client,
          leg.sessionId,
          leg.utilityPaneId,
          leg.shrunkUtilityState?.pane?.visibleContent || null,
          {
            minNonEmptyLineRatio: 0.55,
            minCharCountRatio: 0.45,
            minAnchorMatches: 3,
            timeoutMs: 20_000,
            description: `${agent} utility pane shrunk-width content preserved after restoring window`,
          },
        ),
        assertPaneCoverage(client, leg.sessionId, leg.initialPaneId, {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: `${agent} initial pane coverage after restoring window`,
        }),
        assertPaneCoverage(client, leg.sessionId, leg.utilityPaneId, {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: `${agent} utility pane coverage after restoring window`,
        }),
        assertPaneUsesVisibleWidth(
          client,
          leg.sessionId,
          leg.initialPaneId,
          widthUsageOptions(`${agent} initial pane width usage after restoring window`),
        ),
        assertPaneUsesVisibleWidth(
          client,
          leg.sessionId,
          leg.utilityPaneId,
          widthUsageOptions(`${agent} utility pane width usage after restoring window`),
        ),
      ]);

      await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        `${agent}-09-restored-initial-pane`,
        leg.sessionId,
        leg.initialPaneId,
        {
          target: 'paneBody',
          ...splitNativeCoverageThresholds,
          description: `${agent} initial pane native paint coverage after restoring window`,
        },
      );
      await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        `${agent}-09-restored-utility`,
        leg.sessionId,
        leg.utilityPaneId,
        {
          target: 'paneBody',
          ...splitNativeCoverageThresholds,
          description: `${agent} utility pane native paint coverage after restoring window`,
        },
      );

      // Thresholds are loose because the metric counts occupied cells: an agent
      // UI often does not re-expand to the restored width. Blank panes still fail.
      if (leg.baselineMainNativeMetrics) {
        await assertPaneNativePaintRecovered(
          client,
          runner.runDir,
          `${agent}-09-restored-initial-pane-stability`,
          leg.sessionId,
          leg.initialPaneId,
          leg.baselineMainNativeMetrics,
          {
            target: 'paneBody',
            maxBusyColumnRatioRegression: 0.4,
            maxBusyRowRatioRegression: 0.2,
            maxBBoxWidthRatioRegression: 0.4,
            maxBBoxHeightRatioRegression: 0.3,
            maxActivePixelRatioRegression: null,
            description: `${agent} initial pane native paint recovery after restoring window`,
          },
        );
      }
      if (leg.baselineUtilityNativeMetrics) {
        await assertPaneNativePaintRecovered(
          client,
          runner.runDir,
          `${agent}-09-restored-utility-stability`,
          leg.sessionId,
          leg.utilityPaneId,
          leg.baselineUtilityNativeMetrics,
          {
            target: 'paneBody',
            maxBusyColumnRatioRegression: 0.4,
            maxBusyRowRatioRegression: 0.2,
            maxBBoxWidthRatioRegression: 0.4,
            maxBBoxHeightRatioRegression: 0.3,
            maxActivePixelRatioRegression: null,
            description: `${agent} utility pane native paint recovery after restoring window`,
          },
        );
      }

      await captureSessionArtifacts(client, runner.runDir, `${agent}-09-restored`, leg.sessionId);
      return nextWindow;
    });
  }

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    baselineWindow = await runner.step('normalize_window_bounds', async () => {
      const currentBounds = await getFrontWindowBounds(client.bundleId, { client });
      const targetBounds = normalizeBaselineWindowBounds(currentBounds);
      return setFrontWindowBounds(targetBounds, { client });
    });

    // Codex runs first: its header-frame leg narrows the window below the
    // 1248px split floor, which is only safe while no split exists anywhere.
    const codexLeg = await startAgentLeg('codex');
    await runCodexHeaderFrameLeg(codexLeg);
    await runCloseRedrawLeg(codexLeg);
    await runWindowResizeLeg(codexLeg);

    const claudeLeg = await startAgentLeg('claude');
    await runCloseRedrawLeg(claudeLeg);
    await runWindowResizeLeg(claudeLeg);

    const summary = await runner.finishSuccess({
      legs: legSummary(),
      windowBounds: {
        baselineWindow,
        codexNarrowWindow,
        shrunkWindow,
        restoredWindow,
      },
      artifacts: {
        runDir: runner.runDir,
        trace: runner.tracePath,
      },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    for (const sessionId of openSessionIds()) {
      await captureSessionArtifacts(client, runner.runDir, `failure-${sessionId}`, sessionId).catch(() => {});
    }
    const summary = await runner.finishFailure(error, {
      legs: legSummary(),
      windowBounds: {
        baselineWindow,
        codexNarrowWindow,
        shrunkWindow,
        restoredWindow,
      },
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of openSessionIds()) {
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
