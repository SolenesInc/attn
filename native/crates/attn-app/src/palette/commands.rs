use attn_protocol::CommandUsage;

use attn_protocol::go_values::FEATURES;

use super::{Icon, Look, Pick, Row};
use crate::setting;
use crate::sidebar::{model, queue, width};
use crate::{Action, State};

const AUTO_SETTLE: &str = "auto_settle_enabled";

fn set(key: &str, on: bool) -> Action {
    Action::SetSetting {
        key: key.into(),
        value: if on { "true" } else { "false" }.into(),
    }
}

/// A command the palette offers: its id is the key the daemon keeps its history under, shared
/// with the Tauri app.
pub(crate) struct Command {
    id: String,
    title: String,
    description: Option<String>,
    keywords: Vec<String>,
    pinned: bool,
    search_only: bool,
    action: Action,
    shortcut: Option<Action>,
}

impl Command {
    pub(crate) fn new(id: &str, title: &str, keywords: &[&str], action: Action) -> Command {
        Command {
            id: id.into(),
            title: title.into(),
            description: None,
            keywords: keywords.iter().map(|k| (*k).to_owned()).collect(),
            pinned: false,
            search_only: false,
            shortcut: None,
            action,
        }
    }

    pub(crate) fn describe(mut self, description: &str) -> Command {
        self.description = Some(description.into());
        self
    }

    fn shortcut(mut self, action: Option<Action>) -> Command {
        self.shortcut = action;
        self
    }

    fn search_only(mut self) -> Command {
        self.search_only = true;
        self
    }

    pub(crate) fn pin(mut self) -> Command {
        self.pinned = true;
        self
    }
}

/// Whether the agent of `leaf` has a delegate with a pane on `desktop`.
fn has_delegate_on(
    facts: &attn_client::Facts,
    desktop: &attn_protocol::Desktop,
    leaf: &str,
) -> bool {
    let Some(agent) = desktop.panes.iter().find(|p| p.pane_id == leaf) else {
        return false;
    };
    facts
        .sessions
        .iter()
        .filter(|s| s.dispatcher_session_id.as_deref() == Some(agent.session_id.as_str()))
        .any(|delegate| desktop.panes.iter().any(|p| p.session_id == delegate.id))
}

