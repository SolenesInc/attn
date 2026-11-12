use attn_protocol::CommandUsage;

use super::Row;
use crate::sidebar::{model, queue};
use crate::{Action, State};

/// A command the palette offers: its id is the key the daemon keeps its history under, shared
/// with the Tauri app.
pub(crate) struct Command {
    id: String,
    title: String,
    description: Option<String>,
    keywords: Vec<String>,
    pinned: bool,
    action: Action,
}

impl Command {
    fn new(id: &str, title: &str, keywords: &[&str], action: Action) -> Command {
        Command {
            id: id.into(),
            title: title.into(),
            description: None,
            keywords: keywords.iter().map(|k| (*k).to_owned()).collect(),
            pinned: false,
            action,
        }
    }

    fn describe(mut self, description: &str) -> Command {
        self.description = Some(description.into());
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
    for desktop in model::ordered_desktops(&desktops) {
        commands.push(Command::new(
            &format!("desktop-{}", desktop.id),
            &format!("Go to {}", model::desktop_title(desktop, &desktops)),
            &["desktop", "switch"],
            Action::GoToDesktop {
                desktop: desktop.id.clone(),
            },
        ));
    }
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
            action: Action::SessionPriority,
        });
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
            shortcut: state
                .keymap
                .binding(&c.action)
                .map(|b| state.keymap.label(b)),
            title: c.title,
            description: c.description,
            command: Some(c.id),
            action: c.action,
        })
        .collect()
}

fn score(command: &Command, terms: &[String]) -> u32 {
    if terms.is_empty() {
        return 1;
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
