import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import os from 'node:os';
import { randomUUID } from 'node:crypto';
import { agentTripwireLaunchEnv } from './agentTripwire.mjs';
import { mockGitHubLaunchEnv } from './mockGitHub.mjs';
import { appPlatform } from './platform.mjs';
import {
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  defaultAppPathForInstance,
  manifestPathForInstance,
  instanceForAppPath,
} from './harnessInstance.mjs';


function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function isTransientManifestReadError(error) {
  if (!error) {
    return false;
  }
  if (error instanceof SyntaxError) {
    return true;
  }
  if (typeof error === 'object' && error && 'code' in error) {
    return error.code === 'ENOENT';
  }
  return false;
}

export class UiAutomationClient {
  constructor({
    appPath = defaultAppPathForInstance(),
    manifestPath = null,
    launchEnv = null,
    bundleId = null,
    platform = appPlatform,
    artifactsDir = process.env.ATTN_REAL_APP_ARTIFACTS_DIR || path.join(os.tmpdir(), 'attn-real-app-harness'),
  } = {}) {
    const appInstance = instanceForAppPath(appPath);
    const resolvedBundleId = bundleId || bundleIdentifierForAppPath(appPath);
    assertProductionRunAllowed({ appPath, bundleId: resolvedBundleId });
    this.appPath = appPath;
    this.manifestPath = manifestPath || manifestPathForInstance(appInstance);
    this.launchEnv = launchEnv;
    this.bundleId = resolvedBundleId;
    this.platform = platform;
    this.artifactsDir = artifactsDir;
    this.launch = null;
  }

  async launchApp() {
    const alwaysOnTop = process.env.ATTN_HARNESS_ALWAYS_ON_TOP !== '0';
    const harnessDaemonEnv = { ...agentTripwireLaunchEnv(), ...mockGitHubLaunchEnv() };
    // Park the attn window off-screen by default so scenarios don't cover the
    // caller's work. Opt out with ATTN_HARNESS_PARK_VISIBLE_PX=0.
    const HARNESS_PARK_VISIBLE_PX_DEFAULT = '20';
    const parkPxStr = process.env.ATTN_HARNESS_PARK_VISIBLE_PX ?? HARNESS_PARK_VISIBLE_PX_DEFAULT;
    const effectiveLaunchEnv = alwaysOnTop
      ? { ...harnessDaemonEnv, ...this.launchEnv, ATTN_HARNESS_ALWAYS_ON_TOP: '1' }
      : { ...harnessDaemonEnv, ...this.launchEnv };

    const focusDriver = this.#focusDriver();
    fs.mkdirSync(this.artifactsDir, { recursive: true });
    const logPath = path.resolve(this.artifactsDir, `app-launch-${randomUUID()}.log`);
    const launched = await this.platform.launchApp({
      appPath: this.appPath,
      env: effectiveLaunchEnv,
      logPath,
    });
    this.launch = launched;

    await focusDriver.waitForMainWindow(10_000, 150, { pid: launched.pid }).catch(() => null);
    this.#assertAppRunning();
    const parkPx = alwaysOnTop ? Number.parseInt(parkPxStr || '', 10) : 0;
    await this.platform.placeWindow(focusDriver, { parkPx, pid: launched.pid }).catch((error) => {
      console.warn(`[RealAppHarness] Window placement failed: ${error?.message || error}`);
    });
  }

