import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';
import { registeredAgentPid } from './workerRegistry.mjs';

function dataDirWith(entries) {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-registry-'));
  entries.forEach((entry, index) => {
    const registryDir = path.join(dataDir, 'workers', `instance-${index}`, 'registry');
    fs.mkdirSync(registryDir, { recursive: true });
    fs.writeFileSync(path.join(registryDir, 'session.json'), JSON.stringify(entry));
  });
  return dataDir;
}

function exitedPid() {
  const result = spawnSync(process.execPath, ['-e', 'process.exit(0)']);
  return result.pid;
}

describe('registeredAgentPid', () => {
  it('returns the pid whose live process runs in the cwd the session was started in', () => {
    const dataDir = dataDirWith([{ session_id: 'alpha', child_pid: process.pid }]);
    expect(registeredAgentPid(dataDir, 'alpha', process.cwd())).toBe(process.pid);
  });

  it('refuses a recycled pid: alive, but running somewhere else', () => {
    const dataDir = dataDirWith([{ session_id: 'alpha', child_pid: process.pid }]);
    expect(registeredAgentPid(dataDir, 'alpha', path.join(process.cwd(), 'nowhere'))).toBeNull();
  });

  it('refuses a pid that has already exited', () => {
    const dataDir = dataDirWith([{ session_id: 'alpha', child_pid: exitedPid() }]);
    expect(registeredAgentPid(dataDir, 'alpha', process.cwd())).toBeNull();
  });

  it('skips entries for other sessions and other daemon instances', () => {
    const dataDir = dataDirWith([
      { session_id: 'beta', child_pid: process.pid },
      { session_id: 'alpha', child_pid: process.pid },
    ]);
    expect(registeredAgentPid(dataDir, 'alpha', process.cwd())).toBe(process.pid);
    expect(registeredAgentPid(dataDir, 'gamma', process.cwd())).toBeNull();
  });
});
