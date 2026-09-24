import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { SessionLedgerPage } from '../../hooks/daemonSessionLedgerEvents';
import { SessionReopenRefusal } from '../../hooks/daemonSessionLedgerEvents';
import type { SessionReopen } from '../../types/generated';
import { SessionReopenAction } from '../../types/generated';
import { closedEntry, entry, liveEntry, verdict } from '../../test/sessionLedgerFixtures';
import { listing, page, renderSessionsTab, rows } from './testSupport';

const row = (label: string) => rows().getByText(label).closest('.ledger-row') as HTMLElement;
const inspector = () => screen.getByRole('complementary', { name: 'Details' });
const goneEverywhere = verdict({
  reopenable: false,
  reason: 'the directory is gone; branch feat/x is gone from this repository and its remotes',
  directory_state: 'missing',
  branch_state: 'gone',
  workspace_plan: 'create',
  actions: [SessionReopenAction.StartFreshDefaultBranch, SessionReopenAction.StartFreshElsewhere],
});

function refusal(offer: SessionReopen) {
  const offered = offer.actions.join(', ');
  const message = offered
    ? `11111111-2222-3333-4444-555555555555 cannot be reopened with reopen: ${offer.reason}. Offered instead: ${offered}`
    : `11111111-2222-3333-4444-555555555555 cannot be reopened: ${offer.reason}`;
  return new SessionReopenRefusal(message, offer);
}

async function refuseFirstReopen(label: string) {
  await rows().findByText(label);
  fireEvent.click(within(row(label)).getByRole('button', { name: 'Reopen' }));
  await within(row(label)).findByRole('status');
}

