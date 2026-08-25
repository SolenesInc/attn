export type UISessionState =
  | 'launching'
  | 'working'
  | 'waiting_input'
  | 'idle'
  | 'pending_approval'
  | 'scheduled'
  | 'recoverable'
  | 'unknown';

export function normalizeSessionState(state: string): UISessionState {
  switch (state) {
    case 'launching':
    case 'working':
    case 'waiting_input':
    case 'idle':
    case 'pending_approval':
    case 'scheduled':
    case 'recoverable':
    case 'unknown':
      return state;
    default:
      return 'unknown';
  }
}

// `scheduled` and `recoverable` are deliberately excluded: both auto-resume, so
// neither needs steering.
export function isAttentionSessionState(state: UISessionState): boolean {
  return state === 'waiting_input' || state === 'pending_approval' || state === 'unknown';
}
