import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import {
  appExecutableInAppTree,
  defaultAppPathForProfile,
  manifestPathForProfile,
  profileForAppPath,
} from './harnessProfile.mjs';

export const ALWAYS_ON_TOP_VAR = 'ATTN_HARNESS_ALWAYS_ON_TOP';

const OPT_OUT_FIX = `Set process.env.${ALWAYS_ON_TOP_VAR} = '0' before the app launch in this scenario, as the `
  + 'other key-pressing scenarios do, and the window takes focus for the run.';

// `ps eww -o command=` prints the executable, its arguments, then its
// environment, so one read answers both which app this is and how it launched.
function readAppCommand(pid) {
  return String(execFileSync('ps', ['eww', '-p', String(pid), '-o', 'command='], { encoding: 'utf8' })).trim();
}

function appPidFromManifest(manifestPath) {
  const pid = JSON.parse(fs.readFileSync(manifestPath, 'utf8'))?.pid;
  if (!Number.isInteger(pid) || pid <= 0) {
    return null;
  }
  process.kill(pid, 0);
  return pid;
}

// tao answers set_focusable(false) with canBecomeKeyWindow: false, so the
// always-on-top window never becomes key and AppKit drops every key event.
export function keyInputVerdict({
  platform = process.platform,
  appRunning = false,
  appCommand = null,
  appExecutable = '',
} = {}) {
  if (platform !== 'darwin') {
    return { reaches: true, reason: `${platform} leaves the window focusable` };
  }
  if (!appRunning) {
    return { reaches: true, reason: 'no launched app is running to press at' };
  }
  if (typeof appCommand !== 'string' || appCommand === '') {
    return {
      reaches: false,
      reason: `its environment could not be read, so whether ${ALWAYS_ON_TOP_VAR}=1 left the window `
        + 'non-focusable is unknown, and an unknown answer here becomes a timeout on the effect instead.',
      fix: `Check the app is still running and that 'ps eww -p <pid>' answers for it. ${OPT_OUT_FIX}`,
    };
  }
  // A dead app leaves its manifest behind, and macOS reuses pids: without this
  // a stranger's environment answers for the app and every press is permitted.
  if (appCommand !== appExecutable && !appCommand.startsWith(`${appExecutable} `)) {
    return {
      reaches: false,
      reason: `that pid is running ${appCommand.split(' ')[0] || 'something unreadable'}, not ${appExecutable}, `
        + 'so the ui-automation manifest is stale and nothing here knows how the running app was launched.',
      fix: 'Launch the app through the harness (launchFreshAppAndConnect) so the manifest names it, or delete the '
        + 'stale manifest if no app should be running.',
    };
  }
  if (new RegExp(`(^|\\s)${ALWAYS_ON_TOP_VAR}=1(\\s|$)`).test(appCommand)) {
    return {
      reaches: false,
      reason: `it was launched with ${ALWAYS_ON_TOP_VAR}=1, which makes its window non-focusable, so macOS `
        + 'delivers the keystroke nowhere and the scenario would time out on the effect instead.',
      fix: OPT_OUT_FIX,
    };
  }
  return { reaches: true, reason: `the app was launched without ${ALWAYS_ON_TOP_VAR}=1` };
}

export function formatKeyInputFailure({ action, pid, reason, fix }) {
  return `[key-input] ${action} cannot reach attn (app pid ${pid}): ${reason} ${fix}`;
}

// The manifest travels with the bundle id the profile baked in; the executable
// is a file inside whatever tree `--app-path` pointed the run at.
export function createKeyInputGuard({
  appPath = defaultAppPathForProfile(),
  platform = process.platform,
  appExecutable = appExecutableInAppTree(appPath, platform),
  manifestPath = manifestPathForProfile(profileForAppPath(appPath)),
  readCommand = readAppCommand,
  readAppPid = appPidFromManifest,
} = {}) {
  let cached = null;

  const verdict = () => {
    let pid = null;
    try {
      pid = readAppPid(manifestPath);
    } catch {
      pid = null;
    }
    if (pid === null) {
      return { pid: null, ...keyInputVerdict({ platform }) };
    }
    if (cached?.pid === pid) {
      return cached;
    }
    let appCommand = null;
    try {
      appCommand = readCommand(pid);
    } catch {
      appCommand = null;
    }
    const answer = { pid, ...keyInputVerdict({ platform, appRunning: true, appCommand, appExecutable }) };
    // A failed read is a transient ps hiccup as often as a dead app; caching it
    // would poison every later press in the run.
    if (appCommand !== null) {
      cached = answer;
    }
    return answer;
  };

  return {
    verdict,
    assertReaches(action) {
      const current = verdict();
      if (current.reaches) {
        return;
      }
      throw new Error(formatKeyInputFailure({ action, ...current }));
    },
  };
}
