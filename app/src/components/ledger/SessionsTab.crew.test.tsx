import { expect, it } from 'vitest';
import { entry, closedEntry } from '../../test/sessionLedgerFixtures';
import { openSessionsLedger, page, pages, rows } from './testSupport';

it('lists the latest crew day once and refreshes it when an earlier day closes', async () => {
 const view = await openSessionsLedger(pages([
  page({ entries: [entry({ id: 'old', member_key: 'keel', member_name: 'Alfred' })] }),
  page({ entries: [entry({ id: 'new', member_key: 'keel', member_name: 'Alfred' })] }),
 ]));
 expect(rows().getByText('Alfred')).toBeInTheDocument();
 await view.closed(closedEntry('old', { member_key: 'keel', member_name: 'Alfred' }));
 expect(rows().getAllByText('Alfred')).toHaveLength(1);
 expect(view.queries()).toHaveLength(2);
});
