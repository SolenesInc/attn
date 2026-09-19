import { invoke } from '@tauri-apps/api/core';
import { useCallback, useEffect, useRef, useState } from 'react';
import './App.css';
import { AppContent } from './application/AppContent';
import { setMarkdownAnnotationsTransport } from './components/MarkdownReader/annotations/transport';
import { DaemonApiProvider } from './contexts/DaemonApiContext';
import { KeybindingsProvider } from './contexts/KeybindingsContext';
import { SettingsProvider } from './contexts/SettingsContext';
import {
  CriticalNotificationState,
  DaemonEndpoint,
  DaemonPlugin,
  DaemonPluginIssue,
  DaemonWorkspace,
  DaemonWorktree,
  SessionExitInfo,
  useDaemonSocket,
} from './hooks/useDaemonSocket';
import { useReleaseUpdates } from './hooks/useReleaseUpdates';
import { useSessionStore } from './store/sessions';
import { useDaemonStore } from './store/daemonSessions';
import type { Presentation, SessionLedgerEntry } from './types/generated';
import type { SessionReopenResolutionEvent } from './hooks/daemonSessionLedgerEvents';
import { hideBootSplash } from './utils/bootSplash';
import { bumpFsChangeSignal } from './utils/fsChangeSignals';
import { seedPresentationNotices, upsertPresentationNotice } from './utils/presentationNotices';
function App() {
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [settingError, setSettingError] = useState<string | null>(null);
  const [daemonEndpoints, setDaemonEndpoints] = useState<DaemonEndpoint[]>([]);
  const [daemonPlugins, setDaemonPlugins] = useState<DaemonPlugin[]>([]);
  const [daemonPluginIssues, setDaemonPluginIssues] = useState<DaemonPluginIssue[]>([]);
  const [daemonGitHubHosts, setDaemonGitHubHosts] = useState<string[]>([]);
  const [githubPollingOffReason, setGithubPollingOffReason] = useState<string | null>(null);
  const handleGitHubHostsUpdate = useCallback(
    (hosts: string[], pollingOffReason: string | null) => {
      setDaemonGitHubHosts(hosts);
      setGithubPollingOffReason(pollingOffReason);
    },
    [],
  );
  const handlePluginsUpdate = useCallback(
    (plugins: DaemonPlugin[], issues: DaemonPluginIssue[]) => {
      setDaemonPlugins(plugins);
      setDaemonPluginIssues(issues);
    },
    [],
  );

  const [daemonWorkspaces, setDaemonWorkspaces] = useState<DaemonWorkspace[]>([]);

  const [, setWorktrees] = useState<DaemonWorktree[]>([]);

  const { updateAvailableVersion, handleOpenLatestRelease, handleDismissLatestRelease } =
    useReleaseUpdates();

  const [presentationNotices, setPresentationNotices] = useState<Presentation[]>([]);
  const [sessionCloseNotice, setSessionCloseNotice] = useState<{
    entry: SessionLedgerEntry;
    nonce: number;
  }>();
  const [sessionResolutionNotice, setSessionResolutionNotice] = useState<{
    resolutions: Record<string, SessionReopenResolutionEvent>;
    nonce: number;
  }>();

  const {
    daemonSessions,
    setDaemonSessions,
    setSeeds,
    setApps,
    setCrew,
    prs,
    setPRs,
    setRepoStates,
    setAuthorStates,
  } = useDaemonStore();

  useEffect(() => {
    hideBootSplash();
  }, []);

  useEffect(() => {
    async function ensureDaemon() {
      try {
        await invoke('ensure_daemon');
        console.log('[App] Daemon ensured');
      } catch (err) {
        console.error('[App] Failed to start daemon:', err);
      }
    }
    ensureDaemon();
  }, []);

  const sessionExitHandlerRef = useRef<((info: SessionExitInfo) => void) | null>(null);
  const registerSessionExitHandler = useCallback(
    (handler: ((info: SessionExitInfo) => void) | null) => {
      sessionExitHandlerRef.current = handler;
    },
    [],
  );
  const handleSessionExited = useCallback((info: SessionExitInfo) => {
    sessionExitHandlerRef.current?.(info);
  }, []);

  const [fsChangeSignals, setFsChangeSignals] = useState<Record<string, number>>({});
  const [notebookTaskChangeSignal, setTaskChangeSignal] = useState(0);
  const [notificationsUnread, setNotificationsUnread] = useState(0);
  const [notificationsChangeSignal, setNotificationsChangeSignal] = useState(0);
  const [criticalNotifications, setCriticalNotificationsState] =
    useState<CriticalNotificationState>({ count: 0, title: '' });
  const setCriticalNotifications = useCallback((next: CriticalNotificationState) => {
    setCriticalNotificationsState((prev) =>
      prev.count === next.count && prev.title === next.title ? prev : next,
    );
  }, []);

  const daemon = useDaemonSocket({
    onSessionsUpdate: (sessions) => {
      useSessionStore.getState().syncFromDaemonSessions(sessions);
      setDaemonSessions(sessions);
    },
    onPresentationAdded: (p) => setPresentationNotices((prev) => upsertPresentationNotice(prev, p)),
    onPresentationUpdated: (p) =>
      setPresentationNotices((prev) => upsertPresentationNotice(prev, p)),
    onFsChanged: (_origin, _paths, root) => {
      setFsChangeSignals((prev) =>
        bumpFsChangeSignal(prev, root, settings['notebook.root.effective'] || ''),
      );
    },
    onTasksChanged: () => setTaskChangeSignal((n) => n + 1),
    onNotificationsUpdated: (unread, critical) => {
      setNotificationsUnread(unread);
      setCriticalNotifications(critical);
      setNotificationsChangeSignal((n) => n + 1);
    },
    onSeedsUpdate: setSeeds,
    onAppsUpdate: setApps,
    onCrewUpdate: setCrew,
    onWorkspacesUpdate: (workspaces) => {
      useSessionStore.getState().syncFromDaemonWorkspaces(workspaces);
      setDaemonWorkspaces(workspaces);
    },
    onPRsUpdate: setPRs,
    onEndpointsUpdate: setDaemonEndpoints,
    onPluginsUpdate: handlePluginsUpdate,
    onGitHubHostsUpdate: handleGitHubHostsUpdate,
    onReposUpdate: setRepoStates,
    onAuthorsUpdate: setAuthorStates,
    onSettingsUpdate: (nextSettings) => {
      useSessionStore.getState().syncNavigationSettings(nextSettings);
      setSettings(nextSettings);
    },
    onSettingError: setSettingError,
    onWorktreesUpdate: setWorktrees,
    onSessionExited: handleSessionExited,
    onSessionClosed: (entry) =>
      setSessionCloseNotice((prev) => ({ entry, nonce: (prev?.nonce ?? 0) + 1 })),
    onSessionReopenResolved: (resolution) =>
      setSessionResolutionNotice((prev) => ({
        resolutions: { ...prev?.resolutions, [resolution.sessionId]: resolution },
        nonce: (prev?.nonce ?? 0) + 1,
      })),
  });

  const {
    getMarkdownAnnotations,
    saveMarkdownAnnotations,
    clearMarkdownAnnotations,
    submitMarkdownAnnotations,
    sendSetSetting,
    sendNotificationList,
    getPresentations,
    hasReceivedInitialState,
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

  useEffect(() => {
    if (!hasReceivedInitialState) return;
    let cancelled = false;
    sendNotificationList()
      .then((r) => {
        if (cancelled) return;
        setNotificationsUnread(r.unreadCount);
        setCriticalNotifications(r.critical);
      })
      .catch(() => {
        /* transient (not connected / timeout); the next broadcast reseeds */
      });
    return () => {
      cancelled = true;
    };
  }, [hasReceivedInitialState, sendNotificationList, setCriticalNotifications]);

  useEffect(() => {
    if (!hasReceivedInitialState) return;
    let cancelled = false;
    getPresentations()
      .then((all) => {
        if (!cancelled) setPresentationNotices(seedPresentationNotices(all));
      })
      .catch(() => {
        /* transient (not connected / timeout); the next broadcast reseeds */
      });
    return () => {
      cancelled = true;
    };
  }, [hasReceivedInitialState, getPresentations]);

  return (
    <SettingsProvider settings={settings} setSetting={sendSetSetting}>
      <KeybindingsProvider>
        <DaemonApiProvider api={daemon}>
          <AppContent
            daemonSessions={daemonSessions}
            daemonWorkspaces={daemonWorkspaces}
            prs={prs}
            daemonEndpoints={daemonEndpoints}
            daemonPlugins={daemonPlugins}
            daemonPluginIssues={daemonPluginIssues}
            daemonGitHubHosts={daemonGitHubHosts}
            githubPollingOffReason={githubPollingOffReason}
            settings={settings}
            updateAvailableVersion={updateAvailableVersion}
            onOpenLatestRelease={handleOpenLatestRelease}
            onDismissLatestRelease={handleDismissLatestRelease}
            presentationNotices={presentationNotices}
            settingError={settingError}
            clearSettingError={() => setSettingError(null)}
            notificationsUnread={notificationsUnread}
            criticalNotifications={criticalNotifications}
            notificationsChangeSignal={notificationsChangeSignal}
            fsChangeSignals={fsChangeSignals}
            notebookTaskChangeSignal={notebookTaskChangeSignal}
            sessionCloseNotice={sessionCloseNotice}
            sessionResolutionNotice={sessionResolutionNotice}
            registerSessionExitHandler={registerSessionExitHandler}
          />
        </DaemonApiProvider>
      </KeybindingsProvider>
    </SettingsProvider>
  );
}
export default App;
