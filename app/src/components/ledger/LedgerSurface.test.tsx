import { describe, expect, it } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import { openSessionsLedger, page, pages, rows } from './testSupport';
import { agentWorkspace, daemonSession } from '../../test/daemonFixtures';
import { closedEntry, liveEntry } from '../../test/sessionLedgerFixtures';

const WORKTREE = '/projects/attn--feat-one';

async function openLedger() {
  const view = await openSessionsLedger(
    pages([page({ entries: [liveEntry('live'), closedEntry('wt', { is_worktree: true, directory: WORKTREE })] })]),
    {
      initialState: {
        sessions: [
          daemonSession('live', { label: 'run live', directory: liveEntry('live').directory }),
          daemonSession('builder', { label: 'run builder', directory: WORKTREE }),
        ],
        workspaces: [agentWorkspace('live'), agentWorkspace('builder')],
      },
    },
  );
  view.daemon.on('worktree_list', () => ({
    event: 'worktree_list_result',
    success: true,
    worktree_list_result: {
      worktrees: [{ path: WORKTREE, branch: 'feat/one', main_repo: '/projects/attn', observed_at: '2026-09-05T10:00:00Z' }],
      repositories: [{ main_repo: '/projects/attn' }],
      omitted: 0,
    },
  }));
  view.daemon.on('worktree_sweep_log', () => ({
    event: 'worktree_sweep_log_result',
    success: true,
    worktree_sweep_log_result: { entries: [], omitted: 0 },
  }));
  return view;
}

const ledger = () => screen.queryByRole('dialog', { name: 'Sessions and worktrees' });
const shownList = () => within(within(ledger()!).getByRole('navigation', { name: 'Which list' })).getByRole('button', { current: 'page' }).textContent;
const row = (label: string) => rows().getByText(label).closest<HTMLElement>('.ledger-row')!;
const selectedAgent = () => document.querySelector('.session-item.selected .session-label')?.textContent ?? null;

describe('the sessions and worktrees ledger', () => {
  it('switches lists with the bracket keys and lands on the first row', async () => {
    const { daemon } = await openLedger();
    expect(document.activeElement).toBe(row('run live'));

    fireEvent.keyDown(row('run live'), { key: ']' });
    await daemon.idle();

    expect(shownList()).toBe('Worktrees');
    expect(document.activeElement).toBe(row('attn--feat-one'));
  });

  it('shows a session\'s worktree, and a worktree\'s sessions, across the two lists', async () => {
    const { daemon } = await openLedger();
    fireEvent.click(row('run wt'));
    fireEvent.keyDown(row('run wt'), { key: '2' });
    await daemon.idle();

    expect(shownList()).toBe('Worktrees');
    expect(row('attn--feat-one').getAttribute('aria-selected')).toBe('true');

    fireEvent.keyDown(row('attn--feat-one'), { key: '2' });
    await daemon.idle();

    expect(shownList()).toBe('Sessions');
    expect((screen.getByLabelText('Filter') as HTMLInputElement).value).toBe(`dir:${WORKTREE}`);
  });

  it('leaves the surface behind when a row goes to its agent', async () => {
    const { daemon } = await openLedger();

    fireEvent.keyDown(row('run live'), { key: 'Enter' });
    await daemon.idle();
    expect(ledger()).toBeNull();
    expect(selectedAgent()).toBe('run live');

    fireEvent.click(screen.getByRole('button', { name: 'Open Worktrees' }));
    await daemon.idle();
    fireEvent.keyDown(row('attn--feat-one'), { key: '3' });
    await daemon.idle();

    expect(ledger()).toBeNull();
    expect(selectedAgent()).toBe('run builder');
  });

  it('gives / to the query and lets an empty query hand focus back to the list', async () => {
    await openLedger();
    fireEvent.keyDown(row('run live'), { key: '/' });
    const query = screen.getByLabelText('Filter');
    expect(document.activeElement).toBe(query);

    fireEvent.keyDown(query, { key: 'Escape' });
    expect(document.activeElement).toBe(row('run live'));
    expect(ledger()).not.toBeNull();
  });
});
