import { useCallback } from 'react';
import { useSessionStore } from '../store/sessions';

export function useAgentNavigation() {
  const { selectAgent, selectAgentPane, cancelPendingSelection, navigateAgentHistory } =
    useSessionStore();
  const back = useCallback(
    (resumeCurrent = false) => Boolean(navigateAgentHistory('back', resumeCurrent)),
    [navigateAgentHistory],
  );
  const forward = useCallback(
    (resumeCurrent = false) => Boolean(navigateAgentHistory('forward', resumeCurrent)),
    [navigateAgentHistory],
  );
  return {
    selectAgent,
    selectAgentPane,
    cancelPendingSelection,
    back,
    forward,
  };
}
