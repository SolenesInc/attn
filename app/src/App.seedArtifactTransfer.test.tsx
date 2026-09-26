import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, daemonSeed, daemonSession, seedDocument } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

const FIVE_MINUTES = 5 * 60 * 1000;
const REFERENCE = { kind: 'markdown_file', path: '/tmp/report.pdf' };

const SEED = daemonSeed('s-7k3f9m', { title: 'ship the report' });

async function moveLinkedFileIntoSeed() {
  const { daemon } = await renderApp({
    initialState: { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')], seeds: [SEED] },
  });
  daemon.on('seed_document_get', () => ({
    event: 'seed_document_get_result',
    success: true,
    document: seedDocument(SEED, { references: [REFERENCE] }),
  }));
  daemon.on('fs_exists', ({ path, root = '' }) => ({
    event: 'fs_exists_result',
    success: true,
    result: { path: `${root}/${path}`, exists: true },
  }));
  await gesture(daemon, () => pressShortcut('board.open'));
  await gesture(daemon, () => fireEvent.click(document.querySelector(`[data-seed-row="${SEED.id}"]`)!));
  await gesture(daemon, () => fireEvent.click(artifacts().getByRole('button', { name: 'Move into seed' })));
  const transfer = await daemon.received('seed_artifact_transfer');
  expect(transfer).toMatchObject({
    seed_id: SEED.id,
    operation: 'move',
    source_path: '/tmp/report.pdf',
    legacy_reference: REFERENCE,
  });
  return { daemon, transfer };
}

function artifacts() {
  return within(screen.getByRole('heading', { name: 'Artifacts' }).closest('section')!);
}

describe('App seed artifact transfer', () => {
  it('waits up to five minutes for the daemon to finish moving a file into a seed', async () => {
    const { daemon, transfer } = await moveLinkedFileIntoSeed();

    await act(() => vi.advanceTimersByTimeAsync(FIVE_MINUTES - 1));
    daemon.emit({
      event: 'seed_artifact_transfer_result',
      request_id: transfer.request_id,
      success: true,
      result: {
        operation_id: 'op-1',
        seed_id: SEED.id,
        operation: 'move',
        source_path: '/tmp/report.pdf',
        destination_path: '/notebook/seeds/s-7k3f9m/report.pdf',
        relative_target: 'report.pdf',
        recovered: false,
      },
    });
    await act(() => vi.advanceTimersByTimeAsync(FIVE_MINUTES));

    expect(artifacts().queryByRole('alert')).toBeNull();
  });

  it('gives up on a move the daemon never answers after five minutes, and says so', async () => {
    await moveLinkedFileIntoSeed();

    await act(() => vi.advanceTimersByTimeAsync(FIVE_MINUTES));

    expect(artifacts().getByRole('alert')).toHaveTextContent('Seed artifact transfer timed out');
    expect(artifacts().getByText('report.pdf')).toBeInTheDocument();
  });
});
