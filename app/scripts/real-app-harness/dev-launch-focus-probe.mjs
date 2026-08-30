#!/usr/bin/env node
// The `dev-` prefix keeps this out of scenario discovery.

// Opt out of always-on-top so launch really steals focus, as in production.
process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';

import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createWindowDriver } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

async function frontmost() {
  const { stdout } = await execFileAsync('osascript', [
    '-e',
    'tell application "System Events" to bundle identifier of first application process whose frontmost is true',
  ]);
  return stdout.trim();
}

async function activateBundle(id) {
  await execFileAsync('osascript', ['-e', `tell application id "${id}" to activate`]).catch(() => {});
}

async function main() {
  const callerCandidates = ['com.mitchellh.ghostty', 'com.apple.Terminal', 'com.googlecode.iterm2'];
  let callerBundle = null;
  for (const id of callerCandidates) {
    try {
      await execFileAsync('osascript', ['-e', `tell application id "${id}" to launch`]);
      await activateBundle(id);
      await new Promise((resolve) => setTimeout(resolve, 600));
      if ((await frontmost()) === id) {
        callerBundle = id;
        break;
      }
    } catch {}
  }
  if (!callerBundle) {
    throw new Error('Could not get a terminal app to frontmost. Is any installed?');
  }
  console.log(`[probe] caller frontmost=${callerBundle}`);

  const client = new UiAutomationClient({
    launchEnv: { ATTN_FOCUS_PROBE: '1' },
  });

  await client.quitApp().catch(() => {});
  await activateBundle(callerBundle);
  await new Promise((resolve) => setTimeout(resolve, 400));
  console.log(`[probe] before launch frontmost=${await frontmost()}`);

  const sampler = execFile('node', ['-e', `
    const { execFile } = require('node:child_process');
    const { promisify } = require('node:util');
    const run = promisify(execFile);
    const start = Date.now();
    (async () => {
      while (Date.now() - start < 4000) {
        try {
          const { stdout } = await run('osascript', [
            '-e',
            'tell application "System Events" to bundle identifier of first application process whose frontmost is true',
          ]);
          console.log('[sampler] t=' + (Date.now() - start) + 'ms ' + stdout.trim());
        } catch {}
        await new Promise(r => setTimeout(r, 60));
      }
    })();
  `]);
  sampler.stdout?.pipe(process.stdout);

  await client.launchApp();
  const afterFrontmost = await frontmost();
  console.log(`[probe] after launch frontmost=${afterFrontmost}`);

  await new Promise((resolve) => setTimeout(resolve, 2500));
  sampler.kill('SIGKILL');

  // The caller restore lands after the window gate fires, ~200ms typical.
  for (let i = 0; i < 5; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 200));
    const now = await frontmost();
    console.log(`[probe] T+${(i + 1) * 200}ms frontmost=${now}`);
  }

  const finalFrontmost = await frontmost();
  const attnDriver = createWindowDriver({ bundleId: client.bundleId, appPath: client.appPath });
  const attnWid = await attnDriver.mainWindowId();
  console.log(`[probe] attn window id after launch=${attnWid}`);
  await client.quitApp().catch(() => {});

  if (finalFrontmost === callerBundle) {
    console.log(`[probe] PASS: caller ${callerBundle} retained frontmost after spawn-with-env launch`);
  } else {
    console.log(`[probe] FAIL: expected ${callerBundle}, got ${finalFrontmost}`);
    process.exitCode = 1;
  }
}

main().catch((err) => {
  console.error('[probe] FAILED');
  console.error(err instanceof Error ? err.stack || err.message : err);
  process.exitCode = 1;
});
