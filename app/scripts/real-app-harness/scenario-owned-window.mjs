#!/usr/bin/env node
import { spawn } from 'node:child_process';
import readline from 'node:readline';
import { launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys, relaunchAppAndConnect } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner, registerNativeFailureEvidence } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const DECOY_WINDOW = `
import ctypes as C, sys
x = C.CDLL('libX11.so.6')
x.XOpenDisplay.restype = C.c_void_p
x.XOpenDisplay.argtypes = [C.c_char_p]
x.XDefaultRootWindow.restype = C.c_ulong
x.XDefaultRootWindow.argtypes = [C.c_void_p]
x.XCreateSimpleWindow.restype = C.c_ulong
x.XCreateSimpleWindow.argtypes = [C.c_void_p, C.c_ulong, C.c_int, C.c_int, C.c_uint, C.c_uint, C.c_uint, C.c_ulong, C.c_ulong]
for name, args in [('XStoreName', [C.c_void_p, C.c_ulong, C.c_char_p]), ('XMapWindow', [C.c_void_p, C.c_ulong]),
                   ('XDestroyWindow', [C.c_void_p, C.c_ulong]), ('XSync', [C.c_void_p, C.c_int]), ('XCloseDisplay', [C.c_void_p])]:
    getattr(x, name).argtypes = args
display = x.XOpenDisplay(None)
if not display:
    sys.exit('cannot open DISPLAY')
window = x.XCreateSimpleWindow(display, x.XDefaultRootWindow(display), 0, 0, 300, 200, 0, 0, 0xff00ff)
x.XStoreName(display, window, sys.argv[1].encode())
x.XMapWindow(display, window)
x.XSync(display, 0)
print('mapped', flush=True)
sys.stdin.read()
x.XDestroyWindow(display, window)
x.XSync(display, 0)
x.XCloseDisplay(display)
`;

async function openDecoyWindow(title) {
  const child = spawn('python3', ['-c', DECOY_WINDOW, title], { stdio: ['pipe', 'pipe', 'inherit'] });
  const exited = new Promise((resolve) => child.once('exit', (code, signal) => resolve({ code, signal })));
  await Promise.race([
    readline.createInterface({ input: child.stdout })[Symbol.asyncIterator]().next(),
    exited.then((exit) => { throw new Error(`Decoy window fixture exited before mapping: ${JSON.stringify(exit)}`); }),
  ]);
  return {
    async close() {
      child.stdin.end();
      return exited;
    },
  };
}

const PALETTE = '[role="combobox"][aria-label="Commands"]';
const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, { scenarioId: 'OWNED-WINDOW', tier: 'local', prefix: 'owned-window' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver(options);
const driver = createWindowDriver({ appPath: options.appPath, client });
registerNativeFailureEvidence(runner, { client, driver });

const openAndClosePaletteWithNativeKeys = async () => {
  await pressShortcutKeys(client, driver, 'ui.commandPalette');
  await client.request('dom_wait', { selector: PALETTE, focused: true, timeoutMs: 15_000 });
  await driver.pressKey('Escape');
  await client.request('dom_wait', { selector: PALETTE, absent: true, timeoutMs: 15_000 });
};

const decoy = await openDecoyWindow(driver.appName);
runner.registerCleanup('close_decoy_window', () => decoy.close());
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

try {
  await runner.step('native_keys_reach_the_app_past_a_same_titled_window', async () => {
    await launchFreshAppAndConnect(client, observer);
    await openAndClosePaletteWithNativeKeys();
  });

  await runner.step('native_keys_reach_the_relaunched_app', async () => {
    await observer.close();
    await relaunchAppAndConnect(client, observer);
    await openAndClosePaletteWithNativeKeys();
  });

  console.log(JSON.stringify(await runner.finishSuccess(), null, 2));
} catch (error) {
  console.error(JSON.stringify(await runner.finishFailure(error), null, 2));
  process.exitCode = 1;
}
