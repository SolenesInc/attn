import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen } from '@testing-library/react';
import { SESSION_FILTERS_SETTING_KEY } from '../../hooks/sessionFiltersSetting';
import { NOW, entry } from '../../test/sessionLedgerFixtures';
import { savedSettings, serveSettings } from '../../test/settings';
import type { ScriptedDaemon } from '../../test/scriptedDaemon';
import { namedWorkspaces, openSessionsLedger, page, pages, rows, type LedgerAnswer } from './testSupport';

const query = () => screen.getByLabelText('Filter') as HTMLInputElement;
const type = (text: string) => fireEvent.change(query(), { target: { value: text } });
const status = () => document.querySelector('.ledger-status-left')?.textContent ?? '';
const rememberedFilters = (daemon: ScriptedDaemon) =>
  savedSettings(daemon).filter(([key]) => key === SESSION_FILTERS_SETTING_KEY).map(([, value]) => JSON.parse(value));

async function openLedger(
  { answer, workspaceNames = {} }: { answer: LedgerAnswer; workspaceNames?: Record<string, string> },
  { values = {} }: { values?: Record<string, string> } = {},
) {
  const view = await openSessionsLedger(answer, { initialState: { workspaces: namedWorkspaces(workspaceNames), settings: values } });
  serveSettings(view.daemon, values);
  return view;
}

async function typeAndPause(view: { daemon: ScriptedDaemon }, text: string) {
  type(text);
  await act(() => vi.advanceTimersByTimeAsync(1000));
  await view.daemon.idle();
}

describe('SessionsTab query', () => {
  it('asks for both live and closed rows, newest page first', async () => {
    const view = await openLedger({ answer: pages([page({ entries: [entry({ id: 's1' })] })]) });

    expect(rows().getByText('run s1')).toBeInTheDocument();
    expect(view.queries()).toEqual([{ all: true, limit: 50 }]);
  });

  it('narrows to closed rows without re-reading on every render', async () => {
    const view = await openLedger({ answer: pages([page()]) });
    expect(view.queries()).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
    await view.daemon.idle();
    expect(view.queries()).toEqual([{ all: true, limit: 50 }, { closed: true, limit: 50 }]);
    await view.daemon.idle();
    expect(view.queries()).toHaveLength(2);
  });

  it('resolves range words into instants in the viewer timezone', async () => {
    const view = await openLedger({ answer: pages([page()]) });

    await typeAndPause(view, 'today');
    const today = new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate()).toISOString();
    expect(view.queries()[1]).toEqual({ all: true, limit: 50, since: today });

    await typeAndPause(view, 'yesterday');
    const yesterday = new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() - 1).toISOString();
    expect(view.queries()[2]).toEqual({ all: true, limit: 50, since: yesterday, until: today });

    await typeAndPause(view, 'week');
    expect(view.queries()[3].since).toBe(new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() - 6).toISOString());
    expect(view.queries()[3].until).toBeUndefined();
  });

  it('counts both ends of a custom range and refuses a backwards one', async () => {
    const view = await openLedger({ answer: pages([page()]) });

    await typeAndPause(view, 'from:2026-09-01 to:2026-09-03');
    expect(view.queries()[1]).toEqual({
      all: true,
      limit: 50,
      since: new Date(2026, 8, 1).toISOString(),
      until: new Date(2026, 8, 4).toISOString(),
    });

    await typeAndPause(view, 'from:2026-09-01 to:2026-08-01');
    expect(rows().getByText('The range ends before it starts; swap the two dates')).toBeInTheDocument();
    expect(view.queries()).toHaveLength(2);
  });

  it('keeps a scope click and a typed range made in one tick', async () => {
    const view = await openLedger({ answer: pages([page()]) });

    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
    await typeAndPause(view, 'today');

    const last = view.queries()[view.queries().length - 1];
    expect(last.closed).toBe(true);
    expect(last.since).toBeTruthy();
  });

  it('resolves repo: and ws: through the facets and flags a token nothing matches', async () => {
    const view = await openLedger({
      answer: pages([page({
        entries: [entry({ id: 's1' })],
        facets: {
          workspaces: [{ value: 'ws-1', count: 4 }],
          repositories: [{ value: '/Users/victor/projects/attn', count: 7 }],
        },
      })]),
      workspaceNames: { 'ws-1': 'attn work' },
    });

    await typeAndPause(view, 'repo:attn ws:attn-work');
    expect(view.queries()[1]).toEqual({ all: true, limit: 50, repository: '/Users/victor/projects/attn', workspace_id: 'ws-1' });

    await typeAndPause(view, 'repo:nope');
    const chip = screen.getByRole('button', { name: /repo:nope/ });
    expect(chip.className).toContain('is-unresolved');
    fireEvent.click(chip);
    expect(query().value).toBe('');
  });

  it('narrows the page by words and dir: without asking the daemon', async () => {
    const view = await openLedger({ answer: pages([page({ entries: [
      entry({ id: 's1', label: 'ledger work', directory: '/Users/victor/projects/attn--wt' }),
      entry({ id: 's2', label: 'other thing', directory: '/Users/victor/projects/elsewhere' }),
    ] })]) });

    await typeAndPause(view, 'ledger');
    expect(rows().queryByText('other thing')).toBeNull();
    expect(rows().getByText('ledger work')).toBeTruthy();
    expect(status()).toContain('1 hidden by the query');

    await typeAndPause(view, 'dir:~/projects/elsewhere');
    expect(rows().queryByText('ledger work')).toBeNull();
    expect(rows().getByText('other thing')).toBeTruthy();
    expect(view.queries()).toHaveLength(1);
  });
});

