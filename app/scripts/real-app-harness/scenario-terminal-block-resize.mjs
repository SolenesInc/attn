#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  relaunchAppAndConnect,
  parseCommonArgs,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  waitForPaneAttached,
  waitForPaneReflowed,
  waitForPaneShellReady,
  waitForPaneState,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const SHELL = 'fish';

function shellAvailable(shell) {
  try {
    execFileSync('/bin/sh', ['-c', `command -v ${shell}`], { encoding: 'utf8' });
    return true;
  } catch {
    return false;
  }
}

// Shipped as a script file: a long one-liner mangles when typed into a live
// shell, and `sh make-sim.sh <token>` is ONE command, so under fish one block.
const MAKE_SIM_SCRIPT = `i=1
while [ $i -le 24 ]; do
  printf "\\033[32m[%3d%%]\\033[0m Building CXX object deep/nested/module_path/src/component_$i/impl/translation_unit_$i.cpp.o -O2 -Wall -Wextra -fno-omit-frame-pointer\\n" $((i*4))
  i=$((i+1))
done
printf "linking objects...\\rlinked OK         \\n"
echo "$1"
`;

function assertBlockInvariants(state, label) {
  if (!state.available) throw new Error(`${label}: block state unavailable`);
  const total = state.scrollback + state.rows;
  for (const block of state.blocks) {
    if (block.endRow !== undefined && block.endRow > total) {
      throw new Error(`${label}: block ${block.id} (${block.command}) endRow=${block.endRow} > total=${total} — stale geometry served`);
    }
  }
  console.log(`[verify] ${label}: ${state.blocks.length} blocks OK (total=${total}, cols=${state.cols}, selected=${state.selectedBlockId})`);
  return state;
}

// Long sentinels wrap and re-wrap on a width change, so they are asserted as
// contiguous tokens once row breaks are joined.
function assertTextIntegrity(text, sentinels, longTokens, label) {
  const lines = text.split('\n').map((line) => line.trim());
  for (const sentinel of sentinels) {
    if (!lines.includes(sentinel)) {
      throw new Error(`${label}: sentinel ${JSON.stringify(sentinel)} missing — replay/reflow corrupted history`);
    }
  }
  const joined = text.replace(/\n/g, '');
  for (const token of longTokens) {
    if (!joined.includes(token)) {
      throw new Error(`${label}: wrapped token ${JSON.stringify(token)} missing — reflow corrupted a long line`);
    }
  }
}

async function runCommandAndWait(client, sessionId, paneId, command, expectedLine) {
  await client.request('write_pane', { sessionId, paneId, text: command });
  await waitForPaneText(client, sessionId, paneId,
    (text) => text.split('\n').some((line) => line.trim() === expectedLine),
    `output of ${expectedLine}`, 30_000);
}

async function waitForPaneColumnsToChange(client, sessionId, paneId, previousCols) {
  await waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => {
      const cols = state?.pane?.visibleContent?.cols;
      return typeof cols === 'number' && cols > 0 && cols !== previousCols;
    },
    `pane ${paneId} columns to change from ${previousCols}`,
    20_000,
  );
  await waitForPaneReflowed(client, sessionId, paneId);
}

// read_pane_text returns the whole buffer; click_pane_cell takes VIEWPORT rows.
async function clickOutputLine(client, sessionId, paneId, state, lineText) {
  const read = await client.request('read_pane_text', { sessionId, paneId });
  const lines = read.text.split('\n');
  const bufferRow = lines.findIndex((line) => line.trim() === lineText);
  if (bufferRow < 0) throw new Error(`line ${JSON.stringify(lineText)} not in pane text`);
  const viewportRow = bufferRow - Math.max(0, lines.length - state.rows);
  if (viewportRow < 0) throw new Error(`line ${JSON.stringify(lineText)} scrolled out of the viewport`);
  await client.request('click_pane_cell', { sessionId, paneId, cell: { row: viewportRow, col: 2 } });
  return client.request('get_pane_block_state', { sessionId, paneId });
}

async function clickAndExpectSelected(client, sessionId, paneId, lineText, commandPrefix, label) {
  const state = await client.request('get_pane_block_state', { sessionId, paneId });
  const selected = await clickOutputLine(client, sessionId, paneId, state, lineText);
  const block = selected.blocks.find((b) => (b.command || '').startsWith(commandPrefix));
  if (!block) throw new Error(`${label}: block for ${JSON.stringify(commandPrefix)} not tracked: ${JSON.stringify(selected.blocks.map((b) => b.command))}`);
  if (selected.selectedBlockId !== block.id) {
    throw new Error(`${label}: clicked ${JSON.stringify(lineText)} but selected ${selected.selectedBlockId} (want ${block.id}) — hit-test wrong`);
  }
  console.log(`[verify] ${label}: click selected the correct block (${block.id})`);
}

