import { Fragment } from 'react';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { agentCapabilityLabel, agentLabel, isAgentAvailable } from '../utils/agentAvailability';
import { AUTO_SETTLE_ARM_SETTING, AUTO_SETTLE_COUNTDOWN_SETTING } from '../utils/queueBands';
import { BackgroundTasksSettings } from './BackgroundTasksSettings';
import { DelegationSwitch } from './DelegationSettings';
import { EventBusSettings } from './EventBusSettings';
import { GardenAdvisorSettings } from './GardenAdvisorSettings';
import { SessionActivitySettings } from './SessionActivitySettings';
import { SessionCostPriceSettings } from './SessionCostPriceSettings';
import {
  CHIEF_EFFORT_LEVELS,
  DEFAULT_CONTEXT_WINDOW_CAP,
  MODEL_CAPTURE_INTERVAL_OPTIONS,
  MODEL_CAPTURE_MAX_GB_OPTIONS,
} from './settingsModalShared';
import { SavedMark } from './useSavedFlash';
import type { SettingsModalState } from './useSettingsModalState';

export function SectionStatusPills({
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
}: Pick<
  SettingsModalState,
  | 'selectedSection'
  | 'delegationPolicy'
  | 'tailscaleEnabled'
  | 'tailscaleStatus'
  | 'endpoints'
  | 'connectedEndpointCount'
  | 'pluginProblemCount'
  | 'activePluginCount'
  | 'plugins'
  | 'modelCaptureEnabled'
  | 'modelCaptureBytes'
  | 'autoModePolicy'
  | 'autoSettleEnabled'
  | 'mutedRepos'
  | 'mutedAuthors'
  | 'hasProjectsDirChange'
  | 'openSentFilesEnabled'
>) {
  switch (selectedSection) {
    case 'delegation':
      return <DelegationSwitch policy={delegationPolicy} />;
    case 'connectivity':
      return (
        <ConnectivityStatusPills
          tailscaleEnabled={tailscaleEnabled}
          tailscaleStatus={tailscaleStatus}
          endpoints={endpoints}
          connectedEndpointCount={connectedEndpointCount}
        />
      );
    case 'plugins':
      return (
        <PluginsStatusPills
          pluginProblemCount={pluginProblemCount}
          activePluginCount={activePluginCount}
          plugins={plugins}
        />
      );
    case 'data':
      return <DataStatusPills modelCaptureEnabled={modelCaptureEnabled} modelCaptureBytes={modelCaptureBytes} />;
    case 'autoMode':
      return <AutoModeStatusPills autoModePolicy={autoModePolicy} />;
    case 'hygiene':
      return (
        <HygieneStatusPills autoSettleEnabled={autoSettleEnabled} mutedRepos={mutedRepos} mutedAuthors={mutedAuthors} />
      );
    case 'workspace':
      return (
        <WorkspaceStatusPills hasProjectsDirChange={hasProjectsDirChange} openSentFilesEnabled={openSentFilesEnabled} />
      );
    case 'general':
    default:
      return null;
  }
}

export function AppearanceSettings({
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
}: Pick<
  SettingsModalState,
  | 'themePreference'
  | 'onSetTheme'
  | 'onDecreaseUIScale'
  | 'uiScale'
  | 'onIncreaseUIScale'
  | 'onResetUIScale'
  | 'onDecreaseGardenScale'
  | 'gardenScale'
  | 'effectiveGardenScale'
  | 'onIncreaseGardenScale'
  | 'onMatchAppGardenScale'
>) {
  return (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Appearance</div>
          <h3>Theme</h3>
          <p className="settings-description">Choose how attn renders the application chrome.</p>
        </div>
        <div className="settings-block-body">
          <div className="settings-segmented" role="group" aria-label="Theme preference">
            <button
              type="button"
              className={`settings-segmented-option ${themePreference === 'dark' ? 'active' : ''}`}
              onClick={() => onSetTheme('dark')}
              aria-pressed={themePreference === 'dark'}
            >
              Dark
            </button>
            <button
              type="button"
              className={`settings-segmented-option ${themePreference === 'light' ? 'active' : ''}`}
              onClick={() => onSetTheme('light')}
              aria-pressed={themePreference === 'light'}
            >
              Light
            </button>
            <button
              type="button"
              className={`settings-segmented-option ${themePreference === 'system' ? 'active' : ''}`}
              onClick={() => onSetTheme('system')}
              aria-pressed={themePreference === 'system'}
            >
              System
            </button>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Appearance</div>
          <h3>Font Size</h3>
          <p className="settings-description">
            Scale text across attn. The garden can use its own size, independent of the rest of the app.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">App</p>
              <p className="settings-row-copy">
                The whole interface, including terminals. Also adjustable with {formatShortcut('ui.increaseFontSize')}{' '}
                and {formatShortcut('ui.decreaseFontSize')}.
              </p>
            </div>
            <div className="settings-font-scale" data-testid="settings-app-font-scale">
              <button
                type="button"
                className="settings-action"
                onClick={onDecreaseUIScale}
                aria-label="Decrease app font size"
              >
                −
              </button>
              <span className="settings-font-scale-value" data-testid="settings-app-font-scale-value">
                {Math.round(uiScale * 100)}%
              </span>
              <button
                type="button"
                className="settings-action"
                onClick={onIncreaseUIScale}
                aria-label="Increase app font size"
              >
                +
              </button>
              {uiScale !== 1 && (
                <button type="button" className="settings-action" onClick={onResetUIScale}>
                  Reset
                </button>
              )}
            </div>
          </div>
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">The garden</p>
              <p className="settings-row-copy">
                Seeds, plots, and their logs. Matches the app size until you change it.
              </p>
            </div>
            <div className="settings-font-scale" data-testid="settings-garden-font-scale">
              <button
                type="button"
                className="settings-action"
                onClick={onDecreaseGardenScale}
                aria-label="Decrease garden font size"
              >
                −
              </button>
              <span className="settings-font-scale-value" data-testid="settings-garden-font-scale-value">
                {gardenScale === null ? 'Match app' : `${Math.round((effectiveGardenScale ?? gardenScale) * 100)}%`}
              </span>
              <button
                type="button"
                className="settings-action"
                onClick={onIncreaseGardenScale}
                aria-label="Increase garden font size"
              >
                +
              </button>
              {gardenScale !== null && (
                <button type="button" className="settings-action" onClick={onMatchAppGardenScale}>
                  Match app
                </button>
              )}
            </div>
          </div>
        </div>
      </section>
    </>
  );
}

