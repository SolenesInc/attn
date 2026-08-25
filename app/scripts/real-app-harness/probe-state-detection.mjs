#!/usr/bin/env node

import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  ensureClaudeInitialPanePromptReady,
  ensureCodexInitialPanePromptReady,
} from './scenarioAgents.mjs';

const AGENT = process.env.PROBE_AGENT ?? 'claude';

const PROMPT =
  process.env.PROBE_PROMPT ??
  'Run exactly this and report the output verbatim: sleep 12 && echo PROBE-DONE . Do not read any files. Then stop.';

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);

  const transitions = [];
  let t0 = Date.now();
  const stamp = () => ((Date.now() - t0) / 1000).toFixed(2).padStart(7);

  await launchFreshAppAndConnect(client, observer);

  if (process.env.PROBE_GUARDIAN) {
    const enabled = process.env.PROBE_GUARDIAN === 'on';
    await client.request('set_setting', {
      key: 'auto_approve_enabled',
      value: String(enabled),
    });
    console.log(`guardian auto-approve ${enabled ? 'enabled' : 'disabled'} for this run`);
  }

  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd: process.cwd(),
    label: `probe-state-${Date.now()}`,
    agent: AGENT,
    promptReadyFn:
      AGENT === 'codex' ? ensureCodexInitialPanePromptReady : ensureClaudeInitialPanePromptReady,
  });
  console.log(`session ${sessionId}`);

  // Poll the daemon rather than subscribing: a dropped broadcast would hide
  // exactly the flicker this probe exists to catch, and a poll cannot.
  let last = null;
  const poll = setInterval(async () => {
    try {
      const state = await client.request('get_state', {});
      const session = (state.sessions ?? []).find((s) => s.id === sessionId);
      if (session && session.state !== last) {
        transitions.push({ at: stamp(), state: session.state });
        console.log(`${stamp()}s  ${session.state}`);
        last = session.state;
      }
    } catch {
      /* the app is busy; the next tick will catch up */
    }
  }, 200);

  t0 = Date.now();
  console.log(`--- prompt submitted ---`);
  await client.request('write_pane', { sessionId, text: PROMPT });
  await new Promise((r) => setTimeout(r, 800));
  await client.request('write_pane', { sessionId, text: '\r' });

  await new Promise((r) => setTimeout(r, Number(process.env.PROBE_SECONDS ?? 75) * 1000));
  clearInterval(poll);

  console.log('\n=== timeline ===');
  for (const t of transitions) console.log(`  ${t.at}s  ${t.state}`);
  console.log(`\nsession ${sessionId}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
