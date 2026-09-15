import { useCallback, useRef, useState } from 'react';
import { useSavedFlash } from '../components/useSavedFlash';
import type { useDaemonApi } from '../contexts/DaemonApiContext';
import type { useAppView } from '../hooks/useAppView';
import type { DaemonWorkspace } from '../hooks/useDaemonSocket';
import type { Session } from '../store/sessions';
import {
  type DiagnosticCaptureContext,
  type DiagnosticPaneDescriptor,
  type PendingDiagnosticCapture,
} from '../utils/diagnosticReport';
import { collectWorkspaceLayoutDiagnostics } from '../utils/workspaceDiagnostics';
import { diagnosticFocusKind, shortenDiagnosticPath } from './appSupport';
interface Options {
  sessions: Session[];
  daemonWorkspaces: DaemonWorkspace[];
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null;
  activeSessionId: string | null;
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  view: ReturnType<typeof useAppView>['view'];
  settings: Record<string, string>;
  sendSupportSnapshot: ReturnType<typeof useDaemonApi>['sendSupportSnapshot'];
  getPaneText: (sessionId: string, paneId: string) => string;
}
export function useAppDiagnostics({
  sessions,
  daemonWorkspaces,
  getPaneSize,
  activeSessionId,
  getActivePaneIdForSession,
  view,
  settings,
  sendSupportSnapshot,
  getPaneText,
}: Options) {
  const diagnosticReportSaved = useSavedFlash();
  const [diagnosticCapture, setDiagnosticCapture] = useState<{
    capture: PendingDiagnosticCapture;
    affectedPaneId: string | null;
  } | null>(null);
  const actionMenuOriginRef = useRef<DiagnosticCaptureContext | null>(null);

  const diagnosticPanes = useCallback((): DiagnosticPaneDescriptor[] => {
    const sessionById = new Map(sessions.map((session) => [session.id, session]));
    const workspaceById = new Map(daemonWorkspaces.map((workspace) => [workspace.id, workspace]));
    const panes = new Map<string, DiagnosticPaneDescriptor>();
    for (const session of sessions) {
      for (const pane of session.workspace.agents) {
        if (panes.has(pane.id)) continue;
        panes.set(pane.id, {
          paneId: pane.id,
          runtimeId: pane.runtimeId,
          sessionId: pane.sessionId,
          title: pane.title,
          sessionLabel: sessionById.get(pane.sessionId)?.label || pane.title,
          workspaceId: session.workspaceId,
          workspaceLabel: workspaceById.get(session.workspaceId)?.title || session.workspaceId,
          available: getPaneSize(pane.sessionId, pane.id) !== null,
        });
      }
    }
    return [...panes.values()];
  }, [daemonWorkspaces, getPaneSize, sessions]);

  const handleCreateDiagnosticReport = useCallback(async () => {
    const fallbackSession = activeSessionId
      ? sessions.find((session) => session.id === activeSessionId)
      : null;
    const fallbackPaneId = fallbackSession
      ? getActivePaneIdForSession(fallbackSession) || null
      : null;
    const context = actionMenuOriginRef.current ?? {
      capturedAtUnixMs: Date.now(),
      view,
      activeSessionId,
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
    const workspaceById = new Map(daemonWorkspaces.map((workspace) => [workspace.id, workspace]));
    const workspaces = new Map<
      string,
      {
        id: string;
        label: string;
        directory: string;
        layout: unknown;
      }
    >();
    for (const session of sessions) {
      if (workspaces.has(session.workspaceId)) continue;
      const workspace = workspaceById.get(session.workspaceId);
      workspaces.set(session.workspaceId, {
        id: session.workspaceId,
        label: workspace?.title || session.workspaceId,
        directory: shortenDiagnosticPath(workspace?.directory || session.cwd),
        layout: collectWorkspaceLayoutDiagnostics(session.workspace.layoutTree),
      });
    }
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
        workspaceId: session.workspaceId,
        endpoint: session.endpointId ? 'remote' : 'local',
        ...(session.endpointId ? { endpointId: session.endpointId } : {}),
        active: session.id === context.activeSessionId,
      })),
      workspaces: [...workspaces.values()],
      settings,
      sendSupportSnapshot,
    });
    setDiagnosticCapture({ capture, affectedPaneId: context.activePaneId });
  }, [
    activeSessionId,
    daemonWorkspaces,
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
      diagnosticReportSaved.flash('saved');
    },
    [diagnosticCapture, diagnosticReportSaved.flash, getPaneSize, getPaneText],
  );

  return {
    diagnosticCapture,
    handleCreateDiagnosticReport,
    actionMenuOriginRef,
    diagnosticReportSaved,
    handleSaveDiagnosticReport,
    setDiagnosticCapture,
  };
}
