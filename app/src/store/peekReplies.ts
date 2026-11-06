import { create } from 'zustand';
import { useToastStore } from './toasts';

export type PeekReply =
  | { status: 'draft'; text: string }
  | { status: 'sending'; text: string }
  | { status: 'not_sent'; text: string; reason: string | null }
  | { status: 'typed'; text: string; reason: string };

interface PeekReplies {
  byTerminal: ReadonlyMap<string, PeekReply>;
  put: (terminalId: string, reply: PeekReply) => void;
  forget: (terminalId: string) => void;
}

export const usePeekReplies = create<PeekReplies>((set) => ({
  byTerminal: new Map(),
  put: (terminalId, reply) => set((state) => ({ byTerminal: new Map(state.byTerminal).set(terminalId, reply) })),
  forget: (terminalId) => set((state) => {
    if (!state.byTerminal.has(terminalId)) return state;
    const byTerminal = new Map(state.byTerminal);
    byTerminal.delete(terminalId);
    return { byTerminal };
  }),
}));

export function retainTypedReply(terminalId: string) {
  const { byTerminal, put } = usePeekReplies.getState();
  const reply = byTerminal.get(terminalId);
  if (reply?.status === 'typed') put(terminalId, { status: 'not_sent', text: reply.text, reason: reply.reason });
}

const reasonOf = (error: unknown) => (error instanceof Error ? error.message : String(error));

export async function deliverPeekReply(
  terminalId: string,
  terminalTitle: string,
  reply: PeekReply,
  write: (terminalId: string, data: string) => Promise<void>,
) {
  const { put, forget } = usePeekReplies.getState();
  const { text } = reply;
  put(terminalId, { status: 'sending', text });
  if (reply.status !== 'typed') {
    try {
      await write(terminalId, text);
    } catch (error) {
      put(terminalId, { status: 'not_sent', text, reason: reasonOf(error) });
      useToastStore.getState().append({ message: `Your reply to ${terminalTitle} may not have arrived`, source: `${reasonOf(error)}. Its tile keeps the text.` });
      return;
    }
  }
  try {
    await write(terminalId, '\r');
    forget(terminalId);
  } catch (error) {
    put(terminalId, { status: 'typed', text, reason: reasonOf(error) });
    useToastStore.getState().append({ message: `Your reply to ${terminalTitle} was typed but not submitted`, source: `${reasonOf(error)}. Hover its chip to finish it.` });
  }
}
