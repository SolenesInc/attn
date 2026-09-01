import fs from 'node:fs';
import path from 'node:path';
import { execFile, execFileSync, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { LinuxDriver } from './linuxDriver.mjs';
import { MacOSDriver } from './macosDriver.mjs';

const execFileAsync = promisify(execFile);

// The harness uses its own bridges; a broken desktop bus blocked WebKitGTK
// before Tauri setup for over 45 seconds.
const UNAVAILABLE_DESKTOP_BUS_ADDRESS = 'unix:path=/dev/null';

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

const darwinPlatform = {
  os: 'darwin',

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

  createWindowDriver(options) {
    return new MacOSDriver(options);
  },

  // The AX set-position call also nudges the WebView out of the
  // off-screen-init throttle state it otherwise enters.
  async placeWindow(driver, { parkPx }) {
    if (Number.isInteger(parkPx) && parkPx > 0) {
      await driver.parkWindow(parkPx);
    }
  },

  readClipboard() {
    try {
      return execFileSync('pbpaste', { encoding: 'utf8' });
    } catch {
      return '';
    }
  },

  writeClipboard(text) {
    execFileSync('pbcopy', { input: text });
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

  appExecutableInTree(appPath) {
    return path.join(appPath, 'bin', 'attn-app');
  },

  appDaemonInTree(appPath) {
    return path.join(appPath, 'bin', 'attn');
  },

  appBuildIdentityInTree(appPath) {
    return path.join(appPath, 'resources', 'build-identity.json');
  },

  createWindowDriver(options) {
    return new LinuxDriver(options);
  },

  // Without a window manager (Xvfb) GTK opens the window at its 800x600
  // minimum; two split panes need the configured 1200x800 to both stay live.
  async placeWindow(driver, { pid }) {
    await driver.waitForWindowTitled(driver.appName, { timeoutMs: 10_000 });
    await driver.setWindowBounds({ x: 0, y: 0, width: 1200, height: 800 }, { pid });
  },

  readClipboard() {
    try {
      return execFileSync('xclip', ['-selection', 'clipboard', '-o'], { encoding: 'utf8' });
    } catch {
      return '';
    }
  },

  writeClipboard(text) {
    // xclip forks a holder child that inherits stdio; ignoring it keeps execFileSync from hanging.
    execFileSync('xclip', ['-selection', 'clipboard', '-in'], { input: text, stdio: ['pipe', 'ignore', 'ignore'] });
  },

  launchEnvironment(env = null) {
    return {
      DBUS_SESSION_BUS_ADDRESS: UNAVAILABLE_DESKTOP_BUS_ADDRESS,
      AT_SPI_BUS_ADDRESS: UNAVAILABLE_DESKTOP_BUS_ADDRESS,
      ...env,
    };
  },

  async launchApp({ appPath, env = null }) {
    return spawnDetached(this.appExecutableInTree(appPath), this.launchEnvironment(env));
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

export function createWindowDriver(options = {}) {
  return appPlatform.createWindowDriver(options);
}

export function appExecutableInTree(appPath) {
  return appPlatform.appExecutableInTree(appPath);
}

export function appDaemonInTree(appPath) {
  return appPlatform.appDaemonInTree(appPath);
}

export function appBuildIdentityInTree(appPath) {
  return appPlatform.appBuildIdentityInTree(appPath);
}

export { delay };
