import type { TerminalInputDiagnostic } from '../ghostty/input';
import { recordDiag } from './terminalDiagnosticsLog';
import { getPtyPerfSnapshot } from './ptyPerf';

const RECENT_LIMIT = 32;
const SAMPLE_INTERVAL_MS = 30_000;
const REASON_COOLDOWN_MS = 30_000;
const appStartedAt = Date.now();
let nextObserverId = 0;

export interface TerminalInputState {
  runtimeId?: string;
  sessionId?: string;
  paneId?: string;
  active: boolean;
  ready: boolean;
  model: boolean;
  surfaceReleased?: boolean;
  findOpen?: boolean;
  filterOpen?: boolean;
  lastWriteAt?: number;
  lastPaintAt?: number;
}

interface TraceDetail {
  event: string;
  [key: string]: unknown;
}

interface TraceEvent extends TraceDetail { at: number }

interface InputTrace {
  element: HTMLElement;
  state: () => TerminalInputState;
  note: (event: TraceDetail, urgent?: string) => void;
}

const traces = new Set<InputTrace>();
const documentListeners = new Map<Document, () => void>();

function focusKind(element: Element | null): string {
  if (!element) return 'none';
  if (element.closest('.ghostty-filter-input')) return 'terminal_filter';
  if (element.closest('.ghostty-find-input')) return 'terminal_find';
  if (element.closest('.terminal-container, .grid-view-stage')) return 'terminal';
  if (element.matches('input, textarea, [contenteditable="true"]')) return 'editor';
  if (element.matches('button, a, select')) return 'control';
  return element === element.ownerDocument.body ? 'body' : 'other';
}

function installDocumentListener(doc: Document): void {
  if (documentListeners.has(doc)) return;
  const keydown = (event: KeyboardEvent) => {
    const path = new Set(event.composedPath());
    for (const trace of traces) {
      if (trace.element.ownerDocument !== doc) continue;
      const inTerminal = path.has(trace.element);
      if (!inTerminal && !trace.state().active) continue;
      trace.note({
        event: 'document_keydown', inTerminal,
        target: focusKind(event.target instanceof Element ? event.target : null),
        trusted: event.isTrusted,
      }, inTerminal ? undefined : 'key_elsewhere');
    }
  };
  doc.addEventListener('keydown', keydown, true);
  documentListeners.set(doc, () => doc.removeEventListener('keydown', keydown, true));
}

