import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { SessionReopenAction } from '../../types/generated';
import { closedEntry, entry, judged, listing, liveEntry, page, renderSessionsTab, rows, verdict } from './testSupport';

const row = (label: string) => rows().getByText(label).closest('.ledger-row') as HTMLElement;
const inspector = () => screen.getByRole('complementary', { name: 'Details' });
const nextPage = () => fireEvent.click(screen.getByRole('button', { name: 'Closed' }));

const goneEverywhere = verdict({
  reopenable: false,
  reason: 'the directory is gone; branch feat/x is gone from this repository and its remotes',
  directory_state: 'missing',
  branch_state: 'gone',
  workspace_plan: 'create',
  actions: [SessionReopenAction.StartFreshDefaultBranch, SessionReopenAction.StartFreshElsewhere],
});

describe('SessionsTab verdicts', () => {
  it('judges every closed row with the one read that fetched them and reads the verdict in the inspector', async () => {
    const { list, calls } = listing([page({
      entries: [closedEntry('s1'), closedEntry('s2'), liveEntry('s3')],
      reopen: [
        judged('s1', { reason: 'the worktree is still there' }),
        judged('s2', { reopenable: false, reason: 'the worktree is gone', actions: [] }),
      ],
    })]);
    renderSessionsTab({ listSessions: list, onReopen: vi.fn() });

    await rows().findByText('run s1');
    expect(within(inspector()).getByText('the worktree is still there')).toBeTruthy();
    // A dead end says so on the row; a verdict with a verb does not repeat itself there.
    expect(within(row('run s2')).getByText('the worktree is gone')).toBeTruthy();
    expect(within(row('run s1')).queryByText('the worktree is still there')).toBeNull();
    expect(within(row('run s1')).getByRole('button', { name: 'Reopen' })).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
    expect(calls[0].reopen).toBe(true);
  });

  it('shows a verdict as checking while git refines it, then settles in place', async () => {
    const { list } = listing([
      page({ entries: [closedEntry('s1')], reopen: [judged('s1', { checking: true, reason: 'checking its branch' })] }),
      page({ entries: [closedEntry('s1')], reopen: [judged('s1', { reason: 'its branch is still here' })] }),
    ]);
    renderSessionsTab({ listSessions: list });

    await rows().findByText('run s1');
    expect(within(inspector()).getByText(/checking the branch/)).toBeTruthy();
    expect(row('run s1').querySelector('.ledger-glyph.is-refreshing')).toBeTruthy();
    nextPage();

    await waitFor(() => expect(within(inspector()).queryByText(/checking the branch/)).toBeNull());
    expect(within(inspector()).getByText('its branch is still here')).toBeTruthy();
  });

  it('holds an action taken mid-check and runs it against the verdict that lands', async () => {
    const onReopen = vi.fn();
    const { list } = listing([
      page({ entries: [closedEntry('s1')], reopen: [judged('s1', { checking: true, reason: 'checking' })] }),
      page({ entries: [closedEntry('s1')], reopen: [judged('s1', { reason: 'it is there' })] }),
    ]);
    renderSessionsTab({ listSessions: list, onReopen });

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));
    expect(onReopen).not.toHaveBeenCalled();
    expect(rows().getByText('waiting for the branch check…')).toBeTruthy();

    nextPage();
    await waitFor(() => expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]));
  });

  it('refuses an action the fresh verdict no longer offers, and says why', async () => {
    const onReopen = vi.fn();
    const { list } = listing([
      page({ entries: [closedEntry('s1')], reopen: [judged('s1', { checking: true, reason: 'checking' })] }),
      page({ entries: [closedEntry('s1')], reopen: [judged('s1', { reopenable: false, reason: 'the worktree is gone', actions: [SessionReopenAction.RecreateWorktreeAndReopen] })] }),
    ]);
    renderSessionsTab({ listSessions: list, onReopen });

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));
    nextPage();

    await screen.findAllByText('The check finished and that is no longer possible: the worktree is gone');
    expect(onReopen).not.toHaveBeenCalled();
    expect(within(row('run s1')).getByRole('button', { name: 'Recreate the worktree' })).toBeTruthy();
  });

  it('runs a settled action straight away and offers none without a hand to run it', async () => {
    const onReopen = vi.fn(async () => true);
    const { list } = listing([page({ entries: [closedEntry('s1')], reopen: [judged('s1')] })]);
    const view = renderSessionsTab({ listSessions: list, onReopen });

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));
    expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]);

    view.unmount();
    renderSessionsTab({ listSessions: list });
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
  it('replaces a live row when the daemon says it closed, and judges it without re-listing', async () => {
    const { list } = listing([page({ entries: [liveEntry('s1')] })]);
    const { rerender } = renderSessionsTab({ listSessions: list, onReopen: vi.fn() });

    await screen.findByRole('button', { name: 'Focus' });
    rerender({ closeNotice: { entry: closedEntry('s1'), reopen: verdict({ reason: 'the worktree is still there' }), nonce: 1 } });

    await waitFor(() => expect(screen.queryByRole('button', { name: 'Focus' })).toBeNull());
    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(row('run s1').getAttribute('data-state')).toBe('closed');
    expect(within(row('run s1')).getByText('closed by you: work finished')).toBeTruthy();
    expect(within(inspector()).getByText('the worktree is still there')).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it('swaps the verdict when the branch check lands, and fires a held click against it', async () => {
    const onReopen = vi.fn(async () => true);
    const { list } = listing([page({
      entries: [closedEntry('s1')],
      reopen: [judged('s1', { checking: true, reason: 'checking its branch', actions: [SessionReopenAction.Reopen] })],
    })]);
    const { rerender } = renderSessionsTab({ listSessions: list, onReopen });

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));
    expect(onReopen).not.toHaveBeenCalled();
    rerender({ verdictNotice: { verdicts: { s1: verdict({ reason: 'it is there' }) }, nonce: 1 } });

    await waitFor(() => expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]));
    expect(within(inspector()).getByText('it is there')).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it('keeps a notice that beat its page and applies it when the page still says checking', async () => {
    const { list } = listing([
      page({ entries: [closedEntry('s1')], reopen: [judged('s1')] }),
      page({ entries: [closedEntry('s1'), closedEntry('elsewhere')], reopen: [
        judged('s1'),
        judged('elsewhere', { checking: true, reason: 'checking its branch', actions: [SessionReopenAction.StartFreshElsewhere] }),
      ] }),
    ]);
    const { rerender } = renderSessionsTab({ listSessions: list });
    await rows().findByText('run s1');
    rerender({ verdictNotice: { verdicts: { elsewhere: goneEverywhere }, nonce: 1 } });

    nextPage();
    await rows().findByText('run elsewhere');
    fireEvent.click(rows().getByText('run elsewhere'));
    await waitFor(() => expect(within(inspector()).getByText(/branch feat\/x is gone/)).toBeTruthy());
    expect(rows().queryByText(/checking the branch/)).toBeNull();
  });
});

