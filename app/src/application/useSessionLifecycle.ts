import { sessionDisplayName } from '../utils/sessionDisplayName';
import { useCallback, useState } from 'react';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { SessionReopenRefusal } from '../hooks/daemonSessionLedgerEvents';
import type { useDesktopRuntimeController } from '../hooks/useDesktopRuntimeController';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { AppContentProps, sessionCloseProtectionHint } from './appSupport';
import { useAppSessions } from './useAppSessions';
import { collectLayoutLeaves, parseLayoutJSON } from '../types/desktop';
import { desktopLabel } from '../utils/desktops';
import type { Desktop } from '../types/generated';

interface Options {
  shownAgentId: string | null;
  handleCloseTile: (desktopId: string, tileId: string) => void;
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  daemonSessions: AppContentProps['daemonSessions'];
  enrichedLocalSessions: ReturnType<typeof useAppSessions>['enrichedLocalSessions'];
  getPaneSize: ReturnType<typeof useDesktopRuntimeController>['getPaneSize'];
  handleSelectSession: (id: string) => boolean;
  showError: ReturnType<typeof useToast>['showError'];
  onReopened: () => void;
}
export function useSessionLifecycle({
  shownAgentId,
  handleCloseTile,
  sessions,
  daemonSessions,
  enrichedLocalSessions,
  getPaneSize,
  handleSelectSession,
  showError,
  onReopened,
}: Options) {
  const { sendUnregisterSession, sendDesktopCloseTile, sendSessionReopen, sendDesktopClose } = useDaemonApi();
  const { closeSession, reloadSession: reloadSessionInStore } = useSessionStore();
  const [desktopClosePrompt, setDesktopClosePrompt] = useState<{
    desktop: Desktop; label: string;
  } | null>(null);
  const refuseOffline = useCallback((id: string) => {
    const session = enrichedLocalSessions.find((entry) => entry.id === id);
    if (!session?.offline) return false;
    showError(`${session.hostName} is unreachable; this needs it now`);
    return true;
  }, [enrichedLocalSessions, showError]);
  const closeProtection = useCallback((desktop: Desktop) => {
    if (desktop.panes.some((pane) => refuseOffline(pane.session_id))) return true;
    const protectedNames = desktop.panes.flatMap((pane) => {
      if (!sessionCloseProtectionHint(daemonSessions, pane.session_id)) return [];
      const session = daemonSessions.find((entry) => entry.id === pane.session_id);
      return [session ? sessionDisplayName(session) : pane.title || pane.session_id];
    });
    if (!protectedNames.length) return false;
    showError(`Cannot close desktop: move these protected sessions first: ${protectedNames.join(', ')}.`);
    return true;
  }, [daemonSessions, refuseOffline, showError]);
  const closeDesktop = useCallback(async (desktop: Desktop) => {
    if (closeProtection(desktop)) return;
    try {
      await sendDesktopClose(desktop.id, desktop.revision);
    } catch (error) {
      showError(error instanceof Error ? error.message : String(error));
    }
  }, [closeProtection, sendDesktopClose, showError]);
  const handleRequestCloseDesktop = useCallback((id: string) => {
    const { desktops } = useProfilesStore.getState();
    const desktop = desktops.find((entry) => entry.id === id);
    if (!desktop || closeProtection(desktop)) return;
    const leaves = collectLayoutLeaves(parseLayoutJSON(desktop.tree_json));
    if (!leaves.length) {
      void closeDesktop(desktop);
      return;
    }
    setDesktopClosePrompt({ desktop, label: desktopLabel(desktop, desktops) });
  }, [closeProtection, closeDesktop]);
  const confirmCloseDesktop = useCallback(() => {
    if (!desktopClosePrompt) return;
    setDesktopClosePrompt(null);
    void closeDesktop(desktopClosePrompt.desktop);
  }, [desktopClosePrompt, closeDesktop]);

  const closeWith = useCallback(
    async (id: string, close: () => Promise<unknown>) => {
      if (refuseOffline(id)) return;
      const session = enrichedLocalSessions.find((s) => s.id === id);

      const localDaemonSession = daemonSessions.find((ds) => ds.id === session?.id);
      if (localDaemonSession && session) {
        await close();
      } else {
        closeSession(id);
      }
    },
    [closeSession, daemonSessions, enrichedLocalSessions, refuseOffline],
  );
  const handleCloseSession = useCallback(
    async (id: string) => {
      const closeProtection = sessionCloseProtectionHint(daemonSessions, id);
      if (closeProtection) {
        showError(closeProtection);
        return;
      }
      await closeWith(id, () => sendUnregisterSession(id));
    },
    [closeWith, daemonSessions, sendUnregisterSession, showError],
  );
  const handleCloseTerminalTile = useCallback(
    (desktopId: string, tileId: string, sessionId: string) => {
      void closeWith(sessionId, () => sendDesktopCloseTile(desktopId, tileId)).catch((error) => {
        showError(`Could not close that tile: ${error instanceof Error ? error.message : String(error)}`);
      });
    },
    [closeWith, sendDesktopCloseTile, showError],
  );

  const handleRequestCloseSession = useCallback(
    (id: string) => {
      if (!sessions.some((entry) => entry.id === id)) return;
      void handleCloseSession(id).catch(console.error);
    },
    [handleCloseSession, sessions],
  );

  const reloadSession = useCallback(async (id: string, size?: { cols: number; rows: number }) => {
    const session = enrichedLocalSessions.find((entry) => entry.id === id);
    if (session?.offline) throw new Error(`${session.hostName} is unreachable; this needs it now`);
    await reloadSessionInStore(id, size);
  }, [enrichedLocalSessions, reloadSessionInStore]);

  const handleReloadSession = useCallback(
    (id: string) => {
      if (refuseOffline(id)) return;
      const session = sessions.find((entry) => entry.id === id);
      const paneId = session?.desktop.agents.find((pane) => pane.sessionId === id)?.id;
      const size = paneId ? getPaneSize(id, paneId) || undefined : undefined;
      void reloadSession(id, size).catch((error) => {
        if (!useSessionStore.getState().sessions.some((entry) => entry.id === id)) return;
        const message = error instanceof Error ? error.message : String(error);
        showError(`Failed to reload session: ${message}`);
      });
    },
    [getPaneSize, refuseOffline, reloadSession, sessions, showError],
  );

  const handleReopenSession = useCallback(
    async (sessionId: string, actionId: string): Promise<boolean> => {
      let result;
      try {
        result = await sendSessionReopen(sessionId, actionId);
      } catch (error) {
        if (error instanceof SessionReopenRefusal) throw error;
        showError(error instanceof Error ? error.message : String(error));
        if (useProfilesStore.getState().desktops.some((desktop) => desktop.panes.some((pane) => pane.session_id === sessionId))) {
          handleSelectSession(sessionId);
          onReopened();
          return false;
        }
        throw error;
      }
      handleSelectSession(result.session_id);
      onReopened();
      return true;
    },
    [sendSessionReopen, onReopened, handleSelectSession, showError],
  );

  const handleCloseCurrentSessionShortcut = useCallback(() => {
    // The packaged app's native "Close Pane" item claims Cmd+W and dispatches session.close, so a focused docked tile must be closed here, not the session.
    const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const tileId =
      focused?.closest('[data-pane-kind="tile"]')?.getAttribute('data-pane-id') ??
      focused?.closest('.session-terminal-desktop')?.getAttribute('data-active-leaf-id') ??
      '';
    const isTile =
      !!tileId &&
      !!document.querySelector(`[data-pane-kind="tile"][data-pane-id="${CSS.escape(tileId)}"]`);
    const { currentDesktopId, desktops } = useProfilesStore.getState();
    if (isTile && currentDesktopId) {
      handleCloseTile(currentDesktopId, tileId);
      return;
    }
    if (!shownAgentId) {
      const desktop = desktops.find((entry) => entry.id === currentDesktopId);
      if (desktop && collectLayoutLeaves(parseLayoutJSON(desktop.tree_json)).length === 0) {
        handleRequestCloseDesktop(desktop.id);
      }
      return;
    }
    const showing = desktops.flatMap((desktop) => desktop.panes.filter((pane) => pane.session_id === shownAgentId));
    const current = desktops.find((desktop) => desktop.id === currentDesktopId);
    const here = current?.panes.filter((pane) => pane.session_id === shownAgentId) ?? [];
    const pane = here.find((candidate) => candidate.pane_id === current?.active_pane_id) ?? here[0];
    if (current && pane && showing.length > 1) {
      handleCloseTerminalTile(current.id, pane.pane_id, shownAgentId);
      return;
    }
    handleRequestCloseSession(shownAgentId);
  }, [shownAgentId, handleCloseTile, handleCloseTerminalTile, handleRequestCloseSession, handleRequestCloseDesktop]);

  return {
    desktopClosePrompt,
    cancelCloseDesktop: () => setDesktopClosePrompt(null),
    confirmCloseDesktop,
    handleRequestCloseDesktop,
    handleCloseCurrentSessionShortcut,
    handleCloseSession,
    handleCloseTerminalTile,
    handleRequestCloseSession,
    handleReloadSession,
    reloadSession,
    handleReopenSession,
  };
}
