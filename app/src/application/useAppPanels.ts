import { useCallback, useRef, useState } from 'react';
import { type DelegationChainHandle } from '../components/DelegationChain';
import { useDockSlotRect } from '../components/GardenFrame';
import { type SettingsModalHandle } from '../components/SettingsModal';
import type { LedgerTab } from '../components/ledger/LedgerSurface';
import { useDockPanels } from '../hooks/useDockPanels';
import { useGardenPresentation } from '../hooks/useGardenPresentation';
import { useWhatsNew } from '../hooks/useWhatsNew';

interface Options {
  agentSurfaceCount: number;
}
export function useAppPanels({ agentSurfaceCount }: Options) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const settingsModalRef = useRef<SettingsModalHandle>(null);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [shortcutEditorOpen, setShortcutEditorOpen] = useState(false);
  const [actionMenuOpen, setActionMenuOpen] = useState(false);
  const delegationChainRef = useRef<DelegationChainHandle>(null);
  const [seedPopoverRequest, setSeedPopoverRequest] = useState<{
    sessionId: string;
    nonce: number;
  }>();
  const [usagePopoverRequest, setUsagePopoverRequest] = useState<{
    sessionId: string;
    nonce: number;
  }>();
  const [sessionsOpen, setSessionsOpen] = useState(false);
  const [ledgerTab, setLedgerTab] = useState<LedgerTab>('sessions');
  const openLedger = useCallback((tab: LedgerTab) => {
    setLedgerTab(tab);
    setSessionsOpen(true);
  }, []);
  const [notebookOpen, setNotebookOpen] = useState(false);
  const [notebookRequestedPath, setNotebookRequestedPath] = useState<string | null>(null);
  const [notificationsPanelOpen, setNotificationsPanelOpen] = useState(false);
  const whatsNew = useWhatsNew();

  const { dockState, toggleDockPanel, openDockPanel, closeDockPanel } = useDockPanels();

  const empty = agentSurfaceCount === 0;
  const [sidebarState, setSidebarState] = useState({ empty, collapsed: empty });
  if (sidebarState.empty !== empty) setSidebarState({ empty, collapsed: empty });
  const sidebarCollapsed = sidebarState.collapsed;
  const setSidebarCollapsed = useCallback((collapsed: boolean) => {
    setSidebarState((state) => ({ ...state, collapsed }));
  }, []);
  const toggleSidebarCollapse = useCallback(() => {
    delegationChainRef.current?.dismiss('sidebar-collapse');
    setSidebarState((state) => ({ ...state, collapsed: !state.collapsed }));
  }, []);

  const openDockPanels = dockState.openPanels;
  const dockPanelStack = dockState.stack;
  const workflowRunPanelOpen = openDockPanels.workflowRun;
  const attentionPanelOpen = openDockPanels.attention;
  const automationsPanelOpen = openDockPanels.automations;
  const gardenPanelOpen = openDockPanels.garden;
  const openGardenDock = useCallback(() => openDockPanel('garden'), [openDockPanel]);
  const closeGardenDock = useCallback(() => closeDockPanel('garden'), [closeDockPanel]);
  const {
    mode: gardenMode,
    holdsWindow: gardenHoldsWindow,
    toggleFrame: toggleGardenFrame,
    toggleFromIcon: toggleGardenFromIcon,
    close: closeGarden,
  } = useGardenPresentation({
    dockOpen: gardenPanelOpen,
    openDock: openGardenDock,
    closeDock: closeGardenDock,
  });
  const [gardenSlotRef, gardenDockRect] = useDockSlotRect();
  const openNotebookBrowser = useCallback(() => {
    setNotebookOpen(true);
  }, []);

  const toggleNotificationsPanel = useCallback(() => {
    setNotificationsPanelOpen((open) => !open);
  }, []);
  const openNotificationsPanel = useCallback(() => {
    setNotificationsPanelOpen(true);
  }, []);
  const closeNotificationsPanel = useCallback(() => {
    setNotificationsPanelOpen(false);
  }, []);

  return {
    settingsOpen,
    setSettingsOpen,
    settingsModalRef,
    shortcutsOpen,
    setShortcutsOpen,
    shortcutEditorOpen,
    setShortcutEditorOpen,
    actionMenuOpen,
    setActionMenuOpen,
    delegationChainRef,
    seedPopoverRequest,
    setSeedPopoverRequest,
    usagePopoverRequest,
    setUsagePopoverRequest,
    sessionsOpen,
    setSessionsOpen,
    ledgerTab,
    setLedgerTab,
    openLedger,
    notebookOpen,
    setNotebookOpen,
    notebookRequestedPath,
    setNotebookRequestedPath,
    notificationsPanelOpen,
    toggleNotificationsPanel,
    openNotificationsPanel,
    closeNotificationsPanel,
    whatsNew,
    toggleDockPanel,
    openDockPanel,
    closeDockPanel,
    sidebarCollapsed,
    setSidebarCollapsed,
    toggleSidebarCollapse,
    dockPanelStack,
    workflowRunPanelOpen,
    attentionPanelOpen,
    automationsPanelOpen,
    gardenPanelOpen,
    gardenMode,
    gardenHoldsWindow,
    toggleGardenFrame,
    toggleGardenFromIcon,
    closeGarden,
    gardenSlotRef,
    gardenDockRect,
    openNotebookBrowser,
  };
}
