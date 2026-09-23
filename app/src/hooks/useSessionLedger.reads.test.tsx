import { useEffect } from 'react';
import { act, render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { SessionLedgerPage, SessionLedgerQuery } from './daemonSessionLedgerEvents';
import { EMPTY_SESSION_FILTERS, useSessionLedger } from './useSessionLedger';
import type { SessionLedgerFilters, SessionLedgerView } from './useSessionLedger';
import { createSessionLedgerTestConnection } from '../test/sessionLedgerTestConnection';
import { closedEntry, now } from '../test/sessionLedgerFixtures';

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

describe('useSessionLedger reads', () => {
  it('re-reads the page after a reconnect', async () => {
    const entry = closedEntry('s1');
    const list = vi.fn(async () => ({ entries: [entry], omitted: 0 }));
    const seen = renderLedger(list);
    await waitFor(() => expect(seen.view?.entries).toEqual([entry]));

    act(() => seen.setConnected(true, 2));

    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    expect(seen.view?.entries).toEqual([entry]);
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

  it('keeps the rows of the same query when its refresh fails', async () => {
    const entry = closedEntry('s1');
    const list = vi.fn()
      .mockResolvedValueOnce({ entries: [entry], omitted: 1, next_before: 'older' })
      .mockRejectedValueOnce(new Error('connection lost'));
    const seen = renderLedger(list);
    await waitFor(() => expect(seen.view?.entries).toEqual([entry]));

    await act(async () => { seen.view?.reload(); });

    await waitFor(() => expect(seen.view?.error).toBe('connection lost'));
    expect(seen.view?.entries).toEqual([entry]);
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

  it('shows no rows for filters whose read failed', async () => {
    const listed = closedEntry('s1', { repository: 'first' });
    const list = vi.fn(async (query: SessionLedgerQuery) => {
      if (query.repository === 'second') throw new Error('timeout');
      return { entries: [listed], omitted: 0, facets: { repositories: [], workspaces: [] } };
    });
    const seen = renderLedger(list, { ...EMPTY_SESSION_FILTERS, repository: 'first' });
    await waitFor(() => expect(seen.view?.entries.map((row) => row.id)).toEqual(['s1']));

    await act(async () => {
      seen.view?.setFilters((filters) => ({ ...filters, repository: 'second' }));
    });
    await waitFor(() => expect(seen.view?.error).toBe('timeout'));

    expect(seen.view?.entries).toEqual([]);
    expect(seen.view?.facets).toBeNull();
  });

  it('drops a failed read\'s error once filters change while disconnected', async () => {
    const list = vi.fn(async () => { throw new Error('timeout'); });
    const seen = renderLedger(list, { ...EMPTY_SESSION_FILTERS, repository: 'first' });
    await waitFor(() => expect(seen.view?.error).toBe('timeout'));

    act(() => seen.setConnected(false));
    await act(async () => {
      seen.view?.setFilters((filters) => ({ ...filters, repository: 'second' }));
    });

    expect(seen.view?.error).toBeNull();
    expect(seen.view?.entries).toEqual([]);
  });

  it('shows no rows for filters changed while disconnected, and reads them on reconnect', async () => {
    const first = closedEntry('s1', { repository: 'first' });
    const second = closedEntry('s2', { repository: 'second' });
    const list = vi.fn(async (query: SessionLedgerQuery) => ({
      entries: [query.repository === 'second' ? second : first],
      omitted: 0,
    }));
    const seen = renderLedger(list, { ...EMPTY_SESSION_FILTERS, repository: 'first' });
    await waitFor(() => expect(seen.view?.entries).toEqual([first]));

    act(() => seen.setConnected(false));
    await act(async () => {
      seen.view?.setFilters((filters) => ({ ...filters, repository: 'second' }));
    });
    expect(seen.view?.entries).toEqual([]);

    act(() => seen.setConnected(true, 2));
    await waitFor(() => expect(seen.view?.entries).toEqual([second]));
  });
});
