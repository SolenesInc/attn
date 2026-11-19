import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import {
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  defaultAppPathForInstance,
} from './harnessInstance.mjs';

const execFileAsync = promisify(execFile);

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

function selectMainWindow({ pid, windows }, { pid: launchedPid = null } = {}) {
  if (launchedPid && pid !== launchedPid) {
    throw new Error(`The app automation server answered from pid ${pid}, not the launched app pid ${launchedPid}`);
  }
  const window = windows.find((candidate) => candidate.label === 'main');
  return window?.mapped ? window : null;
}

export class LinuxDriver {
  constructor({
    bundleId = null,
    appPath = defaultAppPathForInstance(),
    env = process.env,
    run = execFileAsync,
    client = null,
  } = {}) {
    this.client = client;
    this.appPath = appPath;
    this.appName = path.basename(appPath);
    this.bundleId = bundleId || bundleIdentifierForAppPath(appPath);
    assertProductionRunAllowed({ appPath, bundleId: this.bundleId });
    this.env = env;
    this.run = run;
  }

  async activateBackground(opts = {}) {
    const windowId = await this.requireWindow(opts);
    await this.runXdotool(['windowfocus', '--sync', String(windowId)]);
  }

  async frontmostBundleId() {
    const active = Number.parseInt(await this.runXdotoolCapture(['getactivewindow']), 10);
    const main = await this.mainWindowId();
    if (active === main) {
      return this.bundleId;
    }
    return this.runXdotoolCapture(['getwindowname', String(active)]).catch(() => '');
  }

  async ownedWindows() {
    if (!this.client) {
      throw new Error('LinuxDriver resolves app windows through the app automation server; create it with createWindowDriver({ appPath, client })');
    }
    try {
      return await this.client.request('native_windows', {}, { timeoutMs: 5_000 });
    } catch (error) {
      throw new Error(`LinuxDriver could not ask the app for its windows through the automation server: ${error?.message || error}`, { cause: error });
    }
  }

  async mainWindowId(opts = {}) {
    return selectMainWindow(await this.ownedWindows(), opts)?.id ?? null;
  }

  async shownWindowIds() {
    const { pid } = await this.ownedWindows();
    const found = await this.runXdotoolCapture(['search', '--onlyvisible', '--pid', String(pid)]).catch(() => '');
    return found.split(/\s+/).filter(Boolean).map(Number);
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

  async parkWindow() {}

  async setWindowBounds(bounds, opts = {}) {
    const windowId = await this.requireWindow(opts);
    await this.runXdotool([
      'windowmove', String(windowId), String(bounds.x), String(bounds.y),
      'windowsize', String(windowId), String(bounds.width), String(bounds.height),
    ]);
    return this.windowGeometry(windowId);
  }

  async parkPointerBeside({ x, y, width, height }) {
    await this.runXdotool(['mousemove', String(x + width + 1), String(y + height + 1)]);
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

  async requireWindow(opts = {}) {
    if (opts.windowId) return opts.windowId;
    const owned = await this.ownedWindows();
    const window = selectMainWindow(owned, opts);
    if (window) return window.id;
    throw new Error(`Linux input driver found no mapped app window labelled "main" on DISPLAY=${this.env.DISPLAY}`
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

