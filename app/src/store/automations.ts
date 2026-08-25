import { create } from 'zustand';
import { AutomationDefinitionSummary, AutomationRunSummary } from '../types/generated';

interface AutomationsStore {
  definitions: AutomationDefinitionSummary[];

  // Runs keyed by definition_id. A definition with no entry has not been fetched yet, which is
  // distinct from an entry of []: no runs exist.
  runsByDefinition: Record<string, AutomationRunSummary[]>;

  changedTick: number;

  // The daemon persists this as ClaimManualAutomationRun's dedup key, so a retry of the
  // same click must reuse it or a timed-out-but-delivered run re-triggers as a duplicate.
  pendingRunRequests: Record<string, string>;

  setDefinitions: (definitions: AutomationDefinitionSummary[]) => void;
  setRuns: (definitionId: string, runs: AutomationRunSummary[]) => void;
  bumpChanged: () => void;
  ensureRunRequest: (definitionId: string) => string;
  clearRunRequest: (definitionId: string) => void;

  // Never overwrites a stored key: one already in flight is more current than a later fetch.
  adoptRunRequest: (definitionId: string, requestId: string) => void;

  reset: () => void;
}

export const useAutomationsStore = create<AutomationsStore>((set, get) => ({
  definitions: [],
  runsByDefinition: {},
  changedTick: 0,
  pendingRunRequests: {},

  setDefinitions: (definitions) => set({ definitions: definitions ?? [] }),

  setRuns: (definitionId, runs) =>
    set((state) => {
      if (!definitionId) return state;
      return { runsByDefinition: { ...state.runsByDefinition, [definitionId]: runs ?? [] } };
    }),

  bumpChanged: () => set((state) => ({ changedTick: state.changedTick + 1 })),

  ensureRunRequest: (definitionId) => {
    const existing = get().pendingRunRequests[definitionId];
    if (existing) return existing;
    const requestId = crypto.randomUUID();
    set((state) => ({ pendingRunRequests: { ...state.pendingRunRequests, [definitionId]: requestId } }));
    return requestId;
  },

  clearRunRequest: (definitionId) =>
    set((state) => {
      if (!(definitionId in state.pendingRunRequests)) return state;
      const next = { ...state.pendingRunRequests };
      delete next[definitionId];
      return { pendingRunRequests: next };
    }),

  adoptRunRequest: (definitionId, requestId) =>
    set((state) => {
      if (state.pendingRunRequests[definitionId]) return state;
      return { pendingRunRequests: { ...state.pendingRunRequests, [definitionId]: requestId } };
    }),

  reset: () => set({ definitions: [], runsByDefinition: {}, changedTick: 0, pendingRunRequests: {} }),
}));

export function selectDefinitionById(
  definitions: AutomationDefinitionSummary[],
  definitionId: string | null | undefined,
): AutomationDefinitionSummary | null {
  if (!definitionId) return null;
  return definitions.find((definition) => definition.id === definitionId) ?? null;
}
