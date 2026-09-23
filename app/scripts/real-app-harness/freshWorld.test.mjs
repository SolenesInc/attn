import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { assertFreshWorldTargetSafe, commandRunsExecutable, registeredPtyHostPids } from './freshWorld.mjs';

describe('assertFreshWorldTargetSafe', () => {
  it('throws when instance is empty (the prod instance string)', () => {
    expect(() => assertFreshWorldTargetSafe({ instance: '', appPath: '/Users/victor/Applications/attn-dev.app' }))
      .toThrow(/instance/i);
  });

  it("throws when instance is 'default' even with a non-prod appPath", () => {
    expect(() => assertFreshWorldTargetSafe({ instance: 'default', appPath: '/Users/victor/Applications/attn-fxm1.app' }))
      .toThrow(/default/i);
  });

  it('throws when appPath is missing', () => {
    expect(() => assertFreshWorldTargetSafe({ instance: 'fxm1', appPath: '' }))
      .toThrow(/appPath/i);
    expect(() => assertFreshWorldTargetSafe({ instance: 'fxm1' }))
      .toThrow(/appPath/i);
  });

  it('throws for a production-shaped target on a named instance (defense in depth)', () => {
    expect(() => assertFreshWorldTargetSafe({ instance: 'fxm1', appPath: '/Users/victor/Applications/attn.app' }))
      .toThrow();
  });

  it('does not throw for a realistic named-instance target', () => {
    expect(() => assertFreshWorldTargetSafe({ instance: 'fxm1', appPath: '/Users/victor/Applications/attn-fxm1.app' }))
      .not.toThrow();
  });

  it("does not throw for the 'dev' instance", () => {
    expect(() => assertFreshWorldTargetSafe({ instance: 'dev', appPath: '/Users/victor/Applications/attn-dev.app' }))
      .not.toThrow();
  });
});

describe('commandRunsExecutable', () => {
  const host = '/home/runner/.local/share/attn-app-acceptance/bin/attn-pty-host';

  it('matches the exact packaged executable with or without arguments', () => {
    expect(commandRunsExecutable(host, host)).toBe(true);
    expect(commandRunsExecutable(`${host} --socket /tmp/attn.sock`, host)).toBe(true);
  });

  it('does not match another instance or an executable with the same prefix', () => {
    expect(commandRunsExecutable('/home/runner/.local/share/attn-app-other/bin/attn-pty-host', host)).toBe(false);
    expect(commandRunsExecutable(`${host}-debug --socket /tmp/attn.sock`, host)).toBe(false);
    expect(commandRunsExecutable(`/bin/sh ${host}`, host)).toBe(false);
  });
});

describe('registeredPtyHostPids', () => {
  const host = '/home/runner/.local/share/attn-app-acceptance/bin/attn-pty-host';

  function dataDirWithHosts(entries) {
    const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'fresh-world-hosts-'));
    for (const [daemonInstance, generation, body] of entries) {
      const dir = path.join(dataDir, 'pty-hosts', daemonInstance, 'hosts');
      fs.mkdirSync(dir, { recursive: true });
      fs.writeFileSync(path.join(dir, `${generation}.json`), body);
    }
    return dataDir;
  }

  it('returns only registry pids whose live command still runs this executable', () => {
    const dataDir = dataDirWithHosts([
      ['d-1', 'gen-1', JSON.stringify({ host_pid: 4242, executable: host })],
      ['d-1', 'gen-2', JSON.stringify({ host_pid: 4343, executable: host })],
      ['d-2', 'gen-1', JSON.stringify({ host_pid: 4444, executable: host })],
      ['d-2', 'notes.txt', 'not a registry'],
      ['d-3', 'gen-1', '{not json'],
    ]);
    const commands = {
      4242: `${host} --socket /tmp/attn.sock`,
      4343: '/usr/bin/sleep 100',
      4444: '',
    };
    expect(registeredPtyHostPids({ dataDir, executablePath: host, commandLineFor: (pid) => commands[pid] ?? '' }))
      .toEqual([4242]);
  });

  it('is empty when the instance has never started a shared host', () => {
    const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'fresh-world-hosts-'));
    expect(registeredPtyHostPids({ dataDir, executablePath: host, commandLineFor: () => host })).toEqual([]);
  });
});
