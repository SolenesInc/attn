import fs from 'node:fs';
import path from 'node:path';

import { processCwd } from './processCwd.mjs';

// Signal only a pid the registry recorded that is still running in the cwd this
// session was started in; kill(pid, 0) alone would let a recycled pid take it.
export function registeredTerminalProcesses(dataDir, terminalId, expectedCwd) {
  if (!expectedCwd) return null;
  const hostsRoot = path.join(dataDir, 'pty-hosts');
  for (const instance of fs.existsSync(hostsRoot) ? fs.readdirSync(hostsRoot) : []) {
    const registryDir = path.join(hostsRoot, instance, 'registry');
    if (!fs.existsSync(registryDir)) continue;
    for (const name of fs.readdirSync(registryDir)) {
      const record = JSON.parse(fs.readFileSync(path.join(registryDir, name), 'utf8'));
      const pid = Number(record.child_pid);
      if (record.session_id !== terminalId || !Number.isInteger(pid) || pid <= 1) continue;
      if (processCwd(pid) === expectedCwd) return { childPid: pid };
    }
  }
  return null;
}

export function registeredAgentPid(dataDir, terminalId, expectedCwd) {
  return registeredTerminalProcesses(dataDir, terminalId, expectedCwd)?.childPid ?? null;
}
