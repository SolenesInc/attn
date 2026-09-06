import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SessionsPanel } from './SessionsPanel';
import { SettingsProvider } from '../contexts/SettingsContext';
import type { SessionLedgerPage, SessionLedgerQuery } from '../hooks/daemonSessionLedgerEvents';
import type { SessionLedgerEntry, SessionReopen } from '../types/generated';
import { SessionReopenAction, SessionState } from '../types/generated';

function closedEntry(id: string, overrides: Partial<SessionLedgerEntry> = {}): SessionLedgerEntry {
  return {
    id,
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${id}`,
    last_seen: '2026-09-05T09:00:00Z',
    closed_at: '2026-09-05T10:00:00Z',
    closed_by: 'user',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    ...overrides,
  };
}

function verdict(overrides: Partial<SessionReopen> = {}): SessionReopen {
  return {
    reopenable: true,
    actions: [SessionReopenAction.Reopen],
    checking: false,
    directory_state: 'present',
    workspace_id: 'ws-1',
    workspace_plan: 'reuse',
    pane_plan: 'add',
    ...overrides,
  };
}

const goneEverywhere = verdict({
  reopenable: false,
  checking: false,
  reason: 'the directory is gone; branch feat/x is gone from this repository and its remotes',
  directory_state: 'missing',
  branch_state: 'gone',
  actions: [SessionReopenAction.StartFreshDefaultBranch, SessionReopenAction.StartFreshElsewhere],
});

const now = () => new Date('2026-09-05T14:30:00Z');

function panel(pages: SessionLedgerPage[], props: Record<string, unknown> = {}) {
  const calls: SessionLedgerQuery[] = [];
  const list = vi.fn(async (query: SessionLedgerQuery) => {
    calls.push(query);
    return pages[Math.min(calls.length - 1, pages.length - 1)];
  });
  const wrap = (extra: Record<string, unknown>) => (
    <SettingsProvider settings={{}} setSetting={vi.fn()}>
      <SessionsPanel isOpen onClose={() => {}} listSessions={list} now={now} {...props} {...extra} />
    </SettingsProvider>
  );
  const view = render(wrap({}));
  return { list, calls, view, rerender: (next: Record<string, unknown>) => view.rerender(wrap(next)) };
}

describe('SessionsPanel settles a checking row in place', () => {
  it('swaps the verdict when the branch check lands, without re-listing', async () => {
    const { list, rerender } = panel([{
      entries: [closedEntry('s1')],
      omitted: 0,
      reopen: [{ session_id: 's1', reopen: verdict({ checking: true, reason: 'the directory is gone; checking its branch', actions: [SessionReopenAction.StartFreshElsewhere] }) }],
    }], { onReopen: vi.fn() });

    await waitFor(() => expect(screen.getByText('checking…')).toBeTruthy());
    rerender({
      verdictNotice: {
        verdicts: { s1: verdict({ reason: 'the directory is gone; branch feat/x is still here', actions: [SessionReopenAction.RecreateWorktreeAndReopen] }) },
        nonce: 1,
      },
    });

    await waitFor(() => expect(screen.queryByText('checking…')).toBeNull());
    expect(screen.getByText('the directory is gone; branch feat/x is still here')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Recreate the worktree' })).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it('fires a click held mid-check against the verdict the notice brings', async () => {
    const onReopen = vi.fn(async () => true);
    const { rerender } = panel([{
      entries: [closedEntry('s1')],
      omitted: 0,
      reopen: [{ session_id: 's1', reopen: verdict({ checking: true, reason: 'checking' }) }],
    }], { onReopen });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));
    expect(onReopen).not.toHaveBeenCalled();

    rerender({ verdictNotice: { verdicts: { s1: verdict({ reason: 'it is there' }) }, nonce: 1 } });
    await waitFor(() => expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]));
  });

  it('keeps a notice that beat its page and applies it when the page still says checking', async () => {
    const { list, rerender } = panel([
      { entries: [closedEntry('s1')], omitted: 0, reopen: [{ session_id: 's1', reopen: verdict() }] },
      { entries: [closedEntry('s1'), closedEntry('elsewhere')], omitted: 0, reopen: [
        { session_id: 's1', reopen: verdict() },
        { session_id: 'elsewhere', reopen: verdict({ checking: true, reason: 'the directory is gone; checking its branch', actions: [SessionReopenAction.StartFreshElsewhere] }) },
      ] },
    ]);
    await waitFor(() => expect(screen.getByText('it can be reopened where it ran')).toBeTruthy());
    rerender({ verdictNotice: { verdicts: { elsewhere: goneEverywhere }, nonce: 1 } });
    expect(screen.queryByText(/branch feat\/x is gone/)).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByText(/branch feat\/x is gone/)).toBeTruthy());
    expect(screen.queryByText('checking…')).toBeNull();
  });
});

describe('SessionsPanel runs a reopen', () => {
  it('shows the row busy while the daemon works and clears it on success', async () => {
    let finish: () => void = () => {};
    const onReopen = vi.fn(() => new Promise<boolean>((resolve) => { finish = () => resolve(true); }));
    panel([{ entries: [closedEntry('s1')], omitted: 0, reopen: [{ session_id: 's1', reopen: verdict() }] }], { onReopen });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));
    const busy = screen.getByRole('button', { name: 'Reopening…' }) as HTMLButtonElement;
    expect(busy.disabled).toBe(true);
    expect(busy.closest('tr')!.getAttribute('aria-busy')).toBe('true');

    finish();
    await waitFor(() => expect(screen.queryByText('Reopening…')).toBeNull());
    expect((screen.getByRole('button', { name: 'Reopen' }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('reads a refusal on the row and re-lists to learn what is offered now', async () => {
    const onReopen = vi.fn(async () => {
      throw new Error('11111111-2222-3333-4444-555555555555 cannot be reopened with reopen: the directory /Users/me/src/attn/wt/feat-x is gone. Offered instead: recreate_worktree_and_reopen');
    });
    const { list } = panel([
      { entries: [closedEntry('s1')], omitted: 0, reopen: [{ session_id: 's1', reopen: verdict() }] },
      { entries: [closedEntry('s1')], omitted: 0, reopen: [{ session_id: 's1', reopen: verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [SessionReopenAction.RecreateWorktreeAndReopen] }) }] },
    ], { onReopen });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));

    await waitFor(() => expect(screen.getByRole('status').textContent).toBe('reopen was refused; it offers Recreate the worktree instead'));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Recreate the worktree' })).toBeTruthy());
    expect(list).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('dialog', { name: /cannot/ })).toBeNull();
  });

  it('says nothing when the user backs out of the directory picker', async () => {
    const onReopen = vi.fn(async () => false);
    const elsewhereOnly = verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [SessionReopenAction.StartFreshElsewhere] });
    panel([{ entries: [closedEntry('s1')], omitted: 0, reopen: [{ session_id: 's1', reopen: elsewhereOnly }] }], { onReopen });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Start fresh elsewhere' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Start fresh elsewhere' }));
    await waitFor(() => expect(screen.queryByText('Reopening…')).toBeNull());
    expect(screen.queryByRole('status')).toBeNull();
  });
});

describe('SessionsPanel row shape', () => {
  it('offers the first action as a button and the rest behind the arrow', async () => {
    const onReopen = vi.fn(async () => true);
    panel([{ entries: [closedEntry('s1')], omitted: 0, reopen: [{ session_id: 's1', reopen: goneEverywhere }] }], { onReopen });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Start fresh on the default branch' })).toBeTruthy());
    expect(screen.queryByRole('button', { name: 'Start fresh elsewhere' })).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: /More ways to bring back/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Start fresh elsewhere/ }));
    expect(onReopen.mock.calls).toEqual([['s1', 'start_fresh_elsewhere']]);
    expect(screen.queryByRole('menu')).toBeNull();
  });

  it('Space opens the detail under the selected row and it follows the selection', async () => {
    const onReopen = vi.fn(async () => true);
    panel([{
      entries: [closedEntry('s1', { branch: 'feat/x' }), closedEntry('s2', { branch: 'feat/y' })],
      omitted: 0,
      reopen: [{ session_id: 's1', reopen: goneEverywhere }, { session_id: 's2', reopen: verdict() }],
    }], { onReopen, workspaceNames: { 'ws-1': 'attn' } });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Start fresh on the default branch' })).toBeTruthy());
    expect(screen.queryByText('directory is gone')).toBeNull();

    const first = screen.getByText('run s1').closest('tr')!;
    fireEvent.keyDown(first, { key: ' ' });
    expect(screen.getByText('directory is gone')).toBeTruthy();
    expect(screen.getByText('branch is gone everywhere: feat/x')).toBeTruthy();
    expect(screen.getByText('lands in attn, in a new pane')).toBeTruthy();
    expect(first.getAttribute('aria-expanded')).toBe('true');

    fireEvent.keyDown(first, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(screen.getByText('run s2').closest('tr'));
    expect(screen.queryByText('branch is gone everywhere: feat/x')).toBeNull();
    expect(screen.getByText('directory is there')).toBeTruthy();

    const second = screen.getByText('run s2').closest('tr')!;
    fireEvent.keyDown(second, { key: ' ' });
    expect(screen.queryByText('directory is there')).toBeNull();
  });

  it('clicking a row selects it', async () => {
    panel([{ entries: [closedEntry('s1'), closedEntry('s2')], omitted: 0, reopen: [] }], { onReopen: vi.fn() });
    await waitFor(() => expect(screen.getByText('run s2')).toBeTruthy());
    const second = screen.getByText('run s2').closest('tr')!;
    expect(second.getAttribute('aria-selected')).toBe('false');
    fireEvent.click(screen.getByText('run s2'));
    expect(second.getAttribute('aria-selected')).toBe('true');
  });
});

describe('SessionsPanel keyboard', () => {
  it('Enter runs the first action and a digit runs the nth', async () => {
    const onReopen = vi.fn(async () => true);
    panel([{ entries: [closedEntry('s1')], omitted: 0, reopen: [{ session_id: 's1', reopen: goneEverywhere }] }], { onReopen });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Start fresh on the default branch' })).toBeTruthy());

    const row = screen.getByText('run s1').closest('tr')!;
    fireEvent.keyDown(row, { key: 'Enter' });
    fireEvent.keyDown(row, { key: '2' });
    await waitFor(() => expect(screen.queryByText('Reopening…')).toBeNull());
    fireEvent.keyDown(row, { key: '2' });
    await waitFor(() => expect(screen.queryByText('Reopening…')).toBeNull());
    fireEvent.keyDown(row, { key: '3' });
    expect(onReopen.mock.calls).toEqual([['s1', 'start_fresh_default_branch'], ['s1', 'start_fresh_elsewhere']]);
  });
});
