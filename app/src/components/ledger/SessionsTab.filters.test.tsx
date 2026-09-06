import { describe, expect, it } from 'vitest';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { SESSION_FILTERS_SETTING_KEY } from '../../hooks/sessionFiltersSetting';
import { NOW, entry, listing, page, renderSessionsTab, rows } from './testSupport';

const query = () => screen.getByLabelText('Filter') as HTMLInputElement;
const type = (text: string) => fireEvent.change(query(), { target: { value: text } });
const status = () => screen.getByTestId('status').textContent ?? '';

describe('SessionsTab query', () => {
  it('asks for both live and closed rows, newest page first', async () => {
    const { list, calls } = listing([page({ entries: [entry({ id: 's1' })] })]);
    renderSessionsTab({ listSessions: list });

    await rows().findByText('run s1');
    expect(calls).toEqual([{ all: true, limit: 50, reopen: true }]);
  });

  it('narrows to closed rows without re-reading on every render', async () => {
    const { list, calls } = listing([page()]);
    renderSessionsTab({ listSessions: list });
    await waitFor(() => expect(calls).toHaveLength(1));

    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]).toEqual({ closed: true, limit: 50, reopen: true });
    await act(async () => { await Promise.resolve(); });
    expect(calls).toHaveLength(2);
  });

  it('resolves range words into instants in the viewer timezone', async () => {
    const { list, calls } = listing([page()]);
    renderSessionsTab({ listSessions: list });
    await waitFor(() => expect(calls).toHaveLength(1));

    type('today');
    await waitFor(() => expect(calls).toHaveLength(2));
    const today = new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate()).toISOString();
    expect(calls[1]).toEqual({ all: true, limit: 50, since: today, reopen: true });

    type('yesterday');
    await waitFor(() => expect(calls).toHaveLength(3));
    const yesterday = new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() - 1).toISOString();
    expect(calls[2]).toEqual({ all: true, limit: 50, since: yesterday, until: today, reopen: true });

    type('week');
    await waitFor(() => expect(calls).toHaveLength(4));
    expect(calls[3].since).toBe(new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() - 6).toISOString());
    expect(calls[3].until).toBeUndefined();
  });

  it('counts both ends of a custom range and refuses a backwards one', async () => {
    const { list, calls } = listing([page()]);
    renderSessionsTab({ listSessions: list });
    await waitFor(() => expect(calls).toHaveLength(1));

    type('from:2026-09-01 to:2026-09-03');
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]).toEqual({
      all: true,
      limit: 50,
      since: new Date(2026, 8, 1).toISOString(),
      until: new Date(2026, 8, 4).toISOString(),
      reopen: true,
    });

    type('from:2026-09-01 to:2026-08-01');
    await rows().findByText('The range ends before it starts; swap the two dates');
    expect(calls).toHaveLength(2);
  });

  it('keeps a scope click and a typed range made in one tick', async () => {
    const { list, calls } = listing([page()]);
    renderSessionsTab({ listSessions: list });
    await waitFor(() => expect(calls).toHaveLength(1));

    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
    type('today');
    await waitFor(() => {
      const last = calls[calls.length - 1];
      expect(last.closed).toBe(true);
      expect(last.since).toBeTruthy();
    });
  });

  it('resolves repo: and ws: through the facets and flags a token nothing matches', async () => {
    const { list, calls } = listing([page({
      entries: [entry({ id: 's1' })],
      facets: {
        workspaces: [{ value: 'ws-1', count: 4 }],
        repositories: [{ value: '/Users/victor/projects/attn', count: 7 }],
      },
    })]);
    renderSessionsTab({ listSessions: list, workspaceNames: { 'ws-1': 'attn work' } });
    await rows().findByText('run s1');

    type('repo:attn ws:attn-work');
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]).toEqual({ all: true, limit: 50, repository: '/Users/victor/projects/attn', workspace_id: 'ws-1', reopen: true });

    type('repo:nope');
    const chip = await screen.findByRole('button', { name: /repo:nope/ });
    expect(chip.className).toContain('is-unresolved');
    fireEvent.click(chip);
    expect(query().value).toBe('');
  });

  it('narrows the page by words and dir: without asking the daemon', async () => {
    const { list, calls } = listing([page({ entries: [
      entry({ id: 's1', label: 'ledger work', directory: '/Users/victor/projects/attn--wt' }),
      entry({ id: 's2', label: 'other thing', directory: '/Users/victor/projects/elsewhere' }),
    ] })]);
    renderSessionsTab({ listSessions: list });
    await rows().findByText('other thing');

    type('ledger');
    await waitFor(() => expect(rows().queryByText('other thing')).toBeNull());
    expect(rows().getByText('ledger work')).toBeTruthy();
    await waitFor(() => expect(status()).toContain('1 hidden by the query'));

    type('dir:/Users/victor/projects/elsewhere');
    await waitFor(() => expect(rows().queryByText('ledger work')).toBeNull());
    expect(rows().getByText('other thing')).toBeTruthy();
    expect(calls).toHaveLength(1);
  });
});

