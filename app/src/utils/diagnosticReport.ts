import { invoke, isTauri } from '@tauri-apps/api/core';
import type { DaemonSupportSnapshot, DaemonSettings } from '../hooks/useDaemonSocket';
import { getPtyPerfSnapshot } from './ptyPerf';
import {
  readTerminalInputDiagnostics,
  supportTerminalGeometrySnapshot,
  supportTerminalDiagnosticsSnapshot,
} from './terminalDiagnosticsLog';
import { supportUiDiagnosticsSnapshot } from './uiDiagnosticsLog';
import { snapshotFrontendInputTrace, type FrontendInputTraceSnapshot } from './supportInputTrace';

export const DIAGNOSTIC_REPORT_SCHEMA = 'attn.support-report.v1';
const PANE_CONTENT_BYTE_LIMIT = 32 * 1024;
const REPORT_BYTE_LIMIT = 8 * 1024 * 1024;

export interface NativeInputObservation {
  sequence: number;
  atUnixMs: number;
  monotonicUs: number;
  eventClass: string;
  modifiers: number;
  repeat: boolean;
  windowNumber: number;
  windowFocused: boolean;
  responderCategory: string;
}

export interface NativeInputSnapshot {
  supported: boolean;
  os: string;
  arch: string;
  capacity: number;
  total: number;
  capturedAtUnixMs: number;
  observations: NativeInputObservation[];
}

export interface NativeFrontendInputMatch {
  nativeSequence?: number;
  frontendTraceId?: string;
  deltaMs?: number;
  confidence: 'high' | 'medium' | 'unmatched';
  reason: string;
}

export interface DiagnosticInputJourney {
  traceId: string;
  runtimeId?: string;
  startedAtUnixMs: number;
  terminalOutcome?: string;
  transportReady?: boolean;
  daemonWrite?: { result: string; durationUs: number; errorClass?: string };
  outputObservedAtUnixMs?: number;
  conclusion: string;
}

export interface DiagnosticPaneDescriptor {
  paneId: string;
  runtimeId: string;
  sessionId: string;
  title: string;
  sessionLabel: string;
  workspaceId: string;
  workspaceLabel: string;
  available: boolean;
}

export interface DiagnosticSessionSnapshot {
  id: string;
  label: string;
  state: string;
  agent: string;
  cwd: string;
  workspaceId: string;
  endpoint: 'local' | 'remote';
  endpointId?: string;
  active: boolean;
}

export interface DiagnosticWorkspaceSnapshot {
  id: string;
  label: string;
  directory: string;
  layout: unknown;
}

export interface DiagnosticCaptureContext {
  capturedAtUnixMs: number;
  view: string;
  activeSessionId: string | null;
  activePaneId: string | null;
  activeElement: string;
  documentFocused: boolean;
  visibility: string;
  window: { width: number; height: number; devicePixelRatio: number };
}

export interface PendingDiagnosticCapture {
  context: DiagnosticCaptureContext;
  panes: DiagnosticPaneDescriptor[];
  sessions: DiagnosticSessionSnapshot[];
  workspaces: DiagnosticWorkspaceSnapshot[];
  settings: Record<string, string>;
  frontendInput: FrontendInputTraceSnapshot;
  terminalGeometry: unknown;
  terminalDiagnostics: BoundedDiagnosticSnapshot;
  uiDiagnostics: BoundedDiagnosticSnapshot;
  pty: unknown;
  daemons: Promise<{ snapshots: DaemonSupportSnapshot[]; unavailableEndpoints: string[] }>;
  nativeInput: Promise<NativeInputSnapshot>;
  historicalInput: Promise<BoundedDiagnosticSnapshot>;
}

export interface BoundedDiagnosticSnapshot {
  capacity: number;
  total: number;
  capturedAtUnixMs: number;
  events: unknown[];
}

