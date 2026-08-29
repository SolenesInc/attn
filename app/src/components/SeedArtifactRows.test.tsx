import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke } from '@tauri-apps/api/core';
import { open, save } from '@tauri-apps/plugin-dialog';
import { DaemonApiProvider, type DaemonApi } from '../contexts/DaemonApiContext';
import { SeedArtifactRows } from './SeedArtifactRows';

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
  save: vi.fn(),
}));

function api(overrides: Record<string, unknown> = {}): DaemonApi {
  return {
    sendSeedArtifactTarget: vi.fn(async () => ({ relative_target: 'report.bin', path: '/notebook/seeds/s-1/report.bin' })),
    sendSeedArtifactTransfer: vi.fn(async () => ({
      operation_id: 'op-1', seed_id: 's-1', operation: 'copy', source_path: '/tmp/report.bin',
      destination_path: '/notebook/seeds/s-1/report.bin', relative_target: 'report.bin', recovered: false,
    })),
    sendSeedArtifactReferenceDetach: vi.fn(async () => {}),
    ...overrides,
  } as unknown as DaemonApi;
}

describe('SeedArtifactRows', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(open).mockResolvedValue(null);
    vi.mocked(save).mockResolvedValue(null);
  });

  it('opens, reveals, and moves a safe managed file out through typed actions', async () => {
    const daemon = api({
      sendSeedArtifactTarget: vi.fn(async () => ({ relative_target: 'report.pdf', path: '/notebook/seeds/s-1/report.pdf' })),
    });
    vi.mocked(save).mockResolvedValue('/tmp/out/report.pdf');
    render(
      <DaemonApiProvider api={daemon}>
        <SeedArtifactRows
          seedId="s-1"
          artifacts={[{ filename: 'report.pdf', relative_target: 'report.pdf', size: 12, modified_at: '2026-08-29T20:00:00Z' }]}
        />
      </DaemonApiProvider>,
    );

    expect(screen.getByText('report.pdf').closest('li')).toHaveTextContent('12 bytes');
    fireEvent.click(screen.getByRole('button', { name: 'Open' }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('open_safe_seed_artifact_target', {
      path: '/notebook/seeds/s-1/report.pdf', reveal: false,
    }));
    fireEvent.click(screen.getByRole('button', { name: 'Reveal' }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('open_safe_seed_artifact_target', {
      path: '/notebook/seeds/s-1/report.pdf', reveal: true,
    }));
    fireEvent.click(screen.getByRole('button', { name: 'Move out' }));
    await waitFor(() => expect(daemon.sendSeedArtifactTransfer).toHaveBeenCalledWith({
      seedId: 's-1', operation: 'detach', filename: 'report.pdf', destinationPath: '/tmp/out/report.pdf',
    }));
  });

  it('keeps active managed files reveal-only', async () => {
    const daemon = api({
      sendSeedArtifactTarget: vi.fn(async () => ({ relative_target: 'setup.command', path: '/notebook/seeds/s-1/setup.command' })),
    });
    render(
      <DaemonApiProvider api={daemon}>
        <SeedArtifactRows
          seedId="s-1"
          artifacts={[{ filename: 'setup.command', relative_target: 'setup.command', size: 12, modified_at: '2026-08-29T20:00:00Z' }]}
        />
      </DaemonApiProvider>,
    );

    expect(screen.queryByRole('button', { name: 'Open' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Reveal' }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('open_safe_seed_artifact_target', {
      path: '/notebook/seeds/s-1/setup.command', reveal: true,
    }));
  });

  it('keeps a missing linked file visible and migrates it only through explicit Move or Copy', async () => {
    const sendSeedArtifactTransfer = vi.fn(async () => ({
      operation_id: 'op-2', seed_id: 's-1', operation: 'copy', source_path: '/gone/legacy.md',
      destination_path: '/notebook/seeds/s-1/legacy.md', relative_target: 'legacy.md', recovered: false,
    }));
    const daemon = api({ sendSeedArtifactTransfer });
    const reference = { kind: 'markdown_file', path: '/gone/legacy.md' };
    render(
      <DaemonApiProvider api={daemon}>
        <SeedArtifactRows
          seedId="s-1"
          artifacts={[]}
          references={[reference]}
          checkArtifactPath={async () => false}
        />
      </DaemonApiProvider>,
    );

    expect(await screen.findByText('not on disk')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Move into seed' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Copy into seed' }));
    await waitFor(() => expect(sendSeedArtifactTransfer).toHaveBeenCalledWith({
      seedId: 's-1', operation: 'copy', sourcePath: '/gone/legacy.md', legacyReference: reference,
    }));
  });

  it('asks for an exact source before bringing a repository-relative legacy link', async () => {
    vi.mocked(open).mockResolvedValue('/chosen/legacy.md');
    const daemon = api();
    const reference = { kind: 'markdown_file', path: 'docs/legacy.md' };
    render(
      <DaemonApiProvider api={daemon}>
        <SeedArtifactRows seedId="s-1" artifacts={[]} references={[reference]} />
      </DaemonApiProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Move into seed' }));
    await waitFor(() => expect(daemon.sendSeedArtifactTransfer).toHaveBeenCalledWith({
      seedId: 's-1', operation: 'move', sourcePath: '/chosen/legacy.md', legacyReference: reference,
    }));
  });

  it('leaves the linked row intact when a transfer is refused', async () => {
    const daemon = api({ sendSeedArtifactTransfer: vi.fn(async () => { throw new Error('destination already exists'); }) });
    render(
      <DaemonApiProvider api={daemon}>
        <SeedArtifactRows
          seedId="s-1"
          artifacts={[]}
          references={[{ kind: 'markdown_file', path: '/tmp/legacy.md' }]}
        />
      </DaemonApiProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Move into seed' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('destination already exists');
    expect(screen.getByText('legacy.md')).toBeInTheDocument();
  });
});
