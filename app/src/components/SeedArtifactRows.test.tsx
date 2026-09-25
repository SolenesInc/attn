import { fireEvent, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke } from '@tauri-apps/api/core';
import { open, save } from '@tauri-apps/plugin-dialog';
import { SeedArtifactRows } from './SeedArtifactRows';
import { renderWithDaemon } from '../test/renderApp';
import type { ScriptedDaemon } from '../test/scriptedDaemon';

type Rows = Parameters<typeof SeedArtifactRows>[0];

async function renderRows(rows: Omit<Rows, 'seedId'>, { transferRefusal = '' } = {}) {
  const { daemon } = await renderWithDaemon(<SeedArtifactRows seedId="s-1" {...rows} />);
  daemon.on('seed_artifact_target', ({ relative_target }) => ({
    event: 'seed_artifact_target_result',
    success: true,
    result: { relative_target, path: `/notebook/seeds/s-1/${relative_target}` },
  }));
  daemon.on('seed_artifact_transfer', ({ operation, source_path = '', destination_path = '' }) => (transferRefusal
    ? { event: 'seed_artifact_transfer_result', success: false, error: transferRefusal }
    : {
      event: 'seed_artifact_transfer_result',
      success: true,
      result: {
        operation_id: 'op-1', seed_id: 's-1', operation, source_path,
        destination_path, relative_target: 'report.bin', recovered: false,
      },
    }));
  await daemon.idle();
  return daemon;
}

async function click(daemon: ScriptedDaemon, name: string) {
  fireEvent.click(screen.getByRole('button', { name }));
  await daemon.idle();
}

describe('SeedArtifactRows', () => {
  beforeEach(() => {
    vi.mocked(invoke).mockClear();
    vi.mocked(open).mockResolvedValue(null);
    vi.mocked(save).mockResolvedValue(null);
  });

  it('opens, reveals, and moves a safe managed file out through typed actions', async () => {
    vi.mocked(save).mockResolvedValue('/tmp/out/report.pdf');
    const daemon = await renderRows({
      artifacts: [{ filename: 'report.pdf', relative_target: 'report.pdf', size: 12, modified_at: '2026-08-29T20:00:00Z' }],
    });

    expect(screen.getByText('report.pdf').closest('li')).toHaveTextContent('12 bytes');
    await click(daemon, 'Open');
    expect(invoke).toHaveBeenCalledWith('open_safe_seed_artifact_target', {
      path: '/notebook/seeds/s-1/report.pdf', reveal: false,
    });
    await click(daemon, 'Reveal');
    expect(invoke).toHaveBeenCalledWith('open_safe_seed_artifact_target', {
      path: '/notebook/seeds/s-1/report.pdf', reveal: true,
    });
    await click(daemon, 'Move out');
    expect(daemon.sentOf('seed_artifact_transfer')).toEqual([expect.objectContaining({
      seed_id: 's-1', operation: 'detach', filename: 'report.pdf', destination_path: '/tmp/out/report.pdf',
    })]);
  });

  it('keeps active managed files reveal-only', async () => {
    const daemon = await renderRows({
      artifacts: [{ filename: 'setup.command', relative_target: 'setup.command', size: 12, modified_at: '2026-08-29T20:00:00Z' }],
    });

    expect(screen.queryByRole('button', { name: 'Open' })).toBeNull();
    await click(daemon, 'Reveal');
    expect(invoke).toHaveBeenCalledWith('open_safe_seed_artifact_target', {
      path: '/notebook/seeds/s-1/setup.command', reveal: true,
    });
  });

  it('keeps a missing linked file visible and migrates it only through explicit Move or Copy', async () => {
    const reference = { kind: 'markdown_file', path: '/gone/legacy.md' };
    const daemon = await renderRows({ artifacts: [], references: [reference], checkArtifactPath: async () => false });

    expect(screen.getByText('not on disk')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Move into seed' })).toBeInTheDocument();
    expect(daemon.sentOf('seed_artifact_transfer')).toEqual([]);
    await click(daemon, 'Copy into seed');
    expect(daemon.sentOf('seed_artifact_transfer')).toEqual([expect.objectContaining({
      seed_id: 's-1', operation: 'copy', source_path: '/gone/legacy.md', legacy_reference: reference,
    })]);
  });

  it('asks for an exact source before bringing a repository-relative legacy link', async () => {
    vi.mocked(open).mockResolvedValue('/chosen/legacy.md');
    const reference = { kind: 'markdown_file', path: 'docs/legacy.md' };
    const daemon = await renderRows({ artifacts: [], references: [reference] });

    await click(daemon, 'Move into seed');
    expect(daemon.sentOf('seed_artifact_transfer')).toEqual([expect.objectContaining({
      seed_id: 's-1', operation: 'move', source_path: '/chosen/legacy.md', legacy_reference: reference,
    })]);
  });

  it('leaves the linked row intact when a transfer is refused', async () => {
    const daemon = await renderRows(
      { artifacts: [], references: [{ kind: 'markdown_file', path: '/tmp/legacy.md' }] },
      { transferRefusal: 'destination already exists' },
    );

    await click(daemon, 'Move into seed');
    expect(screen.getByRole('alert')).toHaveTextContent('destination already exists');
    expect(screen.getByText('legacy.md')).toBeInTheDocument();
  });
});