export function WorkspaceSettings({
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
  openSentFilesEnabled,
}: Pick<
  SettingsModalState,
  | 'editorDraft'
  | 'savedFlash'
  | 'projectsDirDraft'
  | 'handleBrowse'
  | 'handleToggleWorktreeSweep'
  | 'worktreeSweepEnabled'
  | 'notebookRootDraft'
  | 'effectiveNotebookRoot'
  | 'handleBrowseNotebookRoot'
  | 'handleToggleOpenSentFiles'
  | 'openSentFilesEnabled'
>) {
  return (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <h3>Editor</h3>
          <p className="settings-description">The editor used when opening files.</p>
        </div>
        <div className="settings-block-body">
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-editor-exec">
              Editor
            </label>
            <span className="settings-status">Used when opening files</span>
            <input
              id="settings-editor-exec"
              type="text"
              value={editorDraft.value}
              onChange={editorDraft.onChange}
              onBlur={editorDraft.commit}
              onKeyDown={editorDraft.onKeyDown}
              placeholder="$EDITOR"
              className="settings-input"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <SavedMark shown={savedFlash.saved('editor_executable')} testID="settings-editor-saved" />
          </div>
        </div>
      </section>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Projects</div>
          <h3>Projects Directory</h3>
          <p className="settings-description">Directory where Git repositories are cloned and opened in worktrees.</p>
        </div>
        <div className="settings-block-body">
          <label className="settings-label" htmlFor="settings-projects-directory">
            Projects directory
          </label>
          <div className="settings-inline-form projects-dir-input">
            <input
              id="settings-projects-directory"
              data-testid="settings-projects-directory-input"
              type="text"
              value={projectsDirDraft.value}
              onChange={projectsDirDraft.onChange}
              onBlur={projectsDirDraft.commit}
              onKeyDown={projectsDirDraft.onKeyDown}
              placeholder="/Users/you/projects"
              className="settings-input"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <SavedMark shown={savedFlash.saved('projects_directory')} testID="settings-projects-directory-saved" />
            <button className="settings-action" onClick={handleBrowse}>
              Browse
            </button>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Worktrees</div>
          <h3>Worktree sweep</h3>
          <p className="settings-description">
            Reclaims worktrees whose work has landed. A worktree is only removed when it has been idle for 14 days, has
            no uncommitted or stashed changes, has nothing unpushed beyond what merged, holds no live session or open
            seed, and its branch is on the repository's integration branch — as a merged pull request, as an ancestor,
            or with an identical tree. Everything it keeps says why, and every removal lands in the sweep log and as a
            note on the seeds that worked there. On by default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Reclaim merged worktrees in the background</p>
              <p className="settings-row-copy">
                Turning it off stops removals; the daemon keeps refreshing worktree state so the Worktrees list in the
                ledger surface stays accurate, and the keep pins you set stay set.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-worktree-sweep-toggle"
              onClick={handleToggleWorktreeSweep}
            >
              {worktreeSweepEnabled ? 'Disable' : 'Enable'}
            </button>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Notebook</div>
          <h3>Notebook Folder</h3>
          <p className="settings-description">
            Where attn keeps your durable Notebook — dated journals and the knowledge base — as plain markdown you own.
            Leave blank to use the default (<code>~/attn-notebook</code>, separate per profile). Changing this points
            attn at the new folder; your existing notes are not moved, so move or sync the folder yourself if you want
            the current contents to come along.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-inline-form projects-dir-input">
            <input
              data-testid="settings-notebook-root-input"
              type="text"
              value={notebookRootDraft.value}
              onChange={notebookRootDraft.onChange}
              onBlur={notebookRootDraft.commit}
              onKeyDown={notebookRootDraft.onKeyDown}
              placeholder={effectiveNotebookRoot || '~/attn-notebook'}
              className="settings-input"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <SavedMark shown={savedFlash.saved('notebook.root')} testID="settings-notebook-root-saved" />
            <button className="settings-action" onClick={handleBrowseNotebookRoot}>
              Browse
            </button>
          </div>
          {effectiveNotebookRoot && (
            <p className="settings-description" data-testid="settings-notebook-root-effective">
              Currently: <code>{effectiveNotebookRoot}</code>
            </p>
          )}
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Workspace</div>
          <h3>Sent files</h3>
          <p className="settings-description">
            When an agent hands you a file with its send-file tool, attn opens the ones it can show as workspace tiles.
            On by default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Open files agents send you</p>
              <p className="settings-row-copy">
                A markdown file an agent sends opens as a live-reloading tile beside its terminal, so you see it without
                hunting through the transcript. File types attn cannot show are left alone.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-open-sent-files-toggle"
              onClick={handleToggleOpenSentFiles}
            >
              {openSentFilesEnabled ? 'Disable' : 'Enable'}
            </button>
          </div>
        </div>
      </section>
    </>
  );
}

export function ConnectivitySettings({
  tailscaleURL,
  tailscaleDomain,
  handleToggleTailscale,
  tailscaleEnabled,
  tailscaleStatus,
  tailscaleAuthURL,
  tailscaleError,
  githubPollingOffReason,
  githubHosts,
  endpointPanel,
  handleAddEndpoint,
  endpoints,
  handleSaveEndpoint,
  handleToggleEndpoint,
  handleRebootstrapEndpoint,
  handleSetEndpointRemoteWeb,
  handleRemoveEndpoint,
}: Pick<
  SettingsModalState,
  | 'tailscaleURL'
  | 'tailscaleDomain'
  | 'handleToggleTailscale'
  | 'tailscaleEnabled'
  | 'tailscaleStatus'
  | 'tailscaleAuthURL'
  | 'tailscaleError'
  | 'githubPollingOffReason'
  | 'githubHosts'
  | 'endpointPanel'
  | 'handleAddEndpoint'
  | 'endpoints'
  | 'handleSaveEndpoint'
  | 'handleToggleEndpoint'
  | 'handleRebootstrapEndpoint'
  | 'handleSetEndpointRemoteWeb'
  | 'handleRemoveEndpoint'
>) {
  return (
    <>
      <MobileWebSettings
        tailscaleURL={tailscaleURL}
        tailscaleDomain={tailscaleDomain}
        handleToggleTailscale={handleToggleTailscale}
        tailscaleEnabled={tailscaleEnabled}
        tailscaleStatus={tailscaleStatus}
        tailscaleAuthURL={tailscaleAuthURL}
        tailscaleError={tailscaleError}
      />

      <GitHubHostsSettings githubPollingOffReason={githubPollingOffReason} githubHosts={githubHosts} />

      <RemoteEndpointsSettings
        endpointPanel={endpointPanel}
        handleAddEndpoint={handleAddEndpoint}
        endpoints={endpoints}
        handleSaveEndpoint={handleSaveEndpoint}
        handleToggleEndpoint={handleToggleEndpoint}
        handleRebootstrapEndpoint={handleRebootstrapEndpoint}
        handleSetEndpointRemoteWeb={handleSetEndpointRemoteWeb}
        handleRemoveEndpoint={handleRemoveEndpoint}
      />
    </>
  );
}

