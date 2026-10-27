import { spawn } from 'node:child_process';
import { EventEmitter, once } from 'node:events';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, readdirSync, copyFileSync, existsSync, realpathSync, chmodSync, watch } from 'node:fs';
import { join, resolve, relative } from 'node:path';
import { homedir, tmpdir } from 'node:os';
import { randomUUID } from 'node:crypto';

export const json = path => JSON.parse(readFileSync(path, 'utf8'));
export const save = (path, value) => writeFileSync(path, JSON.stringify(value, null, 2) + '\n');
export const failure = (kind, message) => Object.assign(new Error(message), { failure_class: kind });
const quote = value => "'" + String(value).replaceAll("'", "'\\''") + "'";
const matchesInput = (text, prompt) => {
  if (text.trim() === prompt.trim()) return true;
  const paste = text.trim().match(/^<pasted_content id="([0-9a-f]{4})">\n([\s\S]*)\n<\/pasted_content id="\1">$/);
  return paste?.[2].trim() === prompt.trim();
};

export function environment() {
  return Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    !/^(ATTN_|CLAUDE_CONFIG_DIR$|CLAUDECODE$|CODEX_HOME$|CODEX_THREAD_ID$)/.test(key)));
}

export async function command(binary, args, options = {}) {
  const child = spawn(binary, args, { env: environment(), ...options, stdio: [options.input === undefined ? 'ignore' : 'pipe', 'pipe', 'pipe'] });
  const closed = new Promise(resolve => child.once('close', resolve));
  let stdout = '', stderr = '';
  child.stdout.on('data', data => { stdout += data; });
  child.stderr.on('data', data => { stderr += data; });
  child.stdin?.end(options.input);
  let code, signal;
  try { [code, signal] = await once(child, 'exit'); }
  catch (error) {
    await closed;
    throw options.signal?.aborted ? options.signal.reason : error;
  }
  if (code !== 0) throw failure('harness', `${binary} exited ${code ?? signal}: ${stderr || stdout}`);
  return stdout;
}

export function copyTree(source, destination) {
  mkdirSync(destination, { recursive: true });
  for (const entry of readdirSync(source, { withFileTypes: true })) {
    if (entry.isSymbolicLink()) throw failure('fixture', `fixture contains a symlink: ${join(source, entry.name)}`);
    const to = join(destination, entry.name), from = join(source, entry.name);
    if (entry.isDirectory()) copyTree(from, to);
    else if (entry.isFile()) copyFileSync(from, to);
  }
}

export function sandboxPolicy(hiddenRoots, runtimeBase, currentRoot) {
  return '(version 1)\n(allow default)\n' + hiddenRoots.map(path =>
    `(deny file-read* file-write* (subpath ${JSON.stringify(resolve(path))}))\n`).join('') +
    hiddenRoots.map(path => `(deny network-outbound (remote unix-socket (path-literal ${JSON.stringify(join(resolve(path), 'attn.sock'))})))\n`).join('') +
    `(deny file-read* file-write* (require-all (subpath ${JSON.stringify(runtimeBase)}) (require-not (subpath ${JSON.stringify(currentRoot)}))))\n`;
}

export class Trial extends EventEmitter {
  constructor(config, signal) {
    super();
    this.config = config;
    this.handoffAbort = new AbortController();
    this.signal = AbortSignal.any([signal, this.handoffAbort.signal]);
    this.handoffs = [];
    this.sleepConfirmations = [];
    this.on('changed', () => this.observeExchange());
    this.runtimeBase = join(tmpdir(), 'attn-crew-memory-eval');
    mkdirSync(this.runtimeBase, { recursive: true });
    this.runtimeBase = realpathSync(this.runtimeBase);
    this.root = mkdtempSync(join(this.runtimeBase, 'trial-'));
    this.attnBinary = join(this.root, 'attn');
    copyFileSync(config.attn_binary, this.attnBinary);
    chmodSync(this.attnBinary, 0o500);
    this.instance = 'cm-' + randomUUID().replaceAll('-', '').slice(0, 12);
    this.data = join(this.root, 'instance');
    this.home = join(this.data, 'crew', 'member');
    this.sessions = new Map();
    this.bindings = new Map();
    this.frames = [];
    this.env = { ...environment(), ATTN_INSTANCE: this.instance, ATTN_TOOL_HOME: join(this.root, 'tools'),
      ATTN_PTY_BACKEND: 'worker', ATTN_HEADLESS_TASKS: 'off',
      ATTN_GITHUB_POLLING: 'off', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION: 'false',
      CLAUDE_CONFIG_DIR: join(this.root, 'tools', '.claude') };
    this.env.ATTN_WRAPPER_PATH = this.attnBinary;
    this.env.CODEX_HOME = join(this.env.ATTN_TOOL_HOME, '.codex');
  }

