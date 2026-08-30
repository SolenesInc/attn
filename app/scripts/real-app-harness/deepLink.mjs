import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';

import { appExecutableForAppPath } from './harnessProfile.mjs';

const execFileAsync = promisify(execFile);

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
  const child = spawn(command, args, { detached: true, stdio: 'ignore' });
  child.unref();
}
