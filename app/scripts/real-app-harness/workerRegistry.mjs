import fs from 'node:fs';
import path from 'node:path';

import { processCwd } from './processCwd.mjs';

// Signal only a pid the registry recorded that is still running in the cwd this
// session was started in; kill(pid, 0) alone would let a recycled pid take it.
export function registeredAgentPid(dataDir, sessionId, expectedCwd) {
  const workersRoot = path.join(dataDir, 'workers');
  if (!expectedCwd) return null;
  for (const instance of fs.readdirSync(workersRoot)) {
    const registryDir = path.join(workersRoot, instance, 'registry');
    if (!fs.existsSync(registryDir)) continue;
    for (const name of fs.readdirSync(registryDir)) {
      const record = JSON.parse(fs.readFileSync(path.join(registryDir, name), 'utf8'));
      const pid = Number(record.child_pid);
      if (record.session_id !== sessionId || !Number.isInteger(pid) || pid <= 1) continue;
      if (processCwd(pid) === expectedCwd) return pid;
    }
  }
  return null;
}
