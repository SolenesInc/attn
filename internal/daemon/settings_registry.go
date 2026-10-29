package daemon

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/settingkeys"
)

type HomeSetting = settingkeys.HomeSetting
type MachineSetting = settingkeys.MachineSetting

const (
	settingNotebookSyncState     MachineSetting = "notebook_sync_state"
	settingNotebookSyncLastError MachineSetting = "notebook_sync_last_error"
)

const (
	settingOutpostsEnabled               = settingkeys.SettingOutpostsEnabled
	settingOutpostListen                 = settingkeys.SettingOutpostListen
	settingOutpostState                  = settingkeys.SettingOutpostState
	settingOutpostHome                   = settingkeys.SettingOutpostHome
	settingOutpostLastError              = settingkeys.SettingOutpostLastError
	SettingProjectsDirectory             = settingkeys.SettingProjectsDirectory
	SettingUIScale                       = settingkeys.SettingUIScale
	SettingGardenScale                   = settingkeys.SettingGardenScale
	SettingModelTierOverrides            = settingkeys.SettingModelTierOverrides
	SettingClaudeExecutable              = settingkeys.SettingClaudeExecutable
	SettingCodexExecutable               = settingkeys.SettingCodexExecutable
	SettingEditorExecutable              = settingkeys.SettingEditorExecutable
	SettingNewSessionAgent               = settingkeys.SettingNewSessionAgent
	SettingClaudeAvailable               = settingkeys.SettingClaudeAvailable
	SettingCodexAvailable                = settingkeys.SettingCodexAvailable
	SettingPTYBackendMode                = settingkeys.SettingPTYBackendMode
	SettingNewSessionLauncherEnabled     = settingkeys.SettingNewSessionLauncherEnabled
	SettingDirectAgentLaunchEnabled      = settingkeys.SettingDirectAgentLaunchEnabled
	SettingSharedPTYHostEnabled          = settingkeys.SettingSharedPTYHostEnabled
	SettingSharedPTYHostActive           = settingkeys.SettingSharedPTYHostActive
	SettingTheme                         = settingkeys.SettingTheme
	SettingReviewerModel                 = settingkeys.SettingReviewerModel
	SettingTailscaleEnabled              = settingkeys.SettingTailscaleEnabled
	SettingQueueModeEnabled              = settingkeys.SettingQueueModeEnabled
	SettingQueueCrewEnabled              = settingkeys.SettingQueueCrewEnabled
	SettingQueueShowHiddenSessions       = settingkeys.SettingQueueShowHiddenSessions
	SettingQueueQuietNotifications       = settingkeys.SettingQueueQuietNotifications
	SettingCodexSharedEnabled            = settingkeys.SettingCodexSharedEnabled
	SettingSidebarHarnessLogosEnabled    = settingkeys.SettingSidebarHarnessLogosEnabled
	SettingAutoApproveEnabled            = settingkeys.SettingAutoApproveEnabled
	SettingOpenSentFilesEnabled          = settingkeys.SettingOpenSentFilesEnabled
	SettingAutoSettleEnabled             = settingkeys.SettingAutoSettleEnabled
	SettingAsksEnabled                   = settingkeys.SettingAsksEnabled
	SettingCaptureShortcut               = settingkeys.SettingCaptureShortcut
	SettingQuickCaptureEnabled           = settingkeys.SettingQuickCaptureEnabled
	SettingCrewV2Enabled                 = settingkeys.SettingCrewV2Enabled
	SettingCalloutsEnabled               = settingkeys.SettingCalloutsEnabled
	SettingAutoSettleArmSeconds          = settingkeys.SettingAutoSettleArmSeconds
	SettingAutoSettleCountdownSeconds    = settingkeys.SettingAutoSettleCountdownSeconds
	SettingKeybindingsConfig             = settingkeys.SettingKeybindingsConfig
	SettingNewSessionYoloPrefix          = settingkeys.SettingNewSessionYoloPrefix
	SettingNewSessionDestinationPrefix   = settingkeys.SettingNewSessionDestinationPrefix
	SettingChiefModelPrefix              = settingkeys.SettingChiefModelPrefix
	SettingChiefEffortPrefix             = settingkeys.SettingChiefEffortPrefix
	SettingDefaultModelPrefix            = settingkeys.SettingDefaultModelPrefix
	SettingDefaultEffortPrefix           = settingkeys.SettingDefaultEffortPrefix
	SettingNotebookRoot                  = settingkeys.SettingNotebookRoot
	SettingNotebookRootEffective         = settingkeys.SettingNotebookRootEffective
	SettingAutoModeEnabledDefault        = settingkeys.SettingAutoModeEnabledDefault
	SettingActivityEnabled               = settingkeys.SettingActivityEnabled
	SettingActivityConfig                = settingkeys.SettingActivityConfig
	SettingActivityIntervals             = settingkeys.SettingActivityIntervals
	SettingActivityPresenceIdleSeconds   = settingkeys.SettingActivityPresenceIdleSeconds
	SettingGardenAdvisor                 = settingkeys.SettingGardenAdvisor
	SettingCrewHeartbeatEnabled          = settingkeys.SettingCrewHeartbeatEnabled
	SettingCrewAutoSleepEnabled          = settingkeys.SettingCrewAutoSleepEnabled
	SettingCrewCacheTTLSeconds           = settingkeys.SettingCrewCacheTTLSeconds
	SettingCrewCacheTTLPrefix            = settingkeys.SettingCrewCacheTTLPrefix
	SettingCrewHeartbeatLeadSeconds      = settingkeys.SettingCrewHeartbeatLeadSeconds
	SettingCrewAwaySeconds               = settingkeys.SettingCrewAwaySeconds
	SettingCrewWakeLimit                 = settingkeys.SettingCrewWakeLimit
	SettingCrewWakeLimitWindowSeconds    = settingkeys.SettingCrewWakeLimitWindowSeconds
	SettingChiefContextWindowCap         = settingkeys.SettingChiefContextWindowCap
	SettingHeadlessContextWindowCap      = settingkeys.SettingHeadlessContextWindowCap
	SettingDefaultContextWindowCapPrefix = settingkeys.SettingDefaultContextWindowCapPrefix
	SettingHeadlessTasksEnabled          = settingkeys.SettingHeadlessTasksEnabled
	SettingHeadlessTasksEnabledStored    = settingkeys.SettingHeadlessTasksEnabledStored
	SettingHeadlessTasksEnabledOverride  = settingkeys.SettingHeadlessTasksEnabledOverride
	SettingDBLastBackupAt                = settingkeys.SettingDBLastBackupAt
	SettingAnalyticsEnabled              = settingkeys.SettingAnalyticsEnabled
	SettingAnalyticsSharingEnabled       = settingkeys.SettingAnalyticsSharingEnabled
	SettingAnalyticsShareVents           = settingkeys.SettingAnalyticsShareVents
	SettingAnalyticsToolVents            = settingkeys.SettingAnalyticsToolVents
	settingWorktreeSweepEnabled          = settingkeys.SettingWorktreeSweepEnabled
	SettingSessionsFilters               = settingkeys.SettingSessionsFilters
	SettingPluginPriorities              = settingkeys.SettingPluginPriorities
	SettingInstalledBundledPlugins       = settingkeys.SettingInstalledBundledPlugins
	settingAutoModeDenialCursor          = settingkeys.SettingAutoModeDenialCursor
	DestinationNewWorktree               = "new_worktree"
	DestinationMainRepo                  = "main_repo"
)

