// Every retained write is a COPY: the bytes are views into buffers the app does
// not own and are written later on an async chain.

export type ModelOp =
  | { t: number; kind: 'write'; bytes: Uint8Array }
  | { t: number; kind: 'resize'; cols: number; rows: number; noReflow: boolean }
  | { t: number; kind: 'reset' };

// Tripwire: measured live PTY chunks run 1–16 KB.
export const MODEL_OP_RING_MAX_BYTES = 512 * 1024;
// Tripwire: measured ~1 resize per frame during a divider drag, ~30s of it.
export const MODEL_OP_RING_MAX_OPS = 2048;
// Tripwire: measured over 106 real attach snapshots, p50 11.2 KB / max 16.0 KB;
// 32x the largest observed, so `snapshotTruncated` marks an abnormal capture.
export const MODEL_OP_RING_MAX_SNAPSHOT_BYTES = 512 * 1024;
// Tripwire: past the ≈1.37 MB base64 the caps above can produce.
export const MODEL_FAULT_CAPTURE_MAX_BYTES = 2 * 1024 * 1024;

export const MODEL_FAULT_CAPTURE_VERSION = 1;

export type EncodedModelOp =
  | { t: number; kind: 'write'; b64: string; len: number }
  | { t: number; kind: 'resize'; cols: number; rows: number; noReflow: boolean }
  | { t: number; kind: 'reset' };

export interface EncodedModelSnapshot {
  cols: number;
  rows: number;
  b64: string;
  len: number;
  dropped: number;
  /** A PREFIX of what the model received, so a replay diverges and is refused. */
  truncated: boolean;
}

export interface ModelFaultCapture {
  version: number;
  epochStartedAt: number;
  /** Geometry at the start of the RETAINED history, not of the model. */
  startCols: number | null;
  startRows: number | null;
  snapshot: EncodedModelSnapshot | null;
  snapshotTruncated: boolean;
  ops: EncodedModelOp[];
  opCount: number;
  retainedWriteBytes: number;
  droppedOps: number;
  droppedWriteBytes: number;
  droppedForRecordBudget: number;
  encodedBytesEstimate: number;
}

export interface GhosttyModelOpRing {
  beginEpoch(cols: number, rows: number): void;
  noteWrite(bytes: Uint8Array): void;
  noteRestoreChunk(bytes: Uint8Array, cols: number, rows: number): void;
  noteResize(cols: number, rows: number, noReflow: boolean): void;
  noteReset(): void;
  clear(): void;
  ops(): ModelOp[];
  stats(): {
    opCount: number;
    retainedWriteBytes: number;
    droppedOps: number;
    droppedWriteBytes: number;
    snapshotBytes: number;
    snapshotTruncated: boolean;
  };
  capture(): ModelFaultCapture;
}

type RetainedOp =
  | Exclude<ModelOp, { kind: 'write' }>
  | { t: number; kind: 'write'; start: number; length: number; oversized: Uint8Array | null };

interface SnapshotState {
  cols: number;
  rows: number;
  chunks: Uint8Array[];
  bytes: number;
  dropped: number;
  truncated: boolean;
}

const WRITE_OP_OVERHEAD_BYTES = 56;
const RESIZE_OP_OVERHEAD_BYTES = 84;
const RESET_OP_OVERHEAD_BYTES = 32;
const CAPTURE_ENVELOPE_BYTES = 512;

function base64Length(byteLength: number): number {
  return Math.ceil(byteLength / 3) * 4;
}

// Spreading a 512 KB string into btoa blows the argument limit; chunk instead.
const BASE64_CHUNK = 8192;

