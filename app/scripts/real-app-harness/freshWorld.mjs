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
  appExecutableInAppTree,
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

const BACKENDS_WHOSE_TERMINALS_OUTLIVE_THE_DAEMON = new Set(['worker', 'shared', 'migrating']);

export function assertDaemonStopKeepsTerminals({ instance, appPath, run = execFileSync }) {
  const label = JSON.stringify(instance || 'production');
  let backend;
  try {
    const answer = run(attnBinaryPath(appPath), ['settings', 'get', 'pty_backend_mode', '--json'], {
      encoding: 'utf8',
      env: instanceCliEnv(instance),
      stdio: 'pipe',
    });
    backend = String(JSON.parse(answer).value ?? '');
  } catch (error) {
    throw new Error(
      `refusing to stop the ${label} daemon: could not read its pty_backend_mode `
      + `(${error?.message || error}), so it cannot be proved that its terminals survive the stop.`,
    );
  }
  if (!BACKENDS_WHOSE_TERMINALS_OUTLIVE_THE_DAEMON.has(backend)) {
    throw new Error(
      `refusing to stop the ${label} daemon: its pty_backend_mode is ${JSON.stringify(backend)}, `
      + `so stopping it ends every terminal it hosts. Save their work and stop it yourself `
      + `(ATTN_INSTANCE=${instance} attn daemon stop), or run against an instance whose terminals `
      + `outlive the daemon (${[...BACKENDS_WHOSE_TERMINALS_OUTLIVE_THE_DAEMON].join(', ')}).`,
    );
  }
}

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

function commandLineForPid(pid) {
  const result = spawnSync('ps', ['-o', 'command=', '-p', String(pid)], { encoding: 'utf8' });
  return (result.stdout || '').trim();
}

export function commandRunsExecutable(command, executablePath) {
  return command === executablePath || command.startsWith(`${executablePath} `);
}

function readJson(filePath) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch {
    return null;
  }
}

function readFileOrEmpty(filePath) {
  try {
    return fs.readFileSync(filePath, 'utf8').trim();
  } catch {
    return '';
  }
}

function readPid(value) {
  const pid = Number(value);
  return Number.isInteger(pid) && pid > 1 ? pid : null;
}

function registeredPids(dataDir, root, registry, field) {
  const rootDir = path.join(dataDir, root);
  if (!fs.existsSync(rootDir)) return [];
  const pids = [];
  for (const daemonInstance of fs.readdirSync(rootDir)) {
    const registryDir = path.join(rootDir, daemonInstance, registry);
    if (!fs.existsSync(registryDir)) continue;
    for (const name of fs.readdirSync(registryDir)) {
      if (!name.endsWith('.json')) continue;
      const pid = readPid(readJson(path.join(registryDir, name))?.[field]);
      if (pid) pids.push(pid);
    }
  }
  return pids;
}

function runningPids(pids, executablePath, commandLineFor) {
  return [...new Set(pids.filter(Boolean))].filter((pid) => commandRunsExecutable(commandLineFor(pid), executablePath));
}

export function registeredPtyHostPids({ dataDir, executablePath, commandLineFor = commandLineForPid }) {
  return runningPids(registeredPids(dataDir, 'pty-hosts', 'hosts', 'host_pid'), executablePath, commandLineFor);
}

export function instanceProcesses({ appPath, dataDir, commandLineFor = commandLineForPid }) {
  const attn = attnBinaryPath(appPath);
  const running = {
    app: runningPids([readPid(readFileOrEmpty(path.join(dataDir, 'app.pid')))], appExecutableInAppTree(appPath), commandLineFor),
    daemon: runningPids([readPid(readFileOrEmpty(path.join(dataDir, 'attn.pid')))], attn, commandLineFor),
    'pty-worker': runningPids(registeredPids(dataDir, 'workers', 'registry', 'worker_pid'), attn, commandLineFor),
    'attn-pty-host': registeredPtyHostPids({ dataDir, executablePath: ptyHostBinaryPath(appPath), commandLineFor }),
  };
  return Object.entries(running).flatMap(([name, pids]) => pids.map((pid) => ({ name, pid })));
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

  const processes = () => instanceProcesses({ appPath, dataDir });
  const appWasRunning = processes().some(({ name }) => name === 'app');
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

  const leaked = processes();
  const leakedPids = (name) => leaked.filter((entry) => entry.name === name).map((entry) => entry.pid);
  const leakedWorkerPids = leakedPids('pty-worker');
  const leakedPtyHostPids = leakedPids('attn-pty-host');
  await terminateProcesses({ pids: leakedWorkerPids, name: 'pty-worker', log });
  await terminateProcesses({ pids: leakedPtyHostPids, name: 'attn-pty-host', log });

  const deadline = Date.now() + timeoutMs;
  let survivors = processes();
  while (survivors.length > 0 && Date.now() < deadline) {
    await sleep(200);
    survivors = processes();
  }
  if (survivors.length > 0) {
    const detail = survivors.map(({ name, pid }) => `${name} ${pid}: ${commandLineForPid(pid)}`).join('; ');
    throw new Error(`fresh-world preflight failed: processes survived cleanup — ${detail}`);
  }

  const summary = {
    appWasRunning,
    daemonStopped,
    leakedWorkersKilled: leakedWorkerPids.length,
    leakedPtyHostsKilled: leakedPtyHostPids.length,
  };
  log(`fresh world ready: appWasRunning=${summary.appWasRunning} daemonStopped=${summary.daemonStopped} leakedWorkersKilled=${summary.leakedWorkersKilled} leakedPtyHostsKilled=${summary.leakedPtyHostsKilled}`);
  return summary;
}
