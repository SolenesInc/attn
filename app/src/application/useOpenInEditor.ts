import { invoke } from '@tauri-apps/api/core';
import { useCallback, useMemo } from 'react';
import { useSessionBehindScreen } from '../hooks/useDesktopSelectionBridge';
import { outpostPathRefusal } from '../utils/outpostPaths';
import { useAppErrorsContext, useAppInputs, useAppSessionsContext } from './AppContexts';

export function useOpenInEditor() {
  const { settings } = useAppInputs();
  const { showError } = useAppErrorsContext();
  const sessionId = useSessionBehindScreen();
  const { enrichedLocalSessions } = useAppSessionsContext();
  const activeSession = enrichedLocalSessions.find((entry) => entry.id === sessionId);

  const handleOpenEditor = useCallback(
    async (cwd: string, filePath?: string) => {
      try {
        await invoke('open_in_editor', {
          cwd,
          filePath,
          editor: settings.editor_executable || '',
        });
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        showError(message || 'Failed to open editor');
      }
    },
    [settings.editor_executable, showError],
  );

  const hasSession = Boolean(activeSession);
  const { cwd, host, hostName } = activeSession ?? {};
  const editorTarget = useMemo(
    () => resolveEditorTarget(hasSession ? { cwd, host, hostName } : undefined),
    [hasSession, cwd, host, hostName],
  );

  const openActiveSessionInEditor = useCallback(() => {
    if ('unavailable' in editorTarget) {
      showError(editorTarget.unavailable);
      return;
    }
    if ('refused' in editorTarget) {
      showError(editorTarget.refused);
      return;
    }
    handleOpenEditor(editorTarget.cwd);
  }, [editorTarget, handleOpenEditor, showError]);

  const editorUnavailableReason = 'unavailable' in editorTarget ? editorTarget.unavailable : null;
  return { openActiveSessionInEditor, editorUnavailableReason };
}

type EditorTarget = { cwd: string } | { unavailable: string } | { refused: string };

function resolveEditorTarget(session: { cwd?: string; host?: string; hostName?: string } | undefined): EditorTarget {
  if (!session) return { unavailable: 'No active session' };
  if (!session.cwd) return { unavailable: 'No session folder' };
  if (session.host) return { refused: outpostPathRefusal(session.cwd, session.hostName ?? session.host) };
  return { cwd: session.cwd };
}
