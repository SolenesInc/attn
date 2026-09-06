#!/usr/bin/env node

import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver, delay } from './platform.mjs';
import {
  captureSessionArtifacts,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const UNKNOWN_SEED_ID = 's-000000';

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

// Window bounds include the title bar; the page does not.
function windowRelativePoint(pageX, pageY, windowBounds, innerWidth, innerHeight) {
  const { width, height } = windowBounds.logicalBounds;
  const chromeX = Math.max(0, width - innerWidth);
  const chromeY = Math.max(0, height - innerHeight);
  return {
    relativeX: (chromeX / 2 + pageX) / width,
    relativeY: (chromeY + pageY) / height,
  };
}

function startProbeServer() {
  return new Promise((resolve, reject) => {
    const hits = [];
    const server = http.createServer((req, res) => {
      hits.push(req.url);
      res.writeHead(200, { 'content-type': 'text/plain' });
      res.end('ok');
    });
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({ server, hits, port });
    });
  });
}

function closeServer(server) {
  return new Promise((resolve) => {
    server.close(() => resolve());
    // The default browser that followed the probe link keeps its connection alive
    // indefinitely; close() alone would wait for it to drain.
    server.closeAllConnections();
  });
}

function occurrences(haystack, needle) {
  let count = 0;
  let at = haystack.indexOf(needle);
  while (at !== -1) {
    count += 1;
    at = haystack.indexOf(needle, at + needle.length);
  }
  return count;
}

async function paneText(client, pane) {
  const payload = await client.request('read_pane_text', pane);
  return payload.text || '';
}

async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const before = occurrences(await paneText(client, pane), expected);
  await client.request('write_pane', { ...pane, text: command });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = await paneText(client, pane);
    if (occurrences(text, expected) > before) return text;
  }
  throw new Error(`pane never answered ${JSON.stringify(command)} with ${JSON.stringify(expected)}:\n${text}`);
}

