import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import WebSocket, { WebSocketServer } from 'ws';
import { conversationHeaderRecords, messageRecords, mockAgentSplash, readMockAgentConfig, selectMockAgentActions } from './mockAgent.mjs';

function arg(name) { const index = process.argv.indexOf(name); return index < 0 ? '' : process.argv[index + 1]; }
function hook(root, event, input = {}) {
  for (const group of root.config?.hooks?.[event] || []) for (const entry of group.hooks || []) {
    const result = spawnSync('/bin/sh', ['-c', entry.command], { cwd: root.cwd,
      env: { ...process.env, ...root.config?.shell_environment_policy?.set },
      input: JSON.stringify({ session_id: root.id, transcript_path: root.path, cwd: root.cwd, ...input }), encoding: 'utf8' });
    if (result.status !== 0) throw new Error(`shared mock ${event} hook: ${result.stderr}`);
  }
}

export async function runSharedMockServer() {
  const roots = new Map();
  const server = http.createServer();
  const peers = new WebSocketServer({ server });
  const broadcast = (method, params) => { for (const peer of peers.clients) if (peer.readyState === WebSocket.OPEN) peer.send(JSON.stringify({ method, params })); };
  const metadata = (root) => ({ id: root.id, name: root.name || null, cwd: root.cwd, path: root.path, source: 'cli', ephemeral: false, status: root.status });
  const status = (root, type, activeFlags = []) => { root.status = { type, activeFlags }; broadcast('thread/status/changed', { threadId: root.id, status: root.status }); };
  const approval = (root) => ({ id: `approval:${root.id}`, method: 'item/commandExecution/requestApproval', params: { threadId: root.id, turnId: root.pending.turnId, itemId: 'command', command: 'fixture command' } });
  const finish = (root, turnId, text, reply) => {
    root.turns.push({ id: turnId, status: 'completed' });
    const records = [...messageRecords({ agent: 'codex', role: 'user', text, sequence: root.turns.length }), ...messageRecords({ agent: 'codex', role: 'assistant', text: reply, sequence: root.turns.length })];
    fs.appendFileSync(root.path, records.join('\n') + '\n');
    broadcast('attn-fixture/reply', { threadId: root.id, text: reply }); hook(root, 'Stop', { last_assistant_message: reply });
    broadcast('turn/completed', { threadId: root.id, turn: { id: turnId } }); status(root, 'idle');
  };
  peers.on('connection', peer => peer.on('message', raw => {
    const message = JSON.parse(raw);
    if (message.id === undefined) return;
    if (!message.method) {
      const root = [...roots.values()].find(root => root.pending && message.id === `approval:${root.id}`);
      if (root) {
        const { turnId, text, reply } = root.pending; root.pending = null;
        broadcast('serverRequest/resolved', { threadId: root.id, requestId: message.id }); status(root, 'active');
        finish(root, turnId, text, reply);
      }
      return;
    }
    try {
      const p = message.params || {}; let root = roots.get(p.threadId); let result = {};
      switch (message.method) {
        case 'initialize': break;
        case 'thread/start': case 'thread/fork': {
          const id = randomUUID(); const cwd = p.cwd || process.cwd();
          root = { id, cwd, path: path.join(cwd, '.attn-shared-mock', `${id}.jsonl`), config: p.config || {}, name: '', archived: false, turns: [], status: { type: 'idle' } };
          fs.mkdirSync(path.dirname(root.path), { recursive: true });
          fs.writeFileSync(root.path, conversationHeaderRecords({ agent: 'codex', id, cwd, launch: { argv: [] } }).join('\n') + '\n');
          roots.set(id, root); hook(root, 'SessionStart', { source: 'startup' }); result = { thread: metadata(root) }; break;
        }
        case 'thread/resume': {
          if (!root) throw new Error(`unknown root ${p.threadId}`);
          if (readMockAgentConfig(root.cwd).rejectBlankResume && root.turns.length === 0) throw new Error(`no rollout found for thread id ${root.id}`);
          root.config = p.config || root.config; hook(root, 'SessionStart', { source: 'resume' }); result = { thread: metadata(root) }; break;
        }
        case 'thread/name/set':
          if (p.name === 'fixture rejected name') throw new Error('fixture name write rejected');
          root.name = p.name; broadcast('thread/name/updated', { threadId: root.id, threadName: root.name }); break;
        case 'thread/read': result = { thread: { ...metadata(root), turns: root.turns } }; break;
        case 'thread/archive': root.archived = true; broadcast('thread/archived', { threadId: root.id }); break;
        case 'thread/unarchive': root.archived = false; break;
        case 'thread/loaded/list': result = { data: [...roots.values()].filter(r => !r.archived).map(r => r.id) }; break;
        case 'turn/start': case 'turn/steer': {
          if (!root.name) { root.name = 'Generated ' + path.basename(root.cwd); broadcast('thread/name/updated', { threadId: root.id, threadName: root.name }); }
          const text = p.input.map(item => item.text || '').join('\n'); const turnId = randomUUID();
          hook(root, 'UserPromptSubmit', { prompt: text }); broadcast('turn/started', { threadId: root.id, turn: { id: turnId } }); status(root, 'active');
          const fixture = readMockAgentConfig(root.cwd);
          const actions = selectMockAgentActions(fixture, text).filter(action => action.type === 'reply');
          const reply = actions.map(action => `${action.text} <!-- attn:state=${action.state || 'waiting_input'} -->`).join('\n');
          if (actions.some(action => action.approval)) {
            root.pending = { turnId, text, reply }; status(root, 'active', ['waitingOnApproval']);
            for (const other of peers.clients) if (other.readyState === WebSocket.OPEN) other.send(JSON.stringify(approval(root)));
          } else finish(root, turnId, text, reply);
          result = { turn: { id: turnId } }; break;
        }
        default: throw new Error(`shared mock does not implement ${message.method}`);
      }
      peer.send(JSON.stringify({ id: message.id, result }));
      if (message.method === 'thread/resume' && root.pending) peer.send(JSON.stringify(approval(root)));
    } catch (error) { peer.send(JSON.stringify({ id: message.id, error: { code: -32603, message: error.message } })); }
  }));
  server.listen(arg('--listen').replace('unix://', ''));
}

