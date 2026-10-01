import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { openSessionsLedger, page, pages, rows } from './components/ledger/testSupport';
import { closedEntry, entry } from './test/sessionLedgerFixtures';
import type { EventMessage } from './test/protocol';

type Result = NonNullable<EventMessage<'kept_conversation_list_result'>['kept_conversation_list_result']>;
const AT = '2026-10-01T12:00:00Z';
const copy = { bytes: 4200, copied_at: AT };
const fixture: Result = {
  count: 3, stored_bytes: 12600, pending_count: 2, next_delete_after: '2026-10-13T12:00:00Z',
  rows: [
    { agent: 'claude', resume_id: 'pin', title: 'Pinned parser', session_ids: ['s-pin'], seeds: [], pinned_at: AT, kept: { ...copy, pinned_at: AT } },
    { agent: 'claude', resume_id: 'seed', title: 'Seed work', session_ids: ['s-seed'], seeds: [{ id: 's-garden', slug: 'parser-work', title: 'Parser work' }], kept: copy },
    { agent: 'claude', resume_id: 'release', title: 'Old spike', session_ids: ['s-release'], seeds: [], kept: { ...copy, delete_after: '2026-10-13T12:00:00Z' } },
    { agent: 'claude', resume_id: 'pending-pin', title: 'Pending pin', session_ids: ['s-pending'], seeds: [], pinned_at: AT, pending_reason: 'waiting for the first copy' },
    { agent: 'claude', resume_id: 'pending-seed', title: 'Pending seed', session_ids: ['s-pending-seed'], seeds: [{ id: 's-garden', slug: 'parser-work', title: 'Parser work' }], pending_reason: 'waiting for the first copy' },
  ],
};

function row(title: string) { return rows().getByText(title).closest<HTMLElement>('.ledger-row')!; }
const inspector = () => within(document.querySelector<HTMLElement>('.ledger-inspector')!);
const request = (include_deleted = false) => ({ cmd: 'kept_conversation_list', include_deleted, request_id: expect.any(String) });

async function open(result = fixture) {
  const view = await renderApp();
  view.daemon.on('kept_conversation_list', () => ({ event: 'kept_conversation_list_result', success: true, kept_conversation_list_result: result }));
  pressShortcut('sessions.open');
  await view.daemon.idle();
  await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Conversations' })));
  return view;
}

