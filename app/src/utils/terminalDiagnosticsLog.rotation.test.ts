import { beforeEach, describe, expect, it, onTestFinished, vi } from 'vitest';
import { createGhosttyModelOpRing } from './ghosttyModelOpRing';

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

function seedExactly(path: string, bytes: number): void {
  const shell = (pad: string) => `${JSON.stringify({ at: 1, kind: 'resize', pane: 'pane-before-rotate', pad })}\n`;
  files.set(path, shell('x'.repeat(bytes - byteLength(shell('')))));
}

async function loadModule() {
  vi.resetModules();
  return import('./terminalDiagnosticsLog');
}

type Diagnostics = Awaited<ReturnType<typeof loadModule>>;

function modelFaultCapture() {
  const ring = createGhosttyModelOpRing();
  ring.beginEpoch(80, 24);
  ring.noteRestoreChunk(new Uint8Array(512 * 1024).fill(0x41), 80, 24);
  for (let index = 0; index < 512; index += 1) {
    ring.noteWrite(new Uint8Array(1024).fill(0x42));
  }
  return ring.capture();
}

const RECORDS = {
  resize: {
    path: LIFECYCLE_PATH,
    writes: 1,
    marker: '"pane":"pane-boundary"',
    write: (diagnostics: Diagnostics) => diagnostics.recordDiag({ kind: 'resize', pane: 'pane-boundary' }),
  },
  'model fault': {
    path: LIFECYCLE_PATH,
    writes: 1,
    marker: '"kind":"model_fault"',
    write: (diagnostics: Diagnostics) => diagnostics.noteModelFault('pane-fault', {
      session: 's-1', operation: 'render', error: 'Out of bounds memory access', model: 7, rendererEpoch: 2, capture: modelFaultCapture(),
    }),
  },
  incident: {
    path: INCIDENT_PATH,
    writes: 2,
    marker: '"reason":"paint_underdraw"',
    write: (diagnostics: Diagnostics) => diagnostics.recordPaint({
      pane: 'pane-incident', session: 's-1', cols: 80, rows: 24, force: false, offset: 0,
      modelPrintable: 500, quads: 3, cellsArrayLen: null, skipNull: null, skipZeroWidth: null,
    }),
  },
};

async function writeOnto(record: (typeof RECORDS)[keyof typeof RECORDS], existingBytes: (cap: number) => number) {
  files.clear();
  const diagnostics = await loadModule();
  if (existingBytes(diagnostics.FILE_SIZE_CAP_BYTES) > 0) seedExactly(record.path, existingBytes(diagnostics.FILE_SIZE_CAP_BYTES));
  const written = afterWrites(record.writes);
  record.write(diagnostics);
  await written;
  return { contents: files.get(record.path) ?? '', cap: diagnostics.FILE_SIZE_CAP_BYTES };
}

describe('diagnostics file rotation', () => {
  beforeEach(() => {
    files.clear();
    writeCount = 0;
    notifyWrite = null;
    window.localStorage.setItem('attn:terminal-diagnostics', '1');
    const now = vi.spyOn(Date, 'now').mockReturnValue(1_700_000_000_000);
    onTestFinished(() => now.mockRestore());
  });

  it.each([
    { record: 'resize', existing: 'fills the cap exactly', overshoot: 0, rotates: false },
    { record: 'resize', existing: 'passes the cap by one byte', overshoot: 1, rotates: true },
    { record: 'model fault', existing: 'fills the cap exactly', overshoot: 0, rotates: false },
    { record: 'model fault', existing: 'passes the cap by one byte', overshoot: 1, rotates: true },
    { record: 'incident', existing: 'fills the cap exactly', overshoot: 0, rotates: false },
    { record: 'incident', existing: 'passes the cap by one byte', overshoot: 1, rotates: true },
  ] as const)('a $record write that $existing rotates: $rotates', async ({ record, overshoot, rotates }) => {
    const spec = RECORDS[record];
    const { contents: alone } = await writeOnto(spec, () => 0);
    const lineBytes = byteLength(alone);

    const { contents, cap } = await writeOnto(spec, (limit) => limit - lineBytes + overshoot);

    const lines = contents.split('\n').filter(Boolean);
    expect(byteLength(contents)).toBeLessThanOrEqual(cap);
    expect(contents.includes('pane-before-rotate')).toBe(!rotates);
    expect(lines[0].includes('"kind":"rotate"')).toBe(rotates);
    expect(lines[lines.length - 1]).toContain(spec.marker);
  });
});