export function PluginSettings({
  pluginPanel,
  setPluginSourcePath,
  handleBrowsePluginPath,
  handleInstallPlugin,
  pluginIssues,
  plugins,
  handleInstallBundledPlugin,
  handleUninstallPlugin,
  handleRemovePlugin,
  commitPluginPriority,
  savedFlash,
}: Pick<
  SettingsModalState,
  | 'pluginPanel'
  | 'setPluginSourcePath'
  | 'handleBrowsePluginPath'
  | 'handleInstallPlugin'
  | 'pluginIssues'
  | 'plugins'
  | 'handleInstallBundledPlugin'
  | 'handleUninstallPlugin'
  | 'handleRemovePlugin'
  | 'commitPluginPriority'
  | 'savedFlash'
>) {
  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <div className="settings-kicker">Extensions</div>
        <h3>Plugins</h3>
        <p className="settings-description">
          Install first-party bundled plugins or add user-owned plugins from a local directory or Git repository.
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-inline-form plugin-form">
          <input
            type="text"
            value={pluginPanel.sourcePath}
            onChange={(e) => setPluginSourcePath(e.target.value)}
            placeholder="git@host:team/my-attn-plugin.git or /Users/you/src/my-attn-plugin"
            className="settings-input"
            aria-label="Plugin source"
            disabled={pluginPanel.busy}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
          />
          <button className="settings-action" onClick={() => void handleBrowsePluginPath()} disabled={pluginPanel.busy}>
            Browse
          </button>
          <button className="settings-action" onClick={() => void handleInstallPlugin()} disabled={pluginPanel.busy}>
            Install Plugin
          </button>
        </div>
        {pluginPanel.error && <div className="settings-warning">{pluginPanel.error}</div>}
        {pluginIssues.map((issue) => (
          <div key={issue.path} className="settings-warning">
            {issue.path}: {issue.error}
          </div>
        ))}
        {pluginPanel.loading ? (
          <p className="settings-empty">Loading plugins...</p>
        ) : plugins.length === 0 ? (
          <p className="settings-empty">No plugins available or installed.</p>
        ) : (
          <div className="plugin-list">
            {plugins.map((plugin) => {
              const busy = pluginPanel.busyKey === plugin.name;
              const draftPriority = pluginPanel.priorityDraft(plugin.name, String(plugin.priority));
              const healthStatus = plugin.health_status || 'unknown';
              const installed = plugin.installation_state === 'installed';
              const runtimePhase =
                plugin.runtime_state ||
                plugin.runtime_phase ||
                (plugin.connected ? 'connected' : plugin.running ? 'starting' : 'stopped');
              return (
                <div key={plugin.name} className="plugin-card">
                  <div className="plugin-card-header">
                    <div className="plugin-card-title">
                      <span className="endpoint-name">{plugin.name}</span>
                      <span className="settings-pill">v{plugin.version}</span>
                      {plugin.availability === 'bundled' && <span className="settings-pill">Bundled</span>}
                      {plugin.link_target && <span className="settings-pill">Linked</span>}
                      <span className="settings-pill">{installed ? 'Installed' : 'Available'}</span>
                      <span className={`plugin-status-badge ${runtimePhase}`}>{runtimePhase}</span>
                      {installed && <span className={`plugin-health-badge ${healthStatus}`}>{healthStatus}</span>}
                    </div>
                    {plugin.availability === 'bundled' && !installed ? (
                      <button
                        className="settings-action"
                        onClick={() => void handleInstallBundledPlugin(plugin.name)}
                        disabled={pluginPanel.busy || !plugin.can_install}
                      >
                        Install
                      </button>
                    ) : plugin.availability === 'bundled' ? (
                      <button
                        className="settings-action danger"
                        onClick={() => void handleUninstallPlugin(plugin.name)}
                        disabled={pluginPanel.busy || !plugin.can_uninstall}
                      >
                        Uninstall
                      </button>
                    ) : (
                      <button
                        className="settings-action danger"
                        onClick={() => void handleRemovePlugin(plugin.name)}
                        disabled={pluginPanel.busy || !plugin.can_uninstall}
                      >
                        Remove
                      </button>
                    )}
                  </div>
                  {plugin.description && (
                    <p className="settings-description plugin-description">{plugin.description}</p>
                  )}
                  {installed && plugin.health_message && (
                    <div className="settings-warning">Healthcheck: {plugin.health_message}</div>
                  )}
                  {installed && plugin.last_exit && (
                    <div className="settings-warning">Last exit: {plugin.last_exit}</div>
                  )}
                  <div className="plugin-meta-grid">
                    <div className="settings-meta-row">
                      <span className="settings-meta-label">Path</span>
                      <code>{plugin.dir}</code>
                    </div>
                    {plugin.link_target && (
                      <div className="settings-meta-row">
                        <span className="settings-meta-label">Linked to</span>
                        <code>{plugin.link_target}</code>
                      </div>
                    )}
                    {plugin.restart_attempt !== undefined && (
                      <div className="settings-meta-row">
                        <span className="settings-meta-label">Restart attempt</span>
                        <code>{plugin.restart_attempt}</code>
                      </div>
                    )}
                    {plugin.next_restart_at && (
                      <div className="settings-meta-row">
                        <span className="settings-meta-label">Next restart</span>
                        <code>{plugin.next_restart_at}</code>
                      </div>
                    )}
                    {installed && (
                      <label className="plugin-priority-control">
                        <span className="settings-meta-label">Priority</span>
                        <input
                          type="number"
                          value={draftPriority}
                          onChange={(e) => pluginPanel.setPriorityDraft(plugin.name, e.target.value)}
                          onBlur={() => void commitPluginPriority(plugin.name, plugin.priority)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') {
                              void commitPluginPriority(plugin.name, plugin.priority);
                            }
                          }}
                          className="settings-input plugin-priority-input"
                          aria-label={`${plugin.name} priority`}
                          disabled={pluginPanel.busy}
                        />
                        <SavedMark
                          shown={savedFlash.saved(`plugin_priority_${plugin.name}`)}
                          testID={`settings-plugin-priority-saved-${plugin.name}`}
                        />
                      </label>
                    )}
                  </div>
                  {busy && <div className="settings-hint">Updating {plugin.name}...</div>}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
}

export function WorkflowsSettings({
  handleToggleWorkflows,
  workflowsEnabled,
}: Pick<SettingsModalState, 'handleToggleWorkflows' | 'workflowsEnabled'>) {
  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <p className="settings-description">
          Off by default. When on, agents learn how and when to use workflows and only start one when you opt in per
          task ("attn workflow") or for the session ("hypercode").
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-row-card">
          <div>
            <p className="settings-row-title">Enable workflows</p>
            <p className="settings-row-copy">
              While off, "attn workflow run" is refused and agents aren't told about workflows. Turning it off won't
              interrupt a run already in flight.
            </p>
          </div>
          <button
            type="button"
            className="settings-action"
            data-testid="settings-workflows-toggle"
            onClick={handleToggleWorkflows}
          >
            {workflowsEnabled ? 'Disable' : 'Enable'}
          </button>
        </div>
      </div>
    </section>
  );
}

export function TerminalSettings({
  ptyBackendHint,
  ptyBackendMode,
  ptyBackendLabel,
  sharedPtyHostActive,
  sharedPtyHostEnabled,
  onSetSetting,
}: Pick<
  SettingsModalState,
  | 'ptyBackendHint'
  | 'ptyBackendMode'
  | 'ptyBackendLabel'
  | 'sharedPtyHostActive'
  | 'sharedPtyHostEnabled'
  | 'onSetSetting'
>) {
  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <div className="settings-kicker">Terminal</div>
        <h3>PTY Backend</h3>
        <p className="settings-description">
          Shows whether terminal sessions run in external worker processes or directly in the daemon.
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-row-card compact">
          <div>
            <p className="settings-row-title">Runtime mode</p>
            <p className="settings-row-copy">{ptyBackendHint}</p>
          </div>
          <span className={`settings-status mode-${ptyBackendMode}`}>{ptyBackendLabel}</span>
        </div>
        <div className="settings-row-card">
          <div>
            <p className="settings-row-title">Shared PTY host (experimental)</p>
            <p className="settings-row-copy" id="shared-pty-host-description">
              Off by default. Use a shared Rust process for new terminals and agents to reduce memory use. Changes apply
              to new or explicitly reloaded sessions. Running sessions stay untouched.
            </p>
            <p className="settings-row-copy" data-testid="settings-shared-pty-host-status">
              {ptyBackendMode !== 'migrating'
                ? 'This setting requires the default daemon backend.'
                : sharedPtyHostActive
                  ? 'New sessions use the shared Rust host.'
                  : sharedPtyHostEnabled
                    ? 'Shared host unavailable. New sessions use dedicated Go workers; disable and re-enable to retry.'
                    : 'New sessions use dedicated Go workers.'}
            </p>
          </div>
          <button
            type="button"
            role="switch"
            aria-label="Shared PTY host (experimental)"
            aria-checked={sharedPtyHostEnabled}
            aria-describedby="shared-pty-host-description"
            className="settings-action"
            data-testid="settings-shared-pty-host-toggle"
            disabled={ptyBackendMode !== 'migrating'}
            onClick={() => onSetSetting('pty_shared_host_enabled', sharedPtyHostEnabled ? 'false' : 'true')}
          >
            {sharedPtyHostEnabled ? 'Disable' : 'Enable'}
          </button>
        </div>
      </div>
    </section>
  );
}

export function BackgroundAgentSettings({
  settings,
  activityAgents,
  onSetSetting,
  gardenAdvisorAgents,
  chiefOverrideAgentList,
  chiefModelDrafts,
  chiefEffortDrafts,
  savedFlash,
  chiefContextCapDraft,
  headlessContextCapDraft,
}: Pick<
  SettingsModalState,
  | 'settings'
  | 'activityAgents'
  | 'onSetSetting'
  | 'gardenAdvisorAgents'
  | 'chiefOverrideAgentList'
  | 'chiefModelDrafts'
  | 'chiefEffortDrafts'
  | 'savedFlash'
  | 'chiefContextCapDraft'
  | 'headlessContextCapDraft'
>) {
  return (
    <>
      <SessionActivitySettings settings={settings} agents={activityAgents} onSetSetting={onSetSetting} />
      <GardenAdvisorSettings settings={settings} agents={gardenAdvisorAgents} onSetSetting={onSetSetting} />
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Agents</div>
          <h3>Chief of staff</h3>
          <p className="settings-description">
            Model and effort for Chief launches. Empty values use the agent's default.
          </p>
        </div>
        <div className="settings-block-body">
          {chiefOverrideAgentList.length === 0 ? (
            <div className="settings-warning">No installed agent supports a model or effort override.</div>
          ) : (
            <div className="settings-field-grid two-column">
              {chiefOverrideAgentList.map((agent) => {
                const inputId = `settings-chief-model-${agent}`;
                const effortId = `settings-chief-effort-${agent}`;
                const value = chiefModelDrafts.value(agent);
                const effortValue = chiefEffortDrafts.value(agent);
                const effortLevels = CHIEF_EFFORT_LEVELS[agent] || [];
                return (
                  <Fragment key={agent}>
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={inputId}>
                        {agentLabel(agent)}
                      </label>
                      <input
                        id={inputId}
                        data-testid={inputId}
                        type="text"
                        value={value}
                        onChange={(e) => chiefModelDrafts.set(agent, e.target.value)}
                        onBlur={() => chiefModelDrafts.commit(agent)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            chiefModelDrafts.commit(agent);
                          }
                        }}
                        placeholder="Agent default"
                        className="settings-input"
                        autoCapitalize="none"
                        autoCorrect="off"
                        spellCheck={false}
                      />
                      <SavedMark
                        shown={savedFlash.saved(`chief_model_${agent}`)}
                        testID={`settings-chief-model-saved-${agent}`}
                      />
                    </div>
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={effortId}>
                        {agentLabel(agent)} effort
                      </label>
                      <select
                        id={effortId}
                        data-testid={effortId}
                        className="settings-input"
                        value={effortValue}
                        onChange={(e) => chiefEffortDrafts.apply(agent, e.target.value)}
                      >
                        <option value="">Agent default</option>
                        {effortLevels.map((level) => (
                          <option key={level} value={level}>
                            {level}
                          </option>
                        ))}
                      </select>
                    </div>
                  </Fragment>
                );
              })}
            </div>
          )}
        </div>
      </section>
      <section className="settings-block">
        <div className="settings-block-intro">
          <h3>Compaction</h3>
          <p className="settings-description">
            Token thresholds for the Chief and background runs. Empty values use the default of{' '}
            {DEFAULT_CONTEXT_WINDOW_CAP.toLocaleString()} tokens.
          </p>
        </div>
        <div className="settings-block-body settings-field-grid">
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-chief-context-cap">
              Chief of staff
            </label>
            <input
              id="settings-chief-context-cap"
              data-testid="settings-chief-context-cap"
              type="number"
              min={10000}
              max={2000000}
              step={1000}
              value={chiefContextCapDraft.value}
              onChange={chiefContextCapDraft.onChange}
              onBlur={chiefContextCapDraft.commit}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  chiefContextCapDraft.commit();
                }
              }}
              className="settings-input"
            />
            <SavedMark shown={savedFlash.saved('chief_context_window_cap')} testID="settings-chief-context-cap-saved" />
          </div>
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-headless-context-cap">
              Headless runs
            </label>
            <input
              id="settings-headless-context-cap"
              data-testid="settings-headless-context-cap"
              type="number"
              min={10000}
              max={2000000}
              step={1000}
              value={headlessContextCapDraft.value}
              onChange={headlessContextCapDraft.onChange}
              onBlur={headlessContextCapDraft.commit}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  headlessContextCapDraft.commit();
                }
              }}
              className="settings-input"
            />
            <SavedMark
              shown={savedFlash.saved('headless_context_window_cap')}
              testID="settings-headless-context-cap-saved"
            />
          </div>
        </div>
      </section>
    </>
  );
}

