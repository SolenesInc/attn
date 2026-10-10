package daemon

import (
	"fmt"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/sessioncost"
	"strings"
)

const (
	settingProjectsDirectory             daemonSettingKey  = "projects_directory"
	settingUIScale                       daemonSettingKey  = "uiScale"
	settingGardenScale                   daemonSettingKey  = "gardenScale"
	settingModelTierOverrides            daemonSettingKey  = "model_tier_overrides"
	settingClaudeExecutable              daemonSettingKey  = "claude_executable"
	settingCodexExecutable               daemonSettingKey  = "codex_executable"
	settingCopilotExecutable             daemonSettingKey  = "copilot_executable"
	settingEditorExecutable              daemonSettingKey  = "editor_executable"
	settingNewSessionAgent               daemonSettingKey  = "new_session_agent"
	settingClaudeAvailable               daemonSettingKey  = "claude_available"
	settingCodexAvailable                daemonSettingKey  = "codex_available"
	settingCopilotAvailable              daemonSettingKey  = "copilot_available"
	settingPTYBackendMode                daemonSettingKey  = "pty_backend_mode"
	settingSharedPTYHostEnabled          daemonSettingKey  = "pty_shared_host_enabled"
	settingSharedPTYHostActive           daemonSettingKey  = "pty_shared_host_active"
	settingTheme                         daemonSettingKey  = "theme"
	settingReviewerModel                 daemonSettingKey  = "reviewer_model"
	settingTailscaleEnabled              daemonSettingKey  = "tailscale_enabled"
	settingWorkflowsEnabled              daemonSettingKey  = "workflows_enabled"
	settingModelCaptureEnabled           daemonSettingKey  = "model_capture.enabled"
	settingModelCaptureIntervalSeconds   daemonSettingKey  = "model_capture.interval_seconds"
	settingModelCaptureMaxGB             daemonSettingKey  = "model_capture.max_gb"
	settingModelCapturePath              daemonSettingKey  = "model_capture.path"
	settingModelCaptureBytes             daemonSettingKey  = "model_capture.bytes"
	settingQueueModeEnabled              daemonSettingKey  = "queue_mode_enabled"
	settingQueueCrewEnabled              daemonSettingKey  = "queue_crew_enabled"
	settingSidebarHarnessLogosEnabled    daemonSettingKey  = "sidebar_harness_logos_enabled"
	settingAutoApproveEnabled            daemonSettingKey  = "auto_approve_enabled"
	settingOpenSentFilesEnabled          daemonSettingKey  = "open_sent_files_enabled"
	settingAutoSettleEnabled             daemonSettingKey  = "auto_settle_enabled"
	settingAutoSettleArmSeconds          daemonSettingKey  = "auto_settle_arm_seconds"
	settingAutoSettleCountdownSeconds    daemonSettingKey  = "auto_settle_countdown_seconds"
	settingKeybindingsConfig             daemonSettingKey  = "keybindings_config"
	settingNewSessionYoloPrefix          daemonSettingKey  = "new_session_yolo_"
	settingNewSessionDestinationPrefix   daemonSettingKey  = "new_session_destination_"
	DestinationNewWorktree                                 = "new_worktree"
	DestinationMainRepo                                    = "main_repo"
	settingChiefModelPrefix              daemonSettingKey  = "chief_model_"
	settingChiefEffortPrefix             daemonSettingKey  = "chief_effort_"
	settingDefaultModelPrefix            daemonSettingKey  = "default_model_"
	settingDefaultEffortPrefix           daemonSettingKey  = "default_effort_"
	settingNotebookRoot                  profileSettingKey = "notebook.root"
	settingNotebookRootEffective         profileSettingKey = "notebook.root.effective"
	settingNotebookRootDefault           profileSettingKey = "notebook.root.default"
	settingAutoModeEnabledDefault        daemonSettingKey  = "automode_enabled_default"
	settingActivityEnabled               daemonSettingKey  = "activity.enabled"
	settingActivityConfig                daemonSettingKey  = "activity.config"
	settingActivityIntervals             daemonSettingKey  = "activity.intervals"
	settingActivityPresenceIdleSeconds   daemonSettingKey  = "activity.presence_idle_seconds"
	settingGardenAdvisor                 daemonSettingKey  = "garden.advisor"
	settingCrewHeartbeatEnabled          daemonSettingKey  = "crew.heartbeat_enabled"
	settingCrewAutoSleepEnabled          daemonSettingKey  = "crew.autosleep_enabled"
	settingCrewCacheTTLSeconds           daemonSettingKey  = "crew.cache_ttl_seconds"
	settingCrewCacheTTLPrefix            daemonSettingKey  = "crew.cache_ttl_seconds."
	settingCrewHeartbeatLeadSeconds      daemonSettingKey  = "crew.heartbeat_lead_seconds"
	settingCrewAwaySeconds               daemonSettingKey  = "crew.away_seconds"
	settingCrewWakeLimit                 daemonSettingKey  = "crew.wake_limit"
	settingCrewWakeLimitWindowSeconds    daemonSettingKey  = "crew.wake_limit_window_seconds"
	settingChiefContextWindowCap         daemonSettingKey  = "chief_context_window_cap"
	settingHeadlessContextWindowCap      daemonSettingKey  = "headless_context_window_cap"
	settingDefaultContextWindowCapPrefix daemonSettingKey  = "default_context_window_cap_"
	settingHeadlessTasksEnabled          daemonSettingKey  = headless.SettingKey
	settingHeadlessTasksEnabledStored    daemonSettingKey  = headless.SettingKey + ".stored"
	settingHeadlessTasksEnabledOverride  daemonSettingKey  = headless.SettingKey + ".override"
	settingDBLastBackupAt                daemonSettingKey  = "db.last_backup_at"
)

