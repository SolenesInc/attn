import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { SessionLedgerPage } from '../../hooks/daemonSessionLedgerEvents';
import { SessionReopenAction } from '../../types/generated';
import { closedEntry, entry, liveEntry, resolved, unresolvable, verdict } from '../../test/sessionLedgerFixtures';
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

describe('SessionsTab verdicts', () => {
  it('settles every closed row and reads the verdict in the inspector', async () => {
    const { list, calls } = listing([page({
      entries: [closedEntry('s1'), closedEntry('s2'), liveEntry('s3')],
    })]);
    const view = renderSessionsTab({ listSessions: list, onReopen: vi.fn() });
    act(() => {
      view.emit(resolved('s1', verdict({ reason: 'the worktree is still there' })));
      view.emit(resolved('s2', verdict({ reopenable: false, reason: 'the worktree is gone', actions: [] })));
    });

    await rows().findByText('run s1');
    await within(inspector()).findByText('the worktree is still there');
    // A dead end says so on the row; a verdict with a verb does not repeat itself there.
    expect(within(row('run s2')).getByText('the worktree is gone')).toBeTruthy();
    expect(within(row('run s1')).queryByText('the worktree is still there')).toBeNull();
    expect(within(row('run s1')).getByRole('button', { name: 'Reopen' })).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
    expect(calls[0].reopen).toBe(true);
  });

  it('shows a row immediately, then settles its eligibility in place', async () => {
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen: vi.fn() });

    await rows().findByText('run s1');
    expect(within(inspector()).getByText('checking reopen eligibility…')).toBeTruthy();
    expect(row('run s1').querySelector('.ledger-glyph.is-refreshing')).toBeTruthy();
    expect(within(row('run s1')).queryByRole('button', { name: 'Reopen' })).toBeNull();

    act(() => view.emit(resolved('s1', verdict({ reason: 'its branch is still here' }))));

    await waitFor(() => expect(within(inspector()).queryByText('checking reopen eligibility…')).toBeNull());
    expect(within(inspector()).getByText('its branch is still here')).toBeTruthy();
    expect(within(row('run s1')).getByRole('button', { name: 'Reopen' })).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it('offers no action while eligibility is pending', async () => {
    const onReopen = vi.fn();
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    renderSessionsTab({ listSessions: list, onReopen });

    await rows().findByText('run s1');
    expect(within(row('run s1')).queryByRole('button')).toBeNull();
    expect(onReopen).not.toHaveBeenCalled();
  });

  it('shows a terminal failure and reloads the page on request', async () => {
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list });

    await rows().findByText('run s1');
    act(() => view.emit(unresolvable('s1', 'git unavailable')));

    await within(inspector()).findByText('Eligibility could not be checked.');
    expect(within(inspector()).getByText('git unavailable')).toBeTruthy();
    fireEvent.click(within(inspector()).getByRole('button', { name: 'Reload' }));
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    await within(inspector()).findByText('checking reopen eligibility…');
  });

  it('runs a settled action straight away and offers none without a hand to run it', async () => {
    const onReopen = vi.fn(async () => true);
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen });
    act(() => view.emit(resolved('s1')));

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));
    expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]);
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());

    view.unmount();
    const withoutHand = renderSessionsTab({ listSessions: list });
    act(() => withoutHand.emit(resolved('s1')));
    await rows().findByText('run s1');
    expect(screen.queryByRole('button', { name: 'Reopen' })).toBeNull();
  });

  it('never judges a live row', async () => {
    const { list } = listing([page({ entries: [liveEntry('s1')] })]);
    renderSessionsTab({ listSessions: list, onReopen: vi.fn() });

    await rows().findByText('run s1');
    expect(row('run s1').getAttribute('data-state')).toBe('idle');
    expect(within(inspector()).queryByText('Reopen')).toBeNull();
  });
});

