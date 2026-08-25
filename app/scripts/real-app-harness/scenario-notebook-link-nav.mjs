#!/usr/bin/env node


import fs from 'node:fs';
import path from 'node:path';
import {
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const FINDER_SELECTOR = '.terminal-wrapper.active .notebook-finder';
const FINDER_INPUT_SELECTOR = '.terminal-wrapper.active .notebook-finder-input';
const FINDER_OPTION_SELECTOR = '.terminal-wrapper.active .notebook-finder-option';
const DOWN_LINK = '.terminal-wrapper.active .cm-md-link[data-href="#down-below"]';
const TOP_LINK = '.terminal-wrapper.active .cm-md-link[data-href="#anchor-top"]';
const IMAGE_IMG = '.terminal-wrapper.active .cm-md-image img';
const IMAGE_BROKEN = '.terminal-wrapper.active .cm-md-image-broken';

// 1x1 transparent PNG, embedded so the harness has no external fixture.
const PIXEL_PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  return {
    options: parseCommonArgs(args),
    help: args.includes('--help') || args.includes('-h'),
  };
}

// Selector presence via the screenshot bridge: "not found" means absent; any
// other error (html-to-image chokes inside CM subtrees) still proves presence.
async function domSelectorPresent(client, selector) {
  try {
    await client.request('capture_screenshot_data', { selector }, { timeoutMs: 8000 });
    return true;
  } catch (error) {
    return !String(error).includes('Screenshot selector not found in DOM');
  }
}

async function waitForDomSelector(client, selector, present, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    if ((await domSelectorPresent(client, selector)) === present) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${selector} to be ${present ? 'present' : 'absent'}: ${description}`);
}

async function waitForWorkspaceUi(client, workspaceId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_workspace_ui_state', { workspaceId }).catch((error) => ({ error: String(error) }));
    if (predicate(last)) {
      return last;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last workspace UI state:\n${JSON.stringify(last, null, 2)}`);
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

async function closeExistingSessions(client, sessionRootDir) {
  const initial = await client.request('get_state');
  const harnessSessions = (initial.sessions || []).filter((session) => session.cwd?.startsWith(sessionRootDir));
  for (const session of harnessSessions) {
    await closeWorkspacePanes(client, session.id).catch(() => {});
  }
}

