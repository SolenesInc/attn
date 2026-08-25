// The daemon tears sessions down with SIGTERM (exit code 143) on normal
// completion, stop, and pane-close, so 143 must not read as a crash.

// 128 + N is the conventional shell encoding for "terminated by signal N".
const SIGNAL_BY_CODE: Record<number, string> = {
  129: 'SIGHUP',
  130: 'SIGINT',
  143: 'SIGTERM',
};

const GRACEFUL_SIGNALS = new Set(['SIGHUP', 'SIGINT', 'SIGTERM']);

function normalizeSignal(code: number, signal?: string): string | undefined {
  const trimmed = signal?.trim();
  if (trimmed) {
    const upper = trimmed.toUpperCase();
    return upper.startsWith('SIG') ? upper : `SIG${upper}`;
  }
  return SIGNAL_BY_CODE[code];
}

export function formatExitNotice(code: number, signal?: string): string {
  if (code === 0 && !signal?.trim()) {
    return '[Session ended]';
  }
  const sig = normalizeSignal(code, signal);
  if (sig && GRACEFUL_SIGNALS.has(sig)) {
    return '[Session ended]';
  }
  return `[Process exited with code ${code}]`;
}