type settingScope int

const (
	daemonScope settingScope = iota + 1
)

type settingSpec struct {
	key          string
	scope        settingScope
	class        settingkeys.Class
	description  string
	validate     func(*Daemon, string, string) error
	readOnly     bool
	experimental bool
}

func (d *Daemon) homeSetting(key HomeSetting) string {
	status := d.enrollmentStatus()
	if !status.IsHome() {
		return d.store.CopiedSetting(string(key), status.HomeDaemonID)
	}
	return d.store.GetSetting(string(key))
}
func (d *Daemon) machineSetting(key MachineSetting) string { return d.store.GetSetting(string(key)) }

var settingSpecs = classifiedSettings([]settingSpec{
	{key: string(settingNotebookSyncState), scope: daemonScope, description: "Current Notebook sync state on this machine.", readOnly: true},
	{key: string(settingNotebookSyncLastError), scope: daemonScope, description: "Current Notebook sync error and its reason.", readOnly: true},
	{key: string(settingOutpostsEnabled), scope: daemonScope, experimental: true, description: "Connect to outposts from this home: true or false. Experimental; off by default.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(settingOutpostListen), scope: daemonScope, description: "Listen for a home bridge on this address:port. Empty disables the outpost listener.", validate: func(d *Daemon, key, value string) error {
		if value == "" {
			return nil
		}
		return validateOutpostAddress(value)
	}},
	{key: string(settingOutpostState), scope: daemonScope, description: "Current outpost listener and bridge state.", readOnly: true},
	{key: string(settingOutpostHome), scope: daemonScope, description: "Recorded home daemon id. Release it with attn enrollment leave.", readOnly: true},
	{key: string(settingOutpostLastError), scope: daemonScope, description: "Last outpost bridge error, kept until the next successful connection.", readOnly: true},
	{key: string(SettingCaptureShortcut), scope: daemonScope, description: "Global shortcut for Quick Capture. Empty turns the shortcut off.", validate: func(d *Daemon, key, value string) error { return nil }},
	{key: string(SettingQuickCaptureEnabled), scope: daemonScope, experimental: true, description: "Enable the experimental Quick Capture panel: true or false. Changes to agent guidance apply at their next launch.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingCrewV2Enabled), scope: daemonScope, experimental: true, description: "Enable experimental crew v2: true or false. Crew guidance and harness memory settings apply at the next launch.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingNewSessionLauncherEnabled), scope: daemonScope, experimental: true, description: "Show the new session launcher: true or false.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingDirectAgentLaunchEnabled), scope: daemonScope, experimental: true, description: "Launch agents directly: true or false.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingCodexSharedEnabled), scope: daemonScope, experimental: true, description: "Use the shared Codex app server: true or false.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingQueueShowHiddenSessions), scope: daemonScope, experimental: true, description: "Include hidden sessions in the queue: true or false.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingQueueQuietNotifications), scope: daemonScope, experimental: true, description: "Keep agents a notification woke out of the queue unless they open their turn: true or false.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingAsksEnabled), scope: daemonScope, experimental: true, description: "Enable agent asks: true or false.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingAnalyticsEnabled), scope: daemonScope, experimental: true, description: "Record usage analytics on this machine: true or false. Experimental; off by default.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingAnalyticsSharingEnabled), scope: daemonScope, description: "Upload the recorded usage analytics to attn's maintainers: true or false. Needs analytics.enabled; off by default.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingAnalyticsShareVents), scope: daemonScope, description: "Send each new attn vent, with its text, in the next upload: true or false. Off by default.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingAnalyticsToolVents), scope: daemonScope, description: "Let new sessions also vent about their other tools, such as skills, other CLIs and harness permissions; tool vents never leave the machine: true or false. Off by default.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingCalloutsEnabled), scope: daemonScope, experimental: true, description: "Let agents point at the app interface: true or false.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: string(SettingChiefContextWindowCap), scope: daemonScope, description: "Chief context budget in tokens, from 10000 to 2000000. Empty uses the default budget.", validate: func(d *Daemon, key, value string) error { return validateContextWindowCap(value) }},
	{key: "chief_model_<harness>", scope: daemonScope, description: "Model for chief sessions. Empty uses the deep intelligence tier.", validate: func(d *Daemon, key, value string) error { return nil }},
	{key: "chief_effort_<harness>", scope: daemonScope, description: "Reasoning effort for chief sessions. Empty uses the default effort.", validate: func(d *Daemon, key, value string) error { return nil }},
	{key: string(SettingClaudeExecutable), scope: daemonScope, description: "Executable for Claude. Empty finds the default executable on PATH.", validate: func(d *Daemon, key, value string) error { return d.validateExecutableSetting(value) }},
	{key: string(SettingCodexExecutable), scope: daemonScope, description: "Executable for Codex. Empty finds the default executable on PATH.", validate: func(d *Daemon, key, value string) error { return d.validateExecutableSetting(value) }},
	{key: string(SettingModelTierOverrides), scope: daemonScope, description: "Models for each intelligence tier as a JSON object. Empty uses harness defaults.", validate: func(d *Daemon, key, value string) error {
		_, err := modeltiers.ParseOverrides(value)
		return err
	}},
	{key: string(SettingProjectsDirectory), scope: daemonScope, description: "Folder used to discover projects. Absolute or ~/ path; must not be empty.", validate: func(d *Daemon, key, value string) error {
		return validateProjectsDirectory(value)
	}},
	{key: string(SettingUIScale), scope: daemonScope, description: "App interface scale, from 0.5 to 2.0.", validate: func(d *Daemon, key, value string) error {
		return validateUIScale(value)
	}},
	{key: string(SettingGardenScale), scope: daemonScope, description: "Garden text scale, from 0.5 to 2.0. Empty uses the app scale.", validate: func(d *Daemon, key, value string) error {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return validateUIScale(value)
	}},
	{key: string(SettingEditorExecutable), scope: daemonScope, description: "Command to open files in your editor. Empty uses the default editor.", validate: func(d *Daemon, key, value string) error {
		return validateEditorSetting(value)
	}},
	{key: string(SettingNewSessionAgent), scope: daemonScope, description: "Harness selected for new sessions. Empty uses the default harness.", validate: func(d *Daemon, key, value string) error {
		return d.validateNewSessionAgent(value)
	}},
	{key: string(SettingTheme), scope: daemonScope, description: "App color theme: dark, light or system.", validate: func(d *Daemon, key, value string) error {
		return validateTheme(value)
	}},
	{key: string(SettingSharedPTYHostEnabled), scope: daemonScope, experimental: true, description: "Use the experimental shared terminal host: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingTailscaleEnabled), scope: daemonScope, description: "Expose attn through Tailscale: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingAutoApproveEnabled), scope: daemonScope, description: "Auto-approve supported agent requests: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingQueueModeEnabled), scope: daemonScope, description: "Use queue flow across all profiles: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingQueueCrewEnabled), scope: daemonScope, description: "Include crew sessions in the queue: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingSidebarHarnessLogosEnabled), scope: daemonScope, description: "Show harness logos in the sidebar: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingAutoSettleEnabled), scope: daemonScope, description: "Settle completed turns automatically: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingActivityEnabled), scope: daemonScope, description: "Track session activity: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingOpenSentFilesEnabled), scope: daemonScope, description: "Open files sent by agents: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingHeadlessTasksEnabled), scope: daemonScope, description: "Run background model tasks: true or false. An environment override takes precedence.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingWorktreeSweepEnabled), scope: daemonScope, description: "Remove unused worktrees automatically: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingAutoSettleArmSeconds), scope: daemonScope, description: "Delay before arming automatic settlement, in seconds. Empty uses the default delay.", validate: func(d *Daemon, key, value string) error {
		return validateAutoSettleSeconds("auto-settle delay", value, autoSettleArmMinSeconds, autoSettleArmMaxSeconds)
	}},
	{key: string(SettingAutoSettleCountdownSeconds), scope: daemonScope, description: "Automatic settlement countdown, in seconds. Empty uses the default countdown.", validate: func(d *Daemon, key, value string) error {
		return validateAutoSettleSeconds("auto-settle countdown", value, autoSettleCountdownMinSeconds, autoSettleCountdownMaxSeconds)
	}},
	{key: string(SettingHeadlessContextWindowCap), scope: daemonScope, description: "Background model context budget in tokens, from 10000 to 2000000. Empty uses the default budget.", validate: func(d *Daemon, key, value string) error {
		return validateContextWindowCap(value)
	}},
	{key: string(SettingActivityConfig), scope: daemonScope, description: "Session activity tracking configuration as JSON. Empty leaves no agent selected; activity requires a selected agent.", validate: func(d *Daemon, key, value string) error {
		return d.validateActivitySetting(value)
	}},
	{key: string(SettingGardenAdvisor), scope: daemonScope, description: "Garden advisor harness and model as JSON. Empty uses the default advisor.", validate: func(d *Daemon, key, value string) error {
		return d.validateGardenAdvisorSetting(value)
	}},
	{key: string(SettingActivityIntervals), scope: daemonScope, description: "Activity sampling intervals as JSON. Empty uses defaults.", validate: func(d *Daemon, key, value string) error {
		_, err := parseActivityIntervals(value)
		return err
	}},
	{key: string(SettingActivityPresenceIdleSeconds), scope: daemonScope, description: "Seconds without input before presence becomes idle.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting(
			"session activity presence idle",
			value,
			activityPresenceIdleMinSeconds,
			activityPresenceIdleMaxSeconds,
		)
	}},
	{key: string(SettingCrewHeartbeatEnabled), scope: daemonScope, description: "Send crew heartbeats: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingCrewAutoSleepEnabled), scope: daemonScope, description: "Put idle crew members to sleep automatically: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(SettingCrewCacheTTLSeconds), scope: daemonScope, description: "Crew prompt cache lifetime in seconds.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew cache TTL", value, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
	}},
	{key: string(SettingCrewHeartbeatLeadSeconds), scope: daemonScope, description: "Seconds before cache expiry to send a crew heartbeat.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew heartbeat lead", value, crewHeartbeatLeadMinSeconds, crewHeartbeatLeadMaxSeconds)
	}},
	{key: string(SettingCrewAwaySeconds), scope: daemonScope, description: "Seconds of user inactivity before crew sleep.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew away threshold", value, crewAwayMinSeconds, crewAwayMaxSeconds)
	}},
	{key: string(SettingCrewWakeLimit), scope: daemonScope, description: "Maximum automatic crew wakes per window. Zero disables automatic wakes.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew wake limit", value, 0, crewWakeLimitMax)
	}},
	{key: string(SettingCrewWakeLimitWindowSeconds), scope: daemonScope, description: "Crew automatic wake accounting window in seconds.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew wake limit window", value, crewWakeLimitWindowMinSecs, crewWakeLimitWindowMaxSecs)
	}},
	{key: string(SettingNotebookRoot), scope: daemonScope, description: "Folder for the Notebook, journal and seed artifacts. Absolute or ~/ path outside attn's data folder. Empty uses the default folder.", validate: func(d *Daemon, key, value string) error {
		if err := validateNotebookRoot(value); err != nil {
			return err
		}
		return d.notebookRootChangeAllowed()
	}},
	{key: string(SettingKeybindingsConfig), scope: daemonScope, description: "Keyboard shortcut overrides as JSON. Empty restores default shortcuts.", validate: func(d *Daemon, key, value string) error {
		return validateKeybindingsConfig(value)
	}},
	{key: string(SettingSessionsFilters), scope: daemonScope, description: "Saved ledger filters as JSON. Empty restores default filters.", validate: func(d *Daemon, key, value string) error {
		return validateSessionsFilters(value)
	}},
	{key: string(SettingReviewerModel), scope: daemonScope, description: "Review model. Empty uses the default review model.", validate: func(d *Daemon, key, value string) error {
		return nil
	}},
	{key: "<harness>_executable", scope: daemonScope, description: "Executable for this harness. Empty finds the default executable on PATH.", validate: func(d *Daemon, key, value string) error { return d.validateExecutableSetting(value) }},
	{key: "new_session_yolo_<location>", scope: daemonScope, description: "Start sessions at this location with approval bypass: true or false. The local location uses new_session_yolo_local.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: "new_session_destination_<repository>", scope: daemonScope, description: "New session destination for a repository: new_worktree or main_repo. Use local_<absolute-path> as the suffix. Empty uses the default destination.", validate: func(d *Daemon, key, value string) error { return validateNewSessionDestination(value) }},
	{key: "default_model_<harness>", scope: daemonScope, description: "Model for new sessions. Empty uses the harness default.", validate: func(d *Daemon, key, value string) error { return nil }},
	{key: "default_effort_<harness>", scope: daemonScope, description: "Reasoning effort for new sessions. Empty uses the harness default.", validate: func(d *Daemon, key, value string) error { return nil }},
	{key: "default_context_window_cap_<harness>", scope: daemonScope, description: "Context budget for new sessions, from 10000 to 2000000 tokens. Empty uses the harness default.", validate: func(d *Daemon, key, value string) error { return validateContextWindowCap(value) }},
	{key: "crew.cache_ttl_seconds.<harness>", scope: daemonScope, description: "Crew prompt cache lifetime for this harness, in seconds.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew cache TTL", value, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
	}},
	{key: "session_cost.price.<model>", scope: daemonScope, description: "Custom model token prices as JSON. Empty uses catalog prices.", validate: func(d *Daemon, key, value string) error {
		_, err := sessioncost.ParseOverrides(map[string]string{key: value})
		return err
	}},
	{key: "session_cost.billed_as.<model>", scope: daemonScope, description: "Billing model alias. Empty removes the alias.", validate: func(d *Daemon, key, value string) error { return sessioncost.ValidateBilledAs(key, value) }},
	{key: "notebook.root.effective", scope: daemonScope, description: "Resolved Notebook folder.", readOnly: true},
	{key: "<harness>_available", scope: daemonScope, description: "Whether this harness executable is available on PATH.", readOnly: true},
	{key: "<harness>_cap_<capability>", scope: daemonScope, description: "Whether this harness supports the named capability.", readOnly: true},
	{key: "pty_backend_mode", scope: daemonScope, description: "Active terminal backend.", readOnly: true},
	{key: "pty_shared_host_active", scope: daemonScope, description: "Whether the shared terminal host is active.", readOnly: true},
	{key: "automode_enabled_default", scope: daemonScope, description: "Default auto-mode state from the auto-mode configuration.", readOnly: true},
	{key: "db.last_backup_at", scope: daemonScope, description: "Time of the last database backup.", readOnly: true},
	{key: "headless_tasks.enabled.stored", scope: daemonScope, description: "Stored background model task switch.", readOnly: true},
	{key: "headless_tasks.enabled.override", scope: daemonScope, description: "Environment override for background model tasks.", readOnly: true},
	{key: "tailscale_status", scope: daemonScope, description: "Current Tailscale connection status.", readOnly: true},
	{key: "tailscale_domain", scope: daemonScope, description: "Current Tailscale domain.", readOnly: true},
	{key: "tailscale_url", scope: daemonScope, description: "Current Tailscale app URL.", readOnly: true},
	{key: "tailscale_auth_url", scope: daemonScope, description: "Tailscale sign-in URL.", readOnly: true},
	{key: "tailscale_error", scope: daemonScope, description: "Current Tailscale error.", readOnly: true},
})

