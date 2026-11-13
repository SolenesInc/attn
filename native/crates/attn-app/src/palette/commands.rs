use attn_protocol::CommandUsage;

use attn_protocol::go_values::FEATURES;

use super::{Look, Pick, Row};
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
    fn new(id: &str, title: &str, keywords: &[&str], action: Action) -> Command {
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

    fn describe(mut self, description: &str) -> Command {
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

    fn pin(mut self) -> Command {
        self.pinned = true;
        self
    }
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
    let mut commands = vec![
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
    ];
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
    commands.push(Command::new(
        "keyboard-shortcuts",
        "Keyboard shortcuts",
        &["shortcuts", "keys", "cheatsheet", "help"],
        Action::UiShowShortcuts,
    ));
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
    let settings = &facts.settings;
    let queue_on = setting::on(settings, queue::QUEUE_MODE);
    let crew_on = setting::on(settings, queue::QUEUE_CREW);
    let auto_settle_on = setting::on(settings, AUTO_SETTLE);
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