/// The commands the palette offers now, in the order they are declared: the ones the native
/// app runs and that apply to what the window shows.
pub(crate) fn catalog(state: &State) -> Vec<Command> {
    let facts = &state.facts;
    let on_screen = state
        .window
        .on_screen(facts)
        .and_then(|id| facts.sessions.iter().find(|s| s.id == id));
    let turn_actions = on_screen.map(|s| queue::actions(facts, s, state.window.now_ms));
    let mut commands = chief_commands(state);
    commands.extend(crate::shared_codex::commands(state));
    commands.extend([
        Command::new(
            "new-agent",
            "New agent",
            &["session", "spawn", "launch", "create"],
            Action::SessionNew,
        )
        .describe("Start an agent beside the active pane"),
        Command::new(
            "jump-to-waiting",
            "Jump to the oldest turn",
            &["queue", "waiting", "turn", "next"],
            Action::SessionJumpToWaiting,
        )
        .describe("Open the agent that has waited longest for you"),
    ]);
    if turn_actions.as_ref().is_some_and(|a| a.settle) {
        commands.push(
            Command::new(
                "settle-active-session",
                "Settle this agent",
                &["settle", "done", "queue", "turn"],
                Action::SessionSettle,
            )
            .describe("Close the turn it owes you")
            .pin(),
        );
    }
    if let Some(session) = on_screen {
        commands.push(
            Command::new(
                "reload-active-session",
                "Reload this agent",
                &["session", "reload", "restart", "relaunch"],
                Action::ReloadSession {
                    session: session.id.clone(),
                },
            )
            .describe("Restart the agent in its current pane")
            .pin(),
        );
    }
    commands.push(
        Command::new(
            "attention",
            "Open attention drawer",
            &["waiting", "pull requests", "prs", "notifications"],
            Action::OpenAttention,
        )
        .describe("Show sessions and pull requests that need a response")
        .shortcut(Some(Action::DockAttention)),
    );
    commands.push(
        Command::new(
            "whats-new",
            "What's new",
            &["whats new", "intro", "tour", "changes", "release", "help"],
            Action::OpenWhatsNew,
        )
        .describe("Replay the intro to profiles, desktops, the queue and the palettes"),
    );
    commands.push(
        Command::new(
            "create-diagnostic-report",
            "Create diagnostic report",
            &[
                "debug", "report", "logs", "dump", "keyboard", "typing", "stuck", "frozen", "error",
            ],
            Action::CreateDiagnosticReport,
        )
        .describe("Save a private troubleshooting report you can share"),
    );
    if crate::asks::available(facts) {
        commands.push(
            Command::new(
                "asks",
                "Open asks",
                &["ask", "asks", "questions", "answer", "reply", "inbox"],
                Action::ShowAsks { ask: None },
            )
            .describe("Answer what your agents asked, one ask after another")
            .shortcut(Some(Action::DockAsks)),
        );
    }
    commands.push(Command::new(
        "home",
        "Home",
        &["dashboard", "overview"],
        Action::SessionGoToDashboard,
    ));
    commands.push(
        Command::new(
            "desktop-overview",
            "Desktop overview",
            &["desktops", "workspaces"],
            Action::DesktopOverview,
        )
        .describe("Every desktop, with shortcuts and sending panes")
        .shortcut(Some(Action::DesktopOverview)),
    );
    let profile = &facts.profiles.selected;
    let desktops: Vec<_> = facts
        .desktops
        .iter()
        .filter(|d| &d.profile_id == profile)
        .cloned()
        .collect();
    if let Some(current) = facts.current_desktop() {
        commands.push(Command::new(
            "close-desktop",
            "Close desktop",
            &["desktop", "close"],
            Action::CloseDesktop {
                desktop: current.id.clone(),
            },
        ));
    }
    if !profile.is_empty() {
        commands.push(Command::new(
            "new-desktop",
            "New desktop",
            &["desktop", "create", "workspace"],
            Action::CreateDesktop,
        ));
        commands.push(
            Command::new(
                "sort-desktops",
                "Sort desktops",
                &["desktop", "order", "organize"],
                Action::SortDesktops {
                    profile: profile.clone(),
                },
            )
            .describe("Numbered desktops first, then names A–Z"),
        );
    }
    for desktop in model::ordered_desktops(&desktops) {
        commands.push(
            Command::new(
                &format!("desktop-{}", desktop.id),
                &format!("Go to {}", model::desktop_title(desktop, &desktops)),
                &["desktop", "switch"],
                Action::GoToDesktop {
                    desktop: desktop.id.clone(),
                },
            )
            .shortcut(desktop.shortcut_slot.and_then(Action::desktop_select)),
        );
    }
    if let Some((current, leaf)) = state.window.acting(facts)
        && state.window.view == crate::shell::View::Desktop
        && has_delegate_on(facts, current, &leaf)
    {
        for desktop in model::ordered_desktops(&desktops)
            .into_iter()
            .filter(|d| d.id != current.id)
        {
            commands.push(Command::new(
                &format!("move-with-delegates-{}", desktop.id),
                &format!(
                    "Move with delegates to {}",
                    model::desktop_title(desktop, &desktops)
                ),
                &["desktop", "move", "send", "delegates"],
                Action::MoveWithDelegates {
                    desktop: desktop.id.clone(),
                },
            ));
        }
    }
    commands.push(
        Command::new(
            "switch-profile",
            "Switch profile",
            &["profile", "setup"],
            Action::ProfileSwitch,
        )
        .shortcut(Some(Action::ProfileSwitch)),
    );
    for other in facts.profiles.all.iter().filter(|p| &p.id != profile) {
        commands.push(Command::new(
            &format!("profile-{}", other.id),
            &format!("Switch to {}", other.name),
            &["profile", "setup"],
            Action::SwitchToProfile {
                profile: other.id.clone(),
            },
        ));
    }
    commands.push(
        Command::new(
            "garden-frame",
            match crate::garden::mode(state) {
                crate::garden::Mode::Full => "Move the garden to the sidebar",
                crate::garden::Mode::Dock => "Open the garden fullscreen",
                crate::garden::Mode::Closed => "Open the garden",
            },
            &[
                "garden",
                "seed",
                "seeds",
                "plot",
                "board",
                "expand",
                "fullscreen",
            ],
            Action::BoardOpen,
        )
        .describe("Seeds and plots, the trail beside what you are reading")
        .shortcut(Some(Action::BoardOpen)),
    );
    commands.push(
        Command::new(
            "notebook",
            "Open the notebook",
            &["notebook", "journal", "knowledge", "fullscreen"],
            Action::NotebookOpenFullscreen,
        )
        .shortcut(Some(Action::NotebookOpenFullscreen)),
    );
    commands.push(
        Command::new(
            "open-markdown-file",
            "Open a markdown file",
            &["open", "file", "markdown", "md", "recent", "doc", "find"],
            Action::FileOpen,
        )
        .describe("Recently opened documents, then a fuzzy search of this session’s folder")
        .shortcut(Some(Action::FileOpen)),
    );
    commands.push(
        Command::new(
            "notebook-tile",
            "Open Editor tile",
            &[
                "notebook",
                "editor",
                "tile",
                "knowledge",
                "journal",
                "dock",
                "split",
            ],
            Action::NotebookOpenTile,
        )
        .describe("Dock an editor beside your terminals, opened on any folder")
        .shortcut(Some(Action::NotebookOpenTile)),
    );
    commands.push(
        Command::new(
            "sessions",
            "Open the sessions list",
            &["sessions", "ledger", "closed", "history", "reopen", "past"],
            Action::SessionsOpen,
        )
        .describe("Every session this machine ran, live and closed, with reopen")
        .shortcut(Some(Action::SessionsOpen)),
    );
    commands.push(
        Command::new(
            "worktrees",
            "Open worktrees",
            &[
                "worktree",
                "worktrees",
                "sweep",
                "branch",
                "reclaim",
                "pin",
                "keep",
            ],
            Action::OpenWorktrees,
        )
        .describe("Every worktree, what the sweep decided, and why it kept the rest"),
    );
    commands.push(Command::new(
        "notifications",
        if crate::notifications::is_open(state) {
            "Hide notifications"
        } else {
            "Show notifications"
        },
        &["notifications", "alerts", "bell"],
        Action::ToggleNotifications,
    ));
    commands.push(Command::new(
        "toggle-sidebar",
        if state.window.sidebar_hidden {
            "Show the sidebar"
        } else {
            "Hide the sidebar"
        },
        &["sidebar", "collapse", "expand", "rail"],
        Action::SessionToggleSidebar,
    ));
    let behind_screen = crate::opener::behind_screen(state);
    if let (Some(session), None) = (behind_screen, crate::editor::unavailable(behind_screen)) {
        commands.push(
            Command::new(
                "open-in-editor",
                "Open in editor",
                &["editor", "zed", "code", "folder"],
                Action::OpenInEditor {
                    session: session.id.clone(),
                },
            )
            .describe("The active agent’s folder in your editor")
            .pin(),
        );
    }
    commands.push(Command::new(
        "keyboard-shortcuts",
        "Keyboard shortcuts",
        &["shortcuts", "keys", "cheatsheet", "help"],
        Action::UiShowShortcuts,
    ));
    commands.push(
        Command::new(
            "customize-shortcuts",
            "Customize keyboard shortcuts",
            &["keybindings", "shortcuts", "keyboard", "rebind", "hotkeys"],
            Action::OpenShortcutEditor,
        )
        .describe("Rebind shortcuts and restore defaults"),
    );
    commands.extend([
        Command::new(
            "sidebar-wider",
            "Widen the sidebar",
            &["sidebar", "width", "wider", "resize"],
            Action::SidebarWider,
        ),
        Command::new(
            "sidebar-narrower",
            "Narrow the sidebar",
            &["sidebar", "width", "narrower", "resize"],
            Action::SidebarNarrower,
        ),
        Command::new(
            "sidebar-reset-width",
            "Reset sidebar width",
            &["sidebar", "width", "default", "resize"],
            Action::SetSetting {
                key: width::SETTING.into(),
                value: String::new(),
            },
        ),
    ]);
    let width = ["shelf", "narrowest", "width", "tiles"];
    let along = ["shelf", "row", "move", "reorder"];
    commands.extend([
        Command::new(
            "shelf-fewer-tiles",
            "Fewer, wider tiles",
            &width,
            Action::ShelfFewerTiles,
        ),
        Command::new(
            "shelf-more-tiles",
            "More, narrower tiles",
            &width,
            Action::ShelfMoreTiles,
        ),
        Command::new(
            "shelf-carry-left",
            "Carry tile left along the row",
            &along,
            Action::ShelfCarryLeft,
        ),
        Command::new(
            "shelf-carry-right",
            "Carry tile right along the row",
            &along,
            Action::ShelfCarryRight,
        ),
        Command::new(
            "shelf-send-to-shelf",
            "Send tile to the shelf",
            &["shelf", "row", "hide", "hold"],
            Action::ShelfSendToShelf,
        ),
    ]);
    commands.extend(facts.current_desktop().map(|desktop| {
        Command::new(
            "shelf-clear-dragged-widths",
            "Clear dragged widths",
            &["shelf", "row", "width", "divider", "reset"],
            Action::ClearDraggedWidths {
                desktop: desktop.id.clone(),
            },
        )
    }));
    if setting::on(&facts.settings, "self_update_enabled")
        && crate::settings::updatable(&facts.settings)
    {
        commands.push(Command::new(
            "update-attn",
            "Update attn",
            &["update", "upgrade", "install", "build"],
            Action::StartUpdate,
        ));
    }
    commands.push(Command::new(
        "settings",
        "Settings",
        &["settings", "preferences"],
        Action::UiOpenSettings,
    ));
    let settings = &facts.settings;
    let queue_on = setting::on(settings, queue::QUEUE_MODE);
    let crew_on = setting::on(settings, queue::QUEUE_CREW);
    let auto_settle_on = setting::on(settings, AUTO_SETTLE);
    let logos_on = setting::on(settings, crate::sidebar::HARNESS_LOGOS);
    commands.extend([
        Command::new(
            "toggle-queue-mode",
            if queue_on {
                "Turn off the agent queue"
            } else {
                "Turn on the agent queue"
            },
            &["queue", "turn", "settle", "attention", "sidebar"],
            set(queue::QUEUE_MODE, !queue_on),
        )
        .describe("Swap the desktop tree for the queue sidebar: the turns you owe, your crew and your runs"),
        Command::new(
            "toggle-crew-queue",
            if crew_on {
                "Take the crew out of the queue"
            } else {
                "Put the crew in the queue"
            },
            &["crew", "chief", "queue", "turn", "member", "sidebar"],
            set(queue::QUEUE_CREW, !crew_on),
        )
        .describe("Whether a crew member or the Chief owing a turn also waits in the queue"),
        Command::new(
            "manage-crew",
            "Manage crew",
            &["crew", "member", "charter", "start", "close"],
            Action::OpenCrew,
        )
        .describe("Start, charter and hand off to crew members"),
        Command::new(
            "toggle-auto-settle",
            if auto_settle_on {
                "Turn off auto-settle"
            } else {
                "Turn on auto-settle"
            },
            &["auto", "settle", "turn", "countdown", "queue", "attention"],
            set(AUTO_SETTLE, !auto_settle_on),
        )
        .describe("Settle a turn once you have steered the agent and it goes back to work"),
        Command::new(
            "toggle-harness-logos",
            if logos_on {
                "Hide harness logos in the sidebar"
            } else {
                "Show harness logos in the sidebar"
            },
            &["harness", "logo", "icon", "sidebar", "claude", "codex"],
            set(crate::sidebar::HARNESS_LOGOS, !logos_on),
        )
        .describe("The Claude or Codex mark beside each agent"),
    ]);
    for feature in FEATURES {
        let on = setting::on(settings, feature.key);
        let keywords: Vec<&str> = ["experimental", "feature"]
            .into_iter()
            .chain(feature.search.split(' '))
            .collect();
        commands.push(
            Command::new(
                &format!("feature-{}", feature.key),
                &format!(
                    "{} {}",
                    if on { "Turn off" } else { "Turn on" },
                    feature.name
                ),
                &keywords,
                set(feature.key, !on),
            )
            .search_only(),
        );
    }
    if let Some(session) = on_screen {
        let name = model::display_name(session);
        commands.push(Command {
            id: "toggle-session-priority".into(),
            title: if session.priority.unwrap_or(false) {
                "Clear priority"
            } else {
                "Mark as priority"
            }
            .into(),
            description: Some(name.clone()),
            keywords: ["priority", "session", "agent", &name]
                .map(str::to_owned)
                .to_vec(),
            pinned: true,
            search_only: false,
            action: Action::SessionPriority,
            shortcut: None,
        });
        commands.push(
            Command::new(
                "rename-active-session",
                "Rename this session…",
                &["rename", "name", "label", "agent", "session", "tile"],
                Action::RenameSession {
                    session: session.id.clone(),
                },
            )
            .describe("Change its name in the sidebar and tile")
            .pin(),
        );
        if crate::desktop::shows_usage(session) {
            commands.push(
                Command::new(
                    "show-session-usage",
                    &format!("Show {name}'s usage"),
                    &["usage", "tokens", "cost", "models", "agent", "session"],
                    Action::ShowSessionUsage {
                        session: session.id.clone(),
                    },
                )
                .describe("Token and cost totals for each model in this session")
                .pin(),
            );
        }
        if crate::desktop::shows_chain(session, &state.facts.sessions) {
            commands.push(
                Command::new(
                    "show-delegation-chain",
                    "Show delegation chain",
                    &[
                        "role",
                        "orchestrator",
                        "builder",
                        "parent",
                        "children",
                        "agent",
                        "session",
                    ],
                    Action::ShowDelegationChain {
                        session: session.id.clone(),
                    },
                )
                .describe("Navigate this agent’s dispatcher, peers, and delegates")
                .pin(),
            );
        }
        if crate::desktop::seed_chip::shown(session, &state.facts) {
            commands.push(
                Command::new(
                    "show-tended-seeds",
                    &format!("Show {name}'s seeds"),
                    &[
                        "seed", "seeds", "tend", "tending", "garden", "plot", "agent", "session",
                    ],
                    Action::ShowSessionSeeds {
                        session: session.id.clone(),
                    },
                )
                .describe("The seeds this agent is tending, and what it reports to")
                .pin(),
            );
        }
    }
    let snoozed = on_screen.filter(|s| {
        queue_on
            && queue::participates(s, crew_on)
            && queue::is_snoozed(s.turn_snoozed_until.as_deref(), state.window.now_ms)
    });
    if let Some(session) = snoozed {
        commands.push(
            Command::new(
                "unsnooze-active-session",
                "Unsnooze this agent",
                &["unsnooze", "snooze", "defer", "queue", "turn"],
                Action::WakeSession {
                    session: session.id.clone(),
                },
            )
            .describe("End the snooze and let it back into the queue")
            .pin(),
        );
        return commands;
    }
    if turn_actions.is_some_and(|a| a.snooze) {
        commands.push(
            Command::new(
                "snooze-active-session",
                "Snooze this agent…",
                &["snooze", "defer", "later", "queue", "turn"],
                Action::SessionSnooze,
            )
            .describe("Take it off your plate until a time you choose")
            .pin(),
        );
    }
    commands
}

