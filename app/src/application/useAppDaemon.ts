import { useEffect } from 'react';
import { setMarkdownAnnotationsTransport } from '../components/MarkdownReader/annotations/transport';
import { useDaemonSocket, type UseDaemonSocketOptions } from '../hooks/useDaemonSocket';
import { useDaemonStore } from '../store/daemonSessions';
import { useSessionStore } from '../store/sessions';

type AppDaemonCallbacks = Omit<
  Partial<UseDaemonSocketOptions>,
  'onSessionsUpdate' | 'onSeedsUpdate' | 'onAppsUpdate' | 'onCrewUpdate' | 'onPRsUpdate' | 'onReposUpdate' | 'onAuthorsUpdate'
>;

export function useAppDaemon(callbacks: AppDaemonCallbacks) {
  const { setDaemonSessions, setSeeds, setApps, setCrew, setPRs, setRepoStates, setAuthorStates } = useDaemonStore();
  const daemon = useDaemonSocket({
    ...callbacks,
    onSessionsUpdate: (sessions) => {
      useSessionStore.getState().syncFromDaemonSessions(sessions);
      setDaemonSessions(sessions);
    },
    onWorkspacesUpdate: (workspaces) => {
      useSessionStore.getState().syncFromDaemonWorkspaces(workspaces);
      callbacks.onWorkspacesUpdate?.(workspaces);
    },
    onSettingsUpdate: (settings) => {
      useSessionStore.getState().syncNavigationSettings(settings);
      callbacks.onSettingsUpdate?.(settings);
    },
    onSeedsUpdate: setSeeds,
    onAppsUpdate: setApps,
    onCrewUpdate: setCrew,
    onPRsUpdate: setPRs,
    onReposUpdate: setRepoStates,
    onAuthorsUpdate: setAuthorStates,
  });

  const {
    getMarkdownAnnotations,
    saveMarkdownAnnotations,
    clearMarkdownAnnotations,
    submitMarkdownAnnotations,
  } = daemon;

  useEffect(() => {
    setMarkdownAnnotationsTransport({
      getMarkdownAnnotations,
      saveMarkdownAnnotations,
      clearMarkdownAnnotations,
      submitMarkdownAnnotations,
    });
    return () => {
      setMarkdownAnnotationsTransport(null);
    };
  }, [
    getMarkdownAnnotations,
    saveMarkdownAnnotations,
    clearMarkdownAnnotations,
    submitMarkdownAnnotations,
  ]);

  return daemon;
}
