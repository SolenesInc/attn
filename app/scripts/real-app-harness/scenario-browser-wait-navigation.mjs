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
const server = http.createServer((request, response) => {
  const id = request.url.slice(1);
  response.setHeader('content-type', 'text/html');
  response.end(`<title>${id}</title><h1 id="${id}">${id}</h1>`);
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
    const result = await exec(appDaemonInTree(options.appPath), args, { env: instanceCliEnv(currentHarnessInstance()) });
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
  await runner.step('initial_open_then_wait', () => openAndWait('initial'));
  await runner.step('reopen_then_wait', async () => {
    const selector = `[data-session-terminal-desktop="${desktop.id}"] [data-tile-kind="browser"]`;
    await client.request('dom_click', { selector: `${selector} [aria-label="Close tile"]` });
    await client.request('dom_wait', { selector, absent: true, timeoutMs: observer.connectTimeoutMs });
    await openAndWait('reopened');
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
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
