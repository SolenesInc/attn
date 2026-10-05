import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Worktree, WorktreeListResult, WorktreeSweepEntry } from '../../types/generated';
import { soloDesktop, daemonSession, type DaemonSession } from '../../test/daemonFixtures';
import { gesture, renderApp } from '../../test/renderApp';
import type { Reply, ScriptedDaemon } from '../../test/scriptedDaemon';
import { NOW } from '../../test/sessionLedgerFixtures';
import { rows } from './testSupport';

const MAIN = '/projects/attn';
const PATH = '/projects/attn--feat-one';

const worktree = (over: Partial<Worktree> = {}): Worktree => ({
  path: PATH,
  branch: 'feat/one',
  main_repo: MAIN,
  observed_at: '2026-09-01T10:00:00Z',
  sweep_status: 'scheduled',
  sweep_reason: 'merged and clean; idle 3 of 14 days',
  sweep_at: '2026-09-15T10:00:00Z',
  merged_signal: 'ancestor',
  ...over,
});

const INTEGRATION = [{ main_repo: MAIN, integration_branch: 'origin/next', integration_source: 'pull_requests' }];

const listed = (worktrees: Worktree[], repositories: WorktreeListResult['repositories'] = INTEGRATION): Reply => ({
  event: 'worktree_list_result',
  success: true,
  worktree_list_result: { worktrees, repositories, omitted: 0 },
});

const swept = (over: Partial<WorktreeSweepEntry> = {}): WorktreeSweepEntry => ({
  id: 'entry-1', path: PATH, main_repo: MAIN, branch: 'feat/one', action: 'deleted', reason: 'at your request', at: '2026-09-05T10:00:00Z', ...over,
});

