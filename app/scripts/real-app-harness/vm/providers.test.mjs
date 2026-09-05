import { describe, expect, it, vi } from 'vitest';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createProvider, quote, run, sshArgs } from './providers.mjs';
import { ensureBundledPiInstalled } from './build.mjs';
import { guestCommand, parseArgs } from '../linux-runner.mjs';

describe('Linux VM adapters', () => {
  it('uses the existing Lima instance and its generated SSH configuration', () => {
    const execute = vi.fn((file, args) => {
      if (args.includes('{{.Name}}')) return 'other\nattn-linux\n';
      if (args.includes('{{.SSHConfigFile}}')) return '/tmp/VM with spaces/ssh.config\n';
      return '';
    });
    const adapter = createProvider({}, execute);
    adapter.up();
    adapter.ssh('printf hello');
    expect(execute).toHaveBeenCalledWith('limactl', ['start', '--tty=false', 'attn-linux']);
    expect(execute).toHaveBeenLastCalledWith('ssh', [
      '-F', '/tmp/VM with spaces/ssh.config', '-o', 'BatchMode=yes', '-o', 'ForwardAgent=no',
      '-o', 'ConnectTimeout=10', 'lima-attn-linux', 'printf hello',
    ], undefined);
  });

  it('does not create an OrbStack VM when listing failed', () => {
    const execute = vi.fn(() => { throw new Error('manager unavailable'); });
    expect(() => createProvider({ provider: 'orb' }, execute).up()).toThrow('manager unavailable');
    expect(execute).toHaveBeenCalledTimes(1);
  });

  it('checks exact OrbStack names before creation', () => {
    const execute = vi.fn((file, args) => args[0] === 'list' ? '[{"name":"attn-linux-other"}]' : '');
    createProvider({ provider: 'orb' }, execute).up();
    expect(execute).toHaveBeenCalledWith('orbctl', ['create', 'ubuntu:noble', 'attn-linux']);
  });

  it('uses an existing SSH machine without owning its lifecycle', () => {
    const execute = vi.fn();
    const adapter = createProvider({ provider: 'ssh', target: 'tester@linux' }, execute);
    adapter.up();
    expect(execute).not.toHaveBeenCalled();
    adapter.ssh('true');
    expect(execute.mock.calls[0][1]).toContain('tester@linux');
    expect(() => adapter.stop()).toThrow('does not own');
  });

  it.each([{ provider: 'unknown' }, { name: '--all' }, { provider: 'ssh' }, { target: '-oProxyCommand=bad' }])(
    'rejects ambiguous or unsafe targets: %j', (options) => expect(() => createProvider(options)).toThrow(),
  );

  it('fails when a Lima SSH config has not been generated', () => {
    expect(() => sshArgs({ target: 'lima-missing', config: '' }, 'true')).toThrow('run up first');
  });

  it('preserves raw output and failing command exit status', () => {
    expect(run('printf', [' trailing space \n'], { stdio: 'pipe' })).toBe(' trailing space \n');
    expect(() => run('sh', ['-c', 'exit 23'], { stdio: 'pipe' })).toThrow(expect.objectContaining({ exitCode: 23 }));
  });

  it('quotes guest arguments without evaluating shell substitutions', () => {
    const literal = "quote' $(exit 12) `exit 13` \n end";
    const result = spawnSync('sh', ['-c', `printf %s ${quote(literal)}`], { encoding: 'utf8' });
    expect(result.status).toBe(0);
    expect(result.stdout).toBe(literal);
    expect(guestCommand('/tmp/checkout', 'linux-test', ['printf', literal])).toContain(quote(literal));
  });

  it('separates runner flags from guest argv and refuses production profiles', () => {
    const options = parseArgs(['run', '--provider', 'ssh', '--target', 'tester@vm', '--', 'printf', '--profile', 'prod'], {});
    expect(options.commandArgs).toEqual(['printf', '--profile', 'prod']);
    expect(() => parseArgs(['--profile', 'default'], {})).toThrow('non-production');
    expect(() => parseArgs(['--provider'], {})).toThrow('needs a value');
  });
});

it.runIf(process.platform === 'linux')('isolates guest environment, excludes the lock fd and refuses overlapping commands', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'runner-command-test-'));
  try {
    const fixtureDir = path.join(root, 'source/app/scripts/real-app-harness');
    fs.mkdirSync(fixtureDir, { recursive: true });
    fs.writeFileSync(path.join(root, 'managed-source'), '');
    fs.writeFileSync(path.join(fixtureDir, 'ci-xdg-open'), '#!/bin/sh\nexit 0\n');
    const script = guestCommand(root, 'linux-test', [process.execPath, '-e', `
      const fs = require('fs');
      console.log(JSON.stringify({ env: process.env, lock: fs.existsSync('/proc/self/fd/9') ? fs.readlinkSync('/proc/self/fd/9') : '' }));
    `]);
    const result = spawnSync('bash', ['-c', script], {
      encoding: 'utf8', env: { ...process.env, ATTN_DATA_DIR: '/host-profile', GITHUB_TOKEN: 'fixture-only' },
    });
    expect(result.status, result.stderr).toBe(0);
    const receipt = JSON.parse(result.stdout);
    expect(receipt.env.ATTN_DATA_DIR).toBeUndefined();
    expect(receipt.env.GITHUB_TOKEN).toBeUndefined();
    expect(receipt.env.ATTN_PROFILE).toBe('linux-test');
    expect(receipt.lock).not.toBe(path.join(root, 'operation.lock'));
    const blocked = spawnSync('flock', ['-n', path.join(root, 'operation.lock'), 'bash', '-c', script], { encoding: 'utf8' });
    expect(blocked.status).toBe(75);
    expect(blocked.stderr).toContain('Runner is busy');
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});

it('rebuilding keeps an installed bundled plugin and propagates catalog errors', () => {
  const execute = vi.fn(() => JSON.stringify({ plugins: [{ name: 'attn-pi', availability: 'bundled', installation_state: 'installed' }] }));
  ensureBundledPiInstalled(execute);
  expect(execute).toHaveBeenCalledTimes(1);
  execute.mockReset().mockReturnValueOnce(JSON.stringify({ plugins: [] }));
  ensureBundledPiInstalled(execute);
  expect(execute).toHaveBeenLastCalledWith('./attn', ['plugin', 'install-bundled', 'attn-pi']);
  execute.mockReset().mockImplementation(() => { throw new Error('daemon unavailable'); });
  expect(() => ensureBundledPiInstalled(execute)).toThrow('daemon unavailable');
  expect(execute).toHaveBeenCalledTimes(1);
});
