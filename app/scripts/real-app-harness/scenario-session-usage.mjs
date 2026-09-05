#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  submitPrompt,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { delay } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { recordingEnabled } from './windowRecording.mjs';

const PACE_MS = recordingEnabled() ? 2_500 : 0;
const TERMINAL_SELECTOR = '.terminal-wrapper.active .terminal-container';

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
        cache_creation: {
          ephemeral_5m_input_tokens: cacheWrite5m,
          ephemeral_1h_input_tokens: 0,
        },
      },
    },
  });
}

async function poll(read, description, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await read();
    if (last) return last;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last value: ${JSON.stringify(last)}`);
}

async function pressShortcut(client, shortcutId) {
  const { binding } = await client.request('shortcut_binding', { shortcutId });
  if (!binding) throw new Error(`shortcut ${shortcutId} is unbound`);
  const press = (combo) => client.request('dom_terminal_key', {
    selector: TERMINAL_SELECTOR,
    key: combo.key,
    code: combo.code || (/^[a-z]$/i.test(combo.key) ? `Key${combo.key.toUpperCase()}` : combo.key),
    modifiers: {
      meta: process.platform === 'darwin' && !!combo.meta,
      ctrl: !!combo.ctrl || (process.platform === 'linux' && !!combo.meta),
      shift: !!combo.shift,
      alt: !!combo.alt,
    },
  });
  if (binding.leader) await press(binding.leader);
  await press(binding.then || binding);
}

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  if (args.includes('--help') || args.includes('-h')) {
    printCommonHelp('scripts/real-app-harness/scenario-session-usage.mjs');
    return;
  }
  const options = parseCommonArgs(args);
  const runner = createScenarioRunner(options, {
    scenarioId: 'SESSION-USAGE',
    tier: 'tier1-local-agent',
    prefix: 'session-usage',
    metadata: { focus: 'native subagent accounting, partial pricing, hover preview, and Action menu pinning' },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;
  let receipt = null;
  let rootPath = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session', async () => {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
  });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      await client.request('dom_click', { selector: '[aria-label="Close what\'s new"]' }).catch((error) => {
        if (!String(error).includes('selector not found in DOM')) throw error;
      });
    });
    await runner.step('create_claude_session', async () => {
      writeMockAgentFixture(runner.sessionDir, {
        name: 'Usage receipt',
        turns: [{
          includes: 'Measure this session',
          actions: [{ type: 'reply', text: 'Primary conversation measured.', state: 'waiting_input' }],
        }],
      });
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: 'Usage receipt',
        agent: 'claude',
        sessionWaitMs: 60_000,
        promptReadyFn: ensureClaudePromptReadyViaPty,
        promptReadyTimeoutMs: 60_000,
      });
      await client.request('select_session', { sessionId });
      const pane = await waitForFirstWorkspacePane(client, sessionId, 'usage receipt pane', 20_000);
      await submitPrompt(client, sessionId, pane.paneId, 'Measure this session');
      const transcriptDir = path.join(runner.sessionDir, '.attn-mock-agent');
      rootPath = await poll(() => {
        const names = fs.existsSync(transcriptDir)
          ? fs.readdirSync(transcriptDir).filter((name) => name.endsWith('.jsonl'))
          : [];
        return names.length === 1 ? path.join(transcriptDir, names[0]) : null;
      }, 'the mock Claude transcript');
      await poll(
        () => fs.readFileSync(rootPath, 'utf8').includes('attn:state=waiting_input'),
        'the mock agent state marker',
      );
      await poll(
        () => observer.getSession(sessionId)?.usage?.total_tokens === 2_095,
        'the complete primary usage receipt',
      );
    });

    await runner.step('add_native_subagent_usage', async () => {
      const childDir = `${rootPath.slice(0, -'.jsonl'.length)}/subagents`;
      fs.mkdirSync(childDir, { recursive: true });
      fs.writeFileSync(path.join(childDir, 'agent-private-evaluator.jsonl'), `${claudeUsageRecord({
        id: 'child-usage', model: 'private-evaluator', input: 40_000, output: 2_000,
      })}\n`);
      fs.appendFileSync(rootPath, `${claudeUsageRecord({
        id: 'priced-root-usage',
        model: 'claude-sonnet-5',
        input: 500_000,
        output: 20_000,
        cacheRead: 100_000,
        cacheWrite5m: 50_000,
      })}\n`);

      receipt = await poll(() => {
        const usage = observer.getSession(sessionId)?.usage;
        return usage?.models?.length === 3 ? usage : null;
      }, 'the combined primary and native subagent usage');
      runner.assert(receipt.total_tokens === 714_095, 'the session total includes all three native conversations', receipt);
      runner.assert(Math.abs(receipt.cost_usd - 1.345) < 0.000001, 'known model usage contributes its dollar amount', receipt);
      runner.assert(receipt.has_unpriced_usage === true, 'unknown model usage marks the dollar amount partial', receipt);
      runner.writeText('usage-receipt.json', `${JSON.stringify(receipt, null, 2)}\n`);
    });

    const badgeSelector = `[data-testid="session-usage-${sessionId}"]`;
    await runner.step('hover_the_breakdown', async () => {
      const badge = await poll(async () => {
        const result = await client.request('dom_text', { selector: badgeSelector }).catch(() => null);
        return result?.text === '$1.35*' ? result : null;
      }, 'the partial-cost header badge');
      runner.assert(badge.text === '$1.35*', 'the header keeps the known dollar amount and marks it partial', badge);
      await client.request('dom_hover', { selector: badgeSelector });
      const panel = await poll(async () => {
        const result = await client.request('dom_text', { selector: '.session-usage-popover' }).catch(() => null);
        return result?.text?.includes('private-evaluator') ? result : null;
      }, 'the usage hover preview');
      runner.assert(panel.text.includes('714,095 tokens'), 'the panel shows an exact session total', panel);
      runner.assert(panel.text.includes('claude-sonnet-5'), 'the priced model has its own receipt', panel);
      runner.assert(panel.text.includes('No price is configured for this model.'), 'the unknown model explains its missing price', panel);
      await client.request('dom_hover', { selector: badgeSelector, leave: true });
      await client.request('dom_hover', { selector: '.session-usage-popover' });
      await delay(350);
      const transferred = await client.request('dom_text', { selector: '.session-usage-popover' });
      runner.assert(transferred.text.includes('private-evaluator'), 'the pointer can move from the badge into the preview', transferred);
      if (PACE_MS) await delay(PACE_MS);
      await client.request('dom_hover', { selector: '.session-usage-popover', leave: true });
      await poll(async () => {
        const result = await client.request('dom_text', { selector: '.session-usage-popover' }).catch(() => null);
        return result ? null : true;
      }, 'the hover preview to close');
    });

    await runner.step('pin_from_the_action_menu', async () => {
      await pressShortcut(client, 'ui.actionMenu');
      await client.request('dom_type', { selector: '.action-menu input', text: 'tokens cost' });
      const menu = await client.request('dom_text', { selector: '.action-menu' });
      runner.assert(menu.text.includes("Show Usage receipt's usage"), 'the Action menu exposes the active session receipt', menu);
      await client.request('dom_click', { selector: '.action-menu-item' });
      const pinned = await poll(async () => {
        const result = await client.request('dom_text', { selector: '.session-usage-popover' }).catch(() => null);
        return result?.text?.includes('esc close') ? result : null;
      }, 'the pinned usage panel');
      runner.assert(pinned.text.includes('714,095 tokens'), 'the Action menu opens the same receipt', pinned);
      if (PACE_MS) await delay(PACE_MS);
      await client.request('dom_key', { selector: '.session-usage-popover', key: 'Escape' });
    });

    console.log(JSON.stringify(await runner.finishSuccess({ sessionId, receipt }), null, 2));
  } catch (error) {
    console.error((await runner.finishFailure(error, { sessionId, receipt })).error);
    process.exitCode = 1;
  } finally {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
