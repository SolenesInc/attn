import { useMemo } from 'react';
import {
  NotebookIcon,
  type DockItem,
  type SidebarHeaderAction,
} from '../components/Sidebar';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { dockShortcutLabel } from '../shortcuts/metadata';
import type { ShortcutId } from '../shortcuts/registry';
import {
  useAppAppearanceContext,
  useAppInputs,
  useAppPanelsContext,
  useAppShell,
  useNavigationContext,
} from './AppContexts';
import {
  AutomationsIcon,
  GardenIcon,
  NotificationsBellIcon,
  SessionsIcon,
} from './AppIcons';
import { useAgentOnScreen } from '../hooks/useDesktopSelectionBridge';
export function useAppSidebarActions() {
  const { notificationsUnread } = useAppInputs();
  const { hasCriticalNotification, zoomModeBySessionId } = useAppShell();
  const {
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
  } = useAppPanelsContext();
  const { keybindings } = useAppAppearanceContext();
  const shownAgentId = useAgentOnScreen();
  const { currentDesktopId } = useNavigationContext();

  const sidebarHeaderActions = useMemo<SidebarHeaderAction[]>(
    () => [
      {
        id: 'garden',
        title: gardenMode === 'closed' ? 'Show the garden' : 'Hide the garden',
        icon: <GardenIcon />,
        active: gardenMode !== 'closed',
        onClick: toggleGardenFromIcon,
      },
      {
        id: 'notebook',
        title: `Open Notebook (${formatShortcut('notebook.openFullscreen')})`,
        icon: <NotebookIcon />,
        active: notebookOpen,
        onClick: openNotebookBrowser,
      },
      {
        id: 'ledger',
        title: `Open Ledger (${formatShortcut('sessions.open')})`,
        icon: <SessionsIcon />,
        active: sessionsOpen,
        onClick: () => openLedger(ledgerTab),
      },
      {
        id: 'automations',
        title: automationsPanelOpen ? 'Hide Automations' : 'Show Automations',
        icon: <AutomationsIcon />,
        active: automationsPanelOpen,
        onClick: () => toggleDockPanel('automations'),
      },
      {
        id: 'notifications',
        title: notificationsPanelOpen ? 'Hide Notifications' : 'Show Notifications',
        icon: <NotificationsBellIcon />,
        active: notificationsPanelOpen,
        unread: notificationsUnread > 0,
        toneClassName: hasCriticalNotification ? 'has-critical' : undefined,
        onClick: toggleNotificationsPanel,
      },
    ],
    [
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
      toggleNotificationsPanel,
    ],
  );

  const activeSessionZoomed = currentDesktopId
    ? Boolean(zoomModeBySessionId[currentDesktopId])
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
      'terminal.splitVertical': { available: Boolean(shownAgentId) },
      'terminal.splitHorizontal': { available: Boolean(shownAgentId) },
      'session.newHorizontal': { available: Boolean(shownAgentId) },
      'terminal.toggleZoom': { isActive: activeSessionZoomed, available: Boolean(shownAgentId) },
    }),
    [shownAgentId, attentionPanelOpen, activeSessionZoomed, toggleDockPanel],
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
