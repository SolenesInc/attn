import { useCallback, useEffect, useRef } from 'react';
import { useSessionStore, type Session } from '../store/sessions';
import type { BlockStateSnapshot, PlacementStateSnapshot } from '../components/GhosttyTerminal';
import type { SessionTerminalWorkspaceHandle } from '../components/SessionTerminalWorkspace';
import type { LeafDropSnapshot } from '../components/SessionTerminalWorkspace/leafDrag';
import { usePaneRuntimeEventRouter } from '../components/SessionTerminalWorkspace/paneRuntimeEventRouter';
import { useWorkspaceDebugHarness } from './useWorkspaceDebugHarness';
import {
  snapshotVisibleTerminalContent,
  type TerminalVisibleContentSnapshot,
} from '../utils/terminalVisibleContent';
import {
  emptyTerminalVisibleStyleSnapshot,
  type TerminalVisibleStyleSnapshot,
} from '../utils/terminalStyleSummary';

interface DesktopRuntimeController {
  eventRouter: ReturnType<typeof usePaneRuntimeEventRouter>;
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  setDesktopRef: (desktopId: string) => (ref: SessionTerminalWorkspaceHandle | null) => void;
  getDesktopLeafDropSnapshot: (desktopId: string | null | undefined) => LeafDropSnapshot | null;
  focusDesktopLeaf: (desktopId: string, leafId: string) => void;
  focusedLeafOf: (desktopId: string) => string | null;
  focusSessionPane: (sessionId: string, paneId: string, retries?: number) => void;
  typeInSessionPaneViaUI: (sessionId: string, paneId: string, text: string) => boolean;
  isSessionPaneInputFocused: (sessionId: string, paneId: string) => boolean;
  scrollSessionPaneToTop: (sessionId: string, paneId: string) => boolean;
  fitSessionActivePane: (sessionId: string) => void;
  getPaneText: (sessionId: string, paneId: string) => string;
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null;
  getPaneVisibleContent: (sessionId: string, paneId: string) => TerminalVisibleContentSnapshot;
  getPaneVisibleStyleSummary: (sessionId: string, paneId: string) => TerminalVisibleStyleSnapshot;
  getPaneBlockState: (sessionId: string, paneId: string) => BlockStateSnapshot | null;
  getPanePlacementState: (sessionId: string, paneId: string) => PlacementStateSnapshot | null;
  resetSessionPaneTerminal: (sessionId: string, paneId: string) => boolean;
  injectSessionPaneBytes: (sessionId: string, paneId: string, bytes: Uint8Array) => Promise<boolean>;
  injectSessionPaneBase64: (sessionId: string, paneId: string, payload: string) => Promise<boolean>;
  drainSessionPaneTerminal: (sessionId: string, paneId: string) => Promise<boolean>;
}

export function sessionPaneId(session: Session | undefined | null): string {
  return session?.desktop.agents.find((pane) => pane.sessionId === session.id)?.id ?? '';
}

