#!/usr/bin/env node
// Live repro for workspace sliver behavior: dead-zone restore on tree
// shrink, LRU victim on sliver click, and drag-to-expand on the divider.

import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createRunContext, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  const { runDir, sessionDir } = createRunContext(options, 'bridge-sliver-verify');
  console.log(`[sliver-verify] runDir=${runDir}`);
  const client = new UiAutomationClient({ appPath: options.appPath });

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
  const bundleId = (await execFileAsync('defaults', ['read', `${options.appPath}/Contents/Info`, 'CFBundleIdentifier'])).stdout.trim();
  const osa = (script) => execFileAsync('osascript', ['-e', `tell application "System Events" to tell (first process whose bundle identifier is "${bundleId}") to ${script}`]);
  const panesSelector = '.terminal-wrapper.active .session-terminal-panes';
  const sizePanesTo = async (targetPx) => {
    await osa('set position of front window to {20, 40}');
    for (let i = 0; i < 5; i++) {
      const panes = await boundsOf(panesSelector);
      const delta = targetPx - panes.width;
      if (Math.abs(delta) < 4) return panes.width;
      const win = await req('get_window_bounds', {});
      await osa(`set size of front window to {${Math.round(win.logicalBounds.width + delta)}, 900}`);
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

  // Size the panes container into the dead zone: fits 3x480 but not 3x504.
  const panesWidth = await sizePanesTo(1470);
  console.log(`[sliver-verify] panes width=${panesWidth} (dead zone target 1470)`);

  console.log(`[sliver-verify] 3 panes, suspended=${await suspendedTitle()}`);

  // Change 1: a 4th pane suspends someone; closing it must restore.
  before = paneIds();
  await req('split_pane', { sessionId, targetPaneId: paneC, direction: 'vertical' });
  const paneD = await newPaneAfter(before);
  await delay(600);
  const during = await suspendedTitle();
  console.log(`[sliver-verify] 4 panes, suspended=${during}`);
  if (during === null) throw new Error('expected a suspended sliver with 4 panes in the dead zone');
  await req('close_pane', { sessionId, paneId: paneD });
  await delay(1000);
  const after = await suspendedTitle();
  console.log(`[sliver-verify] closed 4th, suspended=${after} => ${after === null ? 'RESTORED (fix works)' : 'STILL COLLAPSED (bug)'}`);

  // Change 2: click a sliver; the victim must be the LRU pane, not the focused one.
  before = paneIds();
  await req('split_pane', { sessionId, targetPaneId: paneC, direction: 'vertical' });
  const paneE = await newPaneAfter(before);
  await delay(600);
  await req('focus_pane', { sessionId, paneId: paneA });
  await delay(300);
  const sliverBefore = await suspendedTitle();
  console.log(`[sliver-verify] before click: suspended=${sliverBefore} focused=A`);
  await req('dom_click', { selector: '[data-pane-suspended="true"] .workspace-suspended-leaf' });
  await delay(800);
  const sliverAfterClick = await suspendedTitle();
  ws = await req('get_workspace', { sessionId });
  console.log(`[sliver-verify] after click: suspended=${sliverAfterClick} activePane=${ws.activePaneId}`);

  // Change 3: drag the sliver-adjacent divider away from the sliver.
  ws = await req('get_workspace', { sessionId });
  const suspendedSet = new Set();
  for (const id of paneIds()) {
    try {
      await req('dom_text', { selector: `[data-pane-id="${id}"][data-pane-suspended="true"]` });
      suspendedSet.add(id);
    } catch {}
  }
  if (suspendedSet.size === 0) throw new Error('no suspended pane before drag test');
  console.log(`[sliver-verify] suspended set=${[...suspendedSet].join(', ')}`);
  const leavesOf = (n) => (n.type === 'split'
    ? [...leavesOf(n.children[0]), ...leavesOf(n.children[1])]
    : [n.paneId ?? n.tileId]);
  // The draggable divider sits where a fully suspended subtree meets a visible one.
  const findNode = (node, splitId) => {
    if (!node || node.type !== 'split') return null;
    if (node.splitId === splitId) return node;
    return findNode(node.children[0], splitId) || findNode(node.children[1], splitId);
  };
  const findSplitFor = (node) => {
    if (!node || node.type !== 'split') return null;
    const fully = node.children.map((child) => leavesOf(child).every((id) => suspendedSet.has(id)));
    if (fully[0] !== fully[1]) {
      return { splitId: node.splitId, side: fully[0] ? 'first' : 'second' };
    }
    return findSplitFor(node.children[0]) || findSplitFor(node.children[1]);
  };
  const found = findSplitFor(ws.workspace.layoutTree);
  if (!found) throw new Error('no split beside the suspended pane');
  const releasedLeafIds = leavesOf(found.side === 'first'
    ? findNode(ws.workspace.layoutTree, found.splitId).children[0]
    : findNode(ws.workspace.layoutTree, found.splitId).children[1]);
  const watchPaneId = releasedLeafIds[0];
  const delta = found.side === 'first' ? 420 : -420;
  console.log(`[sliver-verify] dragging ${found.splitId} (sliver on ${found.side}) by ${delta}px`);
  await req('drag_split', { sessionId, splitId: found.splitId, deltaPx: delta, steps: 12 });
  await delay(900);
  let dragExpanded = true;
  try {
    await req('dom_text', { selector: `[data-pane-id="${watchPaneId}"][data-pane-suspended="true"]` });
    dragExpanded = false;
  } catch {}
  const expandedBounds = await boundsOf(`[data-pane-id="${watchPaneId}"]`);
  console.log(`[sliver-verify] after drag: expanded=${dragExpanded} width=${expandedBounds.width} suspendedNow=${await suspendedTitle()}`);
  if (!dragExpanded || expandedBounds.width < 300) throw new Error('drag did not expand the sliver');

  console.log('[sliver-verify] ALL CHECKS DONE');
  process.exit(0);
}

main().catch((error) => {
  console.error('[sliver-verify] FAILED:', error?.message || error);
  process.exit(1);
});
