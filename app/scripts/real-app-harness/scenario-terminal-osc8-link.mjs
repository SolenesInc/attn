#!/usr/bin/env node


import http from 'node:http';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver, delay } from './macosDriver.mjs';
import {
  captureSessionArtifacts,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

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

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-osc8-link.mjs');
    return;
  }

  // HID mouse clicks land at absolute screen positions, so the default 20px
  // window park would put every click off-window.
  if (process.env.ATTN_HARNESS_PARK_VISIBLE_PX === undefined) {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '800';
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-OSC8-LINK',
    tier: 'tier1-local-shell',
    prefix: 'terminal-osc8-link',
    metadata: {
      focus: 'OSC 8 hyperlink Cmd+click navigation, plain click stays selection',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;
  const { server, hits, port } = await startProbeServer();

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, wsUrl: options.wsUrl, probeServerPort: port });

  // Runner cleanups run in REVERSE registration order, so the effective order
  // below is: close probe server, close panes, quitApp, observer.close.
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
    await runner.step('create_session', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `osc8-link-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      await client.request('select_session', { sessionId });
      const workspace = await client.request('get_workspace', { sessionId });
      pane = workspace?.panes?.[0];
      runner.assert(Boolean(pane), `No pane in workspace: ${JSON.stringify(workspace)}`);
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, {
        timeoutMs: 20_000,
        description: 'shell pane ready',
      });
    });

    await runner.step('assert_term_program_ghostty', async () => {
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: 'echo "TP=$TERM_PROGRAM"' });
      await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (text) => text.split('\n').some((line) => line.trim() === 'TP=ghostty'),
        'TERM_PROGRAM=ghostty echoed',
        20_000,
      );
      const tpRead = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
      runner.assert(
        tpRead.text.split('\n').some((line) => line.trim() === 'TP=ghostty'),
        `TERM_PROGRAM was not pinned to ghostty. Pane text:\n${tpRead.text}`,
      );
    });

    let label;
    let url;
    let labelRow;
    let labelCol;
    let target;
    let paneState;
    await runner.step('print_link_and_locate_label', async () => {
      // The visible label carries no URL text, so a navigation can only have come
      // from following the escape's URI.
      label = 'CLICK_ME_LINK';
      url = `http://127.0.0.1:${port}/osc8-hit`;
      // Literal shell source text: printf itself turns \e into ESC and \\ into a
      // single backslash when it interprets the format string.
      const esc = '\\e';
      const st = `${esc}\\\\`; // string terminator: ESC + a single literal backslash
      const printfCommand = `printf '${esc}]8;;${url}${st}${label}${esc}]8;;${st}\\n'`;
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: printfCommand });
      paneState = await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (text) => text.includes(label),
        'OSC 8 link label rendered',
        20_000,
      );
      const read = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
      const lines = read.text.split('\n');
      labelRow = lines.findIndex((line) => line.includes(label));
      runner.assert(labelRow >= 0, `Link label row disappeared. Pane text:\n${read.text}`);
      labelCol = lines[labelRow].indexOf(label) + Math.floor(label.length / 2);

      const windowBounds = await client.request('get_window_bounds', {});
      runner.assert(Boolean(windowBounds?.logicalBounds), `No window bounds: ${JSON.stringify(windowBounds)}`);
      const cellRect = await client.request('get_pane_cell_rect', {
        sessionId,
        paneId: pane.paneId,
        cell: { row: labelRow, col: labelCol },
      });
      target = windowRelativePoint(
        cellRect.centerX,
        cellRect.centerY,
        windowBounds,
        cellRect.innerWidth,
        cellRect.innerHeight,
      );

      await driver.activateApp();
    });

    await runner.step('plain_click_stays_selection', async () => {
      await driver.clickWindow(target.relativeX, target.relativeY);
      await delay(1_000);
      runner.assert(
        hits.length === 0,
        `Plain click on the OSC 8 label must not navigate, but the probe server saw: ${JSON.stringify(hits)}`,
      );
    });

    await runner.step('cmd_click_opens_link', async () => {
      await driver.clickWindow(target.relativeX, target.relativeY, { modifiers: { command: true } });
      const deadline = Date.now() + 10_000;
      while (hits.length === 0 && Date.now() < deadline) {
        await delay(250);
      }
      runner.assert(hits.length > 0, 'Cmd+click on the OSC 8 label never reached the probe server.');
      runner.assert(hits.includes('/osc8-hit'), `Probe server received unexpected path(s): ${JSON.stringify(hits)}`);
    });

    const result = await runner.finishSuccess({
      sessionId,
      paneId: pane.paneId,
      label,
      url,
      labelRow,
      labelCol,
      hits,
      paneRows: paneState?.size?.rows ?? null,
    });
    console.log('[verify] PASS — terminal OSC 8 hyperlink: plain click stayed selection, Cmd+click navigated.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'osc8-link-failure', sessionId).catch(() => {});
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
