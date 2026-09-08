const TRACE_CAPACITY = 512;
const appStartedAt = Date.now();
let nextTraceSequence = 0;
let nextEventSequence = 0;

const traceIds = new WeakMap<Event, string>();

export type SupportInputTraceStage = 'document' | 'terminal' | 'transport';

export interface FrontendSupportInputTrace {
  sequence: number;
  traceId: string;
  atUnixMs: number;
  monotonicMs: number;
  stage: SupportInputTraceStage;
  event: string;
  runtimeId?: string;
  sessionId?: string;
  paneId?: string;
  target?: string;
  trusted?: boolean;
  outcome?: string;
  keyClass?: string;
  repeat?: boolean;
  modifiers?: number;
  socketState?: number | null;
  initialStateReceived?: boolean;
  probeId?: string;
}

export interface FrontendInputTraceSnapshot {
  capacity: number;
  total: number;
  capturedAtUnixMs: number;
  capturedAtMonotonicMs: number;
  events: FrontendSupportInputTrace[];
}

const events = Array.from<FrontendSupportInputTrace | undefined>({ length: TRACE_CAPACITY });

function append(event: Omit<FrontendSupportInputTrace, 'sequence' | 'atUnixMs' | 'monotonicMs'>): void {
  const sequence = ++nextEventSequence;
  events[(sequence - 1) % TRACE_CAPACITY] = {
    ...event,
    sequence,
    atUnixMs: Date.now(),
    monotonicMs: performance.now(),
  };
}

function newTraceId(): string {
  nextTraceSequence += 1;
  return `${appStartedAt.toString(36)}-${nextTraceSequence.toString(36)}`;
}

export function inputTraceId(event: Event): string {
  const existing = traceIds.get(event);
  if (existing) return existing;
  const traceId = newTraceId();
  traceIds.set(event, traceId);
  return traceId;
}

export function observeDocumentInput(event: KeyboardEvent | ClipboardEvent | CompositionEvent, target: string): string {
  const traceId = inputTraceId(event);
  append({
    traceId,
    stage: 'document',
    event: event.type,
    target,
    trusted: event.isTrusted,
    ...(event instanceof KeyboardEvent ? {
      repeat: event.repeat,
      modifiers: (event.shiftKey ? 1 : 0)
        | (event.ctrlKey ? 2 : 0)
        | (event.altKey ? 4 : 0)
        | (event.metaKey ? 8 : 0),
    } : {}),
  });
  return traceId;
}

export function recordTerminalInputTrace(
  traceId: string | undefined,
  detail: Omit<FrontendSupportInputTrace, 'sequence' | 'traceId' | 'atUnixMs' | 'monotonicMs' | 'stage'>,
): void {
  if (!traceId) return;
  append({ traceId, stage: 'terminal', ...detail });
}

export function recordTransportInputTrace(
  traceId: string | undefined,
  detail: Omit<FrontendSupportInputTrace, 'sequence' | 'traceId' | 'atUnixMs' | 'monotonicMs' | 'stage'>,
): void {
  if (!traceId) return;
  append({ traceId, stage: 'transport', ...detail });
}

export function snapshotFrontendInputTrace(): FrontendInputTraceSnapshot {
  const total = nextEventSequence;
  const retained = events
    .filter((event): event is FrontendSupportInputTrace => event !== undefined && event.sequence <= total)
    .sort((left, right) => left.sequence - right.sequence)
    .map((event) => ({ ...event }));
  return {
    capacity: TRACE_CAPACITY,
    total,
    capturedAtUnixMs: Date.now(),
    capturedAtMonotonicMs: performance.now(),
    events: retained,
  };
}

export function resetFrontendInputTraceForTests(): void {
  events.fill(undefined);
  nextTraceSequence = 0;
  nextEventSequence = 0;
}
