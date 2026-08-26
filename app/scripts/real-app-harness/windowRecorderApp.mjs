import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);
const DEFAULT_INSTALLER_PATH = fileURLToPath(import.meta.url);
const SCRIPT_DIR = path.dirname(DEFAULT_INSTALLER_PATH);
const DEFAULT_SOURCE_PATH = path.join(SCRIPT_DIR, 'WindowRecorder.swift');
const CODESIGN_IDENTITY_SCRIPT = path.resolve(SCRIPT_DIR, '..', '..', '..', 'scripts', 'macos-codesign-identity.sh');
const LAUNCH_SERVICES_REGISTER = '/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister';

export const WINDOW_RECORDER_APP_NAME = 'attn-window-recorder.app';
export const WINDOW_RECORDER_BUNDLE_ID = 'com.attn.window-recorder';
export const WINDOW_RECORDER_EXECUTABLE = 'attn-window-recorder';

export function defaultWindowRecorderAppPath(homeDir = os.homedir()) {
  return path.join(homeDir, 'Applications', WINDOW_RECORDER_APP_NAME);
}

export function windowRecorderCommand(appPath = defaultWindowRecorderAppPath()) {
  return path.join(appPath, 'Contents', 'MacOS', WINDOW_RECORDER_EXECUTABLE);
}

function sourceFingerprint(sourcePath, installerPath) {
  const fingerprint = createHash('sha256');
  fingerprint.update(fs.readFileSync(sourcePath));
  fingerprint.update('\0');
  fingerprint.update(fs.readFileSync(installerPath));
  return fingerprint.digest('hex');
}

function fingerprintPath(appPath) {
  return path.join(appPath, 'Contents', 'Resources', 'source.sha256');
}

function infoPlist() {
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDisplayName</key>
  <string>attn window recorder</string>
  <key>CFBundleExecutable</key>
  <string>${WINDOW_RECORDER_EXECUTABLE}</string>
  <key>CFBundleIdentifier</key>
  <string>${WINDOW_RECORDER_BUNDLE_ID}</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>attn window recorder</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>1.0</string>
  <key>CFBundleVersion</key>
  <string>1</string>
  <key>LSUIElement</key>
  <true/>
  <key>NSScreenCaptureUsageDescription</key>
  <string>Record attn windows during local real-app verification.</string>
</dict>
</plist>
`;
}

export function requireInstalledWindowRecorder({
  appPath = defaultWindowRecorderAppPath(),
  sourcePath = DEFAULT_SOURCE_PATH,
  installerPath = DEFAULT_INSTALLER_PATH,
} = {}) {
  const command = windowRecorderCommand(appPath);
  const installedFingerprintPath = fingerprintPath(appPath);
  if (!fs.existsSync(command) || !fs.existsSync(installedFingerprintPath)) {
    throw new Error(
      `window recorder is not installed at ${appPath}; run \`make install-window-recorder\` from the attn checkout`
    );
  }

  const expected = sourceFingerprint(sourcePath, installerPath);
  const installed = fs.readFileSync(installedFingerprintPath, 'utf8').trim();
  if (installed !== expected) {
    throw new Error(
      `window recorder at ${appPath} is stale; run \`make install-window-recorder\` from the attn checkout`
    );
  }
  return command;
}

export function requireInstalledWindowRecorderLaunch(options = {}) {
  const appPath = options.appPath || defaultWindowRecorderAppPath();
  requireInstalledWindowRecorder({ ...options, appPath });
  return {
    command: '/usr/bin/open',
    argsPrefix: ['-n', '-W', '-a', appPath],
    captureLaunchedStderr: true,
    stopWithFile: true,
  };
}

async function resolveCodesignIdentity(execFileFn) {
  const { stdout } = await execFileFn('bash', [CODESIGN_IDENTITY_SCRIPT, 'find'], { timeout: 5_000 });
  return stdout.toString().trim();
}

export async function installWindowRecorder({
  appPath = defaultWindowRecorderAppPath(),
  sourcePath = DEFAULT_SOURCE_PATH,
  installerPath = DEFAULT_INSTALLER_PATH,
  codesignIdentity = '',
  platform = process.platform,
  execFileFn = execFileAsync,
  launchServicesRegisterPath = LAUNCH_SERVICES_REGISTER,
} = {}) {
  if (platform !== 'darwin') {
    throw new Error('the attn window recorder can only be installed on macOS');
  }

  const identity = codesignIdentity || await resolveCodesignIdentity(execFileFn);
  if (!identity || identity === '-') {
    throw new Error('a stable macOS code-signing identity is required; run `make ensure-codesign-identity`');
  }

  const applicationsDir = path.dirname(appPath);
  fs.mkdirSync(applicationsDir, { recursive: true });
  const stagingRoot = fs.mkdtempSync(path.join(applicationsDir, '.attn-window-recorder-install-'));
  const stagedApp = path.join(stagingRoot, path.basename(appPath));
  const contentsDir = path.join(stagedApp, 'Contents');
  const executableDir = path.join(contentsDir, 'MacOS');
  const resourcesDir = path.join(contentsDir, 'Resources');
  const stagedCommand = path.join(executableDir, WINDOW_RECORDER_EXECUTABLE);
  const replacedApp = `${appPath}.replaced-${process.pid}-${Date.now()}`;
  let movedExisting = false;

  try {
    fs.mkdirSync(executableDir, { recursive: true });
    fs.mkdirSync(resourcesDir, { recursive: true });
    fs.writeFileSync(path.join(contentsDir, 'Info.plist'), infoPlist(), 'utf8');
    fs.writeFileSync(
      path.join(resourcesDir, 'source.sha256'),
      `${sourceFingerprint(sourcePath, installerPath)}\n`,
      'utf8'
    );

    await execFileFn('/usr/bin/swiftc', ['-O', sourcePath, '-o', stagedCommand], { timeout: 120_000 });
    await execFileFn('/usr/bin/codesign', ['--force', '--sign', identity, stagedApp], { timeout: 10_000 });
    await execFileFn('/usr/bin/codesign', ['--verify', '--deep', '--strict', stagedApp], { timeout: 10_000 });

    if (fs.existsSync(appPath)) {
      fs.renameSync(appPath, replacedApp);
      movedExisting = true;
    }
    try {
      fs.renameSync(stagedApp, appPath);
    } catch (error) {
      if (movedExisting) fs.renameSync(replacedApp, appPath);
      movedExisting = false;
      throw error;
    }
    if (movedExisting) {
      fs.rmSync(replacedApp, { recursive: true, force: true });
      movedExisting = false;
    }

    if (launchServicesRegisterPath && fs.existsSync(launchServicesRegisterPath)) {
      await execFileFn(launchServicesRegisterPath, ['-f', appPath], { timeout: 10_000 });
    }
  } finally {
    if (movedExisting && fs.existsSync(replacedApp) && !fs.existsSync(appPath)) {
      fs.renameSync(replacedApp, appPath);
    }
    fs.rmSync(stagingRoot, { recursive: true, force: true });
  }

  return { appPath, bundleId: WINDOW_RECORDER_BUNDLE_ID, command: windowRecorderCommand(appPath), identity };
}