describe('App kept conversations ledger', () => {
  it('renders daemon totals, reasons and pending copies without counting them as stored bytes', async () => {
    const { daemon } = await open();
    expect(screen.getByText('3 kept · 12.6 KB')).toBeInTheDocument();
    expect(screen.getByText('2 pending')).toBeInTheDocument();
    expect(screen.getByText('next deletion 2026-10-13')).toBeInTheDocument();
    expect(within(row('Pinned parser')).getByText('Forever · pinned 2026-10-01')).toBeInTheDocument();
    expect(within(row('Seed work')).getByText('Open: parser-work')).toBeInTheDocument();
    expect(within(row('Old spike')).getByText('Deletes 2026-10-13')).toBeInTheDocument();
    expect(within(row('Pending pin')).getByText('Pending copy')).toBeInTheDocument();
    expect(within(row('Pending seed')).queryByText('0 B')).toBeNull();
    await gesture(daemon, () => fireEvent.click(row('Seed work')));
    expect(inspector().queryByRole('button', { name: 'Forget…' })).toBeNull();
    expect(inspector().getByText('Harvest or wither these seeds before forgetting the copy.')).toBeInTheDocument();
    expect(daemon.sentOf('kept_conversation_list')).toEqual([request()]);
  });

  it('refreshes on changes and reconnect and ignores a superseded response', async () => {
    const { daemon } = await open();
    daemon.on('kept_conversation_list', () => undefined);
    await gesture(daemon, () => daemon.emit({ event: 'kept_conversations_changed' }));
    const older = daemon.sentOf('kept_conversation_list')[1];
    daemon.on('kept_conversation_list', () => ({ event: 'kept_conversation_list_result', success: true,
      kept_conversation_list_result: { ...fixture, rows: [], count: 0, stored_bytes: 0, pending_count: 0 } }));
    await gesture(daemon, () => daemon.emit({ event: 'kept_conversations_changed' }));
    await gesture(daemon, () => daemon.replyTo(older, { event: 'kept_conversation_list_result', success: true, kept_conversation_list_result: fixture }));
    expect(rows().queryByText('Pinned parser')).toBeNull();
    expect(screen.getByText('0 kept · 0 B')).toBeInTheDocument();
    await daemon.reconnect();
    await daemon.idle();
    expect(daemon.sentOf('kept_conversation_list')).toEqual([request(), request(), request(), request()]);
  });

  it('keeps and unkeeps the conversation id, including a pin without a copy', async () => {
    const { daemon } = await open();
    daemon.on('kept_conversation_keep', () => ({ event: 'kept_conversation_keep_result', success: true }));
    await gesture(daemon, () => fireEvent.click(within(row('Old spike')).getByRole('button', { name: 'Keep forever' })));
    await gesture(daemon, () => fireEvent.click(within(row('Pending pin')).getByRole('button', { name: 'Unkeep' })));
    expect(daemon.sentOf('kept_conversation_keep')).toEqual([
      { cmd: 'kept_conversation_keep', session_id: 'conversation:release', keep: true, request_id: expect.any(String) },
      { cmd: 'kept_conversation_keep', session_id: 'conversation:pending-pin', keep: false, request_id: expect.any(String) },
    ]);
    expect(daemon.sentOf('kept_conversation_list')).toEqual([request()]);
  });

  it('requires explicit confirmation, supports Cancel and Escape, and shows daemon refusals', async () => {
    const { daemon } = await open();
    daemon.on('kept_conversation_forget', () => ({ event: 'kept_conversation_forget_result', success: false, error: 'Open seed parser-work keeps this conversation' }));
    await gesture(daemon, () => fireEvent.click(row('Old spike')));
    await gesture(daemon, () => fireEvent.click(inspector().getByRole('button', { name: 'Forget…' })));
    expect(inspector().getByText('Delete attn’s copy of Old spike (4.2 KB)? Claude’s own files are not touched.')).toBeInTheDocument();
    expect(daemon.sentOf('kept_conversation_forget')).toEqual([]);
    await gesture(daemon, () => fireEvent.keyDown(row('Old spike'), { key: 'Enter' }));
    expect(inspector().queryByRole('button', { name: 'Forget now' })).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(row('Old spike'), { key: '2' }));
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(screen.getByRole('button', { name: 'Conversations' })).toBeInTheDocument();
    expect(daemon.sentOf('kept_conversation_forget')).toEqual([]);
    await gesture(daemon, () => fireEvent.keyDown(row('Old spike'), { key: '2' }));
    await gesture(daemon, () => fireEvent.click(inspector().getByRole('button', { name: 'Forget now' })));
    expect(daemon.sentOf('kept_conversation_forget')).toEqual([
      { cmd: 'kept_conversation_forget', session_id: 'conversation:release', request_id: expect.any(String) },
    ]);
    expect(inspector().getByText('Open seed parser-work keeps this conversation')).toBeInTheDocument();
  });

  it('keeps keyboard navigation on a surviving row after confirmed Forget', async () => {
    const { daemon } = await open();
    daemon.on('kept_conversation_forget', () => ({ event: 'kept_conversation_forget_result', success: true }));
    await gesture(daemon, () => row('Pinned parser').focus());
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: '2' }));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: '2' }));
    daemon.on('kept_conversation_list', () => ({ event: 'kept_conversation_list_result', success: true,
      kept_conversation_list_result: { ...fixture, rows: fixture.rows.slice(1), count: 2, stored_bytes: 8400 } }));
    await gesture(daemon, () => daemon.emit({ event: 'kept_conversations_changed' }));
    expect(row('Seed work')).toHaveFocus();
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' }));
    expect(row('Old spike')).toHaveFocus();
    expect(daemon.sentOf('kept_conversation_forget')).toEqual([
      { cmd: 'kept_conversation_forget', session_id: 'conversation:pin', request_id: expect.any(String) },
    ]);
  });

  it('requests deleted rows only after toggling and shows user deletion dates', async () => {
    const { daemon } = await open();
    daemon.on('kept_conversation_list', () => ({ event: 'kept_conversation_list_result', success: true,
      kept_conversation_list_result: { ...fixture, rows: [...fixture.rows, { agent: 'claude', resume_id: 'gone', title: 'Forgotten work', seeds: [], session_ids: [], kept: { ...copy, deleted_at: AT, deleted_by: 'user' } }] } }));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('checkbox', { name: 'Show deleted' })));
    expect(within(row('Forgotten work')).getByText('You deleted attn’s copy on 2026-10-01')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('checkbox', { name: 'Show deleted' })));
    expect(rows().queryByText('Forgotten work')).toBeNull();
    expect(daemon.sentOf('kept_conversation_list')).toEqual([request(), request(true), request()]);
  });

  it('cycles all three tabs in both directions', async () => {
    const { daemon } = await open();
    await gesture(daemon, () => row('Pinned parser').focus());
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: ']' }));
    expect(screen.getByRole('button', { name: 'Sessions' })).toHaveAttribute('aria-current', 'page');
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: ']' }));
    expect(screen.getByRole('button', { name: 'Worktrees' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('listbox', { name: 'Rows' })).toHaveFocus();
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: ']' }));
    expect(screen.getByRole('button', { name: 'Conversations' })).toHaveAttribute('aria-current', 'page');
    expect(row('Pinned parser')).toHaveFocus();
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: '[' }));
    expect(screen.getByRole('button', { name: 'Worktrees' })).toHaveAttribute('aria-current', 'page');
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: '[' }));
    expect(screen.getByRole('button', { name: 'Sessions' })).toHaveAttribute('aria-current', 'page');
  });
});