const ALLOWED_DIAGNOSTIC_KEYS = new Set([
  'action', 'active', 'activeElement', 'activeWrapperCount', 'activeWrappers', 'agent', 'app',
  'appStartedAt', 'at', 'attempt', 'backend', 'bail', 'base64Chars', 'blank',
  'browserComposing', 'browserHostOwners', 'build', 'canvas', 'capturedAtMonotonicMs',
  'capturedAtUnixMs', 'cellHeight', 'cellWidth', 'cellsArrayLen', 'center', 'ch',
  'clientHeight', 'clientWidth', 'clipping', 'cols', 'command', 'commit', 'composing',
  'compositionEndedAt', 'compositionStartedAt', 'connected', 'counts', 'cw', 'daemonWriteMs',
  'dataBytes', 'decodeCount', 'decodedBytes', 'decodeMs', 'delay', 'delayMs', 'display',
  'documentFocused', 'dpr', 'droppedForRecordBudget', 'droppedOps', 'durationMs', 'event',
  'extraCols', 'extraRows', 'fingerprint', 'flooredCols', 'flooredRows', 'focus', 'force',
  'fromCols', 'fromRows', 'generation', 'hasMeasuredSize', 'height', 'initialStateReceived',
  'isActivePane', 'keyClass', 'kind', 'lastCommandAt', 'lastCommandName', 'lastCommandRuntimeId',
  'lastEventAt', 'lastEventName', 'lastEventRuntimeId', 'lastEventSeq', 'lastInputAt',
  'lastListenerError', 'lastListenerErrorAt', 'lastPaintAt', 'lastPaintQuads', 'lastProbe',
  'lastPtyInputAt', 'lastPtyInputRuntimeId', 'lastPtyOutputAt', 'lastPtyOutputRuntimeId',
  'lastPtyOutputSeq', 'lastReceipt', 'lastRenderAt', 'lastResize', 'lastSend', 'lastWriteAt',
  'lateByMs', 'len', 'listenerErrorCount', 'model', 'modelPrintable', 'modifiers', 'noop',
  'observerId', 'offset', 'opacity', 'operation', 'outcome', 'overflowPx', 'paintedSinceResize',
  'pane', 'paneCount', 'paneIds', 'paneKind', 'pty', 'ptyInputBytes', 'ptyInputCount',
  'ptyJsonParseMs', 'ptyOutputBase64Chars', 'ptyOutputCount', 'quads', 'ready', 'reason',
  'reasons', 'recent', 'recentEvents', 'rect', 'rendererEpoch', 'repairAttempts', 'repeat',
  'restore', 'retainedWriteBytes', 'retries', 'rightOverflowPx', 'root', 'rootChildren', 'rows',
  'runtimeId', 'schema', 'seq', 'session', 'shouldRender', 'skipNull', 'skipZeroWidth', 'skipped',
  'snapshot', 'snapshotBytes', 'snapshotTruncated', 'socketState', 'source', 'splitCount', 'state',
  'success', 'syncActive', 'tag', 'terminalFocused', 'terminalWriteBytes', 'terminalWriteCallMs',
  'terminalWriteCount', 'toCols', 'toRows', 'transportReady', 'trigger', 'trusted', 'updatedAt',
  'version', 'view', 'visibility', 'visibilityState', 'visible', 'width', 'winInnerHeight',
  'winInnerWidth', 'window', 'workspace', 'wsJsonParseMs', 'wsMessageBytes', 'wsMessageCount',
  'x', 'y',
]);

export function sanitizeSupportDiagnostics(value: unknown, depth = 0, parentKey = ''): unknown {
  if (depth > 7 || value === null || typeof value === 'boolean' || typeof value === 'number') return value;
  if (typeof value === 'string') return value.length <= 240 ? value : `${value.slice(0, 240)}…`;
  if (Array.isArray(value)) {
    return value.slice(-400).map((entry) => sanitizeSupportDiagnostics(entry, depth + 1, parentKey));
  }
  if (typeof value !== 'object') return undefined;
  const sanitized: Record<string, unknown> = {};
  for (const [key, entry] of Object.entries(value)) {
    if (parentKey === 'counts') {
      if (/^[a-z_]+(?::[a-z_]+)?$/.test(key) && typeof entry === 'number') sanitized[key] = entry;
      continue;
    }
    if (!ALLOWED_DIAGNOSTIC_KEYS.has(key)) continue;
    const next = sanitizeSupportDiagnostics(entry, depth + 1, key);
    if (next !== undefined) sanitized[key] = next;
  }
  return sanitized;
}

function selectedSettings(settings: DaemonSettings): Record<string, string> {
  const keys = [
    'pty_backend_mode',
    'pty_shared_host_enabled',
    'pty_shared_host_active',
    'theme',
    'model_capture.enabled',
  ];
  return Object.fromEntries(keys.flatMap((key) => settings[key] === undefined ? [] : [[key, settings[key]]]));
}