describe('SessionsTab pagination', () => {
  it('loads the next page from the cursor and appends it', async () => {
    const view = await openLedger({ answer: pages([
      page({ entries: [entry({ id: 's1' })], omitted: 3, next_before: 's1' }),
      page({ entries: [entry({ id: 's2' })], omitted: 0 }),
    ]) });
    expect(status()).toContain('3 older');

    fireEvent.click(screen.getByRole('button', { name: /3 older/ }));
    await view.daemon.idle();

    expect(rows().getByText('run s2')).toBeInTheDocument();
    expect(view.queries()[1]).toEqual({ all: true, limit: 50, before: 's1' });
    expect(rows().getByText('run s1')).toBeTruthy();
    expect(status()).toContain('2 sessions');
    expect(screen.queryByRole('button', { name: /older/ })).toBeNull();
  });
});

describe('SessionsTab filter memory', () => {
  const stored = JSON.stringify({
    scope: 'closed', range: '7d', customFrom: '', customTo: '', workspaceId: 'ws-2', repository: '/Users/victor/projects/attn',
  });

  it('queries with the remembered filters on the first read and shows them as tokens', async () => {
    const view = await openLedger(
      { answer: pages([page()]), workspaceNames: { 'ws-2': 'attn' } },
      { values: { [SESSION_FILTERS_SETTING_KEY]: stored } },
    );

    const since = new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() - 6).toISOString();
    expect(view.queries()).toEqual([{ closed: true, since, workspace_id: 'ws-2', repository: '/Users/victor/projects/attn', limit: 50 }]);
    expect(query().value).toBe('repo:attn ws:attn 7d');
    expect(rememberedFilters(view.daemon)).toEqual([]);
  });

  it('keeps the remembered path when repository names collide', async () => {
    const view = await openLedger(
      { answer: pages([page({
        facets: {
          workspaces: [],
          repositories: [
            { value: '/tmp/earlier/attn', count: 4 },
            { value: '/Users/victor/projects/attn', count: 3 },
          ],
        },
      })]) },
      { values: { [SESSION_FILTERS_SETTING_KEY]: stored } },
    );

    expect(view.queries()).toHaveLength(1);
    expect(view.queries()[0].repository).toBe('/Users/victor/projects/attn');
    expect(query().value).toContain('repo:attn');
    expect(rememberedFilters(view.daemon)).toEqual([]);
  });

  it('keeps remembered facet filters while the first page is loading', async () => {
    const view = await openLedger(
      { answer: () => 'hold', workspaceNames: { 'ws-2': 'attn' } },
      { values: { [SESSION_FILTERS_SETTING_KEY]: stored } },
    );

    expect(query().value).toBe('repo:attn ws:attn 7d');
    expect(rememberedFilters(view.daemon)).toEqual([]);
  });

  it('restores a custom range exactly as it was left', async () => {
    const view = await openLedger({ answer: pages([page()]) }, { values: { [SESSION_FILTERS_SETTING_KEY]: JSON.stringify({
      scope: 'all', range: 'custom', customFrom: '2026-08-01', customTo: '2026-08-03', workspaceId: '', repository: '',
    }) } });

    expect(view.queries()).toHaveLength(1);
    expect(view.queries()[0].since).toBe(new Date(2026, 7, 1).toISOString());
    expect(view.queries()[0].until).toBe(new Date(2026, 7, 4).toISOString());
    expect(query().value).toBe('from:2026-08-01 to:2026-08-03');
  });

  it('remembers a filter the moment it changes', async () => {
    const view = await openLedger({ answer: pages([page()]) });
    const written = () => rememberedFilters(view.daemon);

    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
    await view.daemon.idle();
    expect(written()).toEqual([{ scope: 'closed', range: 'any', customFrom: '', customTo: '', workspaceId: '', repository: '' }]);

    await typeAndPause(view, '30d');
    expect(written()).toHaveLength(2);
    expect(written()[1].range).toBe('30d');
  });

  it.each([
    ['not JSON', '{scope: closed}'],
    ['an unknown scope', JSON.stringify({ scope: 'archived', range: 'any' })],
    ['an unreadable date', JSON.stringify({ scope: 'all', range: 'custom', customFrom: 'yesterday' })],
  ])('opens on the defaults when the setting is %s', async (_label, value) => {
    const view = await openLedger({ answer: pages([page()]) }, { values: { [SESSION_FILTERS_SETTING_KEY]: value } });
    expect(view.queries()).toEqual([{ all: true, limit: 50 }]);
  });
});
