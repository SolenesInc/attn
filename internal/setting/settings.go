package setting

var (
	AsksEnabled = feature("asks_enabled", "Enable agent asks: true or false. On by default; applies to new agents.", Info{
		Name:    "Asks",
		Summary: "Answer agents' questions from one drawer.",
		Group:   Agents,
		Applies: NewAgents,
		Stage:   OnByDefault,
		Where:   "Agents and models → Session defaults",
		Since:   "2026-10-18",
		Search:  "questions answer decisions drawer",
	})
	SigmaEnabled = feature("sigma_enabled", "Offer Sigma as a harness: true or false.", Info{
		Name:    "Sigma",
		Summary: "Offer Sigma, Pi Durable behind an app-server, as a harness.",
		Group:   Agents,
		Applies: NewAgents,
		Stage:   Experimental,
		Since:   "2026-11-12",
		Search:  "harness server pi durable",
	})
	NewSessionLauncherEnabled = feature("new_session_launcher_enabled", "Show the new session launcher: true or false.", Info{
		Name:    "Launcher 2.0",
		Summary: "Pick a path, repository and worktree for a new agent or terminal in one dialog.",
		Group:   Interface,
		Applies: Now,
		Stage:   Experimental,
		Since:   "2026-10-16",
		Search:  "one-screen one screen new-session new session project path",
	})
	OutpostsEnabled = feature("outposts_enabled", "Connect to outposts from this home: true or false. Experimental; off by default.", Info{
		Name:    "Outposts",
		Summary: "Connect this home to attn on other computers.",
		Group:   Connections,
		Applies: Now,
		Stage:   Experimental,
		Since:   "2026-10-27",
		Search:  "remote computers bridge ssh",
	})
	PhoneAccessEnabled = feature("phone_access_enabled", "Let paired phones reach this home's sessions: true or false. Off by default.", Info{
		Name:    "Phone access",
		Summary: "Let phones you pair see this home's agents.",
		Note:    "Turning it off disconnects every phone.",
		Group:   Connections,
		Applies: Now,
		Stage:   Experimental,
		Since:   "2026-11-02",
		Search:  "mobile iphone pair",
	})
	AnalyticsEnabled = feature("analytics.enabled", "Record usage analytics on this home and its outposts: true or false. Experimental; off by default.", Info{
		Name:    "Usage analytics",
		Summary: "Keep a redacted record of how attn is used, here and on outposts.",
		Note:    "Nothing is sent unless you turn on sharing.",
		Group:   Data,
		Applies: Now,
		Stage:   Experimental,
		Since:   "2026-10-24",
		Search:  "telemetry vents record",
	})
	SelfUpdateEnabled = feature("self_update_enabled", "Let attn report and manage its own updates: true or false. Experimental; off by default.", Info{
		Name:    "Self-update",
		Summary: "See how attn was installed and whether an update is available.",
		Group:   UnderTheHood,
		Applies: Now,
		Stage:   Experimental,
		Since:   "2026-11-08",
		Search:  "update upgrade version release install",
	})
	DirectAgentLaunchEnabled = feature("direct_agent_launch_enabled", "Launch agents directly, without an attn process beside each one: true or false. On by default; applies to new and reloaded agents.", Info{
		Name:    "Direct agent launches",
		Summary: "Launch agents using less memory.",
		Group:   UnderTheHood,
		Applies: NewAgents,
		Stage:   OnByDefault,
		Where:   "Agents and models → Session defaults",
		Since:   "2026-10-15",
		Search:  "memory launcher",
	})
	CodeReviewEnabled = feature("code_review_enabled", "Let agents present a change for review as a review document: true or false. Experimental; off by default.", Info{
		Name:    "Code review",
		Summary: "Agents present a change as a review document that resolves against its repository.",
		Group:   Agents,
		Applies: Now,
		Stage:   Experimental,
		Since:   "2026-11-10",
		Search:  "review diff change walkthrough attn review",
	})
	SharedPTYHostEnabled = feature("pty_shared_host_enabled", "Use the shared terminal host, and stream output only to terminals the window draws: true or false. On by default; applies to new and reloaded terminals.", Info{
		Name:    "Shared terminal host",
		Summary: "Reduce memory use by sharing a host process across terminals, and stream output only to the terminals the window shows.",
		Group:   UnderTheHood,
		Applies: NewTerminals,
		Stage:   OnByDefault,
		Where:   "Agents and models → Session defaults",
		Since:   "2026-09-04",
		Search:  "terminal hosting pty backend rust workers memory",
	})
)