function boundedDiagnostics(snapshot: { capacity: number; total: number; events: unknown[] }): BoundedDiagnosticSnapshot {
  return {
    capacity: snapshot.capacity,
    total: snapshot.total,
    capturedAtUnixMs: Date.now(),
    events: snapshot.events.map((event) => sanitizeSupportDiagnostics(event)),
  };
}

async function nativeInputSnapshot(): Promise<NativeInputSnapshot> {
  if (!isTauri()) {
    return { supported: false, os: 'browser', arch: 'unknown', capacity: 512, total: 0, capturedAtUnixMs: Date.now(), observations: [] };
  }
  try {
    return await invoke<NativeInputSnapshot>('native_input_diagnostics_snapshot');
  } catch {
    return { supported: false, os: 'unknown', arch: 'unknown', capacity: 512, total: 0, capturedAtUnixMs: Date.now(), observations: [] };
  }
}

async function historicalInputSnapshot(): Promise<BoundedDiagnosticSnapshot> {
  if (!isTauri()) return { capacity: 256, total: 0, capturedAtUnixMs: Date.now(), events: [] };
  try {
    const raw = await readTerminalInputDiagnostics();
    const events = raw.split('\n').flatMap((line) => {
      if (!line.trim()) return [];
      try {
        return [sanitizeSupportDiagnostics(JSON.parse(line))];
      } catch {
        return [];
      }
    });
    return { capacity: 256, total: events.length, capturedAtUnixMs: Date.now(), events: events.slice(-256) };
  } catch {
    return { capacity: 256, total: 0, capturedAtUnixMs: Date.now(), events: [] };
  }
}

export function beginDiagnosticCapture(input: {
  context: DiagnosticCaptureContext;
  panes: DiagnosticPaneDescriptor[];
  sessions: DiagnosticSessionSnapshot[];
  workspaces: DiagnosticWorkspaceSnapshot[];
  settings: DaemonSettings;
  sendSupportSnapshot: (endpointId?: string) => Promise<DaemonSupportSnapshot>;
}): PendingDiagnosticCapture {
  const endpointIds = [...new Set(input.sessions.flatMap((session) => session.endpointId ? [session.endpointId] : []))];
  const requestedEndpoints = [undefined, ...endpointIds];
  const daemonRequests = requestedEndpoints.map((endpointId) => input.sendSupportSnapshot(endpointId));
  return {
    context: { ...input.context },
    panes: input.panes.map((pane) => ({ ...pane })),
    sessions: input.sessions.map((session) => ({ ...session })),
    workspaces: input.workspaces.map((workspace) => ({ ...workspace })),
    settings: selectedSettings(input.settings),
    frontendInput: snapshotFrontendInputTrace(),
    terminalGeometry: sanitizeSupportDiagnostics(supportTerminalGeometrySnapshot()),
    terminalDiagnostics: boundedDiagnostics(supportTerminalDiagnosticsSnapshot()),
    uiDiagnostics: boundedDiagnostics(supportUiDiagnosticsSnapshot()),
    pty: sanitizeSupportDiagnostics(getPtyPerfSnapshot()),
    daemons: Promise.allSettled(daemonRequests).then((results) => ({
      snapshots: results.flatMap((result) => result.status === 'fulfilled' ? [result.value] : []),
      unavailableEndpoints: results.flatMap((result, index) => (
        result.status === 'rejected' ? [requestedEndpoints[index] ?? 'local'] : []
      )),
    })),
    nativeInput: nativeInputSnapshot(),
    historicalInput: historicalInputSnapshot(),
  };
}

function plainTerminalText(value: string): {
  text: string;
  chars: number;
  bytes: number;
  omittedChars: number;
  omittedBytes: number;
  truncatedAtStart: boolean;
} {
  const plain = value
    .replace(/\r\n?/g, '\n')
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/g, '');
  const encoder = new TextEncoder();
  const originalBytes = encoder.encode(plain).byteLength;
  if (originalBytes <= PANE_CONTENT_BYTE_LIMIT) {
    return { text: plain, chars: plain.length, bytes: originalBytes, omittedChars: 0, omittedBytes: 0, truncatedAtStart: false };
  }
  let low = 0;
  let high = plain.length;
  while (low < high) {
    const middle = Math.floor((low + high) / 2);
    if (encoder.encode(plain.slice(middle)).byteLength <= PANE_CONTENT_BYTE_LIMIT) high = middle;
    else low = middle + 1;
  }
  const start = low < plain.length && /[\uDC00-\uDFFF]/.test(plain[low]) ? low + 1 : low;
  const text = plain.slice(start);
  const bytes = encoder.encode(text).byteLength;
  return {
    text,
    chars: text.length,
    bytes,
    omittedChars: start,
    omittedBytes: originalBytes - bytes,
    truncatedAtStart: true,
  };
}