describe('SessionsTab reopens on demand', () => {
  it('offers Reopen on every closed row and Focus on a live one without asking for eligibility', async () => {
    const { list, calls } = listing([page({ entries: [closedEntry('s1'), closedEntry('s2'), liveEntry('s3')] })]);
    renderSessionsTab({ listSessions: list, onReopen: vi.fn() });

    await rows().findByText('run s1');
    expect(within(row('run s1')).getByRole('button', { name: 'Reopen' })).toBeTruthy();
    expect(within(row('run s2')).getByRole('button', { name: 'Reopen' })).toBeTruthy();
    expect(within(row('run s3')).getByRole('button', { name: 'Focus' })).toBeTruthy();
    expect(within(row('run s3')).queryByRole('button', { name: 'Reopen' })).toBeNull();
    expect(calls[0]).not.toHaveProperty('reopen');
  });

  it('offers nothing without a hand to run it', async () => {
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    renderSessionsTab({ listSessions: list });

    await rows().findByText('run s1');
    expect(screen.queryByRole('button', { name: 'Reopen' })).toBeNull();
  });

  it('shows the row busy while the daemon works and clears it on success', async () => {
    let finish: () => void = () => {};
    const onReopen = vi.fn(() => new Promise<boolean>((resolve) => { finish = () => resolve(true); }));
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    renderSessionsTab({ listSessions: list, onReopen });

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));
    expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]);
    const busy = within(row('run s1')).getByRole('button', { name: 'reopening…' }) as HTMLButtonElement;
    expect(busy.disabled).toBe(true);
    expect(row('run s1').getAttribute('aria-busy')).toBe('true');

    finish();
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());
    expect((within(row('run s1')).getByRole('button', { name: 'Reopen' }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('turns a refusal into the actions the session offers instead, and reads them in the inspector', async () => {
    const onReopen = vi.fn(async () => { throw refusal(goneEverywhere); });
    const { list } = listing([page({ entries: [closedEntry('s1', { branch: 'feat/x' })] })]);
    renderSessionsTab({ listSessions: list, onReopen, workspaceNames: { 'ws-1': 'attn' } });

    await refuseFirstReopen('run s1');

    const refused = row('run s1');
    expect(within(refused).getByRole('status').textContent).toBe('Reopen was refused; it offers Start fresh on the default branch instead');
    expect(within(refused).getByRole('button', { name: 'Start fresh on the default branch' })).toBeTruthy();
    expect(within(inspector()).getByText('directory is gone')).toBeTruthy();
    expect(within(inspector()).getByText('branch is gone everywhere')).toBeTruthy();
    expect(within(inspector()).getByText('opens a workspace named after the session, in a new pane')).toBeTruthy();
    expect(within(inspector()).getByRole('button', { name: /Start fresh elsewhere/ })).toBeTruthy();
  });

  it('keeps Reopen when the refusal names no offer', async () => {
    const onReopen = vi.fn(async () => { throw new Error('the session changed after this row was listed; reload to see it'); });
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    renderSessionsTab({ listSessions: list, onReopen });

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));

    await waitFor(() => expect(within(row('run s1')).getByRole('status').textContent).toContain('the session changed'));
    expect(within(row('run s1')).getByRole('button', { name: 'Reopen' })).toBeTruthy();
  });

  it('runs an offered action from the menu, with Enter and with a digit', async () => {
    const onReopen = vi.fn()
      .mockRejectedValueOnce(refusal(goneEverywhere))
      .mockResolvedValue(true);
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    renderSessionsTab({ listSessions: list, onReopen });
    await refuseFirstReopen('run s1');

    const first = row('run s1');
    fireEvent.click(within(first).getByRole('button', { name: /More for/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Start fresh elsewhere/ }));
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());
    fireEvent.keyDown(first, { key: 'Enter' });
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());
    fireEvent.keyDown(first, { key: '2' });
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());

    expect(onReopen.mock.calls).toEqual([
      ['s1', 'reopen'],
      ['s1', 'start_fresh_elsewhere'],
      ['s1', 'start_fresh_default_branch'],
      ['s1', 'start_fresh_elsewhere'],
    ]);
  });

  it('forgets a refusal and its offer once the session closes again', async () => {
    const onReopen = vi.fn(async () => { throw refusal(goneEverywhere); });
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen });
    await refuseFirstReopen('run s1');

    act(() => view.emit({ type: 'closed', entry: closedEntry('s1', { closed_at: '2026-09-05T12:00:00Z' }) }));

    await within(row('run s1')).findByRole('button', { name: 'Reopen' });
    expect(screen.queryByRole('status')).toBeNull();
    expect(within(inspector()).queryByText('directory is gone')).toBeNull();
  });

  it('says nothing when the user backs out of the directory picker', async () => {
    const elsewhereOnly = verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [SessionReopenAction.StartFreshElsewhere] });
    const onReopen = vi.fn()
      .mockRejectedValueOnce(refusal(elsewhereOnly))
      .mockResolvedValue(false);
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    renderSessionsTab({ listSessions: list, onReopen });
    await refuseFirstReopen('run s1');

    fireEvent.click(within(row('run s1')).getByRole('button', { name: 'Start fresh elsewhere' }));

    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('replaces a live row when the daemon says it closed, and offers Reopen without re-listing', async () => {
    const { list } = listing([page({ entries: [liveEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen: vi.fn() });

    await screen.findByRole('button', { name: 'Focus' });
    act(() => view.emit({ type: 'closed', entry: closedEntry('s1') }));

    await within(row('run s1')).findByRole('button', { name: 'Reopen' });
    expect(screen.queryByRole('button', { name: 'Focus' })).toBeNull();
    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(row('run s1').getAttribute('data-state')).toBe('closed');
    expect(within(row('run s1')).getByText('closed by you: work finished')).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it('hides stale pagination while replacing the current page', async () => {
    let release: ((page: SessionLedgerPage) => void) | undefined;
    const list = vi.fn()
      .mockResolvedValueOnce(page({ entries: [closedEntry('s1')], next_before: 'older', omitted: 1 }))
      .mockImplementationOnce(() => new Promise<SessionLedgerPage>((resolve) => { release = resolve; }));
    const view = renderSessionsTab({ listSessions: list });
    await rows().findByText('run s1');

    act(() => view.setConnected(true, 2));

    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole('button', { name: '1 older ↓' })).toBeNull();
    await act(async () => release?.(page({ entries: [closedEntry('s1')] })));
  });
});

describe('SessionsTab row grammar', () => {
  it('names the session that closed another, falls back to its id, and says you for the user', async () => {
    const { list } = listing([page({ entries: [
      entry({ id: 'dispatcher', label: 'Ledger work' }),
      closedEntry('delegate', { label: 'Worktree reclaim', closed_by: 'dispatcher', close_reason: 'it went quiet' }),
      closedEntry('orphan', { closed_by: 'sess-off-page', close_reason: 'the run finished' }),
      closedEntry('mine', { close_reason: undefined }),
    ] })]);
    renderSessionsTab({ listSessions: list });

    await rows().findByText('Worktree reclaim');
    expect(within(row('Worktree reclaim')).getByText('closed by Ledger work: it went quiet')).toBeTruthy();
    expect(within(row('run orphan')).getByText('closed by sess-off-page: the run finished')).toBeTruthy();
    expect(within(row('run mine')).getByText('closed by you')).toBeTruthy();
  });

  it('keeps ids off the surface: prose names sessions by title and the row title never falls back to an id', async () => {
    const deadEnd = verdict({ reopenable: false, actions: [], reason: 'conversation 12345678-1234-1234-1234-123456789abc is no longer in storage' });
    const onReopen = vi.fn(async () => { throw refusal(deadEnd); });
    const { list } = listing([page({
      entries: [entry({ id: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee', label: 'Fixture run' }), closedEntry('s2', { label: '', close_reason: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee asked' })],
    })]);
    renderSessionsTab({
      listSessions: list,
      onReopen,
      workspaceNames: { 'ws-1': 'workspace-12345678-1234-1234-1234-123456789abc' },
    });

    await rows().findByText('untitled session');
    expect(within(row('untitled session')).getByText('closed by you: Fixture run asked')).toBeTruthy();
    await refuseFirstReopen('untitled session');
    await within(row('untitled session')).findByText('its conversation is no longer in storage');
    expect(rows().queryByText(/12345678-1234/)).toBeNull();
  });

  it('shows a worktree verb until a refusal says the directory is gone, and copies the path with y', async () => {
    const onShowWorktree = vi.fn();
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const onReopen = vi.fn(async (sessionId: string) => {
      if (sessionId === 'gone') throw refusal(verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [] }));
      return true;
    });
    const { list } = listing([page({
      entries: [closedEntry('here', { is_worktree: true }), closedEntry('gone', { is_worktree: true })],
    })]);
    renderSessionsTab({ listSessions: list, onReopen, onShowWorktree });

    await rows().findByText('run gone');
    expect(row('run here').getAttribute('data-verbs')).toContain('Show worktree');
    expect(row('run gone').getAttribute('data-verbs')).toContain('Show worktree');
    await refuseFirstReopen('run gone');
    await waitFor(() => expect(row('run gone').getAttribute('data-verbs')).not.toContain('Show worktree'));

    fireEvent.keyDown(row('run here'), { key: '2' });
    expect(onShowWorktree).toHaveBeenCalledWith('/Users/victor/projects/attn');
    fireEvent.keyDown(row('run here'), { key: 'y' });
    expect(writeText).toHaveBeenCalledWith('/Users/victor/projects/attn');
  });
});
