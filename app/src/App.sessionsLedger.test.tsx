import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSessionsLedger, page, pages, rows, type LedgerAnswer } from './components/ledger/testSupport';
import { closedEntry, entry } from './test/sessionLedgerFixtures';

async function openLedger(answer: LedgerAnswer) {
  const view = await openSessionsLedger(answer);
  view.daemon.on('session_reopen', () => ({ event: 'session_reopen_result', success: false, error: 'the session changed after this row was listed' }));
  return {
    ...view,
    refuseReopen: async (label: string) => {
      fireEvent.click(within(rows().getByText(label).closest<HTMLElement>('.ledger-row')!).getByRole('button', { name: 'Reopen' }));
      await view.daemon.idle();
    },
    disconnect: async () => {
      view.daemon.disconnect();
      await view.daemon.idle();
    },
    reconnect: async () => {
      await view.daemon.reconnect();
      await view.daemon.idle();
    },
  };
}

const ledger = () => within(screen.getByRole('dialog', { name: 'Sessions and worktrees' }));
const listed = () => rows().queryAllByRole('option').map((option) => option.querySelector('.ledger-row-title')?.textContent);
const olderButton = () => ledger().queryByRole('button', { name: /older ↓|loading…/ });
const showClosed = () => fireEvent.click(ledger().getByRole('button', { name: 'Closed' }));

describe('App sessions ledger', () => {
  it('re-reads the page after a reconnect', async () => {
    const view = await openLedger(pages([page({ entries: [closedEntry('s1')] })]));
    expect(rows().getByText('run s1')).toBeInTheDocument();

    await view.reconnect();

    expect(view.queries()).toEqual([{ all: true, limit: 50 }, { all: true, limit: 50 }]);
    expect(rows().getByText('run s1')).toBeInTheDocument();
  });

  it('clears a superseded load-more state after reconnect', async () => {
    const view = await openLedger((query) => (query.before
      ? 'hold'
      : page({ entries: [closedEntry('s1')], omitted: 1, next_before: 'older' })));

    fireEvent.click(olderButton()!);
    await view.daemon.idle();
    expect(olderButton()?.textContent).toBe('loading…');
    await view.reconnect();

    expect(view.queries()).toHaveLength(3);
    expect(olderButton()?.textContent).toBe('1 older ↓');
    expect((olderButton() as HTMLButtonElement).disabled).toBe(false);
  });

  it('keeps the rows of the same query when its refresh fails', async () => {
    const view = await openLedger((_query, index) => (index === 0
      ? page({ entries: [closedEntry('s1')], omitted: 1, next_before: 'older' })
      : new Error('connection lost')));
    expect(olderButton()?.textContent).toBe('1 older ↓');

    await view.refuseReopen('run s1');

    expect(view.queries()).toHaveLength(2);
    expect(rows().getByText('run s1')).toBeInTheDocument();
    expect(olderButton()).toBeNull();
  });

  it('does not load an old cursor while a replacement page is pending', async () => {
    const view = await openLedger((_query, index) => (index === 0
      ? page({ entries: [closedEntry('s1')], omitted: 1, next_before: 'older' })
      : 'hold'));

    await view.refuseReopen('run s1');
    expect(view.queries()).toHaveLength(2);
    expect(olderButton()).toBeNull();

    await view.release(0, page({ entries: [closedEntry('s1')], omitted: 1, next_before: 'newer' }));
    fireEvent.click(olderButton()!);
    await view.daemon.idle();

    expect(view.queries().map((query) => query.before)).toEqual([undefined, undefined, 'newer']);
  });

  it('keeps the fresh page when the read from the dropped connection never returns', async () => {
    const view = await openLedger(() => 'hold');
    expect(view.queries()).toHaveLength(1);

    await view.reconnect();
    expect(view.queries()).toHaveLength(2);
    await view.release(1, page());
    expect(rows().getByText('The ledger is empty.')).toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(60_000));
    await view.daemon.idle();

    expect(rows().getByText('The ledger is empty.')).toBeInTheDocument();
  });

  it('shows no rows for filters whose read failed', async () => {
    const view = await openLedger((query) => (query.closed ? new Error('timeout') : page({ entries: [closedEntry('s1')] })));
    expect(rows().getByText('run s1')).toBeInTheDocument();

    showClosed();
    await view.daemon.idle();

    expect(rows().getByText('timeout')).toBeInTheDocument();
    expect(rows().queryAllByRole('option')).toEqual([]);
  });

  it('drops a failed read\'s error once filters change while disconnected', async () => {
    const view = await openLedger(() => new Error('timeout'));
    expect(rows().getByText('timeout')).toBeInTheDocument();

    await view.disconnect();
    showClosed();
    await view.daemon.idle();

    expect(rows().queryByText('timeout')).toBeNull();
    expect(rows().getByText('No closed sessions yet. Closing one records it here.')).toBeInTheDocument();
  });

  it('shows no rows for filters changed while disconnected, and reads them on reconnect', async () => {
    const view = await openLedger((query) => page({ entries: [query.closed ? closedEntry('s2') : entry({ id: 's1' })] }));
    expect(listed()).toEqual(['run s1']);

    await view.disconnect();
    showClosed();
    await view.daemon.idle();
    expect(listed()).toEqual([]);

    await view.reconnect();
    expect(listed()).toEqual(['run s2']);
    expect(view.queries()).toEqual([{ all: true, limit: 50 }, { closed: true, limit: 50 }]);
  });

  it('places a close by the filters on screen', async () => {
    const view = await openLedger((query) => page({ entries: query.closed ? [] : [entry({ id: 's1' })] }));
    fireEvent.click(ledger().getByRole('button', { name: 'Live' }));
    await view.daemon.idle();
    expect(listed()).toEqual(['run s1']);

    await view.closed(closedEntry('s1'));
    expect(listed()).toEqual([]);

    showClosed();
    await view.daemon.idle();
    await view.closed(closedEntry('s2'));
    expect(listed()).toEqual(['run s2']);
  });
});
