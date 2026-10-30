import { expect, it } from 'vitest';
import { fireEvent, within } from '@testing-library/react';
import { closedEntry } from '../../test/sessionLedgerFixtures';
import { openSessionsLedger, page, pages, rows } from './testSupport';

it('shows a closed session’s outpost and sends its session identity when resuming', async () => {
  const view = await openSessionsLedger(pages([page({ entries: [closedEntry('remote', { host: 'host-linux' })] })]), {
    initialState: { outposts: [{ id: 'host-linux', name: 'Linux studio', destination: 'studio:2200', state: 'connected', reach: 'reachable', state_since: '2026-01-01T00:00:00Z' }] },
  });
  const row = rows().getByText('run remote').closest('.ledger-row') as HTMLElement;
  expect(within(row).getByText('Linux studio')).toBeInTheDocument();
  expect(within(row).queryByRole('button', { name: /Keep conversation/ })).not.toBeInTheDocument();
  view.daemon.on('session_reopen', () => ({ event: 'session_reopen_result', success: false, error: 'Linux studio is unreachable; this needs it now' }));
  fireEvent.click(within(row).getByRole('button', { name: 'Reopen' }));
  await view.daemon.idle();
  expect(view.daemon.sentOf('session_reopen').map(({ session_id, action }) => ({ session_id, action }))).toEqual([{ session_id: 'remote', action: 'reopen' }]);
  expect(view.queries()).toEqual([{ all: true, limit: 50 }, { all: true, limit: 50 }]);
  expect(within(row).getByRole('status')).toHaveTextContent('Linux studio is unreachable');
});

it('keeps the recorded host visible after its outpost was removed', async () => {
  const view = await openSessionsLedger(pages([page({ entries: [closedEntry('remote', { host: 'removed-linux' })] })]));
  expect(rows().getByText('removed-linux')).toBeInTheDocument();
  expect(view.queries()).toHaveLength(1);
  expect(view.daemon.sentOf('session_reopen')).toEqual([]);
});
