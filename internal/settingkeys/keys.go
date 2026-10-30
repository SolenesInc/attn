package settingkeys

import "strings"

type HomeSetting string
type MachineSetting string
type Class int

const (
	Home Class = iota + 1
	Machine
)

const (
	SettingOutpostsEnabled               MachineSetting = "outposts_enabled"
	SettingOutpostState                  MachineSetting = "outpost_state"
	SettingOutpostHome                   MachineSetting = "outpost_home"
	SettingOutpostLastError              MachineSetting = "outpost_last_error"
	SettingProjectsDirectory             HomeSetting    = "projects_directory"
	SettingUIScale                       HomeSetting    = "uiScale"
	SettingGardenScale                   HomeSetting    = "gardenScale"
	SettingModelTierOverrides            HomeSetting    = "model_tier_overrides"
	SettingClaudeExecutable              MachineSetting = "claude_executable"
	SettingCodexExecutable               MachineSetting = "codex_executable"
	SettingEditorExecutable              HomeSetting    = "editor_executable"
	SettingNewSessionAgent               HomeSetting    = "new_session_agent"
	SettingClaudeAvailable               MachineSetting = "claude_available"
	SettingCodexAvailable                MachineSetting = "codex_available"
	SettingPTYBackendMode                MachineSetting = "pty_backend_mode"
	SettingNewSessionLauncherEnabled     HomeSetting    = "new_session_launcher_enabled"
	SettingDirectAgentLaunchEnabled      HomeSetting    = "direct_agent_launch_enabled"
	SettingSharedPTYHostEnabled          MachineSetting = "pty_shared_host_enabled"
	SettingSharedPTYHostActive           MachineSetting = "pty_shared_host_active"
	SettingTheme                         HomeSetting    = "theme"
	SettingReviewerModel                 HomeSetting    = "reviewer_model"
	SettingTailscaleEnabled              HomeSetting    = "tailscale_enabled"
	SettingQueueModeEnabled              HomeSetting    = "queue_mode_enabled"
	SettingQueueCrewEnabled              HomeSetting    = "queue_crew_enabled"
	SettingQueueShowHiddenSessions       HomeSetting    = "queue_show_hidden_sessions"
	SettingQueueQuietNotifications       HomeSetting    = "queue_quiet_notifications"
	SettingCodexSharedEnabled            HomeSetting    = "codex_shared_enabled"
	SettingNisseEnabled                  HomeSetting    = "nisse_enabled"
	SettingSidebarHarnessLogosEnabled    HomeSetting    = "sidebar_harness_logos_enabled"
	SettingAutoApproveEnabled            HomeSetting    = "auto_approve_enabled"
	SettingOpenSentFilesEnabled          HomeSetting    = "open_sent_files_enabled"
	SettingAutoSettleEnabled             HomeSetting    = "auto_settle_enabled"
	SettingAsksEnabled                   HomeSetting    = "asks_enabled"
	SettingCaptureShortcut               HomeSetting    = "capture.shortcut"
	SettingQuickCaptureEnabled           HomeSetting    = "quick_capture_enabled"
	SettingCrewV2Enabled                 HomeSetting    = "crew_v2_enabled"
	SettingCalloutsEnabled               HomeSetting    = "callouts_enabled"
	SettingAutoSettleArmSeconds          HomeSetting    = "auto_settle_arm_seconds"
	SettingAutoSettleCountdownSeconds    HomeSetting    = "auto_settle_countdown_seconds"
	SettingKeybindingsConfig             HomeSetting    = "keybindings_config"
	SettingNewSessionYoloPrefix          HomeSetting    = "new_session_yolo_"
	SettingNewSessionDestinationPrefix   HomeSetting    = "new_session_destination_"
	SettingChiefModelPrefix              HomeSetting    = "chief_model_"
	SettingChiefEffortPrefix             HomeSetting    = "chief_effort_"
	SettingDefaultModelPrefix            HomeSetting    = "default_model_"
	SettingDefaultEffortPrefix           HomeSetting    = "default_effort_"
	SettingNotebookRoot                  MachineSetting = "notebook.root"
	SettingNotebookRootEffective         MachineSetting = "notebook.root.effective"
	SettingAutoModeEnabledDefault        HomeSetting    = "automode_enabled_default"
	SettingActivityEnabled               HomeSetting    = "activity.enabled"
	SettingActivityConfig                HomeSetting    = "activity.config"
	SettingActivityIntervals             HomeSetting    = "activity.intervals"
	SettingActivityPresenceIdleSeconds   HomeSetting    = "activity.presence_idle_seconds"
	SettingGardenAdvisor                 HomeSetting    = "garden.advisor"
	SettingCrewHeartbeatEnabled          HomeSetting    = "crew.heartbeat_enabled"
	SettingCrewAutoSleepEnabled          HomeSetting    = "crew.autosleep_enabled"
	SettingCrewCacheTTLSeconds           HomeSetting    = "crew.cache_ttl_seconds"
	SettingCrewCacheTTLPrefix            HomeSetting    = "crew.cache_ttl_seconds."
	SettingCrewHeartbeatLeadSeconds      HomeSetting    = "crew.heartbeat_lead_seconds"
	SettingCrewAwaySeconds               HomeSetting    = "crew.away_seconds"
	SettingCrewWakeLimit                 HomeSetting    = "crew.wake_limit"
	SettingCrewWakeLimitWindowSeconds    HomeSetting    = "crew.wake_limit_window_seconds"
	SettingChiefContextWindowCap         HomeSetting    = "chief_context_window_cap"
	SettingHeadlessContextWindowCap      HomeSetting    = "headless_context_window_cap"
	SettingDefaultContextWindowCapPrefix HomeSetting    = "default_context_window_cap_"
	SettingHeadlessTasksEnabled          HomeSetting    = "headless_tasks.enabled"
	SettingHeadlessTasksEnabledStored    HomeSetting    = "headless_tasks.enabled" + ".stored"
	SettingHeadlessTasksEnabledOverride  HomeSetting    = "headless_tasks.enabled" + ".override"
	SettingDBLastBackupAt                MachineSetting = "db.last_backup_at"
	SettingAnalyticsEnabled              MachineSetting = "analytics.enabled"
	SettingAnalyticsSharingEnabled       HomeSetting    = "analytics.sharing_enabled"
	SettingAnalyticsShareVents           HomeSetting    = "analytics.share_vents"
	SettingAnalyticsToolVents            HomeSetting    = "analytics.tool_vents"
	SettingWorktreeSweepEnabled          MachineSetting = "worktree_sweep_enabled"
	SettingSessionsFilters               HomeSetting    = "sessions.filters"
	SettingPluginPriorities              HomeSetting    = "plugin_provider_priorities"
	SettingInstalledBundledPlugins       HomeSetting    = "installed_bundled_plugins"
	SettingAutoModeDenialCursor          HomeSetting    = "automode_denial_ledger_cursor"
)