export function AgentSettings({
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
  settings,
  defaultModelDrafts,
  defaultEffortDrafts,
  executableAgentList,
  executableDrafts,
  defaultContextCapDrafts,
  onSetSetting,
}: Pick<
  SettingsModalState,
  | 'hasAvailableAgents'
  | 'orderedAgentList'
  | 'agentAvailability'
  | 'defaultAgent'
  | 'handleDefaultAgentChange'
  | 'autoApproveEnabled'
  | 'handleToggleAutoApprove'
  | 'defaultOverrideAgentList'
  | 'actualAgentCapabilities'
  | 'agentCapabilityOrder'
  | 'resolvedDefaultAgent'
  | 'settings'
  | 'defaultModelDrafts'
  | 'defaultEffortDrafts'
  | 'executableAgentList'
  | 'executableDrafts'
  | 'defaultContextCapDrafts'
  | 'onSetSetting'
>) {
  const overrideAgents = new Set(defaultOverrideAgentList);
  const capabilityOrder = new Set(agentCapabilityOrder);
  const executableAgents = new Set(executableAgentList);
  return (
    <>
      <section className="settings-block settings-session-defaults">
        <div className="settings-block-intro">
          <h3>Session defaults</h3>
        </div>
        {!hasAvailableAgents && <div className="settings-warning">No supported agent CLI found in PATH.</div>}
        <div className="settings-default-row">
          <div>
            <p className="settings-row-title">Default agent</p>
            <p className="settings-row-copy">Used for new sessions and opening PRs.</p>
          </div>
          <div className="settings-segmented" role="group" aria-label="Default session agent">
            {orderedAgentList.map((agent) => {
              const available = isAgentAvailable(agentAvailability, agent);
              return (
                <button
                  key={agent}
                  type="button"
                  className={`settings-segmented-option ${defaultAgent === agent ? 'active' : ''}`}
                  onClick={() => handleDefaultAgentChange(agent)}
                  aria-pressed={defaultAgent === agent}
                  disabled={!available}
                  title={!available ? `${agentLabel(agent)} CLI not found in PATH` : undefined}
                >
                  {agentLabel(agent)}
                </button>
              );
            })}
          </div>
        </div>
        <div className="settings-default-row">
          <div>
            <p className="settings-row-title">Auto-approve</p>
            <p className="settings-row-copy">
              Let agents review approval requests automatically. Applies to new sessions; YOLO sessions already bypass
              approval.
            </p>
          </div>
          <button
            type="button"
            role="switch"
            aria-label="Auto-approve"
            aria-checked={autoApproveEnabled}
            className="settings-action"
            data-testid="settings-auto-approve-toggle"
            onClick={handleToggleAutoApprove}
          >
            {autoApproveEnabled ? 'Disable' : 'Enable'}
          </button>
        </div>
      </section>
      <div className="settings-agent-list">
        {orderedAgentList.map((agent) => {
          const available = isAgentAvailable(agentAvailability, agent);
          const overrides = overrideAgents.has(agent);
          const caps = actualAgentCapabilities[agent] || {};
          const capabilityKeys = [
            ...agentCapabilityOrder.filter((cap) => cap in caps),
            ...Object.keys(caps)
              .filter((cap) => !capabilityOrder.has(cap))
              .sort(),
          ];
          return (
            <details className="settings-agent" key={agent} open={agent === resolvedDefaultAgent ? true : undefined}>
              <summary>
                <span>{agentLabel(agent)}</span>
                <span className="settings-agent-summary">
                  {settings[`default_model_${agent}`] || 'Agent default'} · {available ? 'Available' : 'Unavailable'}
                </span>
              </summary>
              <div className="settings-agent-content">
                {overrides && (
                  <div className="settings-field-grid two-column">
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={`settings-default-model-${agent}`}>
                        Default model
                      </label>
                      <input
                        id={`settings-default-model-${agent}`}
                        data-testid={`settings-default-model-${agent}`}
                        className="settings-input"
                        value={defaultModelDrafts.value(agent)}
                        placeholder="Agent default"
                        autoCapitalize="none"
                        autoCorrect="off"
                        spellCheck={false}
                        onChange={(e) => defaultModelDrafts.set(agent, e.target.value)}
                        onBlur={() => void defaultModelDrafts.commit(agent)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') void defaultModelDrafts.commit(agent);
                        }}
                      />
                    </div>
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={`settings-default-effort-${agent}`}>
                        Reasoning effort
                      </label>
                      <select
                        id={`settings-default-effort-${agent}`}
                        data-testid={`settings-default-effort-${agent}`}
                        className="settings-input"
                        value={defaultEffortDrafts.value(agent)}
                        onChange={(e) => defaultEffortDrafts.apply(agent, e.target.value)}
                      >
                        <option value="">Agent default</option>
                        {(CHIEF_EFFORT_LEVELS[agent] || []).map((level) => (
                          <option key={level} value={level}>
                            {level}
                          </option>
                        ))}
                      </select>
                    </div>
                  </div>
                )}
                {!overrides && (
                  <p className="settings-description">Model and effort follow this agent's own configuration.</p>
                )}
                <details className="settings-agent-advanced">
                  <summary>Advanced</summary>
                  <div className="settings-field-grid two-column">
                    {executableAgents.has(agent) && (
                      <div className="settings-field">
                        <label className="settings-label" htmlFor={`settings-${agent}-exec`}>
                          Executable
                        </label>
                        <input
                          id={`settings-${agent}-exec`}
                          className="settings-input"
                          value={executableDrafts.value(agent)}
                          placeholder={agent}
                          autoCapitalize="none"
                          autoCorrect="off"
                          spellCheck={false}
                          onChange={(e) => executableDrafts.set(agent, e.target.value)}
                          onBlur={() => void executableDrafts.commit(agent)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') void executableDrafts.commit(agent);
                          }}
                        />
                        <span className="settings-hint">Empty uses the executable on PATH.</span>
                      </div>
                    )}
                    {overrides && (
                      <div className="settings-field">
                        <label className="settings-label" htmlFor={`settings-default-context-cap-${agent}`}>
                          Context cap (tokens)
                        </label>
                        <input
                          id={`settings-default-context-cap-${agent}`}
                          data-testid={`settings-default-context-cap-${agent}`}
                          className="settings-input"
                          type="number"
                          min={10000}
                          max={2000000}
                          step={1000}
                          placeholder="Agent default"
                          value={defaultContextCapDrafts.value(agent)}
                          onChange={(e) => defaultContextCapDrafts.set(agent, e.target.value)}
                          onBlur={() => void defaultContextCapDrafts.commit(agent)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') void defaultContextCapDrafts.commit(agent);
                          }}
                        />
                        <span className="settings-hint">Empty uses the agent's own compaction behavior.</span>
                      </div>
                    )}
                  </div>
                  <dl className="settings-agent-capabilities">
                    {capabilityKeys.map((cap) => (
                      <div key={cap}>
                        <dt>{agentCapabilityLabel(cap)}</dt>
                        <dd>{caps[cap] ? 'Supported' : 'Unavailable'}</dd>
                      </div>
                    ))}
                  </dl>
                </details>
              </div>
            </details>
          );
        })}
      </div>
      <details className="settings-agent-advanced settings-pricing">
        <summary>Model pricing overrides</summary>
        <SessionCostPriceSettings settings={settings} onSetSetting={onSetSetting} />
      </details>
    </>
  );
}