// The finder picks on mousedown, not click — see NotebookFinder.tsx.
async function openNoteViaFinderBridge(client, workspaceId, basename, query) {
  await waitForDomSelector(client, FINDER_SELECTOR, true, `finder open for ${basename}`);
  await client.request('dom_type', { selector: FINDER_INPUT_SELECTOR, text: query });
  await new Promise((resolve) => setTimeout(resolve, 500));
  await waitForDomSelector(client, FINDER_OPTION_SELECTOR, true, `finder shows a result for "${query}"`);
  await client.request('dom_click', { selector: FINDER_OPTION_SELECTOR });
  await waitForWorkspaceUi(
    client,
    workspaceId,
    (state) => state?.tileTitles?.includes(`${basename}.md`),
    `finder opens ${basename}.md (tile title)`,
    15_000,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-notebook-link-nav.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'notebook-link-nav');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    // Names are chosen so each finder query fuzzy-matches exactly one note. They
    // live in the workspace directory, which is what a docked tile's finder indexes.
    const noteRoot = path.join(sessionDir, 'linknav-ws');
    fs.mkdirSync(noteRoot, { recursive: true });
    const NAV = `${runId}qnav`;
    const BAR = `${runId}qbar`;
    const ANCHOR = `${runId}qanchor`;
    const navPath = path.join(noteRoot, `${NAV}.md`);
    const barPath = path.join(noteRoot, `${BAR}.md`);
    const anchorPath = path.join(noteRoot, `${ANCHOR}.md`);
    const filler = Array.from({ length: 80 }, (_, i) => `Filler line ${i + 1}.`).join('\n\n');
    fs.writeFileSync(navPath, `# nav probe\n\n[bar](${BAR}.md)\n`, 'utf8');
    fs.writeFileSync(barPath, `# bar\n\nSibling of nav probe.\n\n[anchor](${ANCHOR}.md)\n`, 'utf8');
    const IMG = `${runId}qimg`;
    const imgDirName = `${runId}-imgdir`;
    const assetsDirName = `${runId}-assets`;
    const imgDir = path.join(noteRoot, imgDirName);
    const assetsDir = path.join(noteRoot, assetsDirName);
    fs.mkdirSync(imgDir, { recursive: true });
    fs.mkdirSync(assetsDir, { recursive: true });
    const imgNotePath = path.join(imgDir, `${IMG}.md`);
    const pixelPath = path.join(assetsDir, 'pixel.png');
    const imgLinkHref = `${imgDirName}/${IMG}.md`;
    fs.writeFileSync(imgNotePath, `# image probe\n\n![pixel](../${assetsDirName}/pixel.png)\n`, 'utf8');
    fs.writeFileSync(pixelPath, Buffer.from(PIXEL_PNG_BASE64, 'base64'));
    console.log(`[RealAppHarness] imgNote=${imgNotePath} pixel=${pixelPath}`);

    fs.writeFileSync(anchorPath, [
      '# anchor probe',
      '',
      '[down](#down-below)',
      '',
      filler,
      '',
      '## down below',
      '',
      'You made it. [top](#anchor-top)',
      '',
      `[image probe](${imgLinkHref})`,
      '',
    ].join('\n'), 'utf8');
    console.log(`[RealAppHarness] noteRoot=${noteRoot} nav=${NAV}.md bar=${BAR}.md anchor=${ANCHOR}.md`);

    const cwd = noteRoot;
    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd,
      label: `notebook-link-nav-${runId}`,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    const pane = await waitForFirstWorkspacePane(client, sessionId, 'initial workspace pane');
    await client.request('select_session', { sessionId });
    await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
    await waitForPaneShellReady(client, sessionId, pane.paneId, {
      timeoutMs: 20_000,
      description: 'shell prompt ready',
    });

    const workspace = await client.request('get_workspace', { sessionId });
    const workspaceId = workspace.workspaceId;
    if (!workspaceId) {
      throw new Error(`Could not resolve workspace id for session ${sessionId}: ${JSON.stringify(workspace)}`);
    }

    await client.request('dispatch_shortcut', { shortcutId: 'notebook.openTile' });
    const docked = await waitForWorkspaceUi(
      client,
      workspaceId,
      // A notebook tile with no note open is titled "Editor".
      (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1
        && Array.isArray(state?.tileTitles) && state.tileTitles.includes('Editor'),
      'notebook.openTile docks a fresh notebook tile (titled "Editor")',
      15_000,
    );
    console.log(`[RealAppHarness] docked notebook tile=${docked.tileIds[0]}`);

    await openNoteViaFinderBridge(client, workspaceId, NAV, 'qnav');
    console.log('[RealAppHarness] STEP 1 OK: nav-probe note open in the notebook tile.');

    await client.request('dom_click', {
      selector: `.terminal-wrapper.active .cm-md-link[data-href="${BAR}.md"]`,
      modifiers: { meta: true },
    });
    await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => state?.tileTitles?.includes(`${BAR}.md`),
      'mod-click relative link navigates nav-probe -> bar',
    );
    console.log('[RealAppHarness] STEP 2 OK: mod-click on relative link navigated to the sibling note.');

    await client.request('dom_click', {
      selector: `.terminal-wrapper.active .cm-md-link[data-href="${ANCHOR}.md"]`,
      modifiers: { meta: true },
    });
    await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => state?.tileTitles?.includes(`${ANCHOR}.md`),
      'mod-click relative link navigates bar -> anchor',
    );
    console.log('[RealAppHarness] STEP 3 OK: mod-click on relative link navigated bar -> anchor.');

    // CM only mounts visible lines, so the bottom-of-doc [top] link is not yet
    // mounted and its presence after the jump is what proves the scroll landed.
    await waitForDomSelector(client, DOWN_LINK, true, 'down link rendered');
    if (await domSelectorPresent(client, TOP_LINK)) {
      throw new Error('precondition failed: bottom [top] link already in DOM before the anchor jump');
    }

    await client.request('dom_click', { selector: DOWN_LINK, modifiers: { meta: true } });
    await waitForDomSelector(client, TOP_LINK, true, 'mod-click #down-below scrolls the note into view');
    console.log('[RealAppHarness] STEP 4 OK: mod-click on #down-below scrolled the note (bottom-of-doc link now in the CM DOM).');

    const IMAGE_LINK = `.terminal-wrapper.active .cm-md-link[data-href="${imgLinkHref}"]`;
    await waitForDomSelector(client, IMAGE_LINK, true, 'image-probe link rendered');
    await client.request('dom_click', { selector: IMAGE_LINK, modifiers: { meta: true } });
    await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => state?.tileTitles?.includes(`${IMG}.md`),
      'mod-click relative link navigates anchor -> nested image probe',
    );
    console.log('[RealAppHarness] STEP 5 OK: mod-click on relative link navigated into the nested directory.');

    await waitForDomSelector(client, IMAGE_IMG, true, '`..`-relative image renders (not broken placeholder)');
    if (await domSelectorPresent(client, IMAGE_BROKEN)) {
      throw new Error('`..`-relative image rendered as broken placeholder instead of resolving');
    }
    console.log('[RealAppHarness] STEP 6 OK: `..`-relative image resolved and rendered.');

    const summary = {
      ok: true,
      runId,
      workspaceId,
      tileId: docked.tileIds[0],
      navPath,
      barPath,
      anchorPath,
      imgNotePath,
      pixelPath,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] PASSED: bridge-only mod-click link navigation + heading jump + `..`-relative image resolution verified.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    console.error(`[RealAppHarness] FAILED: ${error?.stack || error}`);
    throw error;
  } finally {
    if (sessionId) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
