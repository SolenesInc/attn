import { useCallback } from 'react';
import { useSessionStore } from '../store/sessions';

export function useAgentNavigation() {
  const { selectAgent, selectLeaf, cancelPendingSelection, navigateAgentHistory } =
    useSessionStore();
  const back = useCallback(
    (resumeCurrent = false) => navigateAgentHistory('back', resumeCurrent),
    [navigateAgentHistory],
  );
  const forward = useCallback(
    (resumeCurrent = false) => navigateAgentHistory('forward', resumeCurrent),
    [navigateAgentHistory],
  );
  return {
    selectAgent,
    selectLeaf,
    cancelPendingSelection,
    back,
    forward,
  };
}
