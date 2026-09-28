import { useEffect, useMemo } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import {
  selectLatestWorkflowRunForSession,
  useWorkflowRunsStore,
  workflowRunIdNeedingHydration,
} from '../store/workflowRuns';

interface Options {
  contextSessionId: string | null;
  workflowRunPanelOpen: boolean;
}
export function useWorkflowPanel({ contextSessionId, workflowRunPanelOpen }: Options) {
  const { listWorkflowRuns, getWorkflowRun } = useDaemonApi();
  const workflowRunsMap = useWorkflowRunsStore((s) => s.workflowRuns);
  const activeWorkflowRun = useMemo(
    () => selectLatestWorkflowRunForSession(workflowRunsMap, contextSessionId),
    [workflowRunsMap, contextSessionId],
  );
  useEffect(() => {
    if (!contextSessionId) {
      return;
    }
    listWorkflowRuns(contextSessionId).catch((error) => {
      console.error('[App] Failed to list workflow runs:', error);
    });
  }, [contextSessionId, listWorkflowRuns]);

  // listWorkflowRuns omits agent_calls and a completed run sees no further broadcasts, so without this fetch it stays call-less forever after a reload.
  const workflowRunIdToHydrate = workflowRunIdNeedingHydration(
    workflowRunPanelOpen,
    activeWorkflowRun,
  );
  useEffect(() => {
    if (!workflowRunIdToHydrate) {
      return;
    }
    getWorkflowRun(workflowRunIdToHydrate).catch((error) => {
      console.error('[App] Failed to hydrate workflow run:', error);
    });
  }, [workflowRunIdToHydrate, getWorkflowRun]);

  return { activeWorkflowRun };
}