  async start(input) {
    if (process.platform !== 'darwin') throw failure('harness', 'this adapter requires macOS sandbox-exec; Linux runs need a VM adapter');
    const resources = JSON.parse(await this.cli(['instance', 'resolve', '--instance', this.instance]));
    this.data = resources.dataDir;
    this.home = join(this.data, 'crew', 'member');
    this.env.ATTN_DATA_DIR = this.data;
    mkdirSync(this.home, { recursive: true });
    mkdirSync(this.env.CLAUDE_CONFIG_DIR, { recursive: true });
    save(join(this.env.CLAUDE_CONFIG_DIR, '.claude.json'), {
      hasCompletedOnboarding: true, theme: 'dark',
      projects: { [realpathSync(this.home)]: { hasTrustDialogAccepted: true } },
    });
    writeFileSync(join(this.home, 'CHARTER.md'), input.day_brief ? input.day_brief + '\n\n' + input.charter : input.charter);
    mkdirSync(join(this.home, 'handoffs'));
    for (const letter of input.letters) writeFileSync(join(this.home, 'handoffs', letter.name), letter.text);
    for (const [path, text] of Object.entries(input.files ?? {})) this.writeInput(path, text);
    const priorInstances = readdirSync(homedir()).filter(name => name.startsWith('.attn-cm-'))
      .map(name => join(homedir(), name)).filter(path => path !== this.data);
    const roots = [this.config.private_root, ...priorInstances, join(homedir(), '.attn'), join(homedir(), 'exo'),
      join(homedir(), '.claude', 'projects'), join(homedir(), '.codex', 'sessions'),
      join(homedir(), '.codex', 'archived_sessions'), ...(this.config.hidden_roots ?? [])];
    this.policy = join(this.root, 'trial.sb');
    let policy = sandboxPolicy(roots, this.runtimeBase, this.root);
    if (this.config.local_provider_only) policy = policy.replace('(allow default)\n',
      '(allow default)\n(deny network-outbound)\n(allow network-outbound (remote ip "localhost:*"))\n(allow network-outbound (remote unix-socket))\n');
    writeFileSync(this.policy, policy);
    const wrapper = join(this.root, 'claude');
    const exports = Object.entries(this.env).filter(([key]) => /^(ATTN_|CLAUDE_CODE_DISABLE_AUTO_MEMORY|CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION|CLAUDE_CONFIG_DIR|CODEX_HOME)/.test(key));
    writeFileSync(wrapper, '#!/bin/sh\nunset ATTN_DB_PATH ATTN_SOCKET_PATH ATTN_CONFIG_PATH ATTN_PLUGIN_DIR ATTN_CLIENT_TOKEN ATTN_PROFILE_ID\n' + exports.map(([key, value]) => `export ${key}=${quote(value)}\n`).join('') +
      `exec ${quote(this.config.claude_binary)} --name 'Member' --strict-mcp-config --mcp-config '{"mcpServers":{}}' --setting-sources '' "$@"\n`, { mode: 0o700 });
    this.env.ATTN_CLAUDE_EXECUTABLE = wrapper;
    this.nativeWatch = watch(this.env.CLAUDE_CONFIG_DIR, { recursive: true, signal: this.signal }, (_, filename) => {
      if (filename?.endsWith('.jsonl')) this.emit('changed');
    });
    await this.probeIsolation();
    this.child = spawn('/usr/bin/sandbox-exec', ['-f', this.policy, this.attnBinary, 'daemon'],
      { cwd: this.root, env: { ...this.env, ATTN_DAEMON_READY_FD: '3' }, stdio: ['ignore', 'pipe', 'pipe', 'pipe'] });
    this.daemonOutput = '';
    this.child.stdout.on('data', data => { this.daemonOutput += data; });
    this.child.stderr.on('data', data => { this.daemonOutput += data; });
    this.exited = once(this.child, 'exit');
    this.child.on('exit', () => this.emit('changed'));
    const ready = await Promise.race([
      once(this.child.stdio[3], 'data', { signal: this.signal }).then(([data]) => String(data).trim()),
      this.exited.then(() => { throw failure('harness', `daemon exited before ready: ${this.daemonOutput}`); }),
    ]);
    if (ready !== 'ready') throw failure('harness', `daemon readiness: ${ready}`);
    const listeners = await command('/usr/sbin/lsof', ['-nP', '-a', '-p', String(this.child.pid), '-iTCP', '-sTCP:LISTEN', '-Fn'], { signal: this.signal });
    const port = listeners.match(/^n(?:127\.0\.0\.1|\*):([0-9]+)$/m)?.[1];
    if (!port) throw failure('harness', `cannot find captured daemon PID's listener: ${listeners}`);
    this.ws = new WebSocket(`ws://127.0.0.1:${port}/ws`);
    this.ws.addEventListener('message', event => {
      const frame = JSON.parse(event.data);
      this.frames.push(frame);
      for (const session of frame.sessions ?? (frame.session ? [frame.session] : [])) this.sessions.set(session.id, session);
      if (frame.terminal_bindings) {
        this.bindings.clear();
        for (const binding of frame.terminal_bindings) this.bindings.set(binding.session_id, binding.terminal_id);
      }
      this.emit('changed');
    });
    this.ws.addEventListener('error', () => this.emit('changed'));
    await once(this.ws, 'open', { signal: this.signal });
    this.ws.send(JSON.stringify({ cmd: 'client_hello', client_kind: 'crew-memory-eval', version: '1',
      capabilities: [], client_token: readFileSync(join(this.data, 'client-token'), 'utf8').trim() }));
    await this.until(() => this.frames.some(frame => frame.event === 'initial_state'));
    await this.cli(['crew', 'set', 'member', '--agent', 'claude', '--model', this.config.member_model,
      '--effort', this.config.member_effort]);
    const roster = JSON.parse(await this.cli(['crew', 'list', '--json']));
    const member = roster.find(row => row.key === 'member');
    if (member?.resolved_model !== this.config.member_model || member?.resolved_effort !== this.config.member_effort)
      throw failure('fixture', `crew settings differ: requested ${this.config.member_model}/${this.config.member_effort}, resolved ${member?.resolved_model}/${member?.resolved_effort}`);
    this.home = member.home_dir;
    this.selection = { model: member.resolved_model, effort: member.resolved_effort };
    if (this.config.memory) {
      await this.cli(['settings', 'set', 'crew_memory_enabled', 'true']);
      if (input.frozen_memory) copyTree(input.frozen_memory, join(this.home, 'memory'));
    }
  }

