import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import { openDeepLink as openHarnessDeepLink } from './deepLink.mjs';
import {
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  defaultAppPathForProfile,
} from './harnessProfile.mjs';

const execFileAsync = promisify(execFile);

const LINUX_KEY_CODES = new Map([
  [36, 'Return'],
  [53, 'Escape'],
  [123, 'Left'],
  [124, 'Right'],
]);

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function parsePositiveIntegers(stdout) {
  return String(stdout || '')
    .split(/\s+/)
    .map((value) => Number.parseInt(value, 10))
    .filter((value) => Number.isInteger(value) && value > 0);
}

function escapeRegex(value) {
  return String(value).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function commandErrorDetail(error) {
  return error?.stderr?.toString?.().trim() || error?.message || String(error);
}

function isMissingCommand(error, command) {
  return error?.code === 'ENOENT' && String(error?.path || error?.message || '').includes(command);
}

export function describeLinuxInputFailure(error = null, env = process.env) {
  if (!String(env.DISPLAY || '').trim()) {
    return 'Linux input unavailable: DISPLAY is not set. Run the harness under xvfb-run or export an X11 display.';
  }
  if (isMissingCommand(error, 'xdotool')) {
    return 'Linux input unavailable: xdotool is not installed. Install xdotool and rerun the harness.';
  }
  return 'Linux input driver failed.';
}

export function linuxModifierNames(modifiers = {}) {
  const names = [];
  if (modifiers.command || modifiers.control) names.push('ctrl');
  if (modifiers.option) names.push('alt');
  if (modifiers.shift) names.push('shift');
  return names;
}

export function linuxKeyName(key, modifiers = {}) {
  const keyNames = {
    '.': 'period',
    ',': 'comma',
    '/': 'slash',
    '-': 'minus',
    '=': 'equal',
  };
  const raw = String(key || '').trim();
  const normalized = keyNames[raw] || raw;
  if (!normalized) {
    throw new Error('Linux input key must not be empty');
  }
  return [...linuxModifierNames(modifiers), normalized].join('+');
}

export function linuxKeyCodeName(keyCode) {
  const key = LINUX_KEY_CODES.get(Number(keyCode));
  if (!key) {
    throw new Error(`Unsupported Linux input key code: ${keyCode}`);
  }
  return key;
}

export function parseXdotoolGeometry(stdout) {
  const fields = Object.fromEntries(
    String(stdout || '')
      .split(/\r?\n/)
      .map((line) => line.split('=', 2))
      .filter(([name, value]) => name && value !== undefined),
  );
  const geometry = {
    x: Number.parseInt(fields.X, 10),
    y: Number.parseInt(fields.Y, 10),
    width: Number.parseInt(fields.WIDTH, 10),
    height: Number.parseInt(fields.HEIGHT, 10),
  };
  if (![geometry.x, geometry.y, geometry.width, geometry.height].every(Number.isFinite)
      || geometry.width <= 0 || geometry.height <= 0) {
    throw new Error(`Failed to parse xdotool window geometry: ${stdout}`);
  }
  return geometry;
}

export class LinuxDriver {
  constructor({
    bundleId = null,
    appPath = defaultAppPathForProfile(),
    actionDelayMs = 250,
    env = process.env,
    run = execFileAsync,
  } = {}) {
    this.appPath = appPath;
    this.appName = path.basename(appPath);
    this.bundleId = bundleId || bundleIdentifierForAppPath(appPath);
    assertProductionRunAllowed({ appPath, bundleId: this.bundleId });
    this.actionDelayMs = actionDelayMs;
    this.env = env;
    this.run = run;
  }

  async launchApp() {
    throw new Error('LinuxDriver.launchApp is unsupported; launch through UiAutomationClient');
  }

  async openDeepLink(url) {
    await openHarnessDeepLink(url, { appPath: this.appPath, platform: 'linux' });
    await delay(500);
  }

  async activateApp(opts = {}) {
    const windowId = await this.requireWindow(opts);
    await this.runXdotool(['windowraise', String(windowId), 'windowfocus', '--sync', String(windowId)]);
    await delay(this.actionDelayMs);
  }

  async activateBackground(opts = {}) {
    const windowId = await this.requireWindow(opts);
    await this.runXdotool(['windowfocus', '--sync', String(windowId)]);
    await delay(this.actionDelayMs);
  }

  async menu() {
    throw new Error('Linux input driver does not expose native application menus');
  }

  async frontmostBundleId() {
    const active = Number.parseInt(await this.runXdotoolCapture(['getactivewindow']), 10);
    const main = await this.mainWindowId();
    if (active === main) {
      return this.bundleId;
    }
    return this.runXdotoolCapture(['getwindowname', String(active)]).catch(() => '');
  }

  async displayState() {
    this.requireDisplay();
    return {
      display: this.env.DISPLAY,
      blockReason: null,
    };
  }

  async mainWindowId(opts = {}) {
    const searchArgs = ['search', '--onlyvisible'];
    const pid = Number.isInteger(opts.pid) && opts.pid > 0 ? opts.pid : null;
    if (pid) {
      searchArgs.push('--pid', String(pid));
    } else {
      const title = opts.windowTitle || this.appName;
      searchArgs.push('--name', `^${escapeRegex(title)}$`);
    }
    try {
      const ids = parsePositiveIntegers(await this.runXdotoolCapture(searchArgs));
      return ids[0] || null;
    } catch (error) {
      if (error?.cause?.code === 1 || error?.code === 1) {
        return null;
      }
      throw error;
    }
  }

  async waitForMainWindow(timeoutMs = 10_000, pollIntervalMs = 150, opts = {}) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const windowId = await this.mainWindowId(opts);
      if (windowId) return windowId;
      await delay(pollIntervalMs);
    }
    return null;
  }

  async windowList() {
    const main = await this.mainWindowId();
    if (!main) return [];
    let ids = [main];
    try {
      const pid = await this.runXdotoolCapture(['getwindowpid', String(main)]);
      ids = parsePositiveIntegers(await this.runXdotoolCapture([
        'search', '--onlyvisible', '--pid', pid,
      ]));
    } catch {}

    const windows = [];
    for (const id of ids) {
      const name = await this.runXdotoolCapture(['getwindowname', String(id)]).catch(() => '');
      const bounds = await this.windowGeometry(id).catch(() => null);
      if (bounds) windows.push({ id, name, ...bounds, layer: 0 });
    }
    return windows;
  }

  async waitForWindowTitled(title, { timeoutMs = 10_000, pollIntervalMs = 200 } = {}) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const id = await this.mainWindowId({ windowTitle: title });
      if (id) {
        const bounds = await this.windowGeometry(id);
        return { id, name: title, ...bounds, layer: 0 };
      }
      await delay(pollIntervalMs);
    }
    return null;
  }

  async typeText(text) {
    await this.activateBackground();
    await this.runXdotool(['type', '--clearmodifiers', '--', String(text)]);
    await delay(this.actionDelayMs);
  }

  async pressKey(key, modifiers = {}) {
    await this.activateBackground();
    await this.runXdotool(['key', '--clearmodifiers', linuxKeyName(key, modifiers)]);
    await delay(this.actionDelayMs);
  }

  async pressKeyCode(keyCode, modifiers = {}) {
    await this.pressKey(linuxKeyCodeName(keyCode), modifiers);
  }

  async pressEnter() {
    await this.pressKeyCode(36);
  }

  async movePointerInWindow(relativeX, relativeY, opts = {}) {
    const { windowId, x, y } = await this.relativePoint(relativeX, relativeY, opts);
    await this.runXdotool(['mousemove', '--sync', '--window', String(windowId), String(x), String(y)]);
    await delay(this.actionDelayMs);
  }

  async clickWindow(relativeX, relativeY, opts = {}) {
    await this.clickButton(1, relativeX, relativeY, opts);
  }

  async rightClickWindow(relativeX, relativeY, opts = {}) {
    await this.clickButton(3, relativeX, relativeY, opts);
  }

  async dragWindow(relativeX, relativeY, toRelativeX, toRelativeY, opts = {}) {
    const start = await this.relativePoint(relativeX, relativeY, opts);
    const end = await this.relativePoint(toRelativeX, toRelativeY, opts);
    const stepCount = Math.max(2, Number.isInteger(opts.steps) ? opts.steps : 12);
    const args = [
      'mousemove', '--sync', '--window', String(start.windowId), String(start.x), String(start.y),
      'mousedown', '1',
    ];
    for (let step = 1; step <= stepCount; step += 1) {
      const fraction = step / stepCount;
      const x = Math.round(start.x + (end.x - start.x) * fraction);
      const y = Math.round(start.y + (end.y - start.y) * fraction);
      args.push('mousemove', '--sync', '--window', String(start.windowId), String(x), String(y));
    }
    args.push('mouseup', '1');
    await this.activateBackground(opts);
    await this.runXdotool(args);
    await delay(this.actionDelayMs);
  }

  async scrollWindow(relativeX, relativeY, deltaY, opts = {}) {
    const deltaX = Number(opts.deltaX || 0);
    const vertical = Math.abs(Number(deltaY)) >= Math.abs(deltaX);
    const delta = vertical ? Number(deltaY) : deltaX;
    if (!Number.isFinite(delta) || delta === 0) return;
    const button = vertical ? (delta > 0 ? 4 : 5) : (delta > 0 ? 6 : 7);
    const repeat = Number.isInteger(opts.steps) && opts.steps > 0 ? opts.steps : 1;
    await this.movePointerInWindow(relativeX, relativeY, opts);
    await this.runXdotool(['click', '--repeat', String(repeat), String(button)]);
    await delay(this.actionDelayMs);
  }

  async parkWindow() {}

  async setWindowBounds(bounds, opts = {}) {
    const windowId = await this.requireWindow(opts);
    await this.runXdotool([
      'windowmove', String(windowId), String(bounds.x), String(bounds.y),
      'windowsize', String(windowId), String(bounds.width), String(bounds.height),
    ]);
    return this.windowGeometry(windowId);
  }

  async screenshot(outputPath, opts = {}) {
    const windowId = await this.requireWindow(opts);
    fs.mkdirSync(path.dirname(outputPath), { recursive: true });
    try {
      await this.run('import', ['-window', String(windowId), outputPath], {
        env: this.env,
        timeout: 10_000,
      });
      return;
    } catch (error) {
      if (!isMissingCommand(error, 'import')) {
        throw new Error(`Linux window capture failed: ${commandErrorDetail(error)}`, { cause: error });
      }
    }
    try {
      await this.run('xwd', ['-silent', '-id', String(windowId), '-out', outputPath], {
        env: this.env,
        timeout: 10_000,
      });
    } catch (error) {
      const missing = isMissingCommand(error, 'xwd')
        ? 'ImageMagick import and xwd are both missing'
        : commandErrorDetail(error);
      throw new Error(`Linux window capture failed: ${missing}`, { cause: error });
    }
  }

  async windowGeometry(windowId = null) {
    const id = windowId || await this.requireWindow();
    return parseXdotoolGeometry(await this.runXdotoolCapture([
      'getwindowgeometry', '--shell', String(id),
    ]));
  }

  async relativePoint(relativeX, relativeY, opts = {}) {
    const windowId = await this.requireWindow(opts);
    const bounds = await this.windowGeometry(windowId);
    return {
      windowId,
      x: Math.round(bounds.width * Number(relativeX)),
      y: Math.round(bounds.height * Number(relativeY)),
    };
  }

  async clickButton(button, relativeX, relativeY, opts = {}) {
    const point = await this.relativePoint(relativeX, relativeY, opts);
    const modifiers = linuxModifierNames(opts.modifiers);
    const args = [
      'mousemove', '--sync', '--window', String(point.windowId), String(point.x), String(point.y),
    ];
    for (const modifier of modifiers) args.push('keydown', modifier);
    args.push('click', String(button));
    for (const modifier of [...modifiers].reverse()) args.push('keyup', modifier);
    await this.activateBackground(opts);
    await this.runXdotool(args);
    await delay(this.actionDelayMs);
  }

  async requireWindow(opts = {}) {
    const windowId = await this.mainWindowId(opts);
    if (windowId) return windowId;
    const title = opts.windowTitle || this.appName;
    throw new Error(`Linux input driver found no visible window titled ${JSON.stringify(title)} on DISPLAY=${this.env.DISPLAY}`);
  }

  requireDisplay() {
    const hint = describeLinuxInputFailure(null, this.env);
    if (!String(this.env.DISPLAY || '').trim()) {
      throw new Error(hint);
    }
  }

  async runXdotool(args) {
    this.requireDisplay();
    try {
      await this.run('xdotool', args, { env: this.env, timeout: 5_000 });
    } catch (error) {
      const hint = describeLinuxInputFailure(error, this.env);
      const wrapped = new Error(`${hint}\n${commandErrorDetail(error)}`, { cause: error });
      wrapped.code = error?.code;
      throw wrapped;
    }
  }

  async runXdotoolCapture(args) {
    this.requireDisplay();
    try {
      const { stdout } = await this.run('xdotool', args, { env: this.env, timeout: 5_000 });
      return stdout.toString().trim();
    } catch (error) {
      const hint = describeLinuxInputFailure(error, this.env);
      const wrapped = new Error(`${hint}\n${commandErrorDetail(error)}`, { cause: error });
      wrapped.code = error?.code;
      throw wrapped;
    }
  }
}

export { delay };
