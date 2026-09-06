import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useWorktreeStore } from '../../store/worktrees';
import type { GitOperation, Worktree } from '../../types/generated';
import { renderWorktreesTab, rows } from './testSupport';

const worktree = (over: Partial<Worktree> = {}): Worktree => ({
  path: '/projects/attn--feat-one',
  branch: 'feat/one',
  main_repo: '/projects/attn',
  observed_at: '2026-09-01T10:00:00Z',
  sweep_status: 'scheduled',
  sweep_reason: 'merged and clean; idle 3 of 14 days',
  sweep_at: '2026-09-15T10:00:00Z',
  merged_signal: 'ancestor',
  ...over,
});

const listing = (worktrees: Worktree[], repositories: object[] = [{ main_repo: '/projects/attn', integration_branch: 'origin/next', integration_source: 'pull_requests' }]) =>
  vi.fn().mockResolvedValue({ worktrees, repositories, omitted: 0 });

// The daemon names sessions by id in its prose; the surface must read the title instead.
const LIVE = '11111111-2222-3333-4444-555555555555';

const row = (title: string) => rows().getByText(title).closest('.ledger-row') as HTMLElement;
const inspector = () => screen.getByRole('complementary', { name: 'Details' });
const status = () => screen.getByTestId('status').textContent ?? '';

describe('WorktreesTab rows', () => {
  beforeEach(() => { useWorktreeStore.getState().clear(); });

  it('fetches once and groups rows under their repository and integration branch', async () => {
    const listWorktrees = listing([worktree(), worktree({ path: '/projects/attn--feat-two', branch: 'feat/two' })]);
    renderWorktreesTab({ listWorktrees });

    await rows().findByText('attn--feat-one');
    expect(rows().getByText('attn--feat-two')).toBeTruthy();
    expect(rows().getByText('merges into')).toBeTruthy();
    expect(rows().getByText('origin/next')).toBeTruthy();
    expect(listWorktrees).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(status()).toContain('2 worktrees'));
  });

  it('says what the sweep decided, on the row in a word and in the inspector with the reason', async () => {
    renderWorktreesTab({ listWorktrees: listing([worktree()]) });

    await rows().findByText('attn--feat-one');
    expect(within(row('attn--feat-one')).getByText(/removing in/)).toBeTruthy();
    expect(within(row('attn--feat-one')).getByText('merged · ancestor')).toBeTruthy();
    expect(row('attn--feat-one').getAttribute('data-reason')).toBe('merged and clean; idle 3 of 14 days');
    expect(within(inspector()).getByText('merged and clean; idle 3 of 14 days')).toBeTruthy();
  });

  it('shows every kept state as its own word', async () => {
    renderWorktreesTab({ listWorktrees: listing([worktree({
      dirty: true, dirty_files: 3, stashes: 2, unpushed: 1, prunable: true,
      sweep_status: 'kept_dirty', sweep_reason: '3 uncommitted or untracked file(s)',
    })]) });

    await rows().findByText('attn--feat-one');
    for (const word of ['stale', 'dirty 3', '2 stashed', '1 ahead', 'kept dirty']) {
      expect(within(row('attn--feat-one')).getByText(word)).toBeTruthy();
    }
    await waitFor(() => expect(status()).toContain('1 dirty'));
  });

  it('marks a row refreshing while a git operation runs against its path', async () => {
    const operation = { id: 'op-1', kind: 'refresh_worktree', path: '/projects/attn--feat-one', started_at: '2026-09-05T10:00:00Z', status: 'running' } as GitOperation;
    renderWorktreesTab({ listWorktrees: listing([worktree()]), gitOperations: { 'op-1': operation } });

    await rows().findByText('attn--feat-one');
    expect(row('attn--feat-one').querySelector('.ledger-glyph.is-refreshing')).toBeTruthy();
    expect(within(row('attn--feat-one')).getByText('refreshing…')).toBeTruthy();
  });

  it('names a live session in the worktree and goes to it, in the name of the session', async () => {
    const onSelectSession = vi.fn();
    renderWorktreesTab({
      listWorktrees: listing([worktree({ sweep_status: 'kept_live_session', sweep_reason: `${LIVE} is running in it` })]),
      onSelectSession,
      sessions: [{ id: LIVE, label: 'one', directory: '/projects/attn--feat-one/app' }],
    });

    await rows().findByText('attn--feat-one');
    expect(within(row('attn--feat-one')).getByText('1 live session')).toBeTruthy();
    expect(within(inspector()).getByText('one is running in it')).toBeTruthy();
    fireEvent.click(within(inspector()).getByRole('button', { name: 'one' }));
    expect(onSelectSession).toHaveBeenCalledWith(LIVE);
    expect(row('attn--feat-one').getAttribute('data-verbs')).toContain('Go to one');
  });

  it('narrows by repo: and words, and hands the directory to the sessions list', async () => {
    const onShowSessions = vi.fn();
    renderWorktreesTab({
      listWorktrees: listing(
        [worktree(), worktree({ path: '/projects/other--x', branch: 'x', main_repo: '/projects/other' })],
        [{ main_repo: '/projects/attn' }, { main_repo: '/projects/other' }],
      ),
      onShowSessions,
    });
    await rows().findByText('other--x');

    fireEvent.change(screen.getByLabelText('Filter'), { target: { value: 'repo:attn' } });
    expect(rows().queryByText('other--x')).toBeNull();
    await waitFor(() => expect(status()).toContain('1 hidden by the query'));
    fireEvent.change(screen.getByLabelText('Filter'), { target: { value: 'nothing-like-this' } });
    expect(rows().getByText('Nothing matches the query.')).toBeTruthy();

    fireEvent.change(screen.getByLabelText('Filter'), { target: { value: '' } });
    fireEvent.keyDown(row('attn--feat-one'), { key: '2' });
    expect(onShowSessions).toHaveBeenCalledWith('/projects/attn--feat-one');
  });

  it('surfaces a failed fetch instead of rendering an empty list', async () => {
    renderWorktreesTab({ listWorktrees: vi.fn().mockRejectedValue(new Error('WebSocket not connected')) });
    await waitFor(() => expect(status()).toContain('WebSocket not connected'));
  });
});

