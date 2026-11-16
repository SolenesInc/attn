use std::collections::{HashMap, HashSet};

use attn_protocol::Session;

use super::{Look, Pick, Row, Status};
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

    anchored(state, &bands, &mut list);
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

/// The chief's session, or the Chief between sessions, then each crew member by key: its
/// sessions, or the member between sessions.
fn anchored(state: &State, bands: &Bands<'_>, list: &mut List<'_>) {
    let chief = crate::chief::view(state);
    if let Some(row) = &bands.chief {
        list.agent(row.session);
    } else if let Some(chief) = &chief {
        list.member(&chief.key, &chief.name, Some(chief.agent.is_none()));
    }
    let roster = crate::crew::members(state);
    let mut members: Vec<String> = roster
        .iter()
        .map(|m| m.id.clone())
        .chain(
            bands
                .crew
                .iter()
                .map(|r| r.session.crew_member.clone().unwrap_or_default()),
        )
        .collect();
    members.sort();
    members.dedup();
    for member in members {
        if chief.as_ref().is_some_and(|c| c.key == member) {
            continue;
        }
        let live: Vec<&Session> = bands
            .crew
            .iter()
            .filter(|r| r.session.crew_member.as_deref().unwrap_or_default() == member)
            .map(|r| r.session)
            .collect();
        if live.is_empty() {
            let name = roster
                .iter()
                .find(|m| m.id == member)
                .map_or_else(|| member.clone(), |m| m.name.clone());
            list.member(&member, &name, None);
        }
        for session in live {
            list.agent(session);
        }
    }
}

/// The automation runs as the queue bar's runs peek lists them, as the Tauri app's `RunsPeek`:
/// each automation's name and counts, then its current runs, the one ⌘⇧J opens next tagged with
/// its keys.
pub(crate) fn run_rows(state: &State) -> Vec<Row> {
    let groups = crate::automations::runs::on_desktops(&state.facts, state.window.now_ms);
    let on_screen = state.window.on_screen(&state.facts);
    let next = crate::automations::runs::next_needing_you(&groups, on_screen.as_deref())
        .map(|(run, ..)| run.id.clone());
    let mut list = List {
        state,
        terms: &[],
        head: None,
        seen: HashSet::new(),
        rows: Vec::new(),
    };
    let plural = |n: usize, noun: &str| format!("{n} {noun}{}", if n == 1 { "" } else { "s" });
    for group in &groups {
        let asking = group.needing_you.len();
        let mut detail = String::new();
        if asking > 0 {
            detail.push_str(&format!("{asking} need you · "));
        }
        detail.push_str(&plural(group.runs.len(), "run"));
        list.rows.push(Row {
            key: format!("group:{}", group.id),
            label: group.name.clone(),
            look: Look::Choice { detail },
            pick: None,
        });
        for run in &group.runs {
            list.agent(run);
            if next.as_deref() == Some(run.id.as_str())
                && let Some(Row {
                    look: Look::Agent { tag, .. },
                    ..
                }) = list.rows.last_mut()
            {
                *tag = super::label_of(state, &Action::SessionNextRun);
            }
        }
    }
    list.rows
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
                agent: Box::new(AgentRow::of(session, &state.facts.outposts, false)),
                crew: !crew.is_empty() && session.chief_of_staff != Some(true),
                status: status(
                    session,
                    snoozed,
                    crate::outpost::offline(session, &state.facts.outposts),
                ),
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

    /// A member between sessions, which a pick starts; `chief` says whether the Chief still
    /// needs its setup.
    fn member(&mut self, member: &str, name: &str, chief: Option<bool>) {
        let words = if chief.is_some() { "chief" } else { "" };
        if !self.matches(&[member, name, words]) {
            return;
        }
        let unconfigured = chief == Some(true);
        let action = match chief {
            Some(true) => Action::OpenChiefSetup,
            Some(false) => Action::StartChief,
            None => Action::StartCrewMember {
                member: member.to_owned(),
            },
        };
        self.rows.push(Row {
            key: format!("member:{member}"),
            label: name.to_owned(),
            look: Look::Member {
                chief: chief.is_some(),
                unconfigured,
            },
            pick: Some(Pick::Open(action)),
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

fn status(session: &Session, snoozed: bool, offline: bool) -> Status {
    if offline {
        Status::Unreachable
    } else if session.chief_of_staff == Some(true) {
        Status::Chief
    } else if snoozed {
        Status::Snoozed
    } else if session.turn_owed == Some(true) {
        if session.state == "pending_approval" {
            Status::Approval
        } else {
            Status::Waiting
        }
    } else if matches!(session.state.as_str(), "working" | "launching") {
        Status::Working
    } else {
        Status::Idle
    }
}

/// The keys of a desktop's slot, or `·` for a desktop without one.
fn slot(state: &State, shortcut_slot: Option<i64>) -> String {
    shortcut_slot
        .and_then(Action::desktop_select)
        .and_then(|a| super::label_of(state, &a))
        .unwrap_or_else(|| "·".to_owned())
}
