import type { ChildProcess } from 'child_process';
import type { Readable } from 'stream';

export async function waitForDaemonReady(
  proc: ChildProcess,
  getDebugInfo?: () => string,
): Promise<void> {
  const signal = proc.stdio[3] as Readable | null;
  if (!signal) throw new Error('Daemon startup requires a readiness pipe on fd 3');

  let message = '';
  for await (const chunk of signal) {
    message += chunk.toString();
    if (message.includes('\n')) break;
  }
  if (message.trim() === 'ready') return;

  const reason = message.trim() || 'readiness pipe closed without a ready signal';
  throw new Error(`Daemon startup failed: ${reason}.\n${getDebugInfo?.() ?? ''}`);
}
