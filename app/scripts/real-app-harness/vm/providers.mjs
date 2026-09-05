import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

export function quote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

export function run(file, args, options = {}) {
  const result = spawnSync(file, args, { stdio: 'inherit', ...options, maxBuffer: 128 * 1024 * 1024 });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw Object.assign(new Error(`${file} exited ${result.status ?? result.signal}`), { exitCode: result.status ?? 1 });
  }
  return String(result.stdout || '');
}

export function createProvider({ provider = 'lima', name = 'attn-linux', target, sshConfig } = {}, execute = run) {
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(name)) throw new Error('Invalid VM name');
  if (target && (!/^[a-zA-Z0-9_][a-zA-Z0-9_.@:[\]-]*$/.test(target))) throw new Error('Invalid SSH target');
  const capture = (file, args) => execute(file, args, { stdio: ['ignore', 'pipe', 'inherit'] }).trim();
  const adapters = {
    lima: {
      up() {
        const machines = capture('limactl', ['list', '--format', '{{.Name}}']).split('\n');
        const template = path.join(path.dirname(fileURLToPath(import.meta.url)), 'lima.yaml');
        execute('limactl', ['start', '--tty=false', ...(machines.includes(name) ? [name] : [`--name=${name}`, template])]);
      },
      stop: () => execute('limactl', ['stop', name]),
      status: () => execute('limactl', ['list', name]),
      connection: () => ({
        target: `lima-${name}`,
        config: capture('limactl', ['list', '--format', '{{.SSHConfigFile}}', name]),
      }),
    },
    orb: {
      up() {
        const machines = JSON.parse(capture('orbctl', ['list', '--format', 'json']));
        if (!Array.isArray(machines)) throw new Error('Unexpected orbctl list response');
        if (!machines.some((vm) => vm.name === name)) execute('orbctl', ['create', 'ubuntu:noble', name]);
        execute('orbctl', ['start', name]);
      },
      stop: () => execute('orbctl', ['stop', name]),
      status: () => execute('orbctl', ['info', name]),
      connection: () => ({ target: `${name}@orb` }),
    },
    ssh: {
      up: () => {},
      stop: () => { throw new Error('An SSH adapter does not own the machine; stop it with its VM manager.'); },
      status: () => execute('ssh', sshArgs({ target, config: sshConfig }, 'uname -srmo')),
      connection: () => ({ target, config: sshConfig }),
    },
  };
  if (!adapters[provider]) throw new Error(`Unknown provider ${provider}; choose lima, orb, or ssh`);
  if (provider === 'ssh' && !target) throw new Error('--provider ssh requires --target user@host');
  const adapter = adapters[provider];
  return {
    ...adapter,
    ssh(command, options) { return execute('ssh', sshArgs(adapter.connection(), command), options); },
    describe() { return { provider, name, ...adapter.connection() }; },
  };
}

export function sshArgs({ target, config }, command) {
  if (!target || target.startsWith('-')) throw new Error('Missing or invalid SSH target');
  if (config !== undefined && !config) throw new Error('VM has no SSH configuration; run up first');
  return [
    ...(config ? ['-F', path.resolve(config)] : []),
    '-o', 'BatchMode=yes', '-o', 'ForwardAgent=no', '-o', 'ConnectTimeout=10',
    target, command,
  ];
}
