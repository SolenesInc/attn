import { execFileSync, spawnSync } from 'node:child_process';
import { appDaemonInTree, appPlatform } from './platform.mjs';
import {
  bundleIdentifierForProfile,
  currentHarnessProfile,
  defaultAppPathForProfile,
  isProductionHarnessTarget,
} from './harnessProfile.mjs';

export function assertFreshWorldTargetSafe({ profile, appPath } = {}) {
  if (!profile) {
    throw new Error(`fresh-world preflight refused: profile is empty/falsy (profile=${JSON.stringify(profile)}).`);
  }
  if (!appPath) {
    throw new Error(`fresh-world preflight refused: appPath is empty/falsy (appPath=${JSON.stringify(appPath)}).`);
  }
  // 'default' is the alias for the production profile; isProductionHarnessTarget
  // only checks profile === '', so it would let a 'default' target through.
  const normalizedProfile = profile.trim().toLowerCase();
  if (normalizedProfile === '' || normalizedProfile === 'default') {
    throw new Error(
      `fresh-world preflight refused: profile ${JSON.stringify(profile)} is the production alias `
      + '(\'default\' collapses to production). Refusing to quit/scrub a production app or daemon.',
    );
  }
  if (isProductionHarnessTarget({ profile, appPath })) {
    throw new Error(
      `fresh-world preflight refused: target looks like production (profile=${JSON.stringify(profile)}, `
      + `appPath=${JSON.stringify(appPath)}). Refusing to quit/scrub a production app or daemon.`,
    );
  }
}

// Matching keys on this full app path, never a bare "pty-worker" pattern, so one
// profile's cleanup can never touch another profile's or production's workers.
function attnBinaryPath(appPath) {
  return appDaemonInTree(appPath);
}

function requestAppQuit({ profile, appPath, bundleId }) {
  if (appPlatform.os === 'darwin') {
    try {
      execFileSync('osascript', ['-e', `tell application id "${bundleId}" to quit`], { stdio: 'pipe' });
    } catch {
    }
    return;
  }
  spawnSync(attnBinaryPath(appPath), ['profile', 'stop-app', '--profile', profile], { stdio: 'pipe' });
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

function findAnySurvivingPids(appPath) {
  return pgrepFullCommand(attnBinaryPath(appPath));
}

async function sleep(ms) {
  return new Promise((resolve) => { setTimeout(resolve, ms); });
}

export async function ensureFreshWorld({
  profile = currentHarnessProfile(),
  appPath = defaultAppPathForProfile(profile),
  log = (m) => console.log(`[fresh-world] ${m}`),
  timeoutMs = 20_000,
} = {}) {
  assertFreshWorldTargetSafe({ profile, appPath });

  const appWasRunning = findAnySurvivingPids(appPath).length > 0;
  const bundleId = bundleIdentifierForProfile(profile);

  log(`quitting app bundle ${bundleId}${appWasRunning ? ' (was running)' : ' (not running)'}`);
  requestAppQuit({ profile, appPath, bundleId });

  let daemonStopped = false;
  log(`stopping daemon for profile '${profile}'`);
  const stopResult = spawnSync(attnBinaryPath(appPath), ['daemon', 'stop'], {
    env: { ...process.env, ATTN_PROFILE: profile },
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
  if (leakedPids.length > 0) {
    log(`leaked pty-worker pids=[${leakedPids.join(', ')}] from a previous run — killing`);
    for (const pid of leakedPids) {
      try {
        process.kill(pid, 'SIGTERM');
      } catch {
      }
    }
    await sleep(2_000);
    for (const pid of leakedPids) {
      try {
        process.kill(pid, 0);
        log(`pty-worker pid=${pid} survived SIGTERM — sending SIGKILL`);
        process.kill(pid, 'SIGKILL');
      } catch {
      }
    }
  } else {
    log('no leaked pty-worker processes found');
  }

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
  };
  log(`fresh world ready: appWasRunning=${summary.appWasRunning} daemonStopped=${summary.daemonStopped} leakedWorkersKilled=${summary.leakedWorkersKilled}`);
  return summary;
}
