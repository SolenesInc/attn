import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';

import { appExecutableForAppPath } from './harnessProfile.mjs';

const execFileAsync = promisify(execFile);

// macOS asks LaunchServices to route the URL. Linux hands it to the app binary as
// argv, and the single-instance plugin passes it to the already-running app.
export function deepLinkCommand(url, { appExecutable, platform = process.platform } = {}) {
  if (platform === 'darwin') {
    return { command: 'open', args: [url] };
  }
  if (!appExecutable) {
    throw new Error(`Opening ${url} on ${platform} needs the app executable path`);
  }
  return { command: appExecutable, args: [url] };
}

export async function openDeepLink(url, { appPath, appExecutable, platform = process.platform } = {}) {
  const executable = platform === 'darwin'
    ? null
    : (appExecutable || appExecutableForAppPath(appPath));
  const { command, args } = deepLinkCommand(url, { appExecutable: executable, platform });

  if (platform === 'darwin') {
    await execFileAsync(command, args);
    return;
  }
  // It hands the URL over and exits by itself, so nothing here waits on it.
  const child = spawn(command, args, { detached: true, stdio: 'ignore' });
  child.unref();
}
