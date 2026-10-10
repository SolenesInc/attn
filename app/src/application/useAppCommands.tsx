import { useMemo } from 'react';
import { useToast } from '../components/Toast';
import type { PaletteCommand } from '../components/palette/paletteCommands';
import { EditorIcon, NotebookIcon } from '../components/Sidebar';
import { SessionRoleIcon } from '../components/DelegationChain';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { shortcutTokens } from '../shortcuts/formatShortcut';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { desktopLabel, orderedDesktops, sortedDesktopOrder } from '../utils/desktops';
import type { ShortcutId } from '../shortcuts/registry';
import {
  AUTO_SETTLE_ENABLED_SETTING,
  isAutoSettleEnabled,
  isCrewQueueEnabled,
  isQueueModeEnabled,
} from '../utils/queueBands';
import { areSidebarHarnessLogosEnabled } from '../utils/sidebarHarnessLogos';
import {
  useAppAppearanceContext,
  useAppDiagnosticsContext,
  useAppInputs,
  useAppPanelsContext,
  useAppShell,
  useAttentionQueueContext,
  useCrewPanelContext,
  useDesktopNavigationContext,
  useDesktopTilesContext,
  useNavigationContext,
  useSessionLaunchContext,
  useSessionLifecycleContext,
} from './AppContexts';
import {
  AttentionActionIcon,
  AutomationsIcon,
  BoardActionIcon,
  ContextActionIcon,
  GardenIcon,
  KeyboardActionIcon,
  NotificationsBellIcon,
} from './AppIcons';
import { useAgentOnScreen } from '../hooks/useDesktopSelectionBridge';
import { useOpenInEditor } from './useOpenInEditor';

