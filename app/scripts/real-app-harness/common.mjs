import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import WebSocket from 'ws';
import { waitForFirstWorkspacePane, waitForPaneVisible } from './scenarioAssertions.mjs';
import {
  assertProductionRunAllowed,
  currentHarnessProfile,
  harnessClientHello,
  defaultAppPathForProfile,
  defaultWSURLForProfile,
  deepLinkSchemeForProfile,
  profileForAppPath,
} from './harnessProfile.mjs';

export const DEFAULT_REMOTE_SSH_TARGET =
  process.env.ATTN_HARNESS_REMOTE_SSH_TARGET || 'attn-remote@orb';

export function parseCommonArgs(argv) {
  const options = {
    wsUrl: process.env.ATTN_REAL_APP_WS_URL || null,
    appPath: process.env.ATTN_REAL_APP_PATH || null,
    artifactsDir: process.env.ATTN_REAL_APP_ARTIFACTS_DIR || path.join(os.tmpdir(), 'attn-real-app-harness'),
    sessionRootDir: process.env.ATTN_REAL_APP_SESSION_ROOT || path.join(os.tmpdir(), 'attn-real-app-sessions'),
    runAgainstProd: false,
  };
  let wsUrlExplicit = Boolean(process.env.ATTN_REAL_APP_WS_URL);
  let appPathExplicit = Boolean(process.env.ATTN_REAL_APP_PATH);

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--ws-url') { options.wsUrl = argv[++index]; wsUrlExplicit = true; }
    else if (arg === '--app-path') { options.appPath = argv[++index]; appPathExplicit = true; }
    else if (arg === '--artifacts-dir') options.artifactsDir = argv[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = argv[++index];
    else if (arg === '--run-against-prod') options.runAgainstProd = true;
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  const safetyArgv = argv.length > 0 ? argv : process.argv.slice(2);
  const isHelp = options.help || safetyArgv.includes('--help') || safetyArgv.includes('-h');
  // --help must not resolve the active profile: a named profile resolves via
  // `attn profile resolve`, which needs ./attn built.
  if (isHelp) return options;

  if (!appPathExplicit) options.appPath = defaultAppPathForProfile();
  if (!wsUrlExplicit) options.wsUrl = defaultWSURLForProfile(profileForAppPath(options.appPath));

  assertCommonTargetAllowed(options, safetyArgv);
  return options;
}

export function assertCommonTargetAllowed(options, argv = process.argv.slice(2)) {
  const safetyArgv = options.runAgainstProd ? ['--run-against-prod'] : argv;
  assertProductionRunAllowed({ appPath: options.appPath, wsUrl: options.wsUrl }, safetyArgv);
}

export function printCommonHelp(scriptName) {
  const profile = currentHarnessProfile();
  const label = profile === '' ? 'production' : profile;
  let wsUrl;
  let appPath;
  try {
    wsUrl = defaultWSURLForProfile(profile);
    appPath = defaultAppPathForProfile(profile);
  } catch {
    wsUrl = '(unresolved — build ./attn with `make dev`)';
    appPath = '(unresolved — build ./attn with `make dev`)';
  }

  console.log(`Usage: pnpm exec node ${scriptName} [options]

Active profile: ${label}  (set ATTN_PROFILE, or ATTN_HARNESS_PROFILE to override; see docs/profiles.md)

Options:
  --ws-url <url>             Daemon websocket URL (default: ${wsUrl})
  --app-path <path>          Packaged app path (default: ${appPath})
  --artifacts-dir <path>     Directory for screenshots and summary output
  --session-root-dir <path>  Directory where harness-created session cwd roots are created
  --run-against-prod         Explicitly allow targeting the production app
`);
}

// Contract: an agent greps stdout for the LAST `ATTN_VERDICT ` line and
// JSON.parses the rest, so the payload must stay on one line.
export const ATTN_VERDICT_PREFIX = 'ATTN_VERDICT ';

// Cap keeps firstFailure inside the one-line ATTN_VERDICT contract; shared
// with scenarioRunner.mjs and rssBaselineVerdict.mjs.
export const FIRST_FAILURE_MAX_LENGTH = 300;

export function formatVerdictLine(verdict) {
  return `${ATTN_VERDICT_PREFIX}${JSON.stringify(verdict)}`;
}

export function emitVerdict(verdict) {
  console.log(formatVerdictLine(verdict));
}

export function harnessArtifactsRoot(env = process.env) {
  return env.ATTN_REAL_APP_ARTIFACTS_DIR || path.join(os.tmpdir(), 'attn-real-app-harness');
}

export function ensureDir(dirPath) {
  fs.mkdirSync(dirPath, { recursive: true });
}

export async function captureScreenshot(driver, outputPath) {
  try {
    await driver.screenshot(outputPath);
    return true;
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.warn(`[RealAppHarness] Screenshot skipped: ${message}`);
    return false;
  }
}

// Path-component-aware containment: '/a/b-old/x' is NOT under '/a/b'.
export function isDirectoryUnderRoot(directory, roots) {
  if (!directory) return false;
  return roots.some((root) => directory === root || directory.startsWith(root + path.sep));
}

export async function sweepStaleHarnessSessions(observer, {
  sessionRootDir = process.env.ATTN_REAL_APP_SESSION_ROOT || path.join(os.tmpdir(), 'attn-real-app-sessions'),
  log = (m) => console.log(`[harness] ${m}`),
  timeoutMs = 15_000,
} = {}) {
  // The daemon persists the realpath'd cwd (/private/var/folders/...) while the
  // bridge does not, so match both forms or the symlink defeats this filter.
  const rootCandidates = [...new Set([sessionRootDir, (() => {
    try {
      return fs.realpathSync(sessionRootDir);
    } catch {
      return sessionRootDir;
    }
  })()])];
  const isUnderSessionRoot = (directory) => isDirectoryUnderRoot(directory, rootCandidates);
  const stale = [...observer.sessionsById.values()].filter((session) => isUnderSessionRoot(session.directory));
  if (stale.length === 0) {
    return { swept: 0 };
  }

  log(`stale harness sessions=${stale.length} from a previous run — sweeping: `
    + stale.map((session) => `${session.id} label=${session.label} state=${session.state}`).join('; '));

  const staleIds = new Set(stale.map((session) => session.id));

  // The daemon refuses to unregister a chief-of-staff session, so demote first;
  // demoting a non-chief is a no-op.
  for (const id of staleIds) {
    try {
      observer.send({ cmd: 'set_chief_of_staff', session_id: id, chief_of_staff: false });
    } catch (error) {
      log(`demote (set_chief_of_staff) for ${id} failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  await observer.unregisterMatchingSessions((session) => staleIds.has(session.id), timeoutMs);

  return { swept: stale.length };
}

// Models mirror the "(cheap)" launch-catalog entries. Low effort keeps each
// pinned recipe compatible and cheap regardless of the user's defaults.
const CHEAP_LAUNCH_RECIPES = {
  claude: { model: 'haiku', effort: 'low' },
  codex: { model: 'gpt-5.4-mini', effort: 'low' },
};

function launchRecipeFor(agent) {
  const override = (process.env[`ATTN_HARNESS_LAUNCH_MODEL_${agent.toUpperCase()}`] || '').trim();
  if (override === 'inherit') {
    return null;
  }
  const recipe = CHEAP_LAUNCH_RECIPES[agent];
  return { ...recipe, model: override || recipe.model };
}

// Restores go straight to the daemon, not through the app: by the time the
// last scenario cleanup runs the app is usually gone.
const pendingSettingRestores = [];

async function pinCheapLaunchRecipes(client, observer) {
  for (const agent of Object.keys(CHEAP_LAUNCH_RECIPES)) {
    const recipe = launchRecipeFor(agent);
    if (!recipe) {
      continue;
    }
    const settings = [
      { key: `default_model_${agent}`, value: recipe.model },
      { key: `default_effort_${agent}`, value: recipe.effort },
    ];
    for (const { key, value } of settings) {
      const previous = observer.getSetting(key);
      if (previous === value) {
        continue;
      }
      await client.request('set_setting', { key, value });
      console.log(`[harness] pinned ${key}=${value} (was ${previous ? previous : 'unconfigured'})`);
      if (!pendingSettingRestores.some((restore) => restore.key === key)) {
        pendingSettingRestores.push({ key, value: previous });
      }
    }
  }
  installSettingRestoreHook();
}

// beforeExit does not fire on a signal, so the runner calls the restore from
// its signal path too; draining the queue makes the second call a no-op.
let settingRestoreHookInstalled = false;
function installSettingRestoreHook() {
  if (settingRestoreHookInstalled) {
    return;
  }
  settingRestoreHookInstalled = true;
  process.once('beforeExit', () => {
    // No await before the socket exists: a beforeExit handler only holds the
    // process open through a real handle.
    void restoreHarnessSettings().catch((error) => {
      process.stderr.write(`[harness] could not restore pinned settings: ${error?.message || error}\n`);
    });
  });
}

export async function restoreHarnessSettings({ write = writeDaemonSettings } = {}) {
  const restores = pendingSettingRestores.splice(0, pendingSettingRestores.length);
  if (restores.length === 0) {
    return 0;
  }
  await write(restores);
  for (const { key, value } of restores) {
    console.log(`[harness] restored ${key}=${value ? value : 'unconfigured'}`);
  }
  return restores.length;
}

async function writeDaemonSettings(entries, { wsUrl = defaultWSURLForProfile(), timeoutMs = 10_000 } = {}) {
  const ws = new WebSocket(wsUrl);
  const pending = new Set(entries.map((entry) => entry.key));
  try {
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`timed out writing settings to ${wsUrl}`)), timeoutMs);
      const settle = (error) => {
        clearTimeout(timer);
        if (error) reject(error); else resolve();
      };
      ws.on('error', settle);
      ws.on('close', (code, reason) => settle(pending.size > 0
        ? new Error(`daemon closed before writing ${[...pending].join(', ')} (code ${code}${reason?.length ? `: ${reason}` : ''})`)
        : undefined));
      ws.on('open', () => {
        ws.send(JSON.stringify({
          ...harnessClientHello('harness-observer'),
        }));
        for (const { key, value } of entries) {
          ws.send(JSON.stringify({ cmd: 'set_setting', key, value }));
        }
      });
      ws.on('message', (raw) => {
        let data;
        try {
          data = JSON.parse(raw.toString());
        } catch {
          return;
        }
        if (data.event !== 'settings_updated' || !data.changed_key) {
          return;
        }
        pending.delete(data.changed_key);
        if (pending.size === 0) settle();
      });
    });
  } finally {
    ws.close();
  }
}

export async function launchFreshAppAndConnect(client, observer, { sweepStaleSessions = true } = {}) {
  await client.launchFreshApp();
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
  await client.waitForFrontendResponsive(20_000);
  // A fresh profile's first launch shows the one-time What's New modal, which
  // swallows native HID clicks; dismiss it so scenarios start on a clean UI.
  await client.request('dismiss_whats_new', {}).catch(() => {});
  await observer.connect();
  await pinCheapLaunchRecipes(client, observer);
  if (sweepStaleSessions) {
    await sweepStaleHarnessSessions(observer);
  }
}

export async function relaunchAppAndConnect(client, observer) {
  await client.quitApp();
  // Relaunch scenarios (e.g. tr205) depend on sessions surviving the
  // relaunch, so the stale-session sweep must not run here.
  await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
}

export async function createSessionAndWaitForInitialPane({
  client,
  observer,
  cwd,
  label,
  agent,
  endpointId = null,
  sessionWaitMs = 30_000,
  promptReadyFn = null,
  promptReadyTimeoutMs = 45_000,
  waitForInitialPaneVisible,
  initialPaneWaitMs,
}) {
  const shouldWaitForInitialPane = waitForInitialPaneVisible ?? true;
  const paneWaitMs = initialPaneWaitMs ?? 20_000;
  const result = await client.request('create_session', {
    cwd,
    label,
    agent,
    ...(endpointId ? { endpoint_id: endpointId } : {}),
  });
  await observer.waitForSession({ id: result.sessionId, timeoutMs: sessionWaitMs });
  if (typeof promptReadyFn === 'function') {
    await promptReadyFn(client, result.sessionId, promptReadyTimeoutMs);
  }
  if (shouldWaitForInitialPane) {
    const pane = await waitForFirstWorkspacePane(client, result.sessionId, 'initial workspace pane', paneWaitMs);
    await waitForPaneVisible(client, result.sessionId, pane.paneId, paneWaitMs);
  }
  return result.sessionId;
}

export function timestampSlug() {
  return new Date().toISOString().replace(/[:.]/g, '-');
}

export function createRunContext(options, prefix) {
  ensureDir(options.artifactsDir);
  ensureDir(options.sessionRootDir);
  options.sessionRootDir = fs.realpathSync(options.sessionRootDir);

  const runId = `${prefix}-${timestampSlug()}`;
  const runDir = path.join(options.artifactsDir, runId);
  const sessionDir = path.join(options.sessionRootDir, runId);
  ensureDir(runDir);
  ensureDir(sessionDir);
  return { runId, runDir, sessionDir };
}

export async function bootstrapPackagedAppSession({
  driver,
  observer,
  runDir,
  sessionDir,
  sessionLabel,
}) {
  await driver.launchApp();
  await observer.connect();
  await driver.activateBackground();
  await captureScreenshot(driver, path.join(runDir, '01-app-launched.png'));

  const scheme = deepLinkSchemeForProfile(profileForAppPath(driver.appPath));
  const deepLink = `${scheme}://spawn?cwd=${encodeURIComponent(sessionDir)}&label=${encodeURIComponent(sessionLabel)}`;
  console.log(`[RealAppHarness] deepLink=${deepLink}`);
  await driver.openDeepLink(deepLink);

  const session = await observer.waitForSession({
    label: sessionLabel,
    directory: sessionDir,
    timeoutMs: 30_000,
  });

  console.log(`[RealAppHarness] session=${session.id} agent=${session.agent} state=${session.state}`);

  await observer.waitForWorkspace(
    session.id,
    (workspace) => (workspace.panes || []).length >= 1,
    `initial workspace for session ${session.id}`,
    30_000
  );

  await driver.activateBackground();
  await captureScreenshot(driver, path.join(runDir, '02-session-opened.png'));
  return session;
}

export async function splitAndFocusUtilityPane({
  driver,
  observer,
  sessionId,
  runDir,
  screenshotName,
  clickX = 0.75,
  clickY = 0.5,
}) {
  const existingPaneIds = new Set(
    (observer.getWorkspace(sessionId)?.panes || []).map((pane) => pane.pane_id),
  );
  await driver.pressKey('d', { command: true });
  const utilityPane = await observer.waitForUtilityPane(sessionId, 20_000, existingPaneIds);
  if (!utilityPane?.runtime_id) {
    throw new Error(`Utility pane missing runtime_id for session ${sessionId}`);
  }
  console.log(`[RealAppHarness] utilityPane=${utilityPane.pane_id} runtime=${utilityPane.runtime_id}`);

  await driver.activateBackground();
  await driver.clickWindow(clickX, clickY);
  if (runDir && screenshotName) {
    await captureScreenshot(driver, path.join(runDir, screenshotName));
  }

  return utilityPane;
}

export async function typeIntoFocusedPane(driver, text) {
  await driver.typeText(text);
  await driver.pressEnter();
}
