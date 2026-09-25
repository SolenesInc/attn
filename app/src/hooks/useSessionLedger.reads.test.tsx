import { useEffect } from 'react';
import { act } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { EMPTY_SESSION_FILTERS, useSessionLedger } from './useSessionLedger';
import type { SessionLedgerFilters, SessionLedgerView } from './useSessionLedger';
import { page, pages, serveLedger, useLedgerConnection, type LedgerAnswer } from '../components/ledger/testSupport';
import { renderWithDaemon } from '../test/renderApp';
import { closedEntry, now } from '../test/sessionLedgerFixtures';

async function renderLedger(answer: LedgerAnswer, initialFilters: SessionLedgerFilters = EMPTY_SESSION_FILTERS) {
  const seen: { view: SessionLedgerView | null } = { view: null };
  function Harness() {
    const view = useSessionLedger({ enabled: true, connection: useLedgerConnection(), now, initialFilters });
    useEffect(() => { seen.view = view; });
    return null;
  }
  const rendered = await renderWithDaemon();
  const ledger = serveLedger(rendered.daemon, answer);
  rendered.rerender(<Harness />);
  await rendered.daemon.idle();
  const settle = () => rendered.daemon.idle();
  return {
    seen,
    ...ledger,
    settle,
    act: async (gesture: () => void) => {
      act(gesture);
      await settle();
    },
    disconnect: async () => {
      rendered.daemon.disconnect();
      await settle();
    },
    reconnect: async () => {
      await rendered.daemon.reconnect();
      await settle();
    },
  };
}

describe('useSessionLedger reads', () => {
  it('re-reads the page after a reconnect', async () => {
    const entry = closedEntry('s1');
    const ledger = await renderLedger(pages([page({ entries: [entry] })]));
    expect(ledger.seen.view?.entries).toEqual([entry]);

    await ledger.reconnect();

    expect(ledger.queries()).toHaveLength(2);
    expect(ledger.seen.view?.entries).toEqual([entry]);
  });

  it('clears a superseded load-more state after reconnect', async () => {
    const ledger = await renderLedger((query) => (query.before
      ? 'hold'
      : page({ entries: [closedEntry('s1')], omitted: 1, next_before: 'older' })));
    expect(ledger.seen.view?.loading).toBe(false);

    await ledger.act(() => ledger.seen.view?.loadMore());
    expect(ledger.seen.view?.loadingMore).toBe(true);
    await ledger.reconnect();

    expect(ledger.queries()).toHaveLength(3);
    expect(ledger.seen.view?.loadingMore).toBe(false);
  });

  it('keeps the rows of the same query when its refresh fails', async () => {
    const entry = closedEntry('s1');
    const ledger = await renderLedger((_query, index) => (index === 0
      ? page({ entries: [entry], omitted: 1, next_before: 'older' })
      : new Error('connection lost')));
    expect(ledger.seen.view?.entries).toEqual([entry]);

    await ledger.act(() => ledger.seen.view?.reload());

    expect(ledger.seen.view?.error).toBe('connection lost');
    expect(ledger.seen.view?.entries).toEqual([entry]);
    expect(ledger.seen.view?.omitted).toBe(0);
    await ledger.act(() => ledger.seen.view?.loadMore());
    expect(ledger.queries()).toHaveLength(2);
  });

  it('does not load an old cursor while a replacement page is pending', async () => {
    const entry = closedEntry('s1');
    const ledger = await renderLedger((_query, index) => (index === 0
      ? page({ entries: [entry], omitted: 1, next_before: 'older' })
      : 'hold'));
    expect(ledger.seen.view?.loading).toBe(false);

    await ledger.act(() => ledger.seen.view?.reload());
    expect(ledger.seen.view?.loading).toBe(true);
    await ledger.act(() => ledger.seen.view?.loadMore());

    expect(ledger.queries()).toHaveLength(2);
    await ledger.release(0, page({ entries: [entry] }));
    expect(ledger.seen.view?.loading).toBe(false);
  });

  it('keeps the fresh page when the read from the dropped connection never returns', async () => {
    const freshEntry = closedEntry('fresh');
    const ledger = await renderLedger(() => 'hold');
    expect(ledger.queries()).toHaveLength(1);

    await ledger.reconnect();
    expect(ledger.queries()).toHaveLength(2);
    await ledger.release(1, page({ entries: [freshEntry] }));
    expect(ledger.seen.view?.entries).toEqual([freshEntry]);

    await act(() => vi.advanceTimersByTimeAsync(60_000));
    await ledger.settle();

    expect(ledger.seen.view?.entries).toEqual([freshEntry]);
    expect(ledger.seen.view?.error).toBeNull();
  });

  it('shows no rows for filters whose read failed', async () => {
    const listed = closedEntry('s1', { repository: 'first' });
    const ledger = await renderLedger(
      (query) => (query.repository === 'second'
        ? new Error('timeout')
        : page({ entries: [listed], facets: { repositories: [], workspaces: [] } })),
      { ...EMPTY_SESSION_FILTERS, repository: 'first' },
    );
    expect(ledger.seen.view?.entries.map((row) => row.id)).toEqual(['s1']);

    await ledger.act(() => ledger.seen.view?.setFilters((filters) => ({ ...filters, repository: 'second' })));

    expect(ledger.seen.view?.error).toBe('timeout');
    expect(ledger.seen.view?.entries).toEqual([]);
    expect(ledger.seen.view?.facets).toBeNull();
  });

  it('drops a failed read\'s error once filters change while disconnected', async () => {
    const ledger = await renderLedger(() => new Error('timeout'), { ...EMPTY_SESSION_FILTERS, repository: 'first' });
    expect(ledger.seen.view?.error).toBe('timeout');

    await ledger.disconnect();
    await ledger.act(() => ledger.seen.view?.setFilters((filters) => ({ ...filters, repository: 'second' })));

    expect(ledger.seen.view?.error).toBeNull();
    expect(ledger.seen.view?.entries).toEqual([]);
  });

  it('shows no rows for filters changed while disconnected, and reads them on reconnect', async () => {
    const first = closedEntry('s1', { repository: 'first' });
    const second = closedEntry('s2', { repository: 'second' });
    const ledger = await renderLedger(
      (query) => page({ entries: [query.repository === 'second' ? second : first] }),
      { ...EMPTY_SESSION_FILTERS, repository: 'first' },
    );
    expect(ledger.seen.view?.entries).toEqual([first]);

    await ledger.disconnect();
    await ledger.act(() => ledger.seen.view?.setFilters((filters) => ({ ...filters, repository: 'second' })));
    expect(ledger.seen.view?.entries).toEqual([]);

    await ledger.reconnect();
    expect(ledger.seen.view?.entries).toEqual([second]);
  });
});