function buildDirtyPaths(): string[] {
  const encoded = import.meta.env.VITE_ATTN_SOURCE_DIRTY_PATHS_BASE64 || '';
  if (!encoded) return [];
  try {
    const bytes = Uint8Array.from(atob(encoded), (char) => char.charCodeAt(0));
    return new TextDecoder().decode(bytes).split('\0').filter(Boolean);
  } catch {
    return [];
  }
}

function newReportId(): string {
  return typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

export function correlateNativeAndFrontendInput(
  native: NativeInputSnapshot,
  frontend: FrontendInputTraceSnapshot,
): { matchWindowMs: number; highConfidenceWindowMs: number; matches: NativeFrontendInputMatch[] } {
  const matchWindowMs = 150;
  const highConfidenceWindowMs = 40;
  const frontendEvents = frontend.events
    .filter((event) => event.stage === 'document' && event.event === 'keydown')
    .sort((left, right) => left.atUnixMs - right.atUnixMs);
  const used = new Set<number>();
  const matches: NativeFrontendInputMatch[] = [];

  for (const observation of native.observations) {
    if (observation.eventClass !== 'key_down') {
      matches.push({ nativeSequence: observation.sequence, confidence: 'unmatched', reason: 'native_modifier_event' });
      continue;
    }
    const candidates = frontendEvents.flatMap((event, index) => {
      if (used.has(index)) return [];
      const deltaMs = event.atUnixMs - observation.atUnixMs;
      if (Math.abs(deltaMs) > matchWindowMs) return [];
      return [{ event, index, deltaMs, sameModifiers: event.modifiers === observation.modifiers }];
    }).sort((left, right) => (
      Number(right.sameModifiers) - Number(left.sameModifiers)
      || Math.abs(left.deltaMs) - Math.abs(right.deltaMs)
    ));
    const match = candidates[0];
    if (!match) {
      matches.push({ nativeSequence: observation.sequence, confidence: 'unmatched', reason: 'no_frontend_event_in_window' });
      continue;
    }
    used.add(match.index);
    matches.push({
      nativeSequence: observation.sequence,
      frontendTraceId: match.event.traceId,
      deltaMs: match.deltaMs,
      confidence: match.sameModifiers && Math.abs(match.deltaMs) <= highConfidenceWindowMs ? 'high' : 'medium',
      reason: match.sameModifiers ? 'time_order_and_modifiers' : 'time_order_only',
    });
  }
  frontendEvents.forEach((event, index) => {
    if (!used.has(index)) {
      matches.push({ frontendTraceId: event.traceId, confidence: 'unmatched', reason: 'no_native_event_in_window' });
    }
  });
  return { matchWindowMs, highConfidenceWindowMs, matches };
}

function ptyOutputEvents(value: unknown): Array<{ atUnixMs: number; runtimeId: string }> {
  if (!value || typeof value !== 'object') return [];
  const recentEvents = (value as { recentEvents?: unknown }).recentEvents;
  if (!Array.isArray(recentEvents)) return [];
  return recentEvents.flatMap((entry) => {
    if (!entry || typeof entry !== 'object') return [];
    const event = entry as Record<string, unknown>;
    const atUnixMs = typeof event.at === 'string' ? Date.parse(event.at) : Number.NaN;
    return event.kind === 'ws_event' && event.event === 'pty_output'
      && typeof event.runtimeId === 'string' && Number.isFinite(atUnixMs)
      ? [{ atUnixMs, runtimeId: event.runtimeId }]
      : [];
  });
}

export function deriveInputJourneys(
  frontend: FrontendInputTraceSnapshot,
  daemons: DaemonSupportSnapshot[],
  pty: unknown,
): DiagnosticInputJourney[] {
  const byTrace = new Map<string, typeof frontend.events>();
  for (const event of frontend.events) {
    const trace = byTrace.get(event.traceId) ?? [];
    trace.push(event);
    byTrace.set(event.traceId, trace);
  }
  const daemonByTrace = new Map(daemons.flatMap((daemon) => daemon.input_traces).map((trace) => [trace.trace_id, trace]));
  const outputs = ptyOutputEvents(pty);

  return [...byTrace.entries()].map(([traceId, events]) => {
    const documentEvent = events.find((event) => event.stage === 'document');
    const terminalEvent = events.find((event) => event.stage === 'terminal');
    const transportEvent = events.find((event) => event.stage === 'transport');
    const daemonEvent = daemonByTrace.get(traceId);
    const runtimeId = transportEvent?.runtimeId ?? terminalEvent?.runtimeId ?? daemonEvent?.runtime_id;
    const transportReady = transportEvent
      ? transportEvent.socketState === 1 && transportEvent.initialStateReceived === true
      : undefined;
    let conclusion: string;
    if (!terminalEvent) conclusion = 'document_observed_no_terminal_decision';
    else if (!transportEvent) conclusion = `terminal_${terminalEvent.outcome ?? 'observed'}_no_transport`;
    else if (!daemonEvent) conclusion = transportReady
      ? 'transport_sent_no_daemon_evidence'
      : 'transport_queued_while_unready';
    else if (daemonEvent.write_result !== 'accepted') conclusion = 'pty_write_failed';
    else conclusion = 'pty_write_succeeded_no_agent_evidence';

    const output = daemonEvent && runtimeId
      ? outputs.find((event) => event.runtimeId === runtimeId && event.atUnixMs >= daemonEvent.received_at_unix_ms)
      : undefined;
    if (output && daemonEvent?.write_result === 'accepted') conclusion = 'pty_write_succeeded_output_received';

    return {
      traceId,
      ...(runtimeId ? { runtimeId } : {}),
      startedAtUnixMs: documentEvent?.atUnixMs ?? events[0]?.atUnixMs ?? 0,
      ...(terminalEvent?.outcome ? { terminalOutcome: terminalEvent.outcome } : {}),
      ...(transportReady !== undefined ? { transportReady } : {}),
      ...(daemonEvent ? {
        daemonWrite: {
          result: daemonEvent.write_result,
          durationUs: daemonEvent.write_duration_us,
          ...(daemonEvent.error_class ? { errorClass: daemonEvent.error_class } : {}),
        },
      } : {}),
      ...(output ? { outputObservedAtUnixMs: output.atUnixMs } : {}),
      conclusion,
    };
  });
}

export async function createDiagnosticReport(
  capture: PendingDiagnosticCapture,
  selectedPaneIds: readonly string[],
  readPane: (paneId: string) => { text: string; available: boolean },
): Promise<Record<string, unknown>> {
  const [daemonCapture, nativeInput, historicalInput] = await Promise.all([
    capture.daemons,
    capture.nativeInput,
    capture.historicalInput,
  ]);
  const daemons = daemonCapture.snapshots;
  const selected = new Set(selectedPaneIds);
  const paneContent = capture.panes.flatMap((pane) => {
    if (!selected.has(pane.paneId)) return [];
    const result = readPane(pane.paneId);
    if (!result.available) {
      return [{ paneId: pane.paneId, runtimeId: pane.runtimeId, available: false }];
    }
    return [{
      paneId: pane.paneId,
      runtimeId: pane.runtimeId,
      sessionId: pane.sessionId,
      available: true,
      ...plainTerminalText(result.text),
    }];
  });

  const omissions: Array<{ section: string; reason: string; omittedBytes?: number; omittedCount?: number }> = [];
  if (daemons.length === 0) omissions.push({ section: 'daemons', reason: 'snapshots_unavailable' });
  for (const endpoint of daemonCapture.unavailableEndpoints) {
    omissions.push({ section: `daemons:${endpoint}`, reason: 'snapshot_unavailable' });
  }
  if (!nativeInput.supported) omissions.push({ section: 'nativeInput', reason: 'unsupported_or_unavailable' });
  if (historicalInput.events.length === 0) omissions.push({ section: 'historicalInput', reason: 'no_persisted_samples' });
  if (capture.frontendInput.total > capture.frontendInput.events.length) {
    omissions.push({
      section: 'frontendInput',
      reason: 'ring_entries_overwritten',
      omittedCount: capture.frontendInput.total - capture.frontendInput.events.length,
    });
  }
  if (nativeInput.total > nativeInput.observations.length) {
    omissions.push({
      section: 'nativeInput',
      reason: 'ring_entries_overwritten',
      omittedCount: nativeInput.total - nativeInput.observations.length,
    });
  }
  for (const daemon of daemons) {
    if (daemon.trace_total > daemon.input_traces.length) {
      omissions.push({
        section: `daemonInput:${daemon.endpoint_id ?? 'local'}`,
        reason: 'ring_entries_overwritten',
        omittedCount: daemon.trace_total - daemon.input_traces.length,
      });
    }
  }
  for (const [section, snapshot] of [
    ['terminalDiagnostics', capture.terminalDiagnostics],
    ['uiDiagnostics', capture.uiDiagnostics],
    ['historicalInput', historicalInput],
  ] as const) {
    if (snapshot.total > snapshot.events.length) {
      omissions.push({ section, reason: 'ring_entries_overwritten', omittedCount: snapshot.total - snapshot.events.length });
    }
  }
  for (const pane of paneContent) {
    if (!pane.available) omissions.push({ section: `paneContent:${pane.paneId}`, reason: 'pane_not_mounted' });
    else if ('truncatedAtStart' in pane && pane.truncatedAtStart && 'omittedBytes' in pane) {
      omissions.push({
        section: `paneContent:${pane.paneId}`,
        reason: 'truncated_at_start',
        omittedBytes: typeof pane.omittedBytes === 'number' ? pane.omittedBytes : undefined,
      });
    }
  }

  const createdAtUnixMs = Date.now();
  const sourceFingerprint = import.meta.env.VITE_ATTN_SOURCE_FINGERPRINT || 'unknown';

  const report: Record<string, unknown> = {
    schema: DIAGNOSTIC_REPORT_SCHEMA,
    reportId: newReportId(),
    createdAtUnixMs,
    capture: {
      origin: capture.context,
      confirmation: {
        capturedAtUnixMs: createdAtUnixMs,
        activeElement: typeof document === 'undefined' ? 'unknown' : document.activeElement?.tagName.toLowerCase() || 'none',
        documentFocused: typeof document !== 'undefined' && document.hasFocus(),
        visibility: typeof document === 'undefined' ? 'unknown' : document.visibilityState,
      },
    },
    consent: {
      reviewedAtUnixMs: createdAtUnixMs,
      paneContent: selected.size > 0 ? 'selected_panes' : 'metadata_only',
      selectedPaneIds: [...selected],
    },
    build: {
      version: import.meta.env.VITE_ATTN_BUILD_VERSION || 'unknown',
      sourceFingerprint,
      gitCommit: import.meta.env.VITE_ATTN_GIT_COMMIT || 'unknown',
      buildTime: import.meta.env.VITE_ATTN_BUILD_TIME || 'unknown',
      profile: import.meta.env.VITE_ATTN_BUILD_PROFILE || 'default',
      snapshotFormat: __ATTN_SNAPSHOT_FORMAT__,
      sourceDirty: sourceFingerprint.startsWith('tree:') ? true : sourceFingerprint.startsWith('git:') ? false : null,
      dirtyPaths: buildDirtyPaths(),
    },
    platform: {
      os: nativeInput.os,
      arch: nativeInput.arch,
      userAgent: navigator.userAgent,
      language: navigator.language,
    },
    app: {
      settings: capture.settings,
      sessions: capture.sessions,
      workspaces: capture.workspaces,
      panes: capture.panes,
    },
    daemons,
    diagnostics: {
      input: {
        native: nativeInput,
        frontend: capture.frontendInput,
        correlation: correlateNativeAndFrontendInput(nativeInput, capture.frontendInput),
        journeys: deriveInputJourneys(capture.frontendInput, daemons, capture.pty),
        historical: historicalInput,
      },
      terminal: {
        geometry: capture.terminalGeometry,
        sourceWindow: {
          capacity: capture.terminalDiagnostics.capacity,
          total: capture.terminalDiagnostics.total,
          retained: capture.terminalDiagnostics.events.length,
          capturedAtUnixMs: capture.terminalDiagnostics.capturedAtUnixMs,
        },
        lifecycle: capture.terminalDiagnostics.events.filter((event) => (
          (event as { kind?: unknown })?.kind !== 'incident'
          && (event as { kind?: unknown })?.kind !== 'model_fault'
        )),
        incidents: capture.terminalDiagnostics.events.filter((event) => (
          (event as { kind?: unknown })?.kind === 'incident'
          || (event as { kind?: unknown })?.kind === 'model_fault'
        )),
        performance: capture.pty,
      },
      ui: capture.uiDiagnostics,
    },
    paneContent,
    omissions,
    privacy: {
      metadataOnlyByDefault: true,
      selectedPaneIds: [...selected],
      excluded: [
        'environment variables', 'credentials', 'clipboard', 'raw prompt bodies', 'raw PTY bytes',
        'raw daemon logs', 'arbitrary errors and stacks', 'model fault replay captures',
      ],
    },
    limits: {
      reportBytes: REPORT_BYTE_LIMIT,
      paneContentBytes: PANE_CONTENT_BYTE_LIMIT,
      measuredBytes: 0,
    },
  };
  fitReportToSizeLimit(report, capture.context.activePaneId, omissions);
  return report;
}

function serializedReportBytes(report: Record<string, unknown>): number {
  return new TextEncoder().encode(JSON.stringify(report, null, 2)).byteLength + 1;
}

function fitReportToSizeLimit(
  report: Record<string, unknown>,
  affectedPaneId: string | null,
  omissions: Array<{ section: string; reason: string; omittedBytes?: number; omittedCount?: number }>,
): void {
  let bytes = serializedReportBytes(report);
  if (bytes > REPORT_BYTE_LIMIT) {
    const panes = report.paneContent as Array<Record<string, unknown>>;
    const omit = (pane: Record<string, unknown>) => {
      if (typeof pane.text !== 'string') return;
      const omittedBytes = typeof pane.bytes === 'number' ? pane.bytes : new TextEncoder().encode(pane.text).byteLength;
      delete pane.text;
      pane.contentIncluded = false;
      omissions.push({ section: `paneContent:${String(pane.paneId)}`, reason: 'report_size_limit', omittedBytes });
    };
    for (const pane of panes) {
      if (pane.paneId !== affectedPaneId) omit(pane);
    }
    bytes = serializedReportBytes(report);
    if (bytes > REPORT_BYTE_LIMIT) {
      const affected = panes.find((pane) => pane.paneId === affectedPaneId);
      if (affected) omit(affected);
      bytes = serializedReportBytes(report);
    }
  }
  const limits = report.limits as Record<string, unknown>;
  for (let attempt = 0; attempt < 3; attempt += 1) {
    limits.measuredBytes = bytes;
    const measured = serializedReportBytes(report);
    if (measured === bytes) break;
    bytes = measured;
  }
  if (bytes > REPORT_BYTE_LIMIT) throw new Error('Diagnostic metadata exceeds the report size limit');
}

function reportBaseName(now: Date): string {
  const stamp = now.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}Z$/, 'Z').replace('T', '-');
  return `attn-${stamp}.attn-report.json`;
}

