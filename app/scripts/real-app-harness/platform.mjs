import fs from 'node:fs';
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
  return { spawned: true, pid: Number.isInteger(child.pid) ? child.pid : null, child };
}

function positivePid(value) {
  return Number.isInteger(value) && value > 0 ? value : null;
}

function resolvedPath(candidate) {
  try {
    return fs.realpathSync(candidate);
  } catch {
    return candidate;
  }
}

// Mirrors sameExecutable in cmd/attn/profile.go: /proc/<pid>/exe is already
// resolved, so a symlinked install root matches only once both sides are.
function sameExecutable(a, b) {
  return a === b || resolvedPath(a) === resolvedPath(b);
}

const DELETED_IMAGE_SUFFIX = ' (deleted)';

function executableOfPid(pid, procRoot) {
  const exeLink = path.join(procRoot, String(pid), 'exe');
  try {
    return fs.realpathSync(exeLink);
  } catch {}
  try {
    // An app running out of a replaced install tree is still ours: Linux marks
    // the unlinked image and realpath fails, the path stays the one we spawned.
    const raw = fs.readlinkSync(exeLink);
    return raw.endsWith(DELETED_IMAGE_SUFFIX) ? raw.slice(0, -DELETED_IMAGE_SUFFIX.length) : raw;
  } catch {
    return null;
  }
}

// Node reaps the child it spawned, so an unexited handle proves the pid still
// names that process rather than a stranger that reused the number.
function spawnedOwnedPid(launch) {
  if (!launch?.spawned) {
    return null;
  }
  const pid = positivePid(launch.pid);
  if (!pid) {
    return null;
  }
  const child = launch.child;
  if (child && (child.exitCode !== null || child.signalCode !== null)) {
    return null;
  }
  return pid;
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

  // `open`, osascript, and pgrep address the bundle, so the spawn pid adds
  // nothing here and the manifest pid is only ever a hint for the wait loop.
  ownedPids({ manifestPid = null }) {
    const pid = positivePid(manifestPid);
    return { pids: pid ? [pid] : [], staleManifest: false };
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

  async requestQuit({ pids = [] }) {
    for (const pid of pids) {
      if (!positivePid(pid)) {
        continue;
      }
      try {
        process.kill(pid, 'SIGTERM');
      } catch {
      }
    }
  },

  // The spawn is this run's own evidence; a manifest pid outlives the app that
  // wrote it, so it is signalled only while it still runs our executable.
  ownedPids({ appPath, manifestPid = null, launch = null, procRoot = '/proc' }) {
    const spawned = spawnedOwnedPid(launch);
    const pids = spawned ? [spawned] : [];
    const claimed = positivePid(manifestPid);
    if (!claimed || claimed === spawned) {
      return { pids, staleManifest: false };
    }
    const exe = executableOfPid(claimed, procRoot);
    if (exe && sameExecutable(exe, this.appExecutableInTree(appPath))) {
      pids.push(claimed);
      return { pids, staleManifest: false };
    }
    return { pids, staleManifest: true };
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