func classifiedSettings(specs []settingSpec) []settingSpec {
	for i := range specs {
		specs[i].class = settingkeys.ClassOf(specs[i].key)
	}
	return specs
}

func settingPatternMatches(pattern, key string) bool {
	for {
		start := strings.IndexByte(pattern, '<')
		if start < 0 {
			return pattern == key
		}
		prefix := pattern[:start]
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		key = strings.TrimPrefix(key, prefix)
		end := strings.IndexByte(pattern[start:], '>') + start
		pattern = pattern[end+1:]
		next := strings.IndexByte(pattern, '<')
		suffix := pattern
		if next >= 0 {
			suffix = pattern[:next]
		}
		if suffix == "" {
			return key != ""
		}
		at := strings.Index(key, suffix)
		if at <= 0 {
			return false
		}
		key = key[at:]
	}
}

func lookupSetting(key string) (settingSpec, bool) {
	for _, spec := range settingSpecs {
		if spec.key == key {
			return spec, true
		}
	}
	for _, spec := range settingSpecs {
		if !strings.Contains(spec.key, "<") || !settingPatternMatches(spec.key, key) {
			continue
		}
		if spec.key == "<harness>_executable" {
			if _, ok := isAgentExecutableSettingKey(key); !ok {
				continue
			}
		}
		return spec, true
	}
	return settingSpec{}, false
}