export function DataSettings({
  handleToggleModelCapture,
  modelCaptureEnabled,
  modelCaptureInterval,
  onSetSetting,
  modelCaptureMaxGB,
  modelCaptureBytes,
  modelCapturePath,
}: Pick<
  SettingsModalState,
  | 'handleToggleModelCapture'
  | 'modelCaptureEnabled'
  | 'modelCaptureInterval'
  | 'onSetSetting'
  | 'modelCaptureMaxGB'
  | 'modelCaptureBytes'
  | 'modelCapturePath'
>) {
  return (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Local models</div>
          <h3>Terminal viewport capture</h3>
          <p className="settings-description">
            Builds a local corpus for evaluating and training models that understand Codex and Claude terminal screens.
            Collection is off by default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-warning">
            Captures exact visible terminal text. Records may contain source code, conversations, command output, and
            secrets. Files stay in this attn profile and are never uploaded automatically.
          </div>
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Capture local Codex and Claude sessions</p>
              <p className="settings-row-copy">
                Applies to sessions already running and sessions launched later. Disabling collection stops new records
                but keeps the corpus already on disk.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-model-capture-toggle"
              onClick={handleToggleModelCapture}
            >
              {modelCaptureEnabled ? 'Stop capture' : 'Enable capture'}
            </button>
          </div>
          <div className="settings-field-grid">
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-model-capture-interval">
                Changed-frame interval
              </label>
              <select
                id="settings-model-capture-interval"
                data-testid="settings-model-capture-interval"
                className="settings-input"
                value={modelCaptureInterval}
                onChange={(event) => onSetSetting('model_capture.interval_seconds', event.target.value)}
              >
                {!MODEL_CAPTURE_INTERVAL_OPTIONS.includes(Number(modelCaptureInterval)) && (
                  <option value={modelCaptureInterval}>{modelCaptureInterval} seconds</option>
                )}
                {MODEL_CAPTURE_INTERVAL_OPTIONS.map((seconds) => (
                  <option key={seconds} value={seconds}>
                    {seconds} seconds
                  </option>
                ))}
              </select>
              <span className="settings-hint">
                State changes are captured immediately; unchanged viewports are deduplicated.
              </span>
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-model-capture-max-gb">
                Storage cap
              </label>
              <select
                id="settings-model-capture-max-gb"
                data-testid="settings-model-capture-max-gb"
                className="settings-input"
                value={modelCaptureMaxGB}
                onChange={(event) => onSetSetting('model_capture.max_gb', event.target.value)}
              >
                {!MODEL_CAPTURE_MAX_GB_OPTIONS.includes(Number(modelCaptureMaxGB)) && (
                  <option value={modelCaptureMaxGB}>{modelCaptureMaxGB} GB</option>
                )}
                {MODEL_CAPTURE_MAX_GB_OPTIONS.map((gb) => (
                  <option key={gb} value={gb}>
                    {gb} GB
                  </option>
                ))}
              </select>
              <span className="settings-hint">Oldest hourly files are removed first.</span>
            </div>
          </div>
          <div className="settings-meta-row">
            <span className="settings-meta-label">Captured</span>
            <code data-testid="settings-model-capture-size">{modelCaptureBytes}</code>
          </div>
          <div className="settings-meta-row">
            <span className="settings-meta-label">Folder</span>
            <code data-testid="settings-model-capture-path">{modelCapturePath || 'Waiting for daemon settings…'}</code>
          </div>
        </div>
      </section>
    </>
  );
}

