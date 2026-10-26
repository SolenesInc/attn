import { create } from 'zustand';

export interface TerminalRefusal {
  flash: number;
  flashing: boolean;
  keysNotSent: number;
}

interface TerminalRefusals {
  byTerminal: ReadonlyMap<string, TerminalRefusal>;
  refused: (terminalId: string, keysNotSent: number) => void;
  sent: (terminalId: string) => void;
  flashEnded: (terminalId: string) => void;
}

const settled = (refusal: TerminalRefusal) => !refusal.flashing && refusal.keysNotSent === 0;

function withRefusal(byTerminal: ReadonlyMap<string, TerminalRefusal>, terminalId: string, refusal: TerminalRefusal) {
  const next = new Map(byTerminal);
  if (settled(refusal)) next.delete(terminalId);
  else next.set(terminalId, refusal);
  return next;
}

const untypedSources = new Set(['agent', 'automation', 'attach_replay', 'pointer', 'response']);
export const isTypedInput = (source?: string) => !untypedSources.has(source?.trim() ?? '');

export const useTerminalRefusals = create<TerminalRefusals>((set, get) => ({
  byTerminal: new Map(),
  refused: (terminalId, keysNotSent) => set((state) => {
    const refusal = state.byTerminal.get(terminalId);
    return {
      byTerminal: withRefusal(state.byTerminal, terminalId, {
        flash: (refusal?.flash ?? 0) + 1,
        flashing: true,
        keysNotSent: (refusal?.keysNotSent ?? 0) + keysNotSent,
      }),
    };
  }),
  sent: (terminalId) => {
    const refusal = get().byTerminal.get(terminalId);
    if (!refusal?.keysNotSent) return;
    set((state) => ({ byTerminal: withRefusal(state.byTerminal, terminalId, { ...refusal, keysNotSent: 0 }) }));
  },
  flashEnded: (terminalId) => {
    const refusal = get().byTerminal.get(terminalId);
    if (!refusal?.flashing) return;
    set((state) => ({ byTerminal: withRefusal(state.byTerminal, terminalId, { ...refusal, flashing: false }) }));
  },
}));
