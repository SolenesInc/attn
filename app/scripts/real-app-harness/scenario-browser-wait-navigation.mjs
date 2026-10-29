#!/usr/bin/env node
import http from 'node:http';
import fs from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, { scenarioId: 'BROWSER-WAIT-NAVIGATION', tier: 'local', prefix: 'browser-wait-navigation' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const exec = promisify(execFile);
const receipts = [];
const pages = new Map();
const server = http.createServer((request, response) => {
  const id = request.url.slice(1);
  const page = pages.get(id);
  if (!page) { response.writeHead(404).end(); return; }
  page.requested(response);
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
  const result = await exec(appDaemonInTree(options.appPath), args, { env: instanceCliEnv(currentHarnessInstance()) });
  receipts.push({ args, ...result });
  runner.writeJson('cli.json', receipts);
  return result.stdout;
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
  const openAndWait = async id => {
    const requested = new Promise(resolve => { pages.set(id, { requested: resolve }); });
    const pending = observePendingWait(`#${id}`);
    try {
      await cli('browser', 'open', `http://127.0.0.1:${server.address().port}/${id}`, '--session', sessionId);
      const release = await requested;
      const waiting = cli('browser', 'wait', '--using', 'css', '--value', `#${id}`, '--state', 'visible', '--session', sessionId).then(stdout => ({ stdout }), error => ({ error }));
      const { error, receipt } = await pending.result;
      if (error) throw error;
      runner.log('wait_pending_before_document_replacement', receipt);
      release.setHeader('content-type', 'text/html');
      release.end(`<title>${id}</title><h1 id="${id}">${id}</h1>`);
      const outcome = await waiting;
      if (outcome.error) throw outcome.error;
      const result = JSON.parse(outcome.stdout);
      runner.assert(Boolean(result['element-6066-11e4-a52e-4f735466cecf']), 'wait returns the new document element', { id, result });
      const snapshot = JSON.parse(await cli('browser', 'snapshot', '--session', sessionId));
      runner.assert(snapshot.title === id && snapshot.text === id, 'the wait observed the requested document', { snapshot });
    } finally {
      pending.close();
    }
  };
  await runner.step('wait_during_initial_navigation', () => openAndWait('initial'));
  await runner.step('wait_after_close_and_reopen', async () => {
    const selector = `[data-session-terminal-desktop="${desktop.id}"] [data-tile-kind="browser"]`;
    await client.request('dom_click', { selector: `${selector} [aria-label="Close tile"]` });
    await client.request('dom_wait', { selector, absent: true, timeoutMs: observer.connectTimeoutMs });
    await openAndWait('reopened');
  });
  await runner.step('wait_during_existing_tile_navigation', () => openAndWait('navigated'));
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
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
