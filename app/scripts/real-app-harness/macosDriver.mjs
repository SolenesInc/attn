import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { openDeepLink } from './deepLink.mjs';
import {
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  defaultAppPathForProfile,
} from './harnessProfile.mjs';

const execFileAsync = promisify(execFile);
const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const INPUT_DRIVER_SOURCE = path.join(SCRIPT_DIR, 'InputDriver.swift');
const INPUT_DRIVER_BUILD_DIR = path.join(SCRIPT_DIR, '.build');
const INPUT_DRIVER_BINARY = path.join(INPUT_DRIVER_BUILD_DIR, 'attn-real-input-driver');
const CODESIGN_IDENTITY_SCRIPT = path.resolve(SCRIPT_DIR, '..', '..', '..', 'scripts', 'macos-codesign-identity.sh');

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function describeInputDriverFailure(stderr = '') {
  if (stderr.includes('Accessibility permission is required')) {
    return 'Grant Accessibility access to the attn real-app input driver when macOS prompts, then rerun the harness.';
  }
  if (stderr.includes('Input cannot be delivered')) {
    return 'The display was off or the screen was locked, so input could not be delivered. This is not a product failure — wake the display and rerun.';
  }
  return 'macOS automation failed.';
}

// A secondary Tauri window (e.g. "attn — present") is never enumerated by
// Accessibility; --window-title targets it instead.
export function withWindowTitleArgs(args, opts = {}) {
  if (!opts.windowTitle) {
    return args;
  }
  return [...args, '--window-title', opts.windowTitle];
}

export class MacOSDriver {
  constructor({
    bundleId = null,
    appPath = defaultAppPathForProfile(),
    actionDelayMs = 250,
  } = {}) {
    const resolvedBundleId = bundleId || bundleIdentifierForAppPath(appPath);
    assertProductionRunAllowed({ appPath, bundleId: resolvedBundleId });
    this.bundleId = resolvedBundleId;
    this.appPath = appPath;
    this.actionDelayMs = actionDelayMs;
  }

  async launchApp() {
    await execFileAsync('open', ['-a', this.appPath]);
    await delay(800);
  }

  async openDeepLink(url) {
    await openDeepLink(url, { appPath: this.appPath });
    await delay(500);
  }

  async activateApp() {
    await this.runInputDriver(['activate']);
    await delay(this.actionDelayMs);
  }

  async activateBackground() {
    await this.runInputDriver(['activate_background']);
    await delay(this.actionDelayMs);
  }

  async menu(pathSegments) {
    const path = Array.isArray(pathSegments) ? pathSegments : [pathSegments];
    if (path.length === 0) {
      throw new Error('MacOSDriver.menu requires at least one path segment');
    }
    await this.runInputDriver([
      'menu',
      '--path',
      path.join('>'),
      '--prompt-accessibility',
    ]);
    await delay(this.actionDelayMs);
  }

  async frontmostBundleId() {
    return this.runInputDriverCapture(['frontmost']);
  }

  async displayState() {
    return JSON.parse(await this.runInputDriverCapture(['display_state']));
  }

  // System Events reports 0 windows for Tauri/wry apps even when one is
  // visible, so this reads CGWindowListCopyWindowInfo instead.
  async mainWindowId(opts = {}) {
    try {
      const value = await this.runInputDriverCapture(withWindowTitleArgs(['windowid'], opts));
      const parsed = Number.parseInt(value, 10);
      return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
    } catch {
      return null;
    }
  }

