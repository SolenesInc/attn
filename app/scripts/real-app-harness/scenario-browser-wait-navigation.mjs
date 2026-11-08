#!/usr/bin/env node
import http from 'node:http';
import fs from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, { scenarioId: 'BROWSER-WAIT-NAVIGATION', tier: 'local', prefix: 'browser-wait-navigation' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const exec = promisify(execFile);
const driver = createWindowDriver({ appPath: options.appPath, client });
const receipts = [];
const pageRequests = new Map();
const server = http.createServer((request, response) => {
  const id = request.url.slice(1);
  pageRequests.set(id, (pageRequests.get(id) ?? 0) + 1);
  response.setHeader('content-type', 'text/html');
  response.end(`<title>${id}</title><h1 id="${id}">${id}</h1><input id="field" aria-label="Field">`);
});
const observePendingWait = selector => {
  const logPath = client.launch.logPath;
  let finish;
  let closed = false;
  let timer;
  let watcher;
  const close = () => {
    closed = true;
    watcher?.close();
    clearTimeout(timer);
  };
  const result = new Promise(resolve => {
    finish = (error, receipt) => {
      if (closed) return;
      close();
      resolve({ error, receipt });
    };
  });
  const inspect = () => {
    try {
      const lines = fs.readFileSync(logPath, 'utf8').split('\n');
      lines.pop();
      for (const line of lines) {
        if (!line.startsWith('[BrowserWait] pending ')) continue;
        const receipt = JSON.parse(line.slice('[BrowserWait] pending '.length));
        if (receipt.params.value === selector) {
          finish(null, receipt);
          return;
        }
      }
    } catch (error) {
      finish(error);
    }
  };
  try {
    watcher = fs.watch(logPath, inspect);
    watcher.on('error', error => finish(error));
    timer = setTimeout(() => finish(new Error(`No unsuccessful wait probe for ${selector}; log=${logPath}`)), observer.connectTimeoutMs);
    inspect();
  } catch (error) {
    finish(error);
  }
  return { result, close };
};
const cli = async (...args) => {
  try {
    const result = await exec(appCliInTree(options.appPath), args, { env: instanceCliEnv(currentHarnessInstance()) });
    receipts.push({ args, ...result });
    return result.stdout;
  } catch (error) {
    receipts.push({ args, stdout: error.stdout, stderr: error.stderr, code: error.code });
    throw error;
  } finally {
    runner.writeJson('cli.json', receipts);
  }
};
runner.registerCleanup('close_page_server', () => { server.closeAllConnections(); return new Promise(resolve => server.close(resolve)); });
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());