describe('SessionsTab settles rows in place', () => {
  it('does not replay retained verdicts when a fresh resolution arrives after reload', async () => {
    const entries = [closedEntry('s1'), closedEntry('s2')];
    const { list } = listing([page({ entries }), page({ entries })]);
    const view = renderSessionsTab({ listSessions: list });
    act(() => {
      view.emit(resolved('s1', verdict({ reason: 'old s1 verdict' })));
      view.emit(resolved('s2', verdict({ reason: 'old s2 verdict' })));
    });

    fireEvent.click(await rows().findByText('run s1'));
    await within(inspector()).findByText('old s1 verdict');
    act(() => view.emit(unresolvable('s1', 'old failure')));
    fireEvent.click(await within(inspector()).findByRole('button', { name: 'Reload' }));
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    await within(inspector()).findByText('checking reopen eligibility…');

    act(() => view.emit(resolved('s2', verdict({ reason: 'fresh s2 verdict' }))));

    await within(inspector()).findByText('checking reopen eligibility…');
    expect(within(inspector()).queryByText('old failure')).toBeNull();
    expect(within(inspector()).queryByRole('button', { name: 'Reload' })).toBeNull();
  });

  it('replaces a live row when the daemon says it closed, and judges it without re-listing', async () => {
    const { list } = listing([page({ entries: [liveEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen: vi.fn() });

    await screen.findByRole('button', { name: 'Focus' });
    act(() => {
      view.emit({ type: 'closed', entry: closedEntry('s1') });
      view.emit(resolved('s1', verdict({ reason: 'the worktree is still there' })));
    });

    await waitFor(() => expect(screen.queryByRole('button', { name: 'Focus' })).toBeNull());
    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(row('run s1').getAttribute('data-state')).toBe('closed');
    expect(within(row('run s1')).getByText('closed by you: work finished')).toBeTruthy();
    expect(within(inspector()).getByText('the worktree is still there')).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it('offers the resolved action without re-listing', async () => {
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen: vi.fn() });

    await rows().findByText('run s1');
    expect(within(row('run s1')).queryByRole('button', { name: 'Reopen' })).toBeNull();
    act(() => view.emit(resolved('s1', verdict({ reason: 'it is there' }))));

    await within(row('run s1')).findByRole('button', { name: 'Reopen' });
    expect(within(inspector()).getByText('it is there')).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it('reconciles a resolution that arrives while its page is loading', async () => {
    let release: ((page: SessionLedgerPage) => void) | undefined;
    const list = vi.fn((query: { before?: string }) => query.before
      ? new Promise<SessionLedgerPage>((resolve) => { release = resolve; })
      : Promise.resolve(page({ entries: [closedEntry('s1')], next_before: 'older', omitted: 1 })));
    const view = renderSessionsTab({ listSessions: list });
    await rows().findByText('run s1');

    fireEvent.click(await screen.findByRole('button', { name: '1 older ↓' }));
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    act(() => view.emit(resolved('elsewhere', goneEverywhere)));
    await act(async () => { release?.(page({ entries: [closedEntry('elsewhere')] })); });
    await rows().findByText('run elsewhere');
    fireEvent.click(rows().getByText('run elsewhere'));
    await waitFor(() => expect(within(inspector()).getByText(/branch feat\/x is gone/)).toBeTruthy());
    expect(within(inspector()).queryByText('checking reopen eligibility…')).toBeNull();
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

describe('SessionsTab runs a reopen', () => {
  it('shows the row busy while the daemon works and clears it on success', async () => {
    let finish: () => void = () => {};
    const onReopen = vi.fn(() => new Promise<boolean>((resolve) => { finish = () => resolve(true); }));
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen });
    act(() => view.emit(resolved('s1')));

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));
    const busy = within(row('run s1')).getByRole('button', { name: 'reopening…' }) as HTMLButtonElement;
    expect(busy.disabled).toBe(true);
    expect(row('run s1').getAttribute('aria-busy')).toBe('true');

    finish();
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());
    expect((within(row('run s1')).getByRole('button', { name: 'Reopen' }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('reads a refusal on the row and re-lists to learn what is offered now', async () => {
    const onReopen = vi.fn(async () => {
      throw new Error('11111111-2222-3333-4444-555555555555 cannot be reopened with reopen: the directory /Users/me/src/attn/wt/feat-x is gone. Offered instead: recreate_worktree_and_reopen');
    });
    const { list } = listing([
      page({ entries: [closedEntry('s1')] }),
      page({ entries: [closedEntry('s1')] }),
    ]);
    const view = renderSessionsTab({ listSessions: list, onReopen });
    act(() => view.emit(resolved('s1')));

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));
    await waitFor(() => expect(within(row('run s1')).getByRole('status').textContent).toBe('reopen was refused; it offers Recreate the worktree instead'));
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    act(() => view.emit(resolved('s1', verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [SessionReopenAction.RecreateWorktreeAndReopen] }))));
    await within(row('run s1')).findByRole('button', { name: 'Recreate the worktree' });
    expect(list).toHaveBeenCalledTimes(2);
  });

  it('says nothing when the user backs out of the directory picker', async () => {
    const onReopen = vi.fn(async () => false);
    const elsewhereOnly = verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [SessionReopenAction.StartFreshElsewhere] });
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen });
    act(() => view.emit(resolved('s1', elsewhereOnly)));

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Start fresh elsewhere' }));
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());
    expect(screen.queryByRole('status')).toBeNull();
  });
});