export function bytesToBase64(bytes: Uint8Array): string {
  let binary = '';
  for (let offset = 0; offset < bytes.length; offset += BASE64_CHUNK) {
    const chunk = bytes.subarray(offset, offset + BASE64_CHUNK);
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}

export function createGhosttyModelOpRing(options?: { now?: () => number }): GhosttyModelOpRing {
  const now = options?.now ?? (() => Date.now());
  const slots: Array<RetainedOp | undefined> = Array.from({ length: MODEL_OP_RING_MAX_OPS }, () => undefined);
  let writeArena: Uint8Array | null = null;
  let writeArenaEnd = 0;
  let head = 0;
  let count = 0;
  let retainedWriteBytes = 0;
  let droppedOps = 0;
  let droppedWriteBytes = 0;
  let startCols: number | null = null;
  let startRows: number | null = null;
  let epochStartedAt = now();
  let snapshot: SnapshotState | null = null;
  let restoreInProgress = false;

  const evictOldest = () => {
    const op = slots[head];
    slots[head] = undefined;
    head = (head + 1) % MODEL_OP_RING_MAX_OPS;
    count -= 1;
    droppedOps += 1;
    if (op?.kind === 'write') {
      retainedWriteBytes -= op.length;
      droppedWriteBytes += op.length;
    }
    if (op?.kind === 'resize') {
      startCols = op.cols;
      startRows = op.rows;
    }
  };

  const push = (op: RetainedOp) => {
    if (count === MODEL_OP_RING_MAX_OPS) {
      evictOldest();
    }
    slots[(head + count) % MODEL_OP_RING_MAX_OPS] = op;
    count += 1;
    if (op.kind === 'write') {
      retainedWriteBytes += op.length;
    }
    while (retainedWriteBytes > MODEL_OP_RING_MAX_BYTES && count > 1) {
      evictOldest();
    }
  };

  const copyToArena = (bytes: Uint8Array) => {
    writeArena ??= new Uint8Array(MODEL_OP_RING_MAX_BYTES);
    const roomBeforeWrap = MODEL_OP_RING_MAX_BYTES - writeArenaEnd;
    if (bytes.length <= roomBeforeWrap) {
      writeArena.set(bytes, writeArenaEnd);
    } else {
      writeArena.set(bytes.subarray(0, roomBeforeWrap), writeArenaEnd);
      writeArena.set(bytes.subarray(roomBeforeWrap), 0);
    }
    writeArenaEnd = (writeArenaEnd + bytes.length) % MODEL_OP_RING_MAX_BYTES;
  };

  const retainedBytes = (op: Extract<RetainedOp, { kind: 'write' }>): Uint8Array => {
    if (op.oversized) return op.oversized;
    const arena = writeArena as Uint8Array;
    const end = op.start + op.length;
    if (end <= MODEL_OP_RING_MAX_BYTES) return arena.slice(op.start, end);
    const bytes = new Uint8Array(op.length);
    bytes.set(arena.subarray(op.start), 0);
    bytes.set(arena.subarray(0, end - MODEL_OP_RING_MAX_BYTES), MODEL_OP_RING_MAX_BYTES - op.start);
    return bytes;
  };

  const reset = (cols: number | null, rows: number | null) => {
    slots.fill(undefined);
    head = 0;
    count = 0;
    retainedWriteBytes = 0;
    droppedOps = 0;
    droppedWriteBytes = 0;
    startCols = cols;
    startRows = rows;
    snapshot = null;
    restoreInProgress = false;
    epochStartedAt = now();
  };

  return {
    beginEpoch(cols, rows) {
      reset(cols, rows);
    },

    clear() {
      reset(null, null);
    },

    noteWrite(bytes) {
      restoreInProgress = false;
      if (bytes.length === 0) return;
      if (bytes.length > MODEL_OP_RING_MAX_BYTES) {
        push({ t: now(), kind: 'write', start: 0, length: bytes.length, oversized: bytes.slice() });
        return;
      }
      push({ t: now(), kind: 'write', start: writeArenaEnd, length: bytes.length, oversized: null });
      copyToArena(bytes);
    },

    noteRestoreChunk(bytes, cols, rows) {
      if (!restoreInProgress) {
        reset(cols, rows);
        restoreInProgress = true;
        snapshot = { cols, rows, chunks: [], bytes: 0, dropped: 0, truncated: false };
      }
      const state = snapshot;
      if (!state) return;
      const room = MODEL_OP_RING_MAX_SNAPSHOT_BYTES - state.bytes;
      if (room <= 0) {
        state.dropped += bytes.length;
        state.truncated = true;
        return;
      }
      if (bytes.length > room) {
        state.chunks.push(bytes.slice(0, room));
        state.bytes += room;
        state.dropped += bytes.length - room;
        state.truncated = true;
        return;
      }
      state.chunks.push(bytes.slice());
      state.bytes += bytes.length;
    },

    noteResize(cols, rows, noReflow) {
      restoreInProgress = false;
      push({ t: now(), kind: 'resize', cols, rows, noReflow });
    },

    noteReset() {
      restoreInProgress = false;
      push({ t: now(), kind: 'reset' });
    },

    ops() {
      const out: ModelOp[] = [];
      for (let i = 0; i < count; i += 1) {
        const op = slots[(head + i) % MODEL_OP_RING_MAX_OPS];
        if (!op) continue;
        out.push(op.kind === 'write' ? { t: op.t, kind: 'write', bytes: retainedBytes(op) } : op);
      }
      return out;
    },

    stats() {
      return {
        opCount: count,
        retainedWriteBytes,
        droppedOps,
        droppedWriteBytes,
        snapshotBytes: snapshot?.bytes ?? 0,
        snapshotTruncated: snapshot?.truncated ?? false,
      };
    },

    capture() {
      const encodedSnapshot: EncodedModelSnapshot | null = snapshot
        ? {
            cols: snapshot.cols,
            rows: snapshot.rows,
            b64: bytesToBase64(concat(snapshot.chunks, snapshot.bytes)),
            len: snapshot.bytes,
            dropped: snapshot.dropped,
            truncated: snapshot.truncated,
          }
        : null;

      const encoded: EncodedModelOp[] = [];
      let budget = CAPTURE_ENVELOPE_BYTES + (encodedSnapshot ? encodedSnapshot.b64.length : 0);
      for (let i = 0; i < count; i += 1) {
        const op = slots[(head + i) % MODEL_OP_RING_MAX_OPS];
        if (!op) continue;
        if (op.kind === 'write') {
          encoded.push({ t: op.t, kind: 'write', b64: bytesToBase64(retainedBytes(op)), len: op.length });
          budget += base64Length(op.length) + WRITE_OP_OVERHEAD_BYTES;
        } else if (op.kind === 'resize') {
          encoded.push({ t: op.t, kind: 'resize', cols: op.cols, rows: op.rows, noReflow: op.noReflow });
          budget += RESIZE_OP_OVERHEAD_BYTES;
        } else {
          encoded.push({ t: op.t, kind: 'reset' });
          budget += RESET_OP_OVERHEAD_BYTES;
        }
      }

      let droppedForRecordBudget = 0;
      let captureStartCols = startCols;
      let captureStartRows = startRows;
      while (budget > MODEL_FAULT_CAPTURE_MAX_BYTES && encoded.length > 0) {
        const op = encoded.shift() as EncodedModelOp;
        droppedForRecordBudget += 1;
        if (op.kind === 'write') {
          budget -= base64Length(op.len) + WRITE_OP_OVERHEAD_BYTES;
        } else if (op.kind === 'resize') {
          budget -= RESIZE_OP_OVERHEAD_BYTES;
          captureStartCols = op.cols;
          captureStartRows = op.rows;
        } else {
          budget -= RESET_OP_OVERHEAD_BYTES;
        }
      }

      return {
        version: MODEL_FAULT_CAPTURE_VERSION,
        epochStartedAt,
        startCols: captureStartCols,
        startRows: captureStartRows,
        snapshot: encodedSnapshot,
        snapshotTruncated: encodedSnapshot?.truncated ?? false,
        ops: encoded,
        opCount: encoded.length,
        retainedWriteBytes,
        droppedOps,
        droppedWriteBytes,
        droppedForRecordBudget,
        encodedBytesEstimate: budget,
      };
    },
  };
}

function concat(chunks: Uint8Array[], total: number): Uint8Array {
  if (chunks.length === 1) return chunks[0];
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.length;
  }
  return out;
}
