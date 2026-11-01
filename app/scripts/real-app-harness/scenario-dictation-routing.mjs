#!/usr/bin/env node
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { parseCommonArgs, launchFreshAppAndConnect, printCommonHelp, createSessionAndWaitForInitialPane } from './common.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { appPlatform, appDaemonInTree, createWindowDriver } from './platform.mjs';
import { instanceCliEnv, instanceForAppPath } from './harnessInstance.mjs';

process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';
process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '0';
const options = parseCommonArgs(process.argv.slice(2));
if (options.help) { printCommonHelp('scripts/real-app-harness/scenario-dictation-routing.mjs'); process.exit(0); }
assert.equal(process.platform, 'darwin');
const runner = createScenarioRunner(options, { scenarioId: 'DICTATION-ROUTING', tier: 'tier1-local-shell', prefix: 'dictation-routing', allowRealAgents: false });
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client, systemInput: true });
registerNativeFailureEvidence(runner, { client, driver });
const fixtureId = `com.attn.dictation-fixture.${runner.runId.replace(/[^a-z0-9]/gi, '').toLowerCase()}`;
const fixture = createWindowDriver({ appPath: options.appPath, bundleId: fixtureId });
const instance = instanceForAppPath(options.appPath);
assert.ok(instance, 'Dictation routing requires a named instance');
const runAttnAsync = args => promisify(execFile)(appDaemonInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const testBinding = 'Control+Alt+B';
const state = (payload = {}) => client.request('capture_state', payload);
const hidden = () => state({ visible: false });
const key = (key, modifiers = []) => driver.runInputDriver(['global_key', '--key', key, ...(modifiers.length ? ['--modifiers', modifiers.join(',')] : [])]);
async function openCapture() {
  await driver.runInputDriver(['global_capture', '--key', 'b']);
  return state({ visible: true });
}
function fixtureSignal(name, action) {
  const file = path.join(runner.runDir, name);
  fs.rmSync(file, { force: true });
  return new Promise((resolve, reject) => {
    const watcher = fs.watch(runner.runDir, (_, changed) => {
      if (changed === name && fs.existsSync(file)) { watcher.close(); resolve(); }
    });
    Promise.resolve(action()).catch(error => { watcher.close(); reject(error); });
  });
}
async function waitForPress(count) {
  const { presses } = await client.request('hotkey_wait', { presses: count });
  assert.equal(presses.length, count, 'Each dictation chord routes exactly one toggle press');
  return presses[count - 1];
}

let fixtureLaunch;
runner.registerCleanup('fixture', async () => { if (fixtureLaunch?.pid) await appPlatform.requestQuit({ bundleId: fixtureId }); });
runner.registerCleanup('observer', () => observer.close());
try {
  await runner.step('launch_with_quick_capture_and_an_external_app', async () => {
    await launchFreshAppAndConnect(client, observer);
    const initialEnabled = observer.getSetting('quick_capture_enabled') || 'false';
    runner.registerCleanup('capture-enabled', () => client.request('set_setting', { key: 'quick_capture_enabled', value: initialEnabled }));
    await client.request('set_setting', { key: 'quick_capture_enabled', value: 'false' });
    await client.request('dispatch_shortcut', { shortcutId: 'ui.openSettings' });
    await client.request('settings_select_section', { sectionId: 'experimental' });
    await driver.activateApp();
    const enabled = observer.receive(event => event.event === 'settings_updated' && event.settings?.quick_capture_enabled === 'true', 'Quick Capture enabled in Settings');
    await driver.click({ selector: '[role="switch"][aria-label="Quick Capture"]' });
    await enabled;
    const enabledAt = performance.now();
    const initialBinding = (await state()).binding;
    runner.log('capture bridge answered after Quick Capture was enabled', { ms: Math.round(performance.now() - enabledAt) });
    await driver.click({ selector: '[data-testid="settings-close"]' });
    runner.registerCleanup('capture-binding', () => client.request('capture_binding', { binding: initialBinding }));
    await client.request('capture_binding', { binding: testBinding });
    const app = path.join(runner.runDir, 'DictationFixture.app');
    fs.mkdirSync(path.join(app, 'Contents', 'MacOS'), { recursive: true });
    fs.writeFileSync(path.join(app, 'Contents', 'Info.plist'), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${fixtureId}</string><key>CFBundleExecutable</key><string>app</string><key>CFBundleName</key><string>Dictation Fixture</string><key>NSHighResolutionCapable</key><true/></dict></plist>`);
    execFileSync('swiftc', ['scripts/real-app-harness/CaptureFixture.swift', '-o', path.join(app, 'Contents', 'MacOS', 'app')]);
    await fixtureSignal('fixture-ready', async () => { fixtureLaunch = await appPlatform.launchApp({ appPath: app, logPath: path.join(runner.runDir, 'fixture.log'), env: { CAPTURE_FIXTURE_DIR: runner.runDir, CAPTURE_FIXTURE_NO_HOTKEY: '1' } }); });
  });
  await runner.step('dictation_toggle_routes_only_inside_attn', async () => {
    const dictation = 'Control+Alt+D';
    const hotkeys = (payload = {}) => client.request(payload.binding === undefined ? 'hotkey_status' : 'hotkey_bind', payload);
    const route = status => status.routes.find(route => route.action === 'dictation');
    runner.registerCleanup('dictation-binding', () => hotkeys({ action: 'dictation', binding: '' }));
    await assert.rejects(hotkeys({ action: 'dictation', binding: testBinding }), /already bound to Quick Capture. Previous shortcut remains active/);
    let bareRightOption;
    try { await hotkeys({ action: 'dictation', binding: 'AltRight' }); } catch (error) { bareRightOption = String(error); }
    assert.ok(bareRightOption, 'A bare right-Option key is not a native chord the dispatcher accepts');
    await hotkeys({ action: 'dictation', binding: dictation });

    await fixture.activateApp();
    await fixture.runInputDriver(['wait_frontmost']);
    const outside = await client.request('hotkey_wait', { dictationRegistered: false });
    assert.equal(route(outside).registered, false, 'Dictation stays unregistered while another app owns focus');
    const passedThrough = path.join(runner.runDir, 'key-down.json');
    await fixtureSignal('key-down.json', () => key('d', ['control', 'option']));
    const fixtureKey = JSON.parse(fs.readFileSync(passedThrough, 'utf8'));
    assert.equal(fixtureKey.keyCode, 2, 'The external app receives the dictation chord');
    assert.deepEqual((await hotkeys()).presses, [], 'No dictation press is routed while another app owns focus');

    await driver.activateApp();
    await driver.runInputDriver(['wait_frontmost']);
    assert.equal((await client.request('get_window_bounds')).minimized, false, 'A minimized main window cannot become key, so Dictation would stay off');
    const inside = await client.request('hotkey_wait', { dictationRegistered: true });
    assert.equal(route(inside).registered, true, 'Dictation registers while an attn window is key');
    await key('d', ['control', 'option']);
    const mainPress = await waitForPress(1);

    let pageFocused;
    const focused = new Promise(resolve => { pageFocused = resolve; });
    const page = http.createServer((request, response) => {
      if (request.url === '/focused') { response.end(); pageFocused(); return; }
      response.setHeader('content-type', 'text/html');
      response.end('<title>Dictation browser</title><body><textarea style="width:100%;height:95vh"></textarea><script>document.querySelector("textarea").addEventListener("focus",()=>fetch("/focused"))</script>');
    });
    runner.registerCleanup('dictation-page', () => new Promise(resolve => page.close(resolve)));
    await new Promise(resolve => page.listen(0, '127.0.0.1', resolve));
    const sessionId = await createSessionAndWaitForInitialPane({ client, observer, cwd: runner.runDir, label: 'Dictation browser', agent: 'shell' });
    await runAttnAsync(['browser', 'open', `http://127.0.0.1:${page.address().port}/`, '--session', sessionId]);
    await runAttnAsync(['browser', 'wait', '--using', 'css', '--value', 'textarea', '--state', 'visible', '--timeout', '15000', '--session', sessionId]);
    const browserDesktop = observer.desktopOf(sessionId);
    await observer.setCurrentDesktop(browserDesktop.id);
    const hostSelector = `[data-session-terminal-desktop="${browserDesktop.id}"] .browser-tile-host`;
    await client.request('dom_wait', { selector: hostSelector, timeoutMs: 15_000 });
    await driver.click({ selector: hostSelector, at: { x: 0.5, y: 0.5 } }, { withoutReceipt: 'the browser page takes it' });
    await focused;
    const { label: browserLabel } = await client.request('get_browser_focus_state');
    assert.ok(browserLabel, 'Clicking the page focuses the browser webview');
    await key('d', ['control', 'option']);
    const browserPress = await waitForPress(2);

    await fixture.activateApp();
    await fixture.runInputDriver(['wait_frontmost']);
    await openCapture();
    await key('d', ['control', 'option']);
    const capturePress = await waitForPress(3);
    assert.equal(await driver.frontmostBundleId(), fixtureId, 'Dictation inside Quick Capture does not activate attn');
    await key('escape');
    await hidden();
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(route(await client.request('hotkey_wait', { dictationRegistered: false })).registered, false, 'Dictation unregisters when focus returns to the external app');

    await hotkeys({ action: 'dictation', binding: '' });
    assert.equal(route(await hotkeys()), undefined);
    await openCapture();
    await key('escape');
    await hidden();
    runner.writeJson('dictation-routing.json', { bareRightOption, outside, fixtureKey, inside, mainPress, browserPress, capturePress });
    assert.deepEqual(mainPress, { action: 'dictation', window: 'main', browser: null });
    assert.deepEqual(browserPress, { action: 'dictation', window: 'main', browser: browserLabel });
    assert.deepEqual(capturePress, { action: 'dictation', window: 'capture', browser: null }, 'A browser focused in main does not follow the press into Quick Capture');
  });
  await runner.finishSuccess();
} catch (error) {
  runner.writeJson('failure-hotkeys.json', await client.request('hotkey_status').catch(e => ({ error: String(e) })));
  await runner.finishFailure(error);
  process.exitCode = 1;
}
