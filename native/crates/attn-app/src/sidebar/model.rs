use std::collections::{HashMap, HashSet};

use attn_client::Facts;
use attn_protocol::{Desktop, Session};
use serde_json::Value;

/// Desktops 1 to 9 have their own shortcut slot; the rest number on from 10.
const SHORTCUT_SLOTS: i64 = 9;

/// What an agent is doing, as the daemon names it on the wire.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum AgentState {
    Launching,
    Working,
    WaitingInput,
    PendingApproval,
    Scheduled,
    Idle,
    Recoverable,
    Unknown,
}

impl AgentState {
    pub fn parse(wire: &str) -> AgentState {
        match wire {
            "launching" => AgentState::Launching,
            "working" => AgentState::Working,
            "waiting_input" => AgentState::WaitingInput,
            "pending_approval" => AgentState::PendingApproval,
            "scheduled" => AgentState::Scheduled,
            "idle" => AgentState::Idle,
            "recoverable" => AgentState::Recoverable,
            _ => AgentState::Unknown,
        }
    }

    pub fn wire(self) -> &'static str {
        match self {
            AgentState::Launching => "launching",
            AgentState::Working => "working",
            AgentState::WaitingInput => "waiting_input",
            AgentState::PendingApproval => "pending_approval",
            AgentState::Scheduled => "scheduled",
            AgentState::Idle => "idle",
            AgentState::Recoverable => "recoverable",
            AgentState::Unknown => "unknown",
        }
    }

    /// How the Tauri app says it after the harness's name: "Claude · waiting input".
    pub fn spoken(self) -> &'static str {
        match self {
            AgentState::WaitingInput => "waiting input",
            AgentState::PendingApproval => "pending approval",
            other => other.wire(),
        }
    }
}

/// One agent as its row shows it. A row is built again only when this changes.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct AgentRow {
    pub id: String,
    pub name: String,
    pub harness: String,
    pub state: AgentState,
    pub priority: bool,
    pub owed: bool,
    pub selected: bool,
    pub settling: Settling,
}

/// The daemon's auto-settle countdown, as a row whose tile is not on screen shows it.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub enum Settling {
    #[default]
    Off,
    /// Settles at this instant.
    Counting(String),
    /// Stopped with no deadline until the host comes back.
    Held,
}

impl Settling {
    pub fn of(session: &Session) -> Settling {
        match &session.auto_settle_fires_at {
            Some(at) if !at.is_empty() => Settling::Counting(at.clone()),
            _ if session.auto_settle_held == Some(true) => Settling::Held,
            _ => Settling::Off,
        }
    }
}

impl AgentRow {
    /// The name as rows show it, behind the priority flag when the agent has priority.
    pub fn shown_name(&self) -> String {
        if self.priority {
            format!("{} {}", attn_ui::icon::FLAG, self.name)
        } else {
            self.name.clone()
        }
    }

    pub fn of(session: &Session, selected: bool) -> AgentRow {
        AgentRow {
            id: session.id.clone(),
            name: display_name(session),
            harness: harness_label(&session.agent),
            state: AgentState::parse(&session.state),
            priority: session.priority == Some(true),
            owed: session.turn_owed == Some(true),
            selected,
            settling: Settling::Off,
        }
    }

    /// The row of an agent whose tile is not on screen, which shows its countdown.
    pub fn off_screen(mut self, session: &Session) -> AgentRow {
        self.settling = Settling::of(session);
        self
    }

    /// What the state mark says to a screen reader: "Claude · working".
    pub fn lead(&self) -> String {
        format!("{} · {}", self.harness, self.state.spoken())
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct DesktopHeader {
    pub id: String,
    pub title: String,
    pub number: i64,
    pub current: bool,
    pub empty: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct TileRow {
    pub desktop: String,
    pub id: String,
    pub kind: String,
    pub title: String,
}

impl TileRow {
    /// The word the row shows after the title, as the Tauri app's `tileKindLabel`.
    pub fn word(&self) -> &'static str {
        match self.kind.as_str() {
            "seed" => "SEED",
            "markdown" => "DOC",
            "browser" => "WEB",
            "notebook" => "NOTE",
            _ => "TILE",
        }
    }
}

/// A tile's name from what its layout leaf holds: a browser's host, a seed's id, a file's
/// name. A markdown tile's own heading comes with the markdown unit.
pub fn tile_title(kind: &str, params: &str) -> String {
    let basename = |path: &str| {
        let trimmed = path.trim_end_matches('/');
        trimmed
            .rsplit('/')
            .next()
            .filter(|s| !s.is_empty())
            .unwrap_or(trimmed)
            .to_owned()
    };
    let path_in = |params: &str| {
        serde_json::from_str::<Value>(params)
            .ok()
            .and_then(|v| v.get("path").and_then(Value::as_str).map(str::to_owned))
            .unwrap_or_else(|| params.to_owned())
    };
    match kind {
        "seed" if !params.is_empty() => params.to_owned(),
        "seed" => "seed".into(),
        "browser" if !params.is_empty() => params
            .split_once("://")
            .map(|(_, rest)| rest.split(['/', '?', '#']).next().unwrap_or(rest))
            .filter(|host| !host.is_empty())
            .unwrap_or(params)
            .to_owned(),
        "notebook" => {
            let path = path_in(params);
            if path.is_empty() {
                "Editor".into()
            } else {
                basename(&path)
            }
        }
        _ => {
            let path = if kind == "markdown" {
                path_in(params)
            } else {
                params.to_owned()
            };
            if path.is_empty() {
                kind.to_owned()
            } else {
                basename(&path)
            }
        }
    }
}

/// One line of the desktop sidebar, in the order it shows.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum Line {
    Desktop(DesktopHeader),
    Agent { desktop: String, row: AgentRow },
    Tile(TileRow),
}

impl Line {
    /// The desktop the line shows or belongs to.
    pub fn desktop(&self) -> &str {
        match self {
            Line::Desktop(d) => &d.id,
            Line::Agent { desktop, .. } => desktop,
            Line::Tile(t) => &t.desktop,
        }
    }