// A width change clears the block store and the replay may rebuild it: if a
// block covers the clicked line it must be the RIGHT block, else none.
async function clickAndExpectCorrectOrAbsent(client, sessionId, paneId, lineText, commandPrefix, label) {
  const state = await client.request('get_pane_block_state', { sessionId, paneId });
  const selected = await clickOutputLine(client, sessionId, paneId, state, lineText);
  if (selected.selectedBlockId === null) {
    console.log(`[verify] ${label}: click selected nothing (absent is correct)`);
    return;
  }
  const block = selected.blocks.find((b) => b.id === selected.selectedBlockId);
  if (!block || !(block.command || '').startsWith(commandPrefix)) {
    throw new Error(`${label}: clicked ${JSON.stringify(lineText)} selected block ${selected.selectedBlockId} (${block?.command ?? 'unknown'}) — want ${JSON.stringify(commandPrefix)} or nothing`);
  }
  console.log(`[verify] ${label}: click selected the correct rebuilt block (${block.id})`);
}

async function selectAndWaitForPane(client, sessionId, paneId) {
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, paneId, 20_000);
  await waitForPaneAttached(client, sessionId, paneId, 20_000);
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (!shellAvailable(SHELL)) throw new Error(`${SHELL} required`);

  const runner = createScenarioRunner(options, {
    scenarioId: 'BLOCK-RESIZE',
    tier: 'tier1-local-shell',
    prefix: 'block-resize',
    metadata: {
      shells: [SHELL],
      focus: 'command-block geometry across relaunch replay and pane width changes',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const summary = {};
  let sessionId = null;
  let paneId = null;

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir });

  const token = `RESIZE_${SHELL}_${runner.runId}`;
  const makeDone = `MAKE_DONE_${SHELL}_${runner.runId}`;
  const shellReady = `SHELL_READY_${SHELL}_${runner.runId}`;
  // The terminal resizes without reflow, so a narrower pane truncates each row
  // by design; wrapped tokens hold only at the geometry history was replayed at.
  const sentinels = ['smallblock', '142', 'linked OK'];
  const longTokens = [
    token,
    makeDone,
    'deep/nested/module_path/src/component_7/impl/translation_unit_7.cpp.o',
    'deep/nested/module_path/src/component_24/impl/translation_unit_24.cpp.o',
  ];

  // Runner cleanups run in REVERSE registration order, so the effective order
  // below is: close panes, quitApp, observer.close.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session_panes', async () => {
    if (!sessionId) return;
    const ws = await client.request('get_workspace', { sessionId }).catch(() => null);
    for (const p of ws?.panes || []) {
      await client.request('close_pane', { sessionId, paneId: p.paneId }).catch(() => {});
    }
  });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('phase_a_baseline_blocks', async () => {
      const dir = path.join(runner.sessionDir, SHELL);
      fs.mkdirSync(dir, { recursive: true });
      fs.writeFileSync(path.join(dir, 'make-sim.sh'), MAKE_SIM_SCRIPT);
      sessionId = await createSessionAndWaitForInitialPane({
        client, observer, cwd: dir, label: `blocks-${SHELL}-${runner.runId}`,
        agent: 'shell', waitForInitialPaneVisible: false, sessionWaitMs: 30_000,
      });
      await client.request('select_session', { sessionId });
      const workspace = await client.request('get_workspace', { sessionId });
      const pane = workspace?.panes?.[0];
      runner.assert(Boolean(pane), `No pane for ${SHELL}: ${JSON.stringify(workspace)}`);
      paneId = pane.paneId;
      await waitForPaneVisible(client, sessionId, paneId, 20_000);
      await waitForPaneAttached(client, sessionId, paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, paneId, { timeoutMs: 20_000, description: `${SHELL} shell ready` });
      await client.request('write_pane', { sessionId, paneId, text: `exec ${SHELL} --init-command 'echo ${shellReady}'` });
      await waitForPaneText(client, sessionId, paneId,
        (text) => text.split('\n').some((line) => line.trim() === shellReady),
        `${SHELL} to take over the pane`, 30_000);

      await runCommandAndWait(client, sessionId, paneId, `seq 1 200; echo ${token}`, token);
      await runCommandAndWait(client, sessionId, paneId, `sh make-sim.sh ${makeDone}`, makeDone);
      await runCommandAndWait(client, sessionId, paneId, 'echo smallblock', 'smallblock');

      const baseline = assertBlockInvariants(
        await client.request('get_pane_block_state', { sessionId, paneId }),
        `${SHELL} baseline`,
      );
      runner.assert(
        baseline.blocks.some((b) => (b.command || '').startsWith('seq 1 200')),
        `${SHELL} baseline: tall block not tracked`,
      );
      runner.assert(
        baseline.blocks.some((b) => (b.command || '').startsWith('sh make-sim.sh')),
        `${SHELL} baseline: make-like block not tracked`,
      );
      summary.baselineBlocks = baseline.blocks.length;
    });

    await runner.step('phase_b_relaunch_replay', async () => {
      await relaunchAppAndConnect(client, observer);
      await selectAndWaitForPane(client, sessionId, paneId);
      await waitForPaneText(client, sessionId, paneId, (text) => {
        const lines = text.split('\n').map((line) => line.trim());
        const joined = text.replace(/\n/g, '');
        return sentinels.every((sentinel) => lines.includes(sentinel))
          && longTokens.every((needle) => joined.includes(needle));
      }, `${SHELL} replayed history after relaunch`, 30_000);
      const read = await client.request('read_pane_text', { sessionId, paneId });
      assertTextIntegrity(read.text, sentinels, longTokens, `${SHELL} after-relaunch`);
      const state = assertBlockInvariants(
        await client.request('get_pane_block_state', { sessionId, paneId }),
        `${SHELL} after-relaunch`,
      );
      await clickAndExpectSelected(client, sessionId, paneId, 'smallblock', 'echo smallblock', `${SHELL} after-relaunch small`);
      await clickAndExpectSelected(client, sessionId, paneId, makeDone, 'sh make-sim.sh', `${SHELL} after-relaunch make`);
      summary.afterRelaunchBlocks = state.blocks.length;
      await client.request('capture_native_window_screenshot', { path: path.join(runner.runDir, '1-after-relaunch.png') }).catch(() => {});
    });

    await runner.step('phase_c_width_changes', async () => {
      await selectAndWaitForPane(client, sessionId, paneId);
      const wideCols = (await client.request('get_pane_block_state', { sessionId, paneId })).cols;
      await client.request('split_pane', { sessionId, targetPaneId: paneId, direction: 'vertical' });
      await client.request('select_session', { sessionId });
      await waitForPaneColumnsToChange(client, sessionId, paneId, wideCols);
      await waitForPaneText(client, sessionId, paneId, (text) => {
        const lines = text.split('\n').map((line) => line.trim());
        return sentinels.every((sentinel) => lines.includes(sentinel));
      }, `${SHELL} history restored after split`, 20_000);
      const afterSplit = assertBlockInvariants(
        await client.request('get_pane_block_state', { sessionId, paneId }),
        `${SHELL} after-split`,
      );
      await clickAndExpectCorrectOrAbsent(client, sessionId, paneId, 'smallblock', 'echo smallblock', `${SHELL} after-split`);
      const splitRead = await client.request('read_pane_text', { sessionId, paneId });
      assertTextIntegrity(splitRead.text, sentinels, [], `${SHELL} after-split`);

      await runCommandAndWait(client, sessionId, paneId, 'echo postsplit', 'postsplit');
      await clickAndExpectSelected(client, sessionId, paneId, 'postsplit', 'echo postsplit', `${SHELL} post-split`);

      const ws2 = await client.request('get_workspace', { sessionId });
      const newPane = (ws2?.panes || []).find((p) => p.paneId !== paneId);
      if (newPane) {
        await client.request('close_pane', { sessionId, paneId: newPane.paneId });
        await client.request('select_session', { sessionId });
        await waitForPaneColumnsToChange(client, sessionId, paneId, afterSplit.cols);
      }
      assertBlockInvariants(
        await client.request('get_pane_block_state', { sessionId, paneId }),
        `${SHELL} after-close-split`,
      );
      await runCommandAndWait(client, sessionId, paneId, 'echo postclose', 'postclose');
      await clickAndExpectSelected(client, sessionId, paneId, 'postclose', 'echo postclose', `${SHELL} post-close`);
      await waitForPaneText(client, sessionId, paneId, (text) => {
        const lines = text.split('\n').map((line) => line.trim());
        return [...sentinels, 'postsplit'].every((sentinel) => lines.includes(sentinel));
      }, `${SHELL} history intact after close-split`, 20_000);
      const finalRead = await client.request('read_pane_text', { sessionId, paneId });
      assertTextIntegrity(finalRead.text, [...sentinels, 'postsplit'], [], `${SHELL} final`);
      console.log(`[verify] ${SHELL}: resize round-trip OK`);
      await client.request('capture_native_window_screenshot', { path: path.join(runner.runDir, '2-final.png') }).catch(() => {});
    });

    const result = await runner.finishSuccess(summary);
    console.log('[verify] PASS — fish: replay intact, blocks correct-or-absent across resizes');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    const result = await runner.finishFailure(error, summary);
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      const ws = await client.request('get_workspace', { sessionId }).catch(() => null);
      for (const p of ws?.panes || []) {
        await client.request('close_pane', { sessionId, paneId: p.paneId }).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
