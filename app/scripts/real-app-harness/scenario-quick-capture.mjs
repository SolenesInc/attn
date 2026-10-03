#!/usr/bin/env node
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import { PNG } from 'pngjs';
import { deflateSync } from 'node:zlib';
import { promisify } from 'node:util';
import fs from 'node:fs';
import path from 'node:path';
import { execFile, execFileSync, spawn } from 'node:child_process';
import { parseCommonArgs, launchFreshAppAndConnect, printCommonHelp } from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { appPlatform, createWindowDriver } from './platform.mjs';
import { MOCK_AGENT_EXECUTABLE, writeMockAgentFixture } from './mockAgent.mjs';
import { appDaemonInTree } from './platform.mjs';
import { startWindowRecording, ensureWindowRecorder } from './windowRecording.mjs';
import { instanceCliEnv, instanceForAppPath, dataDirForInstance, appLocalDataDirForInstance } from './harnessInstance.mjs';

process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';
process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '0';
const options = parseCommonArgs(process.argv.slice(2));
if (options.help) { printCommonHelp("scripts/real-app-harness/scenario-quick-capture.mjs"); process.exit(0); }
assert.equal(process.platform, 'darwin');
const runner = createScenarioRunner(options, { scenarioId: 'QUICK-CAPTURE', tier: 'tier1-local-shell', prefix: 'quick-capture', allowRealAgents: false });
const launchApp = appPlatform.launchApp.bind(appPlatform);
appPlatform.launchApp = async args => {
  if (args.appPath !== options.appPath) return launchApp(args);
  const fd = fs.openSync(path.join(runner.runDir, 'native-app.log'), 'w');
  try {
    const child = spawn(appPlatform.appExecutableInTree(args.appPath), [], {
      detached: true, stdio: ['ignore', fd, fd],
      env: instanceCliEnv(instanceForAppPath(args.appPath), args.env ?? {}),
    });
    child.unref();
    return { spawned: true, pid: child.pid, child };
  } finally { fs.closeSync(fd); }
};
const client = new UiAutomationClient({ appPath: options.appPath });
// Quit only this named bundle; never escalate through the generic path-matched PID finder.
client.quitApp = async () => {
  const child = client.launch?.child;
  const exit = child && child.exitCode === null && child.signalCode === null
    ? new Promise(resolve => child.once('exit', resolve)) : Promise.resolve();
  await appPlatform.requestQuit({ bundleId: client.bundleId });
  await exit;
  client.launch = null;
};
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath });
const fixtureId = `com.attn.capture-fixture.${runner.runId.replace(/[^a-z0-9]/gi, '').toLowerCase()}`;
const fixture = createWindowDriver({ appPath: options.appPath, bundleId: fixtureId });
const otherFixtureId = `${fixtureId}.other`;
const otherFixture = createWindowDriver({ appPath: options.appPath, bundleId: otherFixtureId });
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
async function screenshot(name) {
  const windowId = await driver.mainWindowId({ windowTitle: 'Quick Capture' });
  assert.ok(windowId, 'Capture window id is required; never capture the whole display');
  await driver.screenshot(path.join(runner.runDir, name), { windowId });
}
const appVisible = () => execFileSync('osascript', ['-e', `tell application "System Events" to get visible of (first application process whose bundle identifier is "${client.bundleId}")`], { encoding: 'utf8' }).trim() === 'true';
const mainTitle = path.basename(options.appPath, '.app');
const testBinding = 'Control+Alt+B';
const instance = instanceForAppPath(options.appPath);
assert.ok(instance, 'Quick Capture scenarios require a named instance');
const runAttn = args => execFileSync(appDaemonInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const recipient = `cap-${crypto.randomUUID()}`;
const home = path.join(dataDirForInstance(instance), 'crew', recipient);
function saveNativeTrace(name) {
  const log = path.join(appLocalDataDirForInstance(instance), 'debug', 'ui-automation-server.log');
  if (fs.existsSync(log)) fs.copyFileSync(log, path.join(runner.runDir, name));
}

// Large-file receipt waits use the native automation server's existing 120s deadline.
const state = (payload = {}) => client.request('capture_state', payload, payload.staged ? { timeoutMs: 120_000 } : {});
const hidden = () => state({ visible: false });
const key = (key, modifiers = []) => driver.runInputDriver(['global_key', '--key', key, ...(modifiers.length ? ['--modifiers', modifiers.join(',')] : [])]);
async function captureStack() {
  const id = await driver.mainWindowId({ windowTitle: 'Quick Capture' });
  const stack = JSON.parse(await driver.runInputDriverCapture(['windowstack']));
  const index = stack.findIndex(window => window.id === id);
  assert.ok(index >= 0, 'Capture must be on screen');
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
fs.writeFileSync(path.join(runner.runDir, 'inspect-capture.mjs'), `
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';
const attn = process.env.ATTN_WRAPPER_PATH || ${JSON.stringify(appDaemonInTree(options.appPath))};
const batch = JSON.parse(execFileSync(attn, ['agent', 'inbox', '--json'], { encoding: 'utf8' }));
const receipts = [];
for (const item of batch.items) {
  if (item.kind !== 'user_message') continue;
  for (const attachment of item.attachments || []) {
    const out = path.join(process.cwd(), attachment.id + path.extname(attachment.name));
    execFileSync(attn, ['agent', 'attachment', item.source_id, attachment.id, '--out', out]);
    const dimensions = attachment.media_type.startsWith('image/') ? execFileSync('sips', ['-g', 'pixelWidth', '-g', 'pixelHeight', out], { encoding: 'utf8' }) : undefined;
    receipts.push({ captureId: item.source_id, attachmentId: attachment.id, name: attachment.name, mediaType: attachment.media_type, bytes: fs.statSync(out).size,
      sha256: crypto.createHash('sha256').update(fs.readFileSync(out)).digest('hex'), dimensions });
  }
}
fs.writeFileSync(${JSON.stringify(path.join(runner.runDir, 'recipient-image-receipt.json'))}, JSON.stringify(receipts));
console.log('CAPTURE_IMAGE_INSPECTED ' + JSON.stringify(receipts));
`);

runner.writeJson('measurement-conditions.json', { load: execFileSync('uptime', { encoding: 'utf8' }).trim(), swap: execFileSync('sysctl', ['-n', 'vm.swapusage'], { encoding: 'utf8' }).trim(), machine: execFileSync('sysctl', ['-n', 'hw.model'], { encoding: 'utf8' }).trim(), os: execFileSync('sw_vers', ['-productVersion'], { encoding: 'utf8' }).trim(), endpoint: 'OS HID postedAt to capture-open editor-focus acknowledgement; not first painted pixel', note: 'Precreated composer, synthetic fixture and fake agents. Background machine work continues.' });
async function openCapture(text = "") {
  const { postedAtMs } = JSON.parse(await driver.runInputDriverCapture(["global_capture", "--key", "b", "--text", text]));
  const actual = await state({ visible: true });
  const latest = actual.latencyMs[actual.latencyMs.length - 1];
  openingReceipts.push({ postedAtMs, ...latest, hotkeyToFocusMs: latest.focusedAt - postedAtMs });
  runner.writeJson('opening-latency.json', openingReceipts);
  await captureStack();
  return actual;
}
async function recordHostedStep(name, action) {
  if (process.env.CI !== 'true') return action();
  const windowId = await driver.mainWindowId({ windowTitle: 'Quick Capture' });
  assert.ok(windowId);
  const output = path.join(runner.runDir, `${name}.mp4`);
  // These visible-window clips use the same 20s duration as scripts/pr-evidence.sh.
  const child = spawn('/usr/sbin/screencapture', ['-x', '-v', '-V', '20', '-l', String(windowId), output], { stdio: ['ignore', 'ignore', 'pipe'] });
  let stderr = '';
  child.stderr.on('data', chunk => { stderr += chunk; });
  const exit = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', (code, signal) => resolve({ code, signal })); });
  let actionError;
  try { await action(); } catch (error) { actionError = error; }
  const result = await exit;
  runner.writeJson(`${name}-recording.json`, { ...result, windowId, output, stderr });
  if (actionError) throw actionError;
  assert.equal(result.code, 0, stderr);
  assert.ok(fs.statSync(output).size > 0, 'Native recording must contain bytes');
}
async function dropFiles(files) {
  const manifest = path.join(runner.runDir, 'drag-files.json');
  fs.writeFileSync(manifest, JSON.stringify(files));
  try { await fixture.runInputDriver(['drag_between', '--relative-x', '0.1', '--text', driver.bundleId]); }
  finally { fs.rmSync(manifest); }
}
function taggedScreenshot(source, index) {
  const text = Buffer.from(`Capture\0${index}`);
  const chunk = Buffer.alloc(text.length + 12);
  chunk.writeUInt32BE(text.length); chunk.write('tEXt', 4); text.copy(chunk, 8);
  let crc = -1;
  for (const byte of chunk.subarray(4, -4)) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
  }
  chunk.writeUInt32BE((crc ^ -1) >>> 0, chunk.length - 4);
  return Buffer.concat([source.subarray(0, -12), chunk, source.subarray(-12)]);
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
fs.writeFileSync(pdfPath, screenshotPdf(path.resolve('../docs/banner.png'), 20));
runner.writeJson('retained-file-workload.json', { name: path.basename(pdfPath), pages: 20, bytes: fs.statSync(pdfPath).size,
  sha256: crypto.createHash('sha256').update(fs.readFileSync(pdfPath)).digest('hex'), source: 'docs/banner.png' });
async function memorySampler() {
  const appPid = client.readManifest().pid;
  const domain = execFileSync('launchctl', ['print', `pid/${appPid}`], { encoding: 'utf8' });
  const originator = domain.match(/originator = (.+)/)?.[1];
  assert.equal(originator, options.appPath);
  const services = [...domain.matchAll(/^\s+(\d+)\s+-\s+(com\.apple\.WebKit\.\S+)/gm)]
    .map(([, pid, name]) => ({ pid: Number(pid), name })).filter(service => service.pid > 0);
  assert.ok(services.length);
  const pids = [appPid, ...services.map(service => service.pid)], samples = [];
  let pending;
  const sample = () => pending ??= promisify(execFile)('ps', ['-o', 'pid=,rss=', '-p', pids.join(',')])
    .then(({ stdout }) => {
      const rows = stdout.trim().split('\n').map(row => row.trim().split(/\s+/).map(Number));
      const byPid = Object.fromEntries(rows);
      samples.push({ at: Date.now(), appKiB: byPid[appPid], webKitKiB: services.reduce((sum, { pid }) => sum + (byPid[pid] ?? 0), 0), byPid });
    }).finally(() => { pending = undefined; });
  await sample();
  // RSS samples are lower bounds on transient peaks; completion is awaited through upload receipts.
  const interval = setInterval(sample, 50);
  return { stop: async () => {
    clearInterval(interval); await pending; await sample();
    return { appPid, originator, services, sampleIntervalMs: 50, samples,
      baselineWebKitKiB: samples[0].webKitKiB, peakWebKitKiB: Math.max(...samples.map(row => row.webKitKiB)),
      peakAppKiB: Math.max(...samples.map(row => row.appKiB)) };
  } };
}
let normalFixtureWindow;
let fixtureLaunch;
let otherFixtureLaunch;
let recording;
runner.registerCleanup('fixture', async () => { if (fixtureLaunch?.pid) await appPlatform.requestQuit({ bundleId: fixtureId }); });
runner.registerCleanup('other-fixture', async () => { if (otherFixtureLaunch?.pid) await appPlatform.requestQuit({ bundleId: otherFixtureId }); });
runner.registerCleanup('observer', () => observer.close());
// Leave this named instance available for the experience check.
try {
  await runner.step('launch_packaged_capture', async () => {
    assert.equal(fs.existsSync(home), false, 'Each run owns a fresh crew fixture home');
    runner.registerCleanup('crew-fixture', async () => {
      const member = JSON.parse(runAttn(['crew', 'list', '--json'])).find(member => member.id === recipient);
      if (member?.binding_session) {
        runAttn(['handoff', '--session', member.binding_session, '--sleep', '-m', 'Synthetic capture scenario complete']);
        await observer.waitFor(() => !observer.getSession(member.binding_session), 'synthetic crew session closed');
      }
      fs.rmSync(home, { recursive: true, force: true });
      runAttn(['doc', 'delete', 'core/crew', 'members', recipient]);
      assert.equal(JSON.parse(runAttn(['crew', 'list', '--json'])).some(member => member.id === recipient), false);
    });
    fs.mkdirSync(home, { recursive: true });
    fs.writeFileSync(path.join(home, 'CHARTER.md'), '# Capture Fixture Builder\n\nInspect synthetic captures and their copied attachments.\n');
    await client.quitApp();
    runAttn(['daemon', 'stop']);
    const previousDraft = path.join(dataDirForInstance(instance), 'capture-draft.json');
    if (fs.existsSync(previousDraft)) {
      const images = path.join(dataDirForInstance(instance), 'capture-draft-images');
      const backup = path.join(runner.runDir, 'previous-draft-images');
      const metadata = path.join(runner.runDir, 'previous-synthetic-draft.json');
      if (fs.existsSync(images)) fs.cpSync(images, backup, { recursive: true });
      fs.renameSync(previousDraft, metadata);
      runner.registerCleanup('previous-draft', async () => {
        await client.quitApp();
        fs.rmSync(images, { recursive: true, force: true });
        if (fs.existsSync(backup)) fs.cpSync(backup, images, { recursive: true });
        fs.copyFileSync(metadata, previousDraft);
      });
    }
    await launchFreshAppAndConnect(client, observer, { agentExecutables: { codex: MOCK_AGENT_EXECUTABLE, claude: MOCK_AGENT_EXECUTABLE } });
    const initialBinding = (await state()).binding;
    assert.ok(initialBinding === '' || initialBinding === testBinding, 'This named instance must be Off or retain its own test binding');
    runner.writeJson('initial-binding.json', { binding: initialBinding, testBinding });
    runner.registerCleanup('capture-binding', () => client.request('capture_binding', { binding: initialBinding }));
    await client.request('capture_binding', { binding: testBinding });
    writeMockAgentFixture(home, { version: 1, turns: [
      { includes: 'You have been woken', actions: [{ type: 'reply', text: 'CAPTURE_CREW_AWAKE', state: 'idle' }] },
      { includes: '📬 You have unread items', actions: [
        { type: 'exec', cmd: process.execPath, args: [path.join(runner.runDir, 'inspect-capture.mjs')] },
        { type: 'reply', text: 'CAPTURE_IMAGE_INSPECTED', state: 'idle' },
      ] },
    ] });
    runAttn(['crew', 'set', recipient, '--cwd', home, '--agent', 'codex']);
    runAttn(['crew', 'sleep', recipient]);
  });
  await runner.step('build_synthetic_foreground_app', async () => {
    const app = path.join(runner.runDir, 'CaptureFixture.app');
    fs.mkdirSync(path.join(app, 'Contents', 'MacOS'), { recursive: true });
    fs.writeFileSync(path.join(app, 'Contents', 'Info.plist'), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${fixtureId}</string><key>CFBundleExecutable</key><string>app</string><key>CFBundleName</key><string>Capture Fixture</string><key>NSHighResolutionCapable</key><true/></dict></plist>`);
    execFileSync('swiftc', ['scripts/real-app-harness/CaptureFixture.swift', '-o', path.join(app, 'Contents', 'MacOS', 'app')]);
    await fixtureSignal("fixture-ready", async () => { fixtureLaunch = await appPlatform.launchApp({ appPath: app, logPath: path.join(runner.runDir, 'fixture.log'), env: { CAPTURE_FIXTURE_DIR: runner.runDir } }); });
    const otherApp = path.join(runner.runDir, 'OtherCaptureFixture.app');
    fs.cpSync(app, otherApp, { recursive: true });
    const otherPlist = path.join(otherApp, 'Contents', 'Info.plist');
    fs.writeFileSync(otherPlist, fs.readFileSync(otherPlist, 'utf8').replace(fixtureId, otherFixtureId));
    await fixtureSignal('other-ready', async () => { otherFixtureLaunch = await appPlatform.launchApp({ appPath: otherApp, logPath: path.join(runner.runDir, 'other-fixture.log'), env: { CAPTURE_FIXTURE_DIR: runner.runDir, CAPTURE_FIXTURE_NO_HOTKEY: '1', CAPTURE_FIXTURE_READY_FILE: 'other-ready' } }); });
    await fixture.activateApp();
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
    normalFixtureWindow = (await fixture.windowList()).find(window => window.name.includes("Capture Fixture"));
    assert.ok(normalFixtureWindow);
    runner.writeJson("normal-fixture-window.json", normalFixtureWindow);
  });
  await runner.step('fresh_process_first_open_paints', async () => {
    await openCapture('Track launch checklist');
    const beforeFrame = await state();
    assert.equal(beforeFrame.text, 'Track launch checklist', 'The first-ever opening must accept immediate native typing');
    runner.writeJson('fresh-first-open-before-frame.json', beforeFrame);
    await screenshot('fresh-first-open-before-frame.png');
    const actual = await state({ frame: true });
    runner.writeJson('fresh-first-open.json', actual);
    await screenshot('fresh-first-open.png');
    const appPid = client.readManifest().pid;
    const domain = execFileSync('launchctl', ['print', `pid/${appPid}`], { encoding: 'utf8' });
    const originator = domain.match(/originator = (.+)/)?.[1];
    assert.equal(originator, options.appPath, 'Idle measurements must belong to this named app');
    const services = [...domain.matchAll(/^\s+(\d+)\s+-\s+(com\.apple\.WebKit\.\S+)/gm)]
      .map(([, pid, name]) => ({ pid: Number(pid), name })).filter(service => service.pid > 0);
    assert.ok(services.length, 'The app bootstrap domain must identify its WebKit processes');
    runner.writeJson('fresh-first-open-ownership.json', { appPid, originator, services });
    // The failing occluded panel had lost its backing by 35 seconds; observe beyond that receipt.
    const idleSeconds = 40;
    const idle = execFileSync('top', ['-l', '2', '-s', String(idleSeconds), '-stats', 'pid,command,cpu,mem',
      ...[appPid, ...services.map(service => service.pid)].flatMap(pid => ['-pid', String(pid)])], { encoding: 'utf8' });
    fs.writeFileSync(path.join(runner.runDir, 'fresh-first-open-idle.txt'), idle);
    runner.writeJson('fresh-first-open-after-idle.json', { idleSeconds, ...await state({ frame: true }) });
    await screenshot('fresh-first-open-after-idle.png');
    const capture = await captureStack();
    const displayPath = path.join(runner.runDir, 'fresh-first-open-display.png');
    execFileSync('/usr/sbin/screencapture', ['-x', '-R', [capture.x, capture.y, capture.width, capture.height].join(','), displayPath]);
    const pixel = file => {
      const png = PNG.sync.read(fs.readFileSync(file));
      const center = (Math.floor(png.height / 2) * png.width + Math.floor(png.width / 2)) * 4;
      return [...png.data.subarray(center, center + 4)];
    };
    const nativePixel = pixel(path.join(runner.runDir, 'fresh-first-open-after-idle.png'));
    const displayPixel = pixel(displayPath);
    runner.writeJson('fresh-first-open-pixels.json', { nativePixel, displayPixel });
    assert.equal(nativePixel[3], 255, 'The first opening must paint the opaque composer surface');
    assert.deepEqual(displayPixel.slice(0, 3), nativePixel.slice(0, 3), 'The display must show the composer rather than the origin underneath');
    await key('escape');
    await hidden();
  });
  await runner.step('hidden_origin_escape_keeps_app_hidden', async () => {
    await driver.activateApp();
    await driver.runInputDriver(['wait_frontmost']);
    await key('h', ['command']);
    await driver.runInputDriver(['wait_hidden']);
    await fixture.activateApp();
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(appVisible(), false);
    await openCapture();
    const openedWindows = await driver.windowList();
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
    if (openedWindows.some(window => window.name === mainTitle)) {
      nativeFindings.push(new Error('Opening capture surfaces the hidden main window'));
    }
    runner.writeJson('hidden-open-host.json', { appVisible: appVisible(), frontmost: await fixture.frontmostBundleId(), windows: openedWindows });
    await key('escape');
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
    assert.equal((await hidden()).visible, false);
    assert.equal(appVisible(), false, 'Escape must restore the app-hidden state, not surface the main window');
    runner.writeJson('hidden-dismiss.json', { appVisible: appVisible(), frontmost: await fixture.frontmostBundleId(), windows: await driver.windowList() });
  });
  await runner.step('hidden_origin_dismiss_after_switching_apps', async () => {
    assert.equal(appVisible(), false);
    await openCapture();
    await otherFixture.activateApp();
    await otherFixture.runInputDriver(['wait_frontmost']);
    await client.request('capture_dismiss');
    assert.equal(await otherFixture.frontmostBundleId(), otherFixtureId);
    assert.equal(appVisible(), false);
    await fixture.activateApp();
  });
  await runner.step('deliberate_main_window_switch_stays_visible', async () => {
    await openCapture();
    await driver.activateApp();
    await driver.runInputDriver(['wait_frontmost']);
    const main = (await driver.windowList()).find(window => window.name === mainTitle);
    assert.ok(main);
    // Keep the click in the exposed half of this instance's right-hand strip.
    await driver.parkWindow(Math.ceil(main.width * 0.2), { windowTitle: mainTitle });
    runner.registerCleanup('main-position', () => execFileSync('osascript', ['-e',
      `tell application "System Events" to tell (first application process whose bundle identifier is "${client.bundleId}") to set position of first window to {${main.x}, ${main.y}}`]));
    await driver.clickWindow(0.1, 0.1, { windowTitle: mainTitle });
    assert.equal((await state()).nativeFocused, false, 'The deliberate main-window click must transfer native key focus');
    await client.request('capture_dismiss');
    assert.equal(await driver.frontmostBundleId(), driver.bundleId);
    assert.equal(appVisible(), true, 'Dismissal must preserve a deliberate switch to the main window');
    await fixture.activateApp();
  });
  await runner.step('reopen_retains_text_and_native_focus', async () => {
    assert.equal((await hidden()).visible, false);
    await openCapture();
    const actual = await state();
    assert.equal(actual.text, 'Track launch checklist');
    assert.equal(actual.focused, true);
    assert.equal(await driver.frontmostBundleId(), fixtureId, 'Nonactivating capture keeps the origin app active');
    runner.writeJson('first-open.json', actual);
    await screenshot('first-open.png');
    const windowId = await driver.mainWindowId({ windowTitle: 'Quick Capture' });
    if (process.env.CI !== 'true') {
      recording = startWindowRecording({ windowId, outputPath: path.join(runner.runDir, 'capture-drive.mp4'), command: await ensureWindowRecorder() });
    }
  });
  await runner.step('native_pointer_keeps_capture_keyboard_focus', async () => {
    const before = await state();
    const editor = before.controls.editor;
    await driver.clickWindow(editor.x, editor.y, { windowTitle: 'Quick Capture' });
    assert.equal((await state()).nativeFocused, true, 'Clicking the editor keeps the panel key');
    await driver.runInputDriver(['global_text', '--text', ' after editor click']);
    assert.equal((await state()).text, before.text + ' after editor click');
    await key('arrowleft');
    const recent = (await state()).controls.recent;
    await driver.clickWindow(recent.x, recent.y, { windowTitle: 'Quick Capture' });
    assert.equal((await state({ view: 'recent' })).nativeFocused, true, 'Clicking Recent keeps the panel key');
    await screenshot('recent-captures.png');
    await key('escape'); await state({ view: 'compose' });
    await driver.runInputDriver(['global_text', '--text', ' after Recent']);
    const priorText = before.text + ' after editor click';
    assert.equal((await state()).text, priorText.slice(0, -1) + ' after Recent' + priorText.slice(-1), 'Recent preserves the note caret');
    await key('escape'); await hidden();
    await openCapture();
    await key('a', ['command']);
    await driver.runInputDriver(['global_text', '--text', before.text]);
  });
  await runner.step('newline_draft_and_focus_return', async () => {
    await key('enter', ['shift']);
    await driver.runInputDriver(['global_text', '--text', 'with screenshot']);
    assert.equal((await state()).text, 'Track launch checklist\nwith screenshot');
    await key('w', ['command']);
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal((await hidden()).visible, false);
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
    await openCapture();
    assert.equal((await state()).text, 'Track launch checklist\nwith screenshot');
  });
  await runner.step('shared_text_size_shortcuts', async () => {
    await key('0', ['command']);
    const baseline = await state({ fontScale: 1 });
    await key('=', ['command']);
    const enlarged = await state({ fontScale: 1.1 });
    assert.ok(parseFloat(enlarged.editorFontSize) > parseFloat(baseline.editorFontSize));
    assert.equal(enlarged.text, baseline.text);
    await screenshot('text-size-increased.png');
    await key('-', ['command']);
    assert.equal((await state({ fontScale: 1 })).editorFontSize, baseline.editorFontSize);
    await key('=', ['command']);
    await state({ fontScale: 1.1 });
    await key('0', ['command']);
    assert.equal((await state({ fontScale: 1 })).editorFontSize, baseline.editorFontSize);
    await key(',', ['command']);
    const unchanged = await state();
    assert.equal(unchanged.focused, true, 'Cmd-comma leaves the note focused');
    assert.equal(unchanged.text, baseline.text);
    runner.writeJson('shared-font-size.json', { baseline, enlarged, restored: await state() });
  });
  await runner.step('native_screenshot_paste', async () => {
    await key('escape');
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal((await hidden()).visible, false);
    await fixture.pressKey('c', { command: true });
    await openCapture();
    await key('v', ['command']);
    const actual = await client.request('capture_state', { imageCount: 1, settled: true });
    assert.equal(actual.images.length, 1);
    if (!actual.reducedMotion) {
      assert.ok(actual.motion.some(event => event.kind === 'image' && event.phase === 'start'));
      assert.ok(actual.motion.some(event => event.kind === 'image' && event.phase === 'end'));
    }
    assert.equal(actual.flyingImages, 0, 'Paste removes its temporary flying preview');
    if (!actual.reducedMotion) {
      const flight = actual.motion.find(event => event.kind === 'image' && event.phase === 'start');
      assert.equal(flight.source.kind, 'paste');
      assert.ok(flight.source.x !== flight.target.x || flight.source.y !== flight.target.y, 'Paste visibly travels into its slot');
    }
    assert.equal(actual.activeAnimations, 0, 'Attachment motion finishes; no perpetual animation');
    runner.writeJson('paste-motion.json', actual.motion);
    await screenshot('pasted-image.png');
  });
  await runner.step('native_file_drop', async () => {
    await fixture.runInputDriver(['drag_between', '--relative-x', '0.1', '--text', driver.bundleId]);
    const actual = await client.request('capture_state', { imageCount: 2, settled: true });
    assert.equal(actual.images.length, 2);
    if (!actual.reducedMotion) {
      assert.equal(actual.motion.filter(event => event.kind === 'image' && event.phase === 'end').length, 2);
      assert.ok(actual.motion.some(event => event.kind === 'drop' && event.phase === 'start'));
    }
    assert.equal(actual.flyingImages, 0, 'Drop removes its temporary flying preview');
    if (!actual.reducedMotion) {
      const flight = actual.motion.find(event => event.kind === 'image' && event.phase === 'start' && event.source?.kind === 'drop');
      const window = (await driver.windowList()).find(window => window.name.includes('Quick Capture'));
      // Wry truncates AppKit coordinates; the native capture height is 391 points.
      assert.equal(flight.source.x, Math.trunc(window.width / 2), 'Native drop flight starts at the known release point in CSS pixels');
      assert.equal(flight.source.y, Math.trunc(window.height / 2), 'Retina conversion does not displace the release point');
      assert.ok(flight.source.x !== flight.target.x || flight.source.y !== flight.target.y);
    }
    assert.equal(actual.activeAnimations, 0, 'Drop motion finishes; no perpetual animation');
    runner.writeJson('drop-motion.json', actual.motion);
    await screenshot('dropped-image.png');
  });
  await runner.step('native_pdf_drop', () => recordHostedStep('pdf-drop', async () => {
    await dropFiles([pdfPath]);
    const actual = await state({ attachmentCount: 3, imageCount: 2, settled: true, staged: true });
    assert.equal(actual.images[2].name, path.basename(pdfPath));
    assert.equal(actual.nativeFocused, true, 'PDF drop keeps keyboard focus in the note');
    assert.equal(actual.flyingImages, 0);
    assert.equal(actual.activeAnimations, 0);
    runner.writeJson('pdf-drop.json', actual);
    await screenshot('dropped-pdf.png');
  }));
  await runner.step('remove_image_and_keyboard_recipient', async () => {
    const remove = (await state()).controls.remove;
    await driver.clickWindow(remove.x, remove.y, { windowTitle: 'Quick Capture' });
    assert.equal((await state()).images.length, 2);
    await key('2', ['command']);
    assert.equal((await state({ recipient })).recipient, recipient);
    await key('1', ['command']);
    assert.equal((await state({ recipient: 'chief' })).recipient, 'chief');
    await key('k', ['command']);
    await key('arrowdown');
    await key('enter');
    assert.equal((await state({ recipient })).recipient, recipient);
    await fixtureSignal('recipient-image-receipt.json', () => key('enter'));
    const imageReceipt = JSON.parse(fs.readFileSync(path.join(runner.runDir, 'recipient-image-receipt.json')));
    assert.equal(imageReceipt.length, 2);
    const pngReceipt = imageReceipt.find(file => file.mediaType.startsWith('image/'));
    const pdfReceipt = imageReceipt.find(file => file.mediaType === 'application/pdf');
    assert.match(pngReceipt.dimensions, /pixelWidth:/);
    assert.equal(pngReceipt.sha256, crypto.createHash('sha256').update(fs.readFileSync(path.join(runner.runDir, 'synthetic-screenshot.png'))).digest('hex'));
    assert.equal(pdfReceipt.sha256, crypto.createHash('sha256').update(fs.readFileSync(pdfPath)).digest('hex'));
    assert.equal(pdfReceipt.bytes, fs.statSync(pdfPath).size);
    await fixture.runInputDriver(['wait_frontmost']);
    const actual = await hidden();
    assert.equal(actual.saved.length, 1);
    assert.equal(actual.saved[0].recipient, recipient);
    assert.equal(actual.saved[0].images.length, 2);
    assert.equal(actual.visible, false);
    assert.equal(actual.text, '');
    assert.equal(actual.recipient, 'chief');
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
  });
  await runner.step('plain_recent_history', async () => {
    await openCapture();
    const recent = (await state()).controls.recent;
    await driver.clickWindow(recent.x, recent.y, { windowTitle: 'Quick Capture' });
    await recordHostedStep('plain-recent', async () => {
    const actual = await state({ view: 'recent', recentText: path.basename(pdfPath) });
    assert.ok(actual.recentRows.some(row => row.text.includes('Read') && row.text.includes(path.basename(pdfPath))));
    assert.ok(actual.recentRows.every(row => row.buttons.every(button => button === 'Retry image')));
    runner.writeJson('plain-recent.json', actual);
    await screenshot('plain-recent.png');
    });
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
    await key('enter');
    const accepted = await hidden();
    assert.equal(accepted.saved[0].text, '');
    assert.equal(accepted.saved[0].images.length, 1);
    assert.equal(accepted.images.length, 0);
    await fixture.runInputDriver(['wait_frontmost']);
  });
  await runner.step('restart_restores_draft_image_and_binding', async () => {
    await openCapture('Retained after restart');
    await fixture.runInputDriver(['drag_between', '--relative-x', '0.1', '--text', driver.bundleId]);
    await dropFiles([pdfPath]);
    const beforeRestart = await state({ imageCount: 1, attachmentCount: 2, settled: true, staged: true });
    runner.writeJson('drop-before-restart.json', beforeRestart);
    assert.equal(beforeRestart.nativeFocused, true, 'Native panel must retain keyboard focus after a file drop');
    await driver.runInputDriver(['global_text', '--text', ' plus dropped image']);
    assert.equal((await state()).text, 'Retained after restart plus dropped image');
    saveNativeTrace('native-before-restart.log');
    if (recording) { const receipt = await recording.stop(); recording = null; runner.writeJson('recording-result.json', receipt); assert.equal(receipt.failure, null); }
    await key('escape'); await hidden();
    await client.launchFreshApp();
    await client.waitForFrontendResponsive();
    await openCapture();
    const restored = await state({ imageCount: 1, attachmentCount: 2, settled: true, staged: true });
    assert.equal(restored.images[1].name, path.basename(pdfPath));
    assert.equal(restored.text, 'Retained after restart plus dropped image');
    assert.equal(restored.binding, testBinding);
    await screenshot('restored-draft.png');
    await key('2', ['command']); await state({ recipient });
    await fixtureSignal('recipient-image-receipt.json', () => key('enter')); await hidden();
    const restoredReceipt = JSON.parse(fs.readFileSync(path.join(runner.runDir, 'recipient-image-receipt.json'))).find(file => file.mediaType === 'application/pdf');
    assert.equal(restoredReceipt.sha256, crypto.createHash('sha256').update(fs.readFileSync(pdfPath)).digest('hex'));
    runner.writeJson('restored-pdf-receipt.json', restoredReceipt);
  });
  await runner.step('repeat_hidden_and_minimized', async () => {
    await driver.activateApp();
    await driver.runInputDriver(['wait_frontmost']);
    await key('h', ['command']);
    await driver.runInputDriver(['wait_hidden']);
    await fixture.activateApp();
    await openCapture('Hidden app draft');
    assert.equal((await state()).text, 'Hidden app draft');
    await key('enter');
    await hidden();
    await fixture.runInputDriver(['wait_frontmost']);
    await driver.activateApp();
    await key('m', ['command']);
    await fixture.activateApp();
    await openCapture('Minimized app draft');
    assert.equal((await state()).text, 'Minimized app draft');
    await key('enter');
    await hidden();
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
  });
  await runner.step('rebind_disable_and_restore', async () => {
    for (const binding of ['KeyA', 'Shift+KeyA']) {
      await assert.rejects(client.request('capture_binding', { binding }), /Use Command, Control or Option/);
      assert.equal((await state()).binding, testBinding);
    }
    let conflictError;
    try { await client.request('capture_binding', { binding: 'Control+Alt+J' }); }
    catch (error) { conflictError = String(error); }
    runner.writeJson('binding-conflict.json', { fixtureOwns: 'Control+Alt+J', conflictError: conflictError || null, state: await state() });
    assert.ok(conflictError, 'Existing exclusive shortcut owner must be rejected');
    assert.match(conflictError, /Previous shortcut remains active/);
    assert.equal((await state()).binding, testBinding);
    const systemBinding = fs.readFileSync(path.join(runner.runDir, 'system-shortcut.txt'), 'utf8');
    await assert.rejects(client.request('capture_binding', { binding: systemBinding }), /macOS already uses this system shortcut/);
    assert.equal((await state()).binding, testBinding);
    runner.writeJson('system-conflict.json', { rejected: systemBinding, previousBindingRetained: true });
    await openCapture('Conflict recovery');
    assert.equal((await state()).text, 'Conflict recovery', 'Previous shortcut still accepts native typing after rejected replacements');
    await key('enter');
    await hidden();
    await Promise.all([
      key('b', ['control', 'option']),
      client.request('capture_binding', { binding: 'Control+Alt+K' }),
    ]);
    await client.request('capture_dismiss');
    await key('k', ['control', 'option']);
    assert.equal((await state({ visible: true })).visible, true);
    await key('escape');
    await hidden();
    await fixture.runInputDriver(['wait_frontmost']);
    await client.request('capture_binding', { binding: '' });
    await key('k', ['control', 'option']);
    assert.equal((await hidden()).visible, false);
    await client.request('capture_binding', { binding: testBinding });
  });
  await runner.step('dismiss_after_user_changed_apps', async () => {
    await openCapture();
    await otherFixture.activateApp();
    await otherFixture.runInputDriver(['wait_frontmost']);
    await client.request('capture_dismiss');
    assert.equal(await otherFixture.frontmostBundleId(), otherFixtureId);
  });
  try { await runner.step('fullscreen_foreground_app', async () => {
    await fixture.activateApp();
    await fixtureSignal('fullscreen-entered', () => fixture.pressKey('f', { command: true, control: true }));
    await fixture.runInputDriver(['wait_frontmost']);
    assert.equal(await fixture.frontmostBundleId(), fixtureId);
    await openCapture('Fullscreen capture');
    assert.equal((await state()).text, 'Fullscreen capture');
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
  await runner.step('measure_attachment_batches', async () => {
    const source = path.resolve('../docs/banner.png');
    const files = Array.from({ length: 20 }, (_, index) => path.join(runner.runDir, `large-screenshot-${String(index + 1).padStart(2, '0')}.png`));
    const original = fs.readFileSync(source);
    files.forEach((file, index) => fs.writeFileSync(file, taggedScreenshot(original, index)));
    const { width, height } = PNG.sync.read(fs.readFileSync(source));
    const measurements = [];
    runner.writeJson('attachment-batch-fixture.json', { files: files.map(file => ({ name: path.basename(file), bytes: fs.statSync(file).size, sha256: crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex') })),
      width, height, sha256: crypto.createHash('sha256').update(fs.readFileSync(source)).digest('hex'),
      note: 'Each PNG has an individual tEXt identifier so data URLs are distinct; screenshot pixels are unchanged.',
      machine: execFileSync('sysctl', ['-n', 'hw.model'], { encoding: 'utf8' }).trim(),
      os: execFileSync('sw_vers', ['-productVersion'], { encoding: 'utf8' }).trim(), build: runAttn(['--version']).trim() });
    for (const batchSize of [1, 2, 4, 8]) {
      await client.launchFreshApp(); await client.waitForFrontendResponsive();
      await openCapture();
      assert.equal((await state()).images.length, 0, 'Each capacity starts with a fresh process and empty draft');
      await state({ batchSize });
      const sampler = await memorySampler();
      try {
        await dropFiles(files);
        const previews = await state({ attachmentCount: files.length, imageCount: files.length });
        const ingestion = previews.ingestion.at(-1);
        assert.equal(ingestion.count, files.length);
        assert.ok(ingestion.readyAt >= ingestion.startedAt);
        assert.deepEqual(previews.images.map(file => file.name), files.map(file => path.basename(file)));
        const staged = await state({ staged: true });
        assert.equal(staged.work.active, 0); assert.equal(staged.work.pending, 0);
        assert.equal(staged.work.peak, batchSize);
        const memory = await sampler.stop();
        const result = { batchSize, previewMs: ingestion.readyAt - ingestion.startedAt,
          allStagedMs: Date.now() - ingestion.startedAt, memory, work: staged.work };
        measurements.push(result);
        runner.writeJson(`attachment-batch-${batchSize}.json`, result);
        runner.writeJson('attachment-batch-results.json', measurements);
      } catch (error) { runner.writeJson(`attachment-batch-${batchSize}-failure.json`, { memory: await sampler.stop(), error: String(error) }); throw error; }
      await key('enter');
      const accepted = await hidden();
      assert.equal(accepted.saved[0].images.length, files.length);
    }
  });
  if (process.env.CI === 'true') {
    await client.request('capture_dismiss');
    const processes = execFileSync('ps', ['-axo', 'pid,ppid,comm'], { encoding: 'utf8' });
    runner.writeJson('idle-process-ownership.json', { appPid: client.launch?.pid, processes,
      load: execFileSync('uptime', { encoding: 'utf8' }).trim(), note: 'Disposable hosted runner; includes every WebKit process for explicit attribution.' });
    const pids = processes.split('\n').filter(line => line.includes('WebKit') || line.includes(appPlatform.appExecutableInTree(options.appPath)))
      .map(line => Number(line.trim().split(/\s+/)[0])).filter(Number.isInteger);
    const samples = execFileSync('top', ['-l', '2', '-s', '5', '-stats', 'pid,command,cpu,mem', ...pids.flatMap(pid => ['-pid', String(pid)])], { encoding: 'utf8' });
    fs.writeFileSync(path.join(runner.runDir, 'hidden-idle-native-webkit.txt'), samples);
    await openCapture();
    assert.equal((await state()).activeAnimations, 0, 'Stationary composer has no perpetual animations');
    const shown = execFileSync('top', ['-l', '2', '-s', '5', '-stats', 'pid,command,cpu,mem', ...pids.flatMap(pid => ['-pid', String(pid)])], { encoding: 'utf8' });
    fs.writeFileSync(path.join(runner.runDir, 'shown-idle-native-webkit.txt'), shown);
    await key('escape'); await hidden();
  }
  const result = await state();
  runner.writeJson('capture-results.json', result);
  runner.writeJson('opening-latency.json', openingReceipts);
  if (recording) { const receipt = await recording.stop(); recording = null; runner.writeJson('recording-result.json', receipt); assert.equal(receipt.failure, null); }
  await runner.finishSuccess({ hotkeyInput: 'OS HID events, no target PID or app activation', result });
} catch (error) {
  saveNativeTrace('native-failure.log');
  if (recording) { const receipt = await recording.stop().catch(error => ({ failure: String(error) })); runner.writeJson('recording-result.json', receipt); recording = null; }
  await screenshot('failure.png').catch(() => {});
  runner.writeJson('failure-state.json', await state().catch(e => ({ error: String(e) })));
  runner.writeJson('failure-capture-windows.json', await driver.windowList());
  await runner.finishFailure(error);
  process.exitCode = 1;
}