export function observeTerminalInput(element: HTMLElement, state: () => TerminalInputState) {
  const recent: TraceEvent[] = [];
  const counts: Record<string, number> = {};
  const reported = new Map<string, number>();
  let lastPersistedAt = -Infinity;
  let scheduled = false;
  const pendingReasons = new Set<string>();
  let disposed = false;
  let composing = false;
  let compositionStartedAt: number | null = null;
  let compositionEndedAt: number | null = null;
  let lastSend: TraceEvent | null = null;
  let lastProbe: TraceEvent | null = null;
  let lastReceipt: TraceEvent | null = null;
  const observerId = ++nextObserverId;
  const doc = element.ownerDocument;

  const persist = () => {
    scheduled = false;
    if (disposed) return;
    const current = state();
    const pty = getPtyPerfSnapshot();
    const now = Date.now();
    lastPersistedAt = now;
    const reasons = pendingReasons.size ? [...pendingReasons] : ['sample'];
    pendingReasons.clear();
    recordDiag({
      kind: 'input', schema: 1, reasons, appStartedAt, observerId,
      pane: current.paneId, session: current.sessionId, runtimeId: current.runtimeId,
      build: {
        commit: import.meta.env.VITE_ATTN_GIT_COMMIT,
        fingerprint: import.meta.env.VITE_ATTN_SOURCE_FINGERPRINT,
        version: import.meta.env.VITE_ATTN_BUILD_VERSION,
      },
      state: current,
      focus: {
        terminalFocused: doc.activeElement === element,
        activeElement: focusKind(doc.activeElement),
        documentFocused: doc.hasFocus(),
        visibility: doc.visibilityState,
        connected: element.isConnected,
      },
      composing, compositionStartedAt, compositionEndedAt,
      counts: { ...counts }, recent: [...recent], lastSend, lastProbe, lastReceipt,
      pty: {
        recent: pty.recentEvents.filter((entry) => entry.runtimeId === current.runtimeId).slice(-12),
        listenerErrorCount: pty.listenerErrorCount,
        lastListenerErrorAt: pty.lastListenerErrorAt,
      },
    });
  };

  const note: InputTrace['note'] = (event, urgent) => {
    if (disposed) return;
    const now = Date.now();
    const counter = event.outcome ? `${event.event}:${event.outcome}` : event.event;
    counts[counter] = (counts[counter] ?? 0) + 1;
    if (recent.length === RECENT_LIMIT) recent.shift();
    const entry = { ...event, at: now, runtimeId: state().runtimeId };
    recent.push(entry);
    if (event.event === 'transport_send') {
      lastSend = entry;
      if (event.probeId) lastProbe = entry;
    }
    if (event.event === 'pty_receipt') lastReceipt = entry;
    const reportUrgent = urgent && now - (reported.get(urgent) ?? -Infinity) >= REASON_COOLDOWN_MS;
    if (reportUrgent) {
      pendingReasons.add(urgent);
      reported.set(urgent, now);
    }
    if (scheduled || (!reportUrgent && now - lastPersistedAt < SAMPLE_INTERVAL_MS)) return;
    scheduled = true;
    // Capture after dispatch so the document event and the terminal's decision share a record.
    queueMicrotask(persist);
  };

  const trace = { element, state, note };
  traces.add(trace);
  installDocumentListener(doc);
  const focus = () => note({ event: 'focus' }, 'focus');
  const blur = () => note({ event: 'blur' }, 'blur');
  element.addEventListener('focus', focus);
  element.addEventListener('blur', blur);
  note({ event: 'attached' });

  return {
    record(event: TerminalInputDiagnostic) {
      composing = event.composing;
      if (event.event === 'compositionstart') compositionStartedAt = Date.now();
      if (event.event === 'compositionend') compositionEndedAt = Date.now();
      const reason = event.outcome === 'composing' && !event.browserComposing && !event.legacyComposition
        ? 'composition_mismatch'
        : event.outcome === 'no_target' || event.outcome === 'error' ? event.outcome : undefined;
      note({ ...event }, reason);
    },
    dispose() {
      if (disposed) return;
      note({ event: 'disposed' });
      persist();
      disposed = true;
      traces.delete(trace);
      element.removeEventListener('focus', focus);
      element.removeEventListener('blur', blur);
      if (![...traces].some((entry) => entry.element.ownerDocument === doc)) {
        documentListeners.get(doc)?.();
        documentListeners.delete(doc);
      }
    },
  };
}

export function noteTerminalInputTransport(runtimeId: string, detail: {
  socketState: number | null;
  initialStateReceived: boolean;
  probeId?: string;
}): void {
  for (const trace of traces) {
    if (trace.state().runtimeId !== runtimeId) continue;
    trace.note({ event: 'transport_send', ...detail },
      detail.socketState !== 1 || !detail.initialStateReceived ? 'transport_unready' : undefined);
  }
}

export function noteTerminalInputReceipt(runtimeId: string, detail: {
  probeId: string;
  success: boolean;
  roundTripMs: number;
  daemonWriteMs: number;
}): void {
  for (const trace of traces) {
    if (trace.state().runtimeId !== runtimeId) continue;
    const reason = !detail.success ? 'pty_write_failed'
      : detail.roundTripMs >= 250 ? 'pty_round_trip_slow' : 'pty_receipt';
    trace.note({ event: 'pty_receipt', ...detail }, reason);
  }
}
