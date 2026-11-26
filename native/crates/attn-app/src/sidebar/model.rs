use std::collections::{HashMap, HashSet};

use attn_client::Desktop;
use attn_client::Facts;
use attn_protocol::{Harness, OutpostInfo, Session};
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

    /// How it reads after the harness's name: "Claude · waiting input".
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
    pub agent: String,
    pub name: String,
    pub harness: String,
    pub state: AgentState,
    pub priority: bool,
    pub owed: bool,
    pub selected: bool,
    pub settling: Settling,
    /// The agent's open asks.
    pub asks: usize,
    pub pr: Option<crate::desktop::provenance::Badge>,
    pub chief: bool,
    pub unknown_because: Option<&'static str>,
    pub run: Option<crate::desktop::provenance::Run>,
    pub settle_run: bool,
    /// Its machine is unreachable.
    pub offline: bool,
    /// Its way into the delegation chain it sits in.
    pub chain: Option<Box<crate::desktop::chain::Trigger>>,
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
    pub fn of(
        session: &Session,
        harnesses: &[Harness],
        outposts: &[OutpostInfo],
        selected: bool,
    ) -> AgentRow {
        AgentRow {
            id: session.id.clone(),
            agent: session.agent.clone(),
            name: display_name(session, harnesses),
            harness: harness_label(harnesses, &session.agent),
            state: AgentState::parse(&session.state),
            priority: session.priority == Some(true),
            owed: session.turn_owed == Some(true),
            selected,
            settling: Settling::Off,
            asks: 0,
            pr: crate::desktop::provenance::badge(session),
            chief: session.chief_of_staff == Some(true),
            run: crate::desktop::provenance::run(session),
            settle_run: session.automation.is_some() && session.turn_owed == Some(true),
            offline: crate::outpost::offline(session, outposts),
            chain: None,
            unknown_because: (session.state == "unknown")
                .then(|| unknown_reason(session.state_reason.as_deref()))
                .flatten(),
        }
    }

    /// The row with the agent's open asks counted.
    pub fn asking(mut self, facts: &Facts, session: &Session) -> AgentRow {
        self.asks = crate::asks::of_session(facts, session).len();
        self
    }

    /// The row with its way into the delegation chain it sits in.
    pub fn chained(mut self, session: &Session, facts: &Facts) -> AgentRow {
        self.chain = crate::desktop::chain::trigger(session, facts).map(Box::new);
        self
    }

    /// The row of an agent whose tile is not on screen, which shows its countdown.
    pub fn off_screen(mut self, session: &Session) -> AgentRow {
        self.settling = match Settling::of(session) {
            Settling::Counting(_) if self.offline => Settling::Held,
            settling => settling,
        };
        self
    }

    /// What the state mark says to a screen reader: "Claude · working", or "offline" while
    /// its machine is unreachable.
    pub fn lead(&self) -> String {
        if self.offline {
            return "offline".into();
        }
        format!(
            "{} · {}",
            self.harness,
            self.unknown_because.unwrap_or(self.state.spoken())
        )
    }

    pub fn hover(&self) -> String {
        match (self.unknown_because, self.state) {
            (Some(why), _) => format!("{} · {why}", self.harness),
            (None, AgentState::Recoverable) => {
                format!("{} · Session will be recovered when opened", self.harness)
            }
            (None, _) => self.harness.clone(),
        }
    }
}