export function useAppCommands(): PaletteCommand[] {
  const seeds = useDaemonStore((state) => state.seeds);
  const { setMarkdownOpenerOpen, handleOpenNotebookTile } =
    useDesktopTilesContext();
  const { setContextCapPromptSession } = useAppShell();
  const {
    openLedger,
    openDockPanel,
    gardenMode,
    toggleGardenFrame,
    setShortcutEditorOpen,
    delegationChainRef,
    setSeedPopoverRequest,
    setUsagePopoverRequest,
  } = useAppPanelsContext();
  const { settings } = useAppInputs();
  const { handleToggleSidebarHarnessLogos } = useAppAppearanceContext();
  const { handleOpenCrew } = useCrewPanelContext();
  const {
    handleToggleCrewQueue,
    handleToggleQueueMode,
    activeGroupForCommands,
    activeSessionForCommands,
    handleSnoozeActiveSession,
    handleWakeActiveSession,
  } = useAttentionQueueContext();
  const { sendSetSetting, sendDesktopSetOrder } = useDaemonApi();
  const { showAction, showError } = useToast();
  const { handleCreateDiagnosticReport } = useAppDiagnosticsContext();
  const agentOnScreenId = useAgentOnScreen();
  const { handleReloadSession, handleRequestCloseDesktop } = useSessionLifecycleContext();
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const desktops = useProfilesStore((state) => state.desktops);
  const profiles = useProfilesStore((state) => state.profiles);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const {
    goToDashboard,
    handleSelectDesktop,
    handleJumpToWaiting,
    handleNextRun,
    desktopSelectionStyle,
    handleDesktopSelectionStyleChange,
  } = useNavigationContext();
  const { desktopNavigation, setDesktopOverviewOpen, setProfileSwitcherOpen } = useDesktopNavigationContext();
  const { handleNewSession } = useSessionLaunchContext();
  const { handleSettleActiveTurn } = useAttentionQueueContext();
  const {
    sidebarCollapsed,
    toggleSidebarCollapse,
    setShortcutsOpen,
    setSettingsOpen,
    toggleDockPanel,
    automationsPanelOpen,
    notificationsPanelOpen,
    toggleNotificationsPanel,
    openNotebookBrowser,
    whatsNew,
  } = useAppPanelsContext();
  const { openActiveSessionInEditor, editorUnavailableReason } = useOpenInEditor();
  const navigationCommands = useMemo<PaletteCommand[]>(() => {
    const keys = (id: ShortcutId) => [shortcutTokens(id)];
    const commands: PaletteCommand[] = [
      {
        id: 'new-agent',
        title: 'New agent',
        description: 'Start an agent beside the active pane',
        keywords: ['session', 'spawn', 'launch', 'create'],
        icon: <ContextActionIcon />,
        shortcut: keys('session.new'),
        run: () => handleNewSession('vertical'),
      },
      {
        id: 'jump-to-waiting',
        title: 'Jump to the oldest turn',
        description: 'Open the agent that has waited longest for you',
        keywords: ['queue', 'waiting', 'turn', 'next'],
        icon: <AttentionActionIcon />,
        shortcut: keys('session.jumpToWaiting'),
        run: handleJumpToWaiting,
      },
      {
        id: 'next-run-needing-you',
        title: 'Next run needing you',
        description: 'Open the next automation run that stopped with a question',
        keywords: ['automation', 'run', 'batch', 'waiting', 'next'],
        icon: <AttentionActionIcon />,
        shortcut: keys('session.nextRun'),
        run: handleNextRun,
      },
      ...(handleSettleActiveTurn
        ? [{
            id: 'settle-active-session',
            pinned: true,
            title: 'Settle this agent',
            description: 'Close the turn it owes you',
            keywords: ['settle', 'done', 'queue', 'turn'],
            icon: <AttentionActionIcon />,
            shortcut: keys('session.settle'),
            run: handleSettleActiveTurn,
          }]
        : []),
      ...(agentOnScreenId
        ? [{
            id: 'reload-active-session',
            pinned: true,
            title: 'Reload this agent',
            description: 'Restart the agent in its current pane',
            keywords: ['session', 'reload', 'restart', 'relaunch'],
            icon: <ContextActionIcon />,
            run: () => handleReloadSession(agentOnScreenId),
          }]
        : []),
      {
        id: 'home',
        title: 'Home',
        keywords: ['dashboard', 'overview'],
        icon: <ContextActionIcon />,
        shortcut: keys('session.goToDashboard'),
        run: goToDashboard,
      },
      {
        id: 'desktop-overview',
        title: 'Desktop overview',
        description: 'Every desktop, with shortcuts and sending panes',
        keywords: ['desktops', 'workspaces'],
        icon: <BoardActionIcon />,
        shortcut: keys('desktop.overview'),
        run: () => setDesktopOverviewOpen(true),
      },
      {
        id: 'close-desktop',
        title: 'Close desktop',
        keywords: ['desktop', 'close'],
        icon: <BoardActionIcon />,
        run: () => { if (currentDesktopId) handleRequestCloseDesktop(currentDesktopId); },
      },
      {
        id: 'new-desktop',
        title: 'New desktop',
        keywords: ['desktop', 'create', 'workspace'],
        icon: <BoardActionIcon />,
        run: desktopNavigation.createDesktop,
      },
      ...(selectedProfileId ? [{
        id: 'sort-desktops',
        title: 'Sort desktops',
        description: 'Numbered desktops first, then names A–Z',
        keywords: ['desktop', 'order', 'organize'],
        icon: <BoardActionIcon />,
        run: async () => {
          const previous = orderedDesktops(desktops).map((desktop) => desktop.id);
          try {
            await sendDesktopSetOrder(selectedProfileId, sortedDesktopOrder(desktops));
            showAction('Desktops sorted', 'Undo', async () => { await sendDesktopSetOrder(selectedProfileId, previous); });
          } catch (reason) {
            showError(reason instanceof Error ? reason.message : String(reason));
          }
        },
      }] : []),
      ...orderedDesktops(desktops).map((desktop) => ({
        id: `desktop-${desktop.id}`,
        title: `Go to ${desktopLabel(desktop, desktops)}`,
        keywords: ['desktop', 'switch'],
        icon: <BoardActionIcon />,
        shortcut: desktop.shortcut_slot ? keys(`desktop.select${desktop.shortcut_slot}` as ShortcutId) : undefined,
        run: () => handleSelectDesktop(desktop.id),
      })),
      ...(desktopNavigation.canMoveWithDelegates
        ? orderedDesktops(desktops)
          .filter((desktop) => desktop.id !== desktopNavigation.currentDesktop?.id)
          .map((desktop) => ({
            id: `move-with-delegates-${desktop.id}`,
            title: `Move with delegates to ${desktopLabel(desktop, desktops)}`,
            keywords: ['desktop', 'move', 'send', 'delegates'],
            icon: <BoardActionIcon />,
            run: () => desktopNavigation.moveActiveLeafToDesktop(desktop.id, false, true),
          }))
        : []),
      {
        id: 'switch-profile',
        title: 'Switch profile',
        keywords: ['profile', 'setup'],
        icon: <ContextActionIcon />,
        shortcut: keys('profile.switch'),
        run: () => setProfileSwitcherOpen(true),
      },
      ...profiles
        .filter((profile) => profile.id !== selectedProfileId)
        .map((profile) => ({
          id: `profile-${profile.id}`,
          title: `Switch to ${profile.name}`,
          keywords: ['profile', 'setup'],
          icon: <ContextActionIcon />,
          run: () => desktopNavigation.selectProfile(profile.id),
        })),
      {
        id: 'toggle-sidebar',
        title: sidebarCollapsed ? 'Show the sidebar' : 'Hide the sidebar',
        keywords: ['sidebar', 'collapse', 'expand', 'rail'],
        icon: <ContextActionIcon />,
        shortcut: keys('session.toggleSidebar'),
        run: toggleSidebarCollapse,
      },
      ...(editorUnavailableReason === null
        ? [{
            id: 'open-in-editor',
            pinned: true,
            title: 'Open in editor',
            description: 'The active agent\u2019s folder in your editor',
            keywords: ['editor', 'zed', 'code', 'folder'],
            icon: <EditorIcon />,
            run: openActiveSessionInEditor,
          }]
        : []),
      {
        id: 'notebook',
        title: 'Open the notebook',
        keywords: ['notebook', 'journal', 'knowledge', 'fullscreen'],
        icon: <NotebookIcon />,
        shortcut: keys('notebook.openFullscreen'),
        run: openNotebookBrowser,
      },
      {
        id: 'notifications',
        title: notificationsPanelOpen ? 'Hide notifications' : 'Show notifications',
        keywords: ['notifications', 'alerts', 'bell'],
        icon: <NotificationsBellIcon />,
        run: toggleNotificationsPanel,
      },
      {
        id: 'automations',
        title: automationsPanelOpen ? 'Hide automations' : 'Show automations',
        keywords: ['automations', 'schedule', 'runs', 'panel'],
        icon: <AutomationsIcon />,
        run: () => toggleDockPanel('automations'),
      },
      {
        id: 'keyboard-shortcuts',
        title: 'Keyboard shortcuts',
        keywords: ['shortcuts', 'keys', 'cheatsheet', 'help'],
        icon: <KeyboardActionIcon />,
        shortcut: keys('ui.showShortcuts'),
        run: () => setShortcutsOpen(true),
      },
      {
        id: 'whats-new',
        title: "What's new",
        description: 'Replay the intro to profiles, desktops, the queue and the palettes',
        keywords: ['whats new', 'intro', 'tour', 'changes', 'release', 'help'],
        icon: <KeyboardActionIcon />,
        run: whatsNew.open,
      },
      {
        id: 'settings',
        title: 'Settings',
        keywords: ['settings', 'preferences'],
        icon: <KeyboardActionIcon />,
        shortcut: keys('ui.openSettings'),
        run: () => setSettingsOpen(true),
      },
    ];
    return commands;
  }, [
    agentOnScreenId,
    automationsPanelOpen,
    desktopNavigation,
    desktops,
    sendDesktopSetOrder,
    showAction,
    showError,
    editorUnavailableReason,
    goToDashboard,
    handleJumpToWaiting,
    handleNextRun,
    handleNewSession,
    handleReloadSession,
    handleRequestCloseDesktop,
    currentDesktopId,
    handleSelectDesktop,
    handleSettleActiveTurn,
    notificationsPanelOpen,
    openActiveSessionInEditor,
    openNotebookBrowser,
    profiles,
    selectedProfileId,
    setDesktopOverviewOpen,
    setProfileSwitcherOpen,
    setSettingsOpen,
    setShortcutsOpen,
    sidebarCollapsed,
    toggleDockPanel,
    toggleNotificationsPanel,
    toggleSidebarCollapse,
    whatsNew.open,
  ]);
  const actionMenuItems = useMemo<PaletteCommand[]>(
    () => [
      {
        id: 'open-markdown-file',
        title: 'Open a markdown file',
        description: 'Recently opened documents, then a fuzzy search of this session\u2019s folder',
        keywords: ['open', 'file', 'markdown', 'md', 'recent', 'doc', 'find'],
        icon: <ContextActionIcon />,
        shortcut: [shortcutTokens('file.open')],
        run: () => setMarkdownOpenerOpen(true),
      },
      {
        id: 'notebook-tile',
        title: 'Open Editor tile',
        description: 'Dock an editor beside your terminals, opened on any folder',
        keywords: ['notebook', 'editor', 'tile', 'knowledge', 'journal', 'dock', 'split'],
        icon: <ContextActionIcon />,
        shortcut: [shortcutTokens('notebook.openTile')],
        run: handleOpenNotebookTile,
      },
      {
        id: 'sessions',
        title: 'Open the sessions list',
        description: 'Every session this machine ran, live and closed, with reopen',
        keywords: ['sessions', 'ledger', 'closed', 'history', 'reopen', 'past'],
        icon: <ContextActionIcon />,
        shortcut: [shortcutTokens('sessions.open')],
        run: () => openLedger('sessions'),
      },
      {
        id: 'attention',
        title: 'Open attention drawer',
        description: 'Show sessions and pull requests that need a response',
        keywords: ['waiting', 'pull requests', 'prs', 'notifications'],
        icon: <AttentionActionIcon />,
        shortcut: [shortcutTokens('dock.attention')],
        run: () => openDockPanel('attention'),
      },
      {
        id: 'worktrees',
        title: 'Open worktrees',
        description: 'Every worktree, what the sweep decided, and why it kept the rest',
        keywords: ['worktree', 'worktrees', 'sweep', 'branch', 'reclaim', 'pin', 'keep'],
        icon: <ContextActionIcon />,
        run: () => openLedger('worktrees'),
      },
      {
        id: 'garden-frame',
        title:
          gardenMode === 'full'
            ? 'Move the garden to the sidebar'
            : gardenMode === 'dock'
              ? 'Open the garden fullscreen'
              : 'Open the garden',
        description: 'Seeds and plots, the trail beside what you are reading',
        keywords: ['garden', 'seed', 'seeds', 'plot', 'board', 'expand', 'fullscreen'],
        icon: <BoardActionIcon />,
        shortcut: [shortcutTokens('board.open')],
        run: () => toggleGardenFrame(),
      },
      {
        id: 'toggle-queue-mode',
        title: isQueueModeEnabled(settings)
          ? 'Turn off the agent queue'
          : 'Turn on the agent queue',
        description: 'Swap the desktop tree for the queue sidebar: the turns you owe, your crew and your runs',
        keywords: ['queue', 'turn', 'settle', 'attention', 'sidebar'],
        icon: <AttentionActionIcon />,
        run: handleToggleQueueMode,
      },
      {
        id: 'toggle-crew-queue',
        title: isCrewQueueEnabled(settings) ? 'Take the crew out of the queue' : 'Put the crew in the queue',
        description: 'Whether a crew member owing a turn also waits in the queue',
        keywords: ['crew', 'queue', 'turn', 'member', 'sidebar'],
        icon: <AttentionActionIcon />,
        run: handleToggleCrewQueue,
      },
      {
        id: 'manage-crew',
        title: 'Manage crew',
        description: 'Wake, charter and hand off to crew members',
        keywords: ['crew', 'member', 'charter', 'wake', 'sleep'],
        icon: <ContextActionIcon />,
        run: (opener) => handleOpenCrew(undefined, opener ?? undefined),
      },
      {
        id: 'toggle-harness-logos',
        title: areSidebarHarnessLogosEnabled(settings)
          ? 'Hide harness logos in the sidebar'
          : 'Show harness logos in the sidebar',
        description: 'The Claude, Codex, Copilot or Pi mark beside each agent',
        keywords: ['harness', 'logo', 'icon', 'sidebar', 'claude', 'codex'],
        icon: <ContextActionIcon />,
        run: handleToggleSidebarHarnessLogos,
      },
      ...(['dim', 'rail', 'spotlight'] as const).map((style) => ({
        id: `tile-focus-${style}`,
        title: `Tile focus: ${style}`,
        description: desktopSelectionStyle === style ? 'Current tile focus style' : `Use ${style} to mark the selected tile`,
        keywords: ['tile', 'focus', 'style', 'selection', 'dim', 'rail', 'spotlight'],
        icon: <ContextActionIcon />,
        run: () => handleDesktopSelectionStyleChange(style),
      })),
      {
        id: 'toggle-auto-settle',
        title: isAutoSettleEnabled(settings) ? 'Turn off auto-settle' : 'Turn on auto-settle',
        description: 'Settle a turn once you have steered the agent and it goes back to work',
        keywords: ['auto', 'settle', 'turn', 'countdown', 'queue', 'attention'],
        icon: <AttentionActionIcon />,
        run: () =>
          sendSetSetting(
            AUTO_SETTLE_ENABLED_SETTING,
            isAutoSettleEnabled(settings) ? 'false' : 'true',
          ),
      },
      {
        id: 'customize-shortcuts',
        title: 'Customize keyboard shortcuts',
        description: 'Rebind shortcuts and restore defaults',
        keywords: ['keybindings', 'shortcuts', 'keyboard', 'rebind', 'hotkeys'],
        icon: <KeyboardActionIcon />,
        run: () => setShortcutEditorOpen(true),
      },
      {
        id: 'create-diagnostic-report',
        title: 'Create diagnostic report',
        description: 'Save a private troubleshooting report you can share',
        keywords: [
          'debug',
          'report',
          'logs',
          'dump',
          'keyboard',
          'typing',
          'stuck',
          'frozen',
          'error',
        ],
        icon: <KeyboardActionIcon />,
        run: handleCreateDiagnosticReport,
      },
    ],
    [
      setMarkdownOpenerOpen,
      openLedger,
      setShortcutEditorOpen,
      openDockPanel,
      handleOpenNotebookTile,
      toggleGardenFrame,
      gardenMode,
      settings,
      handleToggleQueueMode,
      handleToggleCrewQueue,
      handleOpenCrew,
      handleToggleSidebarHarnessLogos,
      desktopSelectionStyle,
      handleDesktopSelectionStyleChange,
      sendSetSetting,
      handleCreateDiagnosticReport,
    ],
  );

  const actionMenuItemsWithDesktopActions = useMemo<PaletteCommand[]>(() => {
    const group = activeGroupForCommands;
    if (!group) return actionMenuItems;
    const activeSession = activeSessionForCommands;
    const delegationItems: PaletteCommand[] = activeSession
      ? [
          {
            id: 'show-delegation-chain',
            pinned: true,
            title: 'Show delegation chain',
            description: 'Navigate this agent’s dispatcher, peers, and delegates',
            keywords: ['role', 'orchestrator', 'builder', 'parent', 'children', 'agent', 'session'],
            icon: <SessionRoleIcon role={activeSession.delegation_role} />,
            run: () =>
              delegationChainRef.current?.open(activeSession.id),
          },
        ]
      : [];
    const sessionSeedItems: PaletteCommand[] =
      activeSession &&
      (activeSession.seedId || seeds.some((seed) => seed.tender_session === activeSession.id))
        ? [
            {
              id: 'show-tended-seeds',
              pinned: true,
              title: `Show ${activeSession.label}'s seeds`,
              description: 'The seeds this agent is tending, and what it reports to',
              keywords: ['seed', 'seeds', 'tend', 'tending', 'garden', 'plot', 'agent', 'session'],
              icon: <GardenIcon />,
              run: () =>
                setSeedPopoverRequest((prev) => ({
                  sessionId: activeSession.id,
                  nonce: (prev?.nonce ?? 0) + 1,
                })),
            },
          ]
        : [];
    const sessionUsageItems: PaletteCommand[] =
      activeSession?.usage &&
      !activeSession.usage.measurement_incomplete &&
      activeSession.usage.total_tokens > 0
        ? [
            {
              id: 'show-session-usage',
              pinned: true,
              title: `Show ${activeSession.label}'s usage`,
              description: 'Token and cost totals for each model in this session',
              keywords: ['usage', 'tokens', 'cost', 'models', 'agent', 'session'],
              icon: <ContextActionIcon />,
              run: () =>
                setUsagePopoverRequest((prev) => ({
                  sessionId: activeSession.id,
                  nonce: (prev?.nonce ?? 0) + 1,
                })),
            },
          ]
        : [];
    const sessionCapItems: PaletteCommand[] =
      activeSession && ['claude', 'codex'].includes((activeSession.agent ?? '').toLowerCase())
        ? [
            {
              id: 'set-session-context-cap',
              pinned: true,
              title: activeSession.contextWindowCap
                ? `Change ${activeSession.label}'s context window cap`
                : `Cap ${activeSession.label}'s context window`,
              description: activeSession.contextWindowCap
                ? `Compacts at ${activeSession.contextWindowCap.toLocaleString()} tokens — change or clear the cap`
                : 'Make this agent compact at a token budget you choose',
              keywords: [
                'context',
                'window',
                'cap',
                'compact',
                'autocompact',
                'tokens',
                'agent',
                'session',
              ],
              icon: <ContextActionIcon />,
              run: () =>
                setContextCapPromptSession({
                  id: activeSession.id,
                  label: activeSession.label,
                  currentCap: activeSession.contextWindowCap,
                }),
            },
          ]
        : [];
    return [
      ...actionMenuItems,
      ...delegationItems,
      ...sessionSeedItems,
      ...sessionUsageItems,
      ...sessionCapItems,
    ];
  }, [
    delegationChainRef,
    setSeedPopoverRequest,
    setUsagePopoverRequest,
    setContextCapPromptSession,
    actionMenuItems,
    activeGroupForCommands,
    activeSessionForCommands,
    seeds,
  ]);

  const actionMenuItemsWithQueueActions = useMemo<PaletteCommand[]>(() => {
    if (handleWakeActiveSession) {
      return [...actionMenuItemsWithDesktopActions, {
        id: 'wake-active-session',
        pinned: true,
        title: 'Wake this agent now',
        description: 'End the snooze and let it back into the queue',
        keywords: ['wake', 'snooze', 'defer', 'queue', 'turn'],
        icon: <AttentionActionIcon />,
        run: handleWakeActiveSession,
      }];
    }
    if (handleSnoozeActiveSession) {
      return [...actionMenuItemsWithDesktopActions, {
        id: 'snooze-active-session',
        pinned: true,
        title: 'Snooze this agent…',
        description: 'Take it off your plate until a time you choose',
        keywords: ['snooze', 'defer', 'later', 'queue', 'turn'],
        icon: <AttentionActionIcon />,
        shortcut: [shortcutTokens('session.snooze')],
        run: (opener) => handleSnoozeActiveSession(opener),
      }];
    }
    return actionMenuItemsWithDesktopActions;
  }, [actionMenuItemsWithDesktopActions, handleWakeActiveSession, handleSnoozeActiveSession]);

  return useMemo(
    () => [...navigationCommands, ...actionMenuItemsWithQueueActions],
    [navigationCommands, actionMenuItemsWithQueueActions],
  );
}
