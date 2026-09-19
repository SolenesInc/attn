import { forwardRef, type ForwardedRef } from 'react';
import { AutoModeSettings } from './AutoModeSettings';
import { DelegationSettings } from './DelegationSettings';
import { SettingsAutosaveProvider, SettingsAutosaveStatus } from './SettingsAutosave';
import './SettingsModal.css';
import {
  AgentSettings,
  AppearanceSettings,
  BackgroundAgentSettings,
  BackgroundTasksSection,
  ConnectivitySettings,
  DataSettings,
  EventBusSection,
  HygieneSettings,
  PluginSettings,
  SectionStatusPills,
  TerminalSettings,
  WorkflowsSettings,
  WorkspaceSettings,
} from './SettingsModalSections';
import { SettingsModalHandle, SettingsModalProps } from './settingsModalShared';
import { useSettingsModalState, type SettingsModalState } from './useSettingsModalState';
export type { SettingsModalHandle } from './settingsModalShared';

export const SettingsModal = forwardRef<SettingsModalHandle, SettingsModalProps>((props, ref) => (
  <SettingsAutosaveProvider save={props.onSetSetting}>
    <SettingsModalContent {...props} closeRef={ref} />
  </SettingsAutosaveProvider>
));

function SettingsModalContent(props: SettingsModalProps & { closeRef: ForwardedRef<SettingsModalHandle> }) {
  const state = useSettingsModalState(props);
  const {
    closeSettings,
    settingsSearch,
    setSettingsSearch,
    filteredNavGroups,
    selectedSection,
    selectSection,
    selectedNavItem,
  } = state;
  if (!props.isOpen) return null;
  return (
    <div className="settings-overlay">
      <button
        type="button"
        className="settings-backdrop"
        data-testid="settings-overlay"
        aria-label="Dismiss settings"
        tabIndex={-1}
        onClick={() => void closeSettings()}
      />
      <div className="settings-modal" data-testid="settings-modal">
        <div className="settings-header" data-testid="settings-header">
          <div className="settings-title">
            <h2>Settings</h2>
            <span className="settings-profile">local daemon</span>
          </div>
          <div className="settings-top-actions">
            <input
              className="settings-search"
              type="search"
              value={settingsSearch}
              onChange={(e) => setSettingsSearch(e.target.value)}
              placeholder="Search settings"
              aria-label="Search settings"
            />
            <button
              className="settings-close"
              data-testid="settings-close"
              onClick={() => void closeSettings()}
              aria-label="Close settings"
            >
              x
            </button>
          </div>
        </div>

        <div className="settings-layout">
          <nav className="settings-nav" aria-label="Settings sections">
            {filteredNavGroups.length === 0 ? (
              <p className="settings-empty nav-empty">No matching settings.</p>
            ) : (
              filteredNavGroups.map((group) => (
                <div className="settings-nav-group" key={group.label}>
                  <div className="settings-nav-label">{group.label}</div>
                  {group.items.map((item) => (
                    <button
                      key={item.id}
                      type="button"
                      data-testid={`settings-nav-${item.id}`}
                      className={`settings-nav-item ${selectedSection === item.id ? 'active' : ''}`}
                      onClick={() => void selectSection(item.id)}
                    >
                      <span>{item.label}</span>
                      {!['agents', 'backgroundAgents', 'terminal'].includes(item.id) && (
                        <span
                          className={`settings-nav-count${item.id === 'autoMode' && item.count > 0 ? ' waiting' : ''}`}
                        >
                          {item.count}
                        </span>
                      )}
                    </button>
                  ))}
                </div>
              ))
            )}
          </nav>

          <main className="settings-body" data-testid="settings-body">
            <div className="settings-content-head">
              <div>
                {selectedNavItem?.label !== selectedNavItem?.title && (
                  <div className="settings-kicker">{selectedNavItem?.label}</div>
                )}
                <h1>{selectedNavItem?.title}</h1>
                {selectedNavItem?.description && <p className="settings-lead">{selectedNavItem.description}</p>}
              </div>
              <div className="settings-status-pair">
                {
                  <SectionStatusPills
                    selectedSection={state.selectedSection}
                    delegationPolicy={state.delegationPolicy}
                    tailscaleEnabled={state.tailscaleEnabled}
                    tailscaleStatus={state.tailscaleStatus}
                    endpoints={state.endpoints}
                    connectedEndpointCount={state.connectedEndpointCount}
                    pluginProblemCount={state.pluginProblemCount}
                    activePluginCount={state.activePluginCount}
                    plugins={state.plugins}
                    modelCaptureEnabled={state.modelCaptureEnabled}
                    modelCaptureBytes={state.modelCaptureBytes}
                    autoModePolicy={state.autoModePolicy}
                    autoSettleEnabled={state.autoSettleEnabled}
                    mutedRepos={state.mutedRepos}
                    mutedAuthors={state.mutedAuthors}
                    hasProjectsDirChange={state.hasProjectsDirChange}
                    openSentFilesEnabled={state.openSentFilesEnabled}
                  />
                }
              </div>
            </div>
            <SettingsAutosaveStatus />
            <div className="settings-section-content" data-testid={`settings-section-${selectedSection}`}>
              {<SelectedSection state={state} />}
            </div>
          </main>
        </div>
      </div>
    </div>
  );
}