func writableSetting(key string) (settingSpec, error) {
	spec, ok := lookupSetting(key)
	if !ok {
		return settingSpec{}, fmt.Errorf("unknown setting: %s", key)
	}
	if strings.Contains(spec.key, "<") && spec.key == key {
		return settingSpec{}, fmt.Errorf("setting %s requires a concrete key; replace its placeholders", key)
	}
	if spec.readOnly {
		return settingSpec{}, fmt.Errorf("setting %s is read-only", key)
	}
	return spec, nil
}

func validateBooleanSetting(value string) error {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "true", "false":
		return nil
	default:
		return fmt.Errorf("invalid boolean value: %s", value)
	}
}

func resolveBoundedIntSetting(value string, fallback, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < min || n > max {
		return fallback
	}
	return n
}

func validateBoundedIntSetting(label, value string, min, max int) error {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s must be a whole number", label)
	}
	if n < min || n > max {
		return fmt.Errorf("%s must be between %d and %d", label, min, max)
	}
	return nil
}

func (d *Daemon) settingValues() map[string]string {
	values := d.store.GetAllSettings()
	status := d.enrollmentStatus()
	if status.IsHome() {
		return values
	}
	for key := range values {
		if settingkeys.ClassOf(key) == settingkeys.Home {
			delete(values, key)
		}
	}
	for key, value := range d.store.CopiedSettings(status.HomeDaemonID) {
		values[key] = value
	}
	return values
}
