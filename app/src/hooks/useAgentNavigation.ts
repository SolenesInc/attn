import { useCallback } from 'react';
import type { AgentHistoryDirection } from '../navigation/agentHistory';
import type { Session } from '../store/sessions';

export interface AgentNavigationController {
  selectAgent: (sessionId: string) => boolean;
  selectAgentPane: (sessionId: string, paneId: string) => boolean;
  back: (resumeCurrent?: boolean) => boolean;
  forward: (resumeCurrent?: boolean) => boolean;
}

export function useAgentNavigation(options: {
  sessions: Session[];
  setActiveSession: (sessionId: string | null) => void;
  navigateAgentHistory: (direction: AgentHistoryDirection, resumeCurrent?: boolean) => string | null;
  setActivePane: (sessionId: string, paneId: string) => void;
  focusSessionPane: (sessionId: string, paneId: string, retries?: number) => void;
  revealSessionView: () => void;
  requestTerminalFocus: () => void;
}): AgentNavigationController {
  const focusAgentPane = useCallback((sessionId: string, paneId: string, activateSession: boolean) => {
    options.setActivePane(sessionId, paneId);
    if (activateSession) {
      options.setActiveSession(sessionId);
    }
    options.revealSessionView();
    options.requestTerminalFocus();
    options.focusSessionPane(sessionId, paneId, 40);
  }, [options.focusSessionPane, options.revealSessionView, options.requestTerminalFocus, options.setActivePane, options.setActiveSession]);

  const selectAgent = useCallback((sessionId: string) => {
    const session = options.sessions.find((entry) => entry.id === sessionId);
    const pane = session?.workspace.agents.find((entry) => entry.sessionId === sessionId);
    if (!pane) {
      return false;
    }

    focusAgentPane(sessionId, pane.id, true);
    return true;
  }, [focusAgentPane, options.sessions]);

  const selectAgentPane = useCallback((sessionId: string, paneId: string) => {
    const session = options.sessions.find((entry) => entry.id === sessionId);
    const pane = session?.workspace.agents.find((entry) => entry.id === paneId && entry.sessionId === sessionId);
    if (!pane) {
      return false;
    }

    focusAgentPane(sessionId, pane.id, true);
    return true;
  }, [focusAgentPane, options.sessions]);

  const move = useCallback((direction: AgentHistoryDirection, resumeCurrent = false) => {
    const targetSessionId = options.navigateAgentHistory(direction, resumeCurrent);
    if (!targetSessionId) {
      return false;
    }
    const session = options.sessions.find((entry) => entry.id === targetSessionId);
    const pane = session?.workspace.agents.find((entry) => entry.sessionId === targetSessionId);
    if (!pane) {
      return false;
    }

    focusAgentPane(targetSessionId, pane.id, false);
    return true;
  }, [focusAgentPane, options.navigateAgentHistory, options.sessions]);

  const back = useCallback((resumeCurrent = false) => move('back', resumeCurrent), [move]);
  const forward = useCallback((resumeCurrent = false) => move('forward', resumeCurrent), [move]);

  return { selectAgent, selectAgentPane, back, forward };
}
