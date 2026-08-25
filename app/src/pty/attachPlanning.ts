import type { PtyAttachPolicy } from './bridge';

export interface AttachRequestContext {
  requestedCols: number;
  requestedRows: number;
  policy: PtyAttachPolicy;
  shell: boolean;
  agent: string | null;
}

export interface AttachGhosttySnapshot {
  cols: number;
  rows: number;
  snapshot_b64: string;
  format?: string;
  scrollback_truncated?: boolean;
}

// An upgraded app is routinely offered bytes an older worker encoded.
// Design: docs/plans/2026-08-16-snapshot-format-skew.md
export const LOCAL_SNAPSHOT_FORMAT = __ATTN_SNAPSHOT_FORMAT__;

export function snapshotIsDecodable(
  snapshot: Pick<AttachGhosttySnapshot, 'format'> | null | undefined,
  localFormat: string = LOCAL_SNAPSHOT_FORMAT,
): boolean {
  if (!snapshot) return false;
  const format = typeof snapshot.format === 'string' ? snapshot.format.trim() : '';
  return format !== '' && format === localFormat;
}

export interface AttachRestoreData {
  cols?: number;
  rows?: number;
  snapshot?: AttachGhosttySnapshot;
}

export type AttachResultData = AttachRestoreData & {
  last_seq?: number;
};

export interface AttachRuntimeRequest {
  cols: number;
  rows: number;
  shell?: boolean;
  agent?: string | null;
}

export interface PendingAttachOutputChunk {
  data: string | Uint8Array;
  seq?: number;
}

export function enqueuePendingAttachOutput(
  queuedOutputs: PendingAttachOutputChunk[],
  chunk: PendingAttachOutputChunk,
  maxPendingOutputs: number,
): PendingAttachOutputChunk[] {
  const nextQueue = [...queuedOutputs];
  if (nextQueue.length >= maxPendingOutputs) {
    nextQueue.shift();
  }
  nextQueue.push(chunk);
  return nextQueue;
}

export function createAttachRequestContext(
  args: Pick<AttachRuntimeRequest, 'cols' | 'rows' | 'shell' | 'agent'>,
  policy: PtyAttachPolicy,
): AttachRequestContext {
  const normalizedAgent = normalizeAttachAgent(args.agent, args.shell);
  return {
    requestedCols: args.cols,
    requestedRows: args.rows,
    policy,
    shell: normalizedAgent === 'shell',
    agent: normalizedAgent,
  };
}

function normalizeAttachAgent(agent?: string | null, shell?: boolean): string | null {
  if (shell) {
    return 'shell';
  }
  if (typeof agent !== 'string') {
    return null;
  }
  const normalized = agent.trim().toLowerCase();
  return normalized.length > 0 ? normalized : null;
}

export function classifyAttachRestore(
  data: AttachRestoreData,
  context?: AttachRequestContext,
  localFormat: string = LOCAL_SNAPSHOT_FORMAT,
) {
  // A snapshot this build cannot decode is no snapshot: everything downstream of
  // hasSnapshot already handles that case.
  const ghosttySnapshot = data.snapshot
    && data.snapshot.snapshot_b64
    && snapshotIsDecodable(data.snapshot, localFormat)
    ? data.snapshot
    : null;
  const hasSnapshot = ghosttySnapshot !== null;
  const attachedCols = typeof data.cols === 'number' ? data.cols : null;
  const attachedRows = typeof data.rows === 'number' ? data.rows : null;
  const restoreCols = hasSnapshot ? ghosttySnapshot.cols : attachedCols;
  const restoreRows = hasSnapshot ? ghosttySnapshot.rows : attachedRows;

  return {
    agent: context?.agent ?? null,
    hasSnapshot,
    restoreCols,
    restoreRows,
  };
}

