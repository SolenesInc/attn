import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSeed } from './test/daemonFixtures';
import { openGarden, openRow } from './test/garden';
import { gesture } from './test/renderApp';

const kept = { bytes: 1_600_000, copied_at: '2026-10-01T12:00:00Z' };
const seed = daemonSeed('s-kept11', {
  title: 'Keep the parser conversation',
  continuation: {
    agent: 'claude', cwd: '/tmp/parser', directory_state: 'present',
    execution_id: 'execution-1', host_kind: 'local', resume_available: true,
    session_live: false, source: 'last_execution', handover_placement: 'reuse_cwd',
    kept_conversation: kept,
  },
});

async function open(current = seed) {
  const garden = await openGarden([{ ...current, continuation: undefined }]);
  garden.documents[current.id] = { seed: current };
  await openRow(garden.daemon, current.title);
  return garden;
}

function expectReads(daemon: Awaited<ReturnType<typeof open>>['daemon'], count = 1) {
  expect(daemon.sentOf('seed_document_get')).toEqual(Array.from({ length: count }, () => ({
    cmd: 'seed_document_get', seed_id: seed.id, request_id: expect.any(String),
  })));
  expect(daemon.sentOf('seed_resume')).toEqual([]);
}

describe('App kept conversation in the Garden reader', () => {
  it.each([
    [kept, 'conversation kept by attn (1.6 MB) while an open seed points at it'],
    [{ ...kept, bytes: 4_200 }, 'conversation kept by attn (4.2 KB) while an open seed points at it'],
    [{ ...kept, bytes: 42 }, 'conversation kept by attn (42 B) while an open seed points at it'],
    [{ ...kept, pinned_at: '2026-10-01T12:00:00Z' }, 'conversation kept by attn (1.6 MB) forever; pinned'],
    [{ ...kept, delete_after: '2026-10-13T23:30:00Z' }, 'conversation kept by attn (1.6 MB) until 2026-10-13; replant to keep it'],
    [{ ...kept, delete_after: '2026-10-13T23:30:00Z', deleted_at: '2026-10-14T01:00:00Z' }, 'conversation attn deleted its copy on 2026-10-14'],
    [{ ...kept, deleted_at: '2026-10-14T01:00:00Z', deleted_by: 'user' as const }, 'conversation you deleted attn’s copy on 2026-10-14'],
  ])('renders the daemon’s retention state: %j', async (kept_conversation, line) => {
    const { daemon } = await open({ ...seed, continuation: { ...seed.continuation!, kept_conversation } });
    expect(screen.getByText(line)).toBeInTheDocument();
    expectReads(daemon);
  });

  it('says why Resume is unavailable, including the daemon’s deletion date', async () => {
    const reason = 'attn deleted its copy of conversation original on 2026-10-14, 14 days after no open seed pointed at it';
    const { daemon } = await open({ ...seed, continuation: {
      ...seed.continuation!, resume_available: false, resume_reason: reason,
      kept_conversation: { ...kept, deleted_at: '2026-10-14T01:00:00Z' },
    } });
    expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
    expect(screen.getByText(reason)).toBeInTheDocument();
    expectReads(daemon);
  });

  it('shows retention on a closed seed whose conversation another open seed keeps', async () => {
    const garden = await open();
    const closed = { ...seed, rev: 2, status: 'harvested' };
    garden.documents[seed.id] = { seed: closed };
    await gesture(garden.daemon, () => garden.push([{ ...closed, continuation: undefined }]));
    expect(screen.getByText('conversation kept by attn (1.6 MB) while an open seed points at it')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
    expectReads(garden.daemon, 2);
  });

  it('omits retention when the daemon has no kept copy', async () => {
    const { daemon } = await open({ ...seed, continuation: {
      ...seed.continuation!, kept_conversation: undefined, resume_reason: 'An obsolete refusal',
    } });
    expect(screen.queryByText(/conversation kept by attn|conversation attn deleted/)).toBeNull();
    expect(screen.queryByText('An obsolete refusal')).toBeNull();
    expect(screen.getByRole('button', { name: 'Resume' })).toBeInTheDocument();
    expectReads(daemon);
  });

  it('waits for the current seed document before describing its conversation', async () => {
    const garden = await open();
    garden.daemon.on('seed_document_get', () => undefined);
    garden.documents[seed.id] = { seed: { ...seed, rev: 2 } };
    await gesture(garden.daemon, () => garden.push([{ ...seed, rev: 2, continuation: undefined }]));
    expect(screen.queryByText(/conversation kept by attn/)).toBeNull();
    garden.daemon.on('seed_document_get', (read) => garden.answer(read));
    await gesture(garden.daemon, () => {
      const read = garden.daemon.sentOf('seed_document_get')[1];
      garden.daemon.replyTo(read, garden.answer(read));
    });
    expect(screen.getByText('conversation kept by attn (1.6 MB) while an open seed points at it')).toBeInTheDocument();
    expectReads(garden.daemon, 2);
  });

  it('invalidates retention on a same-revision snapshot, even when the read fails', async () => {
    const garden = await open();
    garden.daemon.on('seed_document_get', () => undefined);
    const deleted = { ...seed, continuation: { ...seed.continuation!, resume_available: false,
      resume_reason: 'attn deleted its copy on 2026-10-14',
      kept_conversation: { ...kept, deleted_at: '2026-10-14T01:00:00Z' },
    } };
    garden.documents[seed.id] = { seed: deleted };
    await gesture(garden.daemon, () => garden.push([{ ...deleted, continuation: undefined }]));
    expect(screen.queryByText(/conversation kept by attn/)).toBeNull();
    await gesture(garden.daemon, () => garden.daemon.replyTo(garden.daemon.sentOf('seed_document_get')[1], {
      event: 'seed_document_get_result', success: false, error: 'document unavailable',
    }));
    expect(screen.queryByText(/conversation kept by attn/)).toBeNull();
    garden.daemon.on('seed_document_get', (read) => garden.answer(read));
    await gesture(garden.daemon, () => garden.push([{ ...deleted, continuation: undefined }]));
    expect(screen.getByText('conversation attn deleted its copy on 2026-10-14')).toBeInTheDocument();
    expect(screen.getByText('attn deleted its copy on 2026-10-14')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
    expectReads(garden.daemon, 3);
  });

  it('refreshes the kept line once from a conversation fact’s same-revision snapshot', async () => {
    const garden = await open();
    const releasing = { ...seed, continuation: { ...seed.continuation!,
      kept_conversation: { ...kept, delete_after: '2026-10-15T12:00:00Z' },
    } };
    garden.documents[seed.id] = { seed: releasing };
    await gesture(garden.daemon, () => garden.daemon.emit({ event: 'kept_conversations_changed' }));
    expectReads(garden.daemon, 1);
    await gesture(garden.daemon, () => garden.push([{ ...releasing, continuation: undefined }]));
    expect(screen.getByText('conversation kept by attn (1.6 MB) until 2026-10-15; replant to keep it')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Resume' })).toBeInTheDocument();
    expectReads(garden.daemon, 2);
  });

  it('keeps continuation actions and their keyboard focus during an unrelated snapshot refresh', async () => {
    const garden = await open();
    const resume = screen.getByRole('button', { name: 'Resume' });
    resume.focus();
    garden.daemon.on('seed_document_get', () => undefined);
    await gesture(garden.daemon, () => garden.push([{ ...seed, continuation: undefined }, daemonSeed('s-other1', { title: 'Other work' })]));
    expect(screen.getByRole('button', { name: 'Resume' })).toBe(resume);
    expect(resume).toHaveFocus();
    expect(screen.getByRole('button', { name: 'Handover' })).toBeInTheDocument();
    expectReads(garden.daemon, 2);
  });
});
