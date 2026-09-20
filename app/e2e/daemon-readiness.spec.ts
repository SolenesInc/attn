import { test, expect } from '@playwright/test';
import { spawn, type ChildProcess } from 'child_process';
import { waitForDaemonReady } from './daemonReadiness';

async function stopChild(proc: ChildProcess): Promise<void> {
  if (proc.exitCode !== null || proc.signalCode !== null) return;
  await new Promise<void>((resolve) => {
    proc.once('exit', () => resolve());
    proc.kill('SIGTERM');
  });
}

test('daemon readiness follows its startup signal', async () => {
  const proc = spawn(process.execPath, ['-e',
    'process.stdin.once("data", () => require("fs").writeSync(3, "ready\\n"))',
  ], { stdio: ['pipe', 'pipe', 'pipe', 'pipe'] });

  try {
    const ready = waitForDaemonReady(proc);
    proc.stdin!.write('start');
    await expect(ready).resolves.toBeUndefined();
  } finally {
    await stopChild(proc);
  }
});

for (const [name, script, reason] of [
  ['early exit', 'process.exit(23)', 'closed without a ready signal'],
  ['startup failure', 'require("fs").writeSync(3, "error:port is occupied\\n"); process.exit(1)', 'port is occupied'],
]) {
  test(`daemon readiness reports ${name} with diagnostics`, async () => {
    const proc = spawn(process.execPath, ['-e', script], { stdio: ['pipe', 'pipe', 'pipe', 'pipe'] });
    try {
      await expect(waitForDaemonReady(proc, () => 'daemon log: fixture failed'))
        .rejects.toThrow(new RegExp(`${reason}[\\s\\S]*daemon log: fixture failed`));
    } finally {
      await stopChild(proc);
    }
  });
}
