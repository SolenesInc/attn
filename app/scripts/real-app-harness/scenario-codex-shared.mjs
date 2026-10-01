#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { waitForPaneText } from './scenarioAssertions.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, { scenarioId: 'CODEX-SHARED', tier: 'tier2-local-mock-agent', prefix: 'scenario-codex-shared' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const owners = [];
const panes = new Map();
async function type(id, paneId, text) { await client.request('type_pane_via_ui', { sessionId: id, paneId, text }); }
async function resolved(runtimeId, ownerId) {
  return observer.waitFor(() => [...observer.layoutsByWorkspaceId.values()].flatMap(layout => layout.panes || []).find(pane => pane.runtime_id === runtimeId && pane.session_id === ownerId && pane.codex_resolution === 'resolved'), `runtime ${runtimeId} displays ${ownerId}`);
}
try {
  await runner.step('launch', () => launchFreshAppAndConnect(client, observer));
  observer.send({ cmd: 'set_setting', key: 'codex_shared_enabled', value: 'true' });
  await observer.waitFor(() => observer.getSetting('codex_shared_enabled') === 'true', 'shared launch default');
  const roots = [];
  for (const name of ['exo', 'foo']) {
    const cwd = path.join(runner.sessionDir, name); fs.mkdirSync(cwd, { recursive: true });
    writeMockAgentFixture(cwd, { agent: 'codex', resumable: true, turns: [], defaultActions: [{ type: 'reply', text: `reply ${name}` }] });
    const { sessionId } = await client.request('create_session', { cwd, agent: 'codex', label: name }); owners.push(sessionId);
    const pane = await resolved(sessionId, sessionId); panes.set(sessionId, pane);
    const result = await waitForPaneText(client, sessionId, pane.pane_id, text => /Root ([0-9a-f-]{36})/.test(text), `native root ${name}`);
    const text = result.text.match(/Root ([0-9a-f-]{36})/)[1]; roots.push(text);
  }
  const [a, b] = owners; const paneA = panes.get(a);
  await runner.step('switch_native_owner_without_replacing_pane', async () => {
    await client.request('select_session', { sessionId: a });
    await type(a, paneA.pane_id, `/agents ${roots[1]}\r`); await resolved(a, b);
    const text = await client.request('read_pane_text', { sessionId: a, paneId: paneA.pane_id });
    runner.assert(text.text.includes('OpenAI Codex shared mock'), 'foreign owner removed the terminal', text);
    await type(a, paneA.pane_id, 'draft about B');
  });
  await runner.step('ledger_attach_focuses_the_new_view', async () => {
    await client.request('dispatch_shortcut', { shortcutId: 'sessions.open' });
    await client.request('sessions_row_action', { sessionId: a, action: 'Open another view' });
    const second = await observer.waitFor(() => [...observer.layoutsByWorkspaceId.values()].flatMap(layout => layout.panes || []).find(pane => pane.session_id === a && pane.runtime_id !== a && pane.codex_resolution === 'resolved'), 'second native A view');
    panes.set(second.runtime_id, second);
    const state = await client.request('get_state'); runner.assert(state.activeSessionId === a, 'new view owner is not selected', state);
    const workspace = await client.request('get_workspace', { sessionId: a });
    runner.assert(workspace.activePaneId === second.pane_id, 'ledger selected the original pane instead of the new view', workspace);
    await client.request('focus_pane', { sessionId: a, paneId: paneA.pane_id });
    const draft = await client.request('read_pane_text', { sessionId: a, paneId: paneA.pane_id });
    runner.assert(draft.text.includes('draft about B'), 'attachment lost another view draft', draft);
    await type(a, paneA.pane_id, `\x15/agents ${roots[0]}\r`); await resolved(a, a);
  });
  await runner.step('new_session_from_an_unresolved_shared_pane', async () => {
    await type(a, paneA.pane_id, '/title unknown-root\r');
    await observer.waitFor(() => [...observer.layoutsByWorkspaceId.values()].flatMap(layout => layout.panes || []).some(pane => pane.runtime_id === a && pane.codex_resolution === 'unresolved' && !pane.session_id), 'unresolved selected native view');
    await client.request('dispatch_shortcut', { shortcutId: 'session.new' });
    await client.request('dom_wait', { selector: '[data-testid="location-picker-title"]' });
    const title = await client.request('dom_text', { selector: '[data-testid="location-picker-title"]' });
    runner.assert(title.text === 'New Session Location', 'unresolved pane opened the wrong location picker', title);
    await client.request('dom_key', { selector: '[data-testid="location-picker"]', key: 'Escape' });
    await type(a, paneA.pane_id, `/cached ${roots[0]}\r`);
    await resolved(a, a);
  });
  await runner.step('native_new_keeps_shared_mode_after_default_off', async () => {
    observer.send({ cmd: 'set_setting', key: 'codex_shared_enabled', value: 'false' });
    await observer.waitFor(() => observer.getSetting('codex_shared_enabled') === 'false', 'default off');
    await type(a, paneA.pane_id, '/new\r');
    const changed = await observer.waitFor(() => [...observer.layoutsByWorkspaceId.values()].flatMap(layout => layout.panes || []).find(pane => pane.runtime_id === a && pane.session_id && !owners.includes(pane.session_id) && pane.codex_resolution === 'resolved'), 'new native owner');
    owners.push(changed.session_id);
  });
  console.log(JSON.stringify(await runner.finishSuccess({ owners, roots }), null, 2));
} catch (error) { console.error((await runner.finishFailure(error, { owners })).error); process.exitCode = 1; }
finally {
  for (const layout of observer.layoutsByWorkspaceId.values()) for (const pane of layout.panes || []) {
    if (pane.codex_resolution) await observer.requestResult({ cmd: 'workspace_layout_close_pane', workspace_id: layout.workspace_id, pane_id: pane.pane_id }, 'workspace_layout_action_result').catch(() => {});
  }
  observer.send({ cmd: 'set_setting', key: 'codex_shared_enabled', value: 'false' });
  await client.quitApp().catch(() => {}); await observer.close();
}
