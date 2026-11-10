import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { appCliInTree, appPlatform } from './platform.mjs';
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

function attnBinaryPath(appPath) {
  return appCliInTree(appPath);
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

export function registeredPtyHostPids({ dataDir, commandLineFor = commandLineForPid }) {
  const hostsRoot = `${path.join(dataDir, 'pty-hosts')}${path.sep}`;
  return [...new Set(registeredPids(dataDir, 'pty-hosts', 'hosts', 'host_pid'))]
    .filter((pid) => commandLineFor(pid).startsWith(hostsRoot));
}

export function instanceProcesses({ appPath, dataDir, commandLineFor = commandLineForPid }) {
  const attn = attnBinaryPath(appPath);
  const running = {
    app: runningPids([readPid(readFileOrEmpty(path.join(dataDir, 'app.pid')))], appExecutableInAppTree(appPath), commandLineFor),
    daemon: runningPids([readPid(readFileOrEmpty(path.join(dataDir, 'attn.pid')))], attn, commandLineFor),
    'pty-worker': runningPids(registeredPids(dataDir, 'workers', 'registry', 'worker_pid'), attn, commandLineFor),
    'attn-pty-host': registeredPtyHostPids({ dataDir, commandLineFor }),
  };
  return Object.entries(running).flatMap(([name, pids]) => pids.map((pid) => ({ name, pid })));
}

async function sleep(ms) {
  return new Promise((resolve) => { setTimeout(resolve, ms); });
}

export function resetInstanceData({ instance, appPath }) {
  assertFreshWorldTargetSafe({ instance, appPath });
  const result = spawnSync(attnBinaryPath(appPath), ['instance', 'clean', instance, '--keep-app'], {
    env: instanceCliEnv(instance),
    encoding: 'utf8',
  });
  if (result.status !== 0) {
    throw new Error(`attn instance clean ${instance} --keep-app failed: ${result.error?.message ?? result.stderr}`);
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

  log(`stopping daemon and terminals for instance '${instance}'`);
  const stopResult = spawnSync(attnBinaryPath(appPath), ['daemon', 'stop', '--terminals'], {
    env: instanceCliEnv(instance),
    encoding: 'utf8',
  });
  for (const line of `${stopResult.stdout ?? ''}`.split('\n').filter(Boolean)) log(line.trim());
  if (stopResult.status !== 0) {
    throw new Error(`fresh-world preflight failed: attn daemon stop --terminals exited ${stopResult.status}: ${stopResult.error?.message ?? stopResult.stderr}`);
  }
  for (const name of ['pty-worker', 'attn-pty-host']) {
    const leaked = processes().filter((entry) => entry.name === name).map(({ pid }) => pid);
    if (leaked.length > 0) {
      throw new Error(`fresh-world preflight failed: ${name} pids=[${leaked.join(', ')}] still run after attn daemon stop --terminals; check each with ps and stop it by pid`);
    }
    log(`no leaked ${name} processes found`);
  }

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

  log(`fresh world ready: appWasRunning=${appWasRunning}`);
  return { appWasRunning };
}
