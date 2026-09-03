import { isTauri } from '@tauri-apps/api/core';
import type { ModelFaultCapture } from './ghosttyModelOpRing';

const DEBUG_DIR = 'debug';
const LIFECYCLE_FILE = `${DEBUG_DIR}/terminal-diagnostics.jsonl`;
export const TERMINAL_DIAGNOSTICS_FILE = `$APPLOCALDATA/${LIFECYCLE_FILE}`;
const INCIDENT_FILE = `${DEBUG_DIR}/terminal-incidents.jsonl`;
const STORAGE_KEY = 'attn:terminal-diagnostics';
const RING_LIMIT = 3000;
const INCIDENT_CONTEXT_EVENTS = 400;
export const FILE_SIZE_CAP_BYTES = 8 * 1024 * 1024;
const MIN_CONTENT_CELLS = 40;
const UNDERDRAW_RATIO = 0.25;
const WATCHDOG_DELAYS_MS = [1200, 3500];
const INCIDENT_COOLDOWN_MS = 8000;
const BOTTOM_CLIP_SWEEP_MS = 1500;
// Mirrors geometryOverflowsContainer's sub-pixel slack.
const BOTTOM_CLIP_SLACK_PX = 1;
// 3 sweeps ≈ 4.5s — beyond the longest observed legitimate attach-replay
// transient, so the watchdog never fights a clip that heals on its own.
const CLIP_REPAIR_AFTER_SWEEPS = 3;
const CLIP_REPAIR_BACKOFF_MS = [3000, 10000];
const CLIP_REPAIR_MAX_ATTEMPTS = 3;

export type DiagKind =
  | 'input'
  | 'pane_mount'
  | 'pane_unmount'
  | 'paint'
  | 'write'
  | 'resize'
  | 'reset'
  | 'layout'
  | 'focus'
  | 'attach'
  | 'desync'
  | 'watchdog'
  | 'incident'
  | 'recovery'
  | 'model_fault';

export interface DiagEvent {
  at: number;
  kind: DiagKind;
  pane?: string;
  session?: string;
  [key: string]: unknown;
}

// Paint/write must stay out of the disk stream: an active agent paints many
// times per second.
const LIFECYCLE_KINDS = new Set<DiagKind>([
  'input',
  'pane_mount',
  'pane_unmount',
  'resize',
  'reset',
  'layout',
  'focus',
  'attach',
  'desync',
  'watchdog',
  'incident',
  'recovery',
  'model_fault',
]);

export interface RenderProbe {
  cols: number;
  rows: number;
  modelPrintable: number;
  lastPaintAt: number;
  lastPaintQuads: number;
  // renderSurface refuses to draw an inactive session's panes, so judging one
  // "blank" is meaningless.
  active: boolean;
  // `null` means not measured yet.
  session?: string;
  isActivePane?: boolean | null;
  hasMeasuredSize?: boolean;
  cellWidth?: number | null;
  cellHeight?: number | null;
  clientWidth?: number | null;
  clientHeight?: number | null;
}

export interface TerminalGeometrySnapshot {
  pane: string;
  session?: string;
  active: boolean;
  isActivePane: boolean | null;
  hasMeasuredSize: boolean | null;
  cols: number;
  rows: number;
  cellWidth: number | null;
  cellHeight: number | null;
  clientWidth: number | null;
  clientHeight: number | null;
  flooredCols: number | null;
  flooredRows: number | null;
  overflowPx: number | null;
  rightOverflowPx: number | null;
  clipping: boolean;
}

interface PaneHealth {
  pane: string;
  session?: string;
  cols: number;
  rows: number;
  lastResizeAt: number;
  lastPaintAt: number;
  lastPaintQuads: number;
  lastModelPrintable: number;
  lastIncidentAt: number;
}

declare global {
  interface Window {
    __ATTN_TERMINAL_DIAG?: DiagEvent[];
    __ATTN_TERMINAL_DIAG_DUMP?: () => DiagEvent[];
    __ATTN_TERMINAL_DIAG_FILES?: { lifecycle: string; incidents: string };
    __ATTN_TERMINAL_DIAG_ENABLE?: (enabled: boolean) => void;
    __ATTN_TERMINAL_GEOMETRY?: () => TerminalGeometrySnapshot[];
    // Back-compat alias used by the split-blank e2e repro spec.
    __ATTN_RENDER_TRACE?: unknown[];
    __ATTN_RENDER_TRACE_ON?: boolean;
  }
}

