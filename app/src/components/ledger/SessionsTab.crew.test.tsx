import { crewMember } from '../../test/daemonFixtures';
import { gesture } from '../../test/renderApp';
import { expect, it } from 'vitest';
import { entry, closedEntry } from '../../test/sessionLedgerFixtures';
import { openSessionsLedger, page, pages, rows } from './testSupport';

it('lists the latest crew day once and refreshes it when an earlier day closes', async () => {
 const view = await openSessionsLedger(pages([
  page({ entries: [entry({ id: 'old', member_key: 'keel', member_name: 'Alfred' })] }),
  page({ entries: [entry({ id: 'new', member_key: 'keel', member_name: 'Alfred', branch: 'successor-branch' })] }),
 ]));
 expect(rows().getByText('Alfred')).toBeInTheDocument();
 await view.closed(closedEntry('old', { member_key: 'keel', member_name: 'Alfred' }));
 expect(rows().getAllByText('Alfred')).toHaveLength(1);
 expect(view.queries()).toHaveLength(2);
 expect(rows().getByText('successor-branch')).toBeInTheDocument();
});

it('updates the open ledger when a crew member is renamed', async () => {
 const view = await openSessionsLedger(pages([
  page({ entries: [entry({ id: 'day', member_key: 'keel', member_name: 'Keel' })] }),
 ]), { initialState: { crew: [crewMember('keel')] } });
 expect(rows().getByText('Keel')).toBeInTheDocument();
 await gesture(view.daemon, () => view.daemon.emit({ event: 'crew_updated', profile_id: 'profile-default', members: [crewMember('keel', { name: 'Alfred' })] }));
 expect(rows().queryByText('Keel')).toBeNull();
 expect(rows().getByText('Alfred')).toBeInTheDocument();
 expect(view.queries()).toHaveLength(2);
});

it('refreshes foreign-profile member names on the scoped roster snapshot', async () => {
 const view = await openSessionsLedger(pages([
  page({ entries: [entry({ id: 'bob-day', member_key: 'bob', member_name: 'Bob', profile_id: 'side', profile_name: 'Side' })] }),
  page({ entries: [entry({ id: 'bob-day', member_key: 'bob', member_name: 'Robert', profile_id: 'side', profile_name: 'Side' })] }),
 ]), { initialState: { crew: [crewMember('keel')] } });
 expect(rows().getByText('Bob')).toBeInTheDocument();
 await gesture(view.daemon, () => view.daemon.emit({ event: 'crew_updated', profile_id: 'profile-default', members: [crewMember('keel')] }));
 expect(rows().queryByText('Bob')).toBeNull();
 expect(rows().getByText('Robert')).toBeInTheDocument();
 expect(view.queries()).toHaveLength(2);
});
