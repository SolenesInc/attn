#!/usr/bin/env node
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import { PNG } from 'pngjs';
import { deflateSync } from 'node:zlib';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync, spawn } from 'node:child_process';
import { parseCommonArgs, launchFreshNativeAppAndConnect, printCommonHelp } from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { appPlatform, appCliInTree } from './platform.mjs';
import { MOCK_AGENT_EXECUTABLE, writeMockAgentFixture } from './mockAgent.mjs';
import { instanceCliEnv, instanceForAppPath, dataDirForInstance, captureManifestPathForInstance } from './harnessInstance.mjs';

process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';
process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '0';
const options = parseCommonArgs(process.argv.slice(2));
if (options.help) { printCommonHelp('scripts/real-app-harness/scenario-native-quick-capture.mjs'); process.exit(0); }
assert.equal(process.platform, 'darwin');
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-QUICK-CAPTURE', tier: 'tier1-local-shell', prefix: 'native-quick-capture', allowRealAgents: false,
  metadata: {
    focus: 'The native Quick Capture panel opens on a system-wide shortcut over another app without activating attn, takes system keys, pastes and drops files, sends to a crew member whose agent retrieves the files, lists Recent, keeps its draft across a restart, and refuses shortcuts it cannot hold',
    dropped: {
      quick_capture_walkthrough: 'A hosted-CI recording of steps the scenario already drives; the native entry runs on macOS only outside CI, so the step never ran',
      hidden_idle_cpu: 'A hosted-CI WebKit process sample; the native panel has no WebKit processes. Idle cost belongs to docs/internal/perf-testing.md on the host',
      paste_and_drop_motion: 'The native panel has no flying-file animation, so there is no motion to check',
      editor_font_size: 'The native capture slice reports fontScale, not a CSS font size; the step checks the scale',
      recent_retry_buttons: 'The native Recent rows carry no Retry buttons',
      window_recording: 'The VM records with scripts/vm record; the harness recorder is a host tool',
    },
    launch: 'The app launches without MAC_APP_QUIET: a quiet launch refuses activation for its first seconds, and this scenario activates attn and the fixtures itself, so it runs only on a display it owns',
  },
});
const launchApp = appPlatform.launchApp.bind(appPlatform);
appPlatform.launchApp = async args => {
  if (args.appPath !== options.appPath) return launchApp(args);
  const fd = fs.openSync(args.logPath, 'w');
  try {
    const child = spawn(appPlatform.appExecutableInTree(args.appPath), [], {
      detached: true, stdio: ['ignore', fd, fd],
      env: instanceCliEnv(instanceForAppPath(args.appPath), args.env ?? {}),
    });
    const exited = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })));
    child.unref();
    return { spawned: true, pid: child.pid, child, exited, logPath: args.logPath };
  } finally { fs.closeSync(fd); }
};
const client = new UiAutomationClient({ appPath: options.appPath });
const instance = instanceForAppPath(options.appPath);
assert.ok(instance, 'Quick Capture scenarios require a named instance');
const panel = new UiAutomationClient({ appPath: options.appPath, manifestPath: captureManifestPathForInstance(instance) });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = appPlatform.createWindowDriver({ appPath: options.appPath, systemInput: true });
const fixtureId = `com.attn.capture-fixture.${runner.runId.replace(/[^a-z0-9]/gi, '').toLowerCase()}`;
const fixture = appPlatform.createWindowDriver({ appPath: options.appPath, bundleId: fixtureId });
const otherFixtureId = `${fixtureId}.other`;
const otherFixture = appPlatform.createWindowDriver({ appPath: options.appPath, bundleId: otherFixtureId });
const FIXTURE_SIGNAL_LIMIT_MS = 120_000;
function fixtureSignal(name, action) {
  const file = path.join(runner.runDir, name);
  fs.rmSync(file, { force: true });
  return new Promise((resolve, reject) => {
    const settle = (finish, value) => { clearTimeout(limit); watcher.close(); finish(value); };
    const watcher = fs.watch(runner.runDir, (_, changed) => {
      if (changed === name && fs.existsSync(file)) settle(resolve);
    });
    const limit = setTimeout(() => settle(reject, new Error(`${name} did not appear in ${runner.runDir} within ${FIXTURE_SIGNAL_LIMIT_MS}ms`)), FIXTURE_SIGNAL_LIMIT_MS);
    Promise.resolve(action()).catch(error => settle(reject, error));
  });
}
const PANEL_TITLE = 'Quick Capture';
const MAIN_TITLE = 'attn';
async function screenshot(name) {
  const windowId = await driver.mainWindowId({ windowTitle: PANEL_TITLE });
  assert.ok(windowId, 'Capture window id is required; never capture the whole display');
  await driver.screenshot(path.join(runner.runDir, name), { windowId });
}
const appVisible = async () => !execFileSync('lsappinfo', ['info', '-only', 'hidden', driver.bundleId], { encoding: 'utf8' }).includes('=true');
const testBinding = 'Control+Alt+KeyB';
const runAttn = args => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const mailbox = `cap-${crypto.randomUUID()}`;
const home = path.join(dataDirForInstance(instance), 'crew', mailbox);

