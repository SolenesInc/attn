import { describe, expect, it } from 'vitest';
import {
  createGhosttyModelOpRing,
  decodeModelFaultCapture,
  MODEL_FAULT_CAPTURE_MAX_BYTES,
  MODEL_OP_RING_MAX_BYTES,
  MODEL_OP_RING_MAX_OPS,
  MODEL_OP_RING_MAX_SNAPSHOT_BYTES,
} from './ghosttyModelOpRing';

type Step =
  | ['epoch', number, number]
  | ['write', number]
  | ['restore', number, number, number]
  | ['resize', number, number]
  | ['reset'];

type Kept =
  | { kind: 'write'; bytes: Uint8Array }
  | { kind: 'resize'; cols: number; rows: number; noReflow: boolean }
  | { kind: 'reset' };

function bytesOf(length: number, seed: number): Uint8Array {
  const bytes = new Uint8Array(length);
  for (let i = 0; i < length; i += 1) bytes[i] = (seed * 31 + i) & 0xff;
  return bytes;
}

function fingerprint(bytes: Uint8Array): string {
  let hash = 2166136261;
  for (const byte of bytes) hash = Math.imul(hash ^ byte, 16777619);
  return `${bytes.length}:${hash >>> 0}`;
}

function summarize(op: Kept) {
  return op.kind === 'write' ? { kind: op.kind, bytes: fingerprint(op.bytes) } : op;
}

function concat(chunks: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(chunks.reduce((sum, chunk) => sum + chunk.length, 0));
  chunks.reduce((offset, chunk) => (out.set(chunk, offset), offset + chunk.length), 0);
  return out;
}

function repeat(times: number, step: (i: number) => Step): Step[] {
  return Array.from({ length: times }, (_, i) => step(i));
}

function mixed(length: number): Step[] {
  let state = 7;
  const next = (bound: number) => {
    state = (state * 1103515245 + 12345) % 2147483648;
    return state % bound;
  };
  return repeat(length, () => {
    const pick = next(100);
    if (pick < 70) return ['write', 1 + next(pick < 5 ? 200_000 : 4096)];
    if (pick < 88) return ['resize', 40 + next(160), 10 + next(60)];
    if (pick < 97) return ['reset'];
    return ['restore', 1 + next(60_000), 100, 30];
  });
}

function writeBytes(ops: Kept[]): number {
  return ops.reduce((sum, op) => sum + (op.kind === 'write' ? op.bytes.length : 0), 0);
}

function lastResize(ops: Kept[]): { cols: number; rows: number } | undefined {
  return [...ops].reverse().find((op): op is Extract<Kept, { kind: 'resize' }> => op.kind === 'resize');
}

function longestSuffixWithinBudget(ops: Kept[]): number {
  let start = ops.length;
  let bytes = 0;
  while (start > 0) {
    const kept = ops.length - start + 1;
    const withNext = bytes + writeBytes([ops[start - 1]]);
    if (kept > MODEL_OP_RING_MAX_OPS || (kept > 1 && withNext > MODEL_OP_RING_MAX_BYTES)) break;
    bytes = withNext;
    start -= 1;
  }
  return start;
}

function playBoth(steps: Step[]) {
  const ring = createGhosttyModelOpRing({ now: () => 0 });
  let base = { cols: 80, rows: 24 };
  let history: Kept[] = [];
  let snapshot: { cols: number; rows: number; received: Uint8Array[] } | null = null;
  let restoring = false;
  ring.beginEpoch(80, 24);
  for (const step of steps) {
    const kind = step[0];
    if (kind !== 'restore') restoring = false;
    if (step[0] === 'epoch') {
      ring.beginEpoch(step[1], step[2]);
      base = { cols: step[1], rows: step[2] };
      history = [];
      snapshot = null;
    } else if (step[0] === 'write') {
      const bytes = bytesOf(step[1], history.length);
      ring.noteWrite(bytes);
      history.push({ kind: 'write', bytes: bytes.slice() });
      bytes.fill(0xee);
    } else if (step[0] === 'restore') {
      const bytes = bytesOf(step[1], 3);
      ring.noteRestoreChunk(bytes, step[2], step[3]);
      if (!restoring) {
        base = { cols: step[2], rows: step[3] };
        history = [];
        snapshot = { cols: step[2], rows: step[3], received: [] };
      }
      restoring = true;
      snapshot!.received.push(bytes.slice());
      bytes.fill(0xee);
    } else if (step[0] === 'resize') {
      ring.noteResize(step[1], step[2], step[1] % 2 === 0);
      history.push({ kind: 'resize', cols: step[1], rows: step[2], noReflow: step[1] % 2 === 0 });
    } else {
      ring.noteReset();
      history.push({ kind: 'reset' });
    }
  }
  return { ring, base, history, evicted: longestSuffixWithinBudget(history), snapshot };
}

