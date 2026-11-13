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
  scenarioId: 'NATIVE-TERMINAL-FEATURES', tier: 'tier1-local-shell', prefix: 'native-terminal-features',
  metadata: { focus: 'A shell in the native app scrolls its history, selects and copies, selects a command block, finds through the history and draws a kitty image, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const KITTY_IMAGE = `import base64, sys
w, h = 96, 48
pixels = bytes(v for y in range(h) for x in range(w) for v in (x * 255 // w, y * 255 // h, 255 - x * 255 // w))
data = base64.b64encode(pixels).decode()
chunks = [data[i:i + 4096] for i in range(0, len(data), 4096)]
for n, chunk in enumerate(chunks):
    head = 'a=T,q=2,f=24,s=96,v=48,i=7,' if n == 0 else ''
    more = 0 if n == len(chunks) - 1 else 1
    sys.stdout.write(f'\\033_G{head}m={more};{chunk}\\033\\\\')
sys.stdout.write('\\r\\nafter image\\r\\n')
sys.stdout.flush()
`;

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, 'shop');
  fs.mkdirSync(cwd, { recursive: true });
  const kitty = path.join(cwd, 'kitty-image.py');
  fs.writeFileSync(kitty, KITTY_IMAGE);
  await runner.step('spawn_shell', async () => {
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: {}, label: 'shop', cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
  });

  let runtimeId;
  await runner.step('open_terminal', async () => {
    const desktops = await driver.state('desktops');
    const pane = desktops.flatMap((desktop) => desktop.panes ?? []).find((p) => p.session_id === id || p.pane_id === id);
    runner.assert(pane?.runtime_id, 'a desktop holds the shell as a pane with a terminal', desktops);
    runtimeId = pane.runtime_id;
    await driver.waitFor({ key: `sidebar.session.${id}` });
    await driver.click({ key: `sidebar.session.${id}` });
    await driver.waitFor({ key: `terminal.${runtimeId}` });
  });
  const terminal = () => ({ key: `terminal.${runtimeId}` });
  const screen = (includes) => driver.state(`terminal:${runtimeId}`, { includes, timeoutMs: 15_000 });
  const view = () => driver.state(`terminal-view:${runtimeId}`);
  const run = async (command, until) => {
    await driver.typeInto(null, command);
    await driver.pressKey('Enter');
    return screen(until);
  };

  await runner.step('scroll_select_and_copy', async () => {
    await driver.click(terminal());
    await run('for i in $(seq 1 200); do echo line-$i; done', 'line-200');
    await driver.scroll(terminal(), -400);
    const scrolled = await view();
    runner.assert(scrolled.scrolled_back > 0, 'the wheel scrolls the view into the history', scrolled);
    await driver.drag(terminal(), { by: [60, 40] }, { at: [0.01, 0.3] });
    const selected = await view();
    runner.assert(selected.overlays.some((o) => o.kind === 'Wash'), 'a drag washes the rows it covers', selected.overlays);
    runner.assert(String(selected.copies ?? '').includes('line-'), 'the selection copies the rows it covers', selected.copies);
    await driver.scroll(terminal(), 4000);
  });

  await runner.step('select_a_command_block', async () => {
    const rows = await run('echo block-$((0 + 1))', 'block-1');
    const row = rows.findIndex((text) => text.trim() === 'block-1');
    runner.assert(row >= 0, 'the output row reads block-1 alone', rows);
    const slot = await driver.waitFor(terminal());
    const caret = await driver.waitFor({ key: `terminal.${runtimeId}.caret` });
    const { cursor } = await view();
    const y = caret.rect[1] - cursor[1] * caret.rect[3] + (row + 0.5) * caret.rect[3];
    await driver.click(terminal(), { at: [0.01, (y - slot.rect[1]) / slot.rect[3]] });
    const selected = await view();
    runner.assert(selected.block, 'a press on the output selects its command block', selected);
    runner.assert(selected.overlays.some((o) => o.kind.startsWith('Outline')), 'the selected block draws its outline', selected.overlays);
  });

  await runner.step('find_through_the_history', async () => {
    await run('for i in 1 2 3; do echo find-$i-native; done', 'find-3-native');
    await driver.pressKey(process.platform === 'darwin' ? 'mod+f' : 'ctrl+shift+f');
    await driver.waitFor({ key: `terminal.${runtimeId}.find.field` });
    await driver.typeInto(null, 'NATIVE');
    const found = await driver.state(`terminal-view:${runtimeId}`, { includes: '#f5c542', timeoutMs: 5_000 });
    const matches = found.overlays.filter((o) => o.color === '#f5c542');
    const rows = await screen('find-3-native');
    const outputs = rows.filter((text) => /^find-\d-native$/.test(text.trim())).length;
    runner.assert(outputs === 3 && matches.length >= 4, 'find washes each output and the command line that printed them', matches);
    await driver.pressKey('Escape');
    await driver.waitFor({ key: `terminal.${runtimeId}.find` }, { gone: true });
  });

  await runner.step('draw_a_kitty_image', async () => {
    await run(`clear; python3 ${kitty}`, 'after image');
    const drawn = await view();
    const image = drawn.images?.find((entry) => entry.image === 7);
    runner.assert(image && image.pixels[0] === 96 && image.pixels[1] === 48, 'the image the program placed draws with its pixels', drawn.images);
  });

  const screenshot = path.join(runner.runDir, 'native-terminal-features.png');
  await runner.step('capture_window', () => driver.screenshot(screenshot));

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ sessionId: id, runtimeId, screenshot, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
