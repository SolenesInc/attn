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
  const garden = await openGarden([current]);
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
    [{ ...kept, delete_after: '2026-10-13T23:30:00Z' }, 'conversation kept by attn (1.6 MB) until 2026-10-13; replant to keep it'],
    [{ ...kept, delete_after: '2026-10-13T23:30:00Z', deleted_at: '2026-10-14T01:00:00Z' }, 'conversation attn deleted its copy on 2026-10-14'],
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
    await gesture(garden.daemon, () => garden.push([{ ...seed, rev: 2, status: 'harvested' }]));
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
    await gesture(garden.daemon, () => garden.push([{ ...seed, rev: 2 }]));
    expect(screen.queryByText(/conversation kept by attn/)).toBeNull();
    garden.daemon.on('seed_document_get', (read) => garden.answer(read));
    await gesture(garden.daemon, () => {
      const read = garden.daemon.sentOf('seed_document_get')[1];
      garden.daemon.replyTo(read, garden.answer(read));
    });
    expect(screen.getByText('conversation kept by attn (1.6 MB) while an open seed points at it')).toBeInTheDocument();
    expectReads(garden.daemon, 2);
  });

  it('invalidates retention and Resume on a same-revision snapshot, even when the read fails', async () => {
    const garden = await open();
    garden.daemon.on('seed_document_get', () => undefined);
    const deleted = { ...seed, continuation: { ...seed.continuation!, resume_available: false,
      resume_reason: 'attn deleted its copy on 2026-10-14',
      kept_conversation: { ...kept, deleted_at: '2026-10-14T01:00:00Z' },
    } };
    await gesture(garden.daemon, () => garden.push([deleted]));
    expect(screen.queryByText(/conversation kept by attn/)).toBeNull();
    expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
    await gesture(garden.daemon, () => garden.daemon.replyTo(garden.daemon.sentOf('seed_document_get')[1], {
      event: 'seed_document_get_result', success: false, error: 'document unavailable',
    }));
    expect(screen.queryByText(/conversation kept by attn/)).toBeNull();
    expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
    garden.daemon.on('seed_document_get', (read) => garden.answer(read));
    await gesture(garden.daemon, () => garden.push([deleted]));
    expect(screen.getByText('conversation attn deleted its copy on 2026-10-14')).toBeInTheDocument();
    expect(screen.getByText('attn deleted its copy on 2026-10-14')).toBeInTheDocument();
    expectReads(garden.daemon, 3);
  });
});
