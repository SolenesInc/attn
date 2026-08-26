import { execFileSync } from 'node:child_process';
import { installWindowRecorder } from './windowRecorderApp.mjs';

try {
  const installed = await installWindowRecorder({
    codesignIdentity: process.env.MACOS_CODESIGN_IDENTITY || '',
  });
  console.log(`Installed ${installed.appPath}`);
  console.log(`Bundle id: ${installed.bundleId}`);
  execFileSync('/usr/bin/open', ['-n', '-a', installed.appPath]);
  console.log('Opened the recorder so macOS can request Screen Recording permission.');
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
}
