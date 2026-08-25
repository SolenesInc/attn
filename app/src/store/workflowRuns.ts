import { create } from 'zustand';
import { WorkflowRun } from '../types/generated';

interface WorkflowRunsStore {
  workflowRuns: Record<string, WorkflowRun>;

  upsertWorkflowRun: (run: WorkflowRun) => void;

  upsertWorkflowRuns: (runs: WorkflowRun[]) => void;

  removeWorkflowRun: (runId: string) => void;

  reset: () => void;
}

export const useWorkflowRunsStore = create<WorkflowRunsStore>((set) => ({
  workflowRuns: {},

  upsertWorkflowRun: (run) =>
    set((state) => {
      if (!run || !run.run_id) return state;
      return { workflowRuns: { ...state.workflowRuns, [run.run_id]: run } };
    }),

  upsertWorkflowRuns: (runs) =>
    set((state) => {
      if (!runs || runs.length === 0) return state;
      const next = { ...state.workflowRuns };
      for (const run of runs) {
        if (!run || !run.run_id) continue;
        next[run.run_id] = run;
      }
      return { workflowRuns: next };
    }),

  removeWorkflowRun: (runId) =>
    set((state) => {
      if (!runId || !(runId in state.workflowRuns)) return state;
      const next = { ...state.workflowRuns };
      delete next[runId];
      return { workflowRuns: next };
    }),

  reset: () => set({ workflowRuns: {} }),
}));

// created_at is an ISO-8601 string, so a lexicographic max is a correct
// chronological max.
export function selectLatestWorkflowRunForSession(
  runs: Record<string, WorkflowRun>,
  sessionId: string | null | undefined,
): WorkflowRun | null {
  if (!sessionId) return null;
  let latest: WorkflowRun | null = null;
  for (const run of Object.values(runs)) {
    if (!run || run.session_id !== sessionId) continue;
    if (!latest || run.created_at > latest.created_at) latest = run;
  }
  return latest;
}

// workflow_run_list omits agent_calls and a completed run gets no further
// workflow_run_updated, so it would stay call-less after a reload.
export function workflowRunIdNeedingHydration(
  panelOpen: boolean,
  run: WorkflowRun | null | undefined,
): string | null {
  if (!panelOpen || !run || !run.run_id) return null;
  if ((run.agent_calls?.length ?? 0) > 0) return null;
  return run.run_id;
}
