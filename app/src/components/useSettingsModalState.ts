import { open } from '@tauri-apps/plugin-dialog';
import { isAsksEnabled } from '../utils/asks';
import { useCallback, useEffect, useImperativeHandle, useMemo, useState, type ForwardedRef } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useGitHubPollingOffReason } from '../contexts/GitHubPollingContext';
import { useAutoModePolicy } from '../hooks/useAutoModePolicy';
import { DaemonSettings } from '../hooks/useDaemonSocket';
import { useDelegationPreferences } from '../hooks/useDelegationPreferences';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import { parseActivityConfigSetting } from '../utils/activitySettings';
import {
  AGENT_CAPABILITY_ORDER,
  getAgentAvailability,
  getAgentCapabilities,
  getAgentExecutableSettings,
  hasAnyAvailableAgents,
  isAgentAvailable,
  orderedAgents,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import { parseGardenAdvisorSetting } from '../utils/gardenAdvisorSettings';
import {
  AUTO_SETTLE_ARM_SETTING,
  AUTO_SETTLE_COUNTDOWN_SETTING,
  AUTO_SETTLE_ENABLED_SETTING,
  autoSettleSeconds,
  isAutoSettleEnabled,
} from '../utils/queueBands';
import { useAutosaveSetting, useSettingsAutosave } from './SettingsAutosave';
import { assertValidSettingsSectionID, setSettingsAutomationHandle } from './settingsAutomation';
import { useAgentSettingDrafts, useSettingDraft } from './settingsDrafts';
import {
  DEFAULT_CONTEXT_WINDOW_CAP,
  OPEN_SENT_FILES_ENABLED_SETTING,
  SettingsModalHandle,
  SettingsModalProps,
  SettingsNavGroup,
  SettingsSectionID,
  formatByteCount,
} from './settingsModalShared';
import { usePluginPanel } from './usePluginPanel';
import { useSavedFlash } from './useSavedFlash';

export function useSettingsModalState({
  closeRef,
  isOpen,
  onClose,
  mutedRepos,
  githubHosts,
  onUnmuteRepo,
  mutedAuthors,
  onUnmuteAuthor,
  settings,
  plugins,
  pluginIssues,
  onListPlugins,
  onInstallPlugin,
  onInstallBundledPlugin,
  onUninstallPlugin,
  onRemovePlugin,
  onSetPluginPriority,
  themePreference,
  onSetTheme,
  uiScale = 1,
  onIncreaseUIScale,
  onDecreaseUIScale,
  onResetUIScale,
  gardenScale = null,
  effectiveGardenScale,
  onIncreaseGardenScale,
  onDecreaseGardenScale,
  onMatchAppGardenScale,
  listTasks,
  retryTask,
  taskChangeSignal,
}: SettingsModalProps & { closeRef: ForwardedRef<SettingsModalHandle> }) {
  const {
    sendDelegationPreferencesGet,
    sendDelegationPreferencesSave,
    sendHarnessModels,
    sendGetSettings,
    sendBusStatusGet,
    sendBusSetConsumerEnabled,
    sendAutoModeGet,
    sendAutoModePromote,
    sendAutoModeDiscard,
    sendAutoModeRuleAdd,
    sendAutoModeRuleRemove,
    sendAutoModeLegacyDismiss,
    sendAutoModeHostAdd,
    sendAutoModeHostRemove,
    sendAutoModePolicySet,
    sendAutoModeEnvSlot,
  } = useDaemonApi();
  const githubPollingOffReason = useGitHubPollingOffReason();
  const autoModePolicy = useAutoModePolicy({
    enabled: isOpen,
    getState: sendAutoModeGet,
    promoteProposal: sendAutoModePromote,
    discardProposal: sendAutoModeDiscard,
    addRule: sendAutoModeRuleAdd,
    removeRule: sendAutoModeRuleRemove,
    addHost: sendAutoModeHostAdd,
    removeHost: sendAutoModeHostRemove,
    setPolicy: sendAutoModePolicySet,
    setEnvironmentSlot: sendAutoModeEnvSlot,
    dismissLegacy: sendAutoModeLegacyDismiss,
  });
  const savedFlash = useSavedFlash();
  const autosave = useSettingsAutosave()!;
  const onSetSetting = useCallback(
    (key: string, value: string) => {
      autosave.ensure(key, settings[key] ?? '');
      autosave.set(key, value);
      void autosave.commit(key);
    },
    [autosave, settings],
  );
  const closeSettings = useCallback(async () => {
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
    if (!autosave.needsFlush()) {
      onClose();
      return;
    }
    if (await autosave.flush()) onClose();
  }, [autosave, onClose]);
  useImperativeHandle(closeRef, () => ({ close: closeSettings }), [closeSettings]);
  const selectSection = useCallback(
    async (section: SettingsSectionID) => {
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
      if (!autosave.needsFlush()) {
        setSelectedSection(section);
        return;
      }
      if (await autosave.flush()) setSelectedSection(section);
    },
    [autosave],
  );
  const [selectedSection, setSelectedSection] = useState<SettingsSectionID>('connectivity');
  const outpostsEnabled = settings.outposts_enabled === 'true';
  useEffect(() => {
    if (selectedSection === 'outposts' && !outpostsEnabled) setSelectedSection('connectivity');
  }, [selectedSection, outpostsEnabled]);
  const delegationPolicy = useDelegationPreferences(
    isOpen,
    sendDelegationPreferencesGet,
    sendDelegationPreferencesSave,
  );
  const [settingsSearch, setSettingsSearch] = useState('');
  const pluginPanel = usePluginPanel(onListPlugins);
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = useMemo(() => hasAnyAvailableAgents(agentAvailability), [agentAvailability]);

  const actualProjectsDir = settings.projects_directory || '';
  const actualNotebookRoot = settings['notebook.root'] || '';
  const effectiveNotebookRoot = settings['notebook.root.effective'] || '';
  const {
    tailscaleEnabled,
    modelCaptureEnabled,
    modelCaptureInterval,
    modelCaptureMaxGB,
    modelCapturePath,
    modelCaptureBytes,
    tailscaleStatus,
    tailscaleURL,
    tailscaleDomain,
    tailscaleAuthURL,
    tailscaleError,
  } = settingsConnectionAndCapture(settings);
  const autoApproveEnabled = (settings.auto_approve_enabled || 'true') === 'true';

  const actualAgentExecutables = useMemo(() => getAgentExecutableSettings(settings), [settings]);
  const actualAgentCapabilities = useMemo(() => getAgentCapabilities(settings), [settings]);
  const actualEditorExecutable = settings.editor_executable || '';
  const actualDefaultAgent = normalizeSessionAgent(settings.new_session_agent, 'claude');
  const actualChiefContextCap = settings.chief_context_window_cap || String(DEFAULT_CONTEXT_WINDOW_CAP);
  const actualHeadlessContextCap = settings.headless_context_window_cap || String(DEFAULT_CONTEXT_WINDOW_CAP);
  const autoSettleEnabled = isAutoSettleEnabled(settings);
  const calloutsEnabled = settings.callouts_enabled === 'true';
  const asksEnabled = isAsksEnabled(settings);
  const actualAutoSettleArm = String(autoSettleSeconds(settings, AUTO_SETTLE_ARM_SETTING));
  const actualAutoSettleCountdown = String(autoSettleSeconds(settings, AUTO_SETTLE_COUNTDOWN_SETTING));
  const resolvedDefaultAgent = resolvePreferredAgent(actualDefaultAgent, agentAvailability, 'codex');
  const defaultAgentDraft = useAutosaveSetting('new_session_agent', resolvedDefaultAgent, onSetSetting);
  const defaultAgent = defaultAgentDraft.value;
  const orderedAgentList = useMemo(
    () => orderedAgents(agentAvailability, resolvedDefaultAgent, 'codex'),
    [agentAvailability, resolvedDefaultAgent],
  );
  const executableAgentList = useMemo(
    () => orderedAgentList.filter((agent) => ['codex', 'claude', 'copilot'].includes(agent)),
    [orderedAgentList],
  );
  const chiefOverrideAgentList = useMemo(() => {
    const list = orderedAgentList.filter(
      (agent) => ['codex', 'claude'].includes(agent) && isAgentAvailable(agentAvailability, agent),
    );
    for (const agent of ['claude', 'codex'] as const) {
      if (
        !list.includes(agent) &&
        ((settings[`chief_model_${agent}`] || '').trim() !== '' ||
          (settings[`chief_effort_${agent}`] || '').trim() !== '')
      ) {
        list.push(agent);
      }
    }
    return list;
  }, [orderedAgentList, agentAvailability, settings]);
  const actualChiefModels = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of chiefOverrideAgentList) {
      out[agent] = settings[`chief_model_${agent}`] || '';
    }
    return out;
  }, [settings, chiefOverrideAgentList]);
  const actualChiefEfforts = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of chiefOverrideAgentList) {
      out[agent] = settings[`chief_effort_${agent}`] || '';
    }
    return out;
  }, [settings, chiefOverrideAgentList]);
  const defaultOverrideAgentList = useMemo(() => {
    const list = orderedAgentList.filter(
      (agent) => ['codex', 'claude'].includes(agent) && isAgentAvailable(agentAvailability, agent),
    );
    for (const agent of ['claude', 'codex'] as const) {
      if (
        !list.includes(agent) &&
        ((settings[`default_model_${agent}`] || '').trim() !== '' ||
          (settings[`default_effort_${agent}`] || '').trim() !== '' ||
          (settings[`default_context_window_cap_${agent}`] || '').trim() !== '')
      ) {
        list.push(agent);
      }
    }
    return list;
  }, [orderedAgentList, agentAvailability, settings]);
  const actualDefaultModels = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of defaultOverrideAgentList) {
      out[agent] = settings[`default_model_${agent}`] || '';
    }
    return out;
  }, [settings, defaultOverrideAgentList]);
  const actualDefaultEfforts = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of defaultOverrideAgentList) {
      out[agent] = settings[`default_effort_${agent}`] || '';
    }
    return out;
  }, [settings, defaultOverrideAgentList]);
  const actualDefaultContextCaps = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of defaultOverrideAgentList) {
      out[agent] = settings[`default_context_window_cap_${agent}`] || '';
    }
    return out;
  }, [settings, defaultOverrideAgentList]);
  const activityAgents = useMemo(() => {
    const eligible = orderedAgentList.filter(
      (agent) =>
        ['codex', 'claude'].includes(agent) &&
        isAgentAvailable(agentAvailability, agent) &&
        actualAgentCapabilities[agent]?.headless_task === true,
    );
    const configured = parseActivityConfigSetting(settings['activity.config']).agent;
    if (configured && !eligible.includes(configured)) eligible.push(configured);
    return eligible;
  }, [actualAgentCapabilities, agentAvailability, orderedAgentList, settings]);
  const gardenAdvisorAgents = useMemo(() => {
    const eligible = orderedAgentList.filter(
      (agent) =>
        ['codex', 'claude', 'copilot'].includes(agent) &&
        isAgentAvailable(agentAvailability, agent) &&
        actualAgentCapabilities[agent]?.headless_task === true,
    );
    const configured = parseGardenAdvisorSetting(settings['garden.advisor']).agent;
    if (!eligible.includes(configured)) eligible.push(configured);
    return eligible;
  }, [actualAgentCapabilities, agentAvailability, orderedAgentList, settings]);
  const agentCapabilityOrder = useMemo(() => AGENT_CAPABILITY_ORDER.map((cap) => cap as string), []);
  const ptyBackendMode = (settings.pty_backend_mode || 'unknown').toLowerCase();
  const newSessionLauncherEnabled = settings.new_session_launcher_enabled === 'true';
  const codexSharedEnabled = settings.codex_shared_enabled === 'true';
  const queueShowHiddenSessions = settings.queue_show_hidden_sessions !== 'false';
  const directAgentLaunchEnabled = settings.direct_agent_launch_enabled === 'true';
  const quickCaptureEnabled = settings.quick_capture_enabled === 'true';
  const sharedPtyHostEnabled = settings.pty_shared_host_enabled === 'true';

  const draftDeps = { active: isOpen, onSetSetting, savedFlash };
  const projectsDirDraft = useSettingDraft({
    ...draftDeps,
    actual: actualProjectsDir,
    settingKey: 'projects_directory',
  });
  const notebookRootDraft = useSettingDraft({
    ...draftDeps,
    actual: actualNotebookRoot,
    settingKey: 'notebook.root',
  });
  const editorDraft = useSettingDraft({
    ...draftDeps,
    actual: actualEditorExecutable,
    settingKey: 'editor_executable',
  });
  const chiefContextCapDraft = useSettingDraft({
    ...draftDeps,
    actual: actualChiefContextCap,
    settingKey: 'chief_context_window_cap',
    trim: true,
  });
  const headlessContextCapDraft = useSettingDraft({
    ...draftDeps,
    actual: actualHeadlessContextCap,
    settingKey: 'headless_context_window_cap',
    trim: true,
  });
  // Seconds. The modal mounts before the daemon's settings broadcast, so without the
  // reseed commit-on-blur writes the built-in defaults over a saved policy.
  const autoSettleArmDraft = useSettingDraft({
    ...draftDeps,
    actual: actualAutoSettleArm,
    settingKey: AUTO_SETTLE_ARM_SETTING,
    trim: true,
  });
  const autoSettleCountdownDraft = useSettingDraft({
    ...draftDeps,
    actual: actualAutoSettleCountdown,
    settingKey: AUTO_SETTLE_COUNTDOWN_SETTING,
    trim: true,
  });
  const executableDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualAgentExecutables,
    settingKey: (agent) => `${agent}_executable`,
  });
  const chiefModelDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualChiefModels,
    settingKey: (agent) => `chief_model_${agent}`,
    trim: true,
  });
  const chiefEffortDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualChiefEfforts,
    settingKey: (agent) => `chief_effort_${agent}`,
    flashKey: (agent) => `chief_model_${agent}`,
  });
  const defaultModelDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualDefaultModels,
    settingKey: (agent) => `default_model_${agent}`,
    trim: true,
  });
  const defaultEffortDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualDefaultEfforts,
    settingKey: (agent) => `default_effort_${agent}`,
    flashKey: (agent) => `default_model_${agent}`,
  });
  // Per-agent context-window cap; a chief launch still takes the chief cap above.
  // Blank => uncapped, unlike the chief and headless caps whose blank means the default.
  const defaultContextCapDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualDefaultContextCaps,
    settingKey: (agent) => `default_context_window_cap_${agent}`,
    trim: true,
  });

  const { setSourcePath: setPluginSourcePath } = pluginPanel;
  useEffect(() => {
    if (!isOpen) return;
    setPluginSourcePath('');
  }, [isOpen, setPluginSourcePath]);

  useEscapeStack(closeSettings, isOpen);

  useEffect(() => {
    if (!isOpen || selectedSection !== 'data') return;

    sendGetSettings();
    if (!modelCaptureEnabled) return;

    const intervalSeconds = Number(modelCaptureInterval);
    if (!Number.isFinite(intervalSeconds) || intervalSeconds <= 0) return;

    const intervalID = window.setInterval(sendGetSettings, intervalSeconds * 1000);
    return () => window.clearInterval(intervalID);
  }, [isOpen, selectedSection, modelCaptureEnabled, modelCaptureInterval, sendGetSettings]);

  // UI automation bridge handle (testing only). Registered for the whole
  // lifetime so the bridge can read `open: false` through it.
  useEffect(() => {
    setSettingsAutomationHandle({
      getState: () => ({
        open: isOpen,
        activeSection: selectedSection,
        search: settingsSearch,
      }),
      selectSection: (sectionId) => {
        assertValidSettingsSectionID(sectionId);
        return selectSection(sectionId);
      },
    });
    return () => setSettingsAutomationHandle(null);
  }, [isOpen, selectedSection, settingsSearch, selectSection]);

  const { set: setProjectsDir } = projectsDirDraft;
  const handleBrowse = useCallback(async () => {
    const selected = await open({
      directory: true,
      multiple: false,
      title: 'Select Projects Directory',
    });
    if (selected && typeof selected === 'string') {
      setProjectsDir(selected);
      onSetSetting('projects_directory', selected);
    }
  }, [onSetSetting, setProjectsDir]);

  const handleToggleTailscale = useCallback(() => {
    onSetSetting('tailscale_enabled', tailscaleEnabled ? 'false' : 'true');
  }, [onSetSetting, tailscaleEnabled]);

  const handleToggleAutoSettle = useCallback(() => {
    onSetSetting(AUTO_SETTLE_ENABLED_SETTING, autoSettleEnabled ? 'false' : 'true');
  }, [onSetSetting, autoSettleEnabled]);

  const openSentFilesEnabled = (settings[OPEN_SENT_FILES_ENABLED_SETTING] || 'true') === 'true';
  const handleToggleOpenSentFiles = useCallback(() => {
    onSetSetting(OPEN_SENT_FILES_ENABLED_SETTING, openSentFilesEnabled ? 'false' : 'true');
  }, [onSetSetting, openSentFilesEnabled]);

  const handleToggleModelCapture = useCallback(() => {
    onSetSetting('model_capture.enabled', modelCaptureEnabled ? 'false' : 'true');
  }, [modelCaptureEnabled, onSetSetting]);

  const { set: setNotebookRoot } = notebookRootDraft;
  const handleBrowseNotebookRoot = useCallback(async () => {
    const selected = await open({
      directory: true,
      multiple: false,
      title: 'Select Notebook Folder',
    });
    if (selected && typeof selected === 'string') {
      setNotebookRoot(selected);
      onSetSetting('notebook.root', selected);
    }
  }, [onSetSetting, setNotebookRoot]);

  const handleToggleAutoApprove = useCallback(() => {
    onSetSetting('auto_approve_enabled', autoApproveEnabled ? 'false' : 'true');
  }, [autoApproveEnabled, onSetSetting]);

  const handleDefaultAgentChange = (agent: SessionAgent) => {
    if (isAgentAvailable(agentAvailability, agent)) void defaultAgentDraft.apply(agent);
  };

  const worktreeSweepEnabled = settings['worktree_sweep_enabled'] !== 'false';

  const handleToggleWorktreeSweep = useCallback(() => {
    onSetSetting('worktree_sweep_enabled', worktreeSweepEnabled ? 'false' : 'true');
  }, [worktreeSweepEnabled, onSetSetting]);

  const { refresh: refreshPlugins } = pluginPanel;
  useEffect(() => {
    if (!isOpen) return;
    void refreshPlugins();
  }, [isOpen, refreshPlugins]);

  const handleBrowsePluginPath = useCallback(async () => {
    const selected = await open({
      directory: true,
      multiple: false,
      title: 'Select Plugin Directory',
    });
    if (selected && typeof selected === 'string') {
      setPluginSourcePath(selected);
    }
  }, [setPluginSourcePath]);

  const handleInstallPlugin = useCallback(async () => {
    const source = pluginPanel.sourcePath.trim();
    if (source === '') {
      pluginPanel.fail('Plugin source is required');
      return;
    }
    await pluginPanel.run('install', 'Failed to install plugin', async () => {
      await onInstallPlugin(source);
      setPluginSourcePath('');
      await refreshPlugins();
    });
  }, [onInstallPlugin, pluginPanel, refreshPlugins, setPluginSourcePath]);

  const handleRemovePlugin = useCallback(
    async (name: string) => {
      await pluginPanel.run(name, 'Failed to remove plugin', async () => {
        await onRemovePlugin(name);
        await refreshPlugins();
      });
    },
    [onRemovePlugin, pluginPanel, refreshPlugins],
  );

  const handleInstallBundledPlugin = useCallback(
    async (name: string) => {
      await pluginPanel.run(name, 'Failed to install bundled plugin', async () => {
        if (!onInstallBundledPlugin) throw new Error('Bundled plugin installation is unavailable');
        await onInstallBundledPlugin(name);
        await refreshPlugins();
      });
    },
    [onInstallBundledPlugin, pluginPanel, refreshPlugins],
  );

  const handleUninstallPlugin = useCallback(
    async (name: string) => {
      await pluginPanel.run(name, 'Failed to uninstall plugin', async () => {
        if (!onUninstallPlugin) throw new Error('Plugin uninstall is unavailable');
        await onUninstallPlugin(name);
        await refreshPlugins();
      });
    },
    [onUninstallPlugin, pluginPanel, refreshPlugins],
  );

  const commitPluginPriority = useCallback(
    async (name: string, current: number) => {
      const raw = pluginPanel.priorityDraft(name).trim();
      if (raw === String(current)) return;
      const priority = Number(raw);
      if (!Number.isInteger(priority)) {
        pluginPanel.fail('Plugin priority must be an integer');
        return;
      }
      await pluginPanel.run(name, 'Failed to update plugin priority', async () => {
        await onSetPluginPriority(name, priority);
        await refreshPlugins();
        savedFlash.flash(`plugin_priority_${name}`);
      });
    },
    [onSetPluginPriority, pluginPanel, refreshPlugins, savedFlash],
  );

  const activePluginCount = plugins.filter((plugin) => plugin.connected || plugin.running).length;
  const pluginProblemCount =
    pluginIssues.length + plugins.filter((plugin) => plugin.health_status === 'unhealthy').length;
  const hasProjectsDirChange = projectsDirDraft.value !== actualProjectsDir;

  const settingsNavGroups = useMemo<SettingsNavGroup[]>(
    () => [
      {
        label: 'attn',
        items: [
          {
            id: 'general',
            label: 'Appearance',
            description: 'Theme and text size, for the app and for the garden.',
            keywords: 'theme appearance dark light system font size text scale zoom garden',
          },
          {
            id: 'desktop',
            label: 'Files and locations',
            description:
              'Where attn opens repositories and worktrees, when merged worktrees are reclaimed, where your Notebook lives, and what it does with a file an agent sends you.',
            keywords:
              'projects directory worktrees roots notebook folder knowledge base journal location editor executable sent files tiles open markdown worktree sweep reclaim merged keep pin',
          },
          {
            id: 'hygiene',
            label: 'Attention queue',
            description: 'When a turn settles itself, and which repositories and authors never reach the queue at all.',
            keywords:
              'muted repositories repos authors hide unmute hygiene auto-settle settle turn countdown sidebar attention queue',
          },
        ],
      },
      {
        label: 'Agents',
        items: [
          {
            id: 'agents',
            label: 'Agents and models',
            description: 'Defaults for new sessions, with configuration for each agent.',
            keywords:
              'agents executables claude codex copilot pi default capabilities model effort pricing context window cap tokens compaction auto-approve unattended',
          },
          {
            id: 'models', label: 'Models', description: 'Tiers describe how much intelligence a task needs. Your changes apply across profiles on this daemon; Reset restores attn’s mapping.', keywords: 'harness model tier light standard deep overrides defaults',
          },
          {
            id: 'backgroundAgents',
            label: 'Background agents',
            description: 'The agents that summarize session activity, review the garden, and coordinate work.',
            keywords: 'chief model effort headless context cap session activity summary refresh garden advisor',
          },
          {
            id: 'delegation',
            label: 'Delegation',
            description: '',
            keywords:
              'delegate roles pathfinder builder reviewer orchestrator prototyper fallback harness models effort preferences alternatives',
          },
          {
            id: 'autoMode',
            label: 'Auto mode',
            description: "Manage attn's pi automode plugin",
            keywords:
              'auto mode automode pi safety envelope proposals promote discard allow deny forbidden rules hosts network approval policy sandbox permissions denials',
          },
        ],
      },
      {
        label: 'Connectivity',
        items: [
          {
            id: 'connectivity',
            label: 'Mobile and hosts',
            description: 'Controls for mobile browser access and GitHub host detection.',
            keywords: 'tailscale mobile web github hosts',
          },
          ...(outpostsEnabled ? [{
            id: 'outposts' as const,
            label: 'Outposts',
            title: 'Outposts',
            description: 'Connect this home to attn on other computers.',
            count: 1,
            keywords: 'outposts bridge host machine address pair re-pair remove connection build fingerprint',
          }] : []),
        ],
      },
      {
        label: 'Extensions',
        items: [
          {
            id: 'plugins',
            label: 'Plugins',
            description: 'Install bundled plugins or add your own from a local directory or Git repository, and set provider dispatch priority.',
            keywords: 'plugins extensions providers priority install healthcheck',
          },
        ],
      },
      {
        label: 'System',
        items: [
          {
            id: 'experimental',
            label: 'Experimental',
            description: 'Opt in to features that are still being developed.',
            keywords: 'experimental launcher 2.0 shared terminal host shared codex show hidden sessions in the queue direct agent launches terminal pty backend rust workers memory claude copilot one-screen new-session one screen new session project path asks questions answer decisions drawer agent callouts point guided steps',
          },
          {
            id: 'backgroundTasks',
            label: 'Task runner',
            description: 'Runs compaction, session summaries, desktop narration, and reconciliation. Exhausted retries raise a notification; retry the task here.',
            keywords: 'background tasks runner durable compaction summarize narrate reconcile retry failed dead queue',
          },
          {
            id: 'eventBus',
            label: 'Event bus',
            description:
              'The durable event log: what it holds, who writes to it, and who reads it. Also available through attn bus status.',
            keywords:
              'event bus log durable facts producers consumers cursor lag retention compaction trim stalled disabled kill switch seq',
          },
          {
            id: 'data',
            label: 'Model data capture',
            description:
              'Collect visible Codex and Claude terminal screens for local model evaluation and training. Off by default.',
            keywords: 'local model training dataset capture privacy terminal viewport local retention sampling',
          },
        ],
      },
    ],
    [outpostsEnabled],
  );

  const filteredNavGroups = useMemo(() => {
    const query = settingsSearch.trim().toLowerCase();
    if (query === '') return settingsNavGroups;
    return settingsNavGroups
      .map((group) => ({
        ...group,
        items: group.items.filter((item) =>
          `${item.label} ${item.description} ${item.keywords}`.toLowerCase().includes(query),
        ),
      }))
      .filter((group) => group.items.length > 0);
  }, [settingsNavGroups, settingsSearch]);

  const flatNavItems = useMemo(() => settingsNavGroups.flatMap((group) => group.items), [settingsNavGroups]);

  const selectedNavItem = flatNavItems.find((item) => item.id === selectedSection) || flatNavItems[0];
  return {
    selectedSection,
    outpostsEnabled,
    delegationPolicy,
    tailscaleEnabled,
    tailscaleStatus,
    pluginProblemCount,
    activePluginCount,
    plugins,
    modelCaptureEnabled,
    modelCaptureBytes,
    autoModePolicy,
    autoSettleEnabled,
    asksEnabled,
    calloutsEnabled,
    mutedRepos,
    mutedAuthors,
    hasProjectsDirChange,
    openSentFilesEnabled,
    themePreference,
    onSetTheme,
    onDecreaseUIScale,
    uiScale,
    onIncreaseUIScale,
    onResetUIScale,
    onDecreaseGardenScale,
    gardenScale,
    effectiveGardenScale,
    onIncreaseGardenScale,
    onMatchAppGardenScale,
    editorDraft,
    savedFlash,
    projectsDirDraft,
    handleBrowse,
    handleToggleWorktreeSweep,
    worktreeSweepEnabled,
    notebookRootDraft,
    effectiveNotebookRoot,
    handleBrowseNotebookRoot,
    handleToggleOpenSentFiles,
    tailscaleURL,
    tailscaleDomain,
    handleToggleTailscale,
    tailscaleAuthURL,
    tailscaleError,
    githubPollingOffReason,
    githubHosts,
    pluginPanel,
    setPluginSourcePath,
    handleBrowsePluginPath,
    handleInstallPlugin,
    pluginIssues,
    handleInstallBundledPlugin,
    handleUninstallPlugin,
    handleRemovePlugin,
    commitPluginPriority,
    ptyBackendMode,
    sharedPtyHostEnabled,
    newSessionLauncherEnabled,
    codexSharedEnabled,
    queueShowHiddenSessions,
    directAgentLaunchEnabled,
    quickCaptureEnabled,
    onSetSetting,
    settings,
    activityAgents,
    gardenAdvisorAgents,
    chiefOverrideAgentList,
    chiefModelDrafts,
    chiefEffortDrafts,
    chiefContextCapDraft,
    headlessContextCapDraft,
    hasAvailableAgents,
    orderedAgentList,
    agentAvailability,
    defaultAgent,
    handleDefaultAgentChange,
    autoApproveEnabled,
    handleToggleAutoApprove,
    defaultOverrideAgentList,
    actualAgentCapabilities,
    agentCapabilityOrder,
    resolvedDefaultAgent,
    defaultModelDrafts,
    defaultEffortDrafts,
    executableAgentList,
    executableDrafts,
    defaultContextCapDrafts,
    handleToggleModelCapture,
    modelCaptureInterval,
    modelCaptureMaxGB,
    modelCapturePath,
    handleToggleAutoSettle,
    autoSettleArmDraft,
    autoSettleCountdownDraft,
    onUnmuteRepo,
    onUnmuteAuthor,
    listTasks,
    retryTask,
    taskChangeSignal,
    sendBusStatusGet,
    sendBusSetConsumerEnabled,
    sendHarnessModels,
    closeSettings,
    settingsSearch,
    setSettingsSearch,
    filteredNavGroups,
    selectSection,
    selectedNavItem,
    isOpen,
  };
}

