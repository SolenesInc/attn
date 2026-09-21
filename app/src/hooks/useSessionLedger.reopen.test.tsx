import { useEffect } from 'react';
import { act, render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SessionReopenAction, SessionState } from '../types/generated';
import type { SessionLedgerEntry, SessionReopen } from '../types/generated';
import type { SessionLedgerPage, SessionLedgerQuery } from './daemonSessionLedgerEvents';
import { useSessionLedger } from './useSessionLedger';
import type { SessionLedgerView } from './useSessionLedger';

const NOW = new Date('2026-09-05T14:30:00Z');
const now = () => NOW;

function closedEntry(id: string, closedAt = '2026-09-05T13:00:00Z'): SessionLedgerEntry {
  return {
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${id}`,
    last_seen: '2026-09-05T10:00:00Z',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    id,
    closed_at: closedAt,
    closed_by: 'user',
  };
}

function reopen(reason: string): SessionReopen {
  return {
    reopenable: true,
    actions: [SessionReopenAction.Reopen],
    checking: false,
    directory_state: 'present',
    workspace_id: 'ws-1',
    workspace_plan: 'reuse',
    pane_plan: 'add',
    reason,
  };
}

function renderLedger(list: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>) {
  const seen: { view: SessionLedgerView | null } = { view: null };
  function Harness() {
    const view = useSessionLedger({ enabled: true, list, now });
    useEffect(() => { seen.view = view; });
    return null;
  }
  render(<Harness />);
  return seen;
}

describe('useSessionLedger streamed reopen eligibility', () => {
  it('replaces initial resolutions and appends load-more resolutions', async () => {
    const first = closedEntry('new');
    const older = closedEntry('older', '2026-09-04T13:00:00Z');
    const list = vi.fn(async (query: SessionLedgerQuery) => query.before
      ? { entries: [older], omitted: 0 }
      : { entries: [first], omitted: 1, next_before: 'older' });
    const seen = renderLedger(list);

    await waitFor(() => expect(seen.view?.resolutions.new?.state).toBe('pending'));
    await act(async () => { seen.view?.loadMore(); });
    await waitFor(() => expect(seen.view?.entries.map((entry) => entry.id)).toEqual(['new', 'older']));
    expect(seen.view?.resolutions).toEqual({
      new: { closedAt: first.closed_at, state: 'pending' },
      older: { closedAt: older.closed_at, state: 'pending' },
    });
  });

  it('ignores a stale generation and accepts the matching generation', async () => {
    const entry = closedEntry('s1');
    const seen = renderLedger(async () => ({ entries: [entry], omitted: 0 }));
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));

    await act(async () => {
      seen.view?.recordResolution({
        sessionId: 's1', closedAt: '2026-09-04T13:00:00Z', success: true, reopen: reopen('stale'),
      });
    });
    expect(seen.view?.resolutions.s1).toEqual({ closedAt: entry.closed_at, state: 'pending' });

    await act(async () => {
      seen.view?.recordResolution({
        sessionId: 's1', closedAt: entry.closed_at!, success: true, reopen: reopen('current'),
      });
    });
    expect(seen.view?.resolutions.s1).toMatchObject({ state: 'ready', reopen: { reason: 'current' } });
  });

  it('applies an early event when its row arrives', async () => {
    let release: ((page: SessionLedgerPage) => void) | undefined;
    const list = vi.fn(() => new Promise<SessionLedgerPage>((resolve) => { release = resolve; }));
    const entry = closedEntry('s1');
    const seen = renderLedger(list);
    await waitFor(() => expect(list).toHaveBeenCalledTimes(1));

    await act(async () => {
      seen.view?.recordResolution({
        sessionId: 's1', closedAt: entry.closed_at!, success: true, reopen: reopen('arrived first'),
      });
      release?.({ entries: [entry], omitted: 0 });
    });
    await waitFor(() => expect(seen.view?.resolutions.s1).toMatchObject({
      state: 'ready', reopen: { reason: 'arrived first' },
    }));
  });

  it('keeps a matching terminal failure', async () => {
    const entry = closedEntry('s1');
    const seen = renderLedger(async () => ({ entries: [entry], omitted: 0 }));
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));

    await act(async () => {
      seen.view?.recordResolution({
        sessionId: 's1', closedAt: entry.closed_at!, success: false, error: 'git unavailable',
      });
    });
    expect(seen.view?.resolutions.s1).toEqual({
      closedAt: entry.closed_at,
      state: 'failed',
      error: 'git unavailable',
    });
  });

  it('reissues the streamed page after reconnect', async () => {
    const entry = closedEntry('s1');
    const list = vi.fn(async () => ({ entries: [entry], omitted: 0 }));
    const seen: { view: SessionLedgerView | null } = { view: null };
    function Harness({ generation }: { generation: number }) {
      const view = useSessionLedger({ enabled: true, list, connectionGeneration: generation, now });
      useEffect(() => { seen.view = view; });
      return null;
    }
    const view = render(<Harness generation={1} />);
    await waitFor(() => expect(list).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));

    view.rerender(<Harness generation={2} />);

    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ reopen: true }));
  });
});
