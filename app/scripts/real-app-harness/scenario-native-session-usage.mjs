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
  scenarioId: 'NATIVE-SESSION-USAGE', tier: 'tier2-local-mock-agent', prefix: 'native-session-usage',
  metadata: { focus: 'A native tile header counts a Claude session\'s usage with its native subagents, keeps a partial dollar amount, previews the breakdown on hover and pins it from the command palette, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const LABEL = 'usage receipt';
const mac = process.platform === 'darwin';
const PALETTE = mac ? 'mod+shift+k' : 'mod+alt+k';

function claudeUsageRecord({ id, model, input, output, cacheRead = 0, cacheWrite5m = 0 }) {
  return JSON.stringify({
    type: 'assistant',
    uuid: `mock-${id}`,
    timestamp: new Date().toISOString(),
    message: {
      id,
      model,
      role: 'assistant',
      content: [{ type: 'text', text: `usage fixture ${id}` }],
      usage: {
        input_tokens: input,
        output_tokens: output,
        cache_read_input_tokens: cacheRead,
        cache_creation_input_tokens: cacheWrite5m,
        cache_creation: { ephemeral_5m_input_tokens: cacheWrite5m, ephemeral_1h_input_tokens: 0 },
      },
    },
  });
}

const breakdown = async (key) => {
  const names = [];
  const walk = (node, inside) => {
    const here = inside || node.key === key;
    if (here && node.name) names.push(node.name);
    (node.children ?? []).forEach((child) => walk(child, here));
  };
  walk(await driver.tree(), false);
  return names;
};

try {
  const frontmostBefore = mac ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  const id = crypto.randomUUID();
  const cwd = path.join(runner.sessionDir, 'agent');
  writeMockAgentFixture(cwd, {
    name: 'Usage receipt',
    turns: [{ includes: 'Measure this session', actions: [{ type: 'reply', text: 'Primary conversation measured.', state: 'waiting_input' }] }],
  });

  let runtimeId;
  let leaf;
  await runner.step('spawn_claude', async () => {
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === id && m, 'agent spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id, agent: 'claude', cwd, profile_id: observer.profileId, placement: {}, label: LABEL, cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session starts the mock Claude');
    runner.registerCleanup('unregister_agent', () => observer.unregisterSession(id));
    await driver.waitFor({ key: `sidebar.session.${id}` });
    await driver.click({ key: `sidebar.session.${id}` });
    const desktops = await driver.state('desktops', { includes: id });
    const pane = desktops.flatMap((d) => d.panes ?? []).find((p) => p.session_id === id);
    runtimeId = pane.runtime_id;
    leaf = pane.pane_id;
    await driver.waitFor({ key: `terminal.${runtimeId}` });
  });

  const badge = () => ({ key: `tile.${leaf}.usage` });
  let transcript;
  await runner.step('one_turn_shows_its_tokens', async () => {
    await driver.state(`terminal:${runtimeId}`, { includes: 'Claude Code', timeoutMs: 60_000 });
    await driver.click({ key: `terminal.${runtimeId}` });
    await driver.typeInto(null, 'Measure this session');
    await driver.pressKey('Enter');
    await driver.state(`terminal:${runtimeId}`, { includes: 'Primary conversation measured.', timeoutMs: 30_000 });
    const shown = await driver.waitFor({ role: 'button', name: 'Session usage 2.1k tokens, some usage has no price' });
    runner.assert(shown.key === badge().key, 'the header of the agent\'s tile carries the badge', shown);
    const dir = path.join(cwd, '.attn-mock-agent');
    const files = fs.readdirSync(dir).filter((name) => name.endsWith('.jsonl'));
    runner.assert(files.length === 1, 'the mock wrote one transcript', files);
    transcript = path.join(dir, files[0]);
  });

  await runner.step('native_subagents_and_a_priced_model_add_up', async () => {
    const childDir = `${transcript.slice(0, -'.jsonl'.length)}/subagents`;
    fs.mkdirSync(childDir, { recursive: true });
    fs.writeFileSync(path.join(childDir, 'agent-private-evaluator.jsonl'), `${claudeUsageRecord({ id: 'child-usage', model: 'private-evaluator', input: 40_000, output: 2_000 })}\n`);
    fs.appendFileSync(transcript, `${claudeUsageRecord({ id: 'priced-root-usage', model: 'claude-sonnet-5', input: 500_000, output: 20_000, cacheRead: 100_000, cacheWrite5m: 50_000 })}\n`);
    const shown = await driver.waitFor({ role: 'button', name: 'Session usage $1.35*, some usage has no price' });
    runner.assert(shown.key === badge().key, 'the badge keeps the known dollars and marks them partial', shown);
  });

  const popover = `tile.${leaf}.usage.popover`;
  await runner.step('hover_previews_the_breakdown', async () => {
    await driver.hover(badge());
    await driver.waitFor({ key: popover });
    const lines = await breakdown(popover);
    runner.writeJson('hover-breakdown.json', lines);
    for (const line of ['714,095 tokens', 'claude-sonnet-5', 'private-evaluator']) {
      runner.assert(lines.some((l) => l.includes(line)), `the breakdown shows ${line}`, lines);
    }
    runner.assert(!lines.some((l) => l.includes('esc close')), 'a preview has no close hint', lines);
    await driver.screenshot(path.join(runner.runDir, 'native-usage-preview.png'));
    await driver.hover({ key: `terminal.${runtimeId}` });
    await driver.waitFor({ key: popover }, { gone: true });
  });

  await runner.step('the_palette_pins_the_breakdown_and_escape_closes_it', async () => {
    await driver.pressKey(PALETTE);
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, `${LABEL} usage`);
    await driver.waitFor({ key: 'palette.row.command:show-session-usage' });
    await driver.pressKey('Enter');
    await driver.waitFor({ key: popover });
    const lines = await breakdown(popover);
    runner.assert(lines.some((l) => l.includes('esc close')) && lines.some((l) => l.includes('714,095 tokens')), 'the palette pins the same breakdown', lines);
    await driver.screenshot(path.join(runner.runDir, 'native-usage-pinned.png'));
    await driver.pressKey('Escape');
    await driver.waitFor({ key: popover }, { gone: true });
  });

  const frontmostAfter = mac ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });
  await runner.finishSuccess({ sessionId: id, frontmostBefore, frontmostAfter });
} catch (error) {
  await driver.screenshot(path.join(runner.runDir, 'failure.png')).catch(() => {});
  await driver.tree().then((tree) => runner.writeJson('failure-tree.json', tree)).catch(() => {});
  await runner.finishFailure(error);
  process.exitCode = 1;
}
