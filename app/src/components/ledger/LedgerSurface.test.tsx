import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { LedgerSurface } from './LedgerSurface';
import type { LedgerTab } from './LedgerSurface';
import { closedEntry, judged, listing, liveEntry, now, page, rows } from './testSupport';
import { useWorktreeStore } from '../../store/worktrees';

function surface(tab: LedgerTab = 'sessions', extra: { onClose?: () => void; onFocusSession?: (id: string) => void; onSelectSession?: (id: string) => void } = {}) {
  useWorktreeStore.getState().clear();
  const onTabChange = vi.fn();
  const { list } = listing([page({
    entries: [liveEntry('live'), closedEntry('wt', { is_worktree: true, directory: '/projects/attn--feat-one' })],
    reopen: [judged('wt')],
  })]);
  const props = (current: LedgerTab) => ({
    isOpen: true,
    tab: current,
    onTabChange,
    onClose: extra.onClose ?? vi.fn(),
    now,
    sessions: { listSessions: list, workspaceNames: {}, onFocusSession: extra.onFocusSession ?? vi.fn(), onReopen: vi.fn() },
    worktrees: {
      listWorktrees: vi.fn().mockResolvedValue({ worktrees: [{ path: '/projects/attn--feat-one', branch: 'feat/one', main_repo: '/projects/attn' }], repositories: [{ main_repo: '/projects/attn' }], omitted: 0 }),
      getSweepLog: vi.fn().mockResolvedValue({ entries: [], omitted: 0 }),
      setKeep: vi.fn(),
      refreshWorktrees: vi.fn().mockResolvedValue(true),
      deleteWorktree: vi.fn(),
      sessions: [{ id: 'live', label: 'run live', directory: '/projects/attn--feat-one' }],
      gitOperations: {},
      onSelectSession: extra.onSelectSession ?? vi.fn(),
    },
  });
  const view = render(<LedgerSurface {...props(tab)} />);
  return { onTabChange, retab: (next: LedgerTab) => view.rerender(<LedgerSurface {...props(next)} />) };
}

describe('LedgerSurface', () => {
  it('switches lists with the bracket keys and lands on the first row', async () => {
    const { onTabChange } = surface();
    const first = await rows().findByText('run live');
    expect(document.activeElement).toBe(first.closest('.ledger-row'));

    fireEvent.keyDown(first, { key: ']' });
    expect(onTabChange).toHaveBeenCalledWith('worktrees');
  });

  it('shows a session\'s worktree, and a worktree\'s sessions, across the two lists', async () => {
    const { onTabChange, retab } = surface();
    const wt = (await rows().findByText('run wt')).closest('.ledger-row') as HTMLElement;
    fireEvent.click(wt);
    fireEvent.keyDown(wt, { key: '2' });
    expect(onTabChange).toHaveBeenCalledWith('worktrees');

    retab('worktrees');
    const row = (await rows().findByText('attn--feat-one')).closest('.ledger-row') as HTMLElement;
    expect(row.getAttribute('aria-selected')).toBe('true');
    fireEvent.keyDown(row, { key: '2' });
    expect(onTabChange).toHaveBeenCalledWith('sessions');

    retab('sessions');
    expect((screen.getByLabelText('Filter') as HTMLInputElement).value).toBe('dir:/projects/attn--feat-one');
  });

  it('leaves the surface behind when a row goes to its agent', async () => {
    const onClose = vi.fn();
    const onFocusSession = vi.fn();
    const onSelectSession = vi.fn();
    const { retab } = surface('sessions', { onClose, onFocusSession, onSelectSession });

    fireEvent.keyDown(await rows().findByText('run live'), { key: 'Enter' });
    expect(onFocusSession).toHaveBeenCalledWith('live');
    expect(onClose).toHaveBeenCalledTimes(1);

    retab('worktrees');
    const row = (await rows().findByText('attn--feat-one')).closest('.ledger-row') as HTMLElement;
    fireEvent.keyDown(row, { key: '3' });
    expect(onSelectSession).toHaveBeenCalledWith('live');
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('gives / to the query and lets an empty query hand focus back to the list', async () => {
    surface();
    const first = await rows().findByText('run live');
    fireEvent.keyDown(first, { key: '/' });
    const query = screen.getByLabelText('Filter');
    expect(document.activeElement).toBe(query);

    fireEvent.keyDown(query, { key: 'Escape' });
    expect(document.activeElement).toBe(first.closest('.ledger-row'));
    expect(within(screen.getByRole('dialog', { name: 'Sessions and worktrees' })).getByRole('button', { name: /keys/ })).toBeTruthy();
  });
});