describe('SessionsTab row grammar', () => {
  it('offers the first action as the verb and the rest behind the menu', async () => {
    const onReopen = vi.fn(async () => true);
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen });
    act(() => view.emit(resolved('s1', goneEverywhere)));

    const first = await screen.findByRole('option');
    await within(first).findByRole('button', { name: 'Start fresh on the default branch' });
    expect(within(first).queryByRole('button', { name: 'Start fresh elsewhere' })).toBeNull();

    fireEvent.click(within(first).getByRole('button', { name: /More for/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Start fresh elsewhere/ }));
    expect(onReopen.mock.calls).toEqual([['s1', 'start_fresh_elsewhere']]);
    expect(screen.queryByRole('menu')).toBeNull();
  });

  it('the inspector follows the selection and reads the directory, branch and placement', async () => {
    const { list } = listing([page({
      entries: [closedEntry('s1', { branch: 'feat/x' }), closedEntry('s2', { branch: 'feat/y' })],
    })]);
    const view = renderSessionsTab({ listSessions: list, onReopen: vi.fn(), workspaceNames: { 'ws-1': 'attn' } });
    act(() => {
      view.emit(resolved('s1', goneEverywhere));
      view.emit(resolved('s2'));
    });

    await rows().findByText('run s2');
    await within(inspector()).findByText('directory is gone');
    expect(within(inspector()).getByText('branch is gone everywhere')).toBeTruthy();
    expect(within(inspector()).getByText('opens a workspace named after the session, in a new pane')).toBeTruthy();

    fireEvent.keyDown(row('run s1'), { key: 'ArrowDown' });
    expect(document.activeElement).toBe(row('run s2'));
    expect(row('run s2').getAttribute('aria-selected')).toBe('true');
    expect(within(inspector()).getByText('directory is there')).toBeTruthy();
    expect(within(inspector()).getByText('lands in attn, in a new pane')).toBeTruthy();
  });

  it('Enter runs the first verb and a digit runs the nth', async () => {
    const onReopen = vi.fn(async () => true);
    const { list } = listing([page({ entries: [closedEntry('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen });
    act(() => view.emit(resolved('s1', goneEverywhere)));

    const first = await screen.findByRole('option');
    await within(first).findByRole('button', { name: 'Start fresh on the default branch' });
    fireEvent.keyDown(first, { key: 'Enter' });
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());
    fireEvent.keyDown(first, { key: '2' });
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());
    fireEvent.keyDown(first, { key: '3' });
    expect(onReopen.mock.calls).toEqual([['s1', 'start_fresh_default_branch'], ['s1', 'start_fresh_elsewhere']]);
  });

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
    const { list } = listing([page({
      entries: [entry({ id: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee', label: 'Fixture run' }), closedEntry('s2', { label: '', close_reason: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee asked' })],
    })]);
    const view = renderSessionsTab({
      listSessions: list,
      workspaceNames: { 'ws-1': 'workspace-12345678-1234-1234-1234-123456789abc' },
    });
    act(() => view.emit(resolved('s2', verdict({ reopenable: false, actions: [], reason: 'conversation 12345678-1234-1234-1234-123456789abc is no longer in storage' }))));

    await rows().findByText('untitled session');
    expect(within(row('untitled session')).getByText('closed by you: Fixture run asked')).toBeTruthy();
    await within(row('untitled session')).findByText('its conversation is no longer in storage');
    expect(rows().queryByText(/12345678-1234/)).toBeNull();
  });

  it('shows a worktree verb only while the directory is still there, and copies the path with y', async () => {
    const onShowWorktree = vi.fn();
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const { list } = listing([page({
      entries: [closedEntry('here', { is_worktree: true }), closedEntry('gone', { is_worktree: true })],
    })]);
    const view = renderSessionsTab({ listSessions: list, onReopen: vi.fn(), onShowWorktree });
    act(() => {
      view.emit(resolved('here'));
      view.emit(resolved('gone', verdict({ directory_state: 'missing', actions: [] })));
    });

    await rows().findByText('run gone');
    await waitFor(() => expect(row('run here').getAttribute('data-verbs')).toContain('Show worktree'));
    await waitFor(() => expect(row('run gone').getAttribute('data-verbs')).not.toContain('Show worktree'));
    fireEvent.keyDown(row('run here'), { key: '2' });
    expect(onShowWorktree).toHaveBeenCalledWith('/Users/victor/projects/attn');
    fireEvent.keyDown(row('run here'), { key: 'y' });
    expect(writeText).toHaveBeenCalledWith('/Users/victor/projects/attn');
  });
});