export function planAttachedRuntimeGeometry(
  args: AttachRuntimeRequest,
  attachResult: AttachRestoreData,
  options: {
    attachPolicy: PtyAttachPolicy;
    attachContext?: AttachRequestContext;
    requestedGeometryAuthoritative?: boolean;
  },
) {
  const restorePlan = classifyAttachRestore(attachResult, options.attachContext);
  const requestedCols = args.cols;
  const requestedRows = args.rows;
  const attachedCols = typeof attachResult.cols === 'number' ? attachResult.cols : null;
  const attachedRows = typeof attachResult.rows === 'number' ? attachResult.rows : null;
  const restoreCols = restorePlan.restoreCols;
  const restoreRows = restorePlan.restoreRows;
  const ptyGeometryMatches = attachedCols === requestedCols && attachedRows === requestedRows;
  const restoreGeometryMatches = restorePlan.hasSnapshot
    ? restoreCols === requestedCols && restoreRows === requestedRows
    : false;
  // A provisional client size must not claim PTY geometry authority: a construction-default
  // SIGWINCH churns the shell and bounces every attached model's width.
  const preserveAttachedGeometry = options.attachPolicy === 'relaunch_restore'
    || options.requestedGeometryAuthoritative === false;
  const resizeRequired = !preserveAttachedGeometry && !ptyGeometryMatches;

  return {
    requestedCols,
    requestedRows,
    attachedCols,
    attachedRows,
    restoreCols,
    restoreRows,
    ptyGeometryMatches,
    restoreGeometryMatches,
    hasSnapshot: restorePlan.hasSnapshot,
    agent: restorePlan.agent,
    resizeRequired,
    strategy: resizeRequired ? 'resize' : preserveAttachedGeometry && !ptyGeometryMatches ? 'preserve_attached' : 'none',
    attachPolicy: options.attachPolicy,
  };
}

export function planAttachResultEffects({
  attachResult,
  restorePlan,
  previousSeq,
  queuedOutputs,
}: {
  attachResult: AttachResultData;
  restorePlan: ReturnType<typeof classifyAttachRestore>;
  previousSeq?: number;
  queuedOutputs?: PendingAttachOutputChunk[];
}) {
  // Reset is only safe when a snapshot replaces the whole grid: without one the
  // client's model is the ONLY rendered terminal, and resetting leaves it blank.
  const shouldReset = restorePlan.hasSnapshot;
  const resetReason = shouldReset ? 'snapshot_restore' : null;
  const restoreAction = restorePlan.hasSnapshot && attachResult.snapshot?.snapshot_b64
    ? {
        kind: 'ghostty_snapshot' as const,
        data: attachResult.snapshot.snapshot_b64,
      }
    : {
        kind: 'none' as const,
      };

  // Without a snapshot the baseline is the client's OWN watermark: advancing to
  // last_seq would drop queued chunks the client never rendered.
  let nextSeq = restorePlan.hasSnapshot
    ? (typeof attachResult.last_seq === 'number' ? attachResult.last_seq : 0)
    : (typeof previousSeq === 'number' ? previousSeq : 0);
  const queuedOutputsToEmit: PendingAttachOutputChunk[] = [];
  for (const chunk of queuedOutputs || []) {
    if (typeof chunk.seq === 'number' && chunk.seq <= nextSeq) {
      continue;
    }
    if (typeof chunk.seq === 'number') {
      nextSeq = chunk.seq;
    }
    queuedOutputsToEmit.push(chunk);
  }

  return {
    shouldReset,
    resetReason,
    restoreAction,
    nextSeq,
    queuedOutputsToEmit,
  };
}

export function planLivePtyOutput({
  incomingSeq,
  lastSeq,
}: {
  incomingSeq?: number;
  lastSeq?: number;
}) {
  const shouldDropAsStale = typeof incomingSeq === 'number'
    && typeof lastSeq === 'number'
    && incomingSeq <= lastSeq;

  if (shouldDropAsStale) {
    return {
      shouldDropAsStale: true,
      nextSeq: lastSeq,
    };
  }

  return {
    shouldDropAsStale: false,
    nextSeq: typeof incomingSeq === 'number' ? incomingSeq : lastSeq,
  };
}