fn unknown_reason(reason: Option<&str>) -> Option<&'static str> {
    match reason? {
        "stuck" => Some("Stuck — the agent has stopped reporting anything at all"),
        "no_evidence" => Some("No signal from this agent yet"),
        _ => None,
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct DesktopHeader {
    pub id: String,
    pub title: String,
    /// The desktop's shortcut slot (1–9), when it has one.
    pub slot: Option<i64>,
    pub current: bool,
    pub empty: bool,
    /// The desktop has a name of its own; the heading shows only its number otherwise.
    pub named: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct TileRow {
    pub desktop: String,
    pub id: String,
    pub kind: String,
    pub title: String,
    /// The tile holds the focus of the desktop in view.
    pub selected: bool,
}

impl TileRow {
    /// The word the row shows after the title.
    pub fn word(&self) -> &'static str {
        match self.kind.as_str() {
            "seed" => "SEED",
            "markdown" => "DOC",
            "browser" => "WEB",
            "notebook" => "NOTE",
            _ => "TILE",
        }
    }

    /// The icon the row leads with.
    pub fn icon(&self) -> &'static rune::Icon {
        tile_icon(&self.kind)
    }
}

/// The icon of a tile of `kind`, such as a document.
pub fn tile_icon(kind: &str) -> &'static rune::Icon {
    match kind {
        "seed" => &attn_ui::icon::GARDEN,
        "markdown" => &attn_ui::icon::MARKDOWN,
        "browser" => &attn_ui::icon::BROWSER,
        _ => &attn_ui::icon::NOTEBOOK,
    }
}