var (
	NotebookSyncState            = add(&Setting{Key: "notebook_sync_state", Class: Machine, Kind: Computed, Description: "Current Notebook sync state on this machine."})
	NotebookSyncLastError        = add(&Setting{Key: "notebook_sync_last_error", Class: Machine, Kind: Computed, Description: "Current Notebook sync error and its reason."})
	OutpostState                 = add(&Setting{Key: "outpost_state", Class: Machine, Kind: Computed, Description: "Whether a home bridge is connected: bridged or off."})
	OutpostHome                  = add(&Setting{Key: "outpost_home", Class: Machine, Kind: Computed, Description: "Recorded home daemon id. Release it with attn enrollment leave."})
	UpdateInstallKind            = add(&Setting{Key: "update_install_kind", Class: Machine, Kind: Computed, Description: "How attn was installed on this machine."})
	UpdateCurrentVersion         = add(&Setting{Key: "update_current_version", Class: Machine, Kind: Computed, Description: "The version of the running build."})
	UpdateCurrentCommit          = add(&Setting{Key: "update_current_commit", Class: Machine, Kind: Computed, Description: "The commit of the running build."})
	UpdateAvailableVersion       = add(&Setting{Key: "update_available_version", Class: Machine, Kind: Computed, Description: "The newer published release, if any."})
	UpdateAvailableURL           = add(&Setting{Key: "update_available_url", Class: Machine, Kind: Computed, Description: "The web page of the newer published release, if any."})
	UpdateCheckedAt              = add(&Setting{Key: "update_checked_at", Class: Machine, Kind: Computed, Description: "When attn last looked for a published release."})
	UpdateCheckError             = add(&Setting{Key: "update_check_error", Class: Machine, Kind: Computed, Description: "Why the last look for a published release failed."})
	OutpostLastError             = add(&Setting{Key: "outpost_last_error", Class: Machine, Kind: Computed, Description: "Last outpost bridge error, kept until the next successful connection."})
	CaptureShortcut              = add(&Setting{Key: "capture.shortcut", Class: Home, Kind: Free, Description: "Global shortcut for Quick Capture. Empty turns the shortcut off."})
	QueueShowHiddenSessions      = add(&Setting{Key: "queue_show_hidden_sessions", Class: Home, Kind: Bool, Default: On, Description: "Include hidden sessions in the queue: true or false. On by default."})
	QueueQuietNotifications      = add(&Setting{Key: "queue_quiet_notifications", Class: Home, Kind: Bool, Default: On, Description: "Keep agents a notification woke out of the queue unless they open their turn: true or false. On by default; needs queue flow."})
	AnalyticsSharingEnabled      = add(&Setting{Key: "analytics.sharing_enabled", Class: Home, Kind: Bool, Description: "Upload the recorded usage analytics to attn's maintainers: true or false. Needs analytics.enabled; off by default."})
	AnalyticsShareVents          = add(&Setting{Key: "analytics.share_vents", Class: Home, Kind: Bool, Description: "Send each new attn vent, with its text, in the next upload: true or false. Off by default."})
	AnalyticsToolVents           = add(&Setting{Key: "analytics.tool_vents", Class: Home, Kind: Bool, Description: "Let new sessions also vent about their other tools, such as skills, other CLIs and harness permissions; tool vents never leave the machine: true or false. Off by default."})
	ClaudeExecutable             = add(&Setting{Key: "claude_executable", Class: Machine, Kind: Checked, Description: "Executable for Claude. Empty finds the default executable on PATH."})
	CodexExecutable              = add(&Setting{Key: "codex_executable", Class: Machine, Kind: Checked, Description: "Executable for Codex. Empty finds the default executable on PATH."})
	ModelTierOverrides           = add(&Setting{Key: "model_tier_overrides", Class: Home, Kind: Checked, Description: "Models for each intelligence tier as a JSON object. Empty uses harness defaults."})
	ProjectsDirectory            = add(&Setting{Key: "projects_directory", Class: Home, Kind: Checked, Description: "Folder used to discover projects. Absolute or ~/ path; must not be empty."})
	UIScale                      = add(&Setting{Key: "uiScale", Class: Home, Kind: Checked, Description: "App interface scale, from 0.5 to 2.0."})
	GardenScale                  = add(&Setting{Key: "gardenScale", Class: Home, Kind: Checked, Description: "Garden text scale, from 0.5 to 2.0. Empty uses the app scale."})
	EditorExecutable             = add(&Setting{Key: "editor_executable", Class: Home, Kind: Checked, Description: "Command to open files in your editor. Empty uses the default editor."})
	NewSessionAgent              = add(&Setting{Key: "new_session_agent", Class: Home, Kind: Checked, Description: "Harness selected for new sessions. Empty uses the default harness."})
	Theme                        = add(&Setting{Key: "theme", Class: Home, Kind: Checked, Description: "App color theme: dark, light or system."})
	NarrowestTileCols            = add(&Setting{Key: "narrowest_tile_cols", Class: Home, Kind: Checked, Description: "Narrowest width of a tile in a desktop's row, in terminal columns: a positive whole number. Empty uses the default of 66."})
	SidebarWidth                 = add(&Setting{Key: "sidebar_width", Class: Home, Kind: Checked, Description: "Width of the open sidebar in pixels, from 200 to 600. Empty uses the default of 240."})
	AutoApproveEnabled           = add(&Setting{Key: "auto_approve_enabled", Class: Home, Kind: Bool, Default: On, Description: "Auto-approve supported agent requests: true or false."})
	QueueModeEnabled             = add(&Setting{Key: "queue_mode_enabled", Class: Home, Kind: Bool, Description: "Use queue flow across all profiles: true or false."})
	QueueCrewEnabled             = add(&Setting{Key: "queue_crew_enabled", Class: Home, Kind: Bool, Description: "Include crew sessions in the queue: true or false."})
	SidebarHarnessLogosEnabled   = add(&Setting{Key: "sidebar_harness_logos_enabled", Class: Home, Kind: Bool, Default: On, Description: "Show harness logos in the sidebar: true or false."})
	AutoSettleEnabled            = add(&Setting{Key: "auto_settle_enabled", Class: Home, Kind: Bool, Description: "Settle completed turns automatically: true or false."})
	ActivityEnabled              = add(&Setting{Key: "activity.enabled", Class: Home, Kind: Bool, Description: "Track session activity: true or false."})
	OpenSentFilesEnabled         = add(&Setting{Key: "open_sent_files_enabled", Class: Home, Kind: Bool, Default: On, Description: "Open files sent by agents: true or false."})
	HeadlessTasksEnabled         = add(&Setting{Key: "headless_tasks.enabled", Class: Home, Kind: Bool, Default: On, Description: "Run background model tasks: true or false. An environment override takes precedence."})
	WorktreeSweepEnabled         = add(&Setting{Key: "worktree_sweep_enabled", Class: Machine, Kind: Bool, Default: On, Description: "Remove unused worktrees automatically: true or false."})
	AutoSettleArmSeconds         = add(&Setting{Key: "auto_settle_arm_seconds", Class: Home, Kind: Checked, Description: "Delay before arming automatic settlement, in seconds. Empty uses the default delay."})
	AutoSettleCountdownSeconds   = add(&Setting{Key: "auto_settle_countdown_seconds", Class: Home, Kind: Checked, Description: "Automatic settlement countdown, in seconds. Empty uses the default countdown."})
	HeadlessContextWindowCap     = add(&Setting{Key: "headless_context_window_cap", Class: Home, Kind: Checked, Description: "Background model context budget in tokens, from 10000 to 2000000. Empty uses the default budget."})
	ActivityConfig               = add(&Setting{Key: "activity.config", Class: Home, Kind: Checked, Description: "Session activity tracking configuration as JSON. Empty leaves no agent selected; activity requires a selected agent."})
	GardenAdvisor                = add(&Setting{Key: "garden.advisor", Class: Home, Kind: Checked, Description: "Garden advisor harness and model as JSON. Empty uses the default advisor."})
	DictationRefinementAgent     = add(&Setting{Key: "dictation.refinement_agent", Class: Home, Kind: Checked, Description: "Harness whose light model cleans up dictated text: claude or codex. Empty uses claude."})
	ActivityIntervals            = add(&Setting{Key: "activity.intervals", Class: Home, Kind: Checked, Description: "Activity sampling intervals as JSON. Empty uses defaults."})
	ActivityPresenceIdleSeconds  = add(&Setting{Key: "activity.presence_idle_seconds", Class: Home, Kind: Checked, Description: "Seconds without input before presence becomes idle."})
	CrewHeartbeatEnabled         = add(&Setting{Key: "crew.heartbeat_enabled", Class: Home, Kind: Bool, Default: On, Description: "Send crew heartbeats: true or false."})
	CrewAwayCloseEnabled         = add(&Setting{Key: "crew.away_close_enabled", Class: Home, Kind: Bool, Default: On, Description: "Close idle crew members' sessions while you are away: true or false."})
	CrewCacheTTLSeconds          = add(&Setting{Key: "crew.cache_ttl_seconds", Class: Home, Kind: Checked, Description: "Crew prompt cache lifetime in seconds."})
	CrewHeartbeatLeadSeconds     = add(&Setting{Key: "crew.heartbeat_lead_seconds", Class: Home, Kind: Checked, Description: "Seconds before cache expiry to send a crew heartbeat."})
	CrewAwaySeconds              = add(&Setting{Key: "crew.away_seconds", Class: Home, Kind: Checked, Description: "Seconds of user inactivity before attn closes idle crew members' sessions."})
	CrewMailStartLimit           = add(&Setting{Key: "crew.mail_start_limit", Class: Home, Kind: Checked, Description: "Maximum crew sessions attn starts for arriving mail per window. Zero turns mail-started sessions off."})
	CrewMailStartWindowSeconds   = add(&Setting{Key: "crew.mail_start_window_seconds", Class: Home, Kind: Checked, Description: "Window in seconds over which crew.mail_start_limit counts mail-started sessions."})
	NotebookRoot                 = add(&Setting{Key: "notebook.root", Class: Home, Kind: Checked, Scope: PerProfile, Description: "This profile's folder for the Notebook, journal and seed artifacts, synced with outposts. Absolute or ~/ path. It must be outside attn's data folder, and neither inside nor around another profile's folder or the default folder. Changing it repoints without moving files. Empty uses the default folder."})
	PullRequestOwnComments       = add(&Setting{Key: "pull_requests.own_comments", Class: Home, Kind: Checked, Scope: PerProfile, Description: "How a pull request watch treats the comments and review bodies your GitHub account wrote, and the reopened review threads you started on your own pull requests: quiet skips them, ring delivers them to the agent. Agents post as you, so quiet keeps an agent's own comments from coming back to it; ring lets you steer agents with your comments. Comments by others and state changes always reach the agent. Empty means quiet, the default."})
	GardenTenderResume           = add(&Setting{Key: "garden.tender_resume", Class: Home, Kind: Checked, Scope: PerProfile, Description: "Which seed mail resumes the closed session tending a seed on standby: any resumes it for every message, pull request watch update and reminder; watches only for pull request watch updates and reminders; off never resumes a session, and the mail waits. attn starts a session for a crew member tending a seed under every value. Empty means any, the default."})
	KeybindingsConfig            = add(&Setting{Key: "keybindings_config", Class: Home, Kind: Checked, Description: "Keyboard shortcut overrides as JSON. Empty restores default shortcuts."})
	SessionsFilters              = add(&Setting{Key: "sessions.filters", Class: Home, Kind: Checked, Description: "Saved ledger filters as JSON. Empty restores default filters."})
	CodeNavIdleStop              = add(&Setting{Key: "code_navigation_idle_stop_seconds", Class: Home, Kind: Checked, Description: "Seconds a language server keeps running after the last shown review that needs it closes, from 0 to 86400. Empty uses 300."})
	CodeNavDefinition            = add(&Setting{Key: "code_navigation_<origin>_<language>", Class: Home, Kind: Checked, Description: "How to launch the language server for a language in the repositories with this origin, as JSON: {\"command\": [...], \"extensions\": [...], \"roots\": [...], \"settings\": {...}, \"prepare\": {\"command\": [...], \"inputs\": [...]}}. Set it with `attn nav set`. Empty uses the built-in definition."})
	CodeNavSetupOffer            = add(&Setting{Key: "code_review_setup_offer_<repository>", Class: Home, Kind: Checked, Description: "Set to off to stop a review of this repository from offering to set up a language server. The s key in the review still starts setup. Use local_<absolute-path> as the suffix for this machine, or <outpost-id>_<absolute-path> for a repository on an outpost. Empty offers setup."})
	ReviewerModel                = add(&Setting{Key: "reviewer_model", Class: Home, Kind: Free, Description: "Review model. Empty uses the default review model."})
	Executable                   = add(&Setting{Key: "<harness>_executable", Class: Machine, Kind: Checked, Description: "Executable for this harness. Empty finds the default executable on PATH."})
	NewSessionYolo               = add(&Setting{Key: "new_session_yolo_<location>", Class: Home, Kind: Bool, Description: "Start sessions at this location with approval bypass: true or false. The local location uses new_session_yolo_local."})
	NewSessionDestination        = add(&Setting{Key: "new_session_destination_<repository>", Class: Home, Kind: Checked, Description: "New session destination for a repository: new_worktree or main_repo. Use local_<absolute-path> as the suffix for this machine, or <outpost-id>_<absolute-path> for a repository on an outpost. Empty uses the default destination."})
	DefaultModel                 = add(&Setting{Key: "default_model_<harness>", Class: Home, Kind: Free, Description: "Model for new sessions. Empty uses the harness default."})
	DefaultEffort                = add(&Setting{Key: "default_effort_<harness>", Class: Home, Kind: Free, Description: "Reasoning effort for new sessions. Empty uses the harness default."})
	DefaultContextWindowCap      = add(&Setting{Key: "default_context_window_cap_<harness>", Class: Home, Kind: Checked, Description: "Context budget for new sessions, from 10000 to 2000000 tokens. Empty uses the harness default."})
	CrewCacheTTLFor              = add(&Setting{Key: "crew.cache_ttl_seconds.<harness>", Class: Home, Kind: Checked, Description: "Crew prompt cache lifetime for this harness, in seconds."})
	SessionCostPrice             = add(&Setting{Key: "session_cost.price.<model>", Class: Home, Kind: Checked, Description: "Custom model token prices as JSON. Empty uses catalog prices."})
	SessionCostBilledAs          = add(&Setting{Key: "session_cost.billed_as.<model>", Class: Home, Kind: Checked, Description: "Billing model alias. Empty removes the alias."})
	NotebookRootEffective        = add(&Setting{Key: "notebook.root.effective", Class: Machine, Kind: Computed, Scope: PerProfile, Description: "The Notebook folder this profile uses."})
	Available                    = add(&Setting{Key: "<harness>_available", Class: Machine, Kind: Computed, Description: "Whether this harness executable is available on PATH: true, false, or checking while the daemon reads the login environment."})
	Capability                   = add(&Setting{Key: "<harness>_cap_<capability>", Class: Machine, Kind: Computed, Description: "Whether this harness supports the named capability."})
	PTYBackendMode               = add(&Setting{Key: "pty_backend_mode", Class: Machine, Kind: Computed, Description: "Active terminal backend."})
	SharedPTYHostActive          = add(&Setting{Key: "pty_shared_host_active", Class: Machine, Kind: Computed, Description: "Whether the shared terminal host is active."})
	DBLastBackupAt               = add(&Setting{Key: "db.last_backup_at", Class: Machine, Kind: Computed, Description: "Time of the last database backup."})
	HeadlessTasksEnabledStored   = add(&Setting{Key: "headless_tasks.enabled.stored", Class: Home, Kind: Computed, Description: "Stored background model task switch."})
	HeadlessTasksEnabledOverride = add(&Setting{Key: "headless_tasks.enabled.override", Class: Home, Kind: Computed, Description: "Environment override for background model tasks."})
	PluginPriorities             = add(&Setting{Key: "plugin_provider_priorities", Class: Home, Kind: Free, Internal: true})
	AnalyticsSubjectSecret       = add(&Setting{Key: "analytics.subject_secret", Class: Home, Kind: Free, Internal: true})
	AnalyticsClearedAt           = add(&Setting{Key: "analytics.cleared_at", Class: Home, Kind: Free, Internal: true})
)
