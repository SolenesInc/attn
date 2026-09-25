import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import type { SessionReopen } from '../../types/generated';
import { SessionReopenAction } from '../../types/generated';
import { agentWorkspace, daemonSession } from '../../test/daemonFixtures';
import type { CommandMessage } from '../../test/protocol';
import type { Reply, ScriptedDaemon } from '../../test/scriptedDaemon';
import { closedEntry, entry, liveEntry, verdict } from '../../test/sessionLedgerFixtures';
import { namedWorkspaces, openSessionsLedger, page, pages, rows, type LedgerAnswer } from './testSupport';

type ReopenReply = Reply | 'hold';

const ledger = () => screen.queryByRole('dialog', { name: 'Sessions and worktrees' });
const row = (label: string) => rows().getByText(label).closest('.ledger-row') as HTMLElement;
const inspector = () => screen.getByRole('complementary', { name: 'Details' });
const shownList = () => within(within(ledger()!).getByRole('navigation', { name: 'Which list' })).getByRole('button', { current: 'page' }).textContent;
const goneEverywhere = verdict({
  reopenable: false,
  reason: 'the directory is gone; branch feat/x is gone from this repository and its remotes',
  directory_state: 'missing',
  branch_state: 'gone',
  workspace_plan: 'create',
  actions: [SessionReopenAction.StartFreshDefaultBranch, SessionReopenAction.StartFreshElsewhere],
});

function refused(offer: SessionReopen): Reply {
  const offered = offer.actions.join(', ');
  const error = offered
    ? `11111111-2222-3333-4444-555555555555 cannot be reopened with reopen: ${offer.reason}. Offered instead: ${offered}`
    : `11111111-2222-3333-4444-555555555555 cannot be reopened: ${offer.reason}`;
  return { event: 'session_reopen_result', success: false, error, reopen: offer };
}

function failed(error: string): Reply {
  return { event: 'session_reopen_result', success: false, error };
}

async function openLedger(
  answer: LedgerAnswer,
  { live = [], workspaceNames = {}, reopen = [] }: { live?: string[]; workspaceNames?: Record<string, string>; reopen?: ReopenReply[] } = {},
) {
  const view = await openSessionsLedger(answer, {
    initialState: {
      sessions: live.map((id) => daemonSession(id, { label: `run ${id}` })),
      workspaces: [...live.map(agentWorkspace), ...namedWorkspaces(workspaceNames)],
    },
  });
  const held: CommandMessage<'session_reopen'>[] = [];
  view.daemon.on('session_reopen', (command) => {
    const reply = reopen[Math.min(view.daemon.sentOf('session_reopen').length, reopen.length) - 1];
    if (reply === 'hold') {
      held.push(command);
      return;
    }
    return reply;
  });
  return {
    ...view,
    reopens: () => view.daemon.sentOf('session_reopen').map(({ session_id, action, directory }) => ({ session_id, action, directory })),
    reopened: async (index: number) => {
      const command = held[index];
      view.daemon.replyTo(command, {
        event: 'session_reopen_result',
        request_id: command.request_id,
        success: true,
        result: { action: command.action ?? SessionReopenAction.Reopen, directory: '/Users/victor/projects/attn', session_id: command.session_id, workspace_id: 'ws-1' },
      });
      await view.daemon.idle();
    },
  };
}

async function refuseFirstReopen(view: { daemon: ScriptedDaemon }, label: string) {
  fireEvent.click(within(row(label)).getByRole('button', { name: 'Reopen' }));
  await view.daemon.idle();
  expect(within(row(label)).getByRole('status')).toBeInTheDocument();
}

const pathInput = () => screen.getByPlaceholderText('Type path (e.g., ~/projects) or search...');

async function chooseDirectory(daemon: ScriptedDaemon, path: string) {
  daemon.on('inspect_path', () => ({
    event: 'inspect_path_result',
    success: true,
    inspection: { input_path: path, exists: true, is_directory: true, resolved_path: path },
  }));
  fireEvent.change(pathInput(), { target: { value: path } });
  fireEvent.keyDown(pathInput(), { key: 'Enter' });
  await daemon.idle();
}