const ring: DiagEvent[] = [];
let ringNextIndex = 0;
let ringWrapped = false;
const paneHealth = new Map<string, PaneHealth>();
const renderProbes = new Map<string, () => RenderProbe | null>();
const repairHandlers = new Map<string, () => void>();
const watchdogTimers = new Map<string, ReturnType<typeof setTimeout>[]>();
const clipState = new Map<string, boolean>();
const clipRepairState = new Map<
  string,
  { clippingSweeps: number; attempts: number; nextAttemptAtMs: number; gaveUp: boolean }
>();
let clipSweepTimer: ReturnType<typeof setInterval> | null = null;

let lifecycleBytes = 0;
let incidentBytes = 0;
// Seeded from the existing file on first touch: starting from 0 each launch
// would let the file grow far past the cap across sessions.
let lifecycleSizeSeeded = false;
let incidentSizeSeeded = false;
let fileWriteChain: Promise<void> = Promise.resolve();

const diagTextEncoder = typeof TextEncoder !== 'undefined' ? new TextEncoder() : null;
function byteLength(value: string): number {
  return diagTextEncoder ? diagTextEncoder.encode(value).length : value.length;
}

function isEnabled(): boolean {
  if (typeof window === 'undefined') {
    return false;
  }
  try {
    return window.localStorage.getItem(STORAGE_KEY) !== '0';
  } catch {
    return true;
  }
}

function ensureGlobals() {
  if (typeof window === 'undefined') {
    return;
  }
  window.__ATTN_TERMINAL_DIAG = ring;
  window.__ATTN_RENDER_TRACE = ring;
  window.__ATTN_RENDER_TRACE_ON = true;
  window.__ATTN_TERMINAL_DIAG_FILES = {
    lifecycle: `$APPLOCALDATA/${LIFECYCLE_FILE}`,
    incidents: `$APPLOCALDATA/${INCIDENT_FILE}`,
  };
  if (!window.__ATTN_TERMINAL_DIAG_DUMP) {
    window.__ATTN_TERMINAL_DIAG_DUMP = ringSnapshot;
  }
  if (!window.__ATTN_TERMINAL_DIAG_ENABLE) {
    window.__ATTN_TERMINAL_DIAG_ENABLE = (enabled: boolean) => {
      try {
        window.localStorage.setItem(STORAGE_KEY, enabled ? '1' : '0');
      } catch {
      }
    };
  }
  if (!window.__ATTN_TERMINAL_GEOMETRY) {
    window.__ATTN_TERMINAL_GEOMETRY = dumpTerminalGeometry;
  }
}

