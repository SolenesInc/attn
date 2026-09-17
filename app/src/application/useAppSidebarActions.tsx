import { invoke } from '@tauri-apps/api/core';
import { useCallback, useMemo } from 'react';
import {
  EditorIcon,
  NotebookIcon,
  PRsIcon,
  WorkflowIcon,
  type DockItem,
  type SidebarHeaderAction,
} from '../components/Sidebar';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { dockShortcutLabel } from '../shortcuts/metadata';
import type { ShortcutId } from '../shortcuts/registry';
import { useSessionStore } from '../store/sessions';
import {
  useAppAppearanceContext,
  useAppErrorsContext,
  useAppInputs,
  useAppPanelsContext,
  useAppSessionsContext,
  useAppShell,
  useNavigationContext,
} from './AppContexts';
import {
  AutomationsIcon,
  GardenIcon,
  NotificationsBellIcon,
  SessionsIcon,
  WorktreesIcon,
} from './AppIcons';
export function useAppSidebarActions() {
  const { settings, notificationsUnread } = useAppInputs();
  const { attentionCount, hasCriticalNotification, zoomModeBySessionId } = useAppShell();
  const { activeEndpoint, activeRemoteSession } = useAppSessionsContext();
  const { showError } = useAppErrorsContext();
  const {
    workflowRunPanelOpen,
    toggleDockPanel,
    attentionPanelOpen,
    notebookOpen,
    openNotebookBrowser,
    notificationsPanelOpen,
    toggleNotificationsPanel,
    automationsPanelOpen,
    sessionsOpen,
    ledgerTab,
    openLedger,
    gardenMode,
    toggleGardenFromIcon,
    gardenPanelOpen,
  } = useAppPanelsContext();
  const { keybindings } = useAppAppearanceContext();
  const sessions = useSessionStore((state) => state.sessions);
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const { activeWorkspaceId } = useNavigationContext();
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

  const handleOpenEditorForSession = useCallback(() => {
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

  const sidebarHeaderActions = useMemo<SidebarHeaderAction[]>(
    () => [
      {
        id: 'editor',
        title: !activeSessionId
          ? 'Open in Editor (No active session)'
          : activeRemoteSession
            ? remoteEditorAvailable
              ? 'Open in Zed Remote'
              : 'Open in Editor (Remote requires Zed)'
            : 'Open in Editor',
        icon: <EditorIcon />,
        disabled: !activeSessionId || (activeRemoteSession && !remoteEditorAvailable),
        onClick: handleOpenEditorForSession,
      },
      {
        id: 'workflowRun',
        title: activeSessionId ? 'Workflow Runs' : 'Workflow Runs (No active session)',
        icon: <WorkflowIcon />,
        active: workflowRunPanelOpen,
        disabled: !activeSessionId,
        onClick: () => toggleDockPanel('workflowRun'),
      },
      {
        id: 'attention',
        title: attentionPanelOpen ? 'Hide PRs Drawer' : 'Show PRs Drawer',
        icon: <PRsIcon />,
        active: attentionPanelOpen,
        badge: attentionCount > 0 ? attentionCount : undefined,
        onClick: () => toggleDockPanel('attention'),
      },
      {
        id: 'notebook',
        title: `Open Notebook (${formatShortcut('notebook.openFullscreen')})`,
        icon: <NotebookIcon />,
        active: notebookOpen,
        onClick: openNotebookBrowser,
      },
      {
        id: 'notifications',
        title: notificationsPanelOpen ? 'Hide Notifications' : 'Show Notifications',
        icon: <NotificationsBellIcon />,
        active: notificationsPanelOpen,
        badge: notificationsUnread > 0 ? notificationsUnread : undefined,
        toneClassName: hasCriticalNotification ? 'has-critical' : undefined,
        onClick: toggleNotificationsPanel,
      },
      {
        id: 'automations',
        title: automationsPanelOpen ? 'Hide Automations' : 'Show Automations',
        icon: <AutomationsIcon />,
        active: automationsPanelOpen,
        onClick: () => toggleDockPanel('automations'),
      },
      {
        id: 'sessions',
        title: `Open Sessions (${formatShortcut('sessions.open')})`,
        icon: <SessionsIcon />,
        active: sessionsOpen && ledgerTab === 'sessions',
        onClick: () => openLedger('sessions'),
      },
      {
        id: 'worktrees',
        title: 'Open Worktrees',
        icon: <WorktreesIcon />,
        active: sessionsOpen && ledgerTab === 'worktrees',
        onClick: () => openLedger('worktrees'),
      },
      {
        id: 'garden',
        title: gardenMode === 'closed' ? 'Show the garden' : 'Hide the garden',
        icon: <GardenIcon />,
        active: gardenMode !== 'closed',
        onClick: toggleGardenFromIcon,
      },
    ],
    [
      activeSessionId,
      activeRemoteSession,
      remoteEditorAvailable,
      attentionCount,
      attentionPanelOpen,
      handleOpenEditorForSession,
      workflowRunPanelOpen,
      toggleDockPanel,
      notebookOpen,
      openNotebookBrowser,
      gardenMode,
      toggleGardenFromIcon,
      notificationsPanelOpen,
      notificationsUnread,
      hasCriticalNotification,
      automationsPanelOpen,
      sessionsOpen,
      ledgerTab,
      openLedger,
      gardenPanelOpen,
      toggleNotificationsPanel,
    ],
  );

  const activeSessionZoomed = activeWorkspaceId
    ? Boolean(zoomModeBySessionId[activeWorkspaceId])
    : false;

  const dockActions = useMemo<
    Partial<
      Record<
        ShortcutId,
        {
          run?: () => void;
          isActive?: boolean;
          available?: boolean;
        }
      >
    >
  >(
    () => ({
      'dock.attention': {
        run: () => toggleDockPanel('attention'),
        isActive: attentionPanelOpen,
      },
      'terminal.splitVertical': { available: Boolean(activeSessionId) },
      'terminal.splitHorizontal': { available: Boolean(activeSessionId) },
      'session.newHorizontal': { available: Boolean(activeSessionId) },
      'terminal.toggleZoom': { isActive: activeSessionZoomed, available: Boolean(activeSessionId) },
    }),
    [activeSessionId, attentionPanelOpen, activeSessionZoomed, toggleDockPanel],
  );

  const dockItems = useMemo<DockItem[]>(
    () =>
      keybindings.dock.items.flatMap((id) => {
        const action = dockActions[id];
        if (action && action.available === false) return [];
        const keys = formatShortcut(id);
        if (!keys) return [];
        return [
          {
            id,
            label: dockShortcutLabel(id),
            keys,
            active: action?.isActive ?? false,
            onClick: action?.run,
          },
        ];
      }),
    [keybindings.dock.items, keybindings.config, dockActions],
  );

  return { sidebarHeaderActions, dockItems };
}