try {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  await launchFreshAppAndConnect(client, observer);
  const desktop = await observer.createDesktop();
  await observer.setCurrentDesktop(desktop.id);
  const { sessionId } = await client.request('create_session', { cwd: runner.sessionDir, agent: 'claude', label: 'Browser wait navigation' });
  await client.request('select_session', { sessionId });
  const assertPage = async (id, stdout) => {
    const result = JSON.parse(stdout);
    runner.assert(Boolean(result['element-6066-11e4-a52e-4f735466cecf']), 'wait returns the new document element', { id, result });
    const snapshot = JSON.parse(await cli('browser', 'snapshot', '--session', sessionId));
    runner.assert(snapshot.title === id && snapshot.text === id, 'the wait observed the requested document', { snapshot });
  };
  const waitForPage = id => cli('browser', 'wait', '--using', 'css', '--value', `#${id}`, '--state', 'visible', '--session', sessionId);
  const openAndWait = async id => {
    await cli('browser', 'open', `http://127.0.0.1:${server.address().port}/${id}`, '--session', sessionId);
    await assertPage(id, await waitForPage(id));
  };
  const browserTile = `[data-session-terminal-desktop="${desktop.id}"] [data-tile-kind="browser"]`;
  const closeBrowserTile = async () => {
    await client.request('dom_click', { selector: `${browserTile} [aria-label="Close tile"]` });
    await client.request('dom_wait', { selector: browserTile, absent: true, timeoutMs: observer.connectTimeoutMs });
  };
  const script = (source) => cli('browser', 'command', 'execute_script', '--params', JSON.stringify({ script: source, args: [] }), '--session', sessionId);
  const { logicalBounds } = await client.request('get_window_bounds');
  await runner.step('commands_reach_a_tile_on_the_shelf_and_leave_the_screen_alone', async () => {
    await client.request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1000, height: 800 } });
    const screenState = async () => {
      const { shownLeafIds } = await client.request('get_desktop_ui_state', { desktopId: desktop.id });
      const activeLeaf = (await client.request('get_session_ui_state', { sessionId })).desktop?.view?.activeLeafId;
      const { tag: focusedTag, className: focusedClass } = await client.request('dom_active_element');
      const { label: browserFocus } = await client.request('get_browser_focus_state');
      return JSON.stringify({ shownLeafIds, activeLeaf, focusedTag, focusedClass, browserFocus });
    };
    const before = await screenState();
    const screenStaysTheSame = async (doing) => {
      const now = await screenState();
      runner.assert(now === before, `the screen and focus stay the same after ${doing}`, { before, now });
    };
    await openAndWait('shelved-first');
    const chip = '[data-testid="shelf-chip-tile-browser"]';
    await client.request('dom_wait', { selector: chip, timeoutMs: observer.connectTimeoutMs });
    await screenStaysTheSame('an open that puts the tile on the shelf');
    await openAndWait('shelved');
    await screenStaysTheSame('an open that navigates the tile on the shelf');
    const inPage = (...args) => cli('browser', ...args, '--session', sessionId);
    const heading = JSON.parse(await inPage('find', '--using', 'css', '--value', '#shelved'));
    runner.assert(Boolean(heading['element-6066-11e4-a52e-4f735466cecf']), 'find answers from the shelf', { heading });
    await inPage('click', '--selector', 'h1');
    await inPage('type', '--selector', '#field', '--text', 'typed');
    runner.assert(JSON.parse(await script('return document.querySelector("#field").value;')) === 'typed', 'type reaches the page on the shelf');
    await inPage('press', '--text', 'Tab');
    await inPage('scroll', '--y', '10');
    await inPage('reload');
    await waitForPage('shelved');
    await inPage('back');
    await waitForPage('shelved-first');
    await inPage('forward');
    await waitForPage('shelved');
    await inPage('cookies');
    await screenStaysTheSame('every control command on the tile on the shelf');
    const loads = pageRequests.get('shelved');
    await script('window.attnKept = "kept"; return null;');
    await driver.click(chip);
    await client.request('dom_wait', { selector: browserTile, timeoutMs: observer.connectTimeoutMs });
    const kept = JSON.parse(await script('return window.attnKept;'));
    runner.assert(kept === 'kept' && pageRequests.get('shelved') === loads, 'the tile comes on screen with the page the agent left, not a reload', { kept, loads, requests: pageRequests.get('shelved') });
    await closeBrowserTile();
  });
  await client.request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1900, height: 900 } });
  await runner.step('initial_open_then_wait', () => openAndWait('initial'));
  await runner.step('reopen_then_wait', async () => {
    await closeBrowserTile();
    await openAndWait('reopened');
  });
  await runner.step('command_wait_after_navigation', async () => {
    const id = 'command';
    await cli('browser', 'open', `http://127.0.0.1:${server.address().port}/${id}`, '--session', sessionId);
    await assertPage(id, await cli('browser', 'command', 'wait_for', '--params', JSON.stringify({
      using: 'css', value: `#${id}`, state: 'visible',
    }), '--session', sessionId));
  });
  await runner.step('navigation_replaces_a_pending_wait', async () => {
    const id = 'navigated';
    const pending = observePendingWait(`#${id}`);
    const waiting = waitForPage(id).then(stdout => ({ stdout }), error => ({ error }));
    try {
      const { error, receipt } = await Promise.race([
        pending.result,
        waiting.then(outcome => ({ error: outcome.error || new Error('The wait completed before navigation started') })),
      ]);
      if (error) throw error;
      runner.log('wait_pending_before_document_replacement', receipt);
      await cli('browser', 'open', `http://127.0.0.1:${server.address().port}/${id}`, '--session', sessionId);
      const outcome = await waiting;
      if (outcome.error) throw outcome.error;
      await assertPage(id, outcome.stdout);
    } finally {
      pending.close();
    }
  });
  await runner.step('zero_timeout_checks_once', async () => {
    const found = JSON.parse(await cli('browser', 'wait', '--using', 'css', '--value', '#navigated', '--timeout', '0', '--session', sessionId));
    runner.assert(Boolean(found['element-6066-11e4-a52e-4f735466cecf']), 'an existing element satisfies a zero-timeout wait');
    try {
      await cli('browser', 'wait', '--using', 'css', '--value', '#absent', '--timeout', '0', '--session', sessionId);
      throw new Error('an absent element satisfied a zero-timeout wait');
    } catch (error) {
      runner.assert(error.code === 1 && error.stderr.includes('timeoutMs=0'), 'an absent element fails the zero-timeout wait', { stderr: error.stderr });
    }
  });
  await runner.step('actions_execute_once_and_keep_element_references', async () => {
    const element = JSON.parse(await cli('browser', 'find', '--using', 'css', '--value', '#navigated', '--session', sessionId));
    const result = JSON.parse(await cli('browser', 'command', 'execute_async_script', '--params', JSON.stringify({
      script: `
        const element = arguments[0];
        const done = arguments[1];
        element.dataset.actions = String(Number(element.dataset.actions || 0) + 1);
        queueMicrotask(() => done(element.dataset.actions));
      `,
      args: [element],
    }), '--session', sessionId));
    runner.assert(result === '1', 'an asynchronous action executes once', { result });
    const actions = JSON.parse(await cli('browser', 'command', 'get_element_attribute', '--params', JSON.stringify({
      element, name: 'data-actions',
    }), '--session', sessionId));
    runner.assert(actions === '1', 'later actions retain the element reference and observe one effect', { actions });
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
