// Generated from internal/setting by make generate-types. Do not edit.

export const BoolSetting = {
  outpostsEnabled: "outposts_enabled",
  quickCaptureEnabled: "quick_capture_enabled",
  crewV2Enabled: "crew_v2_enabled",
  newSessionLauncherEnabled: "new_session_launcher_enabled",
  directAgentLaunchEnabled: "direct_agent_launch_enabled",
  codexSharedEnabled: "codex_shared_enabled",
  claudeModEnabled: "claude_mod_enabled",
  nisseEnabled: "nisse_enabled",
  queueShowHiddenSessions: "queue_show_hidden_sessions",
  queueQuietNotifications: "queue_quiet_notifications",
  asksEnabled: "asks_enabled",
  analyticsEnabled: "analytics.enabled",
  analyticsSharingEnabled: "analytics.sharing_enabled",
  analyticsShareVents: "analytics.share_vents",
  analyticsToolVents: "analytics.tool_vents",
  calloutsEnabled: "callouts_enabled",
  phoneAccessEnabled: "phone_access_enabled",
  ptySharedHostEnabled: "pty_shared_host_enabled",
  shelfLayoutEnabled: "shelf_layout_enabled",
  backgroundLoginEnvEnabled: "background_login_env_enabled",
  autoApproveEnabled: "auto_approve_enabled",
  queueModeEnabled: "queue_mode_enabled",
  queueCrewEnabled: "queue_crew_enabled",
  sidebarHarnessLogosEnabled: "sidebar_harness_logos_enabled",
  autoSettleEnabled: "auto_settle_enabled",
  activityEnabled: "activity.enabled",
  openSentFilesEnabled: "open_sent_files_enabled",
  headlessTasksEnabled: "headless_tasks.enabled",
  worktreeSweepEnabled: "worktree_sweep_enabled",
  crewHeartbeatEnabled: "crew.heartbeat_enabled",
  crewAutosleepEnabled: "crew.autosleep_enabled",
} as const;

export type BoolSettingKey = (typeof BoolSetting)[keyof typeof BoolSetting];

export const DEFAULT_ON_SETTINGS: ReadonlySet<string> = new Set([
  "queue_show_hidden_sessions",
  "queue_quiet_notifications",
  "auto_approve_enabled",
  "sidebar_harness_logos_enabled",
  "open_sent_files_enabled",
  "headless_tasks.enabled",
  "worktree_sweep_enabled",
  "crew.heartbeat_enabled",
  "crew.autosleep_enabled",
]);
