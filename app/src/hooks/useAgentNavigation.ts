import { useCallback } from 'react';
import { useSessionStore } from '../store/sessions';

export function useAgentNavigation() {
  const { selectAgent, selectLeaf, cancelPendingSelection, navigateLeafHistory } =
    useSessionStore();
  const back = useCallback(
    (resumeCurrent = false) => navigateLeafHistory('back', resumeCurrent),
    [navigateLeafHistory],
  );
  const forward = useCallback(
    (resumeCurrent = false) => navigateLeafHistory('forward', resumeCurrent),
    [navigateLeafHistory],
  );
  return {
    selectAgent,
    selectLeaf,
    cancelPendingSelection,
    back,
    forward,
  };
}
