import { useCallback, useEffect } from 'react';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { SessionExitInfo } from '../hooks/useDaemonSocket';
import type { useDesktopRuntimeController } from '../hooks/useDesktopRuntimeController';
import { useProfilesStore } from '../store/profiles';
import { isSessionReloading, useSessionStore } from '../store/sessions';
import { AppContentProps, sessionCloseProtectionHint } from './appSupport';
import { useAppSessions } from './useAppSessions';

interface Options {
  shownAgentId: string | null;
  handleCloseTile: (desktopId: string, tileId: string) => void;
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  daemonSessions: AppContentProps['daemonSessions'];
  enrichedLocalSessions: ReturnType<typeof useAppSessions>['enrichedLocalSessions'];
  registerSessionExitHandler: AppContentProps['registerSessionExitHandler'];
  getPaneSize: ReturnType<typeof useDesktopRuntimeController>['getPaneSize'];
  handleSelectSession: (id: string) => boolean;
  showError: ReturnType<typeof useToast>['showError'];
  chooseReopenDirectory: () => Promise<string | undefined>;
  onReopened: () => void;
}
export function useSessionLifecycle({
  shownAgentId,
  handleCloseTile,
  sessions,
  daemonSessions,
  enrichedLocalSessions,
  registerSessionExitHandler,
  getPaneSize,
  handleSelectSession,
  showError,
  chooseReopenDirectory,
  onReopened,
}: Options) {
  const { sendUnregisterSession, sendSessionReopen } = useDaemonApi();
  const { closeSession, reloadSession } = useSessionStore();
  const handleCloseSession = useCallback(
    async (id: string) => {
      const closeProtection = sessionCloseProtectionHint(daemonSessions, id);
      if (closeProtection) {
        showError(closeProtection);
        return;
      }
      const session = enrichedLocalSessions.find((s) => s.id === id);

      const localDaemonSession = daemonSessions.find((ds) => ds.id === session?.id);
      if (localDaemonSession && session) {
        await sendUnregisterSession(session.id);
      } else {
        closeSession(id);
      }
    },
    [closeSession, daemonSessions, enrichedLocalSessions, sendUnregisterSession, showError],
  );

  const handleRequestCloseSession = useCallback(
    (id: string) => {
      if (!sessions.some((entry) => entry.id === id)) return;
      void handleCloseSession(id).catch(console.error);
    },
    [handleCloseSession, sessions],
  );

  const handleSessionProcessExit = useCallback(
    (info: SessionExitInfo) => {
      if (info.exitCode !== 0 || info.signal) {
        return;
      }
      // A reload's kill can surface as a clean exit (code 0, no signal); the same id is about to respawn in place, so closing the pane here would tear the pane down under the pending spawn.
      if (isSessionReloading(info.id)) {
        return;
      }
      handleRequestCloseSession(info.id);
    },
    [handleRequestCloseSession],
  );

  useEffect(() => {
    registerSessionExitHandler(handleSessionProcessExit);
    return () => registerSessionExitHandler(null);
  }, [registerSessionExitHandler, handleSessionProcessExit]);

  const handleReloadSession = useCallback(
    (id: string) => {
      const session = sessions.find((entry) => entry.id === id);
      const paneId = session?.desktop.agents.find((pane) => pane.sessionId === id)?.id;
      const size = paneId ? getPaneSize(id, paneId) || undefined : undefined;
      void reloadSession(id, size).catch((error) => {
        const message = error instanceof Error ? error.message : String(error);
        showError(`Failed to reload session: ${message}`);
      });
    },
    [getPaneSize, reloadSession, sessions, showError],
  );

  const handleReopenSession = useCallback(
    async (sessionId: string, actionId: string): Promise<boolean> => {
      let directory: string | undefined;
      if (actionId === 'start_fresh_elsewhere') {
        const chosen = await chooseReopenDirectory();
        if (!chosen) return false;
        directory = chosen;
      }
      const result = await sendSessionReopen(sessionId, actionId, directory);
      handleSelectSession(result.session_id);
      onReopened();
      return true;
    },
    [sendSessionReopen, chooseReopenDirectory, onReopened, handleSelectSession],
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
    const currentDesktopId = useProfilesStore.getState().currentDesktopId;
    if (isTile && currentDesktopId) {
      handleCloseTile(currentDesktopId, tileId);
      return;
    }
    if (shownAgentId) {
      handleRequestCloseSession(shownAgentId);
    }
  }, [shownAgentId, handleCloseTile, handleRequestCloseSession]);

  return {
    handleCloseCurrentSessionShortcut,
    handleCloseSession,
    handleRequestCloseSession,
    handleReloadSession,
    handleReopenSession,
  };
}
