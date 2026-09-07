import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  downloadDir: vi.fn(async () => '/Downloads'),
  join: vi.fn(async (...parts: string[]) => parts.join('/')),
  exists: vi.fn(async () => false),
  writeTextFile: vi.fn(async () => {}),
  save: vi.fn(async () => null as string | null),
  revealItemInDir: vi.fn(async () => {}),
}));

vi.mock('@tauri-apps/api/path', () => ({ downloadDir: mocks.downloadDir, join: mocks.join }));
vi.mock('@tauri-apps/plugin-fs', () => ({ exists: mocks.exists, writeTextFile: mocks.writeTextFile }));
vi.mock('@tauri-apps/plugin-dialog', () => ({ save: mocks.save }));
vi.mock('@tauri-apps/plugin-opener', () => ({ revealItemInDir: mocks.revealItemInDir }));

import { saveDiagnosticReport } from './diagnosticReport';

describe('saveDiagnosticReport', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.exists.mockResolvedValue(false);
    mocks.writeTextFile.mockResolvedValue(undefined);
    mocks.save.mockResolvedValue(null);
    mocks.revealItemInDir.mockResolvedValue(undefined);
  });

  it('avoids a filename collision and reveals the created report', async () => {
    mocks.writeTextFile.mockRejectedValueOnce(new Error('already exists')).mockResolvedValueOnce(undefined);
    mocks.exists.mockResolvedValueOnce(true);
    const target = await saveDiagnosticReport({ schema: 'attn.support-report.v1' });

    expect(target).toMatch(/^\/Downloads\/attn-\d{8}-\d{6}Z\.attn-report-2\.json$/);
    expect(mocks.writeTextFile).toHaveBeenCalledWith(
      target,
      expect.stringContaining('attn.support-report.v1'),
      { createNew: true },
    );
    expect(mocks.revealItemInDir).toHaveBeenCalledWith(target);
  });

  it('falls back to a save dialog when the automatic write fails', async () => {
    mocks.writeTextFile.mockRejectedValueOnce(new Error('downloads unavailable')).mockResolvedValueOnce(undefined);
    mocks.save.mockResolvedValue('/chosen/report.json');

    await expect(saveDiagnosticReport({ schema: 'attn.support-report.v1' })).resolves.toBe('/chosen/report.json');
    expect(mocks.save).toHaveBeenCalledOnce();
    expect(mocks.writeTextFile).toHaveBeenLastCalledWith('/chosen/report.json', expect.any(String));
    expect(mocks.revealItemInDir).toHaveBeenCalledWith('/chosen/report.json');
  });

  it('reports success without waiting for the desktop to reveal the file', async () => {
    mocks.revealItemInDir.mockImplementation(() => new Promise(() => {}));

    await expect(saveDiagnosticReport({ schema: 'attn.support-report.v1' })).resolves.toMatch(
      /^\/Downloads\/attn-\d{8}-\d{6}Z\.attn-report\.json$/,
    );
  });
});