export async function runSharedMockView() {
  const socket = arg('--remote').replace('unix://', '');
  const peer = new WebSocket(`ws+unix://${socket}:/`);
  await new Promise((resolve, reject) => { peer.once('open', resolve); peer.once('error', reject); });
  let seq = 0; const pending = new Map(); const approvals = new Map(); let selected = ''; let draft = '';
  const title = () => process.stdout.write(`\x1b]0;${selected}\x07`);
  const prompt = () => process.stdout.write('\r\x1b[2K' + (approvals.has(selected) ? 'Allow the command to run? Enter to approve' : '› ' + draft));
  peer.on('message', raw => {
    const m = JSON.parse(raw);
    if (m.method === 'item/commandExecution/requestApproval') { approvals.set(m.params.threadId, m.id); if (m.params.threadId === selected) prompt(); return; }
    if (m.method === 'serverRequest/resolved') { approvals.delete(m.params.threadId); if (m.params.threadId === selected) prompt(); }
    if (m.id !== undefined) { const next = pending.get(m.id); pending.delete(m.id); if (next) { if (m.error) next.reject(new Error(m.error.message)); else next.resolve(m.result); } }
    if (m.method === 'attn-fixture/reply' && m.params.threadId === selected) { process.stdout.write('\r\x1b[2K' + m.params.text + '\r\n'); prompt(); }
  });
  const call = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pending.set(id, { resolve, reject }); peer.send(JSON.stringify({ id, method, params })); });
  await call('initialize', { clientInfo: { name: 'mock-tui', version: '1' } }); peer.send(JSON.stringify({ method: 'initialized' }));
  const resume = process.argv.indexOf('resume');
  const first = resume >= 0 ? await call('thread/resume', { threadId: process.argv[resume + 1] }) : await call('thread/start', { cwd: process.cwd() });
  selected = first.thread.id;
  process.stdout.write(mockAgentSplash({ header: 'OpenAI Codex shared mock', cwd: process.cwd(), cols: process.stdout.columns }).join('\r\n') + '\r\n'); title(); process.stdout.write(`Root ${selected}\r\n`); prompt();
  let turns = Promise.resolve();
  const take = (input) => {
    draft = ''; turns = turns.then(async () => {
      process.stdout.write('\r\n');
      if (input.startsWith('/agents ') || input.startsWith('/cached ')) {
        selected = input.split(' ')[1]; if (input.startsWith('/agents ')) await call('thread/resume', { threadId: selected }); title();
      } else if (input === '/new' || input === '/fork') {
        const result = await call(input === '/new' ? 'thread/start' : 'thread/fork', { threadId: selected, cwd: process.cwd() }); selected = result.thread.id; title();
      } else if (input.startsWith('/rename ')) {
        await call('thread/name/set', { threadId: selected, name: input.slice(8) });
      } else if (input === '/agents') {
        const loaded = await call('thread/loaded/list', {});
        for (const id of loaded.data) { const { thread } = await call('thread/read', { threadId: id }); process.stdout.write(`Agent ${id} ${thread.name || '(unnamed)'}\r\n`); }
      } else if (input.startsWith('/title ')) process.stdout.write(`\x1b]0;${input.slice(7)}\x07`);
      else await call('turn/start', { threadId: selected, input: [{ type: 'text', text: input }] });
      prompt();
    }).catch(error => { console.error(error); process.exitCode = 1; });
  };
  process.stdin.setRawMode?.(true); process.stdin.setEncoding('utf8');
  process.stdin.on('data', chunk => {
    const text = chunk.replace(/\x1b\[[0-9;?]*[a-zA-Z~]/g, '').replace(/\x1b\][^\x07]*\x07/g, '');
    for (const char of text) {
      if (char === '\r' || char === '\n') {
        if (approvals.has(selected)) peer.send(JSON.stringify({ id: approvals.get(selected), result: { decision: 'accept' } }));
        else { const input = draft; draft = ''; if (input) take(input); }
      }
      else if (char === '\x7f') draft = draft.slice(0, -1);
      else if (char === '\x15') draft = '';
      else if (char >= ' ') draft += char;
    }
    prompt();
  }); process.stdin.resume();
}