const state = (expect, timeoutMs) => panel.request('state', { of: 'capture', ...(expect ? { expect } : {}), ...(timeoutMs ? { timeout_ms: timeoutMs } : {}) }, timeoutMs ? { timeoutMs: timeoutMs + 5_000 } : {});
const stateIncluding = text => panel.request('state', { of: 'capture', includes: text });
const hidden = () => state({ visible: false });
const binding = async () => (await client.request('state', { of: 'capture-shortcut' })).binding;
const bind = value => client.request('run', { name: `capture.shortcut=${value}` });
const dismiss = () => panel.request('run', { name: 'dismiss' });
const key = (name, modifiers = []) => driver.runInputDriver(['global_key', '--key', name, ...(modifiers.length ? ['--modifiers', modifiers.join(',')] : [])]);
const typeText = text => driver.runInputDriver(['global_text', '--text', text]);
const crewRoster = () => JSON.parse(runAttn(['crew', 'list', '--json'])).sort((a, b) => Number(Boolean(b.chief)) - Number(Boolean(a.chief)));
const chiefMailbox = () => crewRoster()[0].id;
const rosterPosition = () => {
  const roster = crewRoster();
  const position = 1 + roster.findIndex(member => member.id === mailbox);
  assert.ok(position >= 1 && position <= 4, `crew member ${mailbox} is at Quick Capture roster position ${position}, beyond ⌘1-⌘4`);
  return position;
};
const pickMailbox = () => key(String(rosterPosition()), ['command']);
const arrowToMailbox = async () => { for (let step = rosterPosition() - 1; step > 0; step--) await key('arrowdown'); };
async function panelPoint(target, at = [0.5, 0.5]) {
  const element = await panel.request('wait', { target });
  const window = await panel.request('find', { target: { key: 'capture' } });
  const [x, y, w, h] = element.rect;
  const [wx, wy, ww, wh] = window.rect;
  return { x: (x + w * at[0] - wx) / ww, y: (y + h * at[1] - wy) / wh };
}
async function clickPanel(target) {
  const { x, y } = await panelPoint(target);
  await driver.runInputDriver(['click', '--window-title', PANEL_TITLE, '--relative-x', String(x), '--relative-y', String(y)]);
}
async function captureStack() {
  const lookup = await driver.runInputDriverCapture(['windowid', '--window-title', PANEL_TITLE]).catch(error => error.message);
  const stack = JSON.parse(await driver.runInputDriverCapture(['windowstack']));
  const index = stack.findIndex(window => String(window.id) === lookup);
  assert.ok(index >= 0, `Capture must be on screen: the window lookup gave ${lookup}; the stack's layer-8 windows are ${JSON.stringify(stack.filter(window => window.layer === 8))}`);
  const capture = stack[index];
  const overlaps = window => window.alpha > 0 && window.x < capture.x + capture.width &&
    window.x + window.width > capture.x && window.y < capture.y + capture.height &&
    window.y + window.height > capture.y;
  const covering = stack.slice(0, index).filter(window => !window.systemUI && overlaps(window));
  runner.writeJson('capture-window-stack.json', { capture, covering, stack });
  assert.deepEqual(covering, [], 'Capture must appear above overlapping application windows, below macOS menu and system UI levels');
  return capture;
}
const openingReceipts = [];
const nativeFindings = [];
const HIDE_DOES_NOT_HOLD = '⌘H must keep attn hidden';
async function hideAttn() {
  await driver.activateApp();
  await driver.runInputDriver(['wait_frontmost']);
  await key('h', ['command']);
  const hid = await driver.runInputDriver(['wait_hidden']).then(() => true, () => false);
  await fixture.activateApp();
  await fixture.runInputDriver(['wait_frontmost']);
  if (hid && !(await appVisible())) return true;
  if (!nativeFindings.some(error => error.message === HIDE_DOES_NOT_HOLD)) nativeFindings.push(new Error(HIDE_DOES_NOT_HOLD));
  return false;
}
async function expectAttnHidden(finding) {
  if (!(await appVisible())) return;
  nativeFindings.push(new Error(finding));
  await hideAttn();
}
fs.writeFileSync(path.join(runner.runDir, 'inspect-capture.mjs'), `
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { execFileSync, spawn } from 'node:child_process';
const attn = process.env.ATTN_WRAPPER_PATH || ${JSON.stringify(appCliInTree(options.appPath))};
const item = JSON.parse(execFileSync(attn, ['agent', 'inbox', process.argv[2], '--json'], { encoding: 'utf8' }));
const receipts = [];
for (const attachment of item.attachments || []) {
  const out = path.join(process.cwd(), attachment.id + path.extname(attachment.name));
  execFileSync(attn, ['agent', 'attachment', item.source_id, attachment.id, '--out', out]);
  const dimensions = attachment.media_type.startsWith('image/') ? execFileSync('sips', ['-g', 'pixelWidth', '-g', 'pixelHeight', out], { encoding: 'utf8' }) : undefined;
  receipts.push({ captureId: item.source_id, attachmentId: attachment.id, name: attachment.name, mediaType: attachment.media_type, bytes: fs.statSync(out).size,
    sha256: crypto.createHash('sha256').update(fs.readFileSync(out)).digest('hex'), dimensions });
}
fs.writeFileSync(${JSON.stringify(path.join(runner.runDir, 'mailbox-image-receipt.json'))}, JSON.stringify(receipts));
console.log('CAPTURE_IMAGE_INSPECTED ' + JSON.stringify(receipts));
`);