describe('WorktreesTab actions', () => {
  beforeEach(() => { useWorktreeStore.getState().clear(); });

  const keeping = () => vi.fn().mockImplementation((path: string, keep: boolean) =>
    Promise.resolve(worktree({ path, pinned: keep, sweep_status: keep ? 'pinned' : 'scheduled' })));

  it('keeps a worktree and offers the way back out', async () => {
    const setKeep = keeping();
    renderWorktreesTab({ listWorktrees: listing([worktree()]), setKeep });

    fireEvent.click(await within(await screen.findByRole('option')).findByRole('button', { name: 'Keep' }));
    await within(row('attn--feat-one')).findByRole('button', { name: 'Unpin' });
    expect(setKeep).toHaveBeenCalledWith('/projects/attn--feat-one', true);
    expect(within(row('attn--feat-one')).getByText('kept')).toBeTruthy();
    expect(row('attn--feat-one').getAttribute('data-pinned')).toBe('true');

    fireEvent.click(within(row('attn--feat-one')).getByRole('button', { name: 'Unpin' }));
    await within(row('attn--feat-one')).findByRole('button', { name: 'Keep' });
    expect(setKeep).toHaveBeenLastCalledWith('/projects/attn--feat-one', false);
  });

  it('asks before deleting, then leaves the row to the daemon’s receipt', async () => {
    const deleteWorktree = vi.fn().mockResolvedValue(undefined);
    renderWorktreesTab({ listWorktrees: listing([worktree()]), deleteWorktree });

    const first = await screen.findByRole('option');
    fireEvent.click(within(first).getByRole('button', { name: /More for/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Delete…/ }));
    expect(deleteWorktree).not.toHaveBeenCalled();
    expect(within(first).getByRole('button', { name: 'Delete for real' })).toBeTruthy();

    fireEvent.keyDown(first, { key: '2' });
    expect(within(first).queryByRole('button', { name: 'Delete for real' })).toBeNull();

    fireEvent.click(within(inspector()).getByRole('button', { name: 'Delete…' }));
    fireEvent.click(within(first).getByRole('button', { name: 'Delete for real' }));
    await waitFor(() => expect(deleteWorktree).toHaveBeenCalledWith('/projects/attn--feat-one', false));

    expect(rows().getByText('attn--feat-one')).toBeTruthy();
    useWorktreeStore.getState().swept({
      id: 'entry-1', path: '/projects/attn--feat-one', main_repo: '/projects/attn', branch: 'feat/one',
      action: 'deleted', reason: 'at your request', at: '2026-09-05T10:00:00Z',
    });
    await waitFor(() => expect(rows().queryByText('attn--feat-one')).toBeNull());
  });

  it('says a dirty worktree loses changes and forces the delete', async () => {
    const deleteWorktree = vi.fn().mockResolvedValue(undefined);
    renderWorktreesTab({ listWorktrees: listing([worktree({ dirty: true, dirty_files: 2, sweep_status: 'kept_dirty' })]), deleteWorktree });

    const first = await screen.findByRole('option');
    fireEvent.click(within(inspector()).getByRole('button', { name: 'Delete…' }));
    fireEvent.click(within(first).getByRole('button', { name: 'Delete, losing changes' }));
    await waitFor(() => expect(deleteWorktree).toHaveBeenCalledWith('/projects/attn--feat-one', true));
  });

  it('surfaces a refused delete on the row rather than dropping it', async () => {
    const deleteWorktree = vi.fn().mockRejectedValue(new Error('worktree has uncommitted changes'));
    renderWorktreesTab({ listWorktrees: listing([worktree()]), deleteWorktree });

    const first = await screen.findByRole('option');
    fireEvent.click(within(inspector()).getByRole('button', { name: 'Delete…' }));
    fireEvent.click(within(first).getByRole('button', { name: 'Delete for real' }));
    await waitFor(() => expect(within(first).getByRole('status').textContent).toContain('uncommitted changes'));
    expect(rows().getByText('attn--feat-one')).toBeTruthy();
  });

  it('asks the daemon to refresh in the background and says when it asked', async () => {
    const refreshWorktrees = vi.fn().mockResolvedValue(true);
    renderWorktreesTab({ listWorktrees: listing([worktree()]), refreshWorktrees });

    await rows().findByText('attn--feat-one');
    fireEvent.click(screen.getByRole('button', { name: /^refresh/ }));
    await waitFor(() => expect(refreshWorktrees).toHaveBeenCalled());
    await waitFor(() => expect(status()).toContain('asked now'));
  });

  it('reads the sweep log only when Removed is opened, and says who removed each worktree', async () => {
    const getSweepLog = vi.fn().mockResolvedValue({
      entries: [
        { id: 'entry-1', path: '/projects/attn--feat-gone', main_repo: '/projects/attn', branch: 'feat/gone', action: 'removed', reason: 'merged (ancestor) and clean, idle 19 days', at: '2026-09-02T09:00:00Z' },
        { id: 'entry-2', path: '/projects/attn--feat-byhand', main_repo: '/projects/attn', branch: 'feat/byhand', action: 'deleted', reason: 'at your request', at: '2026-09-02T10:00:00Z' },
      ],
      omitted: 0,
    });
    renderWorktreesTab({ listWorktrees: listing([worktree()]), getSweepLog });

    await rows().findByText('attn--feat-one');
    expect(getSweepLog).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Removed' }));
    await rows().findByText('attn--feat-gone');
    expect(getSweepLog).toHaveBeenCalledWith(undefined, 100);
    expect(within(row('attn--feat-gone')).getByText('merged (ancestor) and clean, idle 19 days')).toBeTruthy();
    expect(within(row('attn--feat-byhand')).getByText('deleted')).toBeTruthy();
    expect(within(row('attn--feat-byhand')).getByText('at your request')).toBeTruthy();
    expect(row('attn--feat-byhand').getAttribute('data-action')).toBe('deleted');
    await waitFor(() => expect(status()).toContain('2 removed'));
  });
});