async function waitForSelectorShot(client, selector, description, timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs;
  let lastError = null;
  while (Date.now() < deadline) {
    try {
      const shot = await client.request('capture_screenshot_data', { selector });
      if (shot?.bounds?.width > 0 && shot?.bounds?.height > 0) return shot;
      lastError = new Error(`selector has zero-sized bounds: ${JSON.stringify(shot?.bounds)}`);
    } catch (error) {
      lastError = error;
    }
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}: ${lastError}`);
}

async function selectorIsAbsent(client, selector) {
  try {
    await client.request('capture_screenshot_data', { selector });
    return false;
  } catch (error) {
    if (String(error).includes('Screenshot selector not found in DOM')) return true;
    throw error;
  }
}

function collectMarkdownTiles(layout) {
  if (!layout?.layout_json) return [];
  let root;
  try {
    root = JSON.parse(layout.layout_json);
  } catch {
    return [];
  }
  const tiles = [];
  const walk = (node) => {
    if (!node || typeof node !== 'object') return;
    if (node.tile_kind === 'markdown') tiles.push(node);
    for (const child of node.children || []) walk(child);
  };
  walk(root);
  return tiles;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-md-link.mjs');
    return;
  }

  // HID mouse clicks land at absolute screen positions, so the default
  // 20px-visible window park would put every click off-window.
  if (process.env.ATTN_HARNESS_PARK_VISIBLE_PX === undefined) {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '1200';
  }
  // Path links resolve on hover; a non-focusable always-on-top window never
  // receives mouse-moved events on macOS, so the Cmd+click finds no link.
  if (process.env.ATTN_HARNESS_ALWAYS_ON_TOP === undefined) {
    process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';
  }

  // Tauri's fs scope allows $HOME/** and does not match dot-directories, so a
  // path under a hidden dir fails the existence check and never becomes a link.
  if (!process.env.ATTN_REAL_APP_SESSION_ROOT && !process.argv.includes('--session-root-dir')) {
    options.sessionRootDir = path.join(os.homedir(), 'Library', 'Caches', 'attn-harness', 'real-app-sessions');
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-MD-LINK',
    tier: 'tier1-local-shell',
    prefix: 'terminal-md-link',
    metadata: {
      focus: 'markdown-path Cmd+click docks/reuses a tile bound to the pane session, '
        + 'OSC 8 Cmd+click reaches the system browser, real seed-plant ids get marked',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver({ appPath: options.appPath });
  let sessionId = null;
  const { server, hits, port } = await startProbeServer();

  runner.log('run context', {
    runDir: runner.runDir,
    sessionDir: runner.sessionDir,
    wsUrl: options.wsUrl,
    probeServerPort: port,
  });

  fs.writeFileSync(path.join(runner.sessionDir, 'alpha.md'), '# Alpha Doc\n\nHello from **alpha**.\n\n- one\n- two\n', 'utf8');
  fs.writeFileSync(path.join(runner.sessionDir, 'beta.md'), '# Beta Doc\n\nHello from *beta*.\n', 'utf8');

  // Runner cleanups run in REVERSE registration order: observer/app are
  // registered first so they close LAST.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session_panes', async () => {
    if (!sessionId) return;
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    for (const pane of workspace?.panes || []) {
      await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    }
  });
  runner.registerCleanup('close_probe_server', () => closeServer(server));

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    let pane;
    let workspaceId;
    await runner.step('create_session', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `md-link-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      await client.request('select_session', { sessionId });
      const workspace = await client.request('get_workspace', { sessionId });
      pane = workspace?.panes?.[0];
      runner.assert(Boolean(pane), `No pane in workspace: ${JSON.stringify(workspace)}`);
      workspaceId = workspace.workspaceId;
      runner.assert(Boolean(workspaceId), `No workspaceId on workspace: ${JSON.stringify(workspace)}`);
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, {
        timeoutMs: 20_000,
        description: 'shell pane ready',
      });
    });

    await runner.step('term_program_is_pinned_to_ghostty', async () => {
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: 'echo "TP=$TERM_PROGRAM"' });
      await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (text) => text.split('\n').some((line) => line.trim() === 'TP=ghostty'),
        'TERM_PROGRAM=ghostty echoed',
        20_000,
      );
    });

    let seedId = null;
    await runner.step('a_real_planted_seed_id_is_marked_and_an_unknown_one_is_not', async () => {
      const planted = await runInPane(
        client,
        { sessionId, paneId: pane.paneId },
        `attn seed plant "Terminal seed mark receipt" -m "Planted by the terminal-md-link scenario." --session ${sessionId}`,
        's-',
      );
      const ids = [...planted.matchAll(/^\s*(s-[0-9a-hjkmnp-tv-z]{6})\b/gm)].map((match) => match[1]);
      seedId = ids[ids.length - 1];
      runner.assert(Boolean(seedId), 'plant answered with a seed id', { ids });

      await runInPane(
        client,
        { sessionId, paneId: pane.paneId },
        `printf 'unknown ${UNKNOWN_SEED_ID}\\n'`,
        `unknown ${UNKNOWN_SEED_ID}`,
      );

      const mark = await waitForSelectorShot(client, `[data-terminal-seed-id="${seedId}"]`, 'known seed mark');
      runner.assert(Boolean(mark?.pngBase64 && mark?.bounds), 'known seed mark is rendered', { seedId });
      fs.writeFileSync(path.join(runner.runDir, 'seed-mark.png'), Buffer.from(mark.pngBase64, 'base64'));
      runner.assert(
        await selectorIsAbsent(client, `[data-terminal-seed-id="${UNKNOWN_SEED_ID}"]`),
        'unknown seed id remains plain terminal text',
        { unknownSeedId: UNKNOWN_SEED_ID },
      );
    });

    const echoPath = async (relPath) => {
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: `echo ${relPath}` });
      await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (text) => text.split('\n').some((line) => line.trim() === relPath),
        `${relPath} echoed as plain output`,
        20_000,
      );
    };

    // Docking a tile resizes the pane, which reflows text and invalidates any
    // cached row/col geometry.
    const clickTargetFor = async (relPath) => {
      // A docked tile takes focus and can fold the terminal into a sliver, whose
      // released surface reads empty; clicking the pane expands it first.
      await client.request('click_pane', { sessionId, paneId: pane.paneId });
      let read = { text: '' };
      let row = -1;
      const deadline = Date.now() + 15_000;
      while (Date.now() < deadline) {
        read = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
        // The echo command's own tail can wrap onto a row of its own; the output row is the last match.
        row = read.text.split('\n').findLastIndex((line) => line.trim() === relPath);
        if (row >= 0) break;
        await delay(250);
      }
      const lines = read.text.split('\n');
      if (row < 0) {
        throw new Error(`Path line for ${relPath} not found. Pane text:\n${read.text}`);
      }
      const col = lines[row].indexOf(relPath) + Math.floor(relPath.length / 2);
      const windowBounds = await client.request('get_window_bounds', {});
      if (!windowBounds?.logicalBounds) {
        throw new Error(`No window bounds: ${JSON.stringify(windowBounds)}`);
      }
      const cellRect = await client.request('get_pane_cell_rect', {
        sessionId,
        paneId: pane.paneId,
        cell: { row, col },
      });
      return {
        cell: { row, col },
        ...windowRelativePoint(
          cellRect.centerX,
          cellRect.centerY,
          windowBounds,
          cellRect.innerWidth,
          cellRect.innerHeight,
        ),
      };
    };

    const markdownTileIds = async () => {
      const state = await client.request('get_workspace_ui_state', { workspaceId });
      return (state.tileIds || []).filter((id) => id.startsWith('tile-markdown'));
    };

    const markdownTileNodes = () => collectMarkdownTiles(observer.workspacesBySessionId.get(sessionId));

    const tileFocused = async (tileId) => {
      const view = (await client.request('get_session_ui_state', { sessionId }))?.workspace?.view;
      return view?.activeLeafId === tileId;
    };

    const movePointerOntoCellFromElsewhere = async (target) => {
      await driver.movePointerInWindow(0.5, 0.9);
      await driver.movePointerInWindow(target.relativeX, target.relativeY);
    };

    // The pointer cursor is the receipt that the async path-exists check resolved.
    const pointAtLiveLink = async (relPath, timeoutMs = 20_000) => {
      const deadline = Date.now() + timeoutMs;
      let cursor = null;
      while (Date.now() < deadline) {
        const target = await clickTargetFor(relPath);
        ({ cursor } = await client.request('hover_pane_cell', {
          sessionId,
          paneId: pane.paneId,
          cell: target.cell,
          meta: true,
        }));
        if (cursor === 'pointer') {
          await movePointerOntoCellFromElsewhere(target);
          return target;
        }
        await delay(100);
      }
      throw new Error(`Timed out waiting for ${relPath} to become a live link (cursor ${cursor})`);
    };

    const waitFor = async (done, description, timeoutMs = 20_000) => {
      const deadline = Date.now() + timeoutMs;
      while (Date.now() < deadline) {
        if (await done()) return;
        await delay(100);
      }
      throw new Error(`Timed out waiting for ${description}`);
    };

    const cmdClickPath = async (relPath, done, description) => {
      const target = await pointAtLiveLink(relPath);
      await driver.clickWindow(target.relativeX, target.relativeY, { modifiers: { command: true } });
      await waitFor(done, description);
    };

    await runner.step('echo_paths', async () => {
      await echoPath('./alpha.md');
      await echoPath('./beta.md');
      await driver.activateApp();
    });

    let alphaTileId;
    let alphaNode;
    await runner.step('plain_click_beta_opens_nothing_cmd_click_alpha_docks', async () => {
      const plainTarget = await pointAtLiveLink('./beta.md');
      await driver.clickWindow(plainTarget.relativeX, plainTarget.relativeY);

      await cmdClickPath(
        './alpha.md',
        () => markdownTileNodes().some((node) => node.tile_params?.endsWith('/alpha.md')),
        'the alpha markdown tile to dock',
      );
      const dockedTiles = markdownTileNodes();
      runner.assert(
        dockedTiles.length === 1,
        `A plain click on the live ./beta.md link must open no markdown tile; docked ${JSON.stringify(dockedTiles)}`,
      );
      const tilesAfterAlpha = await markdownTileIds();
      alphaTileId = tilesAfterAlpha[0];
      runner.assert(
        tilesAfterAlpha.length === 1 && /^tile-markdown-[0-9a-f]{16}$/.test(alphaTileId),
        `Unexpected markdown tile ids: ${JSON.stringify(tilesAfterAlpha)}`,
      );
      alphaNode = dockedTiles[0];
      runner.assert(
        Boolean(alphaNode.tile_params?.endsWith('/alpha.md')),
        `Alpha tile params should end with /alpha.md: ${JSON.stringify(alphaNode)}`,
      );
      runner.assert(
        alphaNode.tile_session_id === sessionId,
        `Alpha tile must be bound to session ${sessionId}: ${JSON.stringify(alphaNode)}`,
      );
    });

    let betaTileId;
    let betaNode;
    await runner.step('cmd_click_beta_docks_second_tile', async () => {
      await cmdClickPath(
        './beta.md',
        () => markdownTileNodes().some((node) => node.tile_params?.endsWith('/beta.md')),
        'the beta markdown tile to dock alongside alpha',
      );
      const tilesAfterBeta = await markdownTileIds();
      runner.assert(
        tilesAfterBeta.length === 2 && tilesAfterBeta.includes(alphaTileId),
        `Beta must dock a second tile beside alpha: ${JSON.stringify(tilesAfterBeta)}`,
      );
      betaTileId = tilesAfterBeta.find((id) => id !== alphaTileId);
      const betaTiles = markdownTileNodes();
      betaNode = betaTiles.find((node) => node.tile_id === betaTileId);
      runner.assert(
        Boolean(betaNode?.tile_params?.endsWith('/beta.md')),
        `Beta tile params should end with /beta.md: ${JSON.stringify(betaTiles)}`,
      );
      runner.assert(
        betaNode.tile_session_id === sessionId,
        `Beta tile must be bound to session ${sessionId}: ${JSON.stringify(betaNode)}`,
      );
    });

    await runner.step('cmd_click_alpha_again_reuses_tile', async () => {
      await cmdClickPath(
        './alpha.md',
        () => tileFocused(alphaTileId),
        'the re-clicked alpha tile to take focus',
      );
      const tilesAfterReuse = await markdownTileIds();
      runner.assert(
        tilesAfterReuse.length === 2 && tilesAfterReuse.includes(alphaTileId) && tilesAfterReuse.includes(betaTileId),
        `Re-cmd+click alpha must reuse its tile (expected exactly [${alphaTileId}, ${betaTileId}]), got: ${JSON.stringify(tilesAfterReuse)}`,
      );

      await client.request('capture_native_window_screenshot', {
        path: path.join(runner.runDir, 'success-window.png'),
      }).catch(() => {});
    });

    // Last: a followed link hands off to the real system browser, which takes the
    // foreground away from attn and would break any later native click.
    const label = 'CLICK_ME_LINK';
    const url = `http://127.0.0.1:${port}/osc8-hit`;
    await runner.step('print_an_osc8_link_whose_uri_is_hidden_behind_a_label', async () => {
      // Octal escapes produce the same OSC bytes in bash, zsh, and fish.
      const esc = '\\033';
      const st = `${esc}\\134`; // string terminator: ESC + a single literal backslash
      // Both markdown tiles are still docked, and they fold the terminal into a
      // sliver whose released surface reads empty; clicking expands it first.
      await client.request('click_pane', { sessionId, paneId: pane.paneId });
      await client.request('write_pane', {
        sessionId,
        paneId: pane.paneId,
        text: `clear; printf '${esc}]8;;${url}${st}${label}${esc}]8;;${st}\\n'`,
      });
      await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (text) => text.split('\n').some((line) => line.trim() === label),
        'OSC 8 link label rendered',
        20_000,
      );
      await driver.activateApp();
    });

    await runner.step('plain_click_on_the_osc8_label_does_not_navigate', async () => {
      const target = await pointAtLiveLink(label);
      await driver.clickWindow(target.relativeX, target.relativeY);
      // A negative has no signal to wait on. 1s is the window a followed link has
      // needed to reach the probe server: on a pass the next step resolves under it.
      await delay(1_000);
      runner.assert(
        hits.length === 0,
        `Plain click on the OSC 8 label must not navigate, but the probe server saw: ${JSON.stringify(hits)}`,
      );
    });

    await runner.step('cmd_click_on_the_osc8_label_reaches_the_system_browser', async () => {
      const target = await pointAtLiveLink(label);
      await driver.clickWindow(target.relativeX, target.relativeY, { modifiers: { command: true } });
      await waitFor(() => hits.length > 0, 'the probe server to receive the followed OSC 8 uri', 10_000);
      runner.assert(hits.includes('/osc8-hit'), `Probe server received unexpected path(s): ${JSON.stringify(hits)}`);
    });

    const result = await runner.finishSuccess({
      sessionId,
      workspaceId,
      paneId: pane.paneId,
      alphaTileId,
      betaTileId,
      alphaTileParams: alphaNode.tile_params,
      betaTileParams: betaNode.tile_params,
      tileSessionId: alphaNode.tile_session_id,
      seedId,
      osc8Url: url,
      osc8Hits: hits,
    });
    console.log('[verify] PASS — terminal markdown-link: seed mark, docked, second tile, reuse on re-click, and OSC 8 navigation all matched.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'md-link-failure', sessionId).catch(() => {});
    }
    const result = await runner.finishFailure(error, { sessionId });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    await closeServer(server).catch(() => {});
    if (sessionId) {
      const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
      for (const pane of workspace?.panes || []) {
        await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
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
