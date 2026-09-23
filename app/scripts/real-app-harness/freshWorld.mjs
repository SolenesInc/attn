import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { appDaemonInTree, appPlatform } from './platform.mjs';
import {
  bundleIdentifierForInstance,
  currentHarnessInstance,
  dataDirForInstance,
  defaultAppPathForInstance,
  isProductionHarnessTarget,
  instanceCliEnv,
} from './harnessInstance.mjs';

export function assertFreshWorldTargetSafe({ instance, appPath } = {}) {
  if (!instance) {
    throw new Error(`fresh-world preflight refused: instance is empty/falsy (instance=${JSON.stringify(instance)}).`);
  }
  if (!appPath) {
    throw new Error(`fresh-world preflight refused: appPath is empty/falsy (appPath=${JSON.stringify(appPath)}).`);
  }
  // 'default' is the alias for the production instance; isProductionHarnessTarget
  // only checks instance === '', so it would let a 'default' target through.
  const normalizedInstance = instance.trim().toLowerCase();
  if (normalizedInstance === '' || normalizedInstance === 'default') {
    throw new Error(
      `fresh-world preflight refused: instance ${JSON.stringify(instance)} is the production alias `
      + '(\'default\' collapses to production). Refusing to quit/scrub a production app or daemon.',
    );
  }
  if (isProductionHarnessTarget({ instance, appPath })) {
    throw new Error(
      `fresh-world preflight refused: target looks like production (instance=${JSON.stringify(instance)}, `
      + `appPath=${JSON.stringify(appPath)}). Refusing to quit/scrub a production app or daemon.`,
    );
  }
}

// Matching keys on this full app path, never a bare "pty-worker" pattern, so one
// instance's cleanup can never touch another instance's or production's workers.
function attnBinaryPath(appPath) {
  return appDaemonInTree(appPath);
}

function ptyHostBinaryPath(appPath) {
  return path.join(path.dirname(attnBinaryPath(appPath)), 'attn-pty-host');
}

function requestAppQuit({ instance, appPath, bundleId }) {
  if (appPlatform.os === 'darwin') {
    try {
      execFileSync('osascript', ['-e', `tell application id "${bundleId}" to quit`], { stdio: 'pipe' });
    } catch {
    }
    return;
  }
  spawnSync(attnBinaryPath(appPath), ['instance', 'stop-app', '--instance', instance], {
    env: instanceCliEnv(instance),
    stdio: 'pipe',
  });
}

// pgrep -f exits 1 on no matches; that is "no pids", not an error.
function pgrepFullCommand(pattern) {
  const result = spawnSync('pgrep', ['-f', pattern], { encoding: 'utf8' });
  if (result.status !== 0 && result.status !== 1) {
    throw new Error(`pgrep -f ${JSON.stringify(pattern)} failed: ${result.stderr || result.status}`);
  }
  return (result.stdout || '')
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => Number(line))
    .filter((pid) => Number.isInteger(pid));
}

function commandLineForPid(pid) {
  const result = spawnSync('ps', ['-o', 'command=', '-p', String(pid)], { encoding: 'utf8' });
  return (result.stdout || '').trim();
}

function findLeakedWorkerPids(appPath) {
  const binPath = attnBinaryPath(appPath);
  return pgrepFullCommand(binPath).filter((pid) => commandLineForPid(pid).includes('pty-worker'));
}

export function commandRunsExecutable(command, executablePath) {
  return command === executablePath || command.startsWith(`${executablePath} `);
}

