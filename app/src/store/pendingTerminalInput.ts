import { create } from 'zustand';

const retainedEvents = new WeakSet<Event>();
export const isRetainedTerminalEvent = (event: Event): boolean => retainedEvents.has(event);

interface PendingTerminalInput {
  pending: Map<string, Array<{ intentId: number; event: Event }>>;
  failure: { message: string } | null;
  append: (sessionId: string, intentId: number, event: Event) => void;
  discard: (intentId: number, reason?: string) => boolean;
  prune: (liveSessionIds: ReadonlySet<string>) => void;
  deliver: (sessionId: string, element: HTMLElement) => void;
}

export const usePendingTerminalInput = create<PendingTerminalInput>((set, get) => ({
  pending: new Map(),
  failure: null,
  append: (sessionId, intentId, event) => {
    const entry = get().pending.get(sessionId);
    if (entry) {
      entry.push({ intentId, event });
      return;
    }
    set((state) => ({ pending: new Map(state.pending).set(sessionId, [{ intentId, event }]) }));
  },
  discard: (intentId, reason = 'The selection failed') => {
    let discarded = false;
    set((state) => {
      const pending = new Map([...state.pending].flatMap(([key, entry]) => {
        const remaining = entry.filter((entry) => entry.intentId !== intentId);
        if (remaining.length !== entry.length) discarded = true;
        return remaining.length ? [[key, remaining] as const] : [];
      }));
      return { pending, failure: discarded ? { message: `Input typed while switching could not be delivered: ${reason}` } : state.failure };
    });
    return discarded;
  },
  prune: (liveSessionIds) => set((state) => {
    const pending = new Map([...state.pending].filter(([sessionId]) => liveSessionIds.has(sessionId)));
    if (pending.size === state.pending.size) return state;
    return { pending, failure: { message: 'Input typed while switching could not be delivered: Its terminal closed' } };
  }),
  deliver: (sessionId, element) => {
    const entry = get().pending.get(sessionId);
    if (!entry) return;
    for (const { event } of entry) element.dispatchEvent(event);
    set((state) => {
      const pending = new Map(state.pending);
      pending.delete(sessionId);
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

