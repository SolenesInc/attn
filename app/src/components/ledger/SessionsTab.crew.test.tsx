import { expect, it } from 'vitest';
import { crewMember } from '../../test/daemonFixtures';
import { gesture } from '../../test/renderApp';
import { entry } from '../../test/sessionLedgerFixtures';
import { openSessionsLedger, page, pages, rows } from './testSupport';

it('names a member row after the member, and follows a rename without asking again', async () => {
  const view = await openSessionsLedger(pages([
    page({ entries: [entry({ id: 'day', label: 'Keel', member_key: 'keel', member_name: 'Keel' })] }),
  ]), { initialState: { crew: [crewMember('keel')] } });
  expect(rows().getByText('Keel')).toBeInTheDocument();
  await gesture(view.daemon, () => view.daemon.emit({ event: 'crew_updated', members: [crewMember('keel', { name: 'Alfred' })] }));
  expect(rows().queryByText('Keel')).toBeNull();
  expect(rows().getByText('Alfred')).toBeInTheDocument();
  expect(view.queries()).toHaveLength(1);
});