describe('SessionsTab runs a reopen', () => {
  it('shows the row busy while the daemon works and clears it on success', async () => {
    let finish: () => void = () => {};
    const onReopen = vi.fn(() => new Promise<boolean>((resolve) => { finish = () => resolve(true); }));
    const { list } = listing([page({ entries: [closedEntry('s1')], reopen: [judged('s1')] })]);
    renderSessionsTab({ listSessions: list, onReopen });

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
      page({ entries: [closedEntry('s1')], reopen: [judged('s1')] }),
      page({ entries: [closedEntry('s1')], reopen: [judged('s1', { reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [SessionReopenAction.RecreateWorktreeAndReopen] })] }),
    ]);
    renderSessionsTab({ listSessions: list, onReopen });

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Reopen' }));
    await waitFor(() => expect(within(row('run s1')).getByRole('status').textContent).toBe('reopen was refused; it offers Recreate the worktree instead'));
    await within(row('run s1')).findByRole('button', { name: 'Recreate the worktree' });
    expect(list).toHaveBeenCalledTimes(2);
  });

  it('says nothing when the user backs out of the directory picker', async () => {
    const onReopen = vi.fn(async () => false);
    const elsewhereOnly = verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [SessionReopenAction.StartFreshElsewhere] });
    const { list } = listing([page({ entries: [closedEntry('s1')], reopen: [{ session_id: 's1', reopen: elsewhereOnly }] })]);
    renderSessionsTab({ listSessions: list, onReopen });

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Start fresh elsewhere' }));
    await waitFor(() => expect(rows().queryByText('reopening…')).toBeNull());
    expect(screen.queryByRole('status')).toBeNull();
  });
});

