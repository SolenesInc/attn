#!/usr/bin/env node
import http from 'node:http';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { launchFreshAppAndConnect, parseCommonArgs, queueDaemonSettingRestore } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, { scenarioId: 'BROWSER-PAGE-KEYS', tier: 'local', prefix: 'browser-page-keys' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const exec = promisify(execFile);
const driver = createWindowDriver({ appPath: options.appPath, client });
const IN_PAGE = { withoutReceipt: 'the browser page takes it' };
const THROUGH_APPKIT = { ...IN_PAGE, queued: true };
const receipts = [];
const server = http.createServer((request, response) => {
  const id = request.url.slice(1);
  response.setHeader('content-type', 'text/html');
  response.end(`<title>${id}</title><h1 id="${id}">${id}</h1>`);
});
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
  queueDaemonSettingRestore(observer, 'uiScale');
  const uiScale = observer.getSetting('uiScale') || '';
  const desktop = await observer.createDesktop();
  await observer.setCurrentDesktop(desktop.id);
  const { sessionId } = await client.request('create_session', { cwd: runner.sessionDir, agent: 'claude', label: 'Browser page keys' });
  await client.request('select_session', { sessionId });
  const inBrowser = (...args) => cli('browser', ...args, '--session', sessionId);
  const script = async source => JSON.parse(await inBrowser('command', 'execute_script', '--params', JSON.stringify({ script: source, args: [] })));
  const page = id => `http://127.0.0.1:${server.address().port}/${id}`;
  const showsPage = id => inBrowser('wait', '--using', 'css', '--value', `#${id}`, '--state', 'visible');
  const width = () => script('return window.innerWidth;');
  const command = key => driver.pressKey(key, { command: true }, THROUGH_APPKIT);
  const sequence = async then => {
    await driver.pressKey('y', { command: true, shift: true }, THROUGH_APPKIT);
    await driver.pressKey(then, {}, THROUGH_APPKIT);
  };
  const widthAfter = async (press, from) => {
    await press();
    return JSON.parse(await inBrowser('command', 'execute_async_script', '--params', JSON.stringify({
      script: `const [from, done] = arguments;
        const settle = () => { if (window.innerWidth !== from) done(window.innerWidth); };
        addEventListener('resize', settle);
        settle();`,
      args: [from],
    })));
  };
  const tile = `[data-session-terminal-desktop="${desktop.id}"] [data-tile-kind="browser"]`;

  await runner.step('open_two_pages_and_focus_the_tile', async () => {
    const { logicalBounds } = await client.request('get_window_bounds');
    await client.request('set_window_bounds', { logicalBounds: { ...logicalBounds, width: 1900, height: 900 } });
    await inBrowser('open', page('first'));
    await showsPage('first');
    await inBrowser('open', page('second'));
    await showsPage('second');
    await client.request('dom_wait', { selector: `${tile} .browser-tile-host`, timeoutMs: observer.connectTimeoutMs });
    await driver.click({ selector: `${tile} .browser-tile-host`, at: { x: 0.5, y: 0.5 } }, IN_PAGE);
    const { label } = await client.request('get_browser_focus_state');
    runner.assert(Boolean(label), 'clicking the page gives it the keyboard', { label });
  });

  await runner.step('command_brackets_go_back_and_forward', async () => {
    await driver.pressKey('[', { command: true }, THROUGH_APPKIT);
    await showsPage('first');
    await driver.pressKey(']', { command: true }, THROUGH_APPKIT);
    await showsPage('second');
  });

  await runner.step('command_plus_minus_and_zero_zoom_the_page_not_the_app', async () => {
    const actual = await width();
    const zoomedIn = await widthAfter(() => command('='), actual);
    runner.assert(zoomedIn < actual, '⌘= zooms the page in', { actual, zoomedIn });
    const backToActual = await widthAfter(() => command('-'), zoomedIn);
    const zoomedOut = await widthAfter(() => command('-'), backToActual);
    runner.assert(backToActual === actual && zoomedOut > actual, '⌘- zooms the page out a step at a time', { actual, backToActual, zoomedOut });
    const reset = await widthAfter(() => command('0'), zoomedOut);
    runner.assert(reset === actual, '⌘0 puts the page back at actual size', { actual, reset });
    runner.assert((observer.getSetting('uiScale') || '') === uiScale, 'the app\'s own text size stays the same', { before: uiScale, after: observer.getSetting('uiScale') });
  });

  await runner.step('keys_rebound_to_sequences_walk_history_and_zoom_the_page', async () => {
    queueDaemonSettingRestore(observer, 'keybindings_config');
    const leader = { key: 'y', meta: true, shift: true };
    await client.request('set_setting', {
      key: 'keybindings_config',
      value: JSON.stringify({ version: 1, overrides: {
        'session.historyBack': { leader, then: { key: 'b' } },
        'session.historyForward': { leader, then: { key: 'f' } },
        'ui.increaseFontSize': { leader, then: { key: 'i' } },
        'ui.resetFontSize': { leader, then: { key: 'r' } },
      } }),
    });
    await sequence('b');
    await showsPage('first');
    await sequence('f');
    await showsPage('second');
    const actual = await width();
    const zoomedIn = await widthAfter(() => sequence('i'), actual);
    runner.assert(zoomedIn < actual, 'the rebound zoom-in sequence zooms the page in', { actual, zoomedIn });
    const reset = await widthAfter(() => sequence('r'), zoomedIn);
    runner.assert(reset === actual, 'the rebound reset sequence puts the page back at actual size', { actual, reset });
    runner.assert((observer.getSetting('uiScale') || '') === uiScale, 'the app\'s own text size stays the same', { before: uiScale, after: observer.getSetting('uiScale') });
  });

  await client.request('dom_click', { selector: `${tile} [aria-label="Close tile"]` });
  await client.request('dom_wait', { selector: tile, absent: true, timeoutMs: observer.connectTimeoutMs });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
