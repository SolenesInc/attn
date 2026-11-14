#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('The native shortcut editor scenario changes keybindings_config, so it needs a named instance');
if (process.platform !== 'linux') throw new Error('The shortcut recorder hears physical keys, which this scenario sends with xdotool on Linux');
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SHORTCUT-EDITOR', tier: 'tier1-local-shell', prefix: 'native-shortcut-editor',
  metadata: { focus: 'Physical Linux keys open the native cheat sheet, record a rebind and a chord in the shortcut editor that then run, ask before taking keys another action holds, refuse what a required action holds, and reset, unbind and restore every default' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const env = instanceCliEnv(instance);
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const ID = 'ui.showShortcuts';
const CHEAT = { key: 'shortcuts.dialog' };
const EDITOR = { key: 'shortcut-editor-dialog' };
const physical = async (...strokes) => {
  await driver.window.activateBackground();
  await driver.window.runXdotool(['key', ...strokes]);
};
const config = () => JSON.parse(attn('settings', 'get', 'keybindings_config') || '{}');
const saved = (predicate, description) => observer.waitForMessage((m) => {
  if (m.event !== 'settings_updated' || m.settings?.keybindings_config === undefined) return null;
  const value = JSON.parse(m.settings.keybindings_config || '{}');
  return predicate(value) && value;
}, description, 15_000);
const nodes = async () => {
  const all = [];
  const walk = (node) => { all.push(node); (node.children ?? []).forEach(walk); };
  walk(await driver.tree());
  return all;
};
const keysOf = async (id) => (await nodes()).find((n) => n.key === `shortcut-editor.keys.${id}`)?.value;
const openEditorOn = async (filter) => {
  await physical('ctrl+alt+k');
  await driver.waitFor({ key: 'palette.dialog' });
  await driver.typeInto(null, 'Customize keyboard shortcuts');
  await driver.waitFor({ key: 'palette.row.command:customize-shortcuts' });
  await driver.pressKey('Enter');
  await driver.waitFor(EDITOR);
  if (filter) await driver.typeInto({ key: 'shortcut-editor.filter' }, filter);
};
const closeEditor = async () => {
  await driver.click({ key: 'shortcut-editor.done' });
  await driver.waitFor(EDITOR, { gone: true });
};
const cheatSheetRow = async () => (await driver.waitFor({ key: `shortcut.${ID}` })).name;

try {
  await runner.step('launch_app', async () => {
    await launchFreshNativeAppAndConnect(client, observer);
    const before = observer.getSetting('keybindings_config');
    runner.registerCleanup('restore_keybindings', () => observer.requestResult({ cmd: 'set_setting', key: 'keybindings_config', value: before }, 'settings_updated').catch(() => {}));
    if (before) await observer.requestResult({ cmd: 'set_setting', key: 'keybindings_config', value: '' }, 'settings_updated');
    await driver.waitFor({ key: 'home' });
  });

  await runner.step('ctrl_shift_slash_opens_the_cheat_sheet', async () => {
    await physical('ctrl+shift+slash');
    await driver.waitFor(CHEAT);
    const row = await cheatSheetRow();
    runner.assert(row === 'Keyboard shortcuts, Ctrl+Shift+/', 'the cheat sheet lists the Linux keys', { row });
    await driver.screenshot(path.join(runner.runDir, 'native-cheatsheet.png'));
  });

  await runner.step('edit_shortcuts_opens_the_editor_from_the_cheat_sheet', async () => {
    await driver.click({ key: 'shortcuts.edit' });
    await driver.waitFor(EDITOR);
    await driver.waitFor(CHEAT, { gone: true });
    const filter = (await nodes()).find((n) => n.key === 'shortcut-editor.filter');
    runner.assert(filter?.states?.includes('focused'), 'the filter takes the keyboard', filter);
    await closeEditor();
  });

  await runner.step('recording_rebinds_and_the_new_keys_open_the_cheat_sheet', async () => {
    await openEditorOn('Keyboard shortcuts');
    await driver.click({ key: `shortcut-editor.keys.${ID}` });
    const rebound = saved((value) => value.overrides?.[ID], `${ID} saved as a rebind`);
    await physical('ctrl+shift+y');
    const stored = await rebound;
    runner.writeJson('rebind.json', stored);
    await driver.waitFor({ key: `shortcut-editor.badge.${ID}.customized` });
    await driver.waitFor({ key: `shortcut-editor.reset.${ID}` });
    runner.assert(config().overrides?.[ID], 'the shell reads the rebind', config());
    await driver.screenshot(path.join(runner.runDir, 'native-shortcut-editor.png'));
    await closeEditor();
    await physical('ctrl+shift+y');
    await driver.waitFor(CHEAT);
    const row = await cheatSheetRow();
    runner.assert(row === 'Keyboard shortcuts, Ctrl+Shift+Y', 'the cheat sheet shows the new keys', { row });
    await driver.pressKey('Escape');
    await driver.waitFor(CHEAT, { gone: true });
  });

  await runner.step('keys_another_action_holds_ask_first_and_cancel_keeps_both', async () => {
    await openEditorOn('Keyboard shortcuts');
    await driver.click({ key: `shortcut-editor.keys.${ID}` });
    await physical('ctrl+alt+b');
    const pending = await driver.waitFor({ key: 'shortcut-editor.pending.text' });
    runner.assert(pending.name?.includes('“Toggle sidebar”'), 'the prompt names the action that holds the keys', pending);
    await driver.waitFor({ key: 'shortcut-editor.reassign' });
    await driver.click({ key: 'shortcut-editor.cancel' });
    await driver.waitFor({ key: 'shortcut-editor.pending' }, { gone: true });
    const now = config();
    runner.assert(!now.overrides?.['session.toggleSidebar'], 'cancel leaves the holder its keys', now);
  });

  await runner.step('keys_a_required_action_holds_are_refused', async () => {
    await driver.click({ key: `shortcut-editor.keys.${ID}` });
    await physical('ctrl+shift+comma');
    const refusal = await driver.waitFor({ role: 'alert' });
    runner.assert(/Settings/.test(refusal.name ?? ''), 'the refusal names the required action', refusal);
    runner.writeJson('reserved.json', refusal);
    await driver.click({ key: 'shortcut-editor.filter' });
  });

  await runner.step('a_recorded_chord_runs_its_action', async () => {
    await driver.click({ key: `shortcut-editor.chord.${ID}` });
    const chord = saved((value) => value.overrides?.[ID]?.then || value.overrides?.[ID]?.leader, `${ID} saved as a chord`);
    await physical('ctrl+shift+y');
    await driver.waitFor({ key: 'shortcut-editor.then.lead' });
    await physical('j');
    runner.writeJson('chord.json', await chord);
    await closeEditor();
    await physical('ctrl+shift+y', 'j');
    await driver.waitFor(CHEAT);
    await driver.pressKey('Escape');
    await driver.waitFor(CHEAT, { gone: true });
  });

  await runner.step('reset_returns_the_default', async () => {
    await openEditorOn('Keyboard shortcuts');
    const reset = saved((value) => !value.overrides?.[ID], `${ID} reset`);
    await driver.click({ key: `shortcut-editor.reset.${ID}` });
    await reset;
    await driver.waitFor({ key: `shortcut-editor.badge.${ID}.customized` }, { gone: true });
    runner.assert(await keysOf(ID) === 'Ctrl Shift /', 'the row shows its default keys', { keys: await keysOf(ID) });
  });

  await runner.step('unbind_and_reset_a_shortcut', async () => {
    await driver.click({ key: 'shortcut-editor.filter' });
    await driver.pressKey('End');
    for (let i = 0; i < 'Keyboard shortcuts'.length; i += 1) await driver.pressKey('Backspace');
    await driver.typeInto({ key: 'shortcut-editor.filter' }, 'Toggle sidebar');
    const unbound = saved((value) => value.overrides && value.overrides['session.toggleSidebar'] === null, 'the sidebar key unbound');
    await driver.click({ key: 'shortcut-editor.unbind.session.toggleSidebar' });
    await unbound;
    await driver.waitFor({ key: 'shortcut-editor.unbind.session.toggleSidebar' }, { gone: true });
    const reset = saved((value) => !(value.overrides && 'session.toggleSidebar' in value.overrides), 'the sidebar key reset');
    await driver.click({ key: 'shortcut-editor.reset.session.toggleSidebar' });
    await reset;
  });

  await runner.step('restore_defaults_drops_every_rebind', async () => {
    await driver.click({ key: 'shortcut-editor.keys.session.toggleSidebar' });
    const rebound = saved((value) => value.overrides?.['session.toggleSidebar'], 'a rebind to restore');
    await physical('ctrl+shift+y');
    await rebound;
    const restored = saved((value) => Object.keys(value.overrides ?? {}).length === 0, 'every rebind dropped');
    await driver.click({ key: 'shortcut-editor.restore' });
    await restored;
    await driver.waitFor({ key: 'shortcut-editor.badge.session.toggleSidebar.customized' }, { gone: true });
    await closeEditor();
  });

  await runner.step('the_shell_restores_every_default', async () => {
    attn('settings', 'set', 'keybindings_config', JSON.stringify({ version: 1, overrides: { [ID]: { key: 'y', meta: true, shift: true } } }));
    await physical('ctrl+shift+y');
    await driver.waitFor(CHEAT);
    await driver.pressKey('Escape');
    await driver.waitFor(CHEAT, { gone: true });
    const cleared = saved((value) => !value.overrides?.[ID], 'the shell reset');
    runner.assert(attn('settings', 'set', 'keybindings_config', '') === 'keybindings_config =', 'the shell restores the defaults');
    await cleared;
    await physical('ctrl+shift+slash');
    await driver.waitFor(CHEAT);
    runner.assert(await cheatSheetRow() === 'Keyboard shortcuts, Ctrl+Shift+/', 'the default keys are back');
    await driver.pressKey('Escape');
  });

  await runner.finishSuccess({});
} catch (error) {
  await driver.screenshot(path.join(runner.runDir, 'failure.png')).catch(() => {});
  await driver.tree().then((tree) => runner.writeJson('failure-tree.json', tree)).catch(() => {});
  await runner.finishFailure(error);
  process.exitCode = 1;
}
