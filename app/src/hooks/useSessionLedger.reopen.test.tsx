import { useEffect } from 'react';
import { act, render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { SessionLedgerEntry } from '../types/generated';
import type { SessionLedgerPage, SessionLedgerQuery } from './daemonSessionLedgerEvents';
import { EMPTY_SESSION_FILTERS, useSessionLedger } from './useSessionLedger';
import type { SessionLedgerFilters, SessionLedgerView } from './useSessionLedger';
import { createSessionLedgerTestConnection } from '../test/sessionLedgerTestConnection';
import { NOW, closedEntry, now, resolved, unresolvable, verdict } from '../test/sessionLedgerFixtures';

function renderLedger(
  list: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>,
  initialFilters: SessionLedgerFilters = EMPTY_SESSION_FILTERS,
) {
  const transport = createSessionLedgerTestConnection(list);
  const seen: { view: SessionLedgerView | null } = { view: null };
  function Harness() {
    const view = useSessionLedger({ enabled: true, connection: transport.connection, now, initialFilters });
    useEffect(() => { seen.view = view; });
    return null;
  }
  render(<Harness />);
  return Object.assign(seen, transport);
}

describe('useSessionLedger streamed reopen eligibility', () => {
  it('replaces initial resolutions and appends load-more resolutions', async () => {
    const first = closedEntry('new');
    const older = closedEntry('older', { closed_at: '2026-09-04T13:00:00Z' });
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
    const staleClosedAt = '2026-09-04T13:00:00Z';
    const seen = renderLedger(async () => ({ entries: [entry], omitted: 0 }));
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));

    act(() => seen.emit(resolved('s1', verdict({ reason: 'stale' }), staleClosedAt)));
    expect(seen.view?.resolutions.s1).toEqual({ closedAt: entry.closed_at, state: 'pending' });

    act(() => seen.emit(resolved('s1', verdict({ reason: 'current' }))));
    expect(seen.view?.resolutions.s1).toMatchObject({ state: 'ready', verdict: { reason: 'current' } });

    act(() => seen.emit(unresolvable('s1', 'stale failure', staleClosedAt)));
    expect(seen.view?.resolutions.s1).toMatchObject({ state: 'ready', verdict: { reason: 'current' } });
  });

  it('applies an early event when its row arrives', async () => {
    let release: ((page: SessionLedgerPage) => void) | undefined;
    const list = vi.fn(() => new Promise<SessionLedgerPage>((resolve) => { release = resolve; }));
    const entry = closedEntry('s1');
    const seen = renderLedger(list);
    await waitFor(() => expect(list).toHaveBeenCalledTimes(1));

    await act(async () => {
      seen.emit(resolved('s1', verdict({ reason: 'arrived first' })));
      release?.({ entries: [entry], omitted: 0 });
    });
    await waitFor(() => expect(seen.view?.resolutions.s1).toMatchObject({
      state: 'ready', verdict: { reason: 'arrived first' },
    }));
  });

  it('keeps a matching terminal failure', async () => {
    const entry = closedEntry('s1');
    const seen = renderLedger(async () => ({ entries: [entry], omitted: 0 }));
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));

    act(() => seen.emit(unresolvable('s1', 'git unavailable')));
    expect(seen.view?.resolutions.s1).toEqual({
      closedAt: entry.closed_at,
      state: 'failed',
      error: 'git unavailable',
    });
  });

  it('reissues the streamed page after reconnect', async () => {
    const list = vi.fn(async () => ({ entries: [closedEntry('s1')], omitted: 0 }));
    const seen = renderLedger(list);
    await waitFor(() => expect(list).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));
    act(() => seen.emit(resolved('s1', verdict({ reason: 'before reconnect' }))));
    expect(seen.view?.resolutions.s1?.state).toBe('ready');

    act(() => seen.setConnected(true, 2));

    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ reopen: true }));
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));
  });

  it('clears a superseded load-more state after reconnect', async () => {
    let releaseLoadMore: ((page: SessionLedgerPage) => void) | undefined;
    const list = vi.fn((query: SessionLedgerQuery) => {
      if (query.before) {
        return new Promise<SessionLedgerPage>((resolve) => { releaseLoadMore = resolve; });
      }
      return Promise.resolve({ entries: [closedEntry('s1')], omitted: 1, next_before: 'older' });
    });
    const seen = renderLedger(list);
    await waitFor(() => expect(seen.view?.loading).toBe(false));

    act(() => { seen.view?.loadMore(); });
    await waitFor(() => expect(seen.view?.loadingMore).toBe(true));
    act(() => seen.setConnected(true, 2));

    await waitFor(() => expect(list).toHaveBeenCalledTimes(3));
    await waitFor(() => expect(seen.view?.loadingMore).toBe(false));
    await act(async () => { releaseLoadMore?.({ entries: [], omitted: 0 }); });
    expect(seen.view?.loadingMore).toBe(false);
  });

  it('keeps rows visible, failed and retryable when a refresh fails', async () => {
    const entry = closedEntry('s1');
    let rejectRefresh: ((error: Error) => void) | undefined;
    const list = vi.fn()
      .mockResolvedValueOnce({ entries: [entry], omitted: 1, next_before: 'older' })
      .mockImplementationOnce(() => new Promise<SessionLedgerPage>((_resolve, reject) => {
        rejectRefresh = reject;
      }));
    const seen = renderLedger(list);
    await waitFor(() => expect(seen.view?.entries).toEqual([entry]));
    act(() => seen.emit(resolved('s1', verdict({ reason: 'ready' }))));
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('ready'));

    act(() => seen.view?.reload());
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));
    expect(seen.view?.entries).toEqual([entry]);
    await act(async () => rejectRefresh?.(new Error('connection lost')));

    await waitFor(() => expect(seen.view?.error).toBe('connection lost'));
    expect(seen.view?.entries).toEqual([entry]);
    expect(seen.view?.resolutions.s1).toEqual({ closedAt: entry.closed_at, state: 'failed', error: 'connection lost' });
    expect(seen.view?.omitted).toBe(0);
    act(() => seen.view?.loadMore());
    expect(list).toHaveBeenCalledTimes(2);
  });

  it('does not load an old cursor while a replacement page is pending', async () => {
    const entry = closedEntry('s1');
    let releaseRefresh: ((page: SessionLedgerPage) => void) | undefined;
    const list = vi.fn()
      .mockResolvedValueOnce({ entries: [entry], omitted: 1, next_before: 'older' })
      .mockImplementationOnce(() => new Promise<SessionLedgerPage>((resolve) => {
        releaseRefresh = resolve;
      }));
    const seen = renderLedger(list);
    await waitFor(() => expect(seen.view?.loading).toBe(false));

    act(() => seen.view?.reload());
    await waitFor(() => expect(seen.view?.loading).toBe(true));
    act(() => seen.view?.loadMore());

    expect(list).toHaveBeenCalledTimes(2);
    await act(async () => releaseRefresh?.({ entries: [entry], omitted: 0 }));
    await waitFor(() => expect(seen.view?.loading).toBe(false));
  });

  it('ignores a response from the connection superseded by reconnect', async () => {
    const oldEntry = closedEntry('old');
    const freshEntry = closedEntry('fresh');
    const releases: Array<(page: SessionLedgerPage) => void> = [];
    const list = vi.fn(() => new Promise<SessionLedgerPage>((resolve) => { releases.push(resolve); }));
    const seen = renderLedger(list);
    await waitFor(() => expect(releases).toHaveLength(1));

    act(() => seen.setConnected(false));
    act(() => seen.setConnected(true, 2));
    await waitFor(() => expect(releases).toHaveLength(2));
    await act(async () => releases[1]?.({ entries: [freshEntry], omitted: 0 }));
    await waitFor(() => expect(seen.view?.entries).toEqual([freshEntry]));
    await act(async () => releases[0]?.({ entries: [oldEntry], omitted: 0 }));

    expect(seen.view?.entries).toEqual([freshEntry]);
  });

  it('settles a listed row that closes after a past range instead of leaving it without a verdict', async () => {
    const live: SessionLedgerEntry = {
      ...closedEntry('s1'),
      closed_at: undefined,
      closed_by: undefined,
      last_seen: new Date(NOW.getTime() - 26 * 3600e3).toISOString(),
    };
    const seen = renderLedger(async () => ({ entries: [live], omitted: 0 }), {
      ...EMPTY_SESSION_FILTERS, scope: 'all', range: 'yesterday',
    });
    await waitFor(() => expect(seen.view?.entries.map((entry) => entry.id)).toEqual(['s1']));

    const closedAt = '2026-09-05T14:00:00Z';
    act(() => seen.emit({ type: 'closed', entry: { ...live, closed_at: closedAt, closed_by: 'user' } }));
    expect(seen.view?.resolutions.s1).toEqual({ closedAt, state: 'pending' });
    act(() => seen.emit(resolved('s1', verdict({ reason: 'back' }), closedAt)));
    expect(seen.view?.resolutions.s1?.state).toBe('ready');
  });

  it('turns rows a failed read left pending into retryable failures', async () => {
    let fail = false;
    const entry = closedEntry('s1');
    const seen = renderLedger(async () => {
      if (fail) throw new Error('timeout');
      return { entries: [entry], omitted: 0 };
    });
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));

    fail = true;
    await act(async () => { seen.view?.reload(); });
    await waitFor(() => expect(seen.view?.error).toBe('timeout'));
    expect(seen.view?.entries.map((row) => row.id)).toEqual(['s1']);
    expect(seen.view?.resolutions.s1).toEqual({ closedAt: entry.closed_at, state: 'failed', error: 'timeout' });
  });

  it('does not mark rows pending while the filter is invalid and no read is issued', async () => {
    const list = vi.fn(async () => ({ entries: [closedEntry('s1')], omitted: 0 }));
    const seen = renderLedger(list);
    await waitFor(() => expect(seen.view?.resolutions.s1?.state).toBe('pending'));
    act(() => seen.emit(resolved('s1', verdict({ reason: 'ok' }))));
    expect(seen.view?.resolutions.s1?.state).toBe('ready');

    await act(async () => {
      seen.view?.setFilters((filters) => ({ ...filters, range: 'custom', customFrom: 'not a date', customTo: '' }));
    });
    expect(seen.view?.filterError).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
    expect(seen.view?.resolutions.s1?.state).toBe('ready');
  });
});
