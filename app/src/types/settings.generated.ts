// Generated from internal/setting by make generate-types. Do not edit.

export const BoolSetting = {
  asksEnabled: "asks_enabled",
  quickCaptureEnabled: "quick_capture_enabled",
  crewV2Enabled: "crew_v2_enabled",
  nisseEnabled: "nisse_enabled",
  claudeModEnabled: "claude_mod_enabled",
  codexSharedEnabled: "codex_shared_enabled",
  newSessionLauncherEnabled: "new_session_launcher_enabled",
  calloutsEnabled: "callouts_enabled",
  outpostsEnabled: "outposts_enabled",
  phoneAccessEnabled: "phone_access_enabled",
  analyticsEnabled: "analytics.enabled",
  directAgentLaunchEnabled: "direct_agent_launch_enabled",
  ptySharedHostEnabled: "pty_shared_host_enabled",
  queueShowHiddenSessions: "queue_show_hidden_sessions",
  queueQuietNotifications: "queue_quiet_notifications",
  analyticsSharingEnabled: "analytics.sharing_enabled",
  analyticsShareVents: "analytics.share_vents",
  analyticsToolVents: "analytics.tool_vents",
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
  "direct_agent_launch_enabled",
  "pty_shared_host_enabled",
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

export interface Feature {
  key: BoolSettingKey;
  name: string;
  summary: string;
  note: string;
  group: string;
  timing: string;
  stage: string;
  since: string;
  search: string;
}

export const FEATURES: readonly Feature[] = [
  { key: "asks_enabled", name: "Asks", summary: "Answer agents' questions from one drawer.", note: "", group: "Agents", timing: "Starts with the next agent you open.", stage: "experimental", since: "2026-10-18", search: "questions answer decisions drawer" },
  { key: "quick_capture_enabled", name: "Quick Capture", summary: "Send messages and files to Chief or a crew member from any app.", note: "", group: "Agents", timing: "", stage: "experimental", since: "2026-10-19", search: "global shortcut panel" },
  { key: "crew_v2_enabled", name: "Crew v2", summary: "Crew members use the v2 operating model, with harness memory off.", note: "", group: "Agents", timing: "Starts with the next agent you open.", stage: "experimental", since: "2026-10-27", search: "chief crew memory letters" },
  { key: "nisse_enabled", name: "Nisse", summary: "Offer Nisse as a harness.", note: "", group: "Agents", timing: "Starts with the next agent you open.", stage: "experimental", since: "2026-10-29", search: "harness server" },
  { key: "claude_mod_enabled", name: "Claude mod", summary: "Deliver messages to Claude through a Claude Code mod instead of typing.", note: "", group: "Agents", timing: "Starts with the next agent you open. Turning it off applies at once.", stage: "experimental", since: "2026-11-04", search: "delivery typing standby compact" },
  { key: "codex_shared_enabled", name: "Shared Codex", summary: "Run one Codex server for every session, using less memory.", note: "", group: "Agents", timing: "Starts with the next agent you open.", stage: "experimental", since: "2026-10-16", search: "app server memory" },
  { key: "new_session_launcher_enabled", name: "Launcher 2.0", summary: "Pick a path, repository and worktree for a new agent or terminal in one dialog.", note: "", group: "Interface", timing: "", stage: "experimental", since: "2026-10-16", search: "one-screen one screen new-session new session project path" },
  { key: "callouts_enabled", name: "Agent callouts", summary: "Let agents point at parts of the app, step by step.", note: "", group: "Interface", timing: "", stage: "experimental", since: "2026-10-18", search: "point guided steps tour" },
  { key: "outposts_enabled", name: "Outposts", summary: "Connect this home to attn on other computers.", note: "", group: "Connections", timing: "", stage: "experimental", since: "2026-10-27", search: "remote computers bridge ssh" },
  { key: "phone_access_enabled", name: "Phone access", summary: "Let phones you pair see this home's agents.", note: "Turning it off disconnects every phone.", group: "Connections", timing: "", stage: "experimental", since: "2026-11-02", search: "mobile iphone pair" },
  { key: "analytics.enabled", name: "Usage analytics", summary: "Keep a redacted record of how attn is used, here and on outposts.", note: "Nothing is sent unless you turn on sharing.", group: "Data", timing: "", stage: "experimental", since: "2026-10-24", search: "telemetry vents record" },
  { key: "direct_agent_launch_enabled", name: "Direct agent launches", summary: "Launch agents using less memory.", note: "", group: "Under the hood", timing: "Starts with the next agent you open.", stage: "on by default", since: "2026-10-15", search: "memory launcher" },
  { key: "pty_shared_host_enabled", name: "Shared terminal host", summary: "Reduce memory use by sharing a host process across terminals, and stream output only to the terminals the window shows.", note: "", group: "Under the hood", timing: "Starts with the next terminal you open.", stage: "on by default", since: "2026-09-04", search: "terminal hosting pty backend rust workers memory" },
];
