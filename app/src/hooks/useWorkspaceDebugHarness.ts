import { useEffect } from 'react';
import type { RefObject } from 'react';
import type { Session } from '../store/sessions';
import type { SessionTerminalWorkspaceHandle } from '../components/SessionTerminalWorkspace';

declare global {
  interface Window {
    __TEST_GET_SESSION_PANE_TEXT?: (sessionId: string) => string;
    __TEST_GET_SESSION_PANE_VISIBLE_TEXT?: (sessionId: string) => string;
    __TEST_GET_SESSION_PANE_SIZE?: (sessionId: string) => { cols: number; rows: number } | null;
    __TEST_GET_ACTIVE_SESSION_PANE_TEXT?: () => string;
    __TEST_GET_ACTIVE_SESSION_PANE_RUNTIME?: (sessionId: string) => string | null;
  }
}

interface UseWorkspaceDebugHarnessArgs {
  sessions: Session[];
  activeSessionId: string | null;
  desktopRefs: RefObject<Map<string, SessionTerminalWorkspaceHandle>>;
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
}

export function useWorkspaceDebugHarness({
  sessions,
  activeSessionId,
  desktopRefs,
  getActivePaneIdForSession,
}: UseWorkspaceDebugHarnessArgs) {
  useEffect(() => {
    if (!import.meta.env.DEV) {
      return;
    }

    window.__TEST_GET_SESSION_PANE_TEXT = (sessionId: string) => {
      const session = sessions.find((entry) => entry.id === sessionId);
      if (!session) {
        return '';
      }
      const paneId = session?.desktop.agents.find((entry) => entry.sessionId === sessionId)?.id || '';
      return desktopRefs.current.get(session.desktopId)?.getPaneText(paneId) || '';
    };

    window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT = (sessionId: string) => {
      const session = sessions.find((entry) => entry.id === sessionId);
      if (!session) {
        return '';
      }
      const paneId = session?.desktop.agents.find((entry) => entry.sessionId === sessionId)?.id || '';
      const visible = desktopRefs.current.get(session.desktopId)?.getPaneVisibleContent(paneId);
      return visible ? visible.lines.join('\n') : '';
    };

    window.__TEST_GET_SESSION_PANE_SIZE = (sessionId: string) => {
      const session = sessions.find((entry) => entry.id === sessionId);
      if (!session) {
        return null;
      }
      const paneId = session?.desktop.agents.find((entry) => entry.sessionId === sessionId)?.id || '';
      return desktopRefs.current.get(session.desktopId)?.getPaneSize(paneId) || null;
    };

    window.__TEST_GET_ACTIVE_SESSION_PANE_TEXT = () => {
      if (!activeSessionId) {
        return '';
      }
      const session = sessions.find((entry) => entry.id === activeSessionId);
      const activePaneId = getActivePaneIdForSession(session);
      if (!session || !activePaneId) {
        return '';
      }
      return desktopRefs.current.get(session.desktopId)?.getPaneText(activePaneId) || '';
    };

    window.__TEST_GET_ACTIVE_SESSION_PANE_RUNTIME = (sessionId: string) => {
      const session = sessions.find((entry) => entry.id === sessionId);
      const activePaneId = getActivePaneIdForSession(session);
      if (!session || !activePaneId) {
        return null;
      }
      return session.desktop.agents.find((entry) => entry.id === activePaneId)?.runtimeId ?? null;
    };

    return () => {
      delete window.__TEST_GET_SESSION_PANE_TEXT;
      delete window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT;
      delete window.__TEST_GET_SESSION_PANE_SIZE;
      delete window.__TEST_GET_ACTIVE_SESSION_PANE_TEXT;
      delete window.__TEST_GET_ACTIVE_SESSION_PANE_RUNTIME;
    };
  }, [activeSessionId, getActivePaneIdForSession, sessions, desktopRefs]);
}
