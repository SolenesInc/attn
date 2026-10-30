#!/usr/bin/env node
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
if (!currentHarnessInstance()) throw new Error('Focus-free native input requires a named instance');
if (process.env.ATTN_HARNESS_ALWAYS_ON_TOP === '0' || process.env.ATTN_HARNESS_PARK_VISIBLE_PX === '0') {
  throw new Error('Focus-free native input requires the default parked harness launch');
}
const runner = createScenarioRunner(options, { scenarioId: 'FOCUSLESS-NATIVE', tier: 'local', prefix: 'focusless-native' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client, actionDelayMs: 0 });
const exec = promisify(execFile);
const receipts = [];
const invoke = client.request.bind(client);
client.request = async (action, payload = {}, ...options) => {
  const started = performance.now();
  try {
    const result = await invoke(action, payload, ...options);
    receipts.push({ action, payload, result, elapsedMs: performance.now() - started });
    runner.writeJson('bridge.json', receipts);
    return result;
  } catch (error) {
    receipts.push({ action, payload, error: String(error), elapsedMs: performance.now() - started });
    runner.writeJson('bridge.json', receipts);
    throw error;
  }
};
const request = (action, payload) => client.request(action, payload);
const wait = (selector, extra = {}) => request('dom_wait', { selector, timeoutMs: observer.connectTimeoutMs, ...extra });
const cli = async (...args) => {
  const result = await exec(appDaemonInTree(options.appPath), args, { env: instanceCliEnv(currentHarnessInstance()) });
  runner.log('cli', { args, ...result });
  return result.stdout;
};
const relative = async (x, y) => {
  const [{ logicalBounds: window }, { viewport }] = await Promise.all([request('get_window_bounds'), request('dom_focus_state')]);
  return {
    x: (Math.max(0, window.width - viewport.innerWidth) / 2 + x) / window.width,
    y: (Math.max(0, window.height - viewport.innerHeight) + y) / window.height,
  };
};
const point = async (selector, textIncludes) => {
  const { bounds } = await request('dom_bounds', { selector, textIncludes });
  const { viewport } = await request('dom_focus_state');
  runner.assert(bounds.width > 0 && bounds.height > 0 && bounds.x >= 0 && bounds.y >= 0
    && bounds.x + bounds.width <= viewport.innerWidth && bounds.y + bounds.height <= viewport.innerHeight,
  'the native target is inside the viewport', { selector, bounds, viewport });
  return relative(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
};
const click = async (selector, textIncludes) => {
  const target = await point(selector, textIncludes);
  await request('arm_native_pointer_witness', { selector });
  await driver.clickWindow(target.x, target.y);
  const receipt = await request('wait_native_pointer_witness');
  runner.assert(receipt.matches, 'native mouseup reaches the requested control', { selector, receipt });
};
let parkedFrame;
const inactive = async stage => {
  const state = await request('native_input_state');
  runner.assert(!state.appActive && !state.actualApplicationKeyWindow && !state.windowKey
    && !state.windowCanBecomeKey && !state.secureInput && state.frontmostPid !== state.appPid,
  'native input leaves actual application and window focus elsewhere', { stage, state });
  parkedFrame ??= state.frame;
  runner.assert(JSON.stringify(state.frame) === JSON.stringify(parkedFrame), 'the window stays parked', { stage, state });
};
let browserLoaded;
const pageLoaded = new Promise(resolve => { browserLoaded = resolve; });
const server = http.createServer((request, response) => {
  if (request.url === '/loaded') { response.end(); browserLoaded(); return; }
  response.setHeader('content-type', 'text/html');
  response.end('<title>Native browser input</title><body><h1>Native browser input</h1><input id="password" type="password" pattern="probe" required style="width:90%;height:50px"><script>document.addEventListener("mousemove",event=>document.body.dataset.buttons=String(event.buttons));fetch("/loaded")</script>');
});
runner.registerCleanup('close_page_server', () => new Promise(resolve => server.close(resolve)));
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('close_observer', () => observer.close());
registerNativeFailureEvidence(runner, { client, driver, selectors: () => ({ sidebar: '[data-testid="sidebar-queue"]' }) });

try {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(runner.sessionDir, '.attn-mock-agent.json'), JSON.stringify({ version: 1, turns: [
    { includes: 'quote', actions: [{ type: 'reply', text: 'retry wrapper handles terminal annotation selection' }] },
  ] }));
  await launchFreshAppAndConnect(client, observer);
  await inactive('launch');
  const desktop = await observer.createDesktop();
  await observer.setCurrentDesktop(desktop.id);
  const { sessionId } = await request('create_session', { cwd: runner.sessionDir, label: 'Focusless native', agent: 'claude' });
  await request('select_session', { sessionId });
  await cli('settings', 'set', 'queue_mode_enabled', 'true');
  await wait(`[data-testid="queue-turn-${sessionId}"]`);

  await runner.step('native_hover_and_row_actions', async () => {
    const row = `.queue-row[data-session-id="${sessionId}"]`;
    await request('native_pointer_trace_start');
    const target = await point(row);
    await driver.movePointerInWindow(target.x, target.y);
    const trace = await request('native_pointer_trace_read');
    runner.assert(trace.events.some(event => event.type === 'mousemove' && event.trusted
      && event.hover.some(element => String(element.className).includes('queue-row'))), 'native movement establishes CSS hover', { trace });
    await click(`[data-testid="session-actions-${sessionId}"]`);
    await wait('[role="menu"][aria-label="Actions for Focusless native"]');
    await driver.pressKey('Escape');
    await click('[aria-label="Snooze Focusless native"]');
    await wait('[data-testid="snooze-choice-1h"]');
    await click('[data-testid="snooze-choice-1h"]');
    await wait(`[data-testid="queue-turn-${sessionId}"]`, { absent: true });
    await click('[data-testid="queue-agents-toggle"]');
    await wait('[aria-label="Wake Focusless native"]');
    const wake = await point(`[data-testid="queue-snoozed-${sessionId}"]`);
    await driver.movePointerInWindow(wake.x, wake.y);
    await click('[aria-label="Wake Focusless native"]');
    await wait(`[data-testid="queue-turn-${sessionId}"]`);
    const sessions = JSON.parse(await cli('agent', 'list', '--json'));
    runner.assert(sessions.find(session => session.id === sessionId)?.turn_owed, 'the native unsnooze is visible through the CLI');
    await inactive('row actions');
  });

  await runner.step('home_settings_ledger_and_queue_bar', async () => {
    await driver.pressKey('l', { command: true, shift: true });
    runner.assert((await request('sessions_get_state')).open, 'the ledger opens through AppKit keyboard dispatch');
    await driver.pressKey('Escape');
    await driver.pressKey('h', { command: true, shift: true });
    await click('[data-testid="settings-button"]');
    for (const section of ['general', 'desktop', 'hygiene', 'experimental', 'connectivity']) {
      await click(`[data-testid="settings-nav-${section}"]`);
      runner.assert((await request('settings_get_state')).activeSection === section, 'native Settings navigation selects its section', { section });
    }
    await click('[aria-label="Search settings"]');
    await driver.typeText('sweep');
    await wait('[aria-label="Search settings"][value="sweep"]');
    const { bounds } = await request('dom_bounds', { selector: '[aria-label="Search settings"]' });
    const from = await relative(bounds.x + 2, bounds.y + bounds.height / 2);
    const to = await relative(bounds.x + bounds.width - 2, bounds.y + bounds.height / 2);
    await request('native_pointer_trace_start');
    await driver.dragWindow(from.x, from.y, to.x, to.y);
    const trace = await request('native_pointer_trace_read');
    runner.assert(trace.events.filter(event => event.type === 'mousemove').slice(1).every(event => event.buttons === 1), 'drag button state survives editable input handling', { trace });
    await driver.typeText('replacement');
    await wait('[aria-label="Search settings"][value="replacement"]');
    runner.assert((await request('dom_value', { selector: '[aria-label="Search settings"]' })).value === 'replacement', 'native dragging selects editable text');
    await driver.pressKey('Escape');
    await driver.pressKey('b', { command: true });
    await wait('[data-testid="queue-bar-pill"]');
    await click('[data-testid="queue-bar-pill"]');
    await wait('[role="combobox"][aria-label="Agents"]', { focused: true });
    await driver.pressKey('Escape');
    await click('[aria-label="Show sidebar"]');
    await inactive('keys and Settings');
  });

  await runner.step('terminal_alt_drag', async () => {
    await click(`[aria-label="Open Focusless native"]`);
    await cli('settings', 'set', 'auto_settle_enabled', 'true');
    const kept = observer.waitForMessage(data => data.event === 'session_state_changed'
      && data.session?.id === sessionId && data.session.auto_settle_dismiss_armed, 'the native File menu shortcut to keep the next turn');
    await driver.pressKey('.', { command: true });
    await kept;
    await cli('settings', 'set', 'auto_settle_enabled', 'false');
    const stopped = observer.waitForMessage(data => data.event === 'session_state_changed'
      && data.session?.id === sessionId && data.session.state === 'waiting_input', 'the mock quote turn to finish');
    await request('write_pane', { sessionId, text: 'quote', submit: true });
    await stopped;
    const { text } = await request('read_pane_text', { sessionId });
    const row = text.split('\n').findIndex(line => line.includes('retry wrapper'));
    runner.assert(row >= 0, 'the mock quote is rendered in the terminal');
    const start = await request('get_pane_cell_rect', { sessionId, cell: { col: 2, row } });
    const end = await request('get_pane_cell_rect', { sessionId, cell: { col: 15, row } });
    const from = await relative(start.centerX, start.centerY);
    const to = await relative(end.centerX, end.centerY);
    await request('native_pointer_trace_start');
    await driver.dragWindow(from.x, from.y, to.x, to.y, { modifiers: { option: true } });
    const trace = await request('native_pointer_trace_read');
    runner.assert(trace.events.filter(event => event.type === 'mousemove').slice(1).every(event => event.buttons === 1 && event.alt), 'native Alt-drag retains button and modifier state', { trace });
    const annotation = await request('get_annotation_state');
    runner.assert(annotation.annotations.some(item => item.quote === 'retry wrapper'), 'the full native drag creates the annotation', { annotation });
    await request('capture_native_window_screenshot', { path: path.join(runner.runDir, 'native-annotation.png') });
    await driver.pressKey('Escape');
    await inactive('terminal drag');
  });

  await runner.step('offscreen_input_fails_without_stalling', async () => {
    for (const payload of [{ x: 0.5, y: 1.34 }, { x: 0.5, y: -1 }, { x: 0, y: 0 }]) {
      try {
        await request('native_mouse', { action: 'click', ...payload });
        throw new Error('an invalid native click was accepted');
      } catch (error) {
        runner.assert(String(error).includes('scroll the target into the viewport'), 'invalid input reports its geometry before dispatch', { payload, error: String(error) });
      }
    }
    await driver.pressKey('l', { command: true, shift: true });
    runner.assert((await request('sessions_get_state')).open, 'native keyboard input still works after invalid clicks');
    await driver.pressKey('Escape');
    await inactive('invalid geometry');
  });

  await runner.step('browser_native_focus_without_secure_input', async () => {
    const url = `http://127.0.0.1:${server.address().port}/`;
    await cli('browser', 'open', url, '--session', sessionId);
    await pageLoaded;
    await cli('browser', 'wait', '--using', 'css', '--value', '#password', '--state', 'visible', '--session', sessionId);
    const rect = JSON.parse(await cli('browser', 'command', 'execute_script', '--params', JSON.stringify({ script: 'const r=document.querySelector("#password").getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}', args: [] }), '--session', sessionId));
    const hostSelector = `[data-session-terminal-desktop="${desktop.id}"] .browser-tile-host`;
    await wait(hostSelector);
    const { bounds: host } = await request('dom_bounds', { selector: hostSelector });
    runner.assert(host.width > 0 && host.height > 0, 'the browser target has visible host bounds', { host });
    const target = await relative(host.x + rect.x + rect.width / 2, host.y + rect.y + rect.height / 2);
    await driver.clickWindow(target.x, target.y);
    const { label } = await request('get_browser_focus_state');
    runner.assert(Boolean(label), 'native clicking focuses the browser page');
    await driver.typeText('probe');
    await cli('browser', 'wait', '--using', 'css', '--value', '#password:valid', '--state', 'visible', '--session', sessionId);
    const value = JSON.parse(await cli('browser', 'command', 'execute_script', '--params', JSON.stringify({ script: 'return document.querySelector("#password").value', args: [] }), '--session', sessionId));
    runner.assert(value === 'probe', 'native typing stays in the clicked browser webview', { value });
    const from = await relative(host.x + rect.x + 2, host.y + rect.y + rect.height / 2);
    const to = await relative(host.x + rect.x + rect.width - 2, host.y + rect.y + rect.height / 2);
    await driver.dragWindow(from.x, from.y, to.x, to.y);
    const buttons = JSON.parse(await cli('browser', 'command', 'execute_script', '--params', JSON.stringify({ script: 'return document.body.dataset.buttons', args: [] }), '--session', sessionId));
    runner.assert(buttons === '1', 'browser editable drag moves retain their pressed button', { buttons });
    const native = await request('native_input_state', { window: label });
    runner.assert(native.webviewIsFirstResponder && !native.secureInput, 'a parked password field keeps native secure input off', { native });
    await driver.pressKey('w', { command: true });
    await wait(`[data-session-terminal-desktop="${desktop.id}"] [data-tile-kind="browser"]`, { absent: true });
    await inactive('browser password and native menu close');
  });
  await runner.step('native_settle', async () => {
    const target = await point(`.queue-row[data-session-id="${sessionId}"]`);
    await driver.movePointerInWindow(target.x, target.y);
    await click('[aria-label="Settle Focusless native"]');
    await wait(`[data-testid="queue-turn-${sessionId}"]`, { absent: true });
    const sessions = JSON.parse(await cli('agent', 'list', '--json'));
    runner.assert(sessions.find(session => session.id === sessionId)?.turn_owed === false, 'the native settle is visible through the CLI');
    await inactive('settle');
  });
  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
