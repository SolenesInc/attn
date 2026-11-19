import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import {
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  defaultAppPathForInstance,
} from './harnessInstance.mjs';

const execFileAsync = promisify(execFile);
const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const INPUT_DRIVER_SOURCE = path.join(SCRIPT_DIR, 'InputDriver.swift');
const INPUT_DRIVER_BUILD_DIR = path.join(SCRIPT_DIR, '.build');
const INPUT_DRIVER_BINARY = path.join(INPUT_DRIVER_BUILD_DIR, 'attn-real-input-driver');
const CODESIGN_IDENTITY_SCRIPT = path.resolve(SCRIPT_DIR, '..', '..', '..', 'scripts', 'macos-codesign-identity.sh');
const INPUT_DRIVER_LIMIT_MS = 15_000;
let inputDriverReady = null;

function inputDriverOutput(error) {
  const stderr = error?.stderr?.toString?.() || '';
  if (error?.killed) return `${stderr}The input driver was killed after ${INPUT_DRIVER_LIMIT_MS / 1000} s, before it reported a result.`;
  return stderr || error.message;
}

function delay(ms) {
  if (ms === 0) return Promise.resolve();
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

function modifierNames(modifiers = {}) {
  const names = [];
  if (modifiers.command) names.push('command');
  if (modifiers.option) names.push('option');
  if (modifiers.shift) names.push('shift');
  if (modifiers.control) names.push('control');
  return names;
}

export class MacOSDriver {
  constructor({
    bundleId = null,
    appPath = defaultAppPathForInstance(),
  } = {}) {
    const resolvedBundleId = bundleId || bundleIdentifierForAppPath(appPath);
    assertProductionRunAllowed({ appPath, bundleId: resolvedBundleId });
    this.bundleId = resolvedBundleId;
    this.appPath = appPath;
  }

  async activateApp() {
    await this.runInputDriver(['activate']);
  }

  async activateBackground() {
    await this.runInputDriver(['activate_background']);
  }

  async frontmostBundleId() {
    return this.runInputDriverCapture(['frontmost']);
  }

  async mainWindowId() {
    try {
      const value = await this.runInputDriverCapture(['windowid']);
      const parsed = Number.parseInt(value, 10);
      return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
    } catch {
      return null;
    }
  }

  async waitForMainWindow(timeoutMs = 10_000, pollIntervalMs = 150) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const wid = await this.mainWindowId();
      if (wid) {
        return wid;
      }
      await delay(pollIntervalMs);
    }
    return null;
  }

  async shownWindowIds() {
    return (await this.windowList()).map((window) => window.id);
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

  async pressKey(key, modifiers = {}) {
    await this.runInputDriver(['key', '--key', key, '--modifiers', modifierNames(modifiers).join(','), '--prompt-accessibility']);
  }

  async parkWindow(visiblePx) {
    const stdout = await this.runInputDriverCapture([
      'window_park',
      '--visible-px',
      String(visiblePx),
      '--prompt-accessibility',
    ]);
    console.log(`[RealAppHarness] Parked window: ${stdout.trim()}`);
  }

  async screenshot(outputPath, opts = {}) {
    const windowArgs = opts.windowId ? ['-l', String(opts.windowId)] : [];
    await execFileAsync('/usr/sbin/screencapture', ['-x', '-o', ...windowArgs, outputPath], { timeout: 10_000 });
  }

  async ensureInputDriver() {
    if (!inputDriverReady) inputDriverReady = this.prepareInputDriver();
    try {
      return await inputDriverReady;
    } catch (error) {
      inputDriverReady = null;
      throw error;
    }
  }

  async prepareInputDriver() {
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
        timeout: INPUT_DRIVER_LIMIT_MS,
      });
    } catch (error) {
      const stderr = error?.stderr?.toString?.() || '';
      throw new Error(`${describeInputDriverFailure(stderr)}\n${inputDriverOutput(error)}`);
    }
  }

  async runInputDriverCapture(args) {
    const binaryPath = await this.ensureInputDriver();
    const fullArgs = ['--bundle-id', this.bundleId, ...args];
    try {
      const { stdout } = await execFileAsync(binaryPath, fullArgs, {
        timeout: INPUT_DRIVER_LIMIT_MS,
      });
      return stdout.toString().trim();
    } catch (error) {
      throw new Error(`macOS input driver capture failed: ${inputDriverOutput(error)}`);
    }
  }
}

