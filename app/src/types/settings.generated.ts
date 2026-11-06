// Generated from internal/setting by make generate-types. Do not edit.

export const BoolSetting = {
  asksEnabled: "asks_enabled",
  quickCaptureEnabled: "quick_capture_enabled",
  crewV2Enabled: "crew_v2_enabled",
  nisseEnabled: "nisse_enabled",
  claudeModEnabled: "claude_mod_enabled",
  codexSharedEnabled: "codex_shared_enabled",
  newSessionLauncherEnabled: "new_session_launcher_enabled",
  shelfLayoutEnabled: "shelf_layout_enabled",
  calloutsEnabled: "callouts_enabled",
  outpostsEnabled: "outposts_enabled",
  phoneAccessEnabled: "phone_access_enabled",
  analyticsEnabled: "analytics.enabled",
  directAgentLaunchEnabled: "direct_agent_launch_enabled",
  ptySharedHostEnabled: "pty_shared_host_enabled",
  backgroundLoginEnvEnabled: "background_login_env_enabled",
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
  group: string;
  applies: string;
  thisMachine: boolean;
  stage: string;
  since: string;
  search: string;
}

export const FEATURES: readonly Feature[] = [
  { key: "asks_enabled", name: "Asks", summary: "Answer agents' questions from one drawer without opening their terminals.", group: "Agents", applies: "New agents", thisMachine: false, stage: "experimental", since: "2026-10-18", search: "questions answer decisions drawer" },
  { key: "quick_capture_enabled", name: "Quick Capture", summary: "Send messages and files to Chief or a crew member from any app with a global shortcut.", group: "Agents", applies: "Now", thisMachine: false, stage: "experimental", since: "2026-10-19", search: "global shortcut panel" },
  { key: "crew_v2_enabled", name: "Crew v2", summary: "Crew members use the v2 operating model with harness memory off, starting at their next launch. Letters filed while it's on record their session so attn can keep its transcript.", group: "Agents", applies: "New agents", thisMachine: false, stage: "experimental", since: "2026-10-27", search: "chief crew memory letters" },
  { key: "nisse_enabled", name: "Nisse", summary: "Offer Nisse as a harness. attn runs the Nisse server and follows each session's state from it.", group: "Agents", applies: "New agents", thisMachine: false, stage: "experimental", since: "2026-10-29", search: "harness server" },
  { key: "claude_mod_enabled", name: "Claude mod", summary: "Deliver messages to Claude sessions through a Claude Code mod instead of typing into the terminal. A session loads the mod when it next launches.", group: "Agents", applies: "On: new agents. Off: now", thisMachine: false, stage: "experimental", since: "2026-11-04", search: "delivery typing standby compact" },
  { key: "codex_shared_enabled", name: "Shared Codex", summary: "Use a shared Codex server for cross-session interaction, tighter integration with attn, and lower memory use.", group: "Agents", applies: "New agents", thisMachine: false, stage: "experimental", since: "2026-10-16", search: "app server memory" },
  { key: "new_session_launcher_enabled", name: "Launcher 2.0", summary: "New dialog for launching agents and terminals. Allows you to select a path, then repository, and worktree, in a single dialog.", group: "Interface", applies: "Now", thisMachine: false, stage: "experimental", since: "2026-10-16", search: "one-screen one screen new-session new session project path" },
  { key: "shelf_layout_enabled", name: "Shelf layout", summary: "Lay out each desktop as one row of tiles that slides sideways, with the rest waiting on a shelf, instead of splits.", group: "Interface", applies: "Now", thisMachine: false, stage: "experimental", since: "2026-11-05", search: "narrowest tile width row columns" },
  { key: "callouts_enabled", name: "Agent callouts", summary: "Let agents point at parts of the app with short, guided steps.", group: "Interface", applies: "Now", thisMachine: false, stage: "experimental", since: "2026-10-18", search: "point guided steps tour" },
  { key: "outposts_enabled", name: "Outposts", summary: "Connect this home to attn on other computers. Add them in Settings → Outposts.", group: "Connections", applies: "Now", thisMachine: false, stage: "experimental", since: "2026-10-27", search: "remote computers bridge ssh" },
  { key: "phone_access_enabled", name: "Phone access", summary: "Let phones you pair see this home's agents. Turning it off disconnects every phone.", group: "Connections", applies: "Now", thisMachine: false, stage: "experimental", since: "2026-11-02", search: "mobile iphone pair" },
  { key: "analytics.enabled", name: "Usage analytics", summary: "Keep a redacted record of how attn is used on this home and its outposts. Nothing is sent.", group: "Data", applies: "Now", thisMachine: false, stage: "experimental", since: "2026-10-24", search: "telemetry vents record" },
  { key: "direct_agent_launch_enabled", name: "Direct agent launches", summary: "Launch agents using less memory.", group: "Under the hood", applies: "New agents", thisMachine: false, stage: "on by default", since: "2026-10-15", search: "memory launcher" },
  { key: "pty_shared_host_enabled", name: "Shared terminal host", summary: "Reduce memory use by sharing a host process across terminals, and stream output only to the terminals the window shows.", group: "Under the hood", applies: "New terminals", thisMachine: true, stage: "on by default", since: "2026-09-04", search: "terminal hosting pty backend rust workers memory" },
  { key: "background_login_env_enabled", name: "Background login environment", summary: "Answer clients while the login shell runs, instead of waiting for it before listening.", group: "Under the hood", applies: "Next daemon start", thisMachine: false, stage: "experimental", since: "2026-11-04", search: "startup shell environment" },
];
