#!/usr/bin/env node

import crypto from 'node:crypto';
import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-TERMINAL-ANNOTATIONS', tier: 'tier2-local-mock-agent', prefix: 'native-terminal-annotations',
  metadata: { focus: 'An Alt-drag over an agent\'s message in the native app marks it; a label and a comment save the draft with the daemon; the note and the send key deliver the batch, which the agent answers' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const MESSAGE = 'A retry wrapper protects idempotent operations from duplicate network effects.';
const PHRASE = 'retry wrapper';
const COMMENT = 'Which operations are idempotent?';
const NOTE = 'Keep it short.';
const MOD = process.platform === 'darwin' ? 'mod' : 'ctrl';

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, 'agent');
  writeMockAgentFixture(cwd, {
    turns: [{ includes: 'say-prose', actions: [{ type: 'reply', text: MESSAGE }] }],
    defaultActions: [{ type: 'reply', text: 'got terminal feedback' }],
  });
  await runner.step('spawn_agent', async () => {
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'agent spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id, agent: 'codex', cwd, profile_id: observer.profileId, placement: {}, label: 'anno', cols: 100, rows: 30 });
    const result = await spawned;
    runner.assert(result.success, 'spawn_session starts the agent', result);
    runner.registerCleanup('unregister_agent', () => observer.unregisterSession(id));
  });

  let runtimeId;
  await runner.step('open_terminal', async () => {
    const desktops = await driver.state('desktops');
    const pane = desktops.flatMap((desktop) => desktop.panes ?? []).find((p) => p.session_id === id);
    runner.assert(pane?.runtime_id, 'a desktop holds the agent as a pane with a terminal', desktops);
    runtimeId = pane.runtime_id;
    await driver.waitFor({ key: `sidebar.session.${id}` });
    await driver.click({ key: `sidebar.session.${id}` });
    await driver.waitFor({ key: `terminal.${runtimeId}` });
  });
  const terminal = { key: `terminal.${runtimeId}` };
  const annotations = async (includes) => (await driver.state(`terminal-view:${runtimeId}`, includes ? { includes, timeoutMs: 15_000 } : {})).annotations;
  const stored = () => observer.requestResult({ cmd: 'session_annotations_get', session_id: id }, 'session_annotations_get_result');

  let at;
  await runner.step('agent_replies', async () => {
    await driver.click(terminal);
    await driver.state(`terminal:${runtimeId}`, { includes: 'OpenAI Codex', timeoutMs: 60_000 });
    await driver.typeInto(null, 'say-prose');
    await driver.pressKey('Enter');
    const rows = await driver.state(`terminal:${runtimeId}`, { includes: 'duplicate network effects.', timeoutMs: 30_000 });
    let message;
    for (;;) {
      const changed = observer.waitForMessage((m) => m.event === 'session_messages_changed' && m.session_id === id, 'the agent\'s message window to change', 30_000);
      const window = await observer.requestResult({ cmd: 'session_messages_get', session_id: id }, 'session_messages_get_result');
      message = (window.messages ?? []).find((m) => m.markdown.includes(MESSAGE));
      if (message) {
        changed.catch(() => {});
        break;
      }
      await changed;
    }
    await driver.state(`terminal-view:${runtimeId}`, { includes: message.key, timeoutMs: 15_000 });
    const slot = await driver.waitFor(terminal);
    const caret = await driver.waitFor({ key: `terminal.${runtimeId}.caret` });
    const { cursor } = await driver.state(`terminal-view:${runtimeId}`);
    const [cx, cy, cw, ch] = caret.rect;
    const row = rows.findIndex((text) => text.includes(PHRASE));
    const col = [...rows[row]].join('').indexOf(PHRASE);
    const x = cx - cursor[0] * cw + (col + 0.5) * cw;
    const y = cy - cursor[1] * ch + (row + 0.5) * ch;
    const [sx, sy, sw, sh] = slot.rect;
    at = { at: [(x - sx) / sw, (y - sy) / sh], cell: cw };
  });

  await runner.step('alt_drag_marks_the_phrase', async () => {
    await driver.holdKeys('alt');
    await driver.drag(terminal, { by: [(PHRASE.length - 1) * at.cell, 0] }, { at: at.at });
    await driver.holdKeys('');
    const state = await annotations(PHRASE);
    runner.assert(state.marks.length === 1 && state.marks[0].quote === PHRASE && state.popup, 'the drag marks the phrase and opens its popup', state);
  });

  await runner.step('label_and_comment_save_the_draft', async () => {
    await driver.click({ key: `terminal.${runtimeId}.annotation.label.clarify-this` });
    await driver.holdKeys('alt');
    await driver.click(terminal, { at: at.at });
    await driver.holdKeys('');
    await driver.click({ key: `terminal.${runtimeId}.annotation.write` });
    await driver.typeInto(null, COMMENT);
    await driver.pressKey(`${MOD}+Enter`);
    await annotations(COMMENT);
    const draft = await stored();
    const mark = draft.annotations?.[0];
    runner.assert(mark?.quick_label_id === 'clarify-this' && mark?.comment === COMMENT, 'the daemon keeps the mark with its label and comment', draft);
  });

  await runner.step('note_and_send', async () => {
    await driver.click({ key: `terminal.${runtimeId}.annotation.note.field` });
    await driver.typeInto(null, NOTE);
    await annotations(NOTE);
    const screenshot = path.join(runner.runDir, 'native-annotations-panel.png');
    await driver.screenshot(screenshot);
    await driver.pressKey(`${MOD}+Enter`);
    await driver.state(`terminal:${runtimeId}`, { includes: 'got terminal feedback', timeoutMs: 30_000 });
    const sent = await driver.state(`terminal-view:${runtimeId}`);
    runner.assert(sent.annotations.marks.length === 0, 'the send clears the marks on screen', sent.annotations);
    const draft = await stored();
    runner.assert((draft.annotations ?? []).length === 0, 'the send clears the stored draft', draft);
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ sessionId: id, runtimeId, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