describe('App session conversation pins', () => {
  it('offers keep only for Claude and refreshes all rows sharing a changed pin', async () => {
    const sessions = [entry({ id: 'claude' }), closedEntry('closed'), entry({ id: 'codex', agent: 'codex' }), entry({ id: 'pi', agent: 'pi' })];
    const { daemon } = await openSessionsLedger(pages([page({ entries: sessions })]));
    daemon.on('kept_conversation_keep', () => ({ event: 'kept_conversation_keep_result', success: true }));
    expect(row('run claude')).toHaveAttribute('data-verbs', expect.stringContaining('Keep conversation'));
    expect(row('run closed')).toHaveAttribute('data-verbs', expect.stringContaining('Keep conversation'));
    expect(row('run codex')).not.toHaveAttribute('data-verbs', expect.stringContaining('Keep conversation'));
    expect(row('run pi')).not.toHaveAttribute('data-verbs', expect.stringContaining('Keep conversation'));
    await gesture(daemon, () => fireEvent.click(row('run claude')));
    await gesture(daemon, () => fireEvent.click(inspector().getByRole('button', { name: 'Keep conversation' })));
    daemon.on('session_list', () => ({ event: 'session_list_result', success: true, result: page({ entries: sessions.map((session) => session.agent === 'claude' ? { ...session, conversation_pinned_at: AT } : session) }) }));
    await gesture(daemon, () => daemon.emit({ event: 'kept_conversations_changed' }));
    expect(row('run closed')).toHaveAttribute('data-verbs', expect.stringContaining('Unkeep'));
    await gesture(daemon, () => fireEvent.click(inspector().getByRole('button', { name: 'Unkeep' })));
    expect(daemon.sentOf('kept_conversation_keep')).toEqual([
      { cmd: 'kept_conversation_keep', session_id: 'session:claude', keep: true, request_id: expect.any(String) },
      { cmd: 'kept_conversation_keep', session_id: 'session:claude', keep: false, request_id: expect.any(String) },
    ]);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Worktrees' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Sessions' })));
    expect(row('run closed')).toHaveAttribute('data-verbs', expect.stringContaining('Unkeep'));
    expect(daemon.sentOf('session_list')).toEqual(Array.from({ length: 3 }, () => ({ cmd: 'session_list', all: true, limit: 50, request_id: expect.any(String) })));
  });
});
