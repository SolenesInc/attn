use std::collections::{HashMap, HashSet};

use attn_protocol::Session;

use super::{Look, Pick, Row};
use crate::sidebar::model::{self, AgentRow, Line, TileRow};
use crate::sidebar::queue::{self, Bands};
use crate::{Action, State};

/// What the agents face lists for `query`, in its standing order: the chief and the crew,
/// then the turns, the other agents with the automation runs, the snoozed agents, and the
/// tiles. A query only drops rows; a divider parts the crew from the rest when both show.
pub(crate) fn rows(state: &State, query: &str) -> Vec<Row> {
    let facts = &state.facts;
    let now_ms = state.window.now_ms;
    let terms: Vec<String> = query
        .to_lowercase()
        .split_whitespace()
        .map(str::to_owned)
        .collect();
    let bands = queue::bands(facts, now_ms);
    let head = terms
        .is_empty()
        .then(|| bands.head().map(|s| s.id.clone()))
        .flatten();
    let lines = model::desktop_lines(facts, false, &[]);
    let mut list = List {
        state,
        terms: &terms,
        head: head.as_deref(),
        seen: HashSet::new(),
        rows: Vec::new(),
    };

    for session in anchored(&bands) {
        list.agent(session);
    }
    let anchored_rows = list.rows.len();

    let settled: HashSet<&str> = bands
        .settled
        .iter()
        .map(|r| r.session.id.as_str())
        .collect();
    let by_id: HashMap<&str, &Session> =
        facts.sessions.iter().map(|s| (s.id.as_str(), s)).collect();
    let on_desktops: Vec<&Session> = lines
        .iter()
        .filter_map(|line| match line {
            Line::Agent { row, .. } => by_id.get(row.id.as_str()).copied(),
            _ => None,
        })
        .filter(|s| settled.contains(s.id.as_str()) || s.automation.is_some())
        .collect();
    let hidden = bands
        .settled
        .iter()
        .filter(|r| r.desktop.is_none())
        .map(|r| r.session);
    for session in bands.turns.iter().map(|r| r.session) {
        list.agent(session);
    }
    for session in on_desktops.into_iter().chain(hidden) {
        list.agent(session);
    }
    for session in bands.snoozed.iter().map(|r| r.session) {
        list.agent(session);
    }
    for line in &lines {
        if let Line::Tile(tile) = line {
            list.tile(tile);
        }
    }

    let mut rows = list.rows;
    if anchored_rows > 0 && rows.len() > anchored_rows {
        rows.insert(
            anchored_rows,
            Row {
                key: "divider".into(),
                label: String::new(),
                look: Look::Divider,
                pick: None,
            },
        );
    }
    rows
}

/// The chief's session, then each crew member's, by member.
fn anchored<'a>(bands: &Bands<'a>) -> Vec<&'a Session> {
    let chief = bands.chief.as_ref().map(|c| c.session);
    let chief_member = chief.and_then(|c| c.crew_member.as_deref());
    chief
        .into_iter()
        .chain(
            bands
                .crew
                .iter()
                .map(|r| r.session)
                .filter(|s| chief_member.is_none() || s.crew_member.as_deref() != chief_member),
        )
        .collect()
}

struct List<'a> {
    state: &'a State,
    terms: &'a [String],
    head: Option<&'a str>,
    seen: HashSet<String>,
    rows: Vec<Row>,
}

impl List<'_> {
    fn matches(&self, texts: &[&str]) -> bool {
        let haystack = texts.join(" ").to_lowercase();
        self.terms.iter().all(|t| haystack.contains(t.as_str()))
    }

    fn agent(&mut self, session: &Session) {
        if !self.seen.insert(session.id.clone()) {
            return;
        }
        let name = model::display_name(session);
        let automation = session
            .automation
            .as_ref()
            .map(|a| a.definition_name.as_str())
            .unwrap_or_default();
        let crew = session.crew_member.as_deref().unwrap_or_default();
        if !self.matches(&[&name, crew, automation]) {
            return;
        }
        let state = self.state;
        let now_ms = state.window.now_ms;
        let snoozed = queue::is_snoozed(session.turn_snoozed_until.as_deref(), now_ms);
        let owed = session.turn_owed == Some(true) && !snoozed;
        let desktop = state
            .facts
            .desktops
            .iter()
            .find(|d| d.panes.iter().any(|p| p.session_id == session.id));
        let tag = (self.head == Some(session.id.as_str()))
            .then(|| super::label_of(state, &Action::SessionJumpToWaiting))
            .flatten();
        self.rows.push(Row {
            key: format!("agent:{}", session.id),
            label: name,
            look: Look::Agent {
                agent: AgentRow::of(session, false),
                crew: !crew.is_empty() && session.chief_of_staff != Some(true),
                status: status(session, snoozed),
                slot: desktop.map_or_else(|| "—".to_owned(), |d| slot(state, d.shortcut_slot)),
                age: if owed {
                    queue::turn_age(session.turn_opened_at.as_deref(), now_ms)
                } else {
                    String::new()
                },
                tag,
            },
            pick: Some(Pick::Open(Action::OpenAgent {
                session: session.id.clone(),
            })),
        });
    }

    fn tile(&mut self, tile: &TileRow) {
        if !self.matches(&[&tile.title, tile.word()]) {
            return;
        }
        let state = self.state;
        let slot_of = state
            .facts
            .desktops
            .iter()
            .find(|d| d.id == tile.desktop)
            .map_or_else(|| "—".to_owned(), |d| slot(state, d.shortcut_slot));
        self.rows.push(Row {
            key: format!("tile:{}:{}", tile.desktop, tile.id),
            label: tile.title.clone(),
            look: Look::Tile {
                word: tile.word(),
                slot: slot_of,
            },
            pick: Some(Pick::Open(Action::OpenTile {
                desktop: tile.desktop.clone(),
                tile: tile.id.clone(),
            })),
        });
    }
}

/// The word an agent row's pill shows, as the Tauri app's `agentStatus`.
fn status(session: &Session, snoozed: bool) -> &'static str {
    if session.chief_of_staff == Some(true) {
        "chief"
    } else if snoozed {
        "snoozed"
    } else if session.turn_owed == Some(true) {
        if session.state == "pending_approval" {
            "approval"
        } else {
            "waiting"
        }
    } else if matches!(session.state.as_str(), "working" | "launching") {
        "working"
    } else {
        "idle"
    }
}

/// The keys of a desktop's slot, or `·` for a desktop without one.
fn slot(state: &State, shortcut_slot: Option<i64>) -> String {
    shortcut_slot
        .and_then(Action::desktop_select)
        .and_then(|a| super::label_of(state, &a))
        .unwrap_or_else(|| "·".to_owned())
}