function SelectedSection({ state }: { state: SettingsModalState }) {
  const { selectedSection, delegationPolicy, sendDelegationModels, autoModePolicy } = state;

  switch (selectedSection) {
    case 'general':
      return (
        <AppearanceSettings
          themePreference={state.themePreference}
          onSetTheme={state.onSetTheme}
          onDecreaseUIScale={state.onDecreaseUIScale}
          uiScale={state.uiScale}
          onIncreaseUIScale={state.onIncreaseUIScale}
          onResetUIScale={state.onResetUIScale}
          onDecreaseGardenScale={state.onDecreaseGardenScale}
          gardenScale={state.gardenScale}
          effectiveGardenScale={state.effectiveGardenScale}
          onIncreaseGardenScale={state.onIncreaseGardenScale}
          onMatchAppGardenScale={state.onMatchAppGardenScale}
        />
      );
    case 'workspace':
      return (
        <WorkspaceSettings
          editorDraft={state.editorDraft}
          savedFlash={state.savedFlash}
          projectsDirDraft={state.projectsDirDraft}
          handleBrowse={state.handleBrowse}
          handleToggleWorktreeSweep={state.handleToggleWorktreeSweep}
          worktreeSweepEnabled={state.worktreeSweepEnabled}
          notebookRootDraft={state.notebookRootDraft}
          effectiveNotebookRoot={state.effectiveNotebookRoot}
          handleBrowseNotebookRoot={state.handleBrowseNotebookRoot}
          handleToggleOpenSentFiles={state.handleToggleOpenSentFiles}
          openSentFilesEnabled={state.openSentFilesEnabled}
        />
      );
    case 'plugins':
      return (
        <PluginSettings
          pluginPanel={state.pluginPanel}
          setPluginSourcePath={state.setPluginSourcePath}
          handleBrowsePluginPath={state.handleBrowsePluginPath}
          handleInstallPlugin={state.handleInstallPlugin}
          pluginIssues={state.pluginIssues}
          plugins={state.plugins}
          handleInstallBundledPlugin={state.handleInstallBundledPlugin}
          handleUninstallPlugin={state.handleUninstallPlugin}
          handleRemovePlugin={state.handleRemovePlugin}
          commitPluginPriority={state.commitPluginPriority}
          savedFlash={state.savedFlash}
        />
      );
    case 'agents':
      return (
        <AgentSettings
          hasAvailableAgents={state.hasAvailableAgents}
          orderedAgentList={state.orderedAgentList}
          agentAvailability={state.agentAvailability}
          defaultAgent={state.defaultAgent}
          handleDefaultAgentChange={state.handleDefaultAgentChange}
          autoApproveEnabled={state.autoApproveEnabled}
          handleToggleAutoApprove={state.handleToggleAutoApprove}
          defaultOverrideAgentList={state.defaultOverrideAgentList}
          actualAgentCapabilities={state.actualAgentCapabilities}
          agentCapabilityOrder={state.agentCapabilityOrder}
          resolvedDefaultAgent={state.resolvedDefaultAgent}
          settings={state.settings}
          defaultModelDrafts={state.defaultModelDrafts}
          defaultEffortDrafts={state.defaultEffortDrafts}
          executableAgentList={state.executableAgentList}
          executableDrafts={state.executableDrafts}
          defaultContextCapDrafts={state.defaultContextCapDrafts}
          onSetSetting={state.onSetSetting}
        />
      );
    case 'backgroundAgents':
      return (
        <BackgroundAgentSettings
          settings={state.settings}
          activityAgents={state.activityAgents}
          onSetSetting={state.onSetSetting}
          gardenAdvisorAgents={state.gardenAdvisorAgents}
          chiefOverrideAgentList={state.chiefOverrideAgentList}
          chiefModelDrafts={state.chiefModelDrafts}
          chiefEffortDrafts={state.chiefEffortDrafts}
          savedFlash={state.savedFlash}
          chiefContextCapDraft={state.chiefContextCapDraft}
          headlessContextCapDraft={state.headlessContextCapDraft}
        />
      );
    case 'terminal':
      return (
        <TerminalSettings
          ptyBackendHint={state.ptyBackendHint}
          ptyBackendMode={state.ptyBackendMode}
          ptyBackendLabel={state.ptyBackendLabel}
          sharedPtyHostActive={state.sharedPtyHostActive}
          sharedPtyHostEnabled={state.sharedPtyHostEnabled}
          onSetSetting={state.onSetSetting}
        />
      );
    case 'data':
      return (
        <DataSettings
          handleToggleModelCapture={state.handleToggleModelCapture}
          modelCaptureEnabled={state.modelCaptureEnabled}
          modelCaptureInterval={state.modelCaptureInterval}
          onSetSetting={state.onSetSetting}
          modelCaptureMaxGB={state.modelCaptureMaxGB}
          modelCaptureBytes={state.modelCaptureBytes}
          modelCapturePath={state.modelCapturePath}
        />
      );
    case 'hygiene':
      return (
        <HygieneSettings
          handleToggleAutoSettle={state.handleToggleAutoSettle}
          autoSettleEnabled={state.autoSettleEnabled}
          autoSettleArmDraft={state.autoSettleArmDraft}
          savedFlash={state.savedFlash}
          autoSettleCountdownDraft={state.autoSettleCountdownDraft}
          mutedRepos={state.mutedRepos}
          onUnmuteRepo={state.onUnmuteRepo}
          mutedAuthors={state.mutedAuthors}
          onUnmuteAuthor={state.onUnmuteAuthor}
        />
      );
    case 'backgroundTasks':
      return (
        <BackgroundTasksSection
          listTasks={state.listTasks}
          retryTask={state.retryTask}
          taskChangeSignal={state.taskChangeSignal}
        />
      );
    case 'eventBus':
      return (
        <EventBusSection
          sendBusStatusGet={state.sendBusStatusGet}
          sendBusSetConsumerEnabled={state.sendBusSetConsumerEnabled}
        />
      );
    case 'delegation':
      return <DelegationSettings policy={delegationPolicy} loadModels={sendDelegationModels} />;
    case 'workflows':
      return (
        <WorkflowsSettings
          handleToggleWorkflows={state.handleToggleWorkflows}
          workflowsEnabled={state.workflowsEnabled}
        />
      );
    case 'autoMode':
      return <AutoModeSettings policy={autoModePolicy} loadModels={sendDelegationModels} />;
    case 'connectivity':
    default:
      return (
        <ConnectivitySettings
          tailscaleURL={state.tailscaleURL}
          tailscaleDomain={state.tailscaleDomain}
          handleToggleTailscale={state.handleToggleTailscale}
          tailscaleEnabled={state.tailscaleEnabled}
          tailscaleStatus={state.tailscaleStatus}
          tailscaleAuthURL={state.tailscaleAuthURL}
          tailscaleError={state.tailscaleError}
          githubPollingOffReason={state.githubPollingOffReason}
          githubHosts={state.githubHosts}
          endpointPanel={state.endpointPanel}
          handleAddEndpoint={state.handleAddEndpoint}
          endpoints={state.endpoints}
          handleSaveEndpoint={state.handleSaveEndpoint}
          handleToggleEndpoint={state.handleToggleEndpoint}
          handleRebootstrapEndpoint={state.handleRebootstrapEndpoint}
          handleSetEndpointRemoteWeb={state.handleSetEndpointRemoteWeb}
          handleRemoveEndpoint={state.handleRemoveEndpoint}
        />
      );
  }
}
