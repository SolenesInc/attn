import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import { LedgerSurface } from './LedgerSurface';
import type { LedgerTab } from './LedgerSurface';
import { page, pages, rows, serveLedger, useLedgerConnection } from './testSupport';
import { useWorktreeStore } from '../../store/worktrees';
import { renderWithDaemon } from '../../test/renderApp';
import { closedEntry, liveEntry, now } from '../../test/sessionLedgerFixtures';

type SurfaceProps = ComponentProps<typeof LedgerSurface>;

function DaemonLedgerSurface({ sessions, ...props }: Omit<SurfaceProps, 'sessions'> & { sessions: Omit<SurfaceProps['sessions'], 'connection'> }) {
  return <LedgerSurface {...props} sessions={{ ...sessions, connection: useLedgerConnection() }} />;
}

async function surface(tab: LedgerTab = 'sessions', extra: { onClose?: () => void; onFocusSession?: (id: string) => void; onSelectSession?: (id: string) => void } = {}) {
  useWorktreeStore.getState().clear();
  const onTabChange = vi.fn();
  const view = await renderWithDaemon();
  serveLedger(view.daemon, pages([page({
    entries: [liveEntry('live'), closedEntry('wt', { is_worktree: true, directory: '/projects/attn--feat-one' })],
  })]));
  const props = (current: LedgerTab) => ({
    isOpen: true,
    tab: current,
    onTabChange,
    onClose: extra.onClose ?? vi.fn(),
    now,
    sessions: {
      workspaceNames: {},
      onFocusSession: extra.onFocusSession ?? vi.fn(),
      onReopen: vi.fn(),
    },
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
  view.rerender(<DaemonLedgerSurface {...props(tab)} />);
  await view.daemon.idle();
  return {
    onTabChange,
    retab: async (next: LedgerTab) => {
      view.rerender(<DaemonLedgerSurface {...props(next)} />);
      await view.daemon.idle();
    },
  };
}

describe('LedgerSurface', () => {
  it('switches lists with the bracket keys and lands on the first row', async () => {
    const { onTabChange } = await surface();
    const first = rows().getByText('run live');
    expect(document.activeElement).toBe(first.closest('.ledger-row'));

    fireEvent.keyDown(first, { key: ']' });
    expect(onTabChange).toHaveBeenCalledWith('worktrees');
  });

  it('shows a session\'s worktree, and a worktree\'s sessions, across the two lists', async () => {
    const { onTabChange, retab } = await surface();
    const wt = (rows().getByText('run wt')).closest('.ledger-row') as HTMLElement;
    fireEvent.click(wt);
    fireEvent.keyDown(wt, { key: '2' });
    expect(onTabChange).toHaveBeenCalledWith('worktrees');

    await retab('worktrees');
    const row = (rows().getByText('attn--feat-one')).closest('.ledger-row') as HTMLElement;
    expect(row.getAttribute('aria-selected')).toBe('true');
    fireEvent.keyDown(row, { key: '2' });
    expect(onTabChange).toHaveBeenCalledWith('sessions');

    await retab('sessions');
    expect((screen.getByLabelText('Filter') as HTMLInputElement).value).toBe('dir:/projects/attn--feat-one');
  });

  it('leaves the surface behind when a row goes to its agent', async () => {
    const onClose = vi.fn();
    const onFocusSession = vi.fn();
    const onSelectSession = vi.fn();
    const { retab } = await surface('sessions', { onClose, onFocusSession, onSelectSession });

    fireEvent.keyDown(rows().getByText('run live'), { key: 'Enter' });
    expect(onFocusSession).toHaveBeenCalledWith('live');
    expect(onClose).toHaveBeenCalledTimes(1);

    await retab('worktrees');
    const row = (rows().getByText('attn--feat-one')).closest('.ledger-row') as HTMLElement;
    fireEvent.keyDown(row, { key: '3' });
    expect(onSelectSession).toHaveBeenCalledWith('live');
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('gives / to the query and lets an empty query hand focus back to the list', async () => {
    await surface();
    const first = rows().getByText('run live');
    fireEvent.keyDown(first, { key: '/' });
    const query = screen.getByLabelText('Filter');
    expect(document.activeElement).toBe(query);

    fireEvent.keyDown(query, { key: 'Escape' });
    expect(document.activeElement).toBe(first.closest('.ledger-row'));
    expect(within(screen.getByRole('dialog', { name: 'Sessions and worktrees' })).getByRole('button', { name: /keys/ })).toBeTruthy();
  });
});
