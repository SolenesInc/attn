import { create } from 'zustand';

export type PendingInputTarget =
  | { kind: 'agent'; id: string }
  | { kind: 'terminal'; id: string; desktopId: string };

const retainedEvents = new WeakSet<Event>();
export const isRetainedTerminalEvent = (event: Event): boolean => retainedEvents.has(event);
const targetKey = (target: PendingInputTarget) => `${target.kind}:${target.id}`;

interface PendingTerminalInput {
  pending: Map<string, { target: PendingInputTarget; events: Array<{ intentId: number; event: Event }> }>;
  failure: { message: string } | null;
  append: (target: PendingInputTarget, intentId: number, event: Event) => void;
  discard: (intentId: number, reason?: string) => boolean;
  deliver: (target: PendingInputTarget, element: HTMLElement) => void;
}

export const usePendingTerminalInput = create<PendingTerminalInput>((set, get) => ({
  pending: new Map(),
  failure: null,
  append: (target, intentId, event) => {
    const key = targetKey(target);
    const entry = get().pending.get(key);
    if (entry) {
      entry.events.push({ intentId, event });
      return;
    }
    set((state) => ({ pending: new Map(state.pending).set(key, { target, events: [{ intentId, event }] }) }));
  },
  discard: (intentId, reason = 'The selection failed') => {
    let discarded = false;
    set((state) => {
      const pending = new Map([...state.pending].flatMap(([key, entry]) => {
        const remaining = entry.events.filter((entry) => entry.intentId !== intentId);
        if (remaining.length !== entry.events.length) discarded = true;
        return remaining.length ? [[key, { ...entry, events: remaining }] as const] : [];
      }));
      return { pending, failure: discarded ? { message: `Input typed while switching could not be delivered: ${reason}` } : state.failure };
    });
    return discarded;
  },
  deliver: (target, element) => {
    const key = targetKey(target);
    const entry = get().pending.get(key);
    if (!entry) return;
    for (const { event } of entry.events) element.dispatchEvent(event);
    set((state) => {
      const pending = new Map(state.pending);
      pending.delete(key);
      return { pending };
    });
  },
}));

export function retainTerminalEvent(event: Event): Event {
  if (event instanceof KeyboardEvent) {
    const copy = new KeyboardEvent(event.type, event);
    Object.defineProperty(copy, 'getModifierState', { value: event.getModifierState.bind(event) });
    retainedEvents.add(copy);
    return copy;
  }
  if (event instanceof ClipboardEvent) {
    const clipboardData = new DataTransfer();
    clipboardData.setData('text/plain', event.clipboardData?.getData('text/plain') ?? '');
    for (const file of Array.from(event.clipboardData?.files ?? [])) clipboardData.items.add(file);
    const copy = new ClipboardEvent(event.type, { bubbles: true, cancelable: true, clipboardData });
    retainedEvents.add(copy);
    return copy;
  }
  const copy = new CompositionEvent(event.type, { bubbles: true, cancelable: true });
  Object.defineProperty(copy, 'data', { value: (event as CompositionEvent).data });
  retainedEvents.add(copy);
  return copy;
}