export function registeredPtyHostPids({ dataDir, executablePath, commandLineFor = commandLineForPid }) {
  const hostsRoot = path.join(dataDir, 'pty-hosts');
  if (!fs.existsSync(hostsRoot)) return [];
  const pids = new Set();
  for (const daemonInstance of fs.readdirSync(hostsRoot)) {
    const hostsDir = path.join(hostsRoot, daemonInstance, 'hosts');
    if (!fs.existsSync(hostsDir)) continue;
    for (const name of fs.readdirSync(hostsDir)) {
      if (!name.endsWith('.json')) continue;
      let entry;
      try {
        entry = JSON.parse(fs.readFileSync(path.join(hostsDir, name), 'utf8'));
      } catch {
        continue;
      }
      const pid = Number(entry.host_pid);
      if (!Number.isInteger(pid) || pid <= 1) continue;
      if (commandRunsExecutable(commandLineFor(pid), executablePath)) pids.add(pid);
    }
  }
  return [...pids];
}

function findAnySurvivingPids(appPath) {
  return pgrepFullCommand(attnBinaryPath(appPath));
}

async function sleep(ms) {
  return new Promise((resolve) => { setTimeout(resolve, ms); });
}

async function terminateProcesses({ pids, name, log }) {
  if (pids.length === 0) {
    log(`no leaked ${name} processes found`);
    return;
  }

  log(`leaked ${name} pids=[${pids.join(', ')}] from a previous run — killing`);
  for (const pid of pids) {
    try {
      process.kill(pid, 'SIGTERM');
    } catch {
    }
  }
  await sleep(2_000);
  for (const pid of pids) {
    try {
      process.kill(pid, 0);
      log(`${name} pid=${pid} survived SIGTERM — sending SIGKILL`);
      process.kill(pid, 'SIGKILL');
    } catch {
    }
  }
}

export async function ensureFreshWorld({
  instance = currentHarnessInstance(),
  appPath = defaultAppPathForInstance(instance),
  dataDir = dataDirForInstance(instance),
  log = (m) => console.log(`[fresh-world] ${m}`),
  timeoutMs = 20_000,
} = {}) {
  assertFreshWorldTargetSafe({ instance, appPath });

  const appWasRunning = findAnySurvivingPids(appPath).length > 0;
  const bundleId = bundleIdentifierForInstance(instance);

  log(`quitting app bundle ${bundleId}${appWasRunning ? ' (was running)' : ' (not running)'}`);
  requestAppQuit({ instance, appPath, bundleId });

  let daemonStopped = false;
  log(`stopping daemon for instance '${instance}'`);
  const stopResult = spawnSync(attnBinaryPath(appPath), ['daemon', 'stop'], {
    env: instanceCliEnv(instance),
    encoding: 'utf8',
  });
  if (stopResult.error) {
    log(`daemon stop could not run: ${stopResult.error.message} (continuing — daemon may already be down)`);
  } else if (stopResult.status === 0) {
    daemonStopped = true;
  } else {
    log(`daemon stop exited ${stopResult.status} (no daemon running is expected here)`);
  }

  const leakedPids = findLeakedWorkerPids(appPath);
  const leakedPtyHostPids = registeredPtyHostPids({ dataDir, executablePath: ptyHostBinaryPath(appPath) });
  await terminateProcesses({ pids: leakedPids, name: 'pty-worker', log });
  await terminateProcesses({ pids: leakedPtyHostPids, name: 'attn-pty-host', log });

  const deadline = Date.now() + timeoutMs;
  let survivors = findAnySurvivingPids(appPath);
  while (survivors.length > 0 && Date.now() < deadline) {
    await sleep(200);
    survivors = findAnySurvivingPids(appPath);
  }
  if (survivors.length > 0) {
    const detail = survivors.map((pid) => `${pid}: ${commandLineForPid(pid)}`).join('; ');
    throw new Error(`fresh-world preflight failed: processes survived cleanup — ${detail}`);
  }

  const summary = {
    appWasRunning,
    daemonStopped,
    leakedWorkersKilled: leakedPids.length,
    leakedPtyHostsKilled: leakedPtyHostPids.length,
  };
  log(`fresh world ready: appWasRunning=${summary.appWasRunning} daemonStopped=${summary.daemonStopped} leakedWorkersKilled=${summary.leakedWorkersKilled} leakedPtyHostsKilled=${summary.leakedPtyHostsKilled}`);
  return summary;
}