describe('the model fault-capture ring', () => {
  it.each<[string, Step[], boolean]>([
    ['writes past the byte cap', repeat(9, () => ['write', 64 * 1024]), false],
    ['one write larger than the byte cap', [['write', 16], ['write', MODEL_OP_RING_MAX_BYTES + 1024]], false],
    ['more ops than the op cap', repeat(MODEL_OP_RING_MAX_OPS + 10, (i) => ['resize', 100 + i, 24]), false],
    ['a resize pushed out by resets', [['resize', 120, 40], ...repeat(MODEL_OP_RING_MAX_OPS + 1, (): Step => ['reset'])], false],
    ['mixed ops across a byte-cap eviction', [['write', MODEL_OP_RING_MAX_BYTES - 8], ['resize', 90, 30], ['reset'], ['write', 64]], false],
    ['a new epoch', [['write', 1024], ['restore', 64, 100, 30], ['epoch', 90, 25]], false],
    ['a restore in chunks after earlier ops', [['write', 128], ['reset'], ['restore', 3, 134, 58], ['restore', 2, 134, 58], ['write', 16]], false],
    ['a restore that outgrows the snapshot cap', [['restore', MODEL_OP_RING_MAX_SNAPSHOT_BYTES - 4, 100, 30], ['restore', 100, 100, 30], ['restore', 50, 100, 30]], false],
    ['a second restore after other ops', [['restore', 10, 100, 30], ['write', 5], ['restore', 20, 120, 40], ['resize', 121, 40]], false],
    ['a capture over the record budget', [['restore', MODEL_OP_RING_MAX_SNAPSHOT_BYTES, 100, 30], ['resize', 90, 30], ['write', 1024], ['write', 2 * 1024 * 1024]], true],
    ['a long mixed session', mixed(3000), false],
  ])('retains what its budgets allow after %s', (_name, steps, overRecordBudget) => {
    const { ring, base, history, evicted, snapshot: restored } = playBoth(steps);
    const snapshot = restored && { ...restored, received: concat(restored.received) };
    const retained = history.slice(evicted);
    const dropped = history.slice(0, evicted);

    expect(ring.ops().map(({ t: _t, ...op }) => summarize(op))).toEqual(retained.map(summarize));
    expect(ring.stats()).toMatchObject({
      opCount: retained.length,
      retainedWriteBytes: writeBytes(retained),
      droppedOps: dropped.length,
      droppedWriteBytes: writeBytes(dropped),
    });

    const capture = ring.capture();
    const kept = Math.min(snapshot?.received.length ?? 0, MODEL_OP_RING_MAX_SNAPSHOT_BYTES);
    expect(capture.snapshot && {
      cols: capture.snapshot.cols,
      rows: capture.snapshot.rows,
      len: capture.snapshot.len,
      dropped: capture.snapshot.dropped,
      truncated: capture.snapshotTruncated,
    }).toEqual(snapshot && {
      cols: snapshot.cols,
      rows: snapshot.rows,
      len: kept,
      dropped: snapshot.received.length - kept,
      truncated: snapshot.received.length > kept,
    });

    const decoded = decodeModelFaultCapture(capture);
    expect(decoded.snapshot && fingerprint(decoded.snapshot)).toEqual(snapshot && fingerprint(snapshot.received.subarray(0, kept)));
    expect(capture.droppedForRecordBudget > 0).toBe(overRecordBudget);
    expect(capture.encodedBytesEstimate).toBeLessThanOrEqual(MODEL_FAULT_CAPTURE_MAX_BYTES);
    const recorded = retained.slice(capture.droppedForRecordBudget);
    expect(decoded.ops.map(({ t: _t, ...op }) => summarize(op))).toEqual(recorded.map(summarize));
    const start = lastResize(retained.slice(0, capture.droppedForRecordBudget)) ?? lastResize(dropped) ?? base;
    expect({ cols: capture.startCols, rows: capture.startRows }).toEqual({ cols: start.cols, rows: start.rows });
  });
});
