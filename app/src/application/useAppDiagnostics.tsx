import { useCallback, useRef, useState } from 'react';
import type { useDaemonApi } from '../contexts/DaemonApiContext';
import type { AppView } from '../navigation/sessionNavigation';
import { useProfilesStore } from '../store/profiles';
import type { Session } from '../store/sessions';
import {
  type DiagnosticCaptureContext,
  type DiagnosticPaneDescriptor,
  type PendingDiagnosticCapture,
} from '../utils/diagnosticReport';
import { desktopLabel, desktopTerminalState } from '../utils/desktops';
import { collectDesktopLayoutDiagnostics } from '../utils/desktopDiagnostics';
import { diagnosticFocusKind, shortenDiagnosticPath } from './appSupport';
interface Options {
  sessions: Session[];
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null;
  contextSessionId: string | null;
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  view: AppView;
  settings: Record<string, string>;
  sendSupportSnapshot: ReturnType<typeof useDaemonApi>['sendSupportSnapshot'];
  getPaneText: (sessionId: string, paneId: string) => string;
}
export function useAppDiagnostics({
  sessions,
  getPaneSize,
  contextSessionId,
  getActivePaneIdForSession,
  view,
  settings,
  sendSupportSnapshot,
  getPaneText,
}: Options) {
  const [diagnosticCapture, setDiagnosticCapture] = useState<{
    capture: PendingDiagnosticCapture;
    affectedPaneId: string | null;
  } | null>(null);
  const paletteOriginRef = useRef<DiagnosticCaptureContext | null>(null);

  const diagnosticPanes = useCallback((): DiagnosticPaneDescriptor[] => {
    const sessionById = new Map(sessions.map((session) => [session.id, session]));
    const { desktops } = useProfilesStore.getState();
    return desktops.flatMap((desktop) =>
      desktopTerminalState(desktop).agents.map((pane) => ({
        paneId: pane.id,
        runtimeId: pane.runtimeId,
        sessionId: pane.sessionId,
        title: pane.title,
        sessionLabel: sessionById.get(pane.sessionId)?.label || pane.title,
        desktopId: desktop.id,
        desktopLabel: desktopLabel(desktop, desktops),
        available: getPaneSize(pane.sessionId, pane.id) !== null,
      })),
    );
  }, [getPaneSize, sessions]);

  const handleCreateDiagnosticReport = useCallback(async () => {
    const fallbackSession = contextSessionId
      ? sessions.find((session) => session.id === contextSessionId)
      : null;
    const fallbackPaneId = fallbackSession
      ? getActivePaneIdForSession(fallbackSession) || null
      : null;
    const context = paletteOriginRef.current ?? {
      capturedAtUnixMs: Date.now(),
      view,
      activeSessionId: contextSessionId,
      activePaneId: fallbackPaneId,
      activeElement: diagnosticFocusKind(document.activeElement),
      documentFocused: document.hasFocus(),
      visibility: document.visibilityState,
      window: {
        width: window.innerWidth,
        height: window.innerHeight,
        devicePixelRatio: window.devicePixelRatio,
      },
    };
    const { desktops } = useProfilesStore.getState();
    const sessionById = new Map(sessions.map((session) => [session.id, session]));
    const desktopSnapshots = desktops.map((desktop) => {
      const state = desktopTerminalState(desktop);
      const activeSessionOnDesktop = state.agents.find((pane) => pane.id === desktop.active_pane_id)?.sessionId;
      return {
        id: desktop.id,
        label: desktopLabel(desktop, desktops),
        directory: shortenDiagnosticPath(sessionById.get(activeSessionOnDesktop ?? '')?.cwd ?? ''),
        layout: collectDesktopLayoutDiagnostics(state.layoutTree),
      };
    });
    const { beginDiagnosticCapture } = await import('../utils/diagnosticReport');
    const capture = beginDiagnosticCapture({
      context,
      panes: diagnosticPanes(),
      sessions: sessions.map((session) => ({
        id: session.id,
        label: session.label,
        state: session.state,
        agent: session.agent,
        cwd: shortenDiagnosticPath(session.cwd),
        desktopId: session.desktopId,
        endpoint: session.endpointId ? 'remote' : 'local',
        ...(session.endpointId ? { endpointId: session.endpointId } : {}),
        active: session.id === context.activeSessionId,
      })),
      desktops: desktopSnapshots,
      settings,
      sendSupportSnapshot,
    });
    setDiagnosticCapture({ capture, affectedPaneId: context.activePaneId });
  }, [
    contextSessionId,
    diagnosticPanes,
    getActivePaneIdForSession,
    sendSupportSnapshot,
    sessions,
    settings,
    view,
  ]);

  const handleSaveDiagnosticReport = useCallback(
    async (selectedPaneIds: string[]) => {
      if (!diagnosticCapture) return;
      const { createDiagnosticReport, saveDiagnosticReport } = await import(
        '../utils/diagnosticReport'
      );
      const report = await createDiagnosticReport(
        diagnosticCapture.capture,
        selectedPaneIds,
        (paneId) => {
          const pane = diagnosticCapture.capture.panes.find((entry) => entry.paneId === paneId);
          if (!pane) return { text: '', available: false };
          return {
            text: getPaneText(pane.sessionId, paneId),
            available: getPaneSize(pane.sessionId, paneId) !== null,
          };
        },
      );
      await saveDiagnosticReport(report);
    },
    [diagnosticCapture, getPaneSize, getPaneText],
  );

  return {
    diagnosticCapture,
    handleCreateDiagnosticReport,
    paletteOriginRef,
    handleSaveDiagnosticReport,
    setDiagnosticCapture,
  };
}