/// What the Chief's phase lets the user do with it.
fn chief_commands(state: &State) -> Vec<Command> {
    use crate::chief::Phase;
    let Some(chief) = crate::chief::view(state) else {
        return Vec::new();
    };
    let name = &chief.name;
    let mut commands = Vec::new();
    match chief.phase {
        Phase::Unconfigured => commands.push(Command::new(
            "chief-setup",
            &format!("Set up {name}"),
            &["chief", "harness", "model"],
            Action::OpenChiefSetup,
        )),
        Phase::BetweenSessions => commands.push(Command::new(
            "chief-start",
            &format!("Start {name}"),
            &["chief", "start"],
            Action::StartChief,
        )),
        Phase::InSession => commands.push(Command::new(
            "chief-close",
            &format!("Ask {name} to close"),
            &["chief", "close"],
            Action::AskChiefToClose,
        )),
        Phase::Configuring | Phase::Starting | Phase::Closing => {}
    }
    if chief.agent.is_some() {
        commands.push(Command::new(
            "chief-route",
            &format!("Change {name}'s harness and model"),
            &["chief", "harness", "model", "effort"],
            Action::OpenChiefSetup,
        ));
    }
    commands
}

/// The commands that match every term of `query`, best first: a stronger match on the title,
/// then pinned, then more used, then more recently used, then as declared.
pub(crate) fn ranked(state: &State, query: &str, history: &[CommandUsage]) -> Vec<Row> {
    let terms: Vec<String> = query
        .to_lowercase()
        .split_whitespace()
        .map(str::to_owned)
        .collect();
    let used = |id: &str| {
        history
            .iter()
            .find(|u| u.command_id == id)
            .map_or((0.0, 0), |u| {
                (u.score, queue::epoch_ms(&u.last_used_at).unwrap_or(0))
            })
    };
    let mut scored: Vec<(u32, Command)> = catalog(state)
        .into_iter()
        .filter_map(|c| {
            let score = score(&c, &terms);
            (score > 0).then_some((score, c))
        })
        .collect();
    scored.sort_by(|(a_score, a), (b_score, b)| {
        let (a_used, a_at) = used(&a.id);
        let (b_used, b_at) = used(&b.id);
        b_score
            .cmp(a_score)
            .then(b.pinned.cmp(&a.pinned))
            .then(b_used.total_cmp(&a_used))
            .then(b_at.cmp(&a_at))
    });
    scored
        .into_iter()
        .map(|(_, c)| Row {
            key: format!("command:{}", c.id),
            look: Look::Command {
                icon: icon_of(&c.id),
                description: c.description,
                shortcut: super::label_of(state, c.shortcut.as_ref().unwrap_or(&c.action)),
            },
            label: c.title,
            pick: Some(Pick::Command {
                id: c.id,
                action: c.action,
            }),
        })
        .collect()
}