type daemonSettingKey string
type profileSettingKey string
type settingScope int

const (
	daemonScope settingScope = iota + 1
	profileScope
)

type settingSpec struct {
	key         string
	scope       settingScope
	description string
	validate    func(*Daemon, string, string) error
	readOnly    bool
}

func (d *Daemon) daemonSetting(key daemonSettingKey) string { return d.store.GetSetting(string(key)) }
func (d *Daemon) profileSetting(id string, key profileSettingKey) string {
	return d.store.ProfileSetting(id, string(key))
}

var settingSpecs = []settingSpec{
	{key: string(settingModelTierOverrides), scope: daemonScope, description: "Models used for each intelligence tier, as a JSON object. Empty uses harness defaults.", validate: func(d *Daemon, key, value string) error {
		_, err := modeltiers.ParseOverrides(value)
		return err
	}},
	{key: string(settingProjectsDirectory), scope: daemonScope, description: "Folder used to discover projects. Absolute or ~/ path; must not be empty.", validate: func(d *Daemon, key, value string) error {
		return validateProjectsDirectory(value)
	}},
	{key: string(settingUIScale), scope: daemonScope, description: "App interface scale, from 0.5 to 2.0.", validate: func(d *Daemon, key, value string) error {
		return validateUIScale(value)
	}},
	{key: string(settingGardenScale), scope: daemonScope, description: "Garden text scale, from 0.5 to 2.0. Empty uses the app scale.", validate: func(d *Daemon, key, value string) error {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return validateUIScale(value)
	}},
	{key: string(settingEditorExecutable), scope: daemonScope, description: "Command used to open files in your editor. Empty uses the default editor.", validate: func(d *Daemon, key, value string) error {
		return validateEditorSetting(value)
	}},
	{key: string(settingNewSessionAgent), scope: daemonScope, description: "Harness selected for new sessions. Empty uses the default harness.", validate: func(d *Daemon, key, value string) error {
		return d.validateNewSessionAgent(value)
	}},
	{key: string(settingTheme), scope: daemonScope, description: "App color theme: dark, light or system.", validate: func(d *Daemon, key, value string) error {
		return validateTheme(value)
	}},
	{key: string(settingSharedPTYHostEnabled), scope: daemonScope, description: "Use the experimental shared terminal host: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingTailscaleEnabled), scope: daemonScope, description: "Expose attn through Tailscale: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingWorkflowsEnabled), scope: daemonScope, description: "Include workflow guidance in agent instructions: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingAutoApproveEnabled), scope: daemonScope, description: "Automatically approve supported agent requests: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingQueueModeEnabled), scope: daemonScope, description: "Use queue flow across all profiles: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingQueueCrewEnabled), scope: daemonScope, description: "Include crew sessions in the queue: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingSidebarHarnessLogosEnabled), scope: daemonScope, description: "Show harness logos in the sidebar: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingAutoSettleEnabled), scope: daemonScope, description: "Settle completed turns automatically: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingModelCaptureEnabled), scope: daemonScope, description: "Capture model traffic locally: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingActivityEnabled), scope: daemonScope, description: "Track session activity: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingOpenSentFilesEnabled), scope: daemonScope, description: "Open files sent by agents: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingHeadlessTasksEnabled), scope: daemonScope, description: "Run background model tasks: true or false. An environment override takes precedence.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingWorktreeSweepEnabled), scope: daemonScope, description: "Remove unused worktrees automatically: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingModelCaptureIntervalSeconds), scope: daemonScope, description: "Model capture interval in seconds. Empty uses the default interval.", validate: func(d *Daemon, key, value string) error {
		return validateModelCaptureInterval(value)
	}},
	{key: string(settingModelCaptureMaxGB), scope: daemonScope, description: "Maximum model capture storage in GB. Empty uses the default storage budget.", validate: func(d *Daemon, key, value string) error {
		return validateModelCaptureMaxGB(value)
	}},
	{key: string(settingAutoSettleArmSeconds), scope: daemonScope, description: "Delay before arming automatic settlement, in seconds. Empty uses the default delay.", validate: func(d *Daemon, key, value string) error {
		return validateAutoSettleSeconds("auto-settle delay", value, autoSettleArmMinSeconds, autoSettleArmMaxSeconds)
	}},
	{key: string(settingAutoSettleCountdownSeconds), scope: daemonScope, description: "Automatic settlement countdown, in seconds. Empty uses the default countdown.", validate: func(d *Daemon, key, value string) error {
		return validateAutoSettleSeconds("auto-settle countdown", value, autoSettleCountdownMinSeconds, autoSettleCountdownMaxSeconds)
	}},
	{key: string(settingChiefContextWindowCap), scope: daemonScope, description: "Chief context budget in tokens, from 10000 to 2000000. Empty uses the default budget.", validate: func(d *Daemon, key, value string) error {
		return validateContextWindowCap(value)
	}},
	{key: string(settingHeadlessContextWindowCap), scope: daemonScope, description: "Background model context budget in tokens, from 10000 to 2000000. Empty uses the default budget.", validate: func(d *Daemon, key, value string) error {
		return validateContextWindowCap(value)
	}},
	{key: string(settingActivityConfig), scope: daemonScope, description: "Session activity tracking configuration as JSON. Empty uses the default configuration.", validate: func(d *Daemon, key, value string) error {
		return d.validateActivitySetting(value)
	}},
	{key: string(settingGardenAdvisor), scope: daemonScope, description: "Garden advisor harness and model as JSON. Empty uses the default advisor.", validate: func(d *Daemon, key, value string) error {
		return d.validateGardenAdvisorSetting(value)
	}},
	{key: string(settingActivityIntervals), scope: daemonScope, description: "Activity sampling intervals as JSON. Empty uses default intervals.", validate: func(d *Daemon, key, value string) error {
		_, err := parseActivityIntervals(value)
		return err
	}},
	{key: string(settingActivityPresenceIdleSeconds), scope: daemonScope, description: "Seconds without input before presence becomes idle. Empty uses the default threshold.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting(
			"session activity presence idle",
			value,
			activityPresenceIdleMinSeconds,
			activityPresenceIdleMaxSeconds,
		)
	}},
	{key: string(settingCrewHeartbeatEnabled), scope: daemonScope, description: "Send crew heartbeats: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingCrewAutoSleepEnabled), scope: daemonScope, description: "Put idle crew members to sleep automatically: true or false.", validate: func(d *Daemon, key, value string) error {
		return validateBooleanSetting(value)
	}},
	{key: string(settingCrewCacheTTLSeconds), scope: daemonScope, description: "Crew prompt cache lifetime in seconds. Empty uses the default lifetime.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew cache TTL", value, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
	}},
	{key: string(settingCrewHeartbeatLeadSeconds), scope: daemonScope, description: "Seconds before cache expiry to send a crew heartbeat. Empty uses the default lead.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew heartbeat lead", value, crewHeartbeatLeadMinSeconds, crewHeartbeatLeadMaxSeconds)
	}},
	{key: string(settingCrewAwaySeconds), scope: daemonScope, description: "Seconds of user inactivity before crew sleep. Empty uses the default threshold.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew away threshold", value, crewAwayMinSeconds, crewAwayMaxSeconds)
	}},
	{key: string(settingCrewWakeLimit), scope: daemonScope, description: "Maximum automatic crew wakes per window. Zero disables automatic wakes; empty uses the default.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew wake limit", value, 0, crewWakeLimitMax)
	}},
	{key: string(settingCrewWakeLimitWindowSeconds), scope: daemonScope, description: "Crew automatic wake accounting window in seconds. Empty uses the default window.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew wake limit window", value, crewWakeLimitWindowMinSecs, crewWakeLimitWindowMaxSecs)
	}},
	{key: string(settingNotebookRoot), scope: profileScope, description: "Folder for this profile's Notebook, journal and seed artifacts. Absolute or ~/ path outside attn's data folder. Empty chooses a name-based default; existing notes are not moved.", validate: func(d *Daemon, key, value string) error {
		return validateNotebookRoot(value)
	}},
	{key: string(settingKeybindingsConfig), scope: daemonScope, description: "Keyboard shortcut overrides as JSON. Empty restores default shortcuts.", validate: func(d *Daemon, key, value string) error {
		return validateKeybindingsConfig(value)
	}},
	{key: string(settingSessionsFilters), scope: daemonScope, description: "Saved ledger filters as JSON. Empty restores default filters.", validate: func(d *Daemon, key, value string) error {
		return validateSessionsFilters(value)
	}},
	{key: string(settingReviewerModel), scope: daemonScope, description: "Model used for reviews. Empty uses the default review model.", validate: func(d *Daemon, key, value string) error {
		return nil
	}},
	{key: "<harness>_executable", scope: daemonScope, description: "Executable for this harness. Empty finds the default executable on PATH.", validate: func(d *Daemon, key, value string) error { return validateExecutableSetting(value) }},
	{key: "new_session_yolo_<harness>", scope: daemonScope, description: "Start this harness with approval bypass: true or false.", validate: func(d *Daemon, key, value string) error { return validateBooleanSetting(value) }},
	{key: "new_session_destination_<harness>", scope: daemonScope, description: "New sessions start in new_worktree or main_repo. Empty uses the default destination.", validate: func(d *Daemon, key, value string) error { return validateNewSessionDestination(value) }},
	{key: "chief_model_<harness>", scope: daemonScope, description: "Model used for the chief. Empty uses the default deep-tier model.", validate: func(d *Daemon, key, value string) error { return nil }},
	{key: "chief_effort_<harness>", scope: daemonScope, description: "Reasoning effort used for the chief. Empty uses the harness default.", validate: func(d *Daemon, key, value string) error { return nil }},
	{key: "default_model_<harness>", scope: daemonScope, description: "Model used for new sessions. Empty uses the harness default.", validate: func(d *Daemon, key, value string) error { return nil }},
	{key: "default_effort_<harness>", scope: daemonScope, description: "Reasoning effort used for new sessions. Empty uses the harness default.", validate: func(d *Daemon, key, value string) error { return nil }},
	{key: "default_context_window_cap_<harness>", scope: daemonScope, description: "Context budget for new sessions, from 10000 to 2000000 tokens. Empty uses the harness default.", validate: func(d *Daemon, key, value string) error { return validateContextWindowCap(value) }},
	{key: "crew.cache_ttl_seconds.<member>", scope: daemonScope, description: "Prompt cache lifetime for this crew member, in seconds. Empty uses the shared crew lifetime.", validate: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew cache TTL", value, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
	}},
	{key: "session_cost.price.<model>", scope: daemonScope, description: "Custom model token prices as JSON. Empty uses catalog prices.", validate: func(d *Daemon, key, value string) error {
		_, err := sessioncost.ParseOverrides(map[string]string{key: value})
		return err
	}},
	{key: "session_cost.billed_as.<model>", scope: daemonScope, description: "Billing model alias. Empty removes the alias.", validate: func(d *Daemon, key, value string) error { return sessioncost.ValidateBilledAs(key, value) }},
	{key: string(settingNotebookRootDefault), scope: profileScope, description: "Folder chosen when this profile clears notebook.root; existing notes are not moved.", readOnly: true},
	{key: "notebook.root.effective", scope: profileScope, description: "Resolved Notebook folder for this profile.", readOnly: true},
	{key: "<harness>_available", scope: daemonScope, description: "Whether this harness executable is available on PATH.", readOnly: true},
	{key: "<harness>_cap_<capability>", scope: daemonScope, description: "Whether this harness supports the named capability.", readOnly: true},
	{key: "pty_backend_mode", scope: daemonScope, description: "Active terminal backend.", readOnly: true},
	{key: "pty_shared_host_active", scope: daemonScope, description: "Whether the shared terminal host is active.", readOnly: true},
	{key: "automode_enabled_default", scope: daemonScope, description: "Default auto-mode state from the auto-mode configuration.", readOnly: true},
	{key: "model_capture.path", scope: daemonScope, description: "Folder containing captured model traffic.", readOnly: true},
	{key: "model_capture.bytes", scope: daemonScope, description: "Current model capture storage in bytes.", readOnly: true},
	{key: "db.last_backup_at", scope: daemonScope, description: "Time of the last database backup.", readOnly: true},
	{key: "headless_tasks.enabled.stored", scope: daemonScope, description: "Stored background model task switch.", readOnly: true},
	{key: "headless_tasks.enabled.override", scope: daemonScope, description: "Environment override for background model tasks.", readOnly: true},
	{key: "tailscale_status", scope: daemonScope, description: "Current Tailscale connection status.", readOnly: true},
	{key: "tailscale_domain", scope: daemonScope, description: "Current Tailscale domain.", readOnly: true},
	{key: "tailscale_url", scope: daemonScope, description: "Current Tailscale app URL.", readOnly: true},
	{key: "tailscale_auth_url", scope: daemonScope, description: "Tailscale sign-in URL.", readOnly: true},
	{key: "tailscale_error", scope: daemonScope, description: "Current Tailscale error.", readOnly: true},
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
