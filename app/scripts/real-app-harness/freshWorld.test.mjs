import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { assertFreshWorldTargetSafe, commandRunsExecutable, registeredPtyHostPids } from './freshWorld.mjs';

describe('assertFreshWorldTargetSafe', () => {
  it('throws when instance is empty (the prod instance string)', () => {
    assert.throws(() => assertFreshWorldTargetSafe({ instance: '', appPath: '/Users/victor/Applications/attn-dev.app' }), /instance/i);
  });

  it("throws when instance is 'default' even with a non-prod appPath", () => {
    assert.throws(() => assertFreshWorldTargetSafe({ instance: 'default', appPath: '/Users/victor/Applications/attn-fxm1.app' }), /default/i);
  });

  it('throws when appPath is missing', () => {
    assert.throws(() => assertFreshWorldTargetSafe({ instance: 'fxm1', appPath: '' }), /appPath/i);
    assert.throws(() => assertFreshWorldTargetSafe({ instance: 'fxm1' }), /appPath/i);
  });

  it('throws for a production-shaped target on a named instance (defense in depth)', () => {
    assert.throws(() => assertFreshWorldTargetSafe({ instance: 'fxm1', appPath: '/Users/victor/Applications/attn.app' }));
  });

  it('does not throw for a realistic named-instance target', () => {
    assert.doesNotThrow(() => assertFreshWorldTargetSafe({ instance: 'fxm1', appPath: '/Users/victor/Applications/attn-fxm1.app' }));
  });
});

describe('commandRunsExecutable', () => {
  const host = '/home/runner/.local/share/attn-app-acceptance/bin/attn-pty-host';

  it('matches the exact packaged executable with or without arguments', () => {
    assert.equal(commandRunsExecutable(host, host), true);
    assert.equal(commandRunsExecutable(`${host} --socket /tmp/attn.sock`, host), true);
  });

  it('does not match another instance or an executable with the same prefix', () => {
    assert.equal(commandRunsExecutable('/home/runner/.local/share/attn-app-other/bin/attn-pty-host', host), false);
    assert.equal(commandRunsExecutable(`${host}-debug --socket /tmp/attn.sock`, host), false);
    assert.equal(commandRunsExecutable(`/bin/sh ${host}`, host), false);
  });
});

describe('registeredPtyHostPids', () => {
  function dataDirWithHosts(entries) {
    const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'fresh-world-hosts-'));
    for (const [daemonInstance, generation, body] of entries) {
      const dir = path.join(dataDir, 'pty-hosts', daemonInstance, 'hosts');
      fs.mkdirSync(dir, { recursive: true });
      fs.writeFileSync(path.join(dir, `${generation}.json`), body);
    }
    return dataDir;
  }

  it('finds hosts running from the artifact the daemon installed in the data dir', () => {
    const dataDir = dataDirWithHosts([
      ['d-1', 'gen-1', JSON.stringify({ host_pid: 4242 })],
      ['d-1', 'gen-2', JSON.stringify({ host_pid: 4343 })],
      ['d-2', 'gen-1', JSON.stringify({ host_pid: 4444 })],
      ['d-2', 'notes.txt', 'not a registry'],
      ['d-3', 'gen-1', '{not json'],
    ]);
    const commands = {
      4242: `${dataDir}/pty-hosts/d-1/artifacts/abc/attn-pty-host --daemon-instance-id d-1`,
      4343: '/usr/bin/sleep 100',
      4444: '',
    };
    assert.deepEqual(registeredPtyHostPids({ dataDir, commandLineFor: (pid) => commands[pid] ?? '' }), [4242]);
  });
});
