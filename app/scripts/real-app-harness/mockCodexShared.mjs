import { execFile } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import WebSocket, { WebSocketServer } from 'ws';
import {
  agentHomeRoots,
  codexTranscriptPath,
  conversationHeaderRecords,
  findMockTranscript,
  markerStateForActions,
  messageRecords,
  mockAgentFlavor,
  mockAgentSplash,
  mockAgentTitle,
  parseMockAgentArgv,
  readMockAgentConfig,
  selectMockAgentTurn,
  stateMarker,
  transcriptMessages,
} from './mockAgent.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const socketPath = (value) => String(value || '').replace(/^unix:\/\//, '');
const waitingOnApproval = { type: 'active', activeFlags: ['waitingOnApproval'] };

function valueOf(args, ...names) {
  const index = args.findIndex((arg) => names.includes(arg));
  return index < 0 ? '' : args[index + 1] || '';
}

function hookCommands(args) {
  const commands = {};
  args.forEach((arg, index) => {
    if (args[index - 1] !== '-c') return;
    const event = /^hooks\.(\w+)=/.exec(arg)?.[1];
    if (!event) return;
    commands[event] = [...arg.matchAll(/command = ("(?:[^"\\]|\\.)*")/g)].map((match) => JSON.parse(match[1]));
  });
  return commands;
}

function runShell(command, cwd, env, input) {
  return new Promise((resolve, reject) => {
    const child = execFile('/bin/sh', ['-c', command], { cwd, env }, (error, stdout, stderr) => {
      if (error) reject(new Error(`${command}: ${error.message} ${stderr || stdout}`.trim()));
      else resolve();
    });
    child.stdin.end(input);
  });
}

export async function runSharedMockServer() {
  const args = process.argv.slice(3);
  const hooks = hookCommands(args);
  const sessionsDir = agentHomeRoots().codexSessions;
  const codexHome = path.dirname(sessionsDir);
  const archivedDir = path.join(codexHome, 'archived_sessions');
  const threads = new Map();
  const conns = new Set();
  let nextRequest = 0;

  const send = (conn, frame) => { if (conn.readyState === WebSocket.OPEN) conn.send(JSON.stringify(frame)); };
  const broadcast = (method, params) => conns.forEach((conn) => send(conn, { method, params }));
  const tell = (thread, frame) => thread.subscribers.forEach((conn) => send(conn, frame));
  const statusOf = (thread) => (thread.approval ? waitingOnApproval : thread.active ? { type: 'active', activeFlags: [] } : { type: 'idle' });
  const metadata = (thread) => ({ id: thread.id, cwd: thread.cwd, path: thread.path, ephemeral: false, source: 'cli', status: statusOf(thread) });
  const announceStatus = (thread) => broadcast('thread/status/changed', { threadId: thread.id, status: statusOf(thread) });

  const runHooks = async (thread, event, extra = {}) => {
    const env = { ...process.env };
    for (const [key, value] of Object.entries(thread.config)) {
      const name = /^shell_environment_policy\.set\.(.+)$/.exec(key)?.[1];
      if (name) env[name] = String(value);
    }
    const input = JSON.stringify({
      session_id: thread.id, transcript_path: thread.path, cwd: thread.cwd, hook_event_name: event,
      model: 'mock-agent-1', permission_mode: 'default', ...(thread.turnId ? { turn_id: thread.turnId } : {}), ...extra,
    });
    const cwd = fs.existsSync(thread.cwd) ? thread.cwd : process.cwd();
    for (const command of hooks[event] || []) await runShell(command, cwd, env, input);
  };

  const write = (thread, records) => {
    fs.mkdirSync(path.dirname(thread.path), { recursive: true });
    const lines = thread.written ? [] : conversationHeaderRecords({ agent: 'codex', id: thread.id, cwd: thread.cwd, launch: { argv: [] } });
    thread.written = true;
    fs.appendFileSync(thread.path, `${[...lines, ...records].join('\n')}\n`, 'utf8');
  };

  const unload = (id) => {
    const thread = threads.get(id);
    if (!thread || thread.subscribers.size > 0 || thread.active) return;
    threads.delete(id);
    broadcast('thread/status/changed', { threadId: id, status: { type: 'notLoaded' } });
    broadcast('thread/closed', { threadId: id });
  };

  const findRollout = (id) => {
    const found = findMockTranscript({ agent: 'codex', cwd: '', id });
    if (!found) return null;
    let cwd = process.cwd();
    try { cwd = JSON.parse(fs.readFileSync(found, 'utf8').split('\n', 1)[0]).payload.cwd || cwd; } catch { /* a header without cwd */ }
    return { file: found, cwd };
  };

  const loadedThread = (id) => {
    const thread = threads.get(id);
    if (!thread) throw new Error(`thread not loaded: ${id}`);
    return thread;
  };

  const newThread = ({ id, cwd, file, source, config, conn }) => ({
    id, cwd, path: file, config, sessionSource: source, sessionStarted: false, written: false, active: false,
    turnId: '', sequence: 0, approval: null, subscribers: new Set([conn]),
  });

  const speak = (thread, role, text) => {
    thread.sequence += 1;
    write(thread, messageRecords({ agent: 'codex', role, text, sequence: thread.sequence }));
  };

  const askApproval = async (thread) => {
    await runHooks(thread, 'PermissionRequest', { tool_name: 'Bash' });
    nextRequest += 1;
    const id = `approval-${nextRequest}`;
    const request = { id, method: 'item/commandExecution/requestApproval', params: { threadId: thread.id, turnId: thread.turnId, itemId: 'command-1', command: 'make migrate', reason: 'run the migration' } };
    const answered = new Promise((resolve) => { thread.approval = { id, request, resolve }; });
    tell(thread, request);
    announceStatus(thread);
    await answered;
  };

  const playTurn = async (thread, text) => {
    const config = readMockAgentConfig(thread.cwd);
    const turn = selectMockAgentTurn(config, text);
    const started = Date.now();
    const replies = [];
    for (const action of turn.actions) {
      if (action.type === 'delay') await delay(Number(action.ms) || 0);
      else if (action.type === 'reply') {
        replies.push(String(action.text ?? ''));
        if (action.approval) await askApproval(thread);
      } else throw new Error(`the shared Codex mock does not play ${action.type} actions`);
    }
    await delay(Math.max(0, (config.minimumWorkingMs ?? 1_500) - (Date.now() - started)));
    const state = markerStateForActions(turn.actions);
    const reply = replies.join('\n');
    speak(thread, 'assistant', `${reply} ${stateMarker(state)}`.trim());
    await runHooks(thread, 'Stop', { stop_hook_active: false, last_assistant_message: `${reply} ${stateMarker(state)}`.trim() });
    thread.active = false;
    tell(thread, { method: 'item/completed', params: { threadId: thread.id, turnId: thread.turnId, item: { type: 'agentMessage', text: reply } } });
    tell(thread, { method: 'turn/completed', params: { threadId: thread.id, turn: { id: thread.turnId, status: 'completed' } } });
    announceStatus(thread);
  };

  const promptOf = (params) => (params.input || []).map((item) => item.text || '').join('\n');

  const handlers = {
    initialize: () => ({ result: { userAgent: 'codex-mock', codexHome, platformFamily: 'unix', platformOs: process.platform } }),
    'thread/start': (conn, params) => {
      const id = `mock-${randomUUID()}`;
      const started = new Date();
      const thread = newThread({ id, cwd: params.cwd || process.cwd(), file: codexTranscriptPath(id, started), source: 'startup', config: params.config || {}, conn });
      if (params.ephemeral) return { result: { thread: { id, ephemeral: true } } };
      threads.set(id, thread);
      return { result: { thread: metadata(thread) }, after: () => broadcast('thread/started', { thread: metadata(thread) }) };
    },
    'thread/resume': (conn, params) => {
      let thread = threads.get(params.threadId);
      if (thread && !thread.written) throw new Error(`no rollout found for thread id ${params.threadId}`);
      if (!thread) {
        const found = findRollout(params.threadId);
        if (!found) throw new Error(`no rollout found for thread id ${params.threadId}`);
        thread = newThread({ id: params.threadId, cwd: found.cwd, file: found.file, source: 'resume', config: params.config || {}, conn });
        thread.written = true;
        thread.sequence = transcriptMessages(fs.readFileSync(found.file, 'utf8')).length;
        threads.set(thread.id, thread);
      }
      thread.subscribers.add(conn);
      return {
        result: { thread: metadata(thread) },
        after: () => {
          announceStatus(thread);
          if (thread.approval) send(conn, thread.approval.request);
        },
      };
    },
    'thread/unsubscribe': (conn, params) => {
      threads.get(params.threadId)?.subscribers.delete(conn);
      return { result: { status: 'unsubscribed' }, after: () => unload(params.threadId) };
    },
    'thread/inject_items': (_conn, params) => {
      const thread = loadedThread(params.threadId);
      if (!params.items?.length) throw new Error('items must not be empty');
      write(thread, params.items.map((item) => JSON.stringify({ timestamp: new Date().toISOString(), type: 'response_item', payload: item })));
      return { result: {} };
    },
    'thread/read': (_conn, params) => {
      const thread = threads.get(params.threadId);
      if (thread) return { result: { thread: metadata(thread) } };
      if (!findRollout(params.threadId)) throw new Error(`thread not loaded: ${params.threadId}`);
      return { result: { thread: { id: params.threadId, status: { type: 'notLoaded' } } } };
    },
    'thread/name/set': (_conn, params) => {
      if (!threads.has(params.threadId) && !findRollout(params.threadId)) throw new Error(`thread not loaded: ${params.threadId}`);
      return { result: {}, after: () => broadcast('thread/name/updated', { threadId: params.threadId, threadName: params.name }) };
    },
    'thread/archive': (_conn, params) => {
      const thread = threads.get(params.threadId);
      if (thread) write(thread, []);
      const found = findRollout(params.threadId);
      if (!found) throw new Error(`no rollout found for thread id ${params.threadId}`);
      fs.mkdirSync(archivedDir, { recursive: true });
      fs.renameSync(found.file, path.join(archivedDir, path.basename(found.file)));
      threads.delete(params.threadId);
      return {
        result: {},
        after: () => {
          broadcast('thread/status/changed', { threadId: params.threadId, status: { type: 'notLoaded' } });
          broadcast('thread/archived', { threadId: params.threadId });
        },
      };
    },
    'thread/unarchive': (_conn, params) => {
      const name = (fs.existsSync(archivedDir) ? fs.readdirSync(archivedDir) : []).find((entry) => entry.endsWith(`-${params.threadId}.jsonl`));
      if (!name) throw new Error(`no archived rollout found for thread id ${params.threadId}`);
      const [year, month, day] = /^rollout-(\d{4}-\d{2}-\d{2})T/.exec(name)[1].split('-');
      const dir = path.join(sessionsDir, year, month, day);
      fs.mkdirSync(dir, { recursive: true });
      fs.renameSync(path.join(archivedDir, name), path.join(dir, name));
      return { result: { thread: { id: params.threadId, status: { type: 'notLoaded' } } }, after: () => broadcast('thread/unarchived', { threadId: params.threadId }) };
    },
    'thread/turns/list': (_conn, params) => {
      const thread = loadedThread(params.threadId);
      const data = thread.turnId ? [{ id: thread.turnId, status: thread.active ? 'inProgress' : 'completed', items: [] }] : [];
      return { result: { data, nextCursor: null, backwardsCursor: null } };
    },
    'thread/loaded/list': () => ({ result: { data: [...threads.keys()] } }),
    'turn/start': async (_conn, params) => {
      const thread = loadedThread(params.threadId);
      if (thread.active) throw new Error(`turn already active on thread ${params.threadId}`);
      const text = promptOf(params);
      thread.active = true;
      thread.turnId = randomUUID();
      const first = !thread.sessionStarted;
      thread.sessionStarted = true;
      if (first) {
        write(thread, []);
        await runHooks(thread, 'SessionStart', { source: thread.sessionSource });
      }
      await runHooks(thread, 'UserPromptSubmit', { prompt: text });
      speak(thread, 'user', text);
      return {
        result: { turn: { id: thread.turnId, status: 'inProgress' } },
        after: () => {
          tell(thread, { method: 'turn/started', params: { threadId: thread.id, turn: { id: thread.turnId, status: 'inProgress' } } });
          announceStatus(thread);
          playTurn(thread, text).catch((error) => {
            console.error(`shared Codex mock turn failed: ${error.message}`);
            thread.active = false;
            announceStatus(thread);
          });
        },
      };
    },
    'turn/steer': (_conn, params) => {
      const thread = loadedThread(params.threadId);
      if (!thread.active) throw new Error('no active turn to steer');
      if (params.expectedTurnId !== thread.turnId) throw new Error(`expected turn ${params.expectedTurnId}, but turn ${thread.turnId} is active`);
      speak(thread, 'user', promptOf(params));
      return { result: { turnId: thread.turnId } };
    },
  };

  const answered = (message) => {
    for (const thread of threads.values()) {
      if (thread.approval?.id !== message.id) continue;
      const { resolve } = thread.approval;
      thread.approval = null;
      tell(thread, { method: 'serverRequest/resolved', params: { threadId: thread.id, requestId: message.id } });
      announceStatus(thread);
      resolve();
    }
  };

  const server = http.createServer();
  const peers = new WebSocketServer({ server });
  peers.on('connection', (conn) => {
    conns.add(conn);
    conn.on('close', () => {
      conns.delete(conn);
      for (const [id, thread] of threads) {
        thread.subscribers.delete(conn);
        unload(id);
      }
    });
    conn.on('message', async (raw) => {
      let message;
      try { message = JSON.parse(String(raw)); } catch { return; }
      if (!message.method) {
        answered(message);
        return;
      }
      if (message.id === undefined) return;
      try {
        const handler = handlers[message.method];
        if (!handler) throw new Error(`the shared Codex mock does not serve ${message.method}`);
        const { result, after } = await handler(conn, message.params || {});
        send(conn, { id: message.id, result });
        after?.();
      } catch (error) {
        send(conn, { id: message.id, error: { code: -32600, message: error.message } });
      }
    });
  });
  const listening = socketPath(valueOf(args, '--listen'));
  server.on('error', (error) => {
    console.error(`Error: app-server control socket is already in use at ${listening}: ${error.message}`);
    process.exit(1);
  });
  for (const signal of ['SIGHUP', 'SIGTERM', 'SIGINT']) process.on(signal, () => process.exit(0));
  server.listen(listening);
}

function remoteLaunch(argv) {
  const launch = parseMockAgentArgv(argv);
  return {
    ...launch,
    socket: socketPath(valueOf(argv, '--remote')),
    cwd: valueOf(argv, '-C', '--cd') || process.cwd(),
    model: valueOf(argv, '--model', '-m'),
  };
}

export async function runSharedMockView() {
  const launch = remoteLaunch(process.argv.slice(2));
  const flavor = mockAgentFlavor('codex');
  const config = readMockAgentConfig(launch.cwd);
  const peer = new WebSocket(`ws+unix://${launch.socket}:/`);
  await new Promise((resolve, reject) => { peer.once('open', resolve); peer.once('error', reject); });

  let sequence = 0;
  let selected = '';
  let draft = '';
  const pending = new Map();
  const approvals = new Map();
  const answering = new Set();
  const busy = new Set();
  const blocks = [];
  let beat = null;

  const needsAnswer = () => approvals.has(selected) && !answering.has(approvals.get(selected));
  const setTitle = () => {
    const title = needsAnswer()
      ? `[ . ] Action Required | ${config.name || 'mock agent'}`
      : mockAgentTitle(config.name, busy.has(selected) ? 'working' : 'ready', flavor.resting);
    process.stdout.write(`\u001b]0;${title}\u0007`);
  };
  const pace = () => {
    setTitle();
    if (busy.has(selected) && !beat) beat = setInterval(setTitle, 500);
    else if (!busy.has(selected) && beat) { clearInterval(beat); beat = null; }
  };
  const showPrompt = () => {
    pace();
    process.stdout.write(`\n${flavor.prompt}`);
  };
  const emit = (block) => {
    blocks.push(block);
    process.stdout.write(`\n${block}\n`);
  };
  const approvalBlock = () => ['Allow the command to run?', '› 1. Yes, proceed', '  2. No, and tell Codex what to do differently', 'Press enter to confirm or esc to cancel'].join('\n');
  const showApproval = () => {
    emit(approvalBlock());
    pace();
  };
  const repaint = () => {
    const splash = mockAgentSplash({ header: config.banner || flavor.header, cwd: launch.cwd, cols: process.stdout.columns }).join('\n');
    process.stdout.write(`\u001b[2J\u001b[H${[splash, ...blocks].join('\n\n')}\n`);
    showPrompt();
  };

  const call = (method, params) => new Promise((resolve, reject) => {
    sequence += 1;
    pending.set(sequence, { resolve, reject });
    peer.send(JSON.stringify({ id: sequence, method, params }));
  });

  peer.on('message', (raw) => {
    const message = JSON.parse(String(raw));
    const params = message.params || {};
    if (message.method === 'item/commandExecution/requestApproval') {
      approvals.set(params.threadId, message.id);
      if (params.threadId === selected && needsAnswer()) showApproval();
    } else if (message.method === 'serverRequest/resolved') {
      const had = approvals.get(params.threadId);
      approvals.delete(params.threadId);
      answering.delete(had);
      if (params.threadId === selected && had) showPrompt();
    } else if (message.method === 'turn/started') {
      busy.add(params.threadId);
      if (params.threadId === selected) pace();
    } else if (message.method === 'turn/completed') {
      busy.delete(params.threadId);
      if (params.threadId === selected) pace();
    } else if (message.method === 'item/completed' && params.threadId === selected && params.item?.type === 'agentMessage') {
      emit(`• ${params.item.text.replaceAll('\n', '\n  ')}`);
      showPrompt();
    } else if (message.id !== undefined && !message.method) {
      const waiting = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) waiting?.reject(new Error(message.error.message));
      else waiting?.resolve(message.result);
    }
  });

  await call('initialize', { clientInfo: { name: 'codex-tui', version: '1' } });
  peer.send(JSON.stringify({ method: 'initialized' }));
  let shown;
  try {
    shown = launch.resumeSessionId
      ? await call('thread/resume', { threadId: launch.resumeSessionId, excludeTurns: true })
      : await call('thread/start', { cwd: launch.cwd, model: launch.model, ephemeral: false, threadSource: 'user', config: {} });
  } catch (error) {
    console.error(error.message);
    peer.close();
    process.exitCode = 1;
    return;
  }
  selected = shown.thread.id;
  blocks.push(`Conversation ${selected}`);
  repaint();
  if (needsAnswer()) showApproval();
  process.stdout.on('resize', repaint);

  const switchTo = async (conversation) => {
    const previous = selected;
    selected = conversation;
    blocks.length = 0;
    blocks.push(`Conversation ${selected}`);
    await call('thread/unsubscribe', { threadId: previous });
    repaint();
    if (needsAnswer()) showApproval();
  };

  const submit = async (input) => {
    const [command, argument = ''] = [input.split(' ')[0], input.split(' ').slice(1).join(' ').trim()];
    if (command === '/new' || command === '/clear') {
      const started = await call('thread/start', { cwd: launch.cwd, model: launch.model, ephemeral: false, threadSource: 'user', config: {} });
      await switchTo(started.thread.id);
    } else if ((command === '/resume' || command === '/agents') && argument) {
      await call('thread/resume', { threadId: argument, excludeTurns: true });
      await switchTo(argument);
    } else if (command === '/rename' && argument) {
      await call('thread/name/set', { threadId: selected, name: argument });
      showPrompt();
    } else {
      blocks.push(`${flavor.prompt}${input}`);
      busy.add(selected);
      pace();
      await call('turn/start', { threadId: selected, input: [{ type: 'text', text: input, text_elements: [] }] });
    }
  };

  const answerApproval = () => {
    const id = approvals.get(selected);
    answering.add(id);
    peer.send(JSON.stringify({ id, result: { decision: 'accept' } }));
    busy.add(selected);
    pace();
  };

  let turns = Promise.resolve();
  const enter = () => {
    if (needsAnswer()) {
      draft = '';
      answerApproval();
      return;
    }
    const input = draft.trim();
    draft = '';
    if (input) {
      turns = turns.then(() => submit(input)).catch((error) => {
        busy.delete(selected);
        emit(`• mock agent error: ${error.message}`);
        showPrompt();
      });
    }
  };

  let inPaste = false;
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', (chunk) => {
    const text = chunk.replace(/\u001b\[200~/g, () => { inPaste = true; return ''; }).replace(/\u001b\[201~/g, () => { inPaste = false; return ''; });
    for (const char of text) {
      if (char === '\r' || char === '\n') {
        if (inPaste) draft += '\n';
        else enter();
      } else if (char === '\u007f') draft = draft.slice(0, -1);
      else if (char === '\u0015') draft = '';
      else if (char >= ' ') draft += char;
    }
  });
  process.stdin.resume();
  if (launch.initialPrompt.trim()) {
    draft = launch.initialPrompt;
    enter();
  }
}