  async waitForMainWindow(timeoutMs = 10_000, pollIntervalMs = 150, opts = {}) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const wid = await this.mainWindowId(opts);
      if (wid) {
        return wid;
      }
      await delay(pollIntervalMs);
    }
    return null;
  }

  async windowList() {
    try {
      const value = await this.runInputDriverCapture(['windowlist']);
      const parsed = JSON.parse(value);
      return Array.isArray(parsed) ? parsed : [];
    } catch {
      return [];
    }
  }

  // Window names need Screen Recording permission; without it this only ever
  // times out to null.
  async waitForWindowTitled(title, { timeoutMs = 10_000, pollIntervalMs = 200 } = {}) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const named = (await this.windowList()).find((w) => w.name === title);
      if (named) return named;
      await delay(pollIntervalMs);
    }
    return null;
  }

  async typeText(text) {
    await this.runInputDriver(['text', '--text', text, '--prompt-accessibility']);
    await delay(this.actionDelayMs);
  }

  async pressKey(key, modifiers = {}) {
    await this.runInputDriver([
      'key',
      '--key',
      key,
      '--modifiers',
      this.serializeModifiers(modifiers),
      '--prompt-accessibility',
    ]);
    await delay(this.actionDelayMs);
  }

  async pressKeyCode(keyCode, modifiers = {}) {
    await this.runInputDriver([
      'keycode',
      '--key-code',
      String(keyCode),
      '--modifiers',
      this.serializeModifiers(modifiers),
      '--prompt-accessibility',
    ]);
    await delay(this.actionDelayMs);
  }

  async pressEnter() {
    await this.pressKeyCode(36);
  }

  async movePointerInWindow(relativeX, relativeY, opts = {}) {
    await this.runInputDriver(withWindowTitleArgs([
      'move',
      '--relative-x',
      String(relativeX),
      '--relative-y',
      String(relativeY),
      '--prompt-accessibility',
    ], opts));
    await delay(this.actionDelayMs);
  }

  async clickWindow(relativeX, relativeY, opts = {}) {
    const args = [
      'click',
      '--relative-x',
      String(relativeX),
      '--relative-y',
      String(relativeY),
      '--prompt-accessibility',
    ];
    if (opts.modifiers) {
      args.push('--modifiers', this.serializeModifiers(opts.modifiers));
    }
    await this.runInputDriver(withWindowTitleArgs(args, opts));
    await delay(this.actionDelayMs);
  }

  // Interpolated leftMouseDragged events: the shape WebKit requires for text
  // selection. Same 0..1 semantics as clickWindow.
  async dragWindow(relativeX, relativeY, toRelativeX, toRelativeY, opts = {}) {
    const args = [
      'drag',
      '--relative-x',
      String(relativeX),
      '--relative-y',
      String(relativeY),
      '--to-relative-x',
      String(toRelativeX),
      '--to-relative-y',
      String(toRelativeY),
      '--prompt-accessibility',
    ];
    if (opts.steps !== undefined) {
      args.push('--steps', String(opts.steps));
    }
    await this.runInputDriver(withWindowTitleArgs(args, opts));
    await delay(this.actionDelayMs);
  }

  async rightClickWindow(relativeX, relativeY, opts = {}) {
    const args = [
      'right_click',
      '--relative-x',
      String(relativeX),
      '--relative-y',
      String(relativeY),
      '--prompt-accessibility',
    ];
    if (opts.modifiers) {
      args.push('--modifiers', this.serializeModifiers(opts.modifiers));
    }
    await this.runInputDriver(withWindowTitleArgs(args, opts));
    await delay(this.actionDelayMs);
  }

  // Positive deltaY scrolls content up, negative down (CGEvent convention).
  async scrollWindow(relativeX, relativeY, deltaY, opts = {}) {
    const args = [
      'scroll',
      '--relative-x',
      String(relativeX),
      '--relative-y',
      String(relativeY),
      '--delta-y',
      String(deltaY),
      '--prompt-accessibility',
    ];
    if (opts.deltaX !== undefined) {
      args.push('--delta-x', String(opts.deltaX));
    }
    if (opts.steps !== undefined) {
      args.push('--steps', String(opts.steps));
    }
    await this.runInputDriver(withWindowTitleArgs(args, opts));
    await delay(this.actionDelayMs);
  }

  async parkWindow(visiblePx, opts = {}) {
    const stdout = await this.runInputDriverCapture(withWindowTitleArgs([
      'window_park',
      '--visible-px',
      String(visiblePx),
      '--prompt-accessibility',
    ], opts));
    console.log(`[RealAppHarness] Parked window: ${stdout.trim()}`);
  }

  async screenshot(outputPath) {
    await execFileAsync('/usr/sbin/screencapture', ['-x', outputPath]);
  }

  serializeModifiers(modifiers = {}) {
    const names = [];
    if (modifiers.command) names.push('command');
    if (modifiers.option) names.push('option');
    if (modifiers.shift) names.push('shift');
    if (modifiers.control) names.push('control');
    return names.join(',');
  }

  async ensureInputDriver() {
    fs.mkdirSync(INPUT_DRIVER_BUILD_DIR, { recursive: true });
    const sourceHash = createHash('sha256').update(fs.readFileSync(INPUT_DRIVER_SOURCE)).digest('hex');
    const fingerprintPath = `${INPUT_DRIVER_BINARY}.fingerprint`;
    const binaryExists = fs.existsSync(INPUT_DRIVER_BINARY);
    const builtFromHash = fs.existsSync(fingerprintPath)
      ? fs.readFileSync(fingerprintPath, 'utf8').trim()
      : null;
    if (!binaryExists || builtFromHash !== sourceHash) {
      await execFileAsync('/usr/bin/swiftc', [INPUT_DRIVER_SOURCE, '-o', INPUT_DRIVER_BINARY], {
        timeout: 30_000,
      });
      fs.writeFileSync(fingerprintPath, `${sourceHash}\n`);
    }
    await this.signInputDriverIfPossible(INPUT_DRIVER_BINARY);
    return INPUT_DRIVER_BINARY;
  }

  async signInputDriverIfPossible(binaryPath) {
    if (process.platform !== 'darwin' || !fs.existsSync(CODESIGN_IDENTITY_SCRIPT)) {
      return;
    }
    const { stdout } = await execFileAsync('bash', [CODESIGN_IDENTITY_SCRIPT, 'find'], {
      timeout: 5_000,
    });
    const identity = stdout.toString().trim();
    if (!identity || identity === '-') {
      return;
    }
    await execFileAsync('/usr/bin/codesign', ['--force', '--sign', identity, binaryPath], {
      timeout: 10_000,
    });
  }

  async runInputDriver(args) {
    const binaryPath = await this.ensureInputDriver();
    const fullArgs = ['--bundle-id', this.bundleId, ...args];
    try {
      await execFileAsync(binaryPath, fullArgs, {
        timeout: 5_000,
      });
    } catch (error) {
      const stderr = error?.stderr?.toString?.() || '';
      const hint = describeInputDriverFailure(stderr);
      throw new Error(`${hint}\n${stderr || error.message}`);
    }
  }

  async runInputDriverCapture(args) {
    const binaryPath = await this.ensureInputDriver();
    const fullArgs = ['--bundle-id', this.bundleId, ...args];
    try {
      const { stdout } = await execFileAsync(binaryPath, fullArgs, {
        timeout: 5_000,
      });
      return stdout.toString().trim();
    } catch (error) {
      const stderr = error?.stderr?.toString?.() || '';
      throw new Error(`macOS input driver capture failed: ${stderr || error.message}`);
    }
  }
}

export { delay };
