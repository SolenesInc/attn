import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import { openDeepLink as openHarnessDeepLink } from './deepLink.mjs';
import {
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  defaultAppPathForInstance,
} from './harnessInstance.mjs';
import { TargetedWindowDriver } from './targetedWindowDriver.mjs';

const execFileAsync = promisify(execFile);

const LINUX_KEY_CODES = new Map([
  [36, 'Return'],
  [48, 'Tab'],
  [49, 'space'],
  [53, 'Escape'],
  [115, 'Home'],
  [119, 'End'],
  [123, 'Left'],
  [124, 'Right'],
]);

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
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
    '[': 'bracketleft',
    ']': 'bracketright',
    Enter: 'Return',
    Backspace: 'BackSpace',
    ArrowLeft: 'Left',
    ArrowRight: 'Right',
    ArrowDown: 'Down',
    ArrowUp: 'Up',
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

function selectOwnedWindow({ pid, windows }, { pid: launchedPid = null, windowTitle = null } = {}) {
  if (launchedPid && pid !== launchedPid) {
    throw new Error(`The app automation bridge answered from pid ${pid}, not the launched app pid ${launchedPid}`);
  }
  const window = windows.find(windowTitle ? (candidate) => candidate.title === windowTitle : (candidate) => candidate.label === 'main');
  return window?.mapped ? window : null;
}

export class LinuxDriver extends TargetedWindowDriver {
  constructor({
    bundleId = null,
    appPath = defaultAppPathForInstance(),
    env = process.env,
    run = execFileAsync,
    client = null,
  } = {}) {
    super({ client, pointer: (gesture) => this.#pointer(gesture) });
    this.appPath = appPath;
    this.appName = path.basename(appPath);
    this.bundleId = bundleId || bundleIdentifierForAppPath(appPath);
    assertProductionRunAllowed({ appPath, bundleId: this.bundleId });
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
  }

  async activateBackground(opts = {}) {
    const windowId = await this.requireWindow(opts);
    await this.runXdotool(['windowfocus', '--sync', String(windowId)]);
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

  async ownedWindows() {
    if (!this.client) {
      throw new Error('LinuxDriver resolves app windows through the app automation bridge; create it with createWindowDriver({ appPath, client })');
    }
    try {
      return await this.client.request('native_windows', {}, { timeoutMs: 5_000 });
    } catch (error) {
      throw new Error(`LinuxDriver could not ask the app for its windows through the automation bridge: ${error?.message || error}`, { cause: error });
    }
  }

  async mainWindowId(opts = {}) {
    return selectOwnedWindow(await this.ownedWindows(), opts)?.id ?? null;
  }

  async waitForMainWindow(timeoutMs = 10_000, pollIntervalMs = 150, opts = {}) {
    const deadline = Date.now() + timeoutMs;
    let lastError = null;
    while (Date.now() < deadline) {
      try {
        const windowId = await this.mainWindowId(opts);
        if (windowId) return windowId;
        lastError = null;
      } catch (error) {
        lastError = error;
      }
      await delay(pollIntervalMs);
    }
    if (lastError) throw lastError;
    return null;
  }

  async windowList() {
    const { windows } = await this.ownedWindows();
    const listed = [];
    for (const window of windows.filter((candidate) => candidate.mapped)) {
      const bounds = await this.windowGeometry(window.id).catch(() => null);
      if (bounds) listed.push({ id: window.id, name: window.title ?? '', ...bounds, layer: 0 });
    }
    return listed;
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
  }

  async pressKey(key, modifiers = {}) {
    await this.activateBackground();
    await this.runXdotool(['key', '--clearmodifiers', linuxKeyName(key, modifiers)]);
  }

  async pressKeyCode(keyCode, modifiers = {}) {
    await this.pressKey(linuxKeyCodeName(keyCode), modifiers);
  }

  async pressEnter() {
    await this.pressKeyCode(36);
  }

  async #pointer({ action, point, to, deltaY, opts }) {
    if (action === 'move') return this.#move(point.x, point.y, opts);
    if (action === 'click') return this.#click(1, point.x, point.y, opts);
    if (action === 'right_click') return this.#click(3, point.x, point.y, opts);
    if (action === 'drag') return this.#drag(point.x, point.y, to.x, to.y, opts);
    if (action === 'scroll') return this.#scroll(point.x, point.y, deltaY, opts);
    throw new Error(`LinuxDriver has no pointer action ${action}`);
  }

  async #move(relativeX, relativeY, opts) {
    const { windowId, x, y } = await this.relativePoint(relativeX, relativeY, opts);
    await this.runXdotool(['mousemove', '--window', String(windowId), String(x), String(y)]);
  }

  async #drag(relativeX, relativeY, toRelativeX, toRelativeY, opts) {
    const start = await this.relativePoint(relativeX, relativeY, opts);
    const end = await this.relativePoint(toRelativeX, toRelativeY, opts);
    const stepCount = Math.max(2, Number.isInteger(opts.steps) ? opts.steps : 12);
    const args = [
      'mousemove', '--window', String(start.windowId), String(start.x), String(start.y),
      'mousedown', '1',
    ];
    for (let step = 1; step <= stepCount; step += 1) {
      const fraction = step / stepCount;
      const x = Math.round(start.x + (end.x - start.x) * fraction);
      const y = Math.round(start.y + (end.y - start.y) * fraction);
      args.push('mousemove', '--window', String(start.windowId), String(x), String(y));
    }
    args.push('mouseup', '1');
    await this.activateBackground(opts);
    await this.runXdotool(args);
  }

