#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const TILE = 'tile-browser';
const exec = promisify(execFile);
const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-BROWSER', tier: 'tier1-local-shell', prefix: 'native-browser',
  metadata: { focus: 'In the native app an agent opens a browser tile, waits, reads, types, clicks, follows history, answers a dialog, captures a screenshot and a PDF, reads cookies, and the close key closes the tile, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const receipts = [];

const pages = {
  page: '<title>page</title><h1 id="page">page</h1><input id="field" aria-label="Field"><button id="go" onclick="document.querySelector(\'h1\').textContent=\'Clicked \'+document.querySelector(\'input\').value">Go</button><button id="ask" onclick="document.querySelector(\'h1\').textContent=\'Asked \'+prompt(\'Your name?\',\'Ada\')">Ask</button><a id="next" href="/other">Other</a><script>document.cookie="drive=1; path=/"</script>',
  other: '<title>other</title><h1 id="other">Other heading</h1>',
};
const server = http.createServer((request, response) => {
  response.setHeader('content-type', 'text/html');
  response.end(pages[request.url.slice(1)] ?? '<title>missing</title>');
});

const cli = async (...args) => {
  try {
    const result = await exec(appCliInTree(options.appPath), args, { env: instanceCliEnv(currentHarnessInstance()), cwd: runner.sessionDir });
    receipts.push({ args, ...result });
    return result.stdout;
  } catch (error) {
    receipts.push({ args, stdout: error.stdout, stderr: error.stderr, code: error.code });
    throw error;
  } finally {
    runner.writeJson('cli.json', receipts);
  }
};

function leafOf(desktop, leafId) {
  const walk = (node) => {
    if (!node) return null;
    if (node.pane_id === leafId || node.tile_id === leafId) return node;
    for (const child of node.children ?? []) {
      const found = walk(child);
      if (found) return found;
    }
    return null;
  };
  return walk(JSON.parse(desktop?.tree_json || 'null'));
}

runner.registerCleanup('close_page_server', () => { server.closeAllConnections(); return new Promise((resolve) => server.close(resolve)); });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

try {
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const at = (page) => `http://127.0.0.1:${server.address().port}/${page}`;
  const frontmostBefore = await driver.frontmostBundleId();
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_two_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  let desktop;
  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => {
      const left = observer.desktop(desktop.id);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  let shell;
  await runner.step('a_shell_to_open_the_browser_beside', async () => {
    const id = crypto.randomUUID();
    const cwd = path.join(runner.sessionDir, 'shell');
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'shell', cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
    shell = { id, pane: result.pane_id };
  });

  const browser = (...args) => cli('browser', ...args, '--session', shell.id);
  const snapshot = async () => JSON.parse(await browser('snapshot'));
  const waitFor = (css) => browser('wait', '--using', 'css', '--value', css, '--state', 'visible');
  const waitText = (text) => browser('wait', '--using', 'text', '--value', text, '--state', 'visible');

  await runner.step('an_agent_opens_a_page_and_the_tile_shows_it', async () => {
    const opened = await browser('open', at('page'));
    runner.assert(opened.includes(`opened ${at('page')}`), 'open reports the address', opened);
    await driver.waitFor({ key: `tile.${TILE}.page` });
    await waitFor('#page');
    const read = await snapshot();
    runner.assert(read.title === 'page', 'the snapshot reads the page the tile shows', read);
    const stored = leafOf(observer.desktop(desktop.id), TILE);
    runner.assert(stored?.tile_params === at('page'), 'the tile stores the address', stored);
  });

  await runner.step('type_click_and_wait', async () => {
    await browser('type', '--selector', '#field', '--text', 'drive');
    await browser('click', '--selector', '#go');
    await waitText('Clicked drive');
  });

  await runner.step('a_dialog_during_an_action_is_the_agents', async () => {
    const clicked = await browser('click', '--selector', '#ask');
    runner.assert(clicked.trim() === 'null' || clicked.trim() === '', 'the dialog ends the click', clicked);
    const text = await browser('command', 'get_alert_text');
    runner.assert(text.includes('Your name?'), 'the agent reads the dialog', text);
    await browser('command', 'send_alert_text', '--params', JSON.stringify({ text: 'Grace' }));
    await browser('command', 'accept_alert');
    await waitText('Asked Grace');
  });

  await runner.step('history_moves_the_page_and_the_stored_address', async () => {
    await browser('click', '--selector', '#next');
    await waitText('Other heading');
    await observer.waitFor(() => leafOf(observer.desktop(desktop.id), TILE)?.tile_params === at('other'), 'the tile stores where the page went');
    await browser('back');
    await waitFor('#page');
    await browser('reload');
    await waitFor('#page');
  });

  await runner.step('captures_and_cookies', async () => {
    const shot = path.join(runner.sessionDir, 'shot.png');
    const pdf = path.join(runner.sessionDir, 'page.pdf');
    await browser('screenshot', shot);
    await browser('pdf', pdf);
    runner.assert(fs.readFileSync(shot).subarray(1, 4).toString() === 'PNG', 'the screenshot is a PNG', { shot });
    runner.assert(fs.readFileSync(pdf).subarray(0, 4).toString() === '%PDF', 'the PDF is a PDF', { pdf });
    const cookies = JSON.parse(await browser('cookies'));
    runner.assert(cookies.some((cookie) => cookie.name === 'drive' && cookie.value === '1'), 'the page\'s cookie reads back', cookies);
  });

  await runner.step('the_close_key_on_the_browser_tile_closes_it', async () => {
    await driver.click({ key: `tile.${TILE}.page` });
    await observer.waitFor(() => observer.desktop(desktop.id)?.active_pane_id === TILE, 'the browser tile is active');
    await driver.pressKey(process.platform === 'darwin' ? 'mod+w' : 'mod+shift+w');
    await observer.waitFor(() => !leafOf(observer.desktop(desktop.id), TILE), 'the browser tile closed');
    runner.assert(leafOf(observer.desktop(desktop.id), shell.pane), 'the shell beside it stays');
    await driver.waitFor({ key: `tile.${TILE}` }, { gone: true });
  });

  const frontmostAfter = await driver.frontmostBundleId();
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ desktopId: desktop.id, shell });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