describe('SessionsTab pagination', () => {
  it('loads the next page from the cursor and appends it', async () => {
    const { list, calls } = listing([
      page({ entries: [entry({ id: 's1' })], omitted: 3, next_before: 's1' }),
      page({ entries: [entry({ id: 's2' })], omitted: 0 }),
    ]);
    renderSessionsTab({ listSessions: list });
    await rows().findByText('run s1');

    await waitFor(() => expect(status()).toContain('3 older'));
    fireEvent.click(screen.getByRole('button', { name: /3 older/ }));

    await rows().findByText('run s2');
    expect(calls[1]).toEqual({ all: true, limit: 50, before: 's1', reopen: true });
    expect(rows().getByText('run s1')).toBeTruthy();
    await waitFor(() => expect(status()).toContain('2 sessions'));
    expect(screen.queryByRole('button', { name: /older/ })).toBeNull();
  });
});

describe('SessionsTab filter memory', () => {
  const stored = JSON.stringify({
    scope: 'closed', range: '7d', customFrom: '', customTo: '', workspaceId: 'ws-2', repository: '/Users/victor/projects/attn',
  });

  it('queries with the remembered filters on the first read and shows them as tokens', async () => {
    const { list, calls } = listing([page()]);
    const { setSetting } = renderSessionsTab(
      { listSessions: list, workspaceNames: { 'ws-2': 'attn' } },
      { values: { [SESSION_FILTERS_SETTING_KEY]: stored } },
    );
    await waitFor(() => expect(calls).toHaveLength(1));

    const since = new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() - 6).toISOString();
    expect(calls[0]).toEqual({ closed: true, since, workspace_id: 'ws-2', repository: '/Users/victor/projects/attn', limit: 50, reopen: true });
    expect(query().value).toBe('repo:attn ws:attn 7d');
    expect(setSetting).not.toHaveBeenCalled();
  });

  it('restores a custom range exactly as it was left', async () => {
    const { list, calls } = listing([page()]);
    renderSessionsTab({ listSessions: list }, { values: { [SESSION_FILTERS_SETTING_KEY]: JSON.stringify({
      scope: 'all', range: 'custom', customFrom: '2026-08-01', customTo: '2026-08-03', workspaceId: '', repository: '',
    }) } });
    await waitFor(() => expect(calls).toHaveLength(1));

    expect(calls[0].since).toBe(new Date(2026, 7, 1).toISOString());
    expect(calls[0].until).toBe(new Date(2026, 7, 4).toISOString());
    expect(query().value).toBe('from:2026-08-01 to:2026-08-03');
  });

  it('remembers a filter the moment it changes', async () => {
    const { list, calls } = listing([page()]);
    const { setSetting } = renderSessionsTab({ listSessions: list });
    await waitFor(() => expect(calls).toHaveLength(1));
    const written = () => setSetting.mock.calls.filter(([key]) => key === SESSION_FILTERS_SETTING_KEY).map(([, value]) => JSON.parse(value as string));

    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
    await waitFor(() => expect(written()).toHaveLength(1));
    expect(written()[0]).toEqual({ scope: 'closed', range: 'any', customFrom: '', customTo: '', workspaceId: '', repository: '' });

    type('30d');
    await waitFor(() => expect(written()).toHaveLength(2));
    expect(written()[1].range).toBe('30d');
  });

  it.each([
    ['not JSON', '{scope: closed}'],
    ['an unknown scope', JSON.stringify({ scope: 'archived', range: 'any' })],
    ['an unreadable date', JSON.stringify({ scope: 'all', range: 'custom', customFrom: 'yesterday' })],
  ])('opens on the defaults when the setting is %s', async (_label, value) => {
    const { list, calls } = listing([page()]);
    renderSessionsTab({ listSessions: list }, { values: { [SESSION_FILTERS_SETTING_KEY]: value } });
    await waitFor(() => expect(calls).toEqual([{ all: true, limit: 50, reopen: true }]));
  });
});