async function appendToFile(file: 'lifecycle' | 'incident', line: string) {
  if (!isTauri()) {
    return;
  }
  try {
    const { mkdir, stat, writeTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
    await mkdir(DEBUG_DIR, { baseDir: BaseDirectory.AppLocalData, recursive: true });
    const path = file === 'lifecycle' ? LIFECYCLE_FILE : INCIDENT_FILE;
    const seeded = file === 'lifecycle' ? lifecycleSizeSeeded : incidentSizeSeeded;
    if (!seeded) {
      try {
        const info = await stat(path, { baseDir: BaseDirectory.AppLocalData });
        const existing = typeof info?.size === 'number' ? info.size : 0;
        if (file === 'lifecycle') lifecycleBytes = existing; else incidentBytes = existing;
      } catch {
      }
      if (file === 'lifecycle') lifecycleSizeSeeded = true; else incidentSizeSeeded = true;
    }
    // Decided against the size the file WOULD reach: one record can be ~2MB, so deciding
    // afterwards overshoots the cap by that much.
    const bytes = file === 'lifecycle' ? lifecycleBytes : incidentBytes;
    const lineBytes = byteLength(line);
    const willReset = bytes + lineBytes > FILE_SIZE_CAP_BYTES;
    const marker = willReset ? `${JSON.stringify({ at: Date.now(), kind: 'rotate' })}\n` : '';
    await writeTextFile(path, willReset ? `${marker}${line}` : line, {
      baseDir: BaseDirectory.AppLocalData,
      append: !willReset,
      create: true,
    });
    const written = byteLength(marker) + lineBytes;
    if (file === 'lifecycle') {
      lifecycleBytes = willReset ? written : lifecycleBytes + written;
    } else {
      incidentBytes = willReset ? written : incidentBytes + written;
    }
  } catch (error) {
    console.warn('[TerminalDiag] write failed:', error);
  }
}

function enqueueWrite(file: 'lifecycle' | 'incident', line: string) {
  fileWriteChain = fileWriteChain.catch(() => {}).then(() => appendToFile(file, line));
}

export async function readTerminalInputDiagnostics(): Promise<string> {
  await fileWriteChain;
  const { exists, readTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
  const options = { baseDir: BaseDirectory.AppLocalData };
  if (!await exists(LIFECYCLE_FILE, options)) return '';
  const contents = await readTextFile(LIFECYCLE_FILE, options);
  const lines = contents.split('\n').filter((line) => {
    try {
      return JSON.parse(line)?.kind === 'input';
    } catch {
      return false;
    }
  });
  return lines.length ? `${lines.join('\n')}\n` : '';
}

function pushRing(event: DiagEvent) {
  if (ring.length < RING_LIMIT) {
    ring.push(event);
    return;
  }
  ring[ringNextIndex] = event;
  ringNextIndex = (ringNextIndex + 1) % RING_LIMIT;
  ringWrapped = true;
}

function ringSnapshot(): DiagEvent[] {
  if (!ringWrapped) {
    return [...ring];
  }
  return [...ring.slice(ringNextIndex), ...ring.slice(0, ringNextIndex)];
}

// The ring is copied wholesale into every later incident's `context`, so keeping
// a fault's ~2MB capture there would duplicate it once per incident.
function summarizeCapture(capture: ModelFaultCapture): Record<string, unknown> {
  return {
    inDiskRecordOnly: TERMINAL_DIAGNOSTICS_FILE,
    opCount: capture.opCount,
    retainedWriteBytes: capture.retainedWriteBytes,
    snapshotBytes: capture.snapshot?.len ?? 0,
    snapshotTruncated: capture.snapshotTruncated,
    droppedOps: capture.droppedOps,
    droppedForRecordBudget: capture.droppedForRecordBudget,
  };
}

export function recordDiag(event: Omit<DiagEvent, 'at'>): void {
  if (typeof window === 'undefined') {
    return;
  }
  ensureGlobals();
  if (!isEnabled()) {
    return;
  }
  const entry = { ...event, at: Date.now() } as DiagEvent;
  pushRing(
    entry.kind === 'input'
      ? { at: entry.at, kind: entry.kind, pane: entry.pane, session: entry.session,
        runtimeId: entry.runtimeId, reasons: entry.reasons, detailsIn: TERMINAL_DIAGNOSTICS_FILE }
      : entry.capture
      ? { ...entry, capture: summarizeCapture(entry.capture as ModelFaultCapture) }
      : entry,
  );
  if (LIFECYCLE_KINDS.has(entry.kind)) {
    enqueueWrite('lifecycle', `${JSON.stringify(entry)}\n`);
  }
}

// focusPane's retry loop calls this ~10x in a burst for the same pane.
const FOCUS_DEDUP_MS = 400;
let lastFocus: { pane?: string; at: number } = { at: 0 };

export function recordFocus(pane: string, retries: number): void {
  const now = Date.now();
  if (lastFocus.pane === pane && now - lastFocus.at < FOCUS_DEDUP_MS) {
    lastFocus.at = now;
    return;
  }
  lastFocus = { pane, at: now };
  recordDiag({ kind: 'focus', pane, retries });
}

const lastLayoutSig = new Map<string, string>();

export function recordLayout(workspace: string, paneIds: string[], splitCount: number): void {
  const sig = `${[...paneIds].sort().join(',')}|${splitCount}`;
  if (lastLayoutSig.get(workspace) === sig) {
    return;
  }
  lastLayoutSig.set(workspace, sig);
  recordDiag({ kind: 'layout', workspace, paneCount: paneIds.length, splitCount, paneIds });
}

export interface PaintSample {
  pane: string;
  session?: string;
  cols: number;
  rows: number;
  force: boolean;
  offset: number;
  modelPrintable: number;
  quads: number | null;
  cellsArrayLen: number | null;
  skipNull: number | null;
  skipZeroWidth: number | null;
}

export function recordPaint(sample: PaintSample): void {
  if (typeof window === 'undefined' || !isEnabled()) {
    ensureGlobals();
    return;
  }
  ensureGlobals();
  const now = Date.now();
  pushRing({ at: now, kind: 'paint', ...sample });

  // quads === null means the renderer SKIPPED the frame and left the canvas as
  // drawn. Folding that in as "0 quads" flags a painted idle pane as under-drawn.
  if (sample.quads === null) {
    return;
  }

  const health = paneHealth.get(sample.pane) ?? {
    pane: sample.pane,
    session: sample.session,
    cols: sample.cols,
    rows: sample.rows,
    lastResizeAt: 0,
    lastPaintAt: 0,
    lastPaintQuads: 0,
    lastModelPrintable: 0,
    lastIncidentAt: 0,
  };
  health.cols = sample.cols;
  health.rows = sample.rows;
  health.lastPaintAt = now;
  health.lastPaintQuads = sample.quads;
  health.lastModelPrintable = sample.modelPrintable;
  health.session = sample.session ?? health.session;
  paneHealth.set(sample.pane, health);

  const quads = sample.quads;
  const underdrawn = sample.modelPrintable >= MIN_CONTENT_CELLS
    && quads < sample.modelPrintable * UNDERDRAW_RATIO;
  const droppedCells = (sample.skipZeroWidth ?? 0) + (sample.skipNull ?? 0)
    >= sample.modelPrintable * UNDERDRAW_RATIO && sample.modelPrintable >= MIN_CONTENT_CELLS;
  if (underdrawn || droppedCells) {
    maybeFlushIncident(sample.pane, underdrawn ? 'paint_underdraw' : 'paint_dropped_cells', {
      modelPrintable: sample.modelPrintable,
      quads,
      skipNull: sample.skipNull,
      skipZeroWidth: sample.skipZeroWidth,
      cellsArrayLen: sample.cellsArrayLen,
      cols: sample.cols,
      rows: sample.rows,
      force: sample.force,
    });
  }
}

export function registerRenderProbe(
  pane: string,
  probe: () => RenderProbe | null,
  repair?: () => void,
): () => void {
  renderProbes.set(pane, probe);
  if (repair) {
    repairHandlers.set(pane, repair);
  }
  ensureClipSweep();
  return () => {
    renderProbes.delete(pane);
    repairHandlers.delete(pane);
    clipState.delete(pane);
    clipRepairState.delete(pane);
    stopClipSweepIfIdle();
  };
}

export function noteResize(
  pane: string,
  info: { session?: string; source: string; fromCols?: number; fromRows?: number; toCols?: number; toRows?: number; bail?: string; noop?: boolean; paneKind?: string; restore?: boolean; cw?: number; ch?: number },
): void {
  recordDiag({ kind: 'resize', pane, ...info });
  // A WebGL canvas clears its drawing buffer only when its pixel dimensions
  // change, so arming the watchdog on a no-op resize produces false blanks.
  const geometryChanged =
    info.bail === undefined &&
    !info.noop &&
    !info.restore &&
    info.toCols != null &&
    info.toRows != null &&
    (info.fromCols !== info.toCols || info.fromRows !== info.toRows);
  if (!geometryChanged) {
    return;
  }
  const health = paneHealth.get(pane);
  if (health) {
    health.lastResizeAt = Date.now();
  }
  if (info.paneKind === 'agent') {
    armWatchdog(pane, info.session);
  }
}

export function noteRecovery(
  pane: string,
  info: {
    session?: string;
    paneKind?: string;
    attempt: number;
    outcome: 'contextLost' | 'constructFailed' | 'scheduled' | 'recovered' | 'giveUp' | 'modelFault';
    delayMs?: number;
    error?: string;
  },
): void {
  recordDiag({ kind: 'recovery', pane, ...info });
}

// `capture` makes the record a replayable repro:
//   node app/scripts/replay-ghostty-model-fault.mjs <terminal-diagnostics.jsonl>
export function noteModelFault(
  pane: string,
  info: {
    session?: string;
    paneKind?: string;
    operation: string;
    error: string;
    stack?: string;
    model: number;
    cols?: number;
    rows?: number;
    rendererEpoch: number;
    capture?: ModelFaultCapture;
  },
): void {
  recordDiag({ kind: 'model_fault', pane, ...info });
}

function armWatchdog(pane: string, session?: string) {
  clearWatchdog(pane);
  const timers = WATCHDOG_DELAYS_MS.map((delay) => setTimeout(() => runWatchdog(pane, session, delay), delay));
  watchdogTimers.set(pane, timers);
}

function clearWatchdog(pane: string) {
  const timers = watchdogTimers.get(pane);
  if (timers) {
    timers.forEach((t) => clearTimeout(t));
    watchdogTimers.delete(pane);
  }
}

function runWatchdog(pane: string, session: string | undefined, delay: number) {
  const probe = renderProbes.get(pane)?.();
  if (!probe) {
    return;
  }
  if (!probe.active) {
    // Hidden panes cannot paint by design; note the skip, but do not judge.
    recordDiag({ kind: 'watchdog', pane, session, delay, skipped: 'inactive' });
    return;
  }
  const health = paneHealth.get(pane);
  const resizeAt = health?.lastResizeAt ?? 0;
  const paintedSinceResize = probe.lastPaintAt >= resizeAt;
  const blank = probe.modelPrintable >= MIN_CONTENT_CELLS
    && (!paintedSinceResize || probe.lastPaintQuads < probe.modelPrintable * UNDERDRAW_RATIO);
  recordDiag({
    kind: 'watchdog',
    pane,
    session,
    delay,
    blank,
    modelPrintable: probe.modelPrintable,
    lastPaintQuads: probe.lastPaintQuads,
    paintedSinceResize,
    cols: probe.cols,
    rows: probe.rows,
  });
  if (blank) {
    maybeFlushIncident(pane, 'blank_after_resize', {
      delay,
      modelPrintable: probe.modelPrintable,
      lastPaintQuads: probe.lastPaintQuads,
      paintedSinceResize,
      cols: probe.cols,
      rows: probe.rows,
    });
  }
}

function maybeFlushIncident(pane: string, reason: string, detail: Record<string, unknown>) {
  const health = paneHealth.get(pane);
  const now = Date.now();
  if (health && now - health.lastIncidentAt < INCIDENT_COOLDOWN_MS) {
    return;
  }
  if (health) {
    health.lastIncidentAt = now;
  }
  recordTerminalIncident(pane, health?.session, reason, detail, now);
}

export function recordTerminalIncident(
  pane: string,
  session: string | undefined,
  reason: string,
  detail: Record<string, unknown>,
  now = Date.now(),
): void {
  if (typeof window === 'undefined' || !isEnabled()) {
    ensureGlobals();
    return;
  }
  ensureGlobals();
  const marker: DiagEvent = { at: now, kind: 'incident', pane, session, reason, ...detail };
  pushRing(marker);
  enqueueWrite('lifecycle', `${JSON.stringify(marker)}\n`);
  const record = {
    at: now,
    kind: 'incident',
    pane,
    session,
    reason,
    detail,
    context: ringSnapshot().slice(-INCIDENT_CONTEXT_EVENTS),
  };
  enqueueWrite('incident', `${JSON.stringify(record)}\n`);
}

// Event names retain `bottom_clip` for compatibility with existing incident logs.

function bottomClipOverflowPx(probe: RenderProbe): number | null {
  const cellHeight = probe.cellHeight ?? 0;
  const clientHeight = probe.clientHeight ?? 0;
  if (cellHeight <= 0 || clientHeight <= 0 || probe.rows <= 0) {
    return null;
  }
  return probe.rows * cellHeight - clientHeight;
}

function rightClipOverflowPx(probe: RenderProbe): number | null {
  const cellWidth = probe.cellWidth ?? 0;
  const clientWidth = probe.clientWidth ?? 0;
  if (cellWidth <= 0 || clientWidth <= 0 || probe.cols <= 0) {
    return null;
  }
  return probe.cols * cellWidth - clientWidth;
}

function recordBottomClipIncident(pane: string, detail: Record<string, unknown>): void {
  const now = Date.now();
  const session = typeof detail.session === 'string' ? detail.session : undefined;
  const marker: DiagEvent = { at: now, kind: 'incident', pane, session, reason: 'bottom_clip', ...detail };
  pushRing(marker);
  enqueueWrite('lifecycle', `${JSON.stringify(marker)}\n`);
  const record = {
    at: now,
    kind: 'incident',
    pane,
    session,
    reason: 'bottom_clip',
    detail,
    context: ringSnapshot().slice(-INCIDENT_CONTEXT_EVENTS),
  };
  enqueueWrite('incident', `${JSON.stringify(record)}\n`);
}

function recordBottomClipRepairIncident(
  pane: string,
  reason: 'bottom_clip_repair' | 'bottom_clip_repair_gave_up',
  detail: Record<string, unknown>,
  session?: string,
): void {
  const now = Date.now();
  const marker: DiagEvent = { at: now, kind: 'incident', pane, session, reason, ...detail };
  pushRing(marker);
  enqueueWrite('lifecycle', `${JSON.stringify(marker)}\n`);
  const record = {
    at: now,
    kind: 'incident',
    pane,
    session,
    reason,
    detail,
    context: ringSnapshot().slice(-INCIDENT_CONTEXT_EVENTS),
  };
  enqueueWrite('incident', `${JSON.stringify(record)}\n`);
}

function sweepBottomClip(): void {
  if (typeof window === 'undefined' || !isEnabled()) {
    return;
  }
  for (const [pane, probeFn] of renderProbes) {
    let probe: RenderProbe | null = null;
    try {
      probe = probeFn();
    } catch {
      probe = null;
    }
    const wasClipping = clipState.get(pane) ?? false;
    // An inactive pane legitimately holds the daemon's geometry until it
    // re-fits, and its container is display:none (height 0).
    if (!probe || !probe.active) {
      if (wasClipping) clipState.set(pane, false);
      clipRepairState.delete(pane);
      continue;
    }
    const overflowPx = bottomClipOverflowPx(probe);
    const rightOverflowPx = rightClipOverflowPx(probe);
    if (overflowPx == null && rightOverflowPx == null) {
      clipRepairState.delete(pane);
      continue;
    }
    const trigger: string[] = [];
    if (overflowPx != null && overflowPx > BOTTOM_CLIP_SLACK_PX) trigger.push('model');
    if (rightOverflowPx != null && rightOverflowPx > BOTTOM_CLIP_SLACK_PX) trigger.push('model_right');
    const clipping = trigger.length > 0;
    const cellHeight = probe.cellHeight ?? 0;
    const cellWidth = probe.cellWidth ?? 0;
    const clientHeight = probe.clientHeight ?? 0;
    const clientWidth = probe.clientWidth ?? 0;
    const flooredRows = cellHeight > 0 ? Math.floor(clientHeight / cellHeight) : 0;
    const flooredCols = cellWidth > 0 ? Math.floor(clientWidth / cellWidth) : 0;

    if (!clipping) {
      const priorState = clipRepairState.get(pane);
      clipRepairState.delete(pane);
      if (clipping === wasClipping) {
        continue;
      }
      clipState.set(pane, clipping);
      recordDiag({
        kind: 'incident',
        pane,
        session: probe.session,
        reason: 'bottom_clip_resolved',
        rows: probe.rows,
        cols: probe.cols,
        flooredRows,
        flooredCols,
        overflowPx: overflowPx == null ? null : Math.round(overflowPx),
        rightOverflowPx: rightOverflowPx == null ? null : Math.round(rightOverflowPx),
        cellHeight,
        cellWidth,
        clientHeight,
        clientWidth,
        repairAttempts: priorState?.attempts ?? 0,
      });
      continue;
    }

    clipState.set(pane, clipping);
    if (clipping !== wasClipping) {
      recordBottomClipIncident(pane, {
        rows: probe.rows,
        cols: probe.cols,
        flooredRows,
        flooredCols,
        extraRows: probe.rows - flooredRows,
        extraCols: probe.cols - flooredCols,
        overflowPx: overflowPx == null ? null : Math.round(overflowPx),
        rightOverflowPx: rightOverflowPx == null ? null : Math.round(rightOverflowPx),
        cellHeight,
        cellWidth,
        clientHeight,
        clientWidth,
        hasMeasuredSize: probe.hasMeasuredSize ?? null,
        isActivePane: probe.isActivePane ?? null,
        session: probe.session,
        trigger,
        dpr: window.devicePixelRatio,
        winInnerWidth: window.innerWidth,
        winInnerHeight: window.innerHeight,
      });
    }

    const repair = repairHandlers.get(pane);
    const state = clipRepairState.get(pane) ?? {
      clippingSweeps: 0,
      attempts: 0,
      nextAttemptAtMs: 0,
      gaveUp: false,
    };
    state.clippingSweeps += 1;
    clipRepairState.set(pane, state);

    if (
      repair
      && document.visibilityState === 'visible'
      && state.clippingSweeps >= CLIP_REPAIR_AFTER_SWEEPS
      && !state.gaveUp
      && Date.now() >= state.nextAttemptAtMs
    ) {
      if (state.attempts >= CLIP_REPAIR_MAX_ATTEMPTS) {
        state.gaveUp = true;
        recordBottomClipRepairIncident(pane, 'bottom_clip_repair_gave_up', {
          rows: probe.rows,
          cols: probe.cols,
          attempts: state.attempts,
        }, probe.session);
      } else {
        state.attempts += 1;
        const backoff = CLIP_REPAIR_BACKOFF_MS[state.attempts - 1] ?? CLIP_REPAIR_BACKOFF_MS[CLIP_REPAIR_BACKOFF_MS.length - 1];
        state.nextAttemptAtMs = Date.now() + backoff;
        recordBottomClipRepairIncident(pane, 'bottom_clip_repair', {
          attempt: state.attempts,
          rows: probe.rows,
          cols: probe.cols,
          overflowPx: overflowPx == null ? null : Math.round(overflowPx),
          rightOverflowPx: rightOverflowPx == null ? null : Math.round(rightOverflowPx),
        }, probe.session);
        try {
          repair();
        } catch {
          // The next sweep re-evaluates; one handler must not break the loop.
        }
      }
    }
  }
}

function ensureClipSweep(): void {
  if (typeof window === 'undefined' || clipSweepTimer != null) {
    return;
  }
  clipSweepTimer = setInterval(sweepBottomClip, BOTTOM_CLIP_SWEEP_MS);
}

function stopClipSweepIfIdle(): void {
  if (clipSweepTimer != null && renderProbes.size === 0) {
    clearInterval(clipSweepTimer);
    clipSweepTimer = null;
  }
}

export function dumpTerminalGeometry(): TerminalGeometrySnapshot[] {
  const snapshots: TerminalGeometrySnapshot[] = [];
  for (const [pane, probeFn] of renderProbes) {
    let probe: RenderProbe | null = null;
    try {
      probe = probeFn();
    } catch {
      probe = null;
    }
    if (!probe) {
      continue;
    }
    const cellHeight = probe.cellHeight ?? null;
    const cellWidth = probe.cellWidth ?? null;
    const clientHeight = probe.clientHeight ?? null;
    const clientWidth = probe.clientWidth ?? null;
    const overflowPx = cellHeight && clientHeight ? probe.rows * cellHeight - clientHeight : null;
    const rightOverflowPx = cellWidth && clientWidth ? probe.cols * cellWidth - clientWidth : null;
    snapshots.push({
      pane,
      session: probe.session,
      active: probe.active,
      isActivePane: probe.isActivePane ?? null,
      hasMeasuredSize: probe.hasMeasuredSize ?? null,
      cols: probe.cols,
      rows: probe.rows,
      cellWidth,
      cellHeight,
      clientWidth,
      clientHeight,
      flooredCols: cellWidth && clientWidth ? Math.floor(clientWidth / cellWidth) : null,
      flooredRows: cellHeight && clientHeight ? Math.floor(clientHeight / cellHeight) : null,
      overflowPx: overflowPx == null ? null : Math.round(overflowPx),
      rightOverflowPx: rightOverflowPx == null ? null : Math.round(rightOverflowPx),
      clipping: (overflowPx != null && overflowPx > BOTTOM_CLIP_SLACK_PX)
        || (rightOverflowPx != null && rightOverflowPx > BOTTOM_CLIP_SLACK_PX),
    });
  }
  enqueueWrite('incident', `${JSON.stringify({ at: Date.now(), kind: 'geometry_dump', snapshots })}\n`);
  if (typeof console !== 'undefined' && typeof console.table === 'function') {
    console.table(snapshots);
  }
  return snapshots;
}

export function disposePaneDiagnostics(pane: string): void {
  clearWatchdog(pane);
  paneHealth.delete(pane);
  renderProbes.delete(pane);
  repairHandlers.delete(pane);
  clipState.delete(pane);
  clipRepairState.delete(pane);
  stopClipSweepIfIdle();
}

ensureGlobals();
