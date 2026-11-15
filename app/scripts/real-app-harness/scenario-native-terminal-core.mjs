#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-TERMINAL-CORE', tier: 'tier1-local-shell', prefix: 'native-terminal-core',
  metadata: { focus: 'A shell in the native app takes typed keys and draws their output, and a program that tracks the mouse hears a press, its release and the wheel, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const MOUSE_READER = `stty raw -echo
printf '\\033[?1000h\\033[?1006hREADY\\r\\n'
while IFS= read -rs -n1 c; do
  case "$c" in $'\\e') printf 'ESC' ;; q) break ;; *) printf '%s' "$c" ;; esac
done
printf '\\033[?1000l\\033[?1006l'
stty sane
printf '\\r\\nDONE\\n'
`;

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, 'shop');
  fs.mkdirSync(cwd, { recursive: true });
  const reader = path.join(cwd, 'mouse-reader.sh');
  fs.writeFileSync(reader, MOUSE_READER);
  await runner.step('spawn_shell', async () => {
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: {}, label: 'shop', cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
  });

  let runtimeId;
  await runner.step('terminal_draws', async () => {
    const desktops = await driver.state('desktops');
    const pane = desktops.flatMap((desktop) => desktop.panes ?? []).find((p) => p.session_id === id || p.pane_id === id);
    runner.assert(pane?.runtime_id, 'a desktop holds the shell as a pane with a terminal', desktops);
    runtimeId = pane.runtime_id;
    await driver.waitFor({ key: `sidebar.session.${id}` });
    const opened = await driver.click({ key: `sidebar.session.${id}` });
    runner.assert(opened.matched, 'the press lands on the sidebar row that opens the shell', opened);
    await driver.waitFor({ key: `terminal.${runtimeId}` });
  });
  const terminal = () => ({ key: `terminal.${runtimeId}` });
  const screen = (includes) => driver.state(`terminal:${runtimeId}`, { includes, timeoutMs: 15_000 });

  await runner.step('type_and_read', async () => {
    const receipt = await driver.click(terminal());
    runner.assert(receipt.matched, 'the press lands on the terminal', receipt);
    await driver.typeInto(null, 'echo native-$((40 + 2))');
    await driver.pressKey('Enter');
    const rows = await screen('native-42');
    runner.assert(rows.some((row) => row.trim() === 'native-42'), 'the shell echoes the typed command and the window draws its output', rows);
  });

  await runner.step('mouse_reaches_the_program', async () => {
    await driver.typeInto(null, `bash ${reader}`);
    await driver.pressKey('Enter');
    await screen('READY');
    await driver.click(terminal());
    const pressed = await screen('ESC[<3;');
    runner.assert(pressed.some((row) => /ESC\[<0;\d+;\d+MESC\[<3;\d+;\d+m/.test(row)), 'the program reads a primary press and its release in SGR', pressed);
    await driver.scroll(terminal(), 60);
    const wheeled = await screen('ESC[<65;');
    runner.assert(wheeled.some((row) => row.includes('ESC[<65;')), 'the program reads the wheel turning down as button 65', wheeled);
    await driver.typeInto(null, 'q');
    await screen('DONE');
  });

  const screenshot = path.join(runner.runDir, 'native-terminal.png');
  await runner.step('capture_window', () => driver.screenshot(screenshot));

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ sessionId: id, runtimeId, screenshot, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
