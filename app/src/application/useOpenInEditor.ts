import { invoke } from '@tauri-apps/api/core';
import { useCallback, useMemo } from 'react';
import { useSessionBehindScreen } from '../hooks/useDesktopSelectionBridge';
import { useSessionStore } from '../store/sessions';
import { useAppErrorsContext, useAppInputs } from './AppContexts';

export function useOpenInEditor() {
  const { settings } = useAppInputs();
  const { showError } = useAppErrorsContext();
  const sessionId = useSessionBehindScreen();
  const activeSession = useSessionStore((state) => state.sessions.find((entry) => entry.id === sessionId));

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

  const editorTarget = useMemo(() => resolveEditorTarget(activeSession), [activeSession]);

  const openActiveSessionInEditor = useCallback(() => {
    if ('unavailable' in editorTarget) {
      showError(editorTarget.unavailable);
      return;
    }
    handleOpenEditor(editorTarget.cwd);
  }, [editorTarget, handleOpenEditor, showError]);

  const editorUnavailableReason = 'unavailable' in editorTarget ? editorTarget.unavailable : null;
  return { openActiveSessionInEditor, editorUnavailableReason };
}

type EditorTarget = { cwd: string } | { unavailable: string };

function resolveEditorTarget(session: { cwd?: string } | undefined): EditorTarget {
  if (!session) return { unavailable: 'No active session' };
  if (!session.cwd) return { unavailable: 'No session folder' };
  return { cwd: session.cwd };
}
