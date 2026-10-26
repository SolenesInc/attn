import { spawn } from 'node:child_process';
import { EventEmitter, once } from 'node:events';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, readdirSync, copyFileSync, existsSync, realpathSync } from 'node:fs';
import { join, resolve, relative } from 'node:path';
import { homedir, tmpdir } from 'node:os';
import { randomUUID } from 'node:crypto';

export const json = path => JSON.parse(readFileSync(path, 'utf8'));
export const save = (path, value) => writeFileSync(path, JSON.stringify(value, null, 2) + '\n');
export const failure = (kind, message) => Object.assign(new Error(message), { failure_class: kind });
const quote = value => "'" + String(value).replaceAll("'", "'\\''") + "'";

export function environment() {
  return Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    !/^(ATTN_|CLAUDE_CONFIG_DIR$|CLAUDECODE$|CODEX_HOME$|CODEX_THREAD_ID$)/.test(key)));
}

export async function command(binary, args, options = {}) {
  const child = spawn(binary, args, { env: environment(), ...options, stdio: ['pipe', 'pipe', 'pipe'] });
  let stdout = '', stderr = '';
  child.stdout.on('data', data => { stdout += data; });
  child.stderr.on('data', data => { stderr += data; });
  child.stdin.end(options.input ?? '');
  const [code, signal] = await once(child, 'exit');
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
    this.signal = signal;
    this.runtimeBase = join(tmpdir(), 'attn-crew-memory-eval');
    mkdirSync(this.runtimeBase, { recursive: true });
    this.runtimeBase = realpathSync(this.runtimeBase);
    this.root = mkdtempSync(join(this.runtimeBase, 'trial-'));
    this.instance = 'cm-' + randomUUID().replaceAll('-', '').slice(0, 12);
    this.data = join(this.root, 'instance');
    this.home = join(this.data, 'crew', 'member');
    this.sessions = new Map();
    this.bindings = new Map();
    this.frames = [];
    this.env = { ...environment(), ATTN_INSTANCE: this.instance, ATTN_TOOL_HOME: join(this.root, 'tools'),
      ATTN_PTY_BACKEND: 'worker', ATTN_HEADLESS_TASKS: 'off',
      ATTN_GITHUB_POLLING: 'off', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CONFIG_DIR: join(this.root, 'tools', '.claude') };
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
    writeFileSync(join(this.home, 'CHARTER.md'), input.charter);
    mkdirSync(join(this.home, 'handoffs'));
    for (const letter of input.letters) writeFileSync(join(this.home, 'handoffs', letter.name), letter.text);
    for (const [path, text] of Object.entries(input.files ?? {})) this.writeInput(path, text);
    const priorInstances = readdirSync(homedir()).filter(name => name.startsWith('.attn-cm-'))
      .map(name => join(homedir(), name)).filter(path => path !== this.data);
    const roots = [this.config.private_root, ...priorInstances, join(homedir(), '.attn'), join(homedir(), 'exo'),
      join(homedir(), '.claude', 'projects'), join(homedir(), '.codex', 'sessions'),
      join(homedir(), '.codex', 'archived_sessions'), ...(this.config.hidden_roots ?? [])];
    this.policy = join(this.root, 'trial.sb');
    writeFileSync(this.policy, sandboxPolicy(roots, this.runtimeBase, this.root));
    const wrapper = join(this.root, 'claude');
    const exports = Object.entries(this.env).filter(([key]) => /^(ATTN_|CLAUDE_CODE_DISABLE_AUTO_MEMORY|CLAUDE_CONFIG_DIR|CODEX_HOME)/.test(key));
    writeFileSync(wrapper, '#!/bin/sh\nunset ATTN_DB_PATH ATTN_SOCKET_PATH ATTN_CONFIG_PATH ATTN_PLUGIN_DIR ATTN_CLIENT_TOKEN ATTN_PROFILE_ID\n' + exports.map(([key, value]) => `export ${key}=${quote(value)}\n`).join('') +
      `exec ${quote(this.config.claude_binary)} --strict-mcp-config --mcp-config '{"mcpServers":{}}' --setting-sources '' "$@"\n`, { mode: 0o700 });
    this.env.ATTN_CLAUDE_EXECUTABLE = wrapper;
    await this.probeIsolation();
    this.child = spawn('/usr/bin/sandbox-exec', ['-f', this.policy, this.config.attn_binary, 'daemon'],
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
    const member = roster.find(row => row.id === 'member');
    if (member?.resolved_model !== this.config.member_model || member?.resolved_effort !== this.config.member_effort)
      throw failure('fixture', `crew settings differ: requested ${this.config.member_model}/${this.config.member_effort}, resolved ${member?.resolved_model}/${member?.resolved_effort}`);
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
    while (!predicate()) {
      if (this.signal.aborted) throw this.signal.reason;
      if (this.child?.exitCode !== null && this.child?.exitCode !== undefined) throw failure('harness', `daemon exited: ${this.daemonOutput}`);
      await once(this, 'changed', { signal: this.signal });
    }
  }

  cli(args) { return command(this.config.attn_binary, args, { env: this.env, signal: this.signal }); }

  async wake() {
    const launched = JSON.parse(await this.cli(['crew', 'wake', 'member', '--json']));
    const id = launched.session_id;
    await this.waiting(id);
    return id;
  }

  async waiting(id, after) {
    await this.until(() => {
      const session = this.sessions.get(id);
      if (session?.state === 'pending_approval') throw failure('approval', `member ${id} needs approval`);
      if (session?.terminal_exit) throw failure('harness', `member ${id} exited before its answer`);
      return session?.state === 'waiting_input' && (!after || session.turn_opened_at !== after);
    });
  }

  async send(id, text) {
    const previous = this.sessions.get(id);
    if (previous?.state !== 'waiting_input') throw failure('harness', `simulator tried to send while ${id} was ${previous?.state}`);
    const terminal = this.bindings.get(id);
    if (!terminal) throw failure('harness', `no terminal binding for ${id}`);
    this.ws.send(JSON.stringify({ cmd: 'settle_turn', session_id: id }));
    await this.until(() => !this.sessions.get(id)?.turn_owed);
    this.ws.send(JSON.stringify({ cmd: 'pty_input', id: terminal, data: `\x1b[200~${text}\x1b[201~\r`, source: 'automation' }));
    await this.waiting(id, previous.turn_opened_at);
  }

  async transcript(id) {
    const lines = (await this.cli(['session', 'transcript', id, '--json'])).trim().split('\n').filter(Boolean);
    return lines.map(line => JSON.parse(line));
  }

  async sleep(id) {
    await this.cli(['crew', 'sleep', 'member', '--json']);
    await this.until(() => this.frames.some(frame => frame.event === 'session_unregistered' && frame.session?.id === id));
  }

  async stop() {
    this.ws?.close();
    if (this.child && this.child.exitCode === null) {
      await command(this.config.attn_binary, ['daemon', 'stop'], { env: this.env });
      await this.exited;
    }
    if (existsSync(join(this.data, 'daemon.log'))) copyFileSync(join(this.data, 'daemon.log'), join(this.root, 'daemon.log'));
    if (existsSync(this.data)) await command(this.config.attn_binary, ['instance', 'clean', this.instance], { env: this.env });
  }
}
