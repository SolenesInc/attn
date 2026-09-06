import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SessionsPanel } from './SessionsPanel';
import { SettingsProvider } from '../contexts/SettingsContext';
import type { SessionLedgerPage, SessionLedgerQuery } from '../hooks/daemonSessionLedgerEvents';
import type { SessionLedgerEntry, SessionReopen, SessionReopenEntry } from '../types/generated';
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
    close_reason: 'work finished',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    ...overrides,
  };
}

function liveEntry(id: string): SessionLedgerEntry {
  return {
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${id}`,
    last_seen: '2026-09-05T13:00:00Z',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    id,
  };
}

function judged(sessionId: string, overrides: Partial<SessionReopen> = {}): SessionReopenEntry {
  return {
    session_id: sessionId,
    reopen: {
      reopenable: true,
      actions: [SessionReopenAction.Reopen],
      checking: false,
      directory_state: 'present',
      workspace_id: 'ws-1',
      workspace_plan: 'reuse',
      pane_plan: 'add',
      ...overrides,
    },
  };
}

const now = () => new Date('2026-09-05T14:30:00Z');

function panel(pages: SessionLedgerPage[], props: Record<string, unknown> = {}) {
  const calls: SessionLedgerQuery[] = [];
  const list = vi.fn(async (query: SessionLedgerQuery) => {
    calls.push(query);
    return pages[Math.min(calls.length - 1, pages.length - 1)];
  });
  render(
    <SettingsProvider settings={{}} setSetting={vi.fn()}>
      <SessionsPanel isOpen onClose={() => {}} listSessions={list} now={now} {...props} />
    </SettingsProvider>,
  );
  return { list, calls };
}

const nextPage = () => fireEvent.click(screen.getByRole('button', { name: 'Closed' }));

describe('SessionsPanel reopen verdicts', () => {
  it('judges every closed row on screen with the one read that fetched them', async () => {
    const { list, calls } = panel([{
      entries: [closedEntry('s1'), closedEntry('s2'), liveEntry('s3')],
      omitted: 0,
      reopen: [
        judged('s1', { reason: 'the worktree is still there' }),
        judged('s2', { reopenable: false, reason: 'the worktree is gone', actions: [] }),
      ],
    }]);

    await waitFor(() => expect(screen.getByText('the worktree is still there')).toBeTruthy());
    expect(screen.getByText('the worktree is gone')).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
    expect(calls[0].reopen).toBe(true);
  });

  it('shows a verdict as refreshing while a git check refines it, then in place', async () => {
    panel([
      {
        entries: [closedEntry('s1')],
        omitted: 0,
        reopen: [judged('s1', { checking: true, reason: 'the worktree is gone; checking its branch' })],
      },
      {
        entries: [closedEntry('s1')],
        omitted: 0,
        reopen: [judged('s1', { reason: 'the worktree is gone; its branch is still here' })],
      },
    ]);

    await waitFor(() => expect(screen.getByText('checking…')).toBeTruthy());
    nextPage();

    await waitFor(() => expect(screen.queryByText('checking…')).toBeNull());
    expect(screen.getByText('the worktree is gone; its branch is still here')).toBeTruthy();
  });

  it('holds an action taken mid-check and runs it against the verdict that lands', async () => {
    const onReopen = vi.fn();
    panel(
      [
        {
          entries: [closedEntry('s1')],
          omitted: 0,
          reopen: [judged('s1', { checking: true, reason: 'checking the worktree' })],
        },
        {
          entries: [closedEntry('s1')],
          omitted: 0,
          reopen: [judged('s1', { reason: 'the worktree is still there' })],
        },
      ],
      { onReopen },
    );

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));
    expect(onReopen).not.toHaveBeenCalled();
    expect(screen.getByText('waiting for the check…')).toBeTruthy();

    nextPage();
    await waitFor(() => expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]));
  });

  it('refuses an action the fresh verdict no longer offers, and says why', async () => {
    const onReopen = vi.fn();
    panel(
      [
        {
          entries: [closedEntry('s1')],
          omitted: 0,
          reopen: [judged('s1', { checking: true, reason: 'checking the worktree' })],
        },
        {
          entries: [closedEntry('s1')],
          omitted: 0,
          reopen: [judged('s1', {
            reopenable: false,
            reason: 'the worktree is gone',
            actions: [SessionReopenAction.RecreateWorktreeAndReopen],
          })],
        },
      ],
      { onReopen },
    );

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));
    nextPage();

    await waitFor(() => expect(
      screen.getByText('The check finished and that is no longer possible: the worktree is gone'),
    ).toBeTruthy());
    expect(onReopen).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Recreate the worktree' })).toBeTruthy();
  });

  it('runs an action straight away once the verdict has settled', async () => {
    const onReopen = vi.fn();
    panel(
      [{
        entries: [closedEntry('s1')],
        omitted: 0,
        reopen: [judged('s1', { reason: 'the worktree is still there' })],
      }],
      { onReopen },
    );

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));
    expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]);
  });

  it('offers no action while nothing can carry one out', async () => {
    panel([{
      entries: [closedEntry('s1')],
      omitted: 0,
      reopen: [judged('s1', { reason: 'the worktree is still there' })],
    }]);

    await waitFor(() => expect(screen.getByText('the worktree is still there')).toBeTruthy());
    expect(screen.queryByRole('button', { name: 'Reopen' })).toBeNull();
  });

  it('leaves the reopen column blank for a live row', async () => {
    panel([{ entries: [liveEntry('s1')], omitted: 0 }]);

    await waitFor(() => expect(screen.getByText('run s1')).toBeTruthy());
    expect(screen.queryByText('checking…')).toBeNull();
  });
});

describe('SessionsPanel live closes', () => {
  it('replaces a live row in place when the daemon says it closed', async () => {
    const page = { entries: [liveEntry('s1')], omitted: 0 };
    const list = vi.fn(async () => page);
    const wrap = (closeNotice?: { entry: ReturnType<typeof closedEntry>; nonce: number }) => (
      <SettingsProvider settings={{}} setSetting={vi.fn()}>
        <SessionsPanel isOpen onClose={() => {}} listSessions={list} now={now} closeNotice={closeNotice} />
      </SettingsProvider>
    );
    const view = render(wrap());

    await waitFor(() => expect(screen.getByRole('button', { name: 'Focus' })).toBeTruthy());
    view.rerender(wrap({ entry: closedEntry('s1'), nonce: 1 }));

    await waitFor(() => expect(screen.queryByRole('button', { name: 'Focus' })).toBeNull());
    expect(screen.getByText('closed')).toBeTruthy();
    expect(screen.getByText(/closed by you: work finished/)).toBeTruthy();
  });

  it('judges a row that closed while the panel was open, without re-listing', async () => {
    const page: SessionLedgerPage = { entries: [liveEntry('s1')], omitted: 0 };
    const list = vi.fn(async () => page);
    const view = render(
      <SessionsPanel isOpen onClose={() => {}} listSessions={list} now={now} />,
    );

    await waitFor(() => expect(screen.getByRole('button', { name: 'Focus' })).toBeTruthy());
    view.rerender(
      <SessionsPanel
        isOpen
        onClose={() => {}}
        listSessions={list}
        now={now}
        closeNotice={{
          entry: closedEntry('s1'),
          reopen: judged('s1', { reason: 'the worktree is still there' }).reopen,
          nonce: 1,
        }}
      />,
    );

    await waitFor(() => expect(screen.getByText('the worktree is still there')).toBeTruthy());
    expect(list).toHaveBeenCalledTimes(1);
  });
});
