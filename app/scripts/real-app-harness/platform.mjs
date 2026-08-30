import path from 'node:path';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { MacOSDriver } from './macosDriver.mjs';

const execFileAsync = promisify(execFile);

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function parsePids(stdout) {
  return String(stdout || '')
    .split(/\s+/)
    .map((value) => Number.parseInt(value, 10))
    .filter((value) => Number.isInteger(value) && value > 0);
}

function spawnDetached(executablePath, env) {
  const child = spawn(executablePath, [], {
    detached: true,
    stdio: 'ignore',
    env: { ...process.env, ...env },
  });
  child.unref();
  return { spawned: true, pid: Number.isInteger(child.pid) ? child.pid : null };
}

export class LinuxWindowDriver {
  constructor({ appPath = null, run = execFileAsync } = {}) {
    this.appPath = appPath;
    this.run = run;
  }

  async waitForMainWindow(timeoutMs = 10_000, pollIntervalMs = 150, opts = {}) {
    const pid = Number.isInteger(opts.pid) && opts.pid > 0 ? opts.pid : null;
    if (!pid) {
      return null;
    }
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      let stdout = '';
      try {
        // Without --onlyvisible the first hit is GTK's unmapped 10x10 helper window.
        ({ stdout } = await this.run('xdotool', ['search', '--onlyvisible', '--pid', String(pid)]));
      } catch (error) {
        if (error?.code === 'ENOENT') {
          return null;
        }
      }
      const [windowId] = parsePids(stdout);
      if (windowId) {
        return windowId;
      }
      await delay(pollIntervalMs);
    }
    return null;
  }

  // The app reads ATTN_HARNESS_PARK_VISIBLE_PX only under cfg(macos).
  async parkWindow() {}
}

const darwinPlatform = {
  os: 'darwin',
  manifestWaitFloorMs: 0,

  appExecutableInTree(appPath) {
    return appPath.endsWith('.app') ? path.join(appPath, 'Contents', 'MacOS', 'app') : appPath;
  },

  appDaemonInTree(appPath) {
    return appPath.endsWith('.app')
      ? path.join(appPath, 'Contents', 'MacOS', 'attn')
      : path.join(path.dirname(appPath), 'attn');
  },

  appBuildIdentityInTree(appPath) {
    return appPath.endsWith('.app')
      ? path.join(appPath, 'Contents', 'Resources', 'build-identity.json')
      : path.join(path.dirname(appPath), 'build-identity.json');
  },

  createWindowDriver({ appPath, bundleId }) {
    return new MacOSDriver({ appPath, bundleId });
  },

  async launchApp({ appPath, env = null, background = false }) {
    if (env && Object.keys(env).length > 0) {
      // LaunchServices and `open` do not reliably propagate env into Tauri's
      // window-creation path, so custom env needs spawn-style delivery.
      return spawnDetached(this.appExecutableInTree(appPath), env);
    }
    await execFileAsync('open', background ? ['-g', appPath] : [appPath]);
    return { spawned: false, pid: null };
  },

  async requestQuit({ bundleId }) {
    try {
      await execFileAsync('osascript', ['-e', `tell application id "${bundleId}" to quit`]);
    } catch {
    }
  },

  async listAppPids({ appPath }) {
    try {
      const { stdout } = await execFileAsync('pgrep', ['-f', this.appExecutableInTree(appPath)]);
      return parsePids(stdout);
    } catch {
      return [];
    }
  },
};

const linuxPlatform = {
  os: 'linux',
  // 8 cold launches under Xvfb wrote the manifest in 6.7s-30.4s, the slow mode
  // pinned near 30.2s by a stall inside WebKitGTK window creation.
  manifestWaitFloorMs: 90_000,

  appExecutableInTree(appPath) {
    return path.join(appPath, 'bin', 'attn-app');
  },

  appDaemonInTree(appPath) {
    return path.join(appPath, 'bin', 'attn');
  },

  appBuildIdentityInTree(appPath) {
    return path.join(appPath, 'resources', 'build-identity.json');
  },

  createWindowDriver({ appPath }) {
    return new LinuxWindowDriver({ appPath });
  },

  async launchApp({ appPath, env = null }) {
    return spawnDetached(this.appExecutableInTree(appPath), env);
  },

  async requestQuit({ pid }) {
    if (!Number.isInteger(pid) || pid <= 0) {
      return;
    }
    try {
      process.kill(pid, 'SIGTERM');
    } catch {
    }
  },

  // A pid comes from the manifest or from spawn, never from a command-line
  // pattern: every attn worker and agent session carries the tree path in argv.
  async listAppPids() {
    return [];
  },
};

export function appPlatformFor(platform = process.platform) {
  return platform === 'darwin' ? darwinPlatform : linuxPlatform;
}

export const appPlatform = appPlatformFor();

export function appExecutableInTree(appPath) {
  return appPlatform.appExecutableInTree(appPath);
}

export function appDaemonInTree(appPath) {
  return appPlatform.appDaemonInTree(appPath);
}

export function appBuildIdentityInTree(appPath) {
  return appPlatform.appBuildIdentityInTree(appPath);
}
