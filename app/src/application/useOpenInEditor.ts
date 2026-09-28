import { invoke } from '@tauri-apps/api/core';
import { useCallback, useMemo } from 'react';
import { useSessionStore } from '../store/sessions';
import { useAppErrorsContext, useAppInputs, useAppSessionsContext } from './AppContexts';
import { useAgentOnScreen } from '../hooks/useDesktopSelectionBridge';

export function useOpenInEditor() {
  const { settings } = useAppInputs();
  const { activeEndpoint } = useAppSessionsContext();
  const { showError } = useAppErrorsContext();
  const sessions = useSessionStore((state) => state.sessions);
  const shownAgentId = useAgentOnScreen();

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

  const activeSession = sessions.find((s) => s.id === shownAgentId);
  const activeSessionIsRemote = Boolean(activeSession?.endpointId);
  const editorTarget = useMemo(
    () => resolveEditorTarget(activeSession, activeEndpoint?.ssh_target, isZedEditorConfigured),
    [activeSession, activeEndpoint?.ssh_target, isZedEditorConfigured],
  );

  const openActiveSessionInEditor = useCallback(() => {
    if ('unavailable' in editorTarget) {
      showError(editorTarget.unavailable);
      return;
    }
    handleOpenEditor(editorTarget.cwd, undefined, editorTarget.remoteTarget);
  }, [editorTarget, handleOpenEditor, showError]);

  const editorUnavailableReason = 'unavailable' in editorTarget ? editorTarget.unavailable : null;
  return { openActiveSessionInEditor, activeSessionIsRemote, editorUnavailableReason };
}

type EditorTarget = { cwd: string; remoteTarget?: string } | { unavailable: string };

function resolveEditorTarget(
  session: { cwd?: string; endpointId?: string } | undefined,
  endpointSshTarget: string | undefined,
  zedConfigured: boolean,
): EditorTarget {
  if (!session) return { unavailable: 'No active session' };
  if (!session.cwd) return { unavailable: 'No session folder' };
  if (!session.endpointId) return { cwd: session.cwd };
  if (!endpointSshTarget) return { unavailable: 'Remote endpoint not available' };
  if (!zedConfigured) return { unavailable: 'Remote requires Zed' };
  return { cwd: session.cwd, remoteTarget: endpointSshTarget };
}
