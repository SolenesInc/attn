// Generated from internal/setting by make generate-types. Do not edit.

export const BoolSetting = {
  asksEnabled: "asks_enabled",
  sigmaEnabled: "sigma_enabled",
  newSessionLauncherEnabled: "new_session_launcher_enabled",
  outpostsEnabled: "outposts_enabled",
  phoneAccessEnabled: "phone_access_enabled",
  analyticsEnabled: "analytics.enabled",
  selfUpdateEnabled: "self_update_enabled",
  directAgentLaunchEnabled: "direct_agent_launch_enabled",
  codeReviewEnabled: "code_review_enabled",
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
  crewAwayCloseEnabled: "crew.away_close_enabled",
} as const;

export type BoolSettingKey = (typeof BoolSetting)[keyof typeof BoolSetting];

export const DEFAULT_ON_SETTINGS: ReadonlySet<string> = new Set([
  "asks_enabled",
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
  "crew.away_close_enabled",
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
  { key: "asks_enabled", name: "Asks", summary: "Answer agents' questions from one drawer.", note: "", group: "Agents", timing: "Starts with the next agent you open.", stage: "on by default", since: "2026-10-18", search: "questions answer decisions drawer" },
  { key: "sigma_enabled", name: "Sigma", summary: "Offer Sigma, Pi Durable behind an app-server, as a harness.", note: "", group: "Agents", timing: "Starts with the next agent you open.", stage: "experimental", since: "2026-11-12", search: "harness server pi durable" },
  { key: "new_session_launcher_enabled", name: "Launcher 2.0", summary: "Pick a path, repository and worktree for a new agent or terminal in one dialog.", note: "", group: "Interface", timing: "", stage: "experimental", since: "2026-10-16", search: "one-screen one screen new-session new session project path" },
  { key: "outposts_enabled", name: "Outposts", summary: "Connect this home to attn on other computers.", note: "", group: "Connections", timing: "", stage: "experimental", since: "2026-10-27", search: "remote computers bridge ssh" },
  { key: "phone_access_enabled", name: "Phone access", summary: "Let phones you pair see this home's agents.", note: "Turning it off disconnects every phone.", group: "Connections", timing: "", stage: "experimental", since: "2026-11-02", search: "mobile iphone pair" },
  { key: "analytics.enabled", name: "Usage analytics", summary: "Keep a redacted record of how attn is used, here and on outposts.", note: "Nothing is sent unless you turn on sharing.", group: "Data", timing: "", stage: "experimental", since: "2026-10-24", search: "telemetry vents record" },
  { key: "self_update_enabled", name: "Self-update", summary: "See how attn was installed and whether an update is available.", note: "", group: "Under the hood", timing: "", stage: "experimental", since: "2026-11-08", search: "update upgrade version release install" },
  { key: "direct_agent_launch_enabled", name: "Direct agent launches", summary: "Launch agents using less memory.", note: "", group: "Under the hood", timing: "Starts with the next agent you open.", stage: "on by default", since: "2026-10-15", search: "memory launcher" },
  { key: "code_review_enabled", name: "Code review", summary: "Agents present a change as a review document that resolves against its repository.", note: "", group: "Agents", timing: "", stage: "experimental", since: "2026-11-10", search: "review diff change walkthrough attn review" },
  { key: "pty_shared_host_enabled", name: "Shared terminal host", summary: "Reduce memory use by sharing a host process across terminals, and stream output only to the terminals the window shows.", note: "", group: "Under the hood", timing: "Starts with the next terminal you open.", stage: "on by default", since: "2026-09-04", search: "terminal hosting pty backend rust workers memory" },
];