export async function saveDiagnosticReport(report: Record<string, unknown>): Promise<string> {
  const contents = `${JSON.stringify(report, null, 2)}\n`;
  const [{ downloadDir, join }, { exists, writeTextFile }, { save }, { revealItemInDir }] = await Promise.all([
    import('@tauri-apps/api/path'),
    import('@tauri-apps/plugin-fs'),
    import('@tauri-apps/plugin-dialog'),
    import('@tauri-apps/plugin-opener'),
  ]);
  const downloads = await downloadDir();
  const baseName = reportBaseName(new Date());
  let target: string;
  try {
    let collisionError: unknown;
    target = '';
    for (let suffix = 1; suffix <= 1_000; suffix += 1) {
      const candidate = await join(
        downloads,
        suffix === 1 ? baseName : baseName.replace(/\.json$/, `-${suffix}.json`),
      );
      try {
        await writeTextFile(candidate, contents, { createNew: true });
        target = candidate;
        break;
      } catch (error) {
        if (!await exists(candidate)) throw error;
        collisionError = error;
      }
    }
    if (!target) throw collisionError ?? new Error('No unique diagnostic report filename is available');
  } catch (automaticSaveError) {
    const selectedTarget = await save({
      defaultPath: await join(downloads, baseName),
      filters: [{ name: 'attn diagnostic report', extensions: ['json'] }],
    });
    if (!selectedTarget) throw automaticSaveError;
    await writeTextFile(selectedTarget, contents);
    target = selectedTarget;
  }
  try {
    await revealItemInDir(target);
  } catch {
    // The report is already saved; Finder reveal is only a convenience.
  }
  return target;
}