runner.writeJson('measurement-conditions.json', { load: execFileSync('uptime', { encoding: 'utf8' }).trim(), machine: execFileSync('sysctl', ['-n', 'hw.model'], { encoding: 'utf8' }).trim(), os: execFileSync('sw_vers', ['-productVersion'], { encoding: 'utf8' }).trim(), endpoint: 'OS HID postedAt to the panel automation reply that it shows with the message field holding the keyboard; not first painted pixel', note: 'Synthetic fixture and mock agents. Background machine work continues.' });
async function openCapture(text = '') {
  const { postedAtMs } = JSON.parse(await driver.runInputDriverCapture(['global_capture', '--key', 'b']));
  const actual = await state({ visible: true, focused: true });
  const focusedAtMs = Date.now();
  if (text) await typeText(text);
  openingReceipts.push({ postedAtMs, focusedAtMs, hotkeyToFocusMs: focusedAtMs - postedAtMs });
  runner.writeJson('opening-latency.json', openingReceipts);
  await captureStack();
  return actual;
}
async function clearDraftFiles() {
  let left = await state({ settled: true }, 120_000);
  while (left.files.length > 0) {
    await panel.request('press', { target: { role: 'button', name: `Remove ${left.files[0].name}` } });
    left = await state({ attachmentCount: left.files.length - 1, settled: true });
  }
  return left;
}
const DROP_KEEPS_KEYS = 'A drop from another app must leave the panel holding the keyboard, as the Tauri panel does';
async function keysAfterDrop(dropped) {
  if (dropped.nativeFocused) return dropped;
  if (!nativeFindings.some(error => error.message === DROP_KEEPS_KEYS)) nativeFindings.push(new Error(DROP_KEEPS_KEYS));
  await clickPanel({ key: 'capture.message' });
  return state({ nativeFocused: true, focused: true });
}
async function dropFiles(files) {
  const manifest = path.join(runner.runDir, 'drag-files.json');
  fs.writeFileSync(manifest, JSON.stringify(files));
  try { await fixture.runInputDriver(['drag_between', '--relative-x', '0.1', '--text', driver.bundleId]); }
  finally { fs.rmSync(manifest); }
}
function screenshotPdf(source, pages) {
  const image = PNG.sync.read(fs.readFileSync(source));
  const rgb = Buffer.alloc(image.width * image.height * 3);
  for (let pixel = 0; pixel < image.width * image.height; pixel++) image.data.copy(rgb, pixel * 3, pixel * 4, pixel * 4 + 3);
  const compressed = deflateSync(rgb);
  const objects = [Buffer.from('<< /Type /Catalog /Pages 2 0 R >>'), Buffer.alloc(0)];
  const kids = [];
  for (let page = 0; page < pages; page++) {
    const pageId = objects.length + 1, contentId = pageId + 1, imageId = pageId + 2;
    kids.push(`${pageId} 0 R`);
    objects.push(Buffer.from(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${image.width} ${image.height}] /Resources << /XObject << /Screenshot ${imageId} 0 R >> >> /Contents ${contentId} 0 R >>`));
    const content = Buffer.from(`q ${image.width} 0 0 ${image.height} 0 0 cm /Screenshot Do Q`);
    objects.push(Buffer.concat([Buffer.from(`<< /Length ${content.length} >>\nstream\n`), content, Buffer.from('\nendstream')]));
    objects.push(Buffer.concat([Buffer.from(`<< /Type /XObject /Subtype /Image /Width ${image.width} /Height ${image.height} /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length ${compressed.length} >>\nstream\n`), compressed, Buffer.from('\nendstream')]));
  }
  objects[1] = Buffer.from(`<< /Type /Pages /Count ${pages} /Kids [${kids.join(' ')}] >>`);
  const chunks = [Buffer.from('%PDF-1.7\n')], offsets = [0];
  let length = chunks[0].length;
  objects.forEach((body, index) => { offsets.push(length); const chunk = Buffer.concat([Buffer.from(`${index + 1} 0 obj\n`), body, Buffer.from('\nendobj\n')]); chunks.push(chunk); length += chunk.length; });
  chunks.push(Buffer.from(`xref\n0 ${objects.length + 1}\n0000000000 65535 f \n${offsets.slice(1).map(offset => `${String(offset).padStart(10, '0')} 00000 n \n`).join('')}trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${length}\n%%EOF\n`));
  return Buffer.concat(chunks);
}
const pdfPath = path.join(runner.runDir, 'screenshot-notes.pdf');
const banner = fileURLToPath(new URL('../../../docs/banner.png', import.meta.url));
fs.writeFileSync(pdfPath, screenshotPdf(banner, 20));
const sha256 = file => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
runner.writeJson('retained-file-workload.json', { name: path.basename(pdfPath), pages: 20, bytes: fs.statSync(pdfPath).size, sha256: sha256(pdfPath), source: 'docs/banner.png' });

let normalFixtureWindow;
let fixtureLaunch;
let otherFixtureLaunch;
runner.registerCleanup('fixture', async () => { if (fixtureLaunch?.pid) await appPlatform.requestQuit({ bundleId: fixtureId }); });
runner.registerCleanup('other-fixture', async () => { if (otherFixtureLaunch?.pid) await appPlatform.requestQuit({ bundleId: otherFixtureId }); });
runner.registerCleanup('observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
try {
  await runner.step('launch_packaged_capture', async () => {
    assert.equal(fs.existsSync(home), false, 'Each run owns a fresh crew fixture home');
    runner.registerCleanup('crew-fixture', async () => {
      const member = JSON.parse(runAttn(['crew', 'list', '--json'])).find(member => member.id === mailbox);
      if (member?.binding_session) {
        runAttn(['handoff', '--session', member.binding_session, '-m', 'Synthetic capture scenario complete']);
        await observer.waitFor(() => !observer.getSession(member.binding_session), 'synthetic crew session closed');
      }
      fs.rmSync(home, { recursive: true, force: true });
      runAttn(['doc', 'delete', 'core/crew', 'members', mailbox]);
      assert.equal(JSON.parse(runAttn(['crew', 'list', '--json'])).some(member => member.id === mailbox), false);
    });
    fs.mkdirSync(home, { recursive: true });
    fs.writeFileSync(path.join(home, 'CHARTER.md'), '# Capture Fixture Builder\n\nInspect synthetic messages and their copied attachments.\n');
    await client.quitApp();
    runAttn(['daemon', 'stop']);
    await launchFreshNativeAppAndConnect(client, observer);
    await panel.waitForManifest(20_000);
    await panel.waitForReady(20_000);
    const previousCodex = runAttn(['settings', 'get', 'codex_executable']).trim();
    runner.registerCleanup('codex-executable', () => runAttn(['settings', 'set', 'codex_executable', previousCodex]));
    runAttn(['settings', 'set', 'codex_executable', MOCK_AGENT_EXECUTABLE]);
    const initialBinding = await binding();
    assert.ok(initialBinding === '' || initialBinding === testBinding, 'This named instance must be Off or retain its own test binding');
    runner.writeJson('initial-binding.json', { binding: initialBinding, testBinding });
    runner.registerCleanup('capture-binding', () => bind(initialBinding));
    await bind(testBinding);
    writeMockAgentFixture(home, { version: 1, turns: [
      { includes: 'This is a new session', actions: [{ type: 'reply', text: 'CAPTURE_CREW_AWAKE', state: 'idle' }] },
      { includes: 'sent through Quick Capture', actions: [
        { type: 'capture', from: 'prompt', pattern: 'attn agent attachment ([0-9a-f-]{36}) ', name: 'capture' },
        { type: 'exec', cmd: process.execPath, args: [path.join(runner.runDir, 'inspect-capture.mjs'), '{{capture}}'] },
        { type: 'reply', text: 'CAPTURE_IMAGE_INSPECTED', state: 'idle' },
      ] },
    ] });
    runAttn(['crew', 'set', mailbox, '--cwd', home, '--agent', 'codex']);
    runAttn(['crew', 'close', mailbox]);
  });
  await runner.step('build_synthetic_foreground_app', async () => {
    const app = path.join(runner.runDir, 'CaptureFixture.app');
    fs.mkdirSync(path.join(app, 'Contents', 'MacOS'), { recursive: true });
    fs.writeFileSync(path.join(app, 'Contents', 'Info.plist'), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${fixtureId}</string><key>CFBundleExecutable</key><string>app</string><key>CFBundleName</key><string>Capture Fixture</string><key>NSHighResolutionCapable</key><true/></dict></plist>`);
    execFileSync('swiftc', [fileURLToPath(new URL('./CaptureFixture.swift', import.meta.url)), '-o', path.join(app, 'Contents', 'MacOS', 'app')]);
    await fixtureSignal('fixture-ready', async () => { fixtureLaunch = await appPlatform.launchApp({ appPath: app, logPath: path.join(runner.runDir, 'fixture.log'), env: { CAPTURE_FIXTURE_DIR: runner.runDir } }); });
    const otherApp = path.join(runner.runDir, 'OtherCaptureFixture.app');
    fs.cpSync(app, otherApp, { recursive: true });
    const otherPlist = path.join(otherApp, 'Contents', 'Info.plist');
    fs.writeFileSync(otherPlist, fs.readFileSync(otherPlist, 'utf8').replace(fixtureId, otherFixtureId));
    await fixtureSignal('other-ready', async () => { otherFixtureLaunch = await appPlatform.launchApp({ appPath: otherApp, logPath: path.join(runner.runDir, 'other-fixture.log'), env: { CAPTURE_FIXTURE_DIR: runner.runDir, CAPTURE_FIXTURE_NO_HOTKEY: '1', CAPTURE_FIXTURE_READY_FILE: 'other-ready' } }); });
    await fixture.activateApp();
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
    normalFixtureWindow = (await fixture.windowList()).find(window => window.name.includes('Capture Fixture'));
    assert.ok(normalFixtureWindow);
    runner.writeJson('normal-fixture-window.json', normalFixtureWindow);
  });
  await runner.step('fresh_process_first_open_paints', async () => {
    await openCapture();
    const left = await clearDraftFiles();
    if (!left.focused) await clickPanel({ key: 'capture.message' });
    await state({ attachmentCount: 0, focused: true });
    await key('a', ['command']);
    await typeText('Track launch checklist');
    const typed = await state({ text: 'Track launch checklist' });
    runner.writeJson('fresh-first-open.json', typed);
    await screenshot('fresh-first-open.png');
    const capture = await captureStack();
    const displayPath = path.join(runner.runDir, 'fresh-first-open-display.png');
    execFileSync('/usr/sbin/screencapture', ['-x', '-R', [capture.x, capture.y, capture.width, capture.height].join(','), displayPath]);
    const pixel = file => {
      const png = PNG.sync.read(fs.readFileSync(file));
      const center = (Math.floor(png.height / 2) * png.width + Math.floor(png.width / 2)) * 4;
      return [...png.data.subarray(center, center + 4)];
    };
    const nativePixel = pixel(path.join(runner.runDir, 'fresh-first-open.png'));
    const displayPixel = pixel(displayPath);
    runner.writeJson('fresh-first-open-pixels.json', { nativePixel, displayPixel });
    assert.equal(nativePixel[3], 255, 'The first opening must paint the opaque composer surface');
    assert.deepEqual(displayPixel.slice(0, 3), nativePixel.slice(0, 3), 'The display must show the composer rather than the origin underneath');
    await key('escape');
    await hidden();
  });
  await runner.step('approved_default_shortcut_opens_panel', async () => {
    await bind('Control+Alt+Super+Space');
    await key('space', ['control', 'option', 'command']);
    const opened = await state({ visible: true, focused: true, text: 'Track launch checklist' });
    runner.writeJson('default-binding.json', { binding: await binding(), ...opened });
    await key('escape'); await hidden();
    await bind(testBinding);
  });
  let attnHides = false;
  await runner.step('hidden_origin_escape_keeps_app_hidden', async () => {
    attnHides = await hideAttn();
    if (!attnHides) return;
    await openCapture();
    const openedWindows = await driver.windowList();
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
    if (openedWindows.some(window => window.name === MAIN_TITLE)) {
      nativeFindings.push(new Error('Opening capture surfaces the hidden main window'));
    }
    runner.writeJson('hidden-open-host.json', { appVisible: await appVisible(), frontmost: await fixture.frontmostBundleId(), windows: openedWindows });
    await key('escape');
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
    await hidden();
    runner.writeJson('hidden-dismiss.json', { appVisible: await appVisible(), frontmost: await fixture.frontmostBundleId(), windows: await driver.windowList() });
    await expectAttnHidden('Escape must restore the app-hidden state, not surface the main window');
  });
  await runner.step('hidden_origin_dismiss_after_switching_apps', async () => {
    if (!attnHides || !(await hideAttn())) return;
    await openCapture();
    await otherFixture.activateApp();
    await otherFixture.runInputDriver(['wait_frontmost']);
    await dismiss();
    assert.equal(await otherFixture.frontmostBundleId(), otherFixtureId);
    await expectAttnHidden('Dismissing after the person switched apps must leave attn hidden');
    await fixture.activateApp();
  });
  await runner.step('deliberate_main_window_switch_stays_visible', async () => {
    await openCapture();
    await driver.activateApp();
    await driver.runInputDriver(['wait_frontmost']);
    const main = (await driver.windowList()).find(window => window.name === MAIN_TITLE);
    assert.ok(main);
    await driver.parkWindow(Math.ceil(main.width * 0.2), { windowTitle: MAIN_TITLE });
    runner.registerCleanup('main-position', () => client.request('set_window_bounds', { logicalBounds: { x: main.x, y: main.y, width: main.width, height: main.height } }));
    await driver.runInputDriver(['click', '--window-title', MAIN_TITLE, '--relative-x', '0.1', '--relative-y', '0.1']);
    await state({ nativeFocused: false });
    await dismiss();
    assert.equal(await driver.frontmostBundleId(), driver.bundleId);
    assert.equal(await appVisible(), true, 'Dismissal must preserve a deliberate switch to the main window');
    await fixture.activateApp();
  });
  await runner.step('reopen_retains_text_and_native_focus', async () => {
    await hidden();
    await openCapture();
    const actual = await state({ text: 'Track launch checklist', focused: true });
    assert.equal(await driver.frontmostBundleId(), fixtureId, 'Nonactivating capture keeps the origin app active');
    runner.writeJson('first-open.json', actual);
    await screenshot('first-open.png');
  });
  await runner.step('native_pointer_keeps_capture_keyboard_focus', async () => {
    const before = await state();
    await clickPanel({ key: 'capture.message' });
    await state({ nativeFocused: true, focused: true });
    await typeText(' after editor click');
    const priorText = before.text + ' after editor click';
    await state({ text: priorText });
    await key('arrowleft');
    await clickPanel({ key: 'capture.recent' });
    await state({ view: 'recent', nativeFocused: true });
    await screenshot('recent-messages.png');
    await key('escape'); await state({ view: 'compose' });
    await typeText(' after Recent');
    await state({ text: priorText.slice(0, -1) + ' after Recent' + priorText.slice(-1) });
    await key('escape'); await hidden();
    await openCapture();
    await key('a', ['command']);
    await typeText(before.text);
    await state({ text: before.text });
  });
  await runner.step('native_undo_and_redo_restore_typing', async () => {
    const before = (await state()).text;
    await key('arrowleft'); await key('arrowright');
    await typeText(' undo me');
    await state({ text: before + ' undo me' });
    await key('z', ['command']);
    await state({ text: before });
    await key('z', ['command', 'shift']);
    await state({ text: before + ' undo me' });
    await key('z', ['command']);
    await state({ text: before });
  });
  await runner.step('panel_surface_drags_the_panel', async () => {
    await clickPanel({ key: 'capture.message' });
    const before = await captureStack();
    const pointer = { x: Math.round(before.width * -0.2), y: Math.round(before.height * -0.23) };
    const from = { x: 0.5, y: 0.02 };
    await driver.runInputDriver(['drag', '--window-title', PANEL_TITLE,
      '--relative-x', String(from.x), '--relative-y', String(from.y),
      '--to-relative-x', String(from.x + pointer.x / before.width), '--to-relative-y', String(from.y + pointer.y / before.height)]);
    const after = await captureStack();
    const moved = { x: after.x - before.x, y: after.y - before.y };
    runner.writeJson('panel-drag.json', { before, after, moved, pointer });
    if (!(Math.abs(moved.x - pointer.x) <= 2 && Math.abs(moved.y - pointer.y) <= 2)) {
      nativeFindings.push(new Error(`Dragging the panel's top edge moves it with the pointer: moved ${JSON.stringify(moved)}, pointer ${JSON.stringify(pointer)}`));
    }
    if (!(await state()).focused) {
      nativeFindings.push(new Error('A press on the panel surface must keep the message field focused'));
      await clickPanel({ key: 'capture.message' });
      await state({ focused: true });
    }
  });
  await runner.step('newline_draft_and_focus_return', async () => {
    await key('enter', ['shift']);
    await typeText('with screenshot');
    await state({ text: 'Track launch checklist\nwith screenshot' });
    await key('w', ['command']);
    await fixture.runInputDriver(['wait_frontmost']);
    await hidden();
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
    await openCapture();
    await state({ text: 'Track launch checklist\nwith screenshot' });
  });
  await runner.step('shared_text_size_shortcuts', async () => {
    await key('0', ['command']);
    const baseline = await state({ fontScale: 1 });
    await key('=', ['command']);
    const enlarged = await state({ fontScale: 1.1, text: baseline.text });
    await screenshot('text-size-increased.png');
    await key('-', ['command']);
    await state({ fontScale: 1 });
    await key('=', ['command']);
    await state({ fontScale: 1.1 });
    await key('0', ['command']);
    await state({ fontScale: 1 });
    await key(',', ['command']);
    const unchanged = await state({ focused: true, text: baseline.text });
    runner.writeJson('shared-font-size.json', { baseline, enlarged, restored: unchanged });
  });
  await runner.step('native_screenshot_paste', async () => {
    await key('escape');
    await fixture.runInputDriver(['wait_frontmost']);
    await hidden();
    await fixture.pressKey('c', { command: true });
    await openCapture();
    await key('v', ['command']);
    const actual = await state({ imageCount: 1, attachmentCount: 1, settled: true });
    runner.writeJson('paste.json', actual);
    await screenshot('pasted-image.png');
  });
  await runner.step('native_file_drop', async () => {
    const dropped = { imageCount: 2, attachmentCount: 2, settled: true };
    await fixture.runInputDriver(['drag_between', '--relative-x', '0.1', '--text', driver.bundleId]);
    const afterPaste = await state(dropped, 10_000).then(() => true, () => false);
    if (!afterPaste) {
      nativeFindings.push(new Error('A file dropped in the opening an image was pasted in must attach'));
      await dismiss();
      await openCapture();
      await fixture.runInputDriver(['drag_between', '--relative-x', '0.1', '--text', driver.bundleId]);
    }
    const actual = await state(dropped);
    runner.writeJson('drop.json', actual);
    await screenshot('dropped-image.png');
  });
  await runner.step('native_pdf_drop', async () => {
    await dropFiles([pdfPath]);
    const actual = await state({ attachmentCount: 3, imageCount: 2, settled: true }, 120_000);
    await keysAfterDrop(actual);
    assert.equal(actual.files[2].name, path.basename(pdfPath));
    runner.writeJson('pdf-drop.json', actual);
    await screenshot('dropped-pdf.png');
  });
  await runner.step('remove_image_and_keyboard_mailbox', async () => {
    const pasted = (await state()).files[0].name;
    await clickPanel({ role: 'button', name: `Remove ${pasted}` });
    const kept = await state({ attachmentCount: 2 });
    assert.ok(!kept.files.some(file => file.name === pasted));
    await pickMailbox();
    await state({ mailbox });
    await key('1', ['command']);
    await state({ mailbox: chiefMailbox() });
    await key('k', ['command']);
    await arrowToMailbox();
    await key('enter');
    await state({ mailbox });
    await fixtureSignal('mailbox-image-receipt.json', () => key('enter'));
    const imageReceipt = JSON.parse(fs.readFileSync(path.join(runner.runDir, 'mailbox-image-receipt.json')));
    assert.equal(imageReceipt.length, 2);
    const pngReceipt = imageReceipt.find(file => file.mediaType.startsWith('image/'));
    const pdfReceipt = imageReceipt.find(file => file.mediaType === 'application/pdf');
    assert.match(pngReceipt.dimensions, /pixelWidth:/);
    assert.equal(pngReceipt.sha256, sha256(path.join(runner.runDir, 'synthetic-screenshot.png')));
    assert.equal(pdfReceipt.sha256, sha256(pdfPath));
    assert.equal(pdfReceipt.bytes, fs.statSync(pdfPath).size);
    await fixture.runInputDriver(['wait_frontmost']);
    await state({ visible: false, text: '', attachmentCount: 0, mailbox: chiefMailbox() });
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
  });
  await runner.step('plain_recent_history', async () => {
    await openCapture();
    await clickPanel({ key: 'capture.recent' });
    await state({ view: 'recent' });
    const actual = await stateIncluding(mailbox);
    const sent = actual.recentRows.find(row => row.mailbox === mailbox && row.files.includes(path.basename(pdfPath)));
    assert.ok(sent, `Recent lists the message sent to ${mailbox}: ${JSON.stringify(actual.recentRows)}`);
    assert.equal(sent.files.length, 2);
    assert.equal(sent.read, true, 'The agent retrieved the message from its inbox before it saved the files');
    runner.writeJson('plain-recent.json', actual);
    await screenshot('plain-recent.png');
    await key('escape');
    await key('escape'); await hidden();
  });
  await runner.step('image_only_capture', async () => {
    await fixture.pressKey('c', { command: true });
    await openCapture();
    assert.equal((await state()).text, '');
    await key('v', ['command']);
    await state({ imageCount: 1, settled: true });
    await screenshot('image-only-capture.png');
    const { captureId } = await state();
    await key('enter');
    await state({ visible: false, attachmentCount: 0 });
    await fixture.runInputDriver(['wait_frontmost']);
    await openCapture();
    await clickPanel({ key: 'capture.recent' });
    await state({ view: 'recent' });
    const recent = await stateIncluding(captureId);
    const imageOnly = recent.recentRows.find(row => row.captureId === captureId);
    assert.deepEqual([imageOnly.text, imageOnly.files], ['', ['Pasted screenshot']], `Recent lists the image-only message: ${JSON.stringify(imageOnly)}`);
    await key('escape'); await key('escape'); await hidden();
  });
  await runner.step('restart_restores_draft_image_and_binding', async () => {
    await openCapture('Retained after restart');
    await fixture.runInputDriver(['drag_between', '--relative-x', '0.1', '--text', driver.bundleId]);
    await dropFiles([pdfPath]);
    const beforeRestart = await state({ imageCount: 1, attachmentCount: 2, settled: true }, 120_000);
    await keysAfterDrop(beforeRestart);
    runner.writeJson('drop-before-restart.json', beforeRestart);
    await typeText(' plus dropped image');
    await state({ text: 'Retained after restart plus dropped image' });
    await key('escape'); await hidden();
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await panel.waitForManifest(20_000);
    await panel.waitForReady(20_000);
    await fixture.activateApp();
    await fixture.runInputDriver(['wait_frontmost']);
    await client.request('state', { of: 'capture-shortcut', expect: { binding: testBinding }, timeout_ms: 10_000 });
    await openCapture();
    const restored = await state({ imageCount: 1, attachmentCount: 2, settled: true, text: 'Retained after restart plus dropped image' }, 120_000);
    assert.equal(restored.files[1].name, path.basename(pdfPath));
    await screenshot('restored-draft.png');
    await pickMailbox(); await state({ mailbox });
    const receipt = fixtureSignal('mailbox-image-receipt.json', () => key('enter'));
    receipt.catch(() => {});
    if (!(await state({ visible: false }, 60_000).then(() => true, () => false))) {
      nativeFindings.push(new Error('A draft restored after a restart that cut its upload short must send; it stays Saving with no error (seed s-k99tv3); the scenario removes the draft to go on'));
      runner.writeJson('restored-send-stuck.json', await state());
      await client.quitApp();
      fs.rmSync(path.join(dataDirForInstance(instance), "quick-capture-drafts"), { recursive: true, force: true });
      await client.launchFreshApp();
      await client.waitForReady(20_000);
      await panel.waitForManifest(20_000);
      await panel.waitForReady(20_000);
      await fixture.activateApp();
      await fixture.runInputDriver(['wait_frontmost']);
      return;
    }
    await receipt;
    const restoredReceipt = JSON.parse(fs.readFileSync(path.join(runner.runDir, 'mailbox-image-receipt.json'))).find(file => file.mediaType === 'application/pdf');
    assert.equal(restoredReceipt.sha256, sha256(pdfPath));
    runner.writeJson('restored-pdf-receipt.json', restoredReceipt);
  });
  await runner.step('repeat_hidden_and_minimized', async () => {
    await hideAttn();
    await openCapture('Hidden app draft');
    await state({ text: 'Hidden app draft' });
    await key('enter');
    await hidden();
    await fixture.runInputDriver(['wait_frontmost']);
    await driver.activateApp();
    await key('m', ['command']);
    await fixture.activateApp();
    await openCapture('Minimized app draft');
    await state({ text: 'Minimized app draft' });
    await key('enter');
    await hidden();
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
  });
  await runner.step('rebind_disable_and_restore', async () => {
    for (const refused of ['KeyA', 'Shift+KeyA']) {
      await assert.rejects(bind(refused), /Use Command, Control or Option/);
      assert.equal(await binding(), testBinding);
    }
    let conflictError;
    try { await bind('Control+Alt+KeyJ'); }
    catch (error) { conflictError = String(error); }
    runner.writeJson('binding-conflict.json', { fixtureOwns: 'Control+Alt+KeyJ', conflictError: conflictError || null, binding: await binding() });
    assert.ok(conflictError, 'Existing exclusive shortcut owner must be rejected');
    assert.match(conflictError, /Previous shortcut remains active/);
    assert.equal(await binding(), testBinding);
    const systemBinding = fs.readFileSync(path.join(runner.runDir, 'system-shortcut.txt'), 'utf8');
    let systemError;
    try { await bind(systemBinding); }
    catch (error) { systemError = String(error); }
    runner.writeJson('system-conflict.json', { rejected: systemBinding, systemError: systemError || null, binding: await binding() });
    assert.ok(systemError, `macOS's own ${systemBinding} must be refused`);
    assert.equal(await binding(), testBinding);
    await openCapture('Conflict recovery');
    await state({ text: 'Conflict recovery' });
    await key('enter');
    await hidden();
    await Promise.all([
      key('b', ['control', 'option']),
      bind('Control+Alt+KeyK'),
    ]);
    await dismiss();
    await hidden();
    await key('k', ['control', 'option']);
    await state({ visible: true });
    await key('escape');
    await hidden();
    await fixture.runInputDriver(['wait_frontmost']);
    await bind('');
    await key('k', ['control', 'option']);
    assert.equal((await state()).visible, false);
    await bind(testBinding);
  });
  await runner.step('dismiss_after_user_changed_apps', async () => {
    await openCapture();
    await otherFixture.activateApp();
    await otherFixture.runInputDriver(['wait_frontmost']);
    await dismiss();
    assert.equal(await otherFixture.frontmostBundleId(), otherFixtureId);
  });
  await runner.step('first_open_with_pointer_on_top_edge_centres_panel', async () => {
    await client.launchFreshApp();
    await client.waitForReady(20_000);
    await panel.waitForManifest(20_000);
    await panel.waitForReady(20_000);
    await fixture.activateApp();
    await fixture.runInputDriver(['wait_frontmost']);
    await client.request('state', { of: 'capture-shortcut', expect: { binding: testBinding }, timeout_ms: 10_000 });
    const edge = JSON.parse(await driver.runInputDriverCapture(['pointer_top_edge']));
    const primary = edge.screens[0];
    assert.equal(edge.pointer.y, primary.y + primary.height, 'macOS reports a pointer pinned to the top row at the screen\'s top edge');
    await openCapture();
    const { visible } = primary;
    const visibleCentre = { x: visible.x + visible.width / 2, y: primary.y + primary.height - visible.y - visible.height / 2 };
    const centred = await driver.runInputDriverCapture(['wait_window_centre', '--window-title', PANEL_TITLE, '--centre', `${visibleCentre.x},${visibleCentre.y}`])
      .then(JSON.parse, error => { throw new Error(`The first opening with the pointer on the top row centres the panel on the main display's visible frame, whose centre is ${JSON.stringify(visibleCentre)}: ${error.message}`); });
    runner.writeJson('pointer-top-edge.json', { edge, visibleCentre, centred });
    await key('escape'); await hidden();
  });
  try {
    await runner.step('fullscreen_foreground_app', async () => {
      await fixture.activateApp();
      await fixtureSignal('fullscreen-entered', () => fixture.pressKey('f', { command: true, control: true }));
      await fixture.runInputDriver(['wait_frontmost']);
      assert.equal(await fixture.frontmostBundleId(), fixtureId);
      await openCapture('Fullscreen capture');
      await state({ text: 'Fullscreen capture' });
      const windows = await fixture.windowList();
      runner.writeJson('fullscreen-fixture-windows.json', windows);
      const fullWindow = windows.find(window => window.name.includes('Capture Fixture'));
      assert.ok(fullWindow && fullWindow.width > normalFixtureWindow.width && fullWindow.height > normalFixtureWindow.height, 'Full-screen origin window remains on the current Space while capture is focused');
      assert.equal(await driver.frontmostBundleId(), fixtureId, 'Capture must stay above the origin fullscreen Space without activating attn');
      await screenshot('fullscreen-capture.png');
      await key('enter');
      await hidden();
      await fixture.runInputDriver(['wait_frontmost']);
      assert.equal(await fixture.frontmostBundleId(), fixtureId);
      await fixtureSignal('fullscreen-exited', () => fixture.pressKey('f', { command: true, control: true }));
    });
  } catch (error) { nativeFindings.push(error); }
  if (nativeFindings.length) throw new AggregateError(nativeFindings, nativeFindings.map(error => error.message).join('; '));

  const result = await state();
  runner.writeJson('capture-results.json', result);
  runner.writeJson('opening-latency.json', openingReceipts);
  await runner.finishSuccess({ hotkeyInput: 'OS HID events, no target PID or app activation', openings: openingReceipts, result });
} catch (error) {
  await screenshot('failure.png').catch(() => {});
  runner.writeJson('failure-state.json', await state().catch(e => ({ error: String(e) })));
  runner.writeJson('failure-capture-windows.json', await driver.windowList().catch(e => ({ error: String(e) })));
  await runner.finishFailure(error);
  process.exitCode = 1;
}
