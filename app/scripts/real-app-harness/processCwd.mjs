import fs from 'node:fs';
import { execFileSync } from 'node:child_process';

export function processCwd(pid) {
  if (process.platform === 'linux') {
    try {
      return fs.readlinkSync(`/proc/${pid}/cwd`);
    } catch {
      return null;
    }
  }

  try {
    const out = execFileSync('lsof', ['-a', '-d', 'cwd', '-p', String(pid), '-Fn'], { encoding: 'utf8' });
    return out.split('\n').find((line) => line.startsWith('n'))?.slice(1) || null;
  } catch {
    return null;
  }
}