describe('SessionsTab reopens on demand', () => {
  it('offers Reopen on every closed row and Focus on a live one without asking for eligibility', async () => {
    const view = await openLedger(pages([page({ entries: [closedEntry('s1'), closedEntry('s2'), liveEntry('s3')] })]), { live: ['s3'] });

    expect(within(row('run s1')).getByRole('button', { name: 'Reopen' })).toBeTruthy();
    expect(within(row('run s2')).getByRole('button', { name: 'Reopen' })).toBeTruthy();
    expect(within(row('run s3')).getByRole('button', { name: 'Focus' })).toBeTruthy();
    expect(within(row('run s3')).queryByRole('button', { name: 'Reopen' })).toBeNull();
    expect(view.queries()[0]).not.toHaveProperty('reopen');
    expect(view.reopens()).toEqual([]);
  });

  it('shows the row busy while the daemon works and leaves for the reopened session on success', async () => {
    const view = await openLedger(pages([page({ entries: [closedEntry('s1')] })]), { reopen: ['hold'] });

    fireEvent.click(within(screen.getByRole('option')).getByRole('button', { name: 'Reopen' }));
    await view.daemon.idle();
    expect(view.reopens()).toEqual([{ session_id: 's1', action: 'reopen', directory: undefined }]);
    const busy = within(row('run s1')).getByRole('button', { name: 'reopening…' }) as HTMLButtonElement;
    expect(busy.disabled).toBe(true);
    expect(row('run s1').getAttribute('aria-busy')).toBe('true');

    await view.reopened(0);
    expect(ledger()).toBeNull();
  });

  it('turns a refusal into the actions the session offers instead, and reads them in the inspector', async () => {
    const view = await openLedger(
      pages([page({ entries: [closedEntry('s1', { branch: 'feat/x' })] })]),
      { reopen: [refused(goneEverywhere)], workspaceNames: { 'ws-1': 'attn' } },
    );

    await refuseFirstReopen(view, 'run s1');

    const refusedRow = row('run s1');
    expect(within(refusedRow).getByRole('status').textContent).toBe('Reopen was refused; it offers Start fresh on the default branch instead');
    expect(within(refusedRow).getByRole('button', { name: 'Start fresh on the default branch' })).toBeTruthy();
    expect(within(inspector()).getByText('directory is gone')).toBeTruthy();
    expect(within(inspector()).getByText('branch is gone everywhere')).toBeTruthy();
    expect(within(inspector()).getByText('opens a workspace named after the session, in a new pane')).toBeTruthy();
    expect(within(inspector()).getByRole('button', { name: /Start fresh elsewhere/ })).toBeTruthy();
  });

  it('keeps Reopen when the refusal names no offer', async () => {
    const view = await openLedger(
      pages([page({ entries: [closedEntry('s1')] })]),
      { reopen: [failed('the session changed after this row was listed; reload to see it')] },
    );

    fireEvent.click(within(screen.getByRole('option')).getByRole('button', { name: 'Reopen' }));
    await view.daemon.idle();

    expect(within(row('run s1')).getByRole('status').textContent).toContain('the session changed');
    expect(within(row('run s1')).getByRole('button', { name: 'Reopen' })).toBeTruthy();
  });

  it('runs an offered action from the menu, with Enter and with a digit', async () => {
    const view = await openLedger(pages([page({ entries: [closedEntry('s1')] })]), { reopen: [refused(goneEverywhere)] });
    await refuseFirstReopen(view, 'run s1');

    fireEvent.click(within(row('run s1')).getByRole('button', { name: /More for/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Start fresh elsewhere/ }));
    await view.daemon.idle();
    await chooseDirectory(view.daemon, '/tmp/first');
    expect(rows().queryByText('reopening…')).toBeNull();
    fireEvent.keyDown(row('run s1'), { key: 'Enter' });
    await view.daemon.idle();
    expect(rows().queryByText('reopening…')).toBeNull();
    fireEvent.keyDown(row('run s1'), { key: '2' });
    await view.daemon.idle();
    await chooseDirectory(view.daemon, '/tmp/second');
    expect(rows().queryByText('reopening…')).toBeNull();

    expect(view.reopens()).toEqual([
      { session_id: 's1', action: 'reopen', directory: undefined },
      { session_id: 's1', action: 'start_fresh_elsewhere', directory: '/tmp/first' },
      { session_id: 's1', action: 'start_fresh_default_branch', directory: undefined },
      { session_id: 's1', action: 'start_fresh_elsewhere', directory: '/tmp/second' },
    ]);
  });

  it('forgets a refusal and its offer once the session closes again', async () => {
    const view = await openLedger(pages([page({ entries: [closedEntry('s1')] })]), { reopen: [refused(goneEverywhere)] });
    await refuseFirstReopen(view, 'run s1');

    await view.closed(closedEntry('s1', { closed_at: '2026-09-05T12:00:00Z' }));

    expect(within(row('run s1')).getByRole('button', { name: 'Reopen' })).toBeInTheDocument();
    expect(screen.queryByRole('status')).toBeNull();
    expect(within(inspector()).queryByText('directory is gone')).toBeNull();
  });

  it('says nothing when the user backs out of the directory picker', async () => {
    const elsewhereOnly = verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [SessionReopenAction.StartFreshElsewhere] });
    const view = await openLedger(pages([page({ entries: [closedEntry('s1')] })]), { reopen: [refused(elsewhereOnly)] });
    await refuseFirstReopen(view, 'run s1');

    fireEvent.click(within(row('run s1')).getByRole('button', { name: 'Start fresh elsewhere' }));
    await view.daemon.idle();
    fireEvent.keyDown(pathInput(), { key: 'Escape' });
    await view.daemon.idle();

    expect(rows().queryByText('reopening…')).toBeNull();
    expect(screen.queryByRole('status')).toBeNull();
    expect(view.reopens()).toHaveLength(1);
  });

  it('replaces a live row when the daemon says it closed, and offers Reopen without re-listing', async () => {
    const view = await openLedger(pages([page({ entries: [liveEntry('s1')] })]), { live: ['s1'] });
    expect(screen.getByRole('button', { name: 'Focus' })).toBeInTheDocument();

    await view.closed(closedEntry('s1'));

    expect(within(row('run s1')).getByRole('button', { name: 'Reopen' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Focus' })).toBeNull();
    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(row('run s1').getAttribute('data-state')).toBe('closed');
    expect(within(row('run s1')).getByText('closed by you: work finished')).toBeTruthy();
    expect(view.queries()).toHaveLength(1);
  });

  it('hides stale pagination while replacing the current page', async () => {
    const view = await openLedger((_query, index) => (index === 0 ? page({ entries: [closedEntry('s1')], next_before: 'older', omitted: 1 }) : 'hold'));
    expect(rows().getByText('run s1')).toBeInTheDocument();

    await view.daemon.reconnect();
    await view.daemon.idle();

    expect(view.queries()).toHaveLength(2);
    expect(screen.queryByRole('button', { name: '1 older ↓' })).toBeNull();
    await view.release(0, page({ entries: [closedEntry('s1')] }));
  });
});

describe('SessionsTab row grammar', () => {
  it('names the session that closed another, falls back to its id, and says you for the user', async () => {
    await openLedger(pages([page({ entries: [
      entry({ id: 'dispatcher', label: 'Ledger work' }),
      closedEntry('delegate', { label: 'Worktree reclaim', closed_by: 'dispatcher', close_reason: 'it went quiet' }),
      closedEntry('orphan', { closed_by: 'sess-off-page', close_reason: 'the run finished' }),
      closedEntry('mine', { close_reason: undefined }),
    ] })]));

    expect(within(row('Worktree reclaim')).getByText('closed by Ledger work: it went quiet')).toBeTruthy();
    expect(within(row('run orphan')).getByText('closed by sess-off-page: the run finished')).toBeTruthy();
    expect(within(row('run mine')).getByText('closed by you')).toBeTruthy();
  });

  it('keeps ids off the surface: prose names sessions by title and the row title never falls back to an id', async () => {
    const deadEnd = verdict({ reopenable: false, actions: [], reason: 'conversation 12345678-1234-1234-1234-123456789abc is no longer in storage' });
    const view = await openLedger(
      pages([page({
        entries: [entry({ id: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee', label: 'Fixture run' }), closedEntry('s2', { label: '', close_reason: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee asked' })],
      })]),
      { reopen: [refused(deadEnd)], workspaceNames: { 'ws-1': 'workspace-12345678-1234-1234-1234-123456789abc' } },
    );

    expect(within(row('untitled session')).getByText('closed by you: Fixture run asked')).toBeTruthy();
    await refuseFirstReopen(view, 'untitled session');
    expect(within(row('untitled session')).getByText('its conversation is no longer in storage')).toBeInTheDocument();
    expect(rows().queryByText(/12345678-1234/)).toBeNull();
  });

  it('shows a worktree verb until a refusal says the directory is gone, copies the path with y, and shows the worktree', async () => {
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const view = await openLedger(
      pages([page({ entries: [closedEntry('here', { is_worktree: true }), closedEntry('gone', { is_worktree: true })] })]),
      { reopen: [refused(verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [] }))] },
    );

    expect(row('run here').getAttribute('data-verbs')).toContain('Show worktree');
    expect(row('run gone').getAttribute('data-verbs')).toContain('Show worktree');
    await refuseFirstReopen(view, 'run gone');
    expect(row('run gone').getAttribute('data-verbs')).not.toContain('Show worktree');

    fireEvent.keyDown(row('run here'), { key: 'y' });
    expect(writeText).toHaveBeenCalledWith('/Users/victor/projects/attn');
    fireEvent.keyDown(row('run here'), { key: '2' });
    await view.daemon.idle();
    expect(shownList()).toBe('Worktrees');
  });
});