describe('SessionsTab row grammar', () => {
  it('offers the first action as the verb and the rest behind the menu', async () => {
    const onReopen = vi.fn(async () => true);
    const { list } = listing([page({ entries: [closedEntry('s1')], reopen: [{ session_id: 's1', reopen: goneEverywhere }] })]);
    renderSessionsTab({ listSessions: list, onReopen });

    const first = await screen.findByRole('option');
    expect(within(first).getByRole('button', { name: 'Start fresh on the default branch' })).toBeTruthy();
    expect(within(first).queryByRole('button', { name: 'Start fresh elsewhere' })).toBeNull();

    fireEvent.click(within(first).getByRole('button', { name: /More for/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Start fresh elsewhere/ }));
    expect(onReopen.mock.calls).toEqual([['s1', 'start_fresh_elsewhere']]);
    expect(screen.queryByRole('menu')).toBeNull();
  });

  it('the inspector follows the selection and reads the directory, branch and placement', async () => {
    const { list } = listing([page({
      entries: [closedEntry('s1', { branch: 'feat/x' }), closedEntry('s2', { branch: 'feat/y' })],
      reopen: [{ session_id: 's1', reopen: goneEverywhere }, judged('s2')],
    })]);
    renderSessionsTab({ listSessions: list, onReopen: vi.fn(), workspaceNames: { 'ws-1': 'attn' } });

    await rows().findByText('run s2');
    expect(within(inspector()).getByText('directory is gone')).toBeTruthy();
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
    const { list } = listing([page({ entries: [closedEntry('s1')], reopen: [{ session_id: 's1', reopen: goneEverywhere }] })]);
    renderSessionsTab({ listSessions: list, onReopen });

    const first = await screen.findByRole('option');
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
      reopen: [judged('s2', { reopenable: false, actions: [], reason: 'conversation 12345678-1234-1234-1234-123456789abc is no longer in storage' })],
    })]);
    renderSessionsTab({ listSessions: list, workspaceNames: { 'ws-1': 'workspace-12345678-1234-1234-1234-123456789abc' } });

    await rows().findByText('untitled session');
    expect(within(row('untitled session')).getByText('closed by you: Fixture run asked')).toBeTruthy();
    expect(within(row('untitled session')).getByText('its conversation is no longer in storage')).toBeTruthy();
    expect(rows().queryByText(/12345678-1234/)).toBeNull();
  });

  it('shows a worktree verb only while the directory is still there, and copies the path with y', async () => {
    const onShowWorktree = vi.fn();
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const { list } = listing([page({
      entries: [closedEntry('here', { is_worktree: true }), closedEntry('gone', { is_worktree: true })],
      reopen: [judged('here'), judged('gone', { directory_state: 'missing', actions: [] })],
    })]);
    renderSessionsTab({ listSessions: list, onReopen: vi.fn(), onShowWorktree });

    await rows().findByText('run gone');
    expect(row('run here').getAttribute('data-verbs')).toContain('Show worktree');
    expect(row('run gone').getAttribute('data-verbs')).not.toContain('Show worktree');
    fireEvent.keyDown(row('run here'), { key: '2' });
    expect(onShowWorktree).toHaveBeenCalledWith('/Users/victor/projects/attn');
    fireEvent.keyDown(row('run here'), { key: 'y' });
    expect(writeText).toHaveBeenCalledWith('/Users/victor/projects/attn');
  });
});
