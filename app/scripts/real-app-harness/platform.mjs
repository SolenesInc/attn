import fs from 'node:fs';
import path from 'node:path';
import { execFileSync, spawn } from 'node:child_process';
import { LinuxDriver } from './linuxDriver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { instanceCliEnv, instanceForAppPath } from './harnessInstance.mjs';

// The harness uses its own bridges; a broken desktop bus blocked WebKitGTK
// before Tauri setup for over 45 seconds.
const UNAVAILABLE_DESKTOP_BUS_ADDRESS = 'unix:path=/dev/null';

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function spawnDetached(executablePath, env, appPath, logPath) {
  const logFd = fs.openSync(logPath, 'wx');
  let child;
  try {
    child = spawn(executablePath, [], {
      detached: true,
      stdio: ['ignore', logFd, logFd],
      env: instanceCliEnv(instanceForAppPath(appPath), env ?? {}),
    });
  } finally {
    fs.closeSync(logFd);
  }
  const exited = new Promise((resolve) => {
    child.once('exit', (code, signal) => resolve({ code, signal }));
    child.once('error', (error) => resolve({ code: null, signal: null, error }));
  });
  child.unref();
  return { spawned: true, pid: Number.isInteger(child.pid) ? child.pid : null, child, exited, logPath };
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

// Mirrors sameExecutable in cmd/attn/instance.go: /proc/<pid>/exe is already
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

  async launchApp({ appPath, env = null, logPath }) {
    return spawnDetached(this.appExecutableInTree(appPath), env, appPath, logPath);
  },

  async requestQuit({ bundleId }) {
    await new MacOSDriver({ bundleId }).runInputDriver(['quit_wait']);
  },

  // Only an unexited child handle authorizes signal escalation on macOS.
  ownedPids({ manifestPid = null, launch = null }) {
    const pid = spawnedOwnedPid(launch);
    return { pids: pid ? [pid] : [], staleManifest: Boolean(manifestPid && manifestPid !== pid) };
  },

  async listAppPids() {
    return [];
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

  // xclip exits non-zero on an empty selection, so only a missing binary is
  // reported: a silent '' there reads as "the copy did not happen".
  readClipboard() {
    try {
      return execFileSync('xclip', ['-selection', 'clipboard', '-o'], { encoding: 'utf8' });
    } catch (error) {
      if (error?.code === 'ENOENT') {
        throw new Error('xclip is required to read the clipboard on Linux; install it on the runner');
      }
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

  async launchApp({ appPath, env = null, logPath }) {
    return spawnDetached(this.appExecutableInTree(appPath), this.launchEnvironment(env), appPath, logPath);
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