/// The icon the Tauri app gives command `id`.
fn icon_of(id: &str) -> Option<Icon> {
    let desktops = ["desktop-", "move-with-delegates-"];
    if desktops.iter().any(|p| id.starts_with(p)) {
        return Some(Icon::Desktops);
    }
    if id.starts_with("feature-") {
        return Some(Icon::Attention);
    }
    if id.starts_with("profile-") {
        return Some(Icon::Context);
    }
    Some(match id {
        "update-attn" => return None,
        "close-desktop" | "new-desktop" | "sort-desktops" | "garden-frame" => Icon::Desktops,
        "jump-to-waiting"
        | "next-run-needing-you"
        | "settle-active-session"
        | "attention"
        | "asks"
        | "toggle-queue-mode"
        | "toggle-crew-queue"
        | "toggle-auto-settle"
        | "toggle-session-priority"
        | "unsnooze-active-session"
        | "snooze-active-session" => Icon::Attention,
        "keyboard-shortcuts"
        | "whats-new"
        | "sidebar-wider"
        | "sidebar-narrower"
        | "sidebar-reset-width"
        | "shelf-fewer-tiles"
        | "shelf-more-tiles"
        | "shelf-carry-left"
        | "shelf-carry-right"
        | "shelf-send-to-shelf"
        | "shelf-clear-dragged-widths"
        | "settings"
        | "customize-shortcuts"
        | "create-diagnostic-report" => Icon::Keys,
        "notebook" => Icon::Notebook,
        "notifications" => Icon::Notifications,
        "show-tended-seeds" => Icon::Garden,
        "show-delegation-chain" => Icon::Delegation,
        _ => Icon::Context,
    })
}

fn score(command: &Command, terms: &[String]) -> u32 {
    if terms.is_empty() {
        return u32::from(!command.search_only);
    }
    let title = command.title.to_lowercase();
    let searchable = format!(
        "{title} {} {}",
        command.description.as_deref().unwrap_or_default(),
        command.keywords.join(" ")
    )
    .to_lowercase();
    terms
        .iter()
        .map(|term| {
            if !searchable.contains(term.as_str()) {
                None
            } else if title.starts_with(term.as_str()) {
                Some(4)
            } else if title.contains(term.as_str()) {
                Some(2)
            } else {
                Some(1)
            }
        })
        .sum::<Option<u32>>()
        .unwrap_or(0)
}
