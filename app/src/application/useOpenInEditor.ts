import { invoke } from '@tauri-apps/api/core';
import { useCallback, useMemo } from 'react';
import { useSessionStore } from '../store/sessions';
import { useAppErrorsContext, useAppInputs, useAppSessionsContext } from './AppContexts';

export function useOpenInEditor() {
  const { settings } = useAppInputs();
  const { activeEndpoint, activeRemoteSession } = useAppSessionsContext();
  const { showError } = useAppErrorsContext();
  const sessions = useSessionStore((state) => state.sessions);
  const activeSessionId = useSessionStore((state) => state.activeSessionId);

  const isZedEditorConfigured = useMemo(() => {
    const editor = (settings.editor_executable || '').trim().toLowerCase();
    if (!editor) {
      return false;
    }
    return editor.includes('zed');
  }, [settings.editor_executable]);

  const handleOpenEditor = useCallback(
    async (cwd: string, filePath?: string, remoteTarget?: string) => {
      try {
        await invoke('open_in_editor', {
          cwd,
          filePath,
          editor: settings.editor_executable || '',
          remoteTarget,
        });
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        showError(message || 'Failed to open editor');
      }
    },
    [settings.editor_executable, showError],
  );

  const openActiveSessionInEditor = useCallback(() => {
    const activeSession = sessions.find((s) => s.id === activeSessionId);
    if (!activeSession?.cwd) {
      showError('No active session directory');
      return;
    }
    if (activeSession.endpointId) {
      if (!activeEndpoint) {
        showError('Remote endpoint not available.');
        return;
      }
      if (!isZedEditorConfigured) {
        showError('Remote open-in-editor currently requires Zed.');
        return;
      }
      handleOpenEditor(activeSession.cwd, undefined, activeEndpoint.ssh_target);
      return;
    }
    handleOpenEditor(activeSession.cwd);
  }, [
    sessions,
    activeSessionId,
    activeEndpoint,
    handleOpenEditor,
    isZedEditorConfigured,
    showError,
  ]);

  const remoteEditorAvailable = Boolean(
    activeRemoteSession && activeEndpoint && isZedEditorConfigured,
  );

  return { openActiveSessionInEditor, remoteEditorAvailable };
}
