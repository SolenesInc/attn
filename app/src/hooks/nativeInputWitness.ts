import { isDomInstance } from '../utils/realm';
export type InputExpectation =
  | { gesture: 'pointer' | 'move'; selector: string | null }
  | { gesture: 'keys'; strokes: number };

const COMPLETING_EVENT: Record<InputExpectation['gesture'], 'mouseup' | 'mousemove' | 'keyup'> = {
  pointer: 'mouseup',
  move: 'mousemove',
  keys: 'keyup',
};

interface DeliveredEvent {
  type: string;
  target: string;
  key?: string;
  clientX?: number;
  clientY?: number;
  matches?: boolean;
}

export interface InputReceipt {
  gesture: InputExpectation['gesture'];
  events: DeliveredEvent[];
  elapsedMs: number;
  clientX?: number;
  clientY?: number;
  target?: string;
  matches?: boolean;
}

const DELIVERY_LIMIT_MS = 5_000;
const MODIFIER_KEYS = new Set(['Shift', 'Control', 'Alt', 'AltGraph', 'Meta', 'CapsLock', 'Fn', 'Hyper', 'Super', 'OS']);

interface InputWitness {
  expectation: InputExpectation;
  armedAt: number;
  events: DeliveredEvent[];
  resolve: ((receipt: InputReceipt) => void) | null;
  reject: ((error: Error) => void) | null;
  stop: () => void;
}

let witness: InputWitness | null = null;

function describeTarget(target: EventTarget | null): string {
  return isDomInstance(target, 'Element') ? `${target.tagName.toLowerCase()}.${Array.from(target.classList).join('.')}` : 'none';
}

function expectedEvents({ expectation }: InputWitness): number {
  return expectation.gesture === 'keys' ? expectation.strokes : 1;
}

function describeExpectation(expectation: InputExpectation): string {
  if (expectation.gesture !== 'keys') {
    return `a trusted ${COMPLETING_EVENT[expectation.gesture]}${expectation.selector ? ` (aimed at ${expectation.selector})` : ''}`;
  }
  return `${expectation.strokes} trusted non-modifier keyup${expectation.strokes === 1 ? '' : 's'}`;
}

function delivered(armed: InputWitness): boolean {
  return armed.events.length >= expectedEvents(armed);
}

function receiptOf(armed: InputWitness): InputReceipt {
  const elapsedMs = Math.round(performance.now() - armed.armedAt);
  if (armed.expectation.gesture === 'keys') return { gesture: 'keys', events: armed.events, elapsedMs };
  const [release] = armed.events;
  return {
    gesture: armed.expectation.gesture, events: armed.events, elapsedMs,
    clientX: release.clientX, clientY: release.clientY, target: release.target, matches: release.matches,
  };
}

function observedEvent(expectation: InputExpectation, event: Event): DeliveredEvent | null {
  if (!event.isTrusted) return null;
  const target = describeTarget(event.target);
  if (isDomInstance(event, 'KeyboardEvent')) {
    return MODIFIER_KEYS.has(event.key) ? null : { type: event.type, key: event.key, target };
  }
  if (!isDomInstance(event, 'MouseEvent') || expectation.gesture === 'keys') return null;
  const element = isDomInstance(event.target, 'Element') ? event.target : null;
  return {
    type: event.type, target, clientX: event.clientX, clientY: event.clientY,
    ...(expectation.selector ? { matches: Boolean(element?.closest(expectation.selector)) } : {}),
  };
}

export function parseInputExpectation(payload: Record<string, unknown>): InputExpectation {
  if (payload.gesture === 'pointer' || payload.gesture === 'move') {
    return { gesture: payload.gesture, selector: typeof payload.selector === 'string' && payload.selector ? payload.selector : null };
  }
  if (payload.gesture === 'keys' && Number.isInteger(payload.strokes) && (payload.strokes as number) >= 0) {
    return { gesture: 'keys', strokes: payload.strokes as number };
  }
  throw new Error(`arm_native_input_witness needs { gesture: 'pointer' | 'move', selector? } or { gesture: 'keys', strokes }, got ${JSON.stringify(payload)}`);
}

export function disarmNativeInputWitness(): void {
  if (!witness) return;
  witness.stop();
  witness.reject?.(new Error(`Native input witness for ${describeExpectation(witness.expectation)} was cancelled`));
  witness = null;
}

export function armNativeInputWitness(expectation: InputExpectation): void {
  if (expectation.gesture !== 'keys' && expectation.selector && !document.querySelector(expectation.selector)) {
    throw new Error(`Native pointer target not found: ${expectation.selector}`);
  }
  disarmNativeInputWitness();
  const type = COMPLETING_EVENT[expectation.gesture];
  const observe = (event: Event) => {
    const seen = observedEvent(expectation, event);
    if (!seen) return;
    armed.events.push(seen);
    if (!delivered(armed)) return;
    armed.stop();
    armed.resolve?.(receiptOf(armed));
    armed.resolve = null;
    armed.reject = null;
  };
  const armed: InputWitness = {
    expectation, armedAt: performance.now(), events: [], resolve: null, reject: null,
    stop: () => window.removeEventListener(type, observe, true),
  };
  witness = armed;
  window.addEventListener(type, observe, true);
}

export function waitForNativeInputWitness(): Promise<InputReceipt> {
  const armed = witness;
  if (!armed) return Promise.reject(new Error('Native input witness is not armed'));
  if (delivered(armed)) {
    armed.stop();
    return Promise.resolve(receiptOf(armed));
  }
  if (armed.resolve) return Promise.reject(new Error(`Native input witness already has a reader: ${describeExpectation(armed.expectation)}`));
  return new Promise((resolve, reject) => {
    const limit = window.setTimeout(() => {
      armed.stop();
      armed.resolve = null;
      armed.reject = null;
      reject(new Error(
        `Native input was not delivered: this webview saw ${armed.events.length} of ${describeExpectation(armed.expectation)} `
        + `within the ${DELIVERY_LIMIT_MS}ms delivery limit. Seen: ${JSON.stringify(armed.events)}`,
      ));
    }, DELIVERY_LIMIT_MS);
    armed.resolve = receipt => { window.clearTimeout(limit); resolve(receipt); };
    armed.reject = error => { window.clearTimeout(limit); reject(error); };
  });
}