  writeInput(path, text) {
    const target = resolve(this.home, path);
    if (relative(this.home, target).startsWith('..')) throw failure('fixture', `input leaves member home: ${path}`);
    mkdirSync(resolve(target, '..'), { recursive: true });
    writeFileSync(target, text);
  }

  async probeIsolation() {
    const canary = join(this.config.private_root, 'isolation-canary');
    writeFileSync(canary, 'hidden-eval-canary');
    const siblingCanary = join(this.runtimeBase, 'other-trial-canary');
    writeFileSync(siblingCanary, 'other-trial-canary');
    const script = 'test -r "$1" && exit 1; /bin/sh -c \'test ! -r "$1"\' probe "$1"; test -w "$2"';
    await command('/usr/bin/sandbox-exec', ['-f', this.policy, '/bin/sh', '-c', script, 'probe', canary, this.home], { cwd: this.root, signal: this.signal });
    await command('/usr/bin/sandbox-exec', ['-f', this.policy, '/bin/sh', '-c', script, 'probe', siblingCanary, this.home], { cwd: this.root, signal: this.signal });
    const socketProbe = "import net from 'node:net';const socket=net.createConnection(process.argv[1]);socket.on('connect',()=>{socket.end();process.exitCode=1;});socket.on('error',error=>{process.exitCode=['EPERM','EACCES'].includes(error.code)?0:2;});";
    await command('/usr/bin/sandbox-exec', ['-f', this.policy, process.execPath, '--input-type=module', '-e', socketProbe, join(homedir(), '.attn', 'attn.sock')], { cwd: this.root, signal: this.signal });
    this.isolation = { private_canary_unreadable: true, child_canary_unreadable: true, other_trial_unreadable: true,
      member_home_writable: true, production_socket_denied: true };
  }

