import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSessionsLedger, page, rows } from './components/ledger/testSupport';
import { daemonSession } from './test/daemonFixtures';
import { closedEntry, entry } from './test/sessionLedgerFixtures';

const listed = () => rows().queryAllByRole('option').map((option) => option.querySelector('.ledger-row-title')?.textContent);

describe('App shell names in the ledger', () => {
  it('keeps open rows, details and search current, then preserves the closed name', async () => {
    const shell = daemonSession('shell', { agent: 'shell', label: '', title: 'shop' });
    const view = await openSessionsLedger(() => page({ entries: [entry({ id: shell.id, agent: 'shell', label: 'shop' })] }), {
      initialState: { sessions: [shell] },
    });
    const change = async (label: string, title: string) => {
      view.daemon.emit({ event: 'session_state_changed', session: { ...shell, label, title } });
      await view.daemon.idle();
    };
    const filter = (text: string) => fireEvent.change(screen.getByLabelText('Filter'), { target: { value: text } });
    expect(listed()).toEqual(['shop']);

    await change('', 'nvim');
    expect(listed()).toEqual(['nvim']);
    expect(within(screen.getByRole('complementary', { name: 'Details' })).getByText('nvim')).toBeInTheDocument();
    filter('nvim');
    expect(listed()).toEqual(['nvim']);
    await change('', 'warehouse');
    expect(listed()).toEqual([]);
    filter('');
    expect(listed()).toEqual(['warehouse']);

    await change('my server', 'warehouse');
    expect(listed()).toEqual(['my server']);
    await change('', 'runner');
    expect(listed()).toEqual(['runner']);

    await view.closed(closedEntry(shell.id, { agent: 'shell', label: 'runner' }));
    await change('', 'late title');
    expect(listed()).toEqual(['runner']);
    expect(view.queries()).toHaveLength(1);
  });
});