func ClassOf(key string) Class {
	for _, prefix := range []string{
		string(SettingChiefModelPrefix), string(SettingChiefEffortPrefix), string(SettingDefaultModelPrefix),
		string(SettingDefaultEffortPrefix), string(SettingDefaultContextWindowCapPrefix), string(SettingNewSessionYoloPrefix),
		string(SettingNewSessionDestinationPrefix), string(SettingCrewCacheTTLPrefix), "session_cost.price.", "session_cost.billed_as.",
	} {
		if strings.HasPrefix(key, prefix) {
			return Home
		}
	}
	switch key {
	case string(SettingOutpostsEnabled), string(SettingOutpostState), string(SettingOutpostHome), string(SettingOutpostLastError), string(SettingClaudeExecutable), string(SettingCodexExecutable), string(SettingPTYBackendMode), string(SettingSharedPTYHostEnabled), string(SettingSharedPTYHostActive), string(SettingNotebookRoot), string(SettingNotebookRootEffective), string(SettingAnalyticsEnabled), string(SettingWorktreeSweepEnabled), string(SettingClaudeAvailable), string(SettingCodexAvailable), string(SettingDBLastBackupAt), "notebook_sync_state", "notebook_sync_last_error":
		return Machine
	}
	if strings.HasSuffix(key, "_available") || strings.Contains(key, "_cap_") || strings.HasPrefix(key, "tailscale_") && key != string(SettingTailscaleEnabled) {
		return Machine
	}
	if strings.HasSuffix(key, "_executable") && key != string(SettingEditorExecutable) {
		return Machine
	}
	return Home
}
