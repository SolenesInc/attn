import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { SessionsPanel } from './SessionsPanel';
import type { SessionLedgerPage, SessionLedgerQuery } from '../hooks/daemonSessionLedgerEvents';
import type { SessionLedgerEntry } from '../types/generated';
import { SessionState } from '../types/generated';

function entry(overrides: Partial<SessionLedgerEntry> & { id: string }): SessionLedgerEntry {
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

const NOW = new Date('2026-09-05T14:30:00Z');

async function open(page: SessionLedgerPage) {
  const list = vi.fn(async (_query: SessionLedgerQuery) => page);
  const view = render(
    <SessionsPanel isOpen onClose={() => {}} listSessions={list} now={() => NOW} />,
  );
  await waitFor(() => expect(screen.queryByText('Reading the ledger…')).toBeNull());
  return view;
}

describe('who closed a session', () => {
  it('names the session that closed another one, not its id', async () => {
    await open({
      omitted: 0,
      entries: [
        entry({ id: 'dispatcher', label: 'Ledger work' }),
        entry({
          id: 'delegate',
          label: 'Worktree reclaim',
          closed_at: '2026-09-05T12:00:00Z',
          closed_by: 'dispatcher',
          close_reason: 'it went quiet three hours ago',
        }),
      ],
    });

    const line = screen.getByText(/closed by/);
    expect(line.textContent).toBe('closed by Ledger work: it went quiet three hours ago');
    expect(line.getAttribute('title')).toBe('dispatcher');
  });

  it('falls back to the id when the closer is not on this page', async () => {
    await open({
      omitted: 0,
      entries: [
        entry({
          id: 'delegate',
          closed_at: '2026-09-05T12:00:00Z',
          closed_by: 'sess-off-page',
          close_reason: 'the run finished',
        }),
      ],
    });

    expect(screen.getByText(/closed by/).textContent)
      .toBe('closed by sess-off-page: the run finished');
  });

  it('still says you for a close the user made in the app', async () => {
    await open({
      omitted: 0,
      entries: [
        entry({ id: 'delegate', closed_at: '2026-09-05T12:00:00Z', closed_by: 'user' }),
      ],
    });

    expect(screen.getByText(/closed by/).textContent).toBe('closed by you');
  });
});
