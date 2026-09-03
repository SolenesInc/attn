import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createGhosttyModelOpRing, type ModelFaultCapture } from './ghosttyModelOpRing';

const LIFECYCLE_PATH = 'debug/terminal-diagnostics.jsonl';
const INCIDENT_PATH = 'debug/terminal-incidents.jsonl';

const files = new Map<string, string>();
let writeCount = 0;
let notifyWrite: (() => void) | null = null;

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

vi.mock('@tauri-apps/api/core', () => ({ isTauri: () => true }));
vi.mock('@tauri-apps/plugin-fs', () => ({
  BaseDirectory: { AppLocalData: 'AppLocalData' },
  mkdir: async () => {},
  exists: async (path: string) => files.has(path),
  readTextFile: async (path: string) => files.get(path) ?? '',
  stat: async (path: string) => {
    const contents = files.get(path);
    if (contents === undefined) {
      throw new Error(`no such file: ${path}`);
    }
    return { size: byteLength(contents) };
  },
  writeTextFile: async (path: string, contents: string, options?: { append?: boolean }) => {
    const previous = options?.append ? files.get(path) ?? '' : '';
    files.set(path, previous + contents);
    writeCount += 1;
    notifyWrite?.();
  },
}));

// Resolves once `count` further writes have reached the fake fs — the disk path has no external
// signal, so call this before the action that triggers the writes.
function afterWrites(count: number): Promise<void> {
  const target = writeCount + count;
  return new Promise((resolve) => {
    notifyWrite = () => {
      if (writeCount >= target) {
        notifyWrite = null;
        resolve();
      }
    };
  });
}

function seedFile(path: string, approxBytes: number): void {
  const line = `${JSON.stringify({ at: 1, kind: 'resize', pane: 'pane-before-rotate' })}\n`;
  files.set(path, line.repeat(Math.floor(approxBytes / byteLength(line))));
}

// Fills a file to exactly `bytes` as one padded JSONL line. The pad is ASCII, so JSON adds no
// escapes and the length is predictable; the self-check keeps that honest.
function seedExactly(path: string, bytes: number): void {
  const shell = (pad: string) =>
    `${JSON.stringify({ at: 1, kind: 'resize', pane: 'pane-before-rotate', pad })}\n`;
  const contents = shell('x'.repeat(bytes - byteLength(shell(''))));
  if (byteLength(contents) !== bytes) {
    throw new Error(`seedExactly produced ${byteLength(contents)} bytes, wanted ${bytes}`);
  }
  files.set(path, contents);
}

// A real capture at the ring's caps: a 512KB restore snapshot plus 512KB of retained writes — the
// reason a rotation decision made after the append can overshoot by megabytes.
function buildModelFaultSizedCapture(): ModelFaultCapture {
  const ring = createGhosttyModelOpRing();
  ring.beginEpoch(80, 24);
  ring.noteRestoreChunk(new Uint8Array(512 * 1024).fill(0x41), 80, 24);
  for (let index = 0; index < 512; index += 1) {
    ring.noteWrite(new Uint8Array(1024).fill(0x42));
  }
  return ring.capture();
}

async function loadModule() {
  vi.resetModules();
  return import('./terminalDiagnosticsLog');
}