/// A tile's name from what its layout leaf holds and, for a document, what the daemon sent of
/// it: the document's heading or first line, else the file's name. A browser goes by its host
/// and a seed by its title when the garden holds it, else by its id.
pub fn tile_title(
    kind: &str,
    params: &str,
    content: Option<&attn_protocol::DesktopTileContentMessage>,
    seeds: &[std::sync::Arc<attn_protocol::Seed>],
    garden: Option<&crate::garden::Garden>,
) -> String {
    let readable = content.filter(|c| c.error.as_deref().is_none_or(str::is_empty));
    if kind == "markdown"
        && let Some(title) = readable.and_then(|c| document_title(&c.content))
    {
        return title;
    }
    match kind {
        "seed" if !params.is_empty() => garden
            .and_then(|g| crate::garden::shown_seed(g, seeds, params))
            .or_else(|| {
                seeds
                    .iter()
                    .find(|s| s.id == params)
                    .map(std::sync::Arc::as_ref)
            })
            .map_or_else(|| params.to_owned(), |s| s.title.clone()),
        "seed" => "seed".into(),
        "browser" if !params.is_empty() => params
            .split_once("://")
            .map(|(_, rest)| rest.split(['/', '?', '#']).next().unwrap_or(rest))
            .filter(|host| !host.is_empty())
            .unwrap_or(params)
            .to_owned(),
        "notebook" => crate::notebook::Params::parse(params)
            .path
            .map_or_else(|| "Editor".into(), |path| basename(&path)),
        _ => {
            let path = if kind == "markdown" {
                content
                    .map(|c| c.path.clone())
                    .filter(|p| !p.is_empty())
                    .unwrap_or_else(|| path_in(params))
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

/// What a review document's tab names it: its file's name, or for a seed its id.
pub fn document_tab(kind: &str, params: &str) -> String {
    let path = if kind == "markdown" {
        path_in(params)
    } else {
        params.to_owned()
    };
    Some(basename(&path))
        .filter(|name| !name.is_empty())
        .unwrap_or_else(|| kind.to_owned())
}

fn basename(path: &str) -> String {
    let trimmed = path.trim_end_matches('/');
    trimmed
        .rsplit('/')
        .next()
        .filter(|s| !s.is_empty())
        .unwrap_or(trimmed)
        .to_owned()
}

fn path_in(params: &str) -> String {
    serde_json::from_str::<Value>(params)
        .ok()
        .and_then(|v| v.get("path").and_then(Value::as_str).map(str::to_owned))
        .unwrap_or_else(|| params.to_owned())
}

/// The longest a document's title runs before it ends in an ellipsis.
const MAX_DOCUMENT_TITLE: usize = 80;

/// A document's first heading or line of text, past a closed front matter block and any
/// rules, with its heading marker and inline markdown taken off.
fn document_title(markdown: &str) -> Option<String> {
    let markdown = markdown.strip_prefix('\u{feff}').unwrap_or(markdown);
    let lines: Vec<&str> = markdown.split('\n').collect();
    let mut from = 0;
    if lines.first().map(|l| l.trim()) == Some("---")
        && let Some(close) = lines.iter().skip(1).position(|l| l.trim() == "---")
    {
        from = close + 2;
    }
    lines[from..].iter().find_map(|line| {
        let raw = line.trim();
        let squeezed: String = raw.chars().filter(|c| !c.is_whitespace()).collect();
        let is_rule = squeezed.len() >= 3
            && ['-', '*', '_']
                .iter()
                .any(|&m| squeezed.chars().all(|c| c == m));
        if raw.is_empty() || is_rule {
            return None;
        }
        let title = plain_inline(heading_text(raw));
        if title.is_empty() {
            return None;
        }
        Some(if title.chars().count() > MAX_DOCUMENT_TITLE {
            let cut: String = title.chars().take(MAX_DOCUMENT_TITLE - 1).collect();
            format!("{}…", cut.trim_end())
        } else {
            title
        })
    })
}

/// `line` without a leading `#` to `######` marker and a closing run of `#` set off by space.
fn heading_text(line: &str) -> &str {
    let hashes = line.chars().take_while(|&c| c == '#').count();
    let marked = (1..=6).contains(&hashes) && line[hashes..].starts_with(char::is_whitespace);
    let text = if marked {
        line[hashes..].trim_start()
    } else {
        line
    };
    let unclosed = text.trim_end_matches('#');
    if unclosed.ends_with(char::is_whitespace) {
        unclosed.trim_end()
    } else {
        text
    }
}

/// `text` with links and images reduced to their text, emphasis, code and strike marks
/// dropped, and runs of whitespace made one space.
fn plain_inline(text: &str) -> String {
    let mut out = String::new();
    let mut rest = text;
    while let Some(open) = rest.find('[') {
        let label_end = rest[open..].find(']').map(|i| open + i);
        let link = label_end.filter(|&e| rest[e + 1..].starts_with('('));
        let close = link.and_then(|e| rest[e + 1..].find(')').map(|i| e + 1 + i));
        match (label_end, close) {
            (Some(end), Some(close)) => {
                let before = &rest[..open];
                out.push_str(before.strip_suffix('!').unwrap_or(before));
                out.push_str(&rest[open + 1..end]);
                rest = &rest[close + 1..];
            }
            _ => {
                out.push_str(&rest[..=open]);
                rest = &rest[open + 1..];
            }
        }
    }
    out.push_str(rest);
    out.retain(|c| !matches!(c, '*' | '_' | '`' | '~'));
    out.split_whitespace().collect::<Vec<_>>().join(" ")
}

/// One line of the desktop sidebar, in the order it shows.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum Line {
    Desktop(DesktopHeader),
    Agent {
        desktop: String,
        leaf: String,
        row: Box<AgentRow>,
    },
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

pub fn display_name(session: &Session, harnesses: &[Harness]) -> String {
    if !session.label.is_empty() {
        return session.label.clone();
    }
    match session.title.as_deref() {
        Some(title) if !title.is_empty() => title.to_owned(),
        _ => harness_label(harnesses, &session.agent),
    }
}

pub fn harness_label(harnesses: &[Harness], agent: &str) -> String {
    crate::harness::catalog::name(harnesses, agent)
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

/// One live automation run row in the desktop sidebar's unified Automations section.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct AutomationRunLine {
    pub desktop: String,
    pub leaf: String,
    pub row: Box<AgentRow>,
}

/// The desktop sidebar's unified Automations section: live automation runs from unslotted
/// automation-only desktops, split into active rows shown by default and finished off-screen runs
/// folded behind a disclosure toggle.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct AutomationSection {
    pub active: Vec<AutomationRunLine>,
    pub finished: Vec<AutomationRunLine>,
    /// Runs in this section that currently need the user (owed a turn and not snoozed).
    pub needing: usize,
}

/// The unslotted launch desktop ids of non-retired crew members in the selected profile.
pub(crate) fn crew_launch_desktops(facts: &Facts) -> HashSet<&str> {
    let selected = &facts.profiles.selected;
    facts
        .crew
        .iter()
        .filter(|m| &m.profile_id == selected && !m.retired)
        .filter_map(|m| {
            m.launch_desktop
                .as_ref()?
                .desktop_id
                .as_deref()
                .filter(|id| !id.is_empty())
        })
        .collect()
}

/// Whether `desktop` is an empty unslotted ephemeral launch desktop awaiting its reap grace.
pub(crate) fn is_empty_ephemeral_desktop(desktop: &Desktop) -> bool {
    desktop.ephemeral == Some(true)
        && desktop.shortcut_slot.unwrap_or(0) <= 0
        && desktop.panes.is_empty()
        && desktop.leaves().into_iter().all(|l| l.tile().is_none())
}

/// Whether `desktop` is an empty unslotted desktop reserved as a crew member's launch desktop.
pub(crate) fn is_empty_crew_launch_desktop(
    desktop: &Desktop,
    crew_launch_desktops: &HashSet<&str>,
) -> bool {
    desktop.shortcut_slot.unwrap_or(0) <= 0
        && desktop.panes.is_empty()
        && desktop.leaves().into_iter().all(|l| l.tile().is_none())
        && crew_launch_desktops.contains(desktop.id.as_str())
}

/// Whether `desktop` is an unslotted desktop holding only live automation runs (no manual agents
/// and no non-pane tiles).
pub(crate) fn is_automation_desktop(desktop: &Desktop, by_id: &HashMap<&str, &Session>) -> bool {
    if desktop.shortcut_slot.unwrap_or(0) > 0 || desktop.panes.is_empty() {
        return false;
    }
    if !desktop.leaves().into_iter().all(|l| l.tile().is_none()) {
        return false;
    }
    desktop.panes.iter().all(|p| {
        by_id
            .get(p.session_id.as_str())
            .is_some_and(|s| s.automation.is_some())
    })
}

/// What the desktop sidebar lists: each desktop, then its agents and tiles in layout order,
/// then any agent of it the layout does not hold. An agent not in `on_screen` shows its
/// countdown. Unslotted automation-only desktops are omitted here and shown in
/// [`desktop_automations`].
pub fn desktop_lines(
    facts: &Facts,
    garden: Option<&crate::garden::Garden>,
    home: bool,
    on_screen: &[String],
    crew_entries: &[super::crew::DesktopEntry],
) -> Vec<Line> {
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
    let crew_sessions: HashSet<&str> = crew_entries
        .iter()
        .filter_map(super::crew::DesktopEntry::active_session)
        .collect();
    let crew_launch_desktops = crew_launch_desktops(facts);
    let mut lines = Vec::new();
    for desktop in ordered_desktops(desktops) {
        if is_automation_desktop(desktop, &by_id) {
            continue;
        }
        let shown = !home && current == Some(desktop.id.as_str());
        if !shown && is_empty_ephemeral_desktop(desktop) {
            continue;
        }
        let unslotted = desktop.shortcut_slot.unwrap_or(0) <= 0;
        let crew_only_panes = unslotted
            && !desktop.panes.is_empty()
            && desktop.leaves().into_iter().all(|l| l.tile().is_none())
            && desktop
                .panes
                .iter()
                .all(|p| crew_sessions.contains(p.session_id.as_str()));
        let crew_own_desktop =
            unslotted && (crew_launch_desktops.contains(desktop.id.as_str()) || crew_only_panes);
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
        let mut suppressed_crew = false;
        for leaf in desktop.leaves() {
            match leaf.tile() {
                Some((kind, params)) => children.push(Line::Tile(TileRow {
                    desktop: desktop.id.clone(),
                    title: tile_title(
                        kind,
                        params,
                        facts
                            .tile_contents
                            .get(&attn_client::TileKey {
                                desktop: desktop.id.clone(),
                                tile: leaf.id.clone(),
                            })
                            .map(|c| &*c.message),
                        facts.garden.of(&facts.profiles.selected),
                        garden,
                    ),
                    selected: shown && leaf.id == desktop.active_pane_id,
                    id: leaf.id.clone(),
                    kind: kind.to_owned(),
                })),
                None => {
                    let Some(session) = session_of
                        .get(leaf.id.as_str())
                        .and_then(|id| by_id.get(id))
                    else {
                        continue;
                    };
                    if crew_own_desktop && crew_sessions.contains(session.id.as_str()) {
                        suppressed_crew = true;
                        placed.insert(session.id.as_str());
                        continue;
                    }
                    if placed.insert(session.id.as_str()) {
                        children.push(agent_line(
                            facts,
                            desktop,
                            &leaf.id,
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
                && let Some(pane) = desktop.panes.iter().find(|p| p.session_id == session.id)
                && placed.insert(session.id.as_str())
            {
                if crew_own_desktop && crew_sessions.contains(session.id.as_str()) {
                    suppressed_crew = true;
                    continue;
                }
                children.push(agent_line(
                    facts,
                    desktop,
                    &pane.pane_id,
                    session,
                    shown,
                    active_session,
                    on_screen,
                ));
            }
        }
        if crew_own_desktop && children.is_empty() && (suppressed_crew || !shown) {
            continue;
        }
        lines.push(Line::Desktop(DesktopHeader {
            id: desktop.id.clone(),
            title: desktop_title(desktop, desktops),
            slot: desktop.shortcut_slot.filter(|slot| *slot > 0),
            current: shown,
            empty: children.is_empty(),
            named: !desktop.name.trim().is_empty(),
        }));
        lines.extend(children);
    }
    lines
}

fn agent_row_for(
    facts: &Facts,
    session: &Session,
    shown: bool,
    active: Option<&str>,
    on_screen: &[String],
) -> AgentRow {
    let selected = shown && active == Some(session.id.as_str());
    let row = AgentRow::of(session, &facts.harnesses, &facts.outposts, selected)
        .asking(facts, session)
        .chained(session, facts);
    if on_screen.contains(&session.id) {
        row
    } else {
        row.off_screen(session)
    }
}

fn agent_line(
    facts: &Facts,
    desktop: &Desktop,
    leaf: &str,
    session: &Session,
    shown: bool,
    active: Option<&str>,
    on_screen: &[String],
) -> Line {
    Line::Agent {
        desktop: desktop.id.clone(),
        leaf: leaf.to_owned(),
        row: Box::new(agent_row_for(facts, session, shown, active, on_screen)),
    }
}

/// Live automation runs on unslotted automation-only desktops, split into active rows (needing
/// the user, actively working, or currently on screen) and finished off-screen runs.
pub fn desktop_automations(
    facts: &Facts,
    home: bool,
    on_screen: &[String],
    now_ms: i64,
) -> AutomationSection {
    let desktops = &facts.desktops;
    let current = facts.current_desktop().map(|d| d.id.as_str());
    let by_id: HashMap<&str, &Session> =
        facts.sessions.iter().map(|s| (s.id.as_str(), s)).collect();
    let mut active: Vec<(u8, Option<&str>, usize, AutomationRunLine)> = Vec::new();
    let mut finished: Vec<(&str, AutomationRunLine)> = Vec::new();
    let mut needing = 0usize;
    let mut seen: HashSet<&str> = HashSet::new();
    let mut order = 0usize;

    for desktop in ordered_desktops(desktops) {
        if !is_automation_desktop(desktop, &by_id) {
            continue;
        }
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
        let ordered_leaves: Vec<(String, &Session)> = desktop
            .leaves()
            .into_iter()
            .filter(|l| l.tile().is_none())
            .filter_map(|l| {
                let session = session_of
                    .get(l.id.as_str())
                    .and_then(|id| by_id.get(id))
                    .copied()?;
                Some((l.id.clone(), session))
            })
            .chain(desktop.panes.iter().filter_map(|p| {
                let session = by_id.get(p.session_id.as_str()).copied()?;
                Some((p.pane_id.clone(), session))
            }))
            .collect();
        for (leaf_id, session) in ordered_leaves {
            if !seen.insert(session.id.as_str()) {
                continue;
            }
            let row = agent_row_for(facts, session, shown, active_session, on_screen);
            let needs = crate::automations::runs::needs_you(session, now_ms);
            if needs {
                needing += 1;
            }
            let snoozed = super::queue::is_snoozed(session.turn_snoozed_until.as_deref(), now_ms);
            let wants_attention = needs || (!snoozed && row.asks > 0);
            let live = matches!(
                row.state,
                AgentState::Launching
                    | AgentState::Working
                    | AgentState::Scheduled
                    | AgentState::Recoverable
            );
            let run = AutomationRunLine {
                desktop: desktop.id.clone(),
                leaf: leaf_id,
                row: Box::new(row),
            };
            if wants_attention {
                active.push((0, session.turn_opened_at.as_deref(), order, run));
            } else if live {
                active.push((1, None, order, run));
            } else if run.row.selected || on_screen.contains(&session.id) {
                active.push((2, None, order, run));
            } else {
                finished.push((session.state_since.as_str(), run));
            }
            order += 1;
        }
    }

    active.sort_by(|a, b| {
        a.0.cmp(&b.0)
            .then_with(|| a.1.is_none().cmp(&b.1.is_none()))
            .then_with(|| a.1.cmp(&b.1))
            .then_with(|| a.2.cmp(&b.2))
    });
    finished.sort_by(|a, b| b.0.cmp(a.0).then_with(|| a.1.row.id.cmp(&b.1.row.id)));

    AutomationSection {
        active: active.into_iter().map(|(_, _, _, run)| run).collect(),
        finished: finished.into_iter().map(|(_, run)| run).collect(),
        needing,
    }
}

pub const SESSION_EMOJIS: [&str; 70] = [
    "🛠️", "🚀", "🧪", "🔭", "🧠", "🛰️", "🧩", "🧵", "🔧", "🗺️", "📡", "🧭", "⚙️", "📦", "🧬", "🪄",
    "🦾", "🧱", "🧯", "🪛", "🪜", "🪐", "🌈", "🔥", "🌊", "🌪️", "☄️", "🌟", "🍀", "🌻", "🦊", "🦉",
    "🦄", "🐙", "🐝", "🦖", "🐢", "🦜", "🦩", "🐬", "🎯", "🎲", "🎨", "🎸", "🎹", "🎮", "🎬", "📚",
    "📝", "🗞️", "🧶", "🪡", "🪢", "💡", "🔮", "🧿", "🗿", "🪁", "🏁", "📍", "🕹️", "⌛", "⏱️", "🌋",
    "🗻", "🏝️", "🏔️", "🛶", "🚲", "🛼",
];

/// The emoji a launching agent leads with, picked by FNV-1a over the id's UTF-16 units.
pub fn session_emoji(id: &str) -> &'static str {
    if id.is_empty() {
        return SESSION_EMOJIS[0];
    }
    let hash = id.encode_utf16().fold(0x811c_9dc5_u32, |hash, unit| {
        (hash ^ u32::from(unit)).wrapping_mul(0x0100_0193)
    });
    SESSION_EMOJIS[hash as usize % SESSION_EMOJIS.len()]
}
