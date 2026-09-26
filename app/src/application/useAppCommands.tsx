import { useMemo } from 'react';
import type { PaletteCommand } from '../components/palette/paletteCommands';
import { EditorIcon, NotebookIcon, WorkflowIcon } from '../components/Sidebar';
import { SessionRoleIcon } from '../components/DelegationChain';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { shortcutTokens } from '../shortcuts/formatShortcut';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { desktopLabel, orderedDesktops } from '../utils/desktops';
import type { ShortcutId } from '../shortcuts/registry';
import { useSessionStore } from '../store/sessions';
import {
  AUTO_SETTLE_ENABLED_SETTING,
  isAutoSettleEnabled,
  isQueueModeEnabled,
} from '../utils/queueBands';
import {
  useAppDiagnosticsContext,
  useAppInputs,
  useAppPanelsContext,
  useAppShell,
  useAttentionQueueContext,
  useDesktopNavigationContext,
  useDesktopTilesContext,
  useNavigationContext,
  useSessionLaunchContext,
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
import { useOpenInEditor } from './useOpenInEditor';

export function useAppCommands(): PaletteCommand[] {
  const apps = useDaemonStore((state) => state.apps);
  const seeds = useDaemonStore((state) => state.seeds);
  const { setAppViewParamsPrompt, dockAppViewTile, setMarkdownOpenerOpen, handleOpenNotebookTile } =
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
  const {
    handleToggleQueueMode,
    activeGroupForCommands,
    activeSessionForCommands,
    activeSessionQueueEligible,
    queueModeEnabled,
    handleSnoozeActiveSession,
  } = useAttentionQueueContext();
  const { sendSetSetting, sendWakeTurn } =
    useDaemonApi();
  const { handleCreateDiagnosticReport } = useAppDiagnosticsContext();
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const desktops = useProfilesStore((state) => state.desktops);
  const profiles = useProfilesStore((state) => state.profiles);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const { goToDashboard, handleSelectDesktop, handleJumpToWaiting, handleNextRun } = useNavigationContext();
  const { desktopNavigation, setDesktopOverviewOpen, setProfileSwitcherOpen } = useDesktopNavigationContext();
  const { handleNewSession } = useSessionLaunchContext();
  const { handleSettleActiveTurn } = useAttentionQueueContext();
  const {
    sidebarCollapsed,
    toggleSidebarCollapse,
    setShortcutsOpen,
    setSettingsOpen,
    toggleDockPanel,
    workflowRunPanelOpen,
    automationsPanelOpen,
    notificationsPanelOpen,
    toggleNotificationsPanel,
    openNotebookBrowser,
  } = useAppPanelsContext();
  const { openActiveSessionInEditor } = useOpenInEditor();
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
            title: 'Settle this agent',
            description: 'Close the turn it owes you',
            keywords: ['settle', 'done', 'queue', 'turn'],
            icon: <AttentionActionIcon />,
            shortcut: keys('session.settle'),
            run: handleSettleActiveTurn,
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
        id: 'new-desktop',
        title: 'New desktop',
        keywords: ['desktop', 'create', 'workspace'],
        icon: <BoardActionIcon />,
        run: desktopNavigation.createDesktop,
      },
      ...orderedDesktops(desktops).map((desktop) => ({
        id: `desktop-${desktop.id}`,
        title: `Go to ${desktopLabel(desktop, desktops)}`,
        keywords: ['desktop', 'switch'],
        icon: <BoardActionIcon />,
        shortcut: desktop.shortcut_slot ? keys(`desktop.select${desktop.shortcut_slot}` as ShortcutId) : undefined,
        run: () => handleSelectDesktop(desktop.id),
      })),
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
      {
        id: 'open-in-editor',
        title: 'Open in editor',
        description: 'The active agent\u2019s folder in your editor',
        keywords: ['editor', 'zed', 'code', 'folder'],
        icon: <EditorIcon />,
        run: openActiveSessionInEditor,
      },
      {
        id: 'workflow-runs',
        title: workflowRunPanelOpen ? 'Hide workflow runs' : 'Show workflow runs',
        keywords: ['workflow', 'runs', 'agents', 'panel'],
        icon: <WorkflowIcon />,
        run: () => toggleDockPanel('workflowRun'),
      },
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
    automationsPanelOpen,
    desktopNavigation,
    desktops,
    goToDashboard,
    handleJumpToWaiting,
    handleNextRun,
    handleNewSession,
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
    workflowRunPanelOpen,
  ]);
  const appViewMenuItems = useMemo<PaletteCommand[]>(() => {
    const items: PaletteCommand[] = [];
    for (const app of apps ?? []) {
      if (!app.enabled) continue;
      for (const view of app.views ?? []) {
        items.push({
          id: `app-view-${app.name}-${view.name}`,
          title: `${view.title} — ${app.name}`,
          description: app.description || `Dock ${app.name}'s ${view.name} view`,
          keywords: ['app', 'view', 'dock', 'tile', app.name, view.name],
          icon: <ContextActionIcon />,
          run: () => {
            if (view.params_label) {
              setAppViewParamsPrompt({
                app: app.name,
                view: view.name,
                viewTitle: `${app.name}/${view.name}`,
                label: view.params_label,
                ...(view.params_placeholder ? { placeholder: view.params_placeholder } : {}),
              });
              return;
            }
            dockAppViewTile(app.name, view.name, '');
          },
        });
      }
    }
    return items;
  }, [apps, dockAppViewTile, setAppViewParamsPrompt]);

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
        description: 'Show the turns you owe above the workspace tree',
        keywords: ['queue', 'turn', 'settle', 'attention', 'sidebar'],
        icon: <AttentionActionIcon />,
        run: handleToggleQueueMode,
      },
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
      sendSetSetting,
      handleCreateDiagnosticReport,
    ],
  );

  const actionMenuItemsWithWorkspaceActions = useMemo<PaletteCommand[]>(() => {
    const workspace = activeGroupForCommands;
    if (!workspace) return [...actionMenuItems, ...appViewMenuItems];
    const activeSession = activeSessionForCommands;
    const delegationItems: PaletteCommand[] = activeSession
      ? [
          {
            id: 'show-delegation-chain',
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
      ...appViewMenuItems,
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
    appViewMenuItems,
    activeGroupForCommands,
    activeSessionForCommands,
    activeSessionQueueEligible,
    seeds,
  ]);

  const activeSessionSnoozedUntil = activeSessionQueueEligible
    ? activeSessionForCommands?.turnSnoozedUntil
    : undefined;
  const actionMenuItemsWithQueueActions = useMemo<PaletteCommand[]>(() => {
    if (!queueModeEnabled || !activeSessionId || !activeSessionQueueEligible) {
      return actionMenuItemsWithWorkspaceActions;
    }
    const items = [...actionMenuItemsWithWorkspaceActions];
    if (activeSessionSnoozedUntil) {
      items.push({
        id: 'wake-active-session',
        title: 'Wake this agent now',
        description: 'End the snooze and let it back into the queue',
        keywords: ['wake', 'snooze', 'defer', 'queue', 'turn'],
        icon: <AttentionActionIcon />,
        run: () => sendWakeTurn(activeSessionId),
      });
    } else if (handleSnoozeActiveSession) {
      items.push({
        id: 'snooze-active-session',
        title: 'Snooze this agent…',
        description: 'Take it off your plate until a time you choose',
        keywords: ['snooze', 'defer', 'later', 'queue', 'turn'],
        icon: <AttentionActionIcon />,
        shortcut: [shortcutTokens('session.snooze')],
        run: handleSnoozeActiveSession,
      });
    }
    return items;
  }, [
    actionMenuItemsWithWorkspaceActions,
    queueModeEnabled,
    activeSessionId,
    activeSessionQueueEligible,
    activeSessionSnoozedUntil,
    handleSnoozeActiveSession,
    sendWakeTurn,
  ]);

  return useMemo(
    () => [...navigationCommands, ...actionMenuItemsWithQueueActions],
    [navigationCommands, actionMenuItemsWithQueueActions],
  );
}