  async until(predicate) {
    while (true) {
      this.signal.throwIfAborted();
      if (predicate()) return;
      if (this.child?.exitCode !== null && this.child?.exitCode !== undefined) throw failure('harness', `daemon exited: ${this.daemonOutput}`);
      try { await once(this, 'changed', { signal: this.signal }); }
      catch (error) { throw this.signal.aborted ? this.signal.reason : error; }
    }
  }

  cli(args) { return command(this.attnBinary, args, { env: this.env, signal: this.signal }); }

  async wake() {
    const launched = JSON.parse(await this.cli(['crew', 'wake', 'member', '--json']));
    const id = launched.session_id;
    this.beginExchange(id, null);
    await this.waiting(id);
    return id;
  }

  nativeRecords(id, terminal = this.bindings.get(id)) {
    const projects = join(this.env.CLAUDE_CONFIG_DIR, 'projects');
    if (!terminal || !existsSync(projects)) return [];
    for (const entry of readdirSync(projects, { withFileTypes: true })) {
      const file = join(projects, entry.name, terminal + '.jsonl');
      if (!entry.isDirectory() || !existsSync(file)) continue;
      const text = readFileSync(file, 'utf8');
      return text.slice(0, text.lastIndexOf('\n') + 1).split('\n').filter(Boolean).map(line => JSON.parse(line));
    }
    return [];
  }

  beginExchange(id, prompt, { cursor = prompt === null ? 0 : this.nativeRecords(id).length, closing = false } = {}) {
    this.exchange = { session: id, prompt, cursor, closing };
    this.observeExchange();
  }

  observeExchange() {
    const exchange = this.exchange;
    if (!exchange || this.signal.aborted || exchange.simulating || this.handoffPhase?.phase === 'simulator_dispatch') return;
    try {
      const session = this.sessions.get(exchange.session);
      if (exchange.closing && this.unregistered(exchange.session)) { this.endHandoff(); return; }
      if (session?.state === 'working') { this.endHandoff(); return; }
      if (!['idle', 'waiting_input'].includes(session?.state)) return;
      const records = this.nativeRecords(exchange.session);
      const after = exchange.cursor;
      const input = records.findIndex((record, index) => index >= after && record.type === 'user' &&
        (exchange.prompt === null || (typeof record.message?.content === 'string'
          ? matchesInput(record.message.content, exchange.prompt)
          : record.message?.content?.some(part => part.type === 'text' && matchesInput(part.text, exchange.prompt)))));
      const completion = input < 0 ? undefined : records.slice(input + 1).find(record => record.type === 'assistant' &&
        record.message?.id && !record.isApiErrorMessage && ['end_turn', 'stop_sequence'].includes(record.message.stop_reason));
      if (completion && !exchange.completion) {
        exchange.completion = { input_cursor: records[input].uuid, id: completion.message.id,
          stop_reason: completion.message.stop_reason, observed_at: performance.now() };
      }
      const phase = exchange.closing ? 'crew_sleep' : exchange.completion ? 'simulator_start'
        : input < 0 && exchange.prompt !== null ? 'native_input_acceptance' : 'normal_assistant_completion';
      if (this.handoffPhase?.phase !== phase) this.armHandoff(phase);
    } catch (error) {
      this.handoffAbort.abort(error.failure_class ? error : failure('native_transcript', `native transcript for ${exchange.session}: ${error.message}`));
    }
  }

  armHandoff(phase) {
    this.endHandoff();
    const limit = this.config.controller_handoff_timeout_ms;
    if (!Number.isFinite(limit) || limit <= 0) throw failure('fixture', `controller_handoff_timeout_ms needs a measured positive limit, got ${limit}`);
    this.handoffPhase = { phase, started_at: performance.now(), session: this.exchange.session };
    this.handoffTimer = setTimeout(() => {
      const elapsed = performance.now() - this.handoffPhase.started_at;
      const error = failure('controller_handoff', `controller_handoff_timeout_ms=${limit}; elapsed_ms=${elapsed.toFixed(3)}; session=${this.exchange.session}; exchange=${this.exchange.completion?.id ?? 'pending'}; awaiting=${phase}`);
      this.endHandoff();
      this.handoffAbort.abort(error);
      this.emit('changed');
    }, limit);
  }

