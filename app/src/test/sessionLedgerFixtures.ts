import type { SessionLedgerEntry, SessionReopen } from '../types/generated';
import { SessionReopenAction, SessionState } from '../types/generated';

export const NOW = new Date('2026-09-05T14:30:00Z');

const CLOSED_AT = '2026-09-05T10:00:00Z';

export function entry(overrides: Partial<SessionLedgerEntry> & { id: string }): SessionLedgerEntry {
  return {
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${overrides.id}`,
    last_seen: '2026-09-05T10:00:00Z',
    state: SessionState.Idle,
    profile_id: 'profile-default',
    profile_name: 'Default',
    ...overrides,
  };
}

export function closedEntry(id: string, overrides: Partial<SessionLedgerEntry> = {}): SessionLedgerEntry {
  return entry({
    id,
    last_seen: '2026-09-05T09:00:00Z',
    closed_at: CLOSED_AT,
    closed_by: { ref: 'user', name: 'the user' },
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
    profile_id: 'profile-default',
    profile_deleted: false,
    ...overrides,
  };
}
