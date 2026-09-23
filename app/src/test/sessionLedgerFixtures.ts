import type { SessionLedgerUpdate } from '../hooks/daemonSessionLedgerEvents';
import { reopenVerdictView } from '../components/sessionsLedger';
import type { SessionLedgerEntry, SessionReopen } from '../types/generated';
import { SessionReopenAction, SessionState } from '../types/generated';

export const NOW = new Date('2026-09-05T14:30:00Z');
export const now = () => NOW;

export const CLOSED_AT = '2026-09-05T10:00:00Z';

export function entry(overrides: Partial<SessionLedgerEntry> & { id: string }): SessionLedgerEntry {
  return {
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${overrides.id}`,
    last_seen: '2026-09-05T10:00:00Z',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    ...overrides,
  };
}

export function closedEntry(id: string, overrides: Partial<SessionLedgerEntry> = {}): SessionLedgerEntry {
  return entry({
    id,
    last_seen: '2026-09-05T09:00:00Z',
    closed_at: CLOSED_AT,
    closed_by: 'user',
    close_reason: 'work finished',
    ...overrides,
  });
}

export function liveEntry(id: string): SessionLedgerEntry {
  return entry({ id, last_seen: '2026-09-05T13:00:00Z' });
}

export function verdict(overrides: Partial<SessionReopen> = {}): SessionReopen {
  return {
    reopenable: true,
    actions: [SessionReopenAction.Reopen],
    directory_state: 'present',
    workspace_id: 'ws-1',
    workspace_plan: 'reuse',
    pane_plan: 'add',
    ...overrides,
  };
}

export function resolved(sessionId: string, reopen = verdict(), closedAt = CLOSED_AT): SessionLedgerUpdate {
  return { type: 'reopen-resolved', sessionId, resolution: { closedAt, state: 'ready', verdict: reopenVerdictView(reopen) } };
}

export function unresolvable(sessionId: string, error: string, closedAt = CLOSED_AT): SessionLedgerUpdate {
  return { type: 'reopen-resolved', sessionId, resolution: { closedAt, state: 'failed', error } };
}