export function useDesktopRuntimeController(
  sessions: Session[],
  activeSessionId: string | null
): DesktopRuntimeController {
  const desktopRefs = useRef<Map<string, SessionTerminalWorkspaceHandle>>(new Map());
  const focusRequest = useSessionStore(state => state.focusRequest);
  const focusedRequest = useRef<typeof focusRequest>(null);
  useEffect(() => {
    if (!focusRequest || focusedRequest.current === focusRequest) return;
    const desktopId = sessions.find(session => session.id === focusRequest.sessionId)?.desktopId;
    const desktop = desktopId ? desktopRefs.current.get(desktopId) : undefined;
    if (!desktop) return;
    focusedRequest.current = focusRequest;
    desktop.focusPane(focusRequest.paneId, 40);
  }, [focusRequest, sessions]);
  const eventRouter = usePaneRuntimeEventRouter();
  const getActivePaneIdForSession = sessionPaneId;

  useWorkspaceDebugHarness({
    sessions,
    activeSessionId,
    desktopRefs,
    getActivePaneIdForSession,
  });

  const desktopIdForSession = useCallback((sessionId: string) => {
    return sessions.find((entry) => entry.id === sessionId)?.desktopId || null;
  }, [sessions]);

  const setDesktopRef = useCallback(
    (desktopId: string) => (ref: SessionTerminalWorkspaceHandle | null) => {
      if (ref) {
        desktopRefs.current.set(desktopId, ref);
        return;
      }
      desktopRefs.current.delete(desktopId);
    },
    []
  );

  const getDesktopLeafDropSnapshot = useCallback((desktopId: string | null | undefined) => (
    desktopId ? desktopRefs.current.get(desktopId)?.getLeafDropSnapshot() ?? null : null
  ), []);

  const focusDesktopLeaf = useCallback((desktopId: string, leafId: string) => {
    desktopRefs.current.get(desktopId)?.focusLeaf(leafId);
  }, []);

  const focusedLeafOf = useCallback(
    (desktopId: string) => desktopRefs.current.get(desktopId)?.getActiveLeafId() || null,
    [],
  );

  const focusSessionPane = useCallback((sessionId: string, paneId: string, retries = 20) => {
    const desktopId = desktopIdForSession(sessionId);
    if (!desktopId) return;
    desktopRefs.current.get(desktopId)?.focusPane(paneId, retries);
  }, [desktopIdForSession]);

  const typeInSessionPaneViaUI = useCallback((sessionId: string, paneId: string, text: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.typePaneTextViaUI(paneId, text) || false : false;
  }, [desktopIdForSession]);

  const isSessionPaneInputFocused = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.isPaneInputFocused(paneId) || false : false;
  }, [desktopIdForSession]);

  const scrollSessionPaneToTop = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.scrollPaneToTop(paneId) || false : false;
  }, [desktopIdForSession]);

  const fitSessionActivePane = useCallback((sessionId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    if (!desktopId) return;
    desktopRefs.current.get(desktopId)?.fitActivePane();
  }, [desktopIdForSession]);

  const getPaneText = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.getPaneText(paneId) || '' : '';
  }, [desktopIdForSession]);

  const getPaneSize = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.getPaneSize(paneId) || null : null;
  }, [desktopIdForSession]);

  const getPaneVisibleContent = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return (desktopId ? desktopRefs.current.get(desktopId)?.getPaneVisibleContent(paneId) : null)
      || snapshotVisibleTerminalContent(null);
  }, [desktopIdForSession]);

  const getPaneVisibleStyleSummary = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return (desktopId ? desktopRefs.current.get(desktopId)?.getPaneVisibleStyleSummary(paneId) : null)
      || emptyTerminalVisibleStyleSnapshot();
  }, [desktopIdForSession]);

  const getPaneBlockState = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.getPaneBlockState(paneId) ?? null : null;
  }, [desktopIdForSession]);

  const getPanePlacementState = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.getPanePlacementState(paneId) ?? null : null;
  }, [desktopIdForSession]);

  const resetSessionPaneTerminal = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.resetPaneTerminal(paneId) || false : false;
  }, [desktopIdForSession]);

  const injectSessionPaneBytes = useCallback((sessionId: string, paneId: string, bytes: Uint8Array) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.injectPaneBytes(paneId, bytes) || Promise.resolve(false) : Promise.resolve(false);
  }, [desktopIdForSession]);

  const injectSessionPaneBase64 = useCallback((sessionId: string, paneId: string, payload: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.injectPaneBase64(paneId, payload) || Promise.resolve(false) : Promise.resolve(false);
  }, [desktopIdForSession]);

  const drainSessionPaneTerminal = useCallback((sessionId: string, paneId: string) => {
    const desktopId = desktopIdForSession(sessionId);
    return desktopId ? desktopRefs.current.get(desktopId)?.drainPaneTerminal(paneId) || Promise.resolve(false) : Promise.resolve(false);
  }, [desktopIdForSession]);

  return {
    eventRouter,
    getActivePaneIdForSession,
    setDesktopRef,
    getDesktopLeafDropSnapshot,
    focusDesktopLeaf,
    focusedLeafOf,
    focusSessionPane,
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    scrollSessionPaneToTop,
    fitSessionActivePane,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    getPaneVisibleStyleSummary,
    getPaneBlockState,
    getPanePlacementState,
    resetSessionPaneTerminal,
    injectSessionPaneBytes,
    injectSessionPaneBase64,
    drainSessionPaneTerminal,
  };
}
