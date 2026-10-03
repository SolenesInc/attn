import { spawn } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { expect, it } from 'vitest';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { appPlatformFor } from './platform.mjs';

it('shutdown stops its captured child and preserves a stranger carrying the app path in argv', async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-shutdown-'));
  const appPath = path.join(root, 'attn-shutdown-test.app');
  const script = "process.stdout.write('ready'); process.stdin.resume()";
  const stranger = spawn(process.execPath, ['-e', script, appPath]);
  const owned = spawn(process.execPath, ['-e', script]);
  try {
    await Promise.all([once(stranger.stdout, 'data'), once(owned.stdout, 'data')]);
    const exited = once(owned, 'exit');
    const platform = { ...appPlatformFor('darwin'), requestQuit: async () => {} };
    const client = new UiAutomationClient({ appPath, bundleId: 'com.attn.manager.shutdown-test', manifestPath: path.join(root, 'manifest.json'), platform });
    client.launch = { spawned: true, pid: owned.pid, child: owned };
    await client.quitApp(0);
    await exited;
    expect(stranger.exitCode).toBeNull();
    expect(stranger.signalCode).toBeNull();
    process.kill(stranger.pid, 0);
  } finally {
    for (const child of [owned, stranger]) {
      if (child.exitCode === null && child.signalCode === null) {
        const exited = once(child, 'exit'); child.kill(); await exited;
      }
    }
    fs.rmSync(root, { recursive: true, force: true });
  }
});