async function openWorktrees(list: Reply, sessions: DaemonSession[] = []) {
  const view = await renderApp({ initialState: { sessions, desktops: sessions.map((session) => soloDesktop(session.id)) } });
  vi.setSystemTime(NOW);
  view.daemon.on('worktree_list', () => list);
  view.daemon.on('worktree_sweep_log', () => ({ event: 'worktree_sweep_log_result', success: true, worktree_sweep_log_result: { entries: [], omitted: 0 } }));
  await gesture(view.daemon, () => {
    fireEvent.click(screen.getByRole('button', { name: /^Open Ledger/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Worktrees' }));
  });
  return view;
}

const row = (title: string) => rows().getByText(title).closest<HTMLElement>('.ledger-row')!;
const inspector = () => within(screen.getByRole('complementary', { name: 'Details' }));
const status = () => document.querySelector('.ledger-status-left')?.textContent ?? '';

async function click(daemon: ScriptedDaemon, element: HTMLElement) {
  await gesture(daemon, () => fireEvent.click(element));
}

describe('the worktrees list', () => {
  it('asks once and groups worktrees under their repository and the branch they merge into', async () => {
    const { daemon } = await openWorktrees(listed([worktree(), worktree({ path: '/projects/attn--feat-two', branch: 'feat/two' })]));

    expect(rows().getByText('attn--feat-one')).toBeInTheDocument();
    expect(rows().getByText('attn--feat-two')).toBeInTheDocument();
    expect(rows().getByText('merges into')).toBeInTheDocument();
    expect(rows().getByText('origin/next')).toBeInTheDocument();
    expect(daemon.sentOf('worktree_list')).toHaveLength(1);
    expect(status()).toContain('2 worktrees');
  });

  it('says what the sweep decided, in a word on the row and with its reason in the inspector', async () => {
    await openWorktrees(listed([worktree()]));

    expect(within(row('attn--feat-one')).getByText('removing in 10d')).toBeInTheDocument();
    expect(within(row('attn--feat-one')).getByText('merged · ancestor')).toBeInTheDocument();
    expect(inspector().getByText('merged and clean; idle 3 of 14 days')).toBeInTheDocument();
  });

  it('shows every reason a worktree is kept as its own word', async () => {
    await openWorktrees(listed([worktree({
      dirty: true, dirty_files: 3, stashes: 2, unpushed: 1, prunable: true,
      sweep_status: 'kept_dirty', sweep_reason: '3 uncommitted or untracked file(s)',
    })]));

    for (const word of ['stale', 'dirty 3', '2 stashed', '1 ahead', 'kept dirty']) {
      expect(within(row('attn--feat-one')).getByText(word)).toBeInTheDocument();
    }
    expect(status()).toContain('1 dirty');
  });

  it('names a live session in the worktree by its title, and goes to it', async () => {
    const live = '11111111-2222-3333-4444-555555555555';
    const { daemon } = await openWorktrees(
      listed([worktree({ sweep_status: 'kept_live_session', sweep_reason: `${live} is running in it` })]),
      [daemonSession(live, { label: 'one', directory: `${PATH}/app` })],
    );

    expect(within(row('attn--feat-one')).getByText('1 live session')).toBeInTheDocument();
    expect(inspector().getByText('one is running in it')).toBeInTheDocument();
    expect(document.querySelector('[data-session-visible="1"]')).toBeNull();
    await click(daemon, inspector().getByRole('button', { name: 'one' }));

    expect(document.querySelector('[data-session-visible="1"]')?.getAttribute('data-active-pane-id')).toBe(`pane-${live}`);
  });

  it('narrows by repo: and words, and says how many the query hides', async () => {
    await openWorktrees(listed(
      [worktree(), worktree({ path: '/projects/other--x', branch: 'x', main_repo: '/projects/other' })],
      [{ main_repo: MAIN }, { main_repo: '/projects/other' }],
    ));

    fireEvent.change(screen.getByLabelText('Filter'), { target: { value: 'repo:attn' } });
    expect(rows().queryByText('other--x')).toBeNull();
    expect(status()).toContain('1 hidden by the query');

    fireEvent.change(screen.getByLabelText('Filter'), { target: { value: 'nothing-like-this' } });
    expect(rows().getByText('Nothing matches the query.')).toBeInTheDocument();
  });

  it('says why the list could not be read instead of showing it empty', async () => {
    await openWorktrees({ event: 'command_error', cmd: 'worktree_list', success: false, error: 'git is not installed' });

    expect(status()).toContain('git is not installed');
  });

  it('follows a worktree the daemon reports changed, and drops one it reports swept', async () => {
    const { daemon } = await openWorktrees(listed([worktree()]));

    daemon.emit({ event: 'worktree_state_changed', worktrees: [worktree({ dirty: true, dirty_files: 4, sweep_status: 'kept_dirty' })] });
    await daemon.idle();
    expect(within(row('attn--feat-one')).getByText('dirty 4')).toBeInTheDocument();

    daemon.emit({ event: 'worktree_swept', sweep_entry: swept({ action: 'removed' }) });
    await daemon.idle();
    expect(rows().queryByText('attn--feat-one')).toBeNull();
  });
});

describe('acting on a worktree', () => {
  it('keeps a worktree and offers the way back out', async () => {
    const { daemon } = await openWorktrees(listed([worktree()]));
    daemon.on('worktree_keep', ({ path, keep }) => ({
      event: 'worktree_keep_result', success: true, worktrees: [worktree({ path, pinned: keep, sweep_status: keep ? 'pinned' : 'scheduled' })],
    }));

    await click(daemon, within(row('attn--feat-one')).getByRole('button', { name: 'Keep' }));
    expect(within(row('attn--feat-one')).getByText('kept')).toBeInTheDocument();
    await click(daemon, within(row('attn--feat-one')).getByRole('button', { name: 'Unpin' }));

    expect(within(row('attn--feat-one')).getByRole('button', { name: 'Keep' })).toBeInTheDocument();
    expect(daemon.sentOf('worktree_keep').map(({ path, keep }) => ({ path, keep }))).toEqual([
      { path: PATH, keep: true },
      { path: PATH, keep: false },
    ]);
  });

  it('shows the daemon’s own words on the row when it refuses to keep a worktree', async () => {
    const { daemon } = await openWorktrees(listed([worktree()]));
    daemon.on('worktree_keep', () => ({ event: 'worktree_keep_result', success: false, error: 'no worktree at that path' }));

    await click(daemon, within(row('attn--feat-one')).getByRole('button', { name: 'Keep' }));

    expect(within(row('attn--feat-one')).getByRole('status')).toHaveTextContent('no worktree at that path');
  });

  it('asks before deleting, and stands down when the user moves on', async () => {
    const { daemon } = await openWorktrees(listed([worktree()]));
    daemon.on('delete_worktree', ({ path }) => ({ event: 'delete_worktree_result', path, success: true }));
    const first = row('attn--feat-one');

    fireEvent.click(within(first).getByRole('button', { name: /More for/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Delete…/ }));
    expect(daemon.sentOf('delete_worktree')).toEqual([]);
    fireEvent.keyDown(first, { key: '2' });
    await daemon.idle();
    expect(screen.queryByRole('button', { name: 'Delete for real' })).toBeNull();
  });

  it('deletes a clean worktree on the second word, and forces a dirty one while saying it loses changes', async () => {
    const { daemon } = await openWorktrees(listed([
      worktree(),
      worktree({ path: '/projects/attn--feat-two', branch: 'feat/two', dirty: true, dirty_files: 2, sweep_status: 'kept_dirty' }),
    ]));
    daemon.on('delete_worktree', ({ path }) => ({ event: 'delete_worktree_result', path, success: true }));

    fireEvent.click(row('attn--feat-one'));
    fireEvent.click(inspector().getByRole('button', { name: 'Delete…' }));
    await click(daemon, within(row('attn--feat-one')).getByRole('button', { name: 'Delete for real' }));
    expect(rows().getByText('attn--feat-one')).toBeInTheDocument();

    fireEvent.click(row('attn--feat-two'));
    fireEvent.click(inspector().getByRole('button', { name: 'Delete…' }));
    await click(daemon, within(row('attn--feat-two')).getByRole('button', { name: 'Delete, losing changes' }));

    expect(daemon.sentOf('delete_worktree')).toEqual([
      { cmd: 'delete_worktree', path: PATH },
      { cmd: 'delete_worktree', path: '/projects/attn--feat-two', force: true },
    ]);
    daemon.emit({ event: 'worktree_swept', sweep_entry: swept() });
    await daemon.idle();
    expect(rows().queryByText('attn--feat-one')).toBeNull();
  });

  it('keeps a worktree whose delete the daemon refused, and says why on its row', async () => {
    const { daemon } = await openWorktrees(listed([worktree()]));
    daemon.on('delete_worktree', ({ path }) => ({ event: 'delete_worktree_result', path, success: false, error: 'worktree has uncommitted changes' }));

    fireEvent.click(inspector().getByRole('button', { name: 'Delete…' }));
    await click(daemon, within(row('attn--feat-one')).getByRole('button', { name: 'Delete for real' }));

    expect(within(row('attn--feat-one')).getByRole('status')).toHaveTextContent('uncommitted changes');
  });

  it.each([
    [true, 'asked now'],
    [false, 'no job queue'],
  ])('asks the daemon to refresh in the background and says how it went (queued: %s)', async (success, words) => {
    const { daemon } = await openWorktrees(listed([worktree()]));
    daemon.on('worktree_refresh', () => ({ event: 'worktree_refresh_result', success }));

    await click(daemon, screen.getByRole('button', { name: /^refresh/ }));

    expect(daemon.sentOf('worktree_refresh')).toHaveLength(1);
    expect(status()).toContain(words);
  });

  it('reads the sweep log only when Removed is opened, and says who removed each worktree', async () => {
    const { daemon } = await openWorktrees(listed([worktree()]));
    daemon.on('worktree_sweep_log', () => ({
      event: 'worktree_sweep_log_result',
      success: true,
      worktree_sweep_log_result: {
        entries: [
          swept({ path: '/projects/attn--feat-gone', branch: 'feat/gone', action: 'removed', reason: 'merged (ancestor) and clean, idle 19 days', at: '2026-09-02T09:00:00Z' }),
          swept({ id: 'entry-2', path: '/projects/attn--feat-byhand', branch: 'feat/byhand', at: '2026-09-02T10:00:00Z' }),
        ],
        omitted: 0,
      },
    }));
    expect(daemon.sentOf('worktree_sweep_log')).toEqual([]);

    await click(daemon, screen.getByRole('button', { name: 'Removed' }));

    expect(daemon.sentOf('worktree_sweep_log').map(({ main_repo, limit }) => ({ main_repo, limit }))).toEqual([{ main_repo: undefined, limit: 100 }]);
    expect(within(row('attn--feat-gone')).getByText('merged (ancestor) and clean, idle 19 days')).toBeInTheDocument();
    expect(within(row('attn--feat-byhand')).getByText('deleted')).toBeInTheDocument();
    expect(within(row('attn--feat-byhand')).getByText('at your request')).toBeInTheDocument();
    expect(status()).toContain('2 removed');
  });
});