describe('diagnostics file rotation', () => {
  beforeEach(() => {
    files.clear();
    writeCount = 0;
    notifyWrite = null;
    window.localStorage.setItem('attn:terminal-diagnostics', '1');
  });

  afterEach(() => {
    // The boundary case stubs Date.now; a leaked stub would freeze time for the rest of the file.
    vi.restoreAllMocks();
  });

  it('persists input evidence without copying its event history into later incident contexts', async () => {
    const diagnostics = await loadModule();
    const written = afterWrites(1);
    diagnostics.recordDiag({
      kind: 'input', pane: 'input-pane', reasons: ['composition_mismatch'],
      recent: [{ event: 'keydown', outcome: 'composing' }],
    });
    await written;
    const persisted = JSON.parse(files.get(LIFECYCLE_PATH)!.trim());
    expect(persisted.recent).toEqual([{ event: 'keydown', outcome: 'composing' }]);
    const summary = window.__ATTN_TERMINAL_DIAG_DUMP?.().find((event) => event.pane === 'input-pane');
    expect(summary?.recent).toBeUndefined();
    expect(summary?.detailsIn).toBe(diagnostics.TERMINAL_DIAGNOSTICS_FILE);
  });

  it('exports persisted and queued input records without unrelated or partial records', async () => {
    const diagnostics = await loadModule();
    files.set(LIFECYCLE_PATH, [
      '{"kind":"input","pane":"before-restart"}',
      '{"kind":"paint","text":"PRIVATE_OUTPUT","context":[{"kind":"input"}]}',
      '{"kind":"input",',
      'null',
      '',
    ].join('\n'));
    diagnostics.recordDiag({ kind: 'input', pane: 'current-pane', reasons: ['composition_mismatch'] });

    const dump = await diagnostics.readTerminalInputDiagnostics();
    expect(dump.trim().split('\n').map((line) => JSON.parse(line).pane))
      .toEqual(['before-restart', 'current-pane']);
    expect(dump).not.toContain('PRIVATE_OUTPUT');
    expect(dump.endsWith('\n')).toBe(true);
  });

  it('returns an empty dump when the diagnostic log does not exist', async () => {
    const diagnostics = await loadModule();
    expect(await diagnostics.readTerminalInputDiagnostics()).toBe('');
  });

  it('rotates before appending, so a near-cap file plus a model_fault capture stays under the cap', async () => {
    const { noteModelFault, FILE_SIZE_CAP_BYTES } = await loadModule();
    seedFile(LIFECYCLE_PATH, FILE_SIZE_CAP_BYTES - 1024);
    const capture = buildModelFaultSizedCapture();

    const written = afterWrites(1);
    noteModelFault('pane-fault', {
      session: 's-1',
      operation: 'render',
      error: 'Out of bounds memory access',
      model: 7,
      rendererEpoch: 2,
      capture,
    });
    await written;

    const contents = files.get(LIFECYCLE_PATH) ?? '';
    expect(byteLength(contents)).toBeLessThanOrEqual(FILE_SIZE_CAP_BYTES);

    const lines = contents.split('\n').filter(Boolean);
    expect(byteLength(lines[1] ?? '')).toBeGreaterThan(1024 * 1024);

    expect(contents).not.toContain('pane-before-rotate');
    expect(lines).toHaveLength(2);
    expect(JSON.parse(lines[0] ?? '{}').kind).toBe('rotate');
    const record = JSON.parse(lines[1] ?? '{}');
    expect(record.kind).toBe('model_fault');
    expect(record.capture).toEqual(capture);
  });

  // Filling the cap exactly must append, one byte more must rotate. A probe write measures the
  // written line's true length, since a hand-built copy of the JSON would drift from the module.
  it('appends when the write fills the cap exactly and rotates one byte past it', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(1_700_000_000_000);
    const event = { kind: 'resize', pane: 'pane-boundary' } as const;

    const probe = await loadModule();
    const measured = afterWrites(1);
    probe.recordDiag(event);
    await measured;
    const lineBytes = byteLength(files.get(LIFECYCLE_PATH) ?? '');
    const { FILE_SIZE_CAP_BYTES } = probe;

    files.clear();
    const atCap = await loadModule();
    seedExactly(LIFECYCLE_PATH, FILE_SIZE_CAP_BYTES - lineBytes);
    let written = afterWrites(1);
    atCap.recordDiag(event);
    await written;

    let contents = files.get(LIFECYCLE_PATH) ?? '';
    expect(contents).not.toContain('"kind":"rotate"');
    expect(contents).toContain('pane-before-rotate');
    expect(byteLength(contents)).toBe(FILE_SIZE_CAP_BYTES);

    files.clear();
    const overCap = await loadModule();
    seedExactly(LIFECYCLE_PATH, FILE_SIZE_CAP_BYTES - lineBytes + 1);
    written = afterWrites(1);
    overCap.recordDiag(event);
    await written;

    contents = files.get(LIFECYCLE_PATH) ?? '';
    expect(contents).not.toContain('pane-before-rotate');
    const lines = contents.split('\n').filter(Boolean);
    expect(JSON.parse(lines[0] ?? '{}').kind).toBe('rotate');
    expect(JSON.parse(lines[1] ?? '{}').pane).toBe('pane-boundary');
    expect(byteLength(contents)).toBeLessThanOrEqual(FILE_SIZE_CAP_BYTES);
  });

  it('applies the same projected-size rule to the incident stream', async () => {
    const { recordPaint, FILE_SIZE_CAP_BYTES } = await loadModule();
    seedFile(INCIDENT_PATH, FILE_SIZE_CAP_BYTES - 64);

    const written = afterWrites(2);
    recordPaint({
      pane: 'pane-incident',
      session: 's-1',
      cols: 80,
      rows: 24,
      force: false,
      offset: 0,
      modelPrintable: 500,
      quads: 3,
      cellsArrayLen: null,
      skipNull: null,
      skipZeroWidth: null,
    });
    await written;

    const contents = files.get(INCIDENT_PATH) ?? '';
    expect(byteLength(contents)).toBeLessThanOrEqual(FILE_SIZE_CAP_BYTES);
    expect(contents).not.toContain('pane-before-rotate');
    const lines = contents.split('\n').filter(Boolean);
    expect(JSON.parse(lines[0] ?? '{}').kind).toBe('rotate');
    expect(JSON.parse(lines[1] ?? '{}').reason).toBe('paint_underdraw');
  });
});