  #focusDriver() {
    if (!this._focusDriver) {
      this._focusDriver = this.platform.createWindowDriver({ bundleId: this.bundleId, appPath: this.appPath, client: this });
    }
    return this._focusDriver;
  }


  async quitApp(timeoutMs = 10_000) {
    let manifestPid = null;

    try {
      manifestPid = this.readManifest()?.pid ?? null;
    } catch {
      manifestPid = null;
    }

    const { pids: ownedPids, staleManifest } = this.#ownedPids(manifestPid);
    if (staleManifest) {
      this.#removeManifest();
    }

    await this.platform.requestQuit({ bundleId: this.bundleId, pids: ownedPids });

    const startedAt = Date.now();
    while (Date.now() - startedAt < timeoutMs) {
      const stillOwned = this.#ownedPids(manifestPid).pids;
      if (!stillOwned.some(processExists)) {
        this.launch = null;
        return;
      }
      await delay(200);
    }

    let escalating = this.#stillOwnedPids(manifestPid, ownedPids);
    for (const pid of livePids(escalating)) {
      try {
        process.kill(pid, 'SIGTERM');
      } catch {}
    }
    await delay(500);
    escalating = this.#stillOwnedPids(manifestPid, escalating);
    for (const pid of livePids(escalating)) {
      try {
        process.kill(pid, 'SIGKILL');
      } catch {}
    }
    this.launch = null;
  }

  #ownedPids(manifestPid) {
    return this.platform.ownedPids({ appPath: this.appPath, manifestPid, launch: this.launch });
  }

  // A pid that stopped passing the ownership fence may already name a stranger
  // that reused the number, so escalation drops it instead of signalling it.
  #stillOwnedPids(manifestPid, previous) {
    const { pids } = this.#ownedPids(manifestPid);
    for (const pid of previous) {
      if (!pids.includes(pid)) {
        console.warn(`[RealAppHarness] pid ${pid} is no longer ours; skipping escalation signal.`);
      }
    }
    return pids;
  }

  #removeManifest() {
    try {
      fs.unlinkSync(this.manifestPath);
    } catch {}
  }

  async launchFreshApp() {
    await this.quitApp();
    this.#removeManifest();
    await this.launchApp();
  }

  // The app was replaced by something other than this client, such as an update that relaunches it.
  forgetLaunchedApp() {
    this.launch = null;
  }

  async waitForManifest(timeoutMs = 15_000) {
    const startedAt = Date.now();
    let lastError = null;
    const appExited = this.launch?.exited;

    while (Date.now() - startedAt < timeoutMs) {
      this.#assertAppRunning();
      try {
        const manifest = this.readManifest();
        if (manifest?.enabled && manifest.port && manifest.token && processExists(manifest.pid)) {
          return manifest;
        }
      } catch (error) {
        lastError = error;
      }
      if (appExited) {
        const exit = await Promise.race([delay(200), appExited]);
        if (exit) throw this.#appExitError(exit);
      } else {
        await delay(200);
      }
    }

    throw new Error(
      `Timed out waiting for UI automation manifest after ${timeoutMs}ms at ${this.manifestPath}: ${lastError instanceof Error ? lastError.message : lastError || 'manifest unavailable'}`
    );
  }

  #appExitError({ code, signal, error = null }) {
    return new Error(
      `Packaged app exited before UI automation became ready: exit code=${code}, signal=${signal}, log=${this.launch.logPath}${error ? `: ${error.message}` : ''}`
    );
  }

  #assertAppRunning() {
    const child = this.launch?.child;
    if (child && (child.exitCode !== null || child.signalCode !== null)) {
      throw this.#appExitError({ code: child.exitCode, signal: child.signalCode });
    }
  }

  readManifest() {
    return JSON.parse(fs.readFileSync(this.manifestPath, 'utf8'));
  }

  async request(action, payload = {}, { timeoutMs = 35_000 } = {}) {
    const requestId = `ui-automation-client-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    let manifest;

    try {
      manifest = this.readManifest();
    } catch (error) {
      if (!isTransientManifestReadError(error)) {
        throw error;
      }
      await this.waitForManifest(Math.min(5_000, timeoutMs));
      manifest = this.readManifest();
    }

    const request = {
      id: requestId,
      token: manifest.token,
      action,
      payload,
    };

    return new Promise((resolve, reject) => {
      const socket = net.createConnection({
        host: '127.0.0.1',
        port: manifest.port,
      });

      let buffer = '';
      const timeout = setTimeout(() => {
        cleanup();
        reject(new Error(`Automation request timed out: ${action} (${requestId}) after ${timeoutMs}ms`));
      }, timeoutMs);
      const cleanup = () => {
        clearTimeout(timeout);
        socket.removeAllListeners();
        socket.destroy();
      };

      socket.on('connect', () => {
        socket.write(`${JSON.stringify(request)}\n`);
      });

      socket.on('data', (chunk) => {
        buffer += chunk.toString();
        const newlineIndex = buffer.indexOf('\n');
        if (newlineIndex === -1) {
          return;
        }
        const line = buffer.slice(0, newlineIndex).trim();
        cleanup();
        try {
          const response = JSON.parse(line);
          if (!response.ok) {
            const detail = response.error || 'unknown error';
            reject(new Error(`Automation request failed: ${action} (${requestId}): ${detail}`));
            return;
          }
          resolve(response.result);
        } catch (error) {
          reject(error);
        }
      });

      socket.on('error', (error) => {
        cleanup();
        reject(error);
      });
    });
  }

  async waitForReady(timeoutMs = 20_000) {
    const startedAt = Date.now();
    let lastError = null;

    while (Date.now() - startedAt < timeoutMs) {
      try {
        const result = await this.request('ping');
        if (result?.frontendReady) {
          return result;
        }
      } catch (error) {
        lastError = error;
      }
      await delay(250);
    }

    throw new Error(
      `Timed out waiting for app automation readiness: ${lastError instanceof Error ? lastError.message : lastError || 'bridge not ready'}`
    );
  }
}

function livePids(candidates) {
  return new Set(candidates.filter((value) => processExists(value)));
}

function processExists(pid) {
  if (!Number.isInteger(pid) || pid <= 0) {
    return false;
  }

  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}