    pub fn key(&self) -> String {
        match self {
            Line::Desktop(d) => format!("sidebar.desktop.{}", d.id),
            Line::Agent { row, .. } => format!("sidebar.session.{}", row.id),
            Line::Tile(t) => format!("sidebar.tile.{}.{}", t.desktop, t.id),
        }
    }
}

pub fn display_name(session: &Session) -> String {
    if !session.label.is_empty() {
        return session.label.clone();
    }
    match session.title.as_deref() {
        Some(title) if !title.is_empty() => title.to_owned(),
        _ => harness_label(&session.agent),
    }
}

pub fn harness_label(agent: &str) -> String {
    match agent.trim().to_lowercase().as_str() {
        "" => "Unknown harness".into(),
        "claude" => "Claude".into(),
        "codex" => "Codex".into(),
        other => other
            .split(['-', '_', ' '])
            .filter(|part| !part.is_empty())
            .map(|part| {
                let mut chars = part.chars();
                chars
                    .next()
                    .map(|c| c.to_uppercase().chain(chars).collect::<String>())
                    .unwrap_or_default()
            })
            .collect::<Vec<_>>()
            .join(" "),
    }
}

/// The selected profile's desktops in their order, the order the daemon's `order_key` gives.
pub fn ordered_desktops(desktops: &[Desktop]) -> Vec<&Desktop> {
    let mut ordered: Vec<&Desktop> = desktops.iter().collect();
    ordered.sort_by(|a, b| a.order_key.cmp(&b.order_key));
    ordered
}

/// The order "Sort desktops" gives: numbered desktops by number, then named ones by name, case
/// aside and numbers by value, then unnamed ones in the order they had.
pub fn sorted_desktop_ids(desktops: &[Desktop]) -> Vec<String> {
    let slot = |d: &Desktop| d.shortcut_slot.filter(|s| *s > 0);
    let mut sorted = ordered_desktops(desktops);
    sorted.sort_by(|a, b| match (slot(a), slot(b)) {
        (Some(a), Some(b)) => a.cmp(&b),
        (Some(_), None) => std::cmp::Ordering::Less,
        (None, Some(_)) => std::cmp::Ordering::Greater,
        (None, None) => match (a.name.trim(), b.name.trim()) {
            ("", "") => std::cmp::Ordering::Equal,
            ("", _) => std::cmp::Ordering::Greater,
            (_, "") => std::cmp::Ordering::Less,
            (a, b) => natural(a, b),
        },
    });
    sorted.into_iter().map(|d| d.id.clone()).collect()
}

/// Compares names as a person reads them: letters without case, and each run of digits by
/// its value, so "review 2" comes before "Review 10".
fn natural(a: &str, b: &str) -> std::cmp::Ordering {
    fn runs(s: &str) -> Vec<(bool, String)> {
        let mut runs: Vec<(bool, String)> = Vec::new();
        for c in s.chars() {
            let digit = c.is_ascii_digit();
            match runs.last_mut() {
                Some((d, run)) if *d == digit => run.push(c),
                _ => runs.push((digit, c.to_string())),
            }
        }
        runs
    }
    let key = |(digit, run): &(bool, String)| {
        if *digit {
            let value = run.trim_start_matches('0');
            (0, value.len(), value.to_owned())
        } else {
            (1, 0, run.to_lowercase())
        }
    };
    runs(a).iter().map(key).cmp(runs(b).iter().map(key))
}

/// A desktop's number: its shortcut slot, or after the nine slots in order for the rest.
pub fn desktop_number(desktop: &Desktop, desktops: &[Desktop]) -> i64 {
    if let Some(slot) = desktop.shortcut_slot.filter(|slot| *slot > 0) {
        return slot;
    }
    let extra = ordered_desktops(desktops)
        .into_iter()
        .filter(|d| d.shortcut_slot.unwrap_or(0) <= 0)
        .position(|d| d.id == desktop.id)
        .unwrap_or(0);
    SHORTCUT_SLOTS + extra as i64 + 1
}

pub fn desktop_title(desktop: &Desktop, desktops: &[Desktop]) -> String {
    let name = desktop.name.trim();
    if name.is_empty() {
        format!("Desktop {}", desktop_number(desktop, desktops))
    } else {
        name.to_owned()
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Leaf {
    Pane(String),
    Tile {
        id: String,
        kind: String,
        params: String,
    },
}

/// The leaves of a desktop's layout tree in reading order: first child before second.
pub fn leaves(tree_json: &str) -> Vec<Leaf> {
    fn walk(node: &Value, out: &mut Vec<Leaf>) {
        let field = |name: &str| node.get(name).and_then(Value::as_str);
        match field("type") {
            Some("pane") => {
                if let Some(id) = field("pane_id") {
                    out.push(Leaf::Pane(id.to_owned()));
                }
            }
            Some("tile") => {
                if let (Some(id), Some(kind)) = (field("tile_id"), field("tile_kind"))
                    && !id.is_empty()
                    && !kind.is_empty()
                {
                    out.push(Leaf::Tile {
                        id: id.to_owned(),
                        kind: kind.to_owned(),
                        params: field("tile_params").unwrap_or_default().to_owned(),
                    });
                }
            }
            Some("split") => {
                if let Some(children) = node.get("children").and_then(Value::as_array) {
                    for child in children.iter().take(2) {
                        walk(child, out);
                    }
                }
            }
            _ => {}
        }
    }
    let mut out = Vec::new();
    if let Ok(tree) = serde_json::from_str::<Value>(tree_json) {
        walk(&tree, &mut out);
    }
    out
}

/// What the desktop sidebar lists: each desktop, then its agents and tiles in layout order,
/// then any agent of it the layout does not hold. An agent not in `on_screen` shows its
/// countdown.
pub fn desktop_lines(facts: &Facts, home: bool, on_screen: &[String]) -> Vec<Line> {
    let desktops = &facts.desktops;
    let current = facts.current_desktop().map(|d| d.id.as_str());
    let by_id: HashMap<&str, &Session> =
        facts.sessions.iter().map(|s| (s.id.as_str(), s)).collect();
    let desktop_of: HashMap<&str, &str> = desktops
        .iter()
        .flat_map(|d| {
            d.panes
                .iter()
                .map(|p| (p.session_id.as_str(), d.id.as_str()))
        })
        .collect();
    let mut lines = Vec::new();
    for desktop in ordered_desktops(desktops) {
        let shown = !home && current == Some(desktop.id.as_str());
        let active_session = desktop
            .panes
            .iter()
            .find(|p| p.pane_id == desktop.active_pane_id)
            .map(|p| p.session_id.as_str());
        let session_of: HashMap<&str, &str> = desktop
            .panes
            .iter()
            .filter(|p| !p.session_id.is_empty())
            .map(|p| (p.pane_id.as_str(), p.session_id.as_str()))
            .collect();
        let mut children = Vec::new();
        let mut placed = HashSet::new();
        for leaf in leaves(&desktop.tree_json) {
            match leaf {
                Leaf::Tile { id, kind, params } => children.push(Line::Tile(TileRow {
                    desktop: desktop.id.clone(),
                    title: tile_title(&kind, &params),
                    id,
                    kind,
                })),
                Leaf::Pane(pane) => {
                    let Some(session) = session_of.get(pane.as_str()).and_then(|id| by_id.get(id))
                    else {
                        continue;
                    };
                    if placed.insert(session.id.as_str()) {
                        children.push(agent_line(
                            desktop,
                            session,
                            shown,
                            active_session,
                            on_screen,
                        ));
                    }
                }
            }
        }
        for session in facts.sessions.iter() {
            if desktop_of.get(session.id.as_str()) == Some(&desktop.id.as_str())
                && placed.insert(session.id.as_str())
            {
                children.push(agent_line(
                    desktop,
                    session,
                    shown,
                    active_session,
                    on_screen,
                ));
            }
        }
        lines.push(Line::Desktop(DesktopHeader {
            id: desktop.id.clone(),
            title: desktop_title(desktop, desktops),
            number: desktop_number(desktop, desktops),
            current: shown,
            empty: children.is_empty(),
        }));
        lines.extend(children);
    }
    lines
}

fn agent_line(
    desktop: &Desktop,
    session: &Session,
    shown: bool,
    active: Option<&str>,
    on_screen: &[String],
) -> Line {
    let selected = shown && active == Some(session.id.as_str());
    let row = AgentRow::of(session, selected);
    Line::Agent {
        desktop: desktop.id.clone(),
        row: if on_screen.contains(&session.id) {
            row
        } else {
            row.off_screen(session)
        },
    }
}
