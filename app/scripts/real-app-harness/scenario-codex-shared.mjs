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
let resumeViewer;
let resumeViewerPane;
async function request(cmd, event, fields) {
  const request_id = `${cmd}-${crypto.randomUUID()}`;
  const reply = observer.waitForMessage(message => message.event === event && message.request_id === request_id ? message : null, event);
  observer.send({ cmd, request_id, ...fields });
  const result = await reply;
  runner.assert(result.success, `${cmd} failed`, result);
  return result;
}
async function type(id, paneId, text) { await client.request('type_pane_via_ui', { sessionId: id, paneId, text }); }
async function closePane(workspaceId, paneId) {
  const pending = observer.waitForMessage(event => event.event === 'workspace_layout_action_result' && event.action === 'workspace_layout_close_pane' && event.workspace_id === workspaceId && event.pane_id === paneId ? event : null, 'shared pane close result');
  observer.send({ cmd: 'workspace_layout_close_pane', workspace_id: workspaceId, pane_id: paneId });
  const result = await pending;
  if (!result.success) throw new Error(result.error || 'shared pane close failed');
}
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
    writeMockAgentFixture(cwd, { agent: 'codex', resumable: true, turns: [{ includes: 'needs approval', actions: [{ type: 'reply', approval: true, text: `approved ${name}` }] }], defaultActions: [{ type: 'reply', text: `reply ${name}` }] });
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
  await runner.step('queue_lists_shared_owner_once_and_opens_its_workspace', async () => {
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await client.request('dom_wait', { selector: '[data-testid="sidebar-queue"]', timeoutMs: observer.connectTimeoutMs });
    const queue = await client.request('queue_get_state');
    const rows = [...queue.turns, ...queue.settled, ...queue.pinned, ...queue.snoozed.rows].filter(row => row.id === b);
    const workspaceId = observer.sessionsById.get(b).workspace_id;
    runner.assert(rows.length === 1 && rows[0].workspaceId === workspaceId, 'shared owner has duplicate or misplaced queue rows', { queue, workspaceId });
    await client.request('dom_click', { selector: `[data-testid="queue-${queue.turns.some(row => row.id === b) ? 'turn' : 'settled'}-${b}"] .queue-row-select` });
    await client.request('dom_wait', { selector: `[data-session-terminal-workspace="${workspaceId}"][data-session-visible="1"]`, timeoutMs: observer.connectTimeoutMs });
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    await client.request('select_workspace', { workspaceId: observer.sessionsById.get(a).workspace_id });
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
  await runner.step('hidden_approval_queue_attaches_native_view', async () => {
    const second = [...panes.values()].find(pane => pane.session_id === a && pane.runtime_id !== a);
    await closePane(observer.sessionsById.get(a).workspace_id, second.pane_id);
    await type(a, paneA.pane_id, `/agents ${roots[1]}\r`); await resolved(a, b);
    await request('session_annotations_submit', 'session_annotations_submit_result', { session_id: a, text: 'A needs approval' });
    await observer.waitFor(() => observer.getSession(a)?.state === 'pending_approval', 'hidden A requires approval');
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await client.request('dom_wait', { selector: `[data-testid="queue-turn-${a}"] .queue-row-select`, timeoutMs: observer.connectTimeoutMs });
    await client.request('dom_click', { selector: `[data-testid="queue-turn-${a}"] .queue-row-select` });
    const attached = await observer.waitFor(() => [...observer.layoutsByWorkspaceId.values()].flatMap(layout => layout.panes || []).find(pane => pane.session_id === a && pane.runtime_id !== a && pane.codex_resolution === 'resolved'), 'queue attached hidden A');
    await waitForPaneText(client, a, attached.pane_id, text => text.includes('Allow the command to run?'), 'attached native approval');
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    await client.request('focus_pane', { sessionId: a, paneId: paneA.pane_id });
    await type(a, paneA.pane_id, `/agents ${roots[0]}\r`); await resolved(a, a);
    await waitForPaneText(client, a, paneA.pane_id, text => text.includes('Allow the command to run?'), 'same approval in both views');
    const recordedReply = observer.waitForMessage(message => message.event === 'session_messages_changed' && message.session_id === a ? message : null, 'approval reply recorded for annotations');
    await type(a, attached.pane_id, '\r');
    await observer.waitFor(() => observer.getSession(a)?.state === 'waiting_input', 'one native approval resolves both views');
    await waitForPaneText(client, a, paneA.pane_id, text => text.includes('approved exo'), 'approval reply in original view');
    await recordedReply;
    await closePane(observer.sessionsById.get(a).workspace_id, attached.pane_id);
  });
  await runner.step('annotation_editor_keeps_A_when_native_view_switches_to_B', async () => {
    const messages = await request('session_messages_get', 'session_messages_get_result', { session_id: a });
    const message = messages.messages.find(message => message.markdown.includes('approved exo'));
    runner.assert(message, 'approval reply has no annotatable owner message', messages);
    await request('session_annotations_save', 'session_annotations_save_result', { session_id: a, generation: 1, annotations: [{ id: 'shared-A-mark', message_key: message.key, start: 0, end: 8, quote: 'approved', comment: '' }] });
    await type(a, paneA.pane_id, `/agents ${roots[1]}\r`); await resolved(a, b);
    await type(a, paneA.pane_id, `/agents ${roots[0]}\r`); await resolved(a, a);
    await client.request('dom_wait', { selector: '.anno-card-open', timeoutMs: observer.connectTimeoutMs });
    await client.request('dom_click', { selector: '.anno-card-open' });
    await client.request('dom_click', { selector: '[aria-label="Write a comment"]' });
    await client.request('dom_type', { selector: '.anno-popup-text', text: 'keep this feedback on A' });
    observer.send({ cmd: 'pty_input', id: a, source: 'automation', data: `/agents ${roots[1]}\r` });
    await resolved(a, b);
    const title = await client.request('dom_text', { selector: '.anno-panel-title' });
    runner.assert(title.text.includes('Annotations for exo'), 'open editor silently changed recipient', title);
    const sent = observer.waitForMessage(message => message.event === 'session_annotations_submit_result' && message.session_id === a ? message : null, 'annotation still submitted to A');
    await client.request('dom_click', { selector: '.anno-panel-send' });
    runner.assert((await sent).success, 'annotation did not submit to captured owner A');
    await observer.waitFor(() => observer.getSession(a)?.state === 'waiting_input', 'A feedback completes');
    await type(a, paneA.pane_id, `/agents ${roots[0]}\r`); await resolved(a, a);
  });
  await runner.step('new_session_from_an_unresolved_shared_pane', async () => {
    await type(a, paneA.pane_id, '/title unknown-root\r');
    await observer.waitFor(() => [...observer.layoutsByWorkspaceId.values()].flatMap(layout => layout.panes || []).some(pane => pane.runtime_id === a && pane.codex_resolution === 'unresolved' && !pane.session_id), 'unresolved selected native view');
    await client.request('dispatch_shortcut', { shortcutId: 'session.new' });
    await client.request('dom_wait', { selector: '[data-testid="location-picker-title"]', timeoutMs: observer.connectTimeoutMs });
    const title = await client.request('dom_text', { selector: '[data-testid="location-picker-title"]' });
    runner.assert(title.text === 'New Session Location', 'unresolved pane opened the wrong location picker', title);
    await client.request('dom_key', { selector: '[data-testid="location-picker"]', key: 'Escape' });
    await type(a, paneA.pane_id, `/cached ${roots[0]}\r`);
    await resolved(a, a);
  });
  await runner.step('moved_shared_view_has_no_phantom_source_row', async () => {
    const source = observer.sessionsById.get(b).workspace_id;
    const destination = observer.sessionsById.get(a).workspace_id;
    const moved = observer.waitForMessage(event => event.event === 'workspace_layout_action_result' && event.action === 'workspace_layout_move_leaf_to_workspace' && event.source_workspace_id === source ? event : null, 'shared view move result');
    observer.send({ cmd: 'workspace_layout_move_leaf_to_workspace', source_workspace_id: source, target_workspace_id: destination, leaf_id: panes.get(b).pane_id, edge: 'right' });
    const result = await moved;
    runner.assert(result.success, 'shared view move failed', result);
    await observer.waitFor(() => observer.layoutsByWorkspaceId.get(destination)?.panes.some(pane => pane.runtime_id === b), 'moved shared pane');
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await client.request('dom_wait', { selector: '[data-testid="sidebar-queue"]', timeoutMs: observer.connectTimeoutMs });
    const queue = await client.request('queue_get_state');
    const rows = [...queue.turns, ...queue.settled].filter(row => row.id === b);
    runner.assert(rows.length === 1 && rows[0].workspaceId === destination, 'moved owner queues in its empty source workspace', { queue, destination });
    await client.request('dom_click', { selector: `[data-testid="queue-${queue.turns.some(row => row.id === b) ? 'turn' : 'settled'}-${b}"] .queue-row-select` });
    await client.request('dom_wait', { selector: `[data-session-terminal-workspace="${destination}"][data-session-visible="1"]`, timeoutMs: observer.connectTimeoutMs });
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    const tree = await client.request('queue_get_state');
    runner.assert(tree.treeSessionIds.filter(id => id === b).length === 1, 'moved owner has a phantom source row', tree);
    await client.request('focus_pane', { sessionId: a, paneId: paneA.pane_id });
  });
  await runner.step('native_new_keeps_shared_mode_after_default_off', async () => {
    observer.send({ cmd: 'set_setting', key: 'codex_shared_enabled', value: 'false' });
    await observer.waitFor(() => observer.getSetting('codex_shared_enabled') === 'false', 'default off');
    await type(a, paneA.pane_id, '/new\r');
    const changed = await observer.waitFor(() => [...observer.layoutsByWorkspaceId.values()].flatMap(layout => layout.panes || []).find(pane => pane.runtime_id === a && pane.session_id && !owners.includes(pane.session_id) && pane.codex_resolution === 'resolved'), 'new native owner');
    owners.push(changed.session_id);
  });
  await runner.step('native_resume_restores_placement_after_workspace_removal', async () => {
    await client.request('set_setting', { key: 'codex_shared_enabled', value: 'true' });
    const cwd = path.join(runner.sessionDir, 'resume-viewer'); fs.mkdirSync(cwd, { recursive: true });
    writeMockAgentFixture(cwd, { agent: 'codex', resumable: true, turns: [] });
    const { sessionId: viewer } = await client.request('create_session', { cwd, agent: 'codex', label: 'resume-viewer' });
    owners.push(viewer);
    const pane = await resolved(viewer, viewer);
    resumeViewer = viewer; resumeViewerPane = pane;
    await client.request('set_setting', { key: 'codex_shared_enabled', value: 'false' });
    const source = observer.sessionsById.get(a).workspace_id;
    const removed = observer.waitForMessage(event => event.event === 'workspace_unregistered' && event.workspace.id === source ? event : null, 'original workspace removed');
    observer.send({ cmd: 'unregister_workspace', id: source });
    await removed;
    const registered = observer.waitForMessage(event => event.event === 'workspace_registered' ? event.workspace : null, 'native resume replacement workspace');
    await type(viewer, pane.pane_id, `/agents ${roots[0]}\r`);
    await resolved(viewer, a);
    const replacement = await registered;
    runner.assert(observer.sessionsById.get(a)?.workspace_id === replacement.id, 'native resume did not assign the registered workspace', { replacement, owner: observer.sessionsById.get(a) });
    await type(viewer, pane.pane_id, '/new\r');
    const successor = await observer.waitFor(() => [...observer.layoutsByWorkspaceId.values()].flatMap(layout => layout.panes || []).find(entry => entry.runtime_id === viewer && entry.session_id && !owners.includes(entry.session_id) && entry.codex_resolution === 'resolved'), 'native New after resume');
    owners.push(successor.session_id);
    runner.assert(observer.sessionsById.get(successor.session_id)?.workspace_id === replacement.id, 'native New inherited removed workspace', observer.sessionsById.get(successor.session_id));
  });
  await runner.step('last_shared_pane_close_clears_the_running_app', async () => {
    const workspaceId = observer.sessionsById.get(resumeViewer).workspace_id;
    const empty = observer.waitForMessage(event => event.event === 'workspace_layout_updated' && event.workspace_layout.workspace_id === workspaceId && event.workspace_layout.panes.length === 0 ? event : null, 'last shared pane clears the layout');
    await closePane(workspaceId, resumeViewerPane.pane_id);
    await empty;
    await client.request('select_session', { sessionId: resumeViewer });
    const workspace = await client.request('get_workspace', { sessionId: resumeViewer });
    runner.assert(workspace.panes.length === 0, 'closed shared pane remains in the running app', workspace);
  });
  console.log(JSON.stringify(await runner.finishSuccess({ owners, roots }), null, 2));
} catch (error) { console.error((await runner.finishFailure(error, { owners })).error); process.exitCode = 1; }
finally {
  for (const layout of observer.layoutsByWorkspaceId.values()) for (const pane of layout.panes || []) {
    if (pane.codex_resolution) await closePane(layout.workspace_id, pane.pane_id).catch(() => {});
  }
  observer.send({ cmd: 'set_setting', key: 'codex_shared_enabled', value: 'false' });
  observer.send({ cmd: 'set_setting', key: 'queue_mode_enabled', value: 'false' });
  await client.quitApp().catch(() => {}); await observer.close();
}
