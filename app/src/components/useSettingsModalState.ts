import { open } from '@tauri-apps/plugin-dialog';
import { useCallback, useEffect, useImperativeHandle, useMemo, useState, type ForwardedRef } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useGitHubPollingOffReason } from '../contexts/GitHubPollingContext';
import { useAutoModePolicy } from '../hooks/useAutoModePolicy';
import { DaemonEndpoint, DaemonSettings } from '../hooks/useDaemonSocket';
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
import { delegationLiveCount } from './delegationRoles';
import { assertValidSettingsSectionID, setSettingsAutomationHandle } from './settingsAutomation';
import { useAgentSettingDrafts, useSettingDraft } from './settingsDrafts';
import {
  DEFAULT_CONTEXT_WINDOW_CAP,
  OPEN_SENT_FILES_ENABLED_SETTING,
  PTY_BACKENDS,
  SettingsModalHandle,
  SettingsModalProps,
  SettingsNavGroup,
  SettingsSectionID,
  formatByteCount,
} from './settingsModalShared';
import { useEndpointPanel } from './useEndpointPanel';
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
  endpoints,
  plugins,
  pluginIssues,
  onAddEndpoint,
  onUpdateEndpoint,
  onRemoveEndpoint,
  onSetEndpointRemoteWeb,
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
    sendDelegationModels,
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
  const delegationPolicy = useDelegationPreferences(
    isOpen,
    sendDelegationPreferencesGet,
    sendDelegationPreferencesSave,
  );
  const [settingsSearch, setSettingsSearch] = useState('');
  const endpointPanel = useEndpointPanel();
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
  const workflowsEnabled = (settings.workflows_enabled || 'false') === 'true';
  const autoApproveEnabled = (settings.auto_approve_enabled || 'false') === 'true';

  const actualAgentExecutables = useMemo(() => getAgentExecutableSettings(settings), [settings]);
  const actualAgentCapabilities = useMemo(() => getAgentCapabilities(settings), [settings]);
  const actualEditorExecutable = settings.editor_executable || '';
  const actualDefaultAgent = normalizeSessionAgent(settings.new_session_agent, 'claude');
  const actualChiefContextCap = settings.chief_context_window_cap || String(DEFAULT_CONTEXT_WINDOW_CAP);
  const actualHeadlessContextCap = settings.headless_context_window_cap || String(DEFAULT_CONTEXT_WINDOW_CAP);
  const autoSettleEnabled = isAutoSettleEnabled(settings);
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
  const rawPtyBackendMode = (settings.pty_backend_mode || 'unknown').toLowerCase();
  const ptyBackendMode = Object.prototype.hasOwnProperty.call(PTY_BACKENDS, rawPtyBackendMode)
    ? rawPtyBackendMode
    : 'unknown';
  const { label: ptyBackendLabel, hint: ptyBackendHint } = PTY_BACKENDS[ptyBackendMode];
  const sharedPtyHostEnabled = settings.pty_shared_host_enabled === 'true';
  const sharedPtyHostActive = settings.pty_shared_host_active === 'true';

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

  const { reopen: reopenEndpointPanel } = endpointPanel;
  const { setSourcePath: setPluginSourcePath } = pluginPanel;
  useEffect(() => {
    if (!isOpen) return;
    reopenEndpointPanel();
    setPluginSourcePath('');
  }, [isOpen, reopenEndpointPanel, setPluginSourcePath]);

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

  const handleToggleWorkflows = useCallback(() => {
    onSetSetting('workflows_enabled', workflowsEnabled ? 'false' : 'true');
  }, [onSetSetting, workflowsEnabled]);

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

  const handleAddEndpoint = useCallback(async () => {
    const name = endpointPanel.draft.name.trim();
    const sshTarget = endpointPanel.draft.target.trim();
    const profile = endpointPanel.draft.profile.trim();
    if (!name || !sshTarget) {
      endpointPanel.fail('Endpoint name and SSH target are required.');
      return;
    }
    await endpointPanel.run('new', 'Failed to add endpoint', async () => {
      await onAddEndpoint(name, sshTarget, profile);
      endpointPanel.clearDraft();
    });
  }, [endpointPanel, onAddEndpoint]);

  const handleSaveEndpoint = useCallback(
    async (endpointId: string) => {
      const editing = endpointPanel.editing;
      if (!editing) return;
      const name = editing.name.trim();
      const sshTarget = editing.target.trim();
      const profile = editing.profile.trim();
      if (!name || !sshTarget) {
        endpointPanel.fail('Endpoint name and SSH target are required.');
        return;
      }
      await endpointPanel.run(endpointId, 'Failed to update endpoint', async () => {
        await onUpdateEndpoint(endpointId, { name, ssh_target: sshTarget, profile });
        endpointPanel.cancelEdit();
      });
    },
    [endpointPanel, onUpdateEndpoint],
  );

  const handleToggleEndpoint = useCallback(
    async (endpoint: DaemonEndpoint) => {
      await endpointPanel.run(endpoint.id, 'Failed to update endpoint', () =>
        onUpdateEndpoint(endpoint.id, { enabled: endpoint.enabled === false }).then(() => undefined),
      );
    },
    [endpointPanel, onUpdateEndpoint],
  );

  const handleRebootstrapEndpoint = useCallback(
    async (endpoint: DaemonEndpoint) => {
      if (endpoint.enabled === false) return;
      await endpointPanel.run(endpoint.id, 'Failed to re-bootstrap endpoint', async () => {
        await onUpdateEndpoint(endpoint.id, { enabled: false });
        await onUpdateEndpoint(endpoint.id, { enabled: true });
      });
    },
    [endpointPanel, onUpdateEndpoint],
  );

  const handleRemoveEndpoint = useCallback(
    async (endpointId: string) => {
      await endpointPanel.run(endpointId, 'Failed to remove endpoint', async () => {
        await onRemoveEndpoint(endpointId);
        if (endpointPanel.editing?.id === endpointId) endpointPanel.cancelEdit();
      });
    },
    [endpointPanel, onRemoveEndpoint],
  );

  const handleSetEndpointRemoteWeb = useCallback(
    async (endpointId: string, enabled: boolean) => {
      await endpointPanel.run(endpointId, 'Failed to update remote web access', () =>
        onSetEndpointRemoteWeb(endpointId, enabled).then(() => undefined),
      );
    },
    [endpointPanel, onSetEndpointRemoteWeb],
  );

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

  const connectedEndpointCount = endpoints.filter((endpoint) => endpoint.status === 'connected').length;
  const activePluginCount = plugins.filter((plugin) => plugin.connected || plugin.running).length;
  const mutedItemCount = mutedRepos.length + mutedAuthors.length;
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
            title: 'Appearance',
            description: 'Theme and text size, for the app and for the garden.',
            count: 2,
            keywords: 'theme appearance dark light system font size text scale zoom garden',
          },
          {
            id: 'workspace',
            label: 'Files and locations',
            title: 'Files and locations',
            description:
              'Where attn opens repositories and worktrees, when merged worktrees are reclaimed, where your Notebook lives, and what it does with a file an agent sends you.',
            count: 4,
            keywords:
              'projects directory worktrees roots notebook folder knowledge base journal location editor executable sent files tiles open markdown worktree sweep reclaim merged keep pin',
          },
          {
            id: 'hygiene',
            label: 'Attention queue',
            title: 'Attention queue',
            description: 'When a turn settles itself, and which repositories and authors never reach the queue at all.',
            count: mutedItemCount + 1,
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
            title: 'Agents and models',
            description: 'Defaults for new sessions, with configuration for each agent.',
            count: orderedAgentList.length + 8,
            keywords:
              'agents executables claude codex copilot pi default capabilities model effort pricing context window cap tokens compaction auto-approve unattended',
          },
          {
            id: 'backgroundAgents',
            label: 'Background agents',
            title: 'Background agents',
            description: 'The agents that summarize session activity, review the garden, and coordinate work.',
            count: 3,
            keywords: 'chief model effort headless context cap session activity summary refresh garden advisor',
          },
          {
            id: 'delegation',
            label: 'Delegation',
            title: 'Delegation',
            description: '',
            count: delegationLiveCount(delegationPolicy.preferences),
            keywords:
              'delegate roles pathfinder builder reviewer orchestrator fallback harness models effort preferences alternatives',
          },
          {
            id: 'workflows',
            label: 'Workflows',
            title: 'Workflows',
            description: 'Durable multi-agent workflows that managed agents can run when you opt in.',
            count: workflowsEnabled ? 1 : 0,
            keywords: 'workflows hypercode multi-agent orchestration attn workflow run',
          },
          {
            id: 'autoMode',
            label: 'Auto mode',
            title: 'Auto mode',
            description: "Manage attn's pi automode plugin",
            count: autoModePolicy.pendingCount,
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
            label: 'Mobile, hosts, remotes',
            title: 'Mobile web, hosts, and remote endpoints',
            description: 'Controls for mobile browser access, GitHub host detection, and remote attn peers.',
            count: Math.max(3, endpoints.length + githubHosts.length + 1),
            keywords: 'tailscale mobile web github hosts ssh remote endpoint daemon',
          },
        ],
      },
      {
        label: 'Extensions',
        items: [
          {
            id: 'plugins',
            label: 'Plugins',
            title: 'Plugins',
            description: 'Install user-owned plugins and tune provider dispatch priority.',
            count: Math.max(1, plugins.length + pluginIssues.length),
            keywords: 'plugins extensions providers priority install healthcheck',
          },
        ],
      },
      {
        label: 'System',
        items: [
          {
            id: 'terminal',
            label: 'Terminal',
            title: 'Terminal',
            description: 'How terminal sessions are hosted.',
            count: 1,
            keywords: 'pty backend shared rust host workers experimental terminal',
          },
          {
            id: 'backgroundTasks',
            label: 'Task runner',
            title: 'Background tasks',
            description: 'The durable task runner: compaction, summaries, narration, and reconciliation, with retry.',
            count: 1,
            keywords: 'background tasks runner durable compaction summarize narrate reconcile retry failed dead queue',
          },
          {
            id: 'eventBus',
            label: 'Event bus',
            title: 'Event bus',
            description:
              'The durable event log: what it holds, which facts are written to it and how fast, and who reads it.',
            count: 1,
            keywords:
              'event bus log durable facts producers consumers cursor lag retention compaction trim stalled disabled kill switch seq',
          },
          {
            id: 'data',
            label: 'Model data capture',
            title: 'Local model data capture',
            description:
              'Opt-in collection of visible Codex and Claude terminal viewports for local model evaluation and training.',
            count: 3,
            keywords: 'model training dataset capture privacy terminal viewport local retention sampling',
          },
        ],
      },
    ],
    [
      githubHosts.length,
      endpoints.length,
      mutedItemCount,
      orderedAgentList.length,
      pluginIssues.length,
      plugins.length,
      autoModePolicy.pendingCount,
      delegationPolicy.preferences,
      workflowsEnabled,
    ],
  );

  const filteredNavGroups = useMemo(() => {
    const query = settingsSearch.trim().toLowerCase();
    if (query === '') return settingsNavGroups;
    return settingsNavGroups
      .map((group) => ({
        ...group,
        items: group.items.filter((item) =>
          `${item.label} ${item.title} ${item.description} ${item.keywords}`.toLowerCase().includes(query),
        ),
      }))
      .filter((group) => group.items.length > 0);
  }, [settingsNavGroups, settingsSearch]);

  const flatNavItems = useMemo(() => settingsNavGroups.flatMap((group) => group.items), [settingsNavGroups]);

  const selectedNavItem = flatNavItems.find((item) => item.id === selectedSection) || flatNavItems[0];
  return {
    selectedSection,
    delegationPolicy,
    tailscaleEnabled,
    tailscaleStatus,
    endpoints,
    connectedEndpointCount,
    pluginProblemCount,
    activePluginCount,
    plugins,
    modelCaptureEnabled,
    modelCaptureBytes,
    autoModePolicy,
    autoSettleEnabled,
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
    endpointPanel,
    handleAddEndpoint,
    handleSaveEndpoint,
    handleToggleEndpoint,
    handleRebootstrapEndpoint,
    handleSetEndpointRemoteWeb,
    handleRemoveEndpoint,
    pluginPanel,
    setPluginSourcePath,
    handleBrowsePluginPath,
    handleInstallPlugin,
    pluginIssues,
    handleInstallBundledPlugin,
    handleUninstallPlugin,
    handleRemovePlugin,
    commitPluginPriority,
    handleToggleWorkflows,
    workflowsEnabled,
    ptyBackendHint,
    ptyBackendMode,
    ptyBackendLabel,
    sharedPtyHostActive,
    sharedPtyHostEnabled,
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
    sendDelegationModels,
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
