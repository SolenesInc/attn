import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { pressShortcut, renderApp } from './test/renderApp';

const AT = '2026-05-16T16:00:00Z';
const WORKTREE = '/tmp/repo--feature';

function worktreeRow(name: string) {
  return within(screen.getByRole('listbox', { name: 'Rows' })).getByText(name).closest<HTMLElement>('.ledger-row')!;
}

describe('App worktrees', () => {
  it('offers to force a delete the daemon refused for local changes, and forces it on yes', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const { daemon } = await renderApp();
    daemon.on('get_recent_locations', () => ({
      event: 'recent_locations_result',
      success: true,
      recent_locations: [{ path: '/tmp/repo', last_seen: AT, use_count: 1 }],
    }));
    daemon.on('inspect_path', ({ path }) => ({
      event: 'inspect_path_result',
      success: true,
      inspection: { input_path: path, exists: true, is_directory: true, resolved_path: path, repo_root: '/tmp/repo' },
    }));
    daemon.on('get_repo_info', () => ({
      event: 'get_repo_info_result',
      success: true,
      info: {
        repo: '/tmp/repo',
        current_branch: 'main',
        current_commit_hash: 'abc123',
        current_commit_time: AT,
        default_branch: 'main',
        branches: [],
        worktrees: [{ path: WORKTREE, branch: 'feature', main_repo: '/tmp/repo' }],
      },
    }));
    daemon.on('delete_worktree', ({ path, force }) => (
      force
        ? { event: 'delete_worktree_result', path, success: true }
        : {
          event: 'delete_worktree_result',
          path,
          success: false,
          error: 'contains modified or untracked files',
          forceable: true,
          reason_kind: 'dirty_worktree',
        }
    ));
    const press = async (key: string) => {
      fireEvent.keyDown(document.activeElement!, { key });
      await daemon.idle();
    };

    pressShortcut('session.new');
    await daemon.idle();
    await press('Enter');
    await press('ArrowDown');
    await press('ArrowDown');
    await press('d');
    await press('y');

    expect(screen.getByTestId('repo-options')).toHaveTextContent('Delete failed: contains modified or untracked files');
    expect(screen.getByTestId('repo-options')).toHaveTextContent('Force delete local worktree and branch? (y/n)');

    await press('y');

    expect(daemon.sentOf('delete_worktree')).toEqual([
      { cmd: 'delete_worktree', path: WORKTREE },
      { cmd: 'delete_worktree', path: WORKTREE, force: true },
    ]);
  });

  it('marks a worktree refreshing from the git operation that starts on it until that operation finishes', async () => {
    const { daemon } = await renderApp();
    daemon.on('worktree_list', () => ({
      event: 'worktree_list_result',
      success: true,
      worktree_list_result: {
        worktrees: [{ path: '/projects/attn--feat-one', branch: 'feat/one', main_repo: '/projects/attn', observed_at: AT }],
        repositories: [],
        omitted: 0,
      },
    }));
    const operation = { id: 'op-1', kind: 'refresh_worktree', path: '/projects/attn--feat-one', started_at: AT } as const;
    fireEvent.click(screen.getByRole('button', { name: 'Open Worktrees' }));
    await daemon.idle();

    daemon.emit({ event: 'git_operation_started', operation: { ...operation, status: 'running' } });
    expect(within(worktreeRow('attn--feat-one')).getByText('refreshing…')).toBeInTheDocument();

    daemon.emit({
      event: 'git_operation_finished',
      operation: { ...operation, status: 'succeeded', finished_at: '2026-05-16T16:00:05Z', duration_ms: 5000 },
    });
    expect(within(worktreeRow('attn--feat-one')).queryByText('refreshing…')).toBeNull();
  });
});