export type SettingsModalState = ReturnType<typeof useSettingsModalState>;

export function settingsConnectionAndCapture(settings: DaemonSettings) {
  const tailscaleEnabled = (settings.tailscale_enabled || 'false') === 'true';
  const modelCaptureEnabled = (settings['model_capture.enabled'] || 'false') === 'true';
  const modelCaptureInterval = settings['model_capture.interval_seconds'] || '10';
  const modelCaptureMaxGB = settings['model_capture.max_gb'] || '5';
  const modelCapturePath = settings['model_capture.path'] || '';
  const modelCaptureBytes = formatByteCount(settings['model_capture.bytes']);
  const tailscaleStatus = settings.tailscale_status || 'disabled';
  const tailscaleURL = settings.tailscale_url || '';
  const tailscaleDomain = settings.tailscale_domain || '';
  const tailscaleAuthURL = settings.tailscale_auth_url || '';
  const tailscaleError = settings.tailscale_error || '';
  return {
    tailscaleEnabled,
    modelCaptureEnabled,
    modelCaptureInterval,
    modelCaptureMaxGB,
    modelCapturePath,
    modelCaptureBytes,
    tailscaleStatus,
    tailscaleURL,
    tailscaleDomain,
    tailscaleAuthURL,
    tailscaleError,
  };
}
