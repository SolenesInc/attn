#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-MARKDOWN-ANNOTATIONS', tier: 'tier2-local-mock-agent', prefix: 'native-markdown-annotations',
  metadata: { focus: 'A selection in a native markdown tile takes a quick label and a comment, and Send delivers both to the agent the tile is bound to, which answers' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const MARKED = 'Cache the tile layout before the first paint.';
const LABELLED = 'A second paragraph takes a quick label.';
const COMMENT = 'Which layout does it cache?';
const MOD = process.platform === 'darwin' ? 'mod' : 'ctrl';

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window_for_two_tiles', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  let desktop;
  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => {
      const left = observer.desktop(desktop.id);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, 'agent');
  let agent;
  await runner.step('spawn_agent', async () => {
    writeMockAgentFixture(cwd, { turns: [{ includes: 'warm-up', actions: [{ type: 'reply', text: 'the conversation is live' }] }], defaultActions: [{ type: 'reply', text: 'got document feedback' }] });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'agent spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id, agent: 'codex', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'reader', cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, 'spawn_session starts the agent', result);
    runner.registerCleanup('unregister_agent', () => observer.unregisterSession(id));
    const desktops = await driver.state('desktops');
    const pane = desktops.flatMap((d) => d.panes ?? []).find((p) => p.session_id === id);
    runner.assert(pane?.runtime_id, 'the desktop holds the agent with a terminal', desktops);
    agent = { pane: result.pane_id, runtimeId: pane.runtime_id };
    await driver.state(`terminal:${agent.runtimeId}`, { includes: 'OpenAI Codex', timeoutMs: 60_000 });
    await driver.click({ key: `terminal.${agent.runtimeId}` });
    await driver.typeInto(null, 'warm-up');
    await driver.pressKey('Enter');
    await driver.state(`terminal:${agent.runtimeId}`, { includes: 'the conversation is live', timeoutMs: 30_000 });
  });

  const tile = `tile-${crypto.randomUUID()}`;
  const doc = path.join(cwd, 'review.md');
  const uri = `attn://file/${encodeURIComponent(doc)}`;
  const stored = () => observer.requestResult({ cmd: 'markdown_annotations_get', document_uri: uri, source_kind: 'file', path: doc }, 'markdown_annotations_get_result');
  const part = (name) => ({ key: `tile.${tile}.document.${name}` });
  const block = (n) => ({ key: `b${n}` });
  const selectAll = (n) => driver.drag(block(n), { target: block(n), at: [0.995, 0.5] }, { at: [0.005, 0.5] });

  await runner.step('dock_a_document_bound_to_the_agent', async () => {
    fs.writeFileSync(doc, `# Review me\n\n${MARKED}\n\n${LABELLED}\n`);
    await observer.profileCommand('desktop_dock_tile', { desktop_id: desktop.id, anchor_id: agent.pane, edge: 'right', tile_id: tile, tile_kind: 'markdown', tile_params: doc, tile_session_id: id });
    await driver.waitFor(block(1));
    await driver.waitFor(part('send.to'));
  });

  await runner.step('a_quick_label_marks_a_selection', async () => {
    await selectAll(2);
    await driver.click(part('toolbar.label.clarify-this'));
    await driver.waitFor(part('toolbar'), { gone: true });
    await driver.waitFor({ role: 'button', name: 'Notes 1' });
  });

  await runner.step('a_comment_typed_over_a_selection_marks_it', async () => {
    await selectAll(1);
    await driver.click(part('toolbar.comment'));
    await driver.typeInto(part('composer.field'), COMMENT);
    await driver.pressKey(`${MOD}+Enter`);
    await driver.waitFor(part('composer'), { gone: true });
    await driver.waitFor({ role: 'button', name: 'Notes 2' });
    await driver.screenshot(path.join(runner.runDir, 'native-markdown-marks.png'));
  });

  await runner.step('send_delivers_both_marks_to_the_bound_agent', async () => {
    await driver.waitFor({ role: 'button', name: 'Send 2 to reader' });
    await driver.click(part('send'));
    await driver.waitFor({ role: 'status', name: 'Sent ✓' });
    await driver.state(`terminal:${agent.runtimeId}`, { includes: COMMENT, timeoutMs: 30_000 });
    await driver.state(`terminal:${agent.runtimeId}`, { includes: 'got document feedback', timeoutMs: 30_000 });
    const draft = await stored();
    runner.assert((draft.annotations ?? []).length === 0, 'the send clears the stored draft', draft);
  });

  await runner.step('leave_no_tile_behind', async () => {
    await observer.profileCommand('desktop_remove_leaf', { desktop_id: desktop.id, leaf_id: tile });
    await driver.waitFor({ key: `tile.${tile}` }, { gone: true });
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ desktopId: desktop.id, sessionId: id, tile, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