  async #scroll(relativeX, relativeY, deltaY, opts) {
    const deltaX = Number(opts.deltaX || 0);
    const vertical = Math.abs(Number(deltaY)) >= Math.abs(deltaX);
    const delta = vertical ? Number(deltaY) : deltaX;
    if (!Number.isFinite(delta) || delta === 0) return;
    const button = vertical ? (delta > 0 ? 4 : 5) : (delta > 0 ? 6 : 7);
    const repeat = Number.isInteger(opts.steps) && opts.steps > 0 ? opts.steps : 1;
    await this.#move(relativeX, relativeY, opts);
    await this.runXdotool(['click', '--repeat', String(repeat), String(button)]);
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
    await this.windowGeometry(windowId).catch((error) => {
      throw new Error(`Linux window capture failed: window ${windowId} is gone: ${commandErrorDetail(error.cause ?? error)}`, { cause: error });
    });
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
    const captureDir = fs.mkdtempSync(path.join(path.dirname(outputPath), '.attn-window-capture-'));
    const xwdPath = path.join(captureDir, 'window.xwd');
    try {
      await this.run('xwd', ['-silent', '-id', String(windowId), '-out', xwdPath], {
        env: this.env,
        timeout: 10_000,
      });
      await this.run('convert', [xwdPath, `png:${outputPath}`], {
        env: this.env,
        timeout: 10_000,
      });
    } catch (error) {
      let detail = commandErrorDetail(error);
      if (isMissingCommand(error, 'xwd')) {
        detail = 'ImageMagick import is missing and xwd is not installed';
      } else if (isMissingCommand(error, 'convert')) {
        detail = 'ImageMagick import and convert are both missing; xwd cannot emit PNG';
      }
      throw new Error(`Linux window capture failed: ${detail}`, { cause: error });
    } finally {
      fs.rmSync(captureDir, { recursive: true, force: true });
    }
  }

  async windowGeometry(windowId = null) {
    const id = windowId || await this.requireWindow();
    return parseXdotoolGeometry(await this.runXdotoolCapture([
      'getwindowgeometry', '--shell', String(id),
    ]));
  }

  async nativeFocusState() {
    const read = (fn) => fn().catch((error) => ({ error: String(error?.message || error) }));
    const describeWindow = async (id) => ({
      id,
      name: await this.runXdotoolCapture(['getwindowname', String(id)]).catch(() => null),
      pid: await this.runXdotoolCapture(['getwindowpid', String(id)]).catch(() => null),
      geometry: await this.windowGeometry(id).catch(() => null),
    });
    const [mainWindow, focusedWindow, pointer, appWindows] = await Promise.all([
      read(async () => {
        const id = await this.mainWindowId();
        return id ? describeWindow(id) : null;
      }),
      read(async () => describeWindow(Number.parseInt(await this.runXdotoolCapture(['getwindowfocus', '-f']), 10))),
      read(async () => Object.fromEntries(
        (await this.runXdotoolCapture(['getmouselocation', '--shell'])).split(/\r?\n/).map((line) => line.split('=', 2)),
      )),
      read(async () => {
        const { pid, windows } = await this.ownedWindows();
        return {
          pid,
          windows: await Promise.all(windows.map(async (window) => ({
            ...window,
            native: window.id ? await describeWindow(window.id) : null,
          }))),
        };
      }),
    ]);
    return { mainWindow, focusedWindow, pointer, appWindows };
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

  // `mousemove --sync` waits for a motion event, and none comes when the
  // pointer already sits at the point (a repeated click), so it hangs.
  async #click(button, relativeX, relativeY, opts) {
    const point = await this.relativePoint(relativeX, relativeY, opts);
    const modifiers = linuxModifierNames(opts.modifiers);
    const args = [
      'mousemove', '--window', String(point.windowId), String(point.x), String(point.y),
    ];
    for (const modifier of modifiers) args.push('keydown', modifier);
    args.push('click', String(button));
    for (const modifier of [...modifiers].reverse()) args.push('keyup', modifier);
    await this.activateBackground(opts);
    await this.runXdotool(args);
  }

  async requireWindow(opts = {}) {
    if (opts.windowId) return opts.windowId;
    const owned = await this.ownedWindows();
    const window = selectOwnedWindow(owned, opts);
    if (window) return window.id;
    const wanted = opts.windowTitle ? `titled ${JSON.stringify(opts.windowTitle)}` : 'labelled "main"';
    throw new Error(`Linux input driver found no mapped app window ${wanted} on DISPLAY=${this.env.DISPLAY}`
      + `; app pid ${owned.pid} reports windows ${JSON.stringify(owned.windows)}`);
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
