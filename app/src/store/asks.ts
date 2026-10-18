import { useMemo } from 'react';
import { create } from 'zustand';
import type { Ask, AskAnswer } from '../types/generated';
import { askBelongsTo, askList, isAsksEnabled, type AskHolderSession, type AskListItem } from '../utils/asks';
import { useDaemonStore } from './daemonSessions';

export interface AskDraft {
  answers: Record<string, AskAnswer>;
  note: string;
}

export const EMPTY_ASK_DRAFT: AskDraft = { answers: {}, note: '' };

interface AsksStore {
  asks: Ask[];
  drafts: Record<string, AskDraft>;
  selectedAskId: string | null;
  focusRequest: number;
  setAsks: (asks: Ask[]) => void;
  setDraft: (askId: string, draft: AskDraft) => void;
  selectAsk: (askId: string | null) => void;
  showAsk: (askId: string | null) => void;
}

export const useAsksStore = create<AsksStore>((set) => ({
  asks: [],
  drafts: {},
  selectedAskId: null,
  focusRequest: 0,
  setAsks: (asks) => set((state) => {
    const open = new Set(asks.map((ask) => ask.id));
    const drafts = Object.fromEntries(Object.entries(state.drafts).filter(([id]) => open.has(id)));
    return { asks, drafts };
  }),
  setDraft: (askId, draft) => set((state) => ({ drafts: { ...state.drafts, [askId]: draft } })),
  selectAsk: (askId) => set({ selectedAskId: askId }),
  showAsk: (askId) => set((state) => ({ selectedAskId: askId, focusRequest: state.focusRequest + 1 })),
}));

export function useAsksAvailable(settings: Record<string, string>): boolean {
  const open = useAsksStore((state) => state.asks.length > 0);
  return open || isAsksEnabled(settings);
}

export function useAskList<S extends AskHolderSession & { label: string }>(sessions: readonly S[]): AskListItem<S>[] {
  const asks = useAsksStore((state) => state.asks);
  const seeds = useDaemonStore((state) => state.seeds);
  return useMemo(() => askList(asks, sessions, seeds), [asks, sessions, seeds]);
}

export function useAsksOf({ id, crewMember, chiefOfStaff, seedId }: AskHolderSession): Ask[] {
  const asks = useAsksStore((state) => state.asks);
  const seeds = useDaemonStore((state) => state.seeds);
  return useMemo(
    () => asks.filter((ask) => askBelongsTo(ask, { id, crewMember, chiefOfStaff, seedId }, seeds)),
    [asks, seeds, id, crewMember, chiefOfStaff, seedId],
  );
}
