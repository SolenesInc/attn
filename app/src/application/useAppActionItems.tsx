import { useMemo } from 'react';
import { type ActionMenuItem } from '../components/ActionMenu';
import { SessionRoleIcon } from '../components/DelegationChain';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { shortcutTokens } from '../shortcuts/formatShortcut';
import { useDaemonStore } from '../store/daemonSessions';
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
  useWorkspaceTilesContext,
} from './AppContexts';
import {
  AttentionActionIcon,
  BoardActionIcon,
  ContextActionIcon,
  GardenIcon,
  KeyboardActionIcon,
} from './AppIcons';
export function useAppActionItems() {
  const apps = useDaemonStore((state) => state.apps);
  const seeds = useDaemonStore((state) => state.seeds);
  const { setAppViewParamsPrompt, dockAppViewTile, setMarkdownOpenerOpen, handleOpenNotebookTile } =
    useWorkspaceTilesContext();
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
    activeWorkspaceForCommands,
    activeSessionForCommands,
    activeSessionQueueEligible,
    queueModeEnabled,
    handleSnoozeActiveSession,
  } = useAttentionQueueContext();
  const { sendSetSetting, sendPinSession, sendPinWorkspace, sendMuteWorkspace, sendWakeTurn } =
    useDaemonApi();
  const { handleCreateDiagnosticReport } = useAppDiagnosticsContext();
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const appViewMenuItems = useMemo<ActionMenuItem[]>(() => {
    const items: ActionMenuItem[] = [];
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

  const actionMenuItems = useMemo<ActionMenuItem[]>(
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

  const actionMenuItemsWithWorkspaceActions = useMemo<ActionMenuItem[]>(() => {
    const workspace = activeWorkspaceForCommands;
    if (!workspace) return [...actionMenuItems, ...appViewMenuItems];
    const activeSession = activeSessionForCommands;
    const delegationItems: ActionMenuItem[] = activeSession
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
    const sessionPinItems: ActionMenuItem[] =
      activeSession && activeSessionQueueEligible && !activeSession.chiefOfStaff
        ? [
            {
              id: 'pin-active-session',
              title: activeSession.pinnedAt
                ? `Unpin ${activeSession.label}`
                : `Pin ${activeSession.label}`,
              description: activeSession.pinnedAt
                ? 'Put this agent back in the queue'
                : 'Take this agent out of the queue and keep it in view',
              keywords: ['pin', 'unpin', 'agent', 'session', 'queue'],
              icon: <AttentionActionIcon />,
              run: () => sendPinSession(activeSession.id, !activeSession.pinnedAt),
            },
          ]
        : [];
    const sessionSeedItems: ActionMenuItem[] =
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
    const sessionUsageItems: ActionMenuItem[] =
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
    const sessionCapItems: ActionMenuItem[] =
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
      ...sessionPinItems,
      ...delegationItems,
      ...sessionSeedItems,
      ...sessionUsageItems,
      ...sessionCapItems,
      {
        id: 'pin-active-workspace',
        title: workspace.pinned ? `Unpin ${workspace.title}` : `Pin ${workspace.title}`,
        description: workspace.pinned
          ? 'Put this workspace back in the queue'
          : 'Take this workspace out of the queue and keep it in view',
        keywords: ['pin', 'unpin', 'workspace', 'queue'],
        icon: <AttentionActionIcon />,
        run: () => sendPinWorkspace(workspace.id, !workspace.pinned),
      },
      {
        id: 'mute-active-workspace',
        title: workspace.muted ? `Unmute ${workspace.title}` : `Mute ${workspace.title}`,
        description: workspace.muted
          ? 'Let this workspace ask for you again'
          : 'Nothing from this workspace reaches you',
        keywords: ['mute', 'unmute', 'workspace', 'silence'],
        icon: <AttentionActionIcon />,
        run: () => sendMuteWorkspace(workspace.id, workspace.endpointId),
      },
    ];
  }, [
    delegationChainRef,
    setSeedPopoverRequest,
    setUsagePopoverRequest,
    setContextCapPromptSession,
    actionMenuItems,
    appViewMenuItems,
    activeWorkspaceForCommands,
    activeSessionForCommands,
    activeSessionQueueEligible,
    seeds,
    sendPinWorkspace,
    sendPinSession,
    sendMuteWorkspace,
  ]);

  const activeSessionSnoozedUntil = activeSessionQueueEligible
    ? activeSessionForCommands?.turnSnoozedUntil
    : undefined;
  const actionMenuItemsWithQueueActions = useMemo<ActionMenuItem[]>(() => {
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

  return actionMenuItemsWithQueueActions;
}