export function HygieneSettings({
  handleToggleAutoSettle,
  autoSettleEnabled,
  autoSettleArmDraft,
  savedFlash,
  autoSettleCountdownDraft,
  mutedRepos,
  onUnmuteRepo,
  mutedAuthors,
  onUnmuteAuthor,
}: Pick<
  SettingsModalState,
  | 'handleToggleAutoSettle'
  | 'autoSettleEnabled'
  | 'autoSettleArmDraft'
  | 'savedFlash'
  | 'autoSettleCountdownDraft'
  | 'mutedRepos'
  | 'onUnmuteRepo'
  | 'mutedAuthors'
  | 'onUnmuteAuthor'
>) {
  return (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Sidebar</div>
          <h3>Auto-settle</h3>
          <p className="settings-description">
            Closes a turn for you once you have steered the agent and walked away, so the queue does not fill up with
            turns you have already dealt with. Off by default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Settle a turn once you have steered the agent</p>
              <p className="settings-row-copy">
                When an agent you owe a turn goes back to work and stays there, its terminal tile runs a countdown and
                then settles the turn for you — the same thing {formatShortcut('session.settle')}
                does. Press {formatShortcut('session.cancelCountdown')} to keep the turn instead. Anything that makes
                the agent want you again — a question, an approval, an error, a finished run — cancels it. Off by
                default.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-auto-settle-toggle"
              onClick={handleToggleAutoSettle}
            >
              {autoSettleEnabled ? 'Disable' : 'Enable'}
            </button>
          </div>

          <div className="settings-field-grid">
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-auto-settle-arm">
                Wait before counting down (seconds)
              </label>
              <input
                id="settings-auto-settle-arm"
                data-testid="settings-auto-settle-arm"
                type="number"
                min={5}
                max={3600}
                step={5}
                value={autoSettleArmDraft.value}
                onChange={autoSettleArmDraft.onChange}
                onBlur={autoSettleArmDraft.commit}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    autoSettleArmDraft.commit();
                  }
                }}
                className="settings-input"
              />
              <SavedMark shown={savedFlash.saved(AUTO_SETTLE_ARM_SETTING)} testID="settings-auto-settle-arm-saved" />
              <p className="settings-hint">
                How long the agent must keep working before anything starts. Nothing is shown during this window.
              </p>
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-auto-settle-countdown">
                Countdown before settling (seconds)
              </label>
              <input
                id="settings-auto-settle-countdown"
                data-testid="settings-auto-settle-countdown"
                type="number"
                min={3}
                max={600}
                step={1}
                value={autoSettleCountdownDraft.value}
                onChange={autoSettleCountdownDraft.onChange}
                onBlur={autoSettleCountdownDraft.commit}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    autoSettleCountdownDraft.commit();
                  }
                }}
                className="settings-input"
              />
              <SavedMark
                shown={savedFlash.saved(AUTO_SETTLE_COUNTDOWN_SETTING)}
                testID="settings-auto-settle-countdown-saved"
              />
              <p className="settings-hint">
                How long the countdown runs on the tile — your window to press{' '}
                {formatShortcut('session.cancelCountdown')} and keep the turn.
              </p>
            </div>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Repositories</div>
          <h3>Muted Repositories</h3>
          <p className="settings-description">Repositories hidden from the attention queue.</p>
        </div>
        <div className="settings-block-body">
          {mutedRepos.length === 0 ? (
            <p className="settings-empty">No muted repositories</p>
          ) : (
            <ul className="muted-items-list" data-testid="settings-muted-repositories-list">
              {mutedRepos.map((repo) => (
                <li key={repo} className="muted-item" data-testid="settings-muted-repository-item">
                  <span className="muted-item-name">{repo}</span>
                  <button
                    className="settings-action"
                    data-testid="settings-unmute-repository-button"
                    onClick={() => onUnmuteRepo(repo)}
                  >
                    Unmute
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Authors</div>
          <h3>Muted Authors</h3>
          <p className="settings-description">Authors hidden from the attention queue.</p>
        </div>
        <div className="settings-block-body">
          {mutedAuthors.length === 0 ? (
            <p className="settings-empty">No muted authors</p>
          ) : (
            <ul className="muted-items-list" data-testid="settings-muted-authors-list">
              {mutedAuthors.map((author) => (
                <li key={author} className="muted-item" data-testid="settings-muted-author-item">
                  <span className="muted-item-name">{author}</span>
                  <button
                    className="settings-action"
                    data-testid="settings-unmute-author-button"
                    onClick={() => onUnmuteAuthor(author)}
                  >
                    Unmute
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </>
  );
}

export function BackgroundTasksSection({
  listTasks,
  retryTask,
  taskChangeSignal,
}: Pick<SettingsModalState, 'listTasks' | 'retryTask' | 'taskChangeSignal'>) {
  return (
    <>
      {listTasks && retryTask ? (
        <BackgroundTasksSettings listTasks={listTasks} retryTask={retryTask} taskChangeSignal={taskChangeSignal ?? 0} />
      ) : (
        <section className="settings-block">
          <div className="settings-block-intro">
            <div className="settings-kicker">Background Tasks</div>
            <h3>Durable task runner</h3>
            <p className="settings-description">Task runner is unavailable.</p>
          </div>
        </section>
      )}
    </>
  );
}

export function EventBusSection({
  sendBusStatusGet,
  sendBusSetConsumerEnabled,
}: Pick<SettingsModalState, 'sendBusStatusGet' | 'sendBusSetConsumerEnabled'>) {
  return <EventBusSettings getBusStatus={sendBusStatusGet} setConsumerEnabled={sendBusSetConsumerEnabled} />;
}

export function MobileWebSettings({
  tailscaleURL,
  tailscaleDomain,
  handleToggleTailscale,
  tailscaleEnabled,
  tailscaleStatus,
  tailscaleAuthURL,
  tailscaleError,
}: Pick<
  SettingsModalState,
  | 'tailscaleURL'
  | 'tailscaleDomain'
  | 'handleToggleTailscale'
  | 'tailscaleEnabled'
  | 'tailscaleStatus'
  | 'tailscaleAuthURL'
  | 'tailscaleError'
>) {
  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <div className="settings-kicker">Mobile</div>
        <h3>Mobile Web Client</h3>
        <p className="settings-description">
          Expose this daemon through the existing Tailscale device identity for mobile browser access.
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-row-card">
          <div>
            <p className="settings-row-title">Tailscale Serve</p>
            <p className="settings-row-copy">
              {tailscaleURL ||
                tailscaleDomain ||
                'Uses the host Tailscale client and does not register a second tailnet device for attn.'}
            </p>
          </div>
          <button className="settings-action" onClick={handleToggleTailscale}>
            {tailscaleEnabled ? 'Disable' : 'Enable'}
          </button>
        </div>
        <div className="settings-row-card compact">
          <div>
            <p className="settings-row-title">Sign-in status</p>
            <p className="settings-row-copy">Status: {tailscaleStatus}</p>
          </div>
          <span className={`settings-pill ${tailscaleEnabled && tailscaleStatus !== 'error' ? 'good' : ''}`}>
            {tailscaleEnabled ? tailscaleStatus : 'disabled'}
          </span>
        </div>
        <div className="settings-hint">
          This uses the host Tailscale client and does not register a second tailnet device for attn.
        </div>
        {tailscaleDomain && (
          <div className="settings-meta-row">
            <span className="settings-meta-label">Device DNS</span>
            <code>{tailscaleDomain}</code>
          </div>
        )}
        {tailscaleURL && (
          <div className="settings-meta-row">
            <span className="settings-meta-label">Web URL</span>
            <a href={tailscaleURL} target="_blank" rel="noreferrer">
              {tailscaleURL}
            </a>
          </div>
        )}
        {tailscaleAuthURL && (
          <div className="settings-warning">
            Sign this machine into Tailscale:{' '}
            <a href={tailscaleAuthURL} target="_blank" rel="noreferrer">
              {tailscaleAuthURL}
            </a>
          </div>
        )}
        {tailscaleError && <div className="settings-warning">{tailscaleError}</div>}
      </div>
    </section>
  );
}

export function GitHubHostsSettings({
  githubPollingOffReason,
  githubHosts,
}: Pick<SettingsModalState, 'githubPollingOffReason' | 'githubHosts'>) {
  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <div className="settings-kicker">GitHub</div>
        <h3>GitHub Hosts</h3>
        <p className="settings-description">
          Authenticated hosts used by PR actions, review lookup, and repository metadata.
        </p>
      </div>
      <div className="settings-block-body">
        {githubPollingOffReason ? (
          <p className="settings-empty" data-testid="github-polling-off">
            {githubPollingOffReason}
          </p>
        ) : githubHosts.length === 0 ? (
          <p className="settings-empty">No authenticated hosts detected.</p>
        ) : (
          <div className="settings-token-list">
            {githubHosts.map((host) => (
              <span key={host} className="settings-token">
                {host}
              </span>
            ))}
          </div>
        )}
        {!githubPollingOffReason && (
          <div className="settings-hint">Add hosts with `gh auth login --hostname &lt;host&gt;`.</div>
        )}
      </div>
    </section>
  );
}

export function RemoteEndpointsSettings({
  endpointPanel,
  handleAddEndpoint,
  endpoints,
  handleSaveEndpoint,
  handleToggleEndpoint,
  handleRebootstrapEndpoint,
  handleSetEndpointRemoteWeb,
  handleRemoveEndpoint,
}: Pick<
  SettingsModalState,
  | 'endpointPanel'
  | 'handleAddEndpoint'
  | 'endpoints'
  | 'handleSaveEndpoint'
  | 'handleToggleEndpoint'
  | 'handleRebootstrapEndpoint'
  | 'handleSetEndpointRemoteWeb'
  | 'handleRemoveEndpoint'
>) {
  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <div className="settings-kicker">Remote</div>
        <h3>Remote Endpoints</h3>
        <p className="settings-description">
          SSH targets that the local daemon bootstraps and keeps connected as remote attn peers.
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-form-grid endpoint-form">
          <input
            type="text"
            value={endpointPanel.draft.name}
            onChange={(e) => endpointPanel.setDraft('name', e.target.value)}
            placeholder="gpu-box"
            className="settings-input"
            aria-label="Endpoint name"
            disabled={endpointPanel.busy}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
          />
          <input
            type="text"
            value={endpointPanel.draft.target}
            onChange={(e) => endpointPanel.setDraft('target', e.target.value)}
            placeholder="user@gpu-box"
            className="settings-input"
            aria-label="SSH target"
            disabled={endpointPanel.busy}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
          />
          <input
            type="text"
            value={endpointPanel.draft.profile}
            onChange={(e) => endpointPanel.setDraft('profile', e.target.value)}
            placeholder="default"
            pattern="[a-z0-9][a-z0-9-]{0,15}"
            className="settings-input"
            aria-label="Profile"
            disabled={endpointPanel.busy}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
          />
          <button className="settings-action" onClick={() => void handleAddEndpoint()} disabled={endpointPanel.busy}>
            Add Endpoint
          </button>
        </div>
        {endpointPanel.error && <div className="settings-warning">{endpointPanel.error}</div>}
        {endpoints.length === 0 ? (
          <p className="settings-empty">No remote endpoints configured.</p>
        ) : (
          <div className="endpoint-list">
            {endpoints.map((endpoint) => {
              const isEditing = endpointPanel.editing?.id === endpoint.id;
              const isBusy = endpointPanel.busyKey === endpoint.id;
              const availableAgents = endpoint.capabilities?.agents_available || [];
              const remoteWebEnabled = endpoint.capabilities?.tailscale_enabled === true;
              const remoteWebStatus =
                endpoint.capabilities?.tailscale_status || (remoteWebEnabled ? 'starting' : 'disabled');
              const remoteWebURL = endpoint.capabilities?.tailscale_url;
              const remoteWebAuthURL = endpoint.capabilities?.tailscale_auth_url;
              const remoteWebError = endpoint.capabilities?.tailscale_error;
              const canToggleRemoteWeb = endpoint.status === 'connected' && !endpointPanel.busy;
              const canRebootstrap = endpoint.enabled !== false && !endpointPanel.busy;
              return (
                <div key={endpoint.id} className={`endpoint-card status-${endpoint.status}`}>
                  <div className="endpoint-card-header">
                    <div className="endpoint-card-title">
                      <span className="endpoint-name">{endpoint.name}</span>
                      <span className="settings-pill">{endpoint.profile || 'default'}</span>
                      <span className={`endpoint-status-badge status-${endpoint.status}`}>{endpoint.status}</span>
                    </div>
                    <div className="endpoint-card-actions">
                      {isEditing ? (
                        <>
                          <button
                            className="settings-action"
                            onClick={() => void handleSaveEndpoint(endpoint.id)}
                            disabled={isBusy}
                          >
                            Save
                          </button>
                          <button
                            className="settings-action"
                            onClick={endpointPanel.cancelEdit}
                            disabled={endpointPanel.busy}
                          >
                            Cancel
                          </button>
                        </>
                      ) : (
                        <button
                          className="settings-action"
                          onClick={() => endpointPanel.beginEdit(endpoint)}
                          disabled={endpointPanel.busy}
                        >
                          Edit
                        </button>
                      )}
                      <button
                        className="settings-action"
                        onClick={() => void handleToggleEndpoint(endpoint)}
                        disabled={endpointPanel.busy}
                      >
                        {endpoint.enabled === false ? 'Enable' : 'Disable'}
                      </button>
                      <button
                        className="settings-action"
                        onClick={() => void handleRebootstrapEndpoint(endpoint)}
                        disabled={!canRebootstrap}
                      >
                        Re-bootstrap
                      </button>
                      <button
                        className="settings-action"
                        onClick={() => void handleSetEndpointRemoteWeb(endpoint.id, !remoteWebEnabled)}
                        disabled={!canToggleRemoteWeb}
                      >
                        {remoteWebEnabled ? 'Disable Web' : 'Enable Web'}
                      </button>
                      <button
                        className="settings-action danger"
                        onClick={() => void handleRemoveEndpoint(endpoint.id)}
                        disabled={endpointPanel.busy}
                      >
                        Remove
                      </button>
                    </div>
                  </div>
                  {isEditing ? (
                    <div className="settings-form-grid endpoint-form-inline">
                      <input
                        type="text"
                        value={endpointPanel.editing?.name ?? ''}
                        onChange={(e) => endpointPanel.setEdit('name', e.target.value)}
                        className="settings-input"
                        aria-label="Edit endpoint name"
                        disabled={endpointPanel.busy}
                        autoCapitalize="none"
                        autoCorrect="off"
                        spellCheck={false}
                      />
                      <input
                        type="text"
                        value={endpointPanel.editing?.target ?? ''}
                        onChange={(e) => endpointPanel.setEdit('target', e.target.value)}
                        className="settings-input"
                        aria-label="Edit SSH target"
                        disabled={endpointPanel.busy}
                        autoCapitalize="none"
                        autoCorrect="off"
                        spellCheck={false}
                      />
                      <input
                        type="text"
                        value={endpointPanel.editing?.profile ?? ''}
                        onChange={(e) => endpointPanel.setEdit('profile', e.target.value)}
                        className="settings-input"
                        aria-label="Edit profile"
                        placeholder="default"
                        pattern="[a-z0-9][a-z0-9-]{0,15}"
                        disabled={endpointPanel.busy}
                        autoCapitalize="none"
                        autoCorrect="off"
                        spellCheck={false}
                      />
                    </div>
                  ) : (
                    <div className="endpoint-summary">
                      <div className="settings-meta-row">
                        <span className="settings-meta-label">SSH</span>
                        <code>{endpoint.ssh_target}</code>
                      </div>
                      <div className="settings-meta-row">
                        <span className="settings-meta-label">Enabled</span>
                        <span>{endpoint.enabled === false ? 'No' : 'Yes'}</span>
                      </div>
                      {endpoint.status_message && (
                        <div className="settings-meta-row">
                          <span className="settings-meta-label">Status</span>
                          <span>{endpoint.status_message}</span>
                        </div>
                      )}
                      {endpoint.capabilities && (
                        <>
                          <div className="settings-meta-row">
                            <span className="settings-meta-label">Protocol</span>
                            <span>{endpoint.capabilities.protocol_version}</span>
                          </div>
                          <div className="settings-meta-row">
                            <span className="settings-meta-label">PTY</span>
                            <span>{endpoint.capabilities.pty_backend_mode || 'unknown'}</span>
                          </div>
                          <div className="settings-meta-row">
                            <span className="settings-meta-label">Sessions</span>
                            <span>{endpoint.session_count ?? 0}</span>
                          </div>
                          <div className="settings-meta-row">
                            <span className="settings-meta-label">Remote Web</span>
                            <span>{remoteWebStatus}</span>
                          </div>
                          <div className="settings-meta-row">
                            <span className="settings-meta-label">Agents</span>
                            <span>{availableAgents.length > 0 ? availableAgents.join(', ') : 'none reported'}</span>
                          </div>
                          {remoteWebURL && (
                            <div className="settings-meta-row">
                              <span className="settings-meta-label">Remote URL</span>
                              <a href={remoteWebURL} target="_blank" rel="noreferrer">
                                {remoteWebURL}
                              </a>
                            </div>
                          )}
                          {remoteWebAuthURL && (
                            <div className="settings-warning">
                              Sign this host into Tailscale:{' '}
                              <a href={remoteWebAuthURL} target="_blank" rel="noreferrer">
                                {remoteWebAuthURL}
                              </a>
                            </div>
                          )}
                          {endpoint.capabilities.tailscale_domain && !remoteWebURL && (
                            <div className="settings-meta-row">
                              <span className="settings-meta-label">Remote DNS</span>
                              <code>{endpoint.capabilities.tailscale_domain}</code>
                            </div>
                          )}
                          {remoteWebError && <div className="settings-warning">{remoteWebError}</div>}
                          {endpoint.capabilities.projects_directory && (
                            <div className="settings-meta-row">
                              <span className="settings-meta-label">Projects</span>
                              <code>{endpoint.capabilities.projects_directory}</code>
                            </div>
                          )}
                        </>
                      )}
                      {!canToggleRemoteWeb && (
                        <div className="settings-hint">
                          Connect to the remote daemon before changing its web access.
                        </div>
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
}

export function ConnectivityStatusPills({
  tailscaleEnabled,
  tailscaleStatus,
  endpoints,
  connectedEndpointCount,
}: Pick<SettingsModalState, 'tailscaleEnabled' | 'tailscaleStatus' | 'endpoints' | 'connectedEndpointCount'>) {
  return (
    <>
      <span className={`settings-pill ${tailscaleEnabled && tailscaleStatus !== 'error' ? 'good' : ''}`}>
        {tailscaleEnabled ? tailscaleStatus : 'mobile off'}
      </span>
      <span
        className={`settings-pill ${endpoints.length === 0 || connectedEndpointCount === endpoints.length ? 'good' : 'warn'}`}
      >
        {connectedEndpointCount}/{endpoints.length} remotes
      </span>
    </>
  );
}

export function PluginsStatusPills({
  pluginProblemCount,
  activePluginCount,
  plugins,
}: Pick<SettingsModalState, 'pluginProblemCount' | 'activePluginCount' | 'plugins'>) {
  return (
    <>
      <span className={`settings-pill ${pluginProblemCount === 0 ? 'good' : 'bad'}`}>
        {pluginProblemCount === 0 ? 'healthy' : `${pluginProblemCount} issue${pluginProblemCount === 1 ? '' : 's'}`}
      </span>
      <span className="settings-pill">
        {activePluginCount}/{plugins.length} running
      </span>
    </>
  );
}

export function DataStatusPills({
  modelCaptureEnabled,
  modelCaptureBytes,
}: Pick<SettingsModalState, 'modelCaptureEnabled' | 'modelCaptureBytes'>) {
  return (
    <>
      <span className={`settings-pill ${modelCaptureEnabled ? 'warn' : 'good'}`}>
        {modelCaptureEnabled ? 'capture on' : 'capture off'}
      </span>
      <span className="settings-pill">{modelCaptureBytes}</span>
    </>
  );
}

export function AutoModeStatusPills({ autoModePolicy }: Pick<SettingsModalState, 'autoModePolicy'>) {
  return (
    <>
      <span className={`settings-pill ${autoModePolicy.pendingCount === 0 ? 'good' : 'warn'}`}>
        {autoModePolicy.pendingCount === 0
          ? 'nothing waiting'
          : `${autoModePolicy.pendingCount} proposal${autoModePolicy.pendingCount === 1 ? '' : 's'} waiting`}
      </span>
      {autoModePolicy.state && (
        <>
          <span className="settings-pill">
            {autoModePolicy.state.config.enabled_default ? 'on by default' : 'off by default'}
          </span>
          <span className="settings-pill">{autoModePolicy.state.config.approval_policy}</span>
        </>
      )}
    </>
  );
}

export function HygieneStatusPills({
  autoSettleEnabled,
  mutedRepos,
  mutedAuthors,
}: Pick<SettingsModalState, 'autoSettleEnabled' | 'mutedRepos' | 'mutedAuthors'>) {
  return (
    <>
      <span className={`settings-pill ${autoSettleEnabled ? 'good' : ''}`}>
        {autoSettleEnabled ? 'auto-settle on' : 'auto-settle off'}
      </span>
      <span className="settings-pill">{mutedRepos.length} repos</span>
      <span className="settings-pill">{mutedAuthors.length} authors</span>
    </>
  );
}

export function WorkspaceStatusPills({
  hasProjectsDirChange,
  openSentFilesEnabled,
}: Pick<SettingsModalState, 'hasProjectsDirChange' | 'openSentFilesEnabled'>) {
  return (
    <>
      <span className={`settings-pill ${hasProjectsDirChange ? 'warn' : 'good'}`}>
        {hasProjectsDirChange ? 'project path edited' : 'project path saved'}
      </span>
      <span className={`settings-pill ${openSentFilesEnabled ? 'good' : ''}`}>
        {openSentFilesEnabled ? 'sent files open' : 'sent files ignored'}
      </span>
    </>
  );
}
