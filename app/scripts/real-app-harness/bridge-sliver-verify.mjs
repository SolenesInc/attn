#!/usr/bin/env node

// Live sliver checks: auto-restore when room returns, LRU click victim,
// and neighbor resize beside a sliver.

import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createRunContext, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { MacOSDriver } from './macosDriver.mjs';

const execFileAsync = promisify(execFile);
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  const { runDir, sessionDir } = createRunContext(options, 'bridge-sliver-verify');
  console.log(`[sliver-verify] runDir=${runDir}`);
  const client = new UiAutomationClient({ appPath: options.appPath });
  const bundleId = (await execFileAsync('defaults', ['read', `${options.appPath}/Contents/Info`, 'CFBundleIdentifier'])).stdout.trim();
  const inputDriver = new MacOSDriver({ bundleId, appPath: options.appPath });

  const req = (cmd, payload) => client.request(cmd, payload);
  const suspendedTitle = async () => {
    try {
      const r = await req('dom_text', { selector: '[data-pane-suspended="true"] .workspace-suspended-label' });
      return r.text;
    } catch {
      return null;
    }
  };
  const boundsOf = async (selector) => (await req('dom_scroll_into_view', { selector })).bounds;
  // A live window capture adds a tiny overlay window to the process, and it
  // becomes "front window"; address the app window by its title instead.
  const appName = (await execFileAsync('defaults', ['read', `${options.appPath}/Contents/Info`, 'CFBundleName'])).stdout.trim();
  const osa = (script) => execFileAsync('osascript', ['-e', `tell application "System Events" to tell (first process whose bundle identifier is "${bundleId}") to tell (first window whose name is "${appName}") to ${script}`]);
  const panesSelector = '.terminal-wrapper.active .session-terminal-panes';
  const sizePanesTo = async (targetPx) => {
    await osa('set position to {20, 40}');
    for (let i = 0; i < 6; i++) {
      const panes = await boundsOf(panesSelector);
      const delta = targetPx - panes.width;
      if (Math.abs(delta) < 4) return panes.width;
      const win = await req('get_window_bounds', {});
      await osa(`set size to {${Math.round(win.logicalBounds.width + delta)}, 900}`);
      await delay(700);
    }
    return (await boundsOf(panesSelector)).width;
  };

  await client.launchFreshApp();
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
  await client.waitForFrontendResponsive(20_000);

  const { sessionId } = await req('create_session', { cwd: sessionDir, label: 'sliver-verify', agent: 'shell' });
  await delay(1500);
  let ws = await req('get_workspace', { sessionId });
  const paneA = ws.activePaneId || ws.panes[0].paneId;
  const paneIds = () => ws.panes.map((p) => p.paneId);
  const newPaneAfter = async (before) => {
    for (let i = 0; i < 40; i++) {
      ws = await req('get_workspace', { sessionId });
      const fresh = paneIds().filter((id) => !before.includes(id));
      if (fresh.length === 1) return fresh[0];
      await delay(250);
    }
    throw new Error('new pane did not appear');
  };

  let before = paneIds();
  await req('split_pane', { sessionId, targetPaneId: paneA, direction: 'vertical' });
  const paneB = await newPaneAfter(before);
  before = paneIds();
  await req('split_pane', { sessionId, targetPaneId: paneB, direction: 'vertical' });
  const paneC = await newPaneAfter(before);
  console.log(`[sliver-verify] panes A=${paneA} B=${paneB} C=${paneC}`);

  let width = await sizePanesTo(1470);
  await delay(600);
  const atRoomy = await suspendedTitle();
  console.log(`[sliver-verify] width=${width} suspended=${atRoomy} => ${atRoomy === null ? 'AUTO-RESTORED' : 'BUG: stuck sliver'}`);
  if (atRoomy !== null) throw new Error('sliver survived a viewport with room');

  width = await sizePanesTo(1200);
  await delay(600);
  const atTight = await suspendedTitle();
  console.log(`[sliver-verify] width=${width} suspended=${atTight}`);
  if (atTight === null) throw new Error('expected a sliver at 1200px');
  width = await sizePanesTo(1470);
  await delay(600);
  const regrown = await suspendedTitle();
  console.log(`[sliver-verify] width=${width} suspended=${regrown} => ${regrown === null ? 'AUTO-EXPANDED on clearance' : 'BUG'}`);
  if (regrown !== null) throw new Error('sliver did not auto-expand when the window grew');

  before = paneIds();
  await req('split_pane', { sessionId, targetPaneId: paneC, direction: 'vertical' });
  const paneD = await newPaneAfter(before);
  await delay(600);
  const during = await suspendedTitle();
  console.log(`[sliver-verify] 4 panes, suspended=${during}`);
  if (during === null) throw new Error('expected a sliver with 4 panes at 1470');
  await req('close_pane', { sessionId, paneId: paneD });
  await delay(1000);
  const afterClose = await suspendedTitle();
  console.log(`[sliver-verify] closed 4th, suspended=${afterClose} => ${afterClose === null ? 'RESTORED' : 'BUG'}`);
  if (afterClose !== null) throw new Error('sliver did not restore after the tile closed');

  width = await sizePanesTo(1200);
  await req('focus_pane', { sessionId, paneId: paneC });
  await delay(300);
  await req('focus_pane', { sessionId, paneId: paneA });
  await delay(300);
  const clickBefore = await suspendedTitle();
  await req('dom_click', { selector: '[data-pane-suspended="true"] .workspace-suspended-leaf' });
  await delay(800);
  ws = await req('get_workspace', { sessionId });
  const clickAfter = await suspendedTitle();
  console.log(`[sliver-verify] click: before=${clickBefore} after=${clickAfter} activePane=${ws.activePaneId}`);

  await req('focus_pane', { sessionId, paneId: paneC });
  await delay(300);
  await req('focus_pane', { sessionId, paneId: paneA });
  await delay(600);
  const middle = await suspendedTitle();
  console.log(`[sliver-verify] middle sliver=${middle}`);
  const grab = await boundsOf('.workspace-split-divider[data-split-grab]');
  const win = await req('get_window_bounds', {});
  const aBefore = await boundsOf(`[data-pane-id="${paneA}"]`);
  const cBefore = await boundsOf(`[data-pane-id="${paneC}"]`);
  const sBefore = await boundsOf('[data-pane-suspended="true"]');
  const relX = (grab.x + grab.width / 2) / win.logicalBounds.width;
  const relY = (grab.y + grab.height / 2) / win.logicalBounds.height;
  const toRelX = (grab.x + grab.width / 2 + 150) / win.logicalBounds.width;
  await inputDriver.dragWindow(relX, relY, toRelX, relY, { steps: 12 });
  await delay(900);
  const aAfter = await boundsOf(`[data-pane-id="${paneA}"]`);
  const cAfter = await boundsOf(`[data-pane-id="${paneC}"]`);
  const sAfter = await boundsOf('[data-pane-suspended="true"]');
  console.log(`[sliver-verify] drag right 150px: A ${aBefore.width}->${aAfter.width} C ${cBefore.width}->${cAfter.width} sliver ${sBefore.width}->${sAfter.width}`);
  if (!(aAfter.width > aBefore.width + 40 && cAfter.width < cBefore.width - 40)) {
    throw new Error('sliver-side drag did not resize the visible neighbors');
  }
  if (Math.abs(sAfter.width - sBefore.width) > 2) {
    throw new Error('sliver changed size during a neighbor resize');
  }

  const grab2 = await boundsOf('.workspace-split-divider[data-split-grab]');
  const rel2X = (grab2.x + grab2.width / 2) / win.logicalBounds.width;
  const farX = (grab2.x + grab2.width / 2 + 400) / win.logicalBounds.width;
  await inputDriver.dragWindow(rel2X, relY, farX, relY, { steps: 16 });
  await delay(900);
  const suspendedCount = async () => {
    let count = 0;
    for (const id of [paneA, paneB, paneC]) {
      try {
        await req('dom_text', { selector: `[data-pane-id="${id}"][data-pane-suspended="true"]` });
        count += 1;
      } catch {}
    }
    return count;
  };
  const afterFoldDrag = await suspendedCount();
  console.log(`[sliver-verify] drag past min: ${afterFoldDrag} slivers (want 2)`);
  if (afterFoldDrag !== 2) throw new Error('dragging past the minimum did not fold the pane');

  await req('dom_click', { selector: `[data-pane-id="${paneC}"][data-pane-suspended="true"] .workspace-suspended-leaf` });
  await delay(900);
  const afterClickRestore = await suspendedCount();
  console.log(`[sliver-verify] clicked folded pane back: ${afterClickRestore} slivers (want 1)`);
  if (afterClickRestore !== 1) throw new Error('clicking the drag-folded sliver did not restore it');

  console.log('[sliver-verify] ALL CHECKS DONE');
  process.exit(0);
}

main().catch((error) => {
  console.error('[sliver-verify] FAILED:', error?.message || error);
  process.exit(1);
});
