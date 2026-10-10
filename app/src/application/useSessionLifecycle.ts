import { useCallback, useState } from 'react';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
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
  chooseReopenDirectory: () => Promise<string | undefined>;
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
  chooseReopenDirectory,
  onReopened,
}: Options) {
  const { sendUnregisterSession, sendSessionReopen, sendDesktopClose } = useDaemonApi();
  const { closeSession, reloadSession } = useSessionStore();
  const [desktopClosePrompt, setDesktopClosePrompt] = useState<{
    desktop: Desktop; label: string;
  } | null>(null);
  const closeProtection = useCallback((desktop: Desktop) => {
    const protectedNames = desktop.panes.flatMap((pane) => {
      if (!sessionCloseProtectionHint(daemonSessions, pane.session_id)) return [];
      const session = daemonSessions.find((entry) => entry.id === pane.session_id);
      return [session?.label || pane.title || pane.session_id];
    });
    if (!protectedNames.length) return false;
    showError(`Cannot close desktop: move these protected sessions first: ${protectedNames.join(', ')}.`);
    return true;
  }, [daemonSessions, showError]);
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
      return;
    }
    const desktop = useProfilesStore.getState().desktops.find((entry) => entry.id === currentDesktopId);
    if (desktop && collectLayoutLeaves(parseLayoutJSON(desktop.tree_json)).length === 0) {
      handleRequestCloseDesktop(desktop.id);
    }
  }, [shownAgentId, handleCloseTile, handleRequestCloseSession, handleRequestCloseDesktop]);

  return {
    desktopClosePrompt,
    cancelCloseDesktop: () => setDesktopClosePrompt(null),
    confirmCloseDesktop,
    handleRequestCloseDesktop,
    handleCloseCurrentSessionShortcut,
    handleCloseSession,
    handleRequestCloseSession,
    handleReloadSession,
    handleReopenSession,
  };
}