  endHandoff() {
    clearTimeout(this.handoffTimer);
    if (this.handoffPhase) this.handoffs.push({ ...this.handoffPhase, elapsed_ms: performance.now() - this.handoffPhase.started_at });
    this.handoffPhase = null;
  }

  beginSimulation(id) {
    this.signal.throwIfAborted();
    if (this.exchange?.session !== id || !this.exchange.completion) throw failure('harness', `no completed exchange for simulator ${id}`);
    this.endHandoff();
    this.exchange.simulating = true;
  }

  finishSimulation(id) {
    this.signal.throwIfAborted();
    if (this.exchange?.session !== id || !this.exchange.simulating) throw failure('harness', `no active simulator for ${id}`);
    this.exchange.simulating = false;
    this.armHandoff('simulator_dispatch');
  }

  async waiting(id) {
    await this.until(() => {
      if (this.exchange?.closing && this.unregistered(id)) return true;
      const session = this.sessions.get(id);
      if (session?.state === 'pending_approval') throw failure('approval', `member ${id} needs approval`);
      if (session?.terminal_exit) throw failure('harness', `member ${id} exited before its answer`);
      if (['program_error', 'stop_failed', 'turn_aborted'].includes(session?.state_reason)) throw failure('member_turn', `member ${id}: ${session.state_reason}`);
      return ['idle', 'waiting_input'].includes(session?.state) && this.exchange?.session === id && this.exchange.completion;
    });
  }

  async send(id, text, { closing = false } = {}) {
    const previous = this.sessions.get(id);
    this.signal.throwIfAborted();
    if (!['idle', 'waiting_input'].includes(previous?.state) || this.exchange?.session !== id || !this.exchange.completion)
      throw failure('harness', `simulator tried to send without a completed settled exchange for ${id}: ${previous?.state}`);
    const terminal = this.bindings.get(id);
    if (!terminal) throw failure('harness', `no terminal binding for ${id}`);
    if (previous.turn_owed) {
      this.ws.send(JSON.stringify({ cmd: 'settle_turn', session_id: id }));
      await this.until(() => !this.sessions.get(id)?.turn_owed);
    }
    if (!['idle', 'waiting_input'].includes(this.sessions.get(id)?.state))
      throw failure('harness', `member ${id} resumed before simulator dispatch`);
    this.endHandoff();
    this.beginExchange(id, text, { closing });
    this.ws.send(JSON.stringify({ cmd: 'pty_input', id: terminal, data: `\x1b[200~${text}\x1b[201~\r`, source: 'automation' }));
    await this.waiting(id);
  }

  async transcript(id) {
    const lines = (await this.cli(['session', 'transcript', id, '--json'])).trim().split('\n').filter(Boolean);
    return lines.map(line => JSON.parse(line));
  }

  async sleep(id) {
    this.endHandoff();
    const terminal = this.bindings.get(id);
    const before = this.nativeRecords(id, terminal).length;
    const confirmationsBefore = this.sleepConfirmations.length;
    this.beginExchange(id, null, { cursor: before, closing: true });
    await this.cli(['crew', 'sleep', 'member', '--json']);
    await this.waiting(id);
    if (!this.unregistered(id)) {
      const message = 'sleep';
      this.sleepConfirmations.push({ session: id, message, at: new Date().toISOString(), origin: 'eval-controller' });
      await this.send(id, message, { closing: true });
    }
    await this.until(() => this.unregistered(id));
    this.endHandoff();
    this.exchange = null;
    return { records: this.nativeRecords(id, terminal).slice(before),
      confirmation_needed: this.sleepConfirmations.length > confirmationsBefore };
  }

  unregistered(id) { return this.frames.some(frame => frame.event === 'session_unregistered' && frame.session?.id === id); }

  async stop() {
    this.endHandoff();
    this.nativeWatch?.close();
    this.exchange = null;
    this.ws?.close();
    if (this.child && this.child.exitCode === null && this.child.signalCode === null) {
      await command(this.attnBinary, ['daemon', 'stop'], { env: this.env });
      await this.exited;
    }
    if (existsSync(join(this.data, 'daemon.log'))) copyFileSync(join(this.data, 'daemon.log'), join(this.root, 'daemon.log'));
    if (existsSync(this.data)) await command(this.attnBinary, ['instance', 'clean', this.instance], { env: this.env });
  }
}
