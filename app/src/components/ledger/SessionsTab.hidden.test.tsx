import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSession } from '../../test/daemonFixtures';
import { entry } from '../../test/sessionLedgerFixtures';
import { openSessionsLedger, page, pages, rows } from './testSupport';

const row = (label: string) => rows().getByText(label).closest('.ledger-row') as HTMLElement;
const inspector = () => screen.getByRole('complementary', { name: 'Details' });

describe('SessionsTab hidden sessions', () => {
  it('marks a live hidden session on its row and in the inspector, and no other', async () => {
    await openSessionsLedger(
      pages([page({ entries: [entry({ id: 's1', hidden: true }), entry({ id: 's2' })] })]),
      { initialState: { sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle' })) } },
    );

    expect(row('run s1')).toHaveAttribute('data-hidden', 'true');
    expect(within(row('run s1')).getByText('hidden')).toBeInTheDocument();
    expect(row('run s2')).toHaveAttribute('data-hidden', 'false');
    expect(within(row('run s2')).queryByText('hidden')).toBeNull();
    expect(within(inspector()).getByText(/hidden/)).toBeInTheDocument();
  });
});
