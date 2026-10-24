import { Toast } from './components/Toast';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { emitTo } from '@tauri-apps/api/event';
import { QUICK_CAPTURE_READ } from './quickCapture/client';
import { useCallback, useEffect, useState } from 'react';
import './App.css';
import { useQuickCaptureHost } from './application/useQuickCaptureHost';
import { QuickCaptureShortcutProvider } from './quickCapture/ShortcutContext';
import { AppContent } from './application/AppContent';
import { setMarkdownAnnotationsTransport } from './components/MarkdownReader/annotations/transport';
import { MigrationFailureScreen } from './components/MigrationFailureScreen';
import { MigrationGate } from './components/MigrationPicker/MigrationGate';
import { DaemonApiProvider } from './contexts/DaemonApiContext';
import { KeybindingsProvider } from './contexts/KeybindingsContext';
import { SettingsProvider } from './contexts/SettingsContext';
import {
  CriticalNotificationState,
  DaemonPlugin,
  DaemonPluginIssue,
  DaemonWorktree,
  useDaemonSocket,
} from './hooks/useDaemonSocket';
import { useReleaseUpdates } from './hooks/useReleaseUpdates';
import { useSessionStore } from './store/sessions';
import { useDaemonStore } from './store/daemonSessions';
import { hideBootSplash } from './utils/bootSplash';
import { bumpFsChangeSignal } from './utils/fsChangeSignals';

function App() {
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [settingError, setSettingError] = useState<string | null>(null);
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


  const [, setWorktrees] = useState<DaemonWorktree[]>([]);

  const { updateAvailableVersion, handleOpenLatestRelease, handleDismissLatestRelease } =
    useReleaseUpdates();

  const {
    daemonSessions,
    setDaemonSessions,
    setSeeds,
    applySeedChanges,
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
    onQuickCaptureRead: receipt => { if (isTauri()) void emitTo('capture', QUICK_CAPTURE_READ, receipt); },
    onSessionsUpdate: (sessions) => {
      useSessionStore.getState().syncFromDaemonSessions(sessions);
      setDaemonSessions(sessions);
    },
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
    onSeedsChange: applySeedChanges,
    onCrewUpdate: setCrew,
    onPRsUpdate: setPRs,
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
  });

  const captureHost = useQuickCaptureHost(daemon, settings);

  const {
    getMarkdownAnnotations,
    saveMarkdownAnnotations,
    clearMarkdownAnnotations,
    submitMarkdownAnnotations,
    sendSetSetting,
    sendNotificationList,
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

  if (daemon.migrationFailure) {
    return <MigrationFailureScreen failure={daemon.migrationFailure} />;
  }

  return (
    <SettingsProvider settings={settings} setSetting={sendSetSetting}>
      <KeybindingsProvider>
        <DaemonApiProvider api={daemon}>
          <QuickCaptureShortcutProvider value={captureHost}>
          <MigrationGate>
            <AppContent
              daemonSessions={daemonSessions}
              prs={prs}
              daemonPlugins={daemonPlugins}
              daemonPluginIssues={daemonPluginIssues}
              daemonGitHubHosts={daemonGitHubHosts}
              githubPollingOffReason={githubPollingOffReason}
              settings={settings}
              updateAvailableVersion={updateAvailableVersion}
              onOpenLatestRelease={handleOpenLatestRelease}
              onDismissLatestRelease={handleDismissLatestRelease}
              settingError={settingError}
              clearSettingError={() => setSettingError(null)}
              notificationsUnread={notificationsUnread}
              criticalNotifications={criticalNotifications}
              notificationsChangeSignal={notificationsChangeSignal}
              fsChangeSignals={fsChangeSignals}
              notebookTaskChangeSignal={notebookTaskChangeSignal}
            />
          </MigrationGate>
          <Toast />
          </QuickCaptureShortcutProvider>
        </DaemonApiProvider>
      </KeybindingsProvider>
    </SettingsProvider>
  );
}
export default App;
