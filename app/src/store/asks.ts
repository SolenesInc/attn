import { create } from 'zustand';
import type { Ask, AskAnswer } from '../types/generated';
import { isAsksEnabled } from '../utils/asks';

export interface AskDraft {
  answers: Record<string, AskAnswer>;
  note: string;
}

export const EMPTY_ASK_DRAFT: AskDraft = { answers: {}, note: '' };

interface AsksStore {
  asks: Ask[];
  drafts: Record<string, AskDraft>;
  selectedAskId: string | null;
  setAsks: (asks: Ask[]) => void;
  setDraft: (askId: string, draft: AskDraft) => void;
  selectAsk: (askId: string | null) => void;
}

export const useAsksStore = create<AsksStore>((set) => ({
  asks: [],
  drafts: {},
  selectedAskId: null,
  setAsks: (asks) => set((state) => {
    const open = new Set(asks.map((ask) => ask.id));
    const drafts = Object.fromEntries(Object.entries(state.drafts).filter(([id]) => open.has(id)));
    return { asks, drafts };
  }),
  setDraft: (askId, draft) => set((state) => ({ drafts: { ...state.drafts, [askId]: draft } })),
  selectAsk: (askId) => set({ selectedAskId: askId }),
}));

export function useAsksAvailable(settings: Record<string, string>): boolean {
  const open = useAsksStore((state) => state.asks.length > 0);
  return open || isAsksEnabled(settings);
}
